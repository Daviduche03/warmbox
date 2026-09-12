package cloudstore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	rfs "github.com/rclone/rclone/fs"
	_ "github.com/rclone/rclone/backend/local"
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
	if err := s.ApplyManifest(ctx, m, g, filepath.Join(root, "cache")); err != nil {
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
