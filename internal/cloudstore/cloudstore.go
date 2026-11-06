// Package cloudstore is warmbox's storage engine: a content-addressed chunk
// store on any rclone remote (S3/R2, or a local dir), plus small JSON documents
// (manifests, metadata).
//
// Chunks are addressed by the SHA-256 of their contents, so identical data is
// stored once and shared across volumes — which makes clones O(1) and lets a
// local chunk cache serve many volumes without re-downloading. internal/volume
// (disks) is the caller.
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
	"sort"
	"strconv"
	"strings"
	"time"

	rfs "github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/object"
	"golang.org/x/sys/unix"
)

// DefaultChunkSize is the transfer/allocation granularity.
const DefaultChunkSize = 16 << 20

// DefaultMaxCacheBytes bounds the local chunk cache for real (non-local)
// remotes: 4 GiB of 16 MiB chunks. A cache bigger than this is a second copy
// of the data, not a cache.
const DefaultMaxCacheBytes = 4 << 30

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

	// localChunks, when non-empty, is a directory on this host's disk holding
	// exactly the objects under chunkRoot: the "remote" is a local dir, so
	// GetChunk reads straight through to it instead of copying every byte
	// into cacheDir first. Without this a local backend stores every volume
	// byte three times (live image + cacheDir copy + remote copy).
	localChunks string

	// maxCacheBytes bounds cacheDir for real remotes (LRU by mtime).
	// <= 0 means DefaultMaxCacheBytes.
	maxCacheBytes int64
}

// New builds a Store. chunkSize <= 0 uses DefaultChunkSize.
func New(f rfs.Fs, chunkRoot string, chunkSize int64) *Store {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	return &Store{fs: f, chunkRoot: chunkRoot, chunkSize: chunkSize, maxCacheBytes: DefaultMaxCacheBytes}
}

// SetLocalChunksDir tells the store its remote chunk root lives at dir on
// this host's disk, so reads go straight to it and the cache stays empty.
// dir must hold exactly the objects under chunkRoot.
func (s *Store) SetLocalChunksDir(dir string) { s.localChunks = dir }

// SetMaxCacheBytes bounds the local chunk cache for real remotes. n <= 0
// restores the default.
func (s *Store) SetMaxCacheBytes(n int64) {
	if n <= 0 {
		n = DefaultMaxCacheBytes
	}
	s.maxCacheBytes = n
}

// ChunkSize returns the configured chunk size.
func (s *Store) ChunkSize() int64 { return s.chunkSize }

// ChunkRef names a stored chunk and its size.
type ChunkRef struct {
	Hash    string
	Size    int64
	ModTime time.Time
}

// DeleteChunk removes one stored chunk.
func (s *Store) DeleteChunk(ctx context.Context, hash string) error {
	return s.Delete(ctx, s.chunkPath(hash))
}

// ListChunks lists the chunks in the remote store. Used by garbage
// collection to find hashes no manifest references anymore.
func (s *Store) ListChunks(ctx context.Context) ([]ChunkRef, error) {
	entries, err := s.fs.List(ctx, s.chunkRoot)
	if err != nil {
		return nil, err
	}
	var out []ChunkRef
	for _, e := range entries {
		obj, ok := e.(rfs.Object)
		if !ok {
			continue
		}
		out = append(out, ChunkRef{Hash: pathBase(obj.Remote()), Size: obj.Size(), ModTime: obj.ModTime(ctx)})
	}
	return out, nil
}

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
//
// A cache hit is hash-verified: a partial file left by a crashed or killed
// run is deleted and re-fetched, never served as good data. Cache entries are
// written atomically (temp file + rename), so a crash cannot leave a torn
// file behind in the first place.
//
// When the remote is a local dir (see SetLocalChunksDir) the bytes are read
// straight through to it and cacheDir is untouched: copying them would store
// every byte twice on one disk for no benefit.
func (s *Store) GetChunk(ctx context.Context, hash, cacheDir string) ([]byte, error) {
	if s.localChunks != "" {
		b, err := os.ReadFile(filepath.Join(s.localChunks, hash))
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	if cacheDir != "" {
		if b, err := os.ReadFile(filepath.Join(cacheDir, hash)); err == nil {
			if Hash(b) == hash {
				return b, nil
			}
			// Corrupt entry: drop it so the fetch below replaces it.
			_ = os.Remove(filepath.Join(cacheDir, hash))
		}
	}
	b, err := s.GetBytes(ctx, s.chunkPath(hash))
	if err != nil {
		return nil, err
	}
	if Hash(b) != hash {
		return nil, fmt.Errorf("chunk %s: remote bytes do not match their hash", hash)
	}
	if cacheDir != "" {
		_ = os.MkdirAll(cacheDir, 0o755)
		_ = writeFileAtomic(filepath.Join(cacheDir, hash), b, 0o644)
		_, _ = s.pruneCache(cacheDir)
	}
	return b, nil
}

// writeFileAtomic writes data to path via a temp file in the same directory
// plus rename, so a crash mid-write leaves the old file (or nothing), never
// a partial one.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// PruneCache deletes the oldest cache entries (by mtime) until cacheDir fits
// in maxBytes, and returns the bytes reclaimed. maxBytes <= 0 uses the
// store's bound. It never touches a cache the local read-through made
// obsolete: with no cache writes there is nothing to bound.
func (s *Store) PruneCache(cacheDir string, maxBytes int64) (int64, error) {
	if s.localChunks != "" {
		return 0, nil
	}
	return s.pruneCacheTo(cacheDir, maxBytes)
}

func (s *Store) pruneCache(cacheDir string) (int64, error) {
	return s.pruneCacheTo(cacheDir, 0)
}

func (s *Store) pruneCacheTo(cacheDir string, maxBytes int64) (int64, error) {
	max := maxBytes
	if max <= 0 {
		max = s.maxCacheBytes
	}
	if max <= 0 {
		max = DefaultMaxCacheBytes
	}
	if cacheDir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	type fi struct {
		name string
		size int64
		mod  time.Time
	}
	var total int64
	files := make([]fi, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".tmp-") {
			// A torn write from a crashed run: never a valid entry. Only
			// remove settled ones so an in-flight write is never disturbed.
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
				_ = os.Remove(filepath.Join(cacheDir, e.Name()))
			}
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		total += info.Size()
		files = append(files, fi{e.Name(), info.Size(), info.ModTime()})
	}
	if total <= max {
		return 0, nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	var reclaimed int64
	for _, f := range files {
		if total <= max {
			break
		}
		if err := os.Remove(filepath.Join(cacheDir, f.name)); err == nil {
			total -= f.size
			reclaimed += f.size
		}
	}
	return reclaimed, nil
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

// ApplyStats reports what ApplyManifest did. Wrote is the number of chunks
// actually written; Skipped is the number already identical on disk.
type ApplyStats struct {
	Wrote        int
	Skipped      int
	BytesWritten int64
	BytesSkipped int64
}

// ApplyManifest materializes a manifest into a local image. Chunks already in
// cacheDir are reused (so shared chunks are downloaded once per host).
//
// Every manifest chunk is compared against the bytes already on disk first,
// and identical ranges are left untouched: a create on an up-to-date image
// performs zero chunk writes instead of rewriting the whole disk. Ranges the
// manifest does not list are never touched, so a crash mid-apply can only
// leave an old-or-new chunk behind — never a torn one — and the next apply
// repairs it by the same comparison. Fetched bytes are always hash-verified
// before they reach the image, so a corrupt cache entry (see GetChunk) can
// never be written into a disk.
func (s *Store) ApplyManifest(ctx context.Context, m *Manifest, img *os.File, cacheDir string) (ApplyStats, error) {
	var st ApplyStats
	if m.ChunkSize <= 0 {
		return st, fmt.Errorf("manifest has no chunk size")
	}
	buf := make([]byte, m.ChunkSize)
	for idxStr, hash := range m.Chunks {
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			continue
		}
		off := int64(idx) * m.ChunkSize
		ln := m.ChunkSize
		if rem := m.Size - off; rem < ln {
			ln = rem
		}
		if ln <= 0 {
			continue
		}
		b := buf[:ln]
		n, err := img.ReadAt(b, off)
		if err != nil && err != io.EOF {
			return st, err
		}
		for i := n; i < len(b); i++ {
			b[i] = 0
		}
		if Hash(b) == hash {
			st.Skipped++
			st.BytesSkipped += ln
			continue
		}
		data, err := s.GetChunk(ctx, hash, cacheDir)
		if err != nil {
			return st, fmt.Errorf("chunk %d: %w", idx, err)
		}
		if Hash(data) != hash {
			return st, fmt.Errorf("chunk %d: fetched bytes do not match their hash", idx)
		}
		if _, err := img.WriteAt(data, off); err != nil {
			return st, err
		}
		st.Wrote++
		st.BytesWritten += int64(len(data))
	}
	if cacheDir != "" {
		_, _ = s.pruneCache(cacheDir)
	}
	return st, nil
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
