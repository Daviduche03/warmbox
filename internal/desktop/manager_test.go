package desktop

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// liveBackend is a hypervisor whose process stays up, so a VM in a test is
// still running when the test looks at it. The capture backend exits at once,
// which is right for most tests and wrong for this one: a VM that has already
// died holds no address.
type liveBackend struct{}

func (b *liveBackend) Name() string           { return "test" }
func (b *liveBackend) Capabilities() Caps     { return Caps{GUI: true} }
func (b *liveBackend) Pause(*Instance) error  { return nil }
func (b *liveBackend) Resume(*Instance) error { return nil }
func (b *liveBackend) GuestHostAddr() string  { return "192.168.64.1" }
func (b *liveBackend) Launch(LaunchSpec) (*Instance, error) {
	return &Instance{Cmd: exec.Command("sleep", "120"), Stderr: NewExcerpt(0)}, nil
}

// Two desktops at one guest address is not a near miss: the daemon reaches
// guests by address, so an exec meant for one would run in the other and the
// VNC console would show the wrong screen. The guest asks the host's DHCP for
// its address and the host does not choose it, so the manager is the only
// place that can catch it.
func TestMarkReadyRefusesAnAddressAnotherDesktopHas(t *testing.T) {
	m := testManager(t)
	namedImage(t, m, "img", "")
	m.backend = &liveBackend{}

	first, err := m.start(StartSpec{ImageName: "img"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.start(StartSpec{ImageName: "img"})
	if err != nil {
		t.Fatal(err)
	}

	if err := m.MarkReady(first.ID, "192.168.64.9"); err != nil {
		t.Fatalf("the first desktop could not take its address: %v", err)
	}
	err = m.MarkReady(second.ID, "192.168.64.9")
	if err == nil {
		t.Fatal("a second desktop took an address that was already in use")
	}
	if !errors.Is(err, ErrDuplicateGuestIP) {
		t.Errorf("error = %v, want ErrDuplicateGuestIP", err)
	}
	if !strings.Contains(err.Error(), first.ID) {
		t.Errorf("error = %v, want it to name the desktop that holds the address", err)
	}
	// The refused desktop must not be usable: it is booted, but it never
	// becomes ready, so nothing routes to it.
	if second.wasReady() {
		t.Error("the refused desktop reports ready")
	}
	if got := second.Target(7077); got != "" {
		t.Errorf("the refused desktop has a target %q, want none", got)
	}
}

// A desktop that is gone does not hold its address: the pool boots a new one
// all the time, and the address has to be reusable.
func TestMarkReadyAllowsAnAddressADeadDesktopHad(t *testing.T) {
	m := testManager(t)
	namedImage(t, m, "img", "")
	m.backend = &liveBackend{}

	first, err := m.start(StartSpec{ImageName: "img"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkReady(first.ID, "192.168.64.9"); err != nil {
		t.Fatal(err)
	}
	if err := m.Destroy(first.ID); err != nil {
		t.Fatal(err)
	}

	second, err := m.start(StartSpec{ImageName: "img"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkReady(second.ID, "192.168.64.9"); err != nil {
		t.Errorf("the address of a destroyed desktop was not reusable: %v", err)
	}
}
