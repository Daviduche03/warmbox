// Package cloudstore is the shared storage engine for runmesh and warmbox: a
// content-addressed chunk store on any rclone remote (S3/R2, or a local dir),
// plus small JSON documents (manifests, metadata).
//
// Chunks are addressed by the SHA-256 of their contents, so identical data is
// stored once and shared across volumes — which makes clones O(1) and lets a
// local chunk cache serve many volumes without re-downloading.
//
// This package is meant to be the one engine used by internal/volume (disks),
// internal/sync (runmesh projects) and internal/fuse (cloud mounts). Volume is
// migrated first; sync/cloudfs follow.
package cloudstore

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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	rfs "github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/object"
	"golang.org/x/sys/unix"
)

// DefaultChunkSize is the transfer/allocation granularity.
const DefaultChunkSize = 16 << 20

// Manifest maps chunk index -> content hash (and the geometry).
type Manifest struct {
	ChunkSize int64             `json:"chunk_size"`
	Size      int64             `json:"size"`
	Chunks    map[string]string `json:"chunks"` // decimal index -> sha256 hex
}

// Store is a content-addressed store rooted at an rclone filesystem.
type Store struct {
	fs        rfs.Fs
	chunkRoot string // e.g. "volumes/chunks"
	chunkSize int64
}

// New builds a Store. chunkSize <= 0 uses DefaultChunkSize.
func New(f rfs.Fs, chunkRoot string, chunkSize int64) *Store {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	return &Store{fs: f, chunkRoot: chunkRoot, chunkSize: chunkSize}
}

// ChunkSize returns the configured chunk size.
func (s *Store) ChunkSize() int64 { return s.chunkSize }

// Fs returns the underlying rclone filesystem.
func (s *Store) Fs() rfs.Fs { return s.fs }

// ListDirs lists the immediate subdirectory names under dir.
func (s *Store) ListDirs(ctx context.Context, dir string) ([]string, error) {
	entries, err := s.fs.List(ctx, dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if _, ok := e.(rfs.Directory); ok {
			out = append(out, pathBase(e.Remote()))
		}
	}
	return out, nil
}

func pathBase(p string) string {
	p = strings.TrimSuffix(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// --- raw remote helpers ---

// Exists reports whether a remote object exists.
func (s *Store) Exists(ctx context.Context, remote string) bool {
	_, err := s.fs.NewObject(ctx, remote)
	return err == nil
}

// GetJSON reads a JSON document.
func (s *Store) GetJSON(ctx context.Context, remote string, v any) error {
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

// PutJSON writes a JSON document.
func (s *Store) PutJSON(ctx context.Context, remote string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return s.PutBytes(ctx, remote, data)
}

// PutBytes writes raw bytes to a remote path.
func (s *Store) PutBytes(ctx context.Context, remote string, data []byte) error {
	info := object.NewStaticObjectInfo(remote, time.Now(), int64(len(data)), true, nil, s.fs)
	_, err := s.fs.Put(ctx, bytes.NewReader(data), info)
	return err
}

// GetBytes reads raw bytes from a remote path.
func (s *Store) GetBytes(ctx context.Context, remote string) ([]byte, error) {
	obj, err := s.fs.NewObject(ctx, remote)
	if err != nil {
		return nil, err
	}
	rc, err := obj.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// Delete removes a remote object if present.
func (s *Store) Delete(ctx context.Context, remote string) error {
	obj, err := s.fs.NewObject(ctx, remote)
	if err != nil {
		return nil
	}
	return obj.Remove(ctx)
}

// --- content-addressed chunks ---

func (s *Store) chunkPath(hash string) string { return s.chunkRoot + "/" + hash }

// PutChunk stores data under its content hash and returns the hash. If an
// identical chunk already exists it is a no-op (dedup).
func (s *Store) PutChunk(ctx context.Context, data []byte) (string, error) {
	h := Hash(data)
	if s.Exists(ctx, s.chunkPath(h)) {
		return h, nil
	}
	if err := s.PutBytes(ctx, s.chunkPath(h), data); err != nil {
		return "", err
	}
	return h, nil
}

// GetChunk returns a chunk by content hash, using cacheDir as a local cache.
func (s *Store) GetChunk(ctx context.Context, hash, cacheDir string) ([]byte, error) {
	if cacheDir != "" {
		if b, err := os.ReadFile(filepath.Join(cacheDir, hash)); err == nil {
			return b, nil
		}
	}
	b, err := s.GetBytes(ctx, s.chunkPath(hash))
	if err != nil {
		return nil, err
	}
	if cacheDir != "" {
		_ = os.MkdirAll(cacheDir, 0o755)
		_ = os.WriteFile(filepath.Join(cacheDir, hash), b, 0o644)
	}
	return b, nil
}

// Hash returns the content hash of a chunk.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// --- manifests over a local image ---

// BuildManifest reads the allocated bytes of a local image and uploads them as
// content-addressed chunks, skipping holes and all-zero chunks. On filesystems
// that report holes this reads only the used space, not the whole disk.
func (s *Store) BuildManifest(ctx context.Context, img *os.File, size, chunkSize int64) (*Manifest, error) {
	if chunkSize <= 0 {
		chunkSize = s.chunkSize
	}
	m := &Manifest{ChunkSize: chunkSize, Size: size, Chunks: map[string]string{}}
	alloc, err := AllocatedChunks(img, size, chunkSize)
	if err != nil {
		return nil, err
	}
	n := int((size + chunkSize - 1) / chunkSize)
	buf := make([]byte, chunkSize)
	for idx := 0; idx < n; idx++ {
		if alloc != nil && !alloc[idx] {
			continue
		}
		off := int64(idx) * chunkSize
		ln := chunkSize
		if rem := size - off; rem < ln {
			ln = rem
		}
		b := buf[:ln]
		if _, err := img.ReadAt(b, off); err != nil && err != io.EOF {
			return nil, err
		}
		if isZero(b) {
			continue
		}
		h, err := s.PutChunk(ctx, b)
		if err != nil {
			return nil, fmt.Errorf("chunk %d: %w", idx, err)
		}
		m.Chunks[strconv.Itoa(idx)] = h
	}
	return m, nil
}

// ApplyManifest materializes a manifest into a local image. Chunks already in
// cacheDir are reused (so shared chunks are downloaded once per host).
func (s *Store) ApplyManifest(ctx context.Context, m *Manifest, img *os.File, cacheDir string) error {
	for idxStr, hash := range m.Chunks {
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			continue
		}
		data, err := s.GetChunk(ctx, hash, cacheDir)
		if err != nil {
			return fmt.Errorf("chunk %d: %w", idx, err)
		}
		if _, err := img.WriteAt(data, int64(idx)*m.ChunkSize); err != nil {
			return err
		}
	}
	return nil
}

// AllocatedChunks returns the chunk indices that contain allocated (non-hole)
// bytes. A nil map means the filesystem can't report holes.
func AllocatedChunks(f *os.File, size, chunkSize int64) (map[int]bool, error) {
	if chunkSize <= 0 {
		return nil, nil
	}
	fd := int(f.Fd())
	out := map[int]bool{}
	off := int64(0)
	for off < size {
		data, err := unix.Seek(fd, off, unix.SEEK_DATA)
		if err != nil {
			if errors.Is(err, unix.ENXIO) {
				break // no data from off onward
			}
			if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
				return nil, nil
			}
			return nil, err
		}
		hole, err := unix.Seek(fd, data, unix.SEEK_HOLE)
		if err != nil {
			hole = size
		}
		if hole > size {
			hole = size
		}
		if hole <= data {
			hole = data + chunkSize
		}
		last := (hole - 1) / chunkSize
		for idx := data / chunkSize; idx <= last; idx++ {
			out[int(idx)] = true
		}
		off = hole
	}
	return out, nil
}

func isZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}
