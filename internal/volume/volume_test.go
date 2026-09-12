package volume

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	rfs "github.com/rclone/rclone/fs"
	_ "github.com/rclone/rclone/backend/local"
)

func testRemote(t *testing.T) (rfs.Fs, string) {
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
	return f, root
}

func mustWriteAt(t *testing.T, path string, off int64, b []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteAt(b, off); err != nil {
		t.Fatal(err)
	}
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	f, root := testRemote(t)

	st := NewStore(f, "volumes", filepath.Join(root, "local1"), "", 16)
	if _, err := st.Create(ctx, "v1", 64, ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	img := st.ImagePath("v1")
	mustWriteAt(t, img, 0, []byte("hello"))
	mustWriteAt(t, img, 16, []byte("chunk-one-data"))
	mustWriteAt(t, img, 48, []byte("xyz"))
	if err := st.Commit(ctx, "v1"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	want, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}

	// A fresh local cache must reconstruct the exact image from the remote.
	st2 := NewStore(f, "volumes", filepath.Join(root, "local2"), "", 16)
	img2, err := st2.EnsureLocal(ctx, "v1")
	if err != nil {
		t.Fatalf("ensure local: %v", err)
	}
	got, err := os.ReadFile(img2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("pulled image differs:\n want %q\n got  %q", want, got)
	}
}

func TestClone(t *testing.T) {
	ctx := context.Background()
	f, root := testRemote(t)
	st := NewStore(f, "volumes", filepath.Join(root, "local"), "", 16)

	if _, err := st.Create(ctx, "src", 64, ""); err != nil {
		t.Fatal(err)
	}
	mustWriteAt(t, st.ImagePath("src"), 32, []byte("cloned"))
	if err := st.Commit(ctx, "src"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Clone(ctx, "src", "dst"); err != nil {
		t.Fatalf("clone: %v", err)
	}

	img, err := st.EnsureLocal(ctx, "dst")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(img)
	src, _ := os.ReadFile(st.ImagePath("src"))
	if !bytes.Equal(src, got) {
		t.Fatalf("clone mismatch:\n src %q\n dst %q", src, got)
	}
}

func TestAttachLock(t *testing.T) {
	s := NewStore(nil, "volumes", t.TempDir(), "", 0)
	if err := s.Attach("v", "vm1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Attach("v", "vm2"); err == nil {
		t.Fatal("expected second attach to fail")
	}
	if err := s.Attach("v", "vm1"); err != nil {
		t.Fatalf("re-attach same owner: %v", err)
	}
	s.Release("v", "vm2") // wrong owner: no-op
	if s.AttachedTo("v") != "vm1" {
		t.Fatal("wrong owner released the lock")
	}
	s.Release("v", "vm1")
	if s.AttachedTo("v") != "" {
		t.Fatal("lock not released")
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	f, root := testRemote(t)
	st := NewStore(f, "volumes", filepath.Join(root, "local"), "", 16)
	if _, err := st.Create(ctx, "gone", 32, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, "gone"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.Get(ctx, "gone"); err == nil {
		t.Fatal("volume still present after delete")
	}
	vols, err := st.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(vols) != 0 {
		t.Fatalf("expected no volumes, got %d", len(vols))
	}
}
