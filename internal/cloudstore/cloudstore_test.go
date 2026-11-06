package cloudstore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/rclone/rclone/backend/local"
	rfs "github.com/rclone/rclone/fs"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote")
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := rfs.NewFs(context.Background(), remote)
	if err != nil {
		t.Fatal(err)
	}
	return New(f, "chunks", 0), root
}

func TestManifestRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, root := testStore(t)

	img := filepath.Join(root, "disk.img")
	f, err := os.OpenFile(img, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	const size = 256 << 20
	const chunk = 1 << 20
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	// Write two distinct chunks far apart, leaving holes between them.
	if _, err := f.WriteAt(bytes.Repeat([]byte{0xAA}, chunk), 3*chunk); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(bytes.Repeat([]byte{0xBB}, chunk), 200*chunk); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}

	m, err := s.BuildManifest(ctx, f, size, chunk)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Chunks["3"]; !ok {
		t.Fatalf("chunk 3 missing: %v", keys(m))
	}
	if _, ok := m.Chunks["200"]; !ok {
		t.Fatalf("chunk 200 missing: %v", keys(m))
	}
	if len(m.Chunks) > 8 {
		t.Fatalf("too many chunks (holes not skipped): %v", keys(m))
	}
	f.Close()

	// Materialize into a new image and compare.
	out := filepath.Join(root, "out.img")
	g, err := os.OpenFile(out, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if err := g.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyManifest(ctx, m, g, filepath.Join(root, "cache")); err != nil {
		t.Fatal(err)
	}
	for _, off := range []int64{3 * chunk, 200 * chunk} {
		b := make([]byte, chunk)
		if _, err := g.ReadAt(b, off); err != nil {
			t.Fatal(err)
		}
		if isZero(b) {
			t.Fatalf("chunk at %d is zero after apply", off)
		}
	}
}

func TestAllocatedChunks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "img")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	const size = 256 << 20
	const chunk = 1 << 20
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(bytes.Repeat([]byte{9}, 4096), 100<<20); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	got, err := AllocatedChunks(f, size, chunk)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Skip("filesystem does not support SEEK_DATA/SEEK_HOLE")
	}
	if !got[100] {
		t.Fatalf("chunk 100 should be allocated; got %v", got)
	}
	if got[0] || got[200] || len(got) > 8 {
		t.Fatalf("sparse detection wrong: %v", got)
	}
}

func keys(m *Manifest) []string {
	out := make([]string, 0, len(m.Chunks))
	for k := range m.Chunks {
		out = append(out, k)
	}
	return out
}

// A cache hit must be hash-verified: garbage in the cache file (a torn write
// from a killed run) is re-fetched from the remote, never served.
func TestGetChunkVerifiesCacheHit(t *testing.T) {
	ctx := context.Background()
	s, root := testStore(t)
	cache := filepath.Join(root, "cache")

	data := bytes.Repeat([]byte{0xAB}, 64)
	h, err := s.PutChunk(ctx, data)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	// Poison the cache entry the way a crash mid-write would leave it.
	if err := os.WriteFile(filepath.Join(cache, h), []byte("torn garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetChunk(ctx, h, cache)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("served corrupt cache data: %q", got)
	}
	// The poisoned entry is repaired, so the next hit is clean.
	got2, err := s.GetChunk(ctx, h, cache)
	if err != nil || !bytes.Equal(got2, data) {
		t.Fatalf("cache not repaired: %v %q", err, got2)
	}
}

// Corrupt bytes fetched from the remote must never be cached or returned.
func TestGetChunkRejectsCorruptRemote(t *testing.T) {
	ctx := context.Background()
	s, root := testStore(t)
	cache := filepath.Join(root, "cache")

	if err := s.PutBytes(ctx, s.chunkPath("deadbeef"), []byte("not the hash")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetChunk(ctx, "deadbeef", cache); err == nil {
		t.Fatal("expected an error for hash-mismatched remote bytes")
	}
	if _, err := os.Stat(filepath.Join(cache, "deadbeef")); !os.IsNotExist(err) {
		t.Fatal("corrupt bytes were cached")
	}
}

// The second apply of the same manifest must write nothing.
func TestApplyManifestSkipsIdentical(t *testing.T) {
	ctx := context.Background()
	s, root := testStore(t)

	img := filepath.Join(root, "disk.img")
	f, err := os.OpenFile(img, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	const size = 4 << 20
	const chunk = 1 << 20
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(bytes.Repeat([]byte{7}, chunk), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(bytes.Repeat([]byte{9}, chunk), 2*chunk); err != nil {
		t.Fatal(err)
	}
	m, err := s.BuildManifest(ctx, f, size, chunk)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Materialize into a fresh image: first apply writes, second skips.
	out := filepath.Join(root, "out.img")
	g, err := os.OpenFile(out, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if err := g.Truncate(size); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, "cache")
	st, err := s.ApplyManifest(ctx, m, g, cache)
	if err != nil {
		t.Fatal(err)
	}
	if st.Wrote != 2 {
		t.Fatalf("first apply wrote %d chunks, want 2", st.Wrote)
	}
	st, err = s.ApplyManifest(ctx, m, g, cache)
	if err != nil {
		t.Fatal(err)
	}
	if st.Wrote != 0 {
		t.Fatalf("second apply wrote %d chunks, want 0", st.Wrote)
	}
	if st.Skipped != 2 {
		t.Fatalf("second apply skipped %d chunks, want 2", st.Skipped)
	}
}

// Kill -9 mid-apply leaves old-or-new chunks, never torn ones: a poisoned
// cache entry plus a corrupted disk range must heal on the next apply.
func TestApplyManifestHealsCorruption(t *testing.T) {
	ctx := context.Background()
	s, root := testStore(t)

	img := filepath.Join(root, "disk.img")
	f, err := os.OpenFile(img, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	const size = 2 << 20
	const chunk = 1 << 20
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte{0x5A}, chunk)
	if _, err := f.WriteAt(want, 0); err != nil {
		t.Fatal(err)
	}
	m, err := s.BuildManifest(ctx, f, size, chunk)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, "cache")
	if _, err := s.ApplyManifest(ctx, m, f, cache); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}

	// Simulate the crash: garbage on disk and a torn cache file.
	if _, err := f.WriteAt(bytes.Repeat([]byte{0xFF}, chunk), 0); err != nil {
		t.Fatal(err)
	}
	var hash string
	for _, h := range m.Chunks {
		hash = h
	}
	if err := os.WriteFile(filepath.Join(cache, hash), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyManifest(ctx, m, f, cache); err != nil {
		t.Fatalf("apply after corruption: %v", err)
	}
	got := make([]byte, chunk)
	if _, err := f.ReadAt(got, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("disk does not match the manifest after healing")
	}
}

// The cache is bounded: entries over the cap are reclaimed oldest-first.
func TestPruneCacheBounds(t *testing.T) {
	s, root := testStore(t)
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSized := func(name string, n int, old bool) {
		p := filepath.Join(cache, name)
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
		if old {
			past := time.Now().Add(-2 * time.Hour)
			if err := os.Chtimes(p, past, past); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeSized("old-a", 100, true)
	writeSized("old-b", 100, true)
	writeSized("new", 100, false)

	reclaimed, err := s.PruneCache(cache, 150)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed < 100 {
		t.Fatalf("reclaimed %d bytes, want >= 100", reclaimed)
	}
	if _, err := os.Stat(filepath.Join(cache, "new")); err != nil {
		t.Fatal("newest entry was pruned before older ones")
	}
	var total int64
	entries, _ := os.ReadDir(cache)
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	if total > 150 {
		t.Fatalf("cache still %d bytes over a 150-byte cap", total)
	}
}

// With a local chunk dir, reads go straight through and no cache is created.
func TestLocalChunksReadThrough(t *testing.T) {
	ctx := context.Background()
	s, root := testStore(t)
	s.SetLocalChunksDir(filepath.Join(root, "remote", "chunks"))

	data := bytes.Repeat([]byte{0xCD}, 32)
	h, err := s.PutChunk(ctx, data)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	cache := filepath.Join(root, "cache-that-should-never-exist")
	got, err := s.GetChunk(ctx, h, cache)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("read-through mismatch: %q", got)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("read-through created a cache copy")
	}
}
