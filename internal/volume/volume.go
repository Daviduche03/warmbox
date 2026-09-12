// Package volume implements portable, cloud-backed disks for warmbox VMs.
//
// A volume is a raw ext4 image stored in a remote (S3/R2 via rclone) as
// fixed-size chunks, with a local sparse working copy. Only chunks that change
// are transferred, so commits stay small. See docs/volumes.md.
package volume

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	rfs "github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/object"
	"github.com/rclone/rclone/fs/operations"
)

// DefaultChunkSize is the transfer granularity when syncing a volume image.
const DefaultChunkSize = 16 << 20

// Meta describes a volume. It is stored at <prefix>/<name>/meta.json.
type Meta struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	ChunkSize int64     `json:"chunk_size"`
	Created   time.Time `json:"created"`
	From      string    `json:"from,omitempty"`
}

// Manifest records which chunks of a volume currently exist remotely.
type Manifest struct {
	ChunkSize int64             `json:"chunk_size"`
	Size      int64             `json:"size"`
	Chunks    map[string]string `json:"chunks"` // decimal chunk index -> sha256 hex
}

// Store manages volumes on a remote filesystem (S3/R2 or a local path) plus a
// local cache of working images.
type Store struct {
	fs        rfs.Fs
	prefix    string
	localDir  string
	baseImage string
	chunkSize int64

	mu       sync.Mutex
	attached map[string]string // volume name -> owner (VM id)
}

// NewStore builds a Store. prefix defaults to "volumes"; chunkSize <= 0 uses
// DefaultChunkSize.
func NewStore(f rfs.Fs, prefix, localDir, baseImage string, chunkSize int64) *Store {
	if prefix == "" {
		prefix = "volumes"
	}
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	return &Store{
		fs:        f,
		prefix:    strings.Trim(prefix, "/"),
		localDir:  localDir,
		baseImage: baseImage,
		chunkSize: chunkSize,
		attached:  map[string]string{},
	}
}

// --- paths ---

func (s *Store) metaRemote(name string) string     { return s.prefix + "/" + name + "/meta.json" }
func (s *Store) manifestRemote(name string) string { return s.prefix + "/" + name + "/manifest.json" }
func (s *Store) chunkRemote(name string, idx int) string {
	return fmt.Sprintf("%s/%s/chunks/%08d", s.prefix, name, idx)
}
func (s *Store) dirPath(name string) string  { return filepath.Join(s.localDir, name) }
func (s *Store) ImagePath(name string) string { return filepath.Join(s.dirPath(name), "disk.img") }

// ValidName reports whether name is a safe volume name.
func ValidName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// --- remote helpers ---

func (s *Store) readJSON(ctx context.Context, remote string, v any) error {
	obj, err := s.fs.NewObject(ctx, remote)
	if err != nil {
		return err
	}
	rc, err := obj.Open(ctx)
	if err != nil {
		return err
	}
	defer rc.Close()
	return json.NewDecoder(rc).Decode(v)
}

func (s *Store) writeJSON(ctx context.Context, remote string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return s.put(ctx, remote, data)
}

func (s *Store) put(ctx context.Context, remote string, data []byte) error {
	info := object.NewStaticObjectInfo(remote, time.Now(), int64(len(data)), true, nil, s.fs)
	_, err := s.fs.Put(ctx, bytes.NewReader(data), info)
	return err
}

// --- API ---

// Create makes a new volume. If from is non-empty it clones that volume;
// otherwise it clones the configured base image, or creates a zero image of
// the requested size. The new volume is committed to the remote immediately.
func (s *Store) Create(ctx context.Context, name string, size int64, from string) (*Meta, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("invalid volume name %q", name)
	}
	if _, err := s.Get(ctx, name); err == nil {
		return nil, fmt.Errorf("volume %q already exists", name)
	} else if !errors.Is(err, rfs.ErrorObjectNotFound) {
		// A missing meta.json is expected; anything else is a real error.
		return nil, err
	}

	meta := &Meta{Name: name, Created: time.Now().UTC(), From: from}
	img := s.ImagePath(name)
	if err := os.MkdirAll(s.dirPath(name), 0o755); err != nil {
		return nil, err
	}

	switch {
	case from != "":
		src, err := s.EnsureLocal(ctx, from)
		if err != nil {
			return nil, fmt.Errorf("cloning source volume: %w", err)
		}
		srcMeta, err := s.Get(ctx, from)
		if err != nil {
			return nil, err
		}
		if err := cloneFile(src, img); err != nil {
			return nil, err
		}
		meta.Size = srcMeta.Size
		meta.ChunkSize = srcMeta.ChunkSize
	case s.baseImage != "":
		if _, err := os.Stat(s.baseImage); err == nil {
			if err := cloneFile(s.baseImage, img); err != nil {
				return nil, err
			}
			fi, err := os.Stat(img)
			if err != nil {
				return nil, err
			}
			meta.Size = fi.Size()
			meta.ChunkSize = s.chunkSize
		}
	}
	if meta.Size == 0 {
		if size <= 0 {
			return nil, fmt.Errorf("volume %q: no base image and no size given", name)
		}
		meta.Size = size
		meta.ChunkSize = s.chunkSize
		zf, err := os.OpenFile(img, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return nil, err
		}
		if err := zf.Truncate(size); err != nil {
			zf.Close()
			return nil, err
		}
		zf.Close()
	}
	if meta.ChunkSize == 0 {
		meta.ChunkSize = s.chunkSize
	}

	if err := s.fs.Mkdir(ctx, s.prefix+"/"+name); err != nil {
		return nil, err
	}
	if err := s.writeJSON(ctx, s.metaRemote(name), meta); err != nil {
		return nil, err
	}
	if err := s.Commit(ctx, name); err != nil {
		return nil, err
	}
	return meta, nil
}

// List returns all volumes.
func (s *Store) List(ctx context.Context) ([]*Meta, error) {
	entries, err := s.fs.List(ctx, s.prefix)
	if err != nil {
		return nil, err
	}
	var out []*Meta
	for _, e := range entries {
		if _, ok := e.(rfs.Directory); !ok {
			continue
		}
		name := pathBase(e.Remote())
		m, err := s.Get(ctx, name)
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get reads a volume's metadata.
func (s *Store) Get(ctx context.Context, name string) (*Meta, error) {
	var m Meta
	if err := s.readJSON(ctx, s.metaRemote(name), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Delete removes a volume and all of its remote chunks.
func (s *Store) Delete(ctx context.Context, name string) error {
	if err := operations.Purge(ctx, s.fs, s.prefix+"/"+name); err != nil {
		return err
	}
	_ = os.RemoveAll(s.dirPath(name))
	return nil
}

// Clone copies src to dst (server-side chunk copy + a local image copy).
func (s *Store) Clone(ctx context.Context, src, dst string) (*Meta, error) {
	if !ValidName(dst) {
		return nil, fmt.Errorf("invalid volume name %q", dst)
	}
	if _, err := s.Get(ctx, dst); err == nil {
		return nil, fmt.Errorf("volume %q already exists", dst)
	}
	return s.Create(ctx, dst, 0, src)
}

// EnsureLocal downloads the volume's image into the local cache and returns
// its path. Missing chunks are left as holes (zeros) in the sparse image.
func (s *Store) EnsureLocal(ctx context.Context, name string) (string, error) {
	meta, err := s.Get(ctx, name)
	if err != nil {
		return "", err
	}
	img := s.ImagePath(name)
	if err := os.MkdirAll(s.dirPath(name), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(img, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := f.Truncate(meta.Size); err != nil {
		return "", err
	}

	man := &Manifest{Chunks: map[string]string{}}
	if err := s.readJSON(ctx, s.manifestRemote(name), man); err != nil {
		if errors.Is(err, rfs.ErrorObjectNotFound) {
			return img, nil // no chunks yet
		}
		return "", err
	}
	for idxStr := range man.Chunks {
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			continue
		}
		if err := s.downloadChunk(ctx, f, name, idx, meta); err != nil {
			return "", err
		}
	}
	return img, nil
}

func (s *Store) downloadChunk(ctx context.Context, img *os.File, name string, idx int, meta *Meta) error {
	obj, err := s.fs.NewObject(ctx, s.chunkRemote(name, idx))
	if err != nil {
		return fmt.Errorf("chunk %d: %w", idx, err)
	}
	rc, err := obj.Open(ctx)
	if err != nil {
		return err
	}
	defer rc.Close()
	buf := make([]byte, obj.Size())
	if _, err := io.ReadFull(rc, buf); err != nil {
		return err
	}
	off := int64(idx) * meta.ChunkSize
	if _, err := img.WriteAt(buf, off); err != nil {
		return err
	}
	return nil
}

// Commit uploads the chunks of the local image that changed since the last
// manifest, and refreshes the remote manifest.
func (s *Store) Commit(ctx context.Context, name string) error {
	meta, err := s.Get(ctx, name)
	if err != nil {
		return err
	}
	img, err := os.Open(s.ImagePath(name))
	if err != nil {
		return err
	}
	defer img.Close()

	old := &Manifest{Chunks: map[string]string{}}
	if err := s.readJSON(ctx, s.manifestRemote(name), old); err == nil {
		if old.Chunks == nil {
			old.Chunks = map[string]string{}
		}
	}

	chunkSize := meta.ChunkSize
	if chunkSize <= 0 {
		chunkSize = s.chunkSize
	}
	nChunks := int((meta.Size + chunkSize - 1) / chunkSize)
	next := &Manifest{ChunkSize: chunkSize, Size: meta.Size, Chunks: map[string]string{}}
	buf := make([]byte, chunkSize)

	for idx := 0; idx < nChunks; idx++ {
		off := int64(idx) * chunkSize
		n := chunkSize
		if rem := meta.Size - off; rem < n {
			n = rem
		}
		b := buf[:n]
		if _, err := img.ReadAt(b, off); err != nil && err != io.EOF {
			return err
		}
		key := strconv.Itoa(idx)
		if isZero(b) {
			continue // hole => not stored
		}
		sum := sha256.Sum256(b)
		h := hex.EncodeToString(sum[:])
		next.Chunks[key] = h
		if old.Chunks[key] == h {
			continue // unchanged, already remote
		}
		if err := s.put(ctx, s.chunkRemote(name, idx), b); err != nil {
			return fmt.Errorf("uploading chunk %d: %w", idx, err)
		}
	}

	// Drop remote chunks that no longer exist.
	for key := range old.Chunks {
		if _, ok := next.Chunks[key]; ok {
			continue
		}
		if obj, err := s.fs.NewObject(ctx, s.chunkRemote(name, atoiSafe(key))); err == nil {
			_ = obj.Remove(ctx)
		}
	}

	return s.writeJSON(ctx, s.manifestRemote(name), next)
}

// --- single-attach lock (in-process) ---

// Attach claims a volume for owner. It errors if another owner holds it.
func (s *Store) Attach(name, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.attached[name]; ok && cur != owner {
		return fmt.Errorf("volume %q is already attached to %s", name, cur)
	}
	s.attached[name] = owner
	return nil
}

// Release frees the attach lock held by owner.
func (s *Store) Release(name, owner string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attached[name] == owner {
		delete(s.attached, name)
	}
}

// AttachedTo reports the current owner of a volume, if any.
func (s *Store) AttachedTo(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attached[name]
}

// --- helpers ---

func isZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func pathBase(p string) string {
	p = strings.TrimSuffix(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// cloneFile makes a copy-on-write clone when the filesystem supports it
// (APFS "cp -c"), otherwise a full copy.
func cloneFile(src, dst string) error {
	if err := exec.Command("cp", "-c", src, dst).Run(); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
