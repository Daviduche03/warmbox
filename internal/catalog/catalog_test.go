package catalog

import "testing"

func mustOpen(t *testing.T) *DB {
	t.Helper()
	d, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestVolumes(t *testing.T) {
	d := mustOpen(t)
	defer d.Close()

	if _, err := d.GetVolume("nope"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := d.UpsertVolume(&Volume{Name: "dev", Size: 8 << 30, ChunkSize: 16 << 20, Remote: "volumes/dev"}); err != nil {
		t.Fatal(err)
	}
	v, err := d.GetVolume("dev")
	if err != nil {
		t.Fatal(err)
	}
	if v.Size != 8<<30 || v.Remote != "volumes/dev" {
		t.Fatalf("unexpected: %+v", v)
	}
	if err := d.UpsertVolume(&Volume{Name: "dev", Size: 16 << 30, ChunkSize: 16 << 20}); err != nil {
		t.Fatal(err)
	}
	v, _ = d.GetVolume("dev")
	if v.Size != 16<<30 {
		t.Fatalf("update failed: %d", v.Size)
	}
	vs, err := d.ListVolumes()
	if err != nil || len(vs) != 1 {
		t.Fatalf("list: %v n=%d", err, len(vs))
	}
	if err := d.DeleteVolume("dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetVolume("dev"); err != ErrNotFound {
		t.Fatal("delete failed")
	}
}

func TestDesktops(t *testing.T) {
	d := mustOpen(t)
	defer d.Close()

	if err := d.UpsertDesktop(&Desktop{ID: "abc", Volume: "dev", State: "booting"}); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertDesktop(&Desktop{ID: "abc", Volume: "dev", State: "ready", GuestIP: "1.2.3.4", PID: 42}); err != nil {
		t.Fatal(err)
	}
	x, err := d.GetDesktop("abc")
	if err != nil {
		t.Fatal(err)
	}
	if x.State != "ready" || x.GuestIP != "1.2.3.4" || x.PID != 42 {
		t.Fatalf("unexpected: %+v", x)
	}
	if err := d.UpsertDesktop(&Desktop{ID: "def", State: "ready"}); err != nil {
		t.Fatal(err)
	}
	xs, err := d.ListDesktops()
	if err != nil || len(xs) != 2 {
		t.Fatalf("list: %v n=%d", err, len(xs))
	}
	if err := d.DeleteDesktop("abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetDesktop("abc"); err != ErrNotFound {
		t.Fatal("delete failed")
	}
}

func TestLeases(t *testing.T) {
	d := mustOpen(t)
	defer d.Close()

	if err := d.AcquireLease("dev", "vm1"); err != nil {
		t.Fatal(err)
	}
	if err := d.AcquireLease("dev", "vm1"); err != nil {
		t.Fatalf("re-acquire same owner: %v", err)
	}
	if err := d.AcquireLease("dev", "vm2"); err == nil {
		t.Fatal("expected conflict for second owner")
	}
	if o, _ := d.LeaseOwner("dev"); o != "vm1" {
		t.Fatalf("owner=%q", o)
	}
	if err := d.ReleaseLease("dev", "vm2"); err != nil {
		t.Fatal(err)
	}
	if o, _ := d.LeaseOwner("dev"); o != "vm1" {
		t.Fatal("wrong owner released the lease")
	}
	if err := d.ReleaseLease("dev", "vm1"); err != nil {
		t.Fatal(err)
	}
	if o, _ := d.LeaseOwner("dev"); o != "" {
		t.Fatal("lease not released")
	}
}
