package api

import (
	"io"
	"path/filepath"
	"testing"

	"warmbox/internal/catalog"
)

// A daemon crash leaves volume leases behind (SQLite outlives the process);
// the next boot must reclaim them or every create with those volumes 409s
// forever. The reclaim is unconditional by design: it runs when no VM can
// be alive, so anything held is stale by construction.
func TestReapStaleLeases(t *testing.T) {
	db, err := catalog.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	defer db.Close()
	if err := db.AcquireLease("mywork", "dead-vm-id"); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := db.AcquireLease("other", "another-dead-vm"); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	s := &Server{catalog: db, log: io.Discard}
	if n := s.ReapStaleLeases(); n != 2 {
		t.Fatalf("reaped %d, want 2", n)
	}
	if owner, _ := db.LeaseOwner("mywork"); owner != "" {
		t.Fatalf("lease still held by %q", owner)
	}
	// Second boot: nothing held, nothing to do.
	if n := s.ReapStaleLeases(); n != 0 {
		t.Fatalf("reaped %d, want 0", n)
	}
	// A fresh lease after boot is acquirable again.
	if err := db.AcquireLease("mywork", "new-vm-id"); err != nil {
		t.Fatalf("re-acquire after reap: %v", err)
	}
	// No catalog at all (in-memory mode) is a no-op, not a crash.
	if n := (&Server{}).ReapStaleLeases(); n != 0 {
		t.Fatalf("reaped %d without a catalog, want 0", n)
	}
}
