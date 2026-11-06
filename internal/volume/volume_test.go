package volume

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

func TestGrowFromBase(t *testing.T) {
	ctx := context.Background()
	f, root := testRemote(t)
	base := filepath.Join(root, "base.img")
	if err := os.WriteFile(base, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	st := NewStore(f, "volumes", filepath.Join(root, "local"), base, 1<<20)

	m, err := st.Create(ctx, "big", 4<<20, "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Size != 4<<20 {
		t.Fatalf("meta size=%d, want %d", m.Size, 4<<20)
	}
	if fi, err := os.Stat(st.ImagePath("big")); err != nil || fi.Size() != 4<<20 {
		t.Fatalf("image not grown: %v %v", fi, err)
	}
	// Shrinking is refused.
	if _, err := st.Create(ctx, "small", 512<<10, ""); err == nil {
		t.Fatal("expected shrink to be refused")
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

func TestSparseManifest(t *testing.T) {
	ctx := context.Background()
	f, root := testRemote(t)
	st := NewStore(f, "volumes", filepath.Join(root, "local"), "", 4096)
	if _, err := st.Create(ctx, "sparse", 1<<20, ""); err != nil {
		t.Fatal(err)
	}
	m, err := st.Manifest(ctx, "sparse")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Chunks) != 0 {
		t.Fatalf("fresh zero volume should have 0 chunks, got %d", len(m.Chunks))
	}
	// Write a single 4 KiB chunk and commit: only that chunk should appear.
	mustWriteAt(t, st.ImagePath("sparse"), 2*4096, bytes.Repeat([]byte{7}, 4096))
	if err := st.Commit(ctx, "sparse"); err != nil {
		t.Fatal(err)
	}
	m, _ = st.Manifest(ctx, "sparse")
	if len(m.Chunks) != 1 {
		t.Fatalf("expected exactly 1 chunk, got %d", len(m.Chunks))
	}
}

// The second EnsureLocal on an unchanged image must write zero chunks: the
// first call records a sidecar of what disk.img holds, the second takes the
// fast path without even comparing.
func TestEnsureLocalSkipsIdentical(t *testing.T) {
	ctx := context.Background()
	f, root := testRemote(t)
	st := NewStore(f, "volumes", filepath.Join(root, "local"), "", 16)

	if _, err := st.Create(ctx, "v", 64, ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	mustWriteAt(t, st.ImagePath("v"), 0, []byte("hello-world-1234"))
	mustWriteAt(t, st.ImagePath("v"), 32, []byte("second-chunk-data"))
	if err := st.Commit(ctx, "v"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Fresh store, empty local dir: materialize from the remote.
	st2 := NewStore(f, "volumes", filepath.Join(root, "local2"), "", 16)
	img, err := st2.EnsureLocal(ctx, "v")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	want, _ := os.ReadFile(st.ImagePath("v"))
	got, _ := os.ReadFile(img)
	if !bytes.Equal(want, got) {
		t.Fatal("materialized image differs")
	}
	if st2.LastApply().Wrote == 0 {
		t.Fatal("first EnsureLocal should have written chunks")
	}

	// Delete the sidecar to force the full per-chunk comparison: still zero
	// writes, every chunk skipped.
	if err := os.Remove(filepath.Join(root, "local2", "v", ".applied.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := st2.EnsureLocal(ctx, "v"); err != nil {
		t.Fatalf("ensure again: %v", err)
	}
	applied := st2.LastApply()
	if applied.Wrote != 0 {
		t.Fatalf("second EnsureLocal wrote %d chunks, want 0", applied.Wrote)
	}
	if applied.Skipped == 0 {
		t.Fatal("second EnsureLocal compared nothing")
	}

	// Untouched since: the sidecar fast path skips even the comparison reads.
	if _, err := st2.EnsureLocal(ctx, "v"); err != nil {
		t.Fatalf("ensure third: %v", err)
	}
	if st := st2.LastApply(); st.Wrote != 0 || st.Skipped != 0 {
		t.Fatalf("fast path did work: %+v", st)
	}
}

// Kill -9 mid-EnsureLocal leaves old-or-new chunks plus possibly a torn cache
// file. The next EnsureLocal must heal both: poison the cache and corrupt the
// disk, then verify the image matches the manifest.
func TestEnsureLocalHealsCrash(t *testing.T) {
	ctx := context.Background()
	f, root := testRemote(t)
	st := NewStore(f, "volumes", filepath.Join(root, "local"), "", 16)

	if _, err := st.Create(ctx, "v", 48, ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	want := []byte("0123456789abcdef")
	mustWriteAt(t, st.ImagePath("v"), 0, want)
	if err := st.Commit(ctx, "v"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	man, err := st.Manifest(ctx, "v")
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	for _, h := range man.Chunks {
		hash = h
	}

	// The crash: garbage straight into the image and a torn cache file.
	mustWriteAt(t, st.ImagePath("v"), 0, bytes.Repeat([]byte{0xFF}, 16))
	cache := filepath.Join(root, "local", "chunks")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, hash), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := st.EnsureLocal(ctx, "v"); err != nil {
		t.Fatalf("ensure after crash: %v", err)
	}
	got := make([]byte, 16)
	rf, err := os.Open(st.ImagePath("v"))
	if err != nil {
		t.Fatal(err)
	}
	defer rf.Close()
	if _, err := rf.ReadAt(got, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("disk not healed: %q", got)
	}
	if st.LastApply().Wrote == 0 {
		t.Fatal("healing apply should have rewritten the corrupt chunk")
	}
}

// GC must list exactly the orphaned hashes on dry-run and reclaim them for
// real, while chunks referenced by any live manifest or snapshot survive.
func TestGCReclaimsOrphans(t *testing.T) {
	ctx := context.Background()
	f, root := testRemote(t)
	st := NewStore(f, "volumes", filepath.Join(root, "local"), "", 16)
	st.SetGCGrace(-1) // no grace window in tests

	if _, err := st.Create(ctx, "v", 64, ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	mustWriteAt(t, st.ImagePath("v"), 0, []byte("chunk-zero-data!"))
	mustWriteAt(t, st.ImagePath("v"), 32, []byte("chunk-two-data!!"))
	if err := st.Commit(ctx, "v"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	man, err := st.Manifest(ctx, "v")
	if err != nil {
		t.Fatal(err)
	}
	chunkTwo := man.Chunks["2"]

	// A snapshot pins chunkTwo even after the volume stops referencing it.
	if _, err := st.Snapshot(ctx, "v", "snap1"); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	mustWriteAt(t, st.ImagePath("v"), 32, bytes.Repeat([]byte{0}, 16))
	if err := st.Commit(ctx, "v"); err != nil {
		t.Fatalf("commit 2: %v", err)
	}

	// An orphan from a failed commit: uploaded, never referenced.
	orphan, err := st.cs.PutChunk(ctx, []byte("orphan-payload!"))
	if err != nil {
		t.Fatalf("orphan put: %v", err)
	}

	rep, err := st.GC(ctx, true)
	if err != nil {
		t.Fatalf("gc dry-run: %v", err)
	}
	if len(rep.Orphans) != 1 || rep.Orphans[0] != orphan {
		t.Fatalf("dry-run orphans = %v, want exactly [%s]", rep.Orphans, orphan)
	}

	// Drop the snapshot: chunkTwo joins the orphan set.
	if err := st.DeleteSnapshot(ctx, "snap1"); err != nil {
		t.Fatal(err)
	}
	rep, err = st.GC(ctx, true)
	if err != nil {
		t.Fatalf("gc dry-run 2: %v", err)
	}
	if len(rep.Orphans) != 2 {
		t.Fatalf("dry-run orphans = %v, want 2", rep.Orphans)
	}

	rep, err = st.GC(ctx, false)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if rep.Deleted != 2 {
		t.Fatalf("deleted %d chunks, want 2", rep.Deleted)
	}
	// Referenced chunks survive; orphans are gone.
	if !st.cs.Exists(ctx, "volumes/chunks/"+man.Chunks["0"]) {
		t.Fatal("referenced chunk was deleted")
	}
	if st.cs.Exists(ctx, "volumes/chunks/"+orphan) {
		t.Fatal("orphan survived gc")
	}
	if st.cs.Exists(ctx, "volumes/chunks/"+chunkTwo) {
		t.Fatal("snapshot-released chunk survived gc")
	}

	// Young chunks are spared by the grace window (in-flight commits).
	st.SetGCGrace(time.Hour)
	fresh, err := st.cs.PutChunk(ctx, []byte("fresh-payload!!"))
	if err != nil {
		t.Fatal(err)
	}
	rep, err = st.GC(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range rep.Orphans {
		if h == fresh {
			t.Fatal("grace window did not protect a fresh chunk")
		}
	}
}

// With a local remote, EnsureLocal must never populate the cache dir: every
// byte would otherwise live twice on one disk.
func TestLocalRemoteSkipsCache(t *testing.T) {
	ctx := context.Background()
	f, root := testRemote(t)
	remote := filepath.Join(root, "remote")

	st := NewStore(f, "volumes", filepath.Join(root, "local1"), "", 16)
	st.SetLocalRemoteDir(remote)
	if _, err := st.Create(ctx, "v", 48, ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	mustWriteAt(t, st.ImagePath("v"), 0, []byte("local-data-12345"))
	if err := st.Commit(ctx, "v"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	st2 := NewStore(f, "volumes", filepath.Join(root, "local2"), "", 16)
	st2.SetLocalRemoteDir(remote)
	img, err := st2.EnsureLocal(ctx, "v")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	want, _ := os.ReadFile(st.ImagePath("v"))
	got, _ := os.ReadFile(img)
	if !bytes.Equal(want, got) {
		t.Fatal("read-through image differs")
	}
	if _, err := os.Stat(filepath.Join(root, "local2", "chunks")); !os.IsNotExist(err) {
		t.Fatal("local backend populated the cache dir")
	}
}
