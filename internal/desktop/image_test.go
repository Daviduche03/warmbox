package desktop

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureBackend records the LaunchSpec the manager produced, so a test can
// assert what would have gone to the hypervisor without booting anything.
// "true" exits immediately, so the VM's supervised process ends as soon as it
// starts and no test leaves a guest behind.
type captureBackend struct {
	spec LaunchSpec
	// stderr stands in for whatever the hypervisor would have complained about.
	stderr string
}

func (b *captureBackend) Name() string           { return "test" }
func (b *captureBackend) Capabilities() Caps     { return Caps{GUI: true} }
func (b *captureBackend) Pause(*Instance) error  { return nil }
func (b *captureBackend) Resume(*Instance) error { return nil }
func (b *captureBackend) GuestHostAddr() string  { return "192.168.64.1" }
func (b *captureBackend) Launch(spec LaunchSpec) (*Instance, error) {
	b.spec = spec
	stderr := NewExcerpt(0)
	if b.stderr != "" {
		_, _ = stderr.Write([]byte(b.stderr + "\n"))
	}
	return &Instance{Cmd: exec.Command("true"), Stderr: stderr}, nil
}

// testManager builds a Manager over an empty temp workdir: no images are
// installed until a test writes one.
func testManager(t *testing.T) *Manager {
	t.Helper()
	wd := t.TempDir()
	return &Manager{cfg: &Config{
		WorkDir:  wd,
		ImageDir: filepath.Join(wd, "images"),
		APIAddr:  "127.0.0.1:7070",
		CPUs:     4,
		MemMiB:   4096,
	}, log: io.Discard, vms: map[string]*VM{}}
}

// namedImage writes the three files imageKind needs to call a directory an
// overlay image, plus meta.json when meta is non-empty.
func namedImage(t *testing.T, m *Manager, name, meta string) {
	t.Helper()
	dir := filepath.Join(m.cfg.ImageDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"vmlinux", "rootfs.squashfs", "initramfs-overlay"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if meta != "" {
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestImageMetasReportsHeadless(t *testing.T) {
	m := testManager(t)
	namedImage(t, m, "headless", `{"headless": true}`)
	namedImage(t, m, "lxqt", `{"mem_mib": 2048}`)
	namedImage(t, m, "plain", "")

	metas := m.ImageMetas()
	if !metas["headless"].Headless {
		t.Error("headless image not reported as headless")
	}
	// An absent key reads as false: every image built before this field
	// existed has no "headless" key, and all of those still have a screen.
	if metas["lxqt"].Headless {
		t.Error("image with a meta.json but no headless key reported as headless")
	}
	if metas["plain"].Headless {
		t.Error("image with no meta.json reported as headless")
	}
	if metas["lxqt"].MemMiB != 2048 {
		t.Errorf("meta.json overrides lost: mem_mib = %d, want 2048", metas["lxqt"].MemMiB)
	}
}

// There is no built-in image any more: everything the daemon can boot is a
// directory under ImageDir, and a directory that is missing part of an image is
// not one.
func TestImagesInListsOnlyCompleteImages(t *testing.T) {
	m := testManager(t)
	namedImage(t, m, "lxqt", "")
	// An interrupted build: the kernel is there, the rootfs is not.
	partial := filepath.Join(m.cfg.ImageDir, "halfbuilt")
	if err := os.MkdirAll(partial, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partial, "vmlinux"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Staging and download leftovers from FetchImage are not images either.
	for _, d := range []string{".lxqt.staging", ".lxqt.download.tar.zst"} {
		_ = os.MkdirAll(filepath.Join(m.cfg.ImageDir, d), 0o755)
	}

	got := m.Images()
	if len(got) != 1 || got[0] != "lxqt" {
		t.Errorf("Images() = %v, want [lxqt]", got)
	}
}

func TestCanonicalImageResolvesAliases(t *testing.T) {
	cases := []struct {
		name, daemonDefault, want string
	}{
		{"", "lxqt", "lxqt"},        // unnamed: the daemon's default
		{"default", "lxqt", "lxqt"}, // the old alias for the same thing
		{"", "", DefaultImage},      // no configured default: xfce
		{"default", "", DefaultImage},
		{"headless", "lxqt", "headless"}, // a real name always wins
		{BuiltinAliasFree, "", BuiltinAliasFree},
	}
	for _, c := range cases {
		if got := CanonicalImage(c.name, c.daemonDefault); got != c.want {
			t.Errorf("CanonicalImage(%q, %q) = %q, want %q", c.name, c.daemonDefault, got, c.want)
		}
	}
	if got := DefaultFor(""); got != DefaultImage {
		t.Errorf("DefaultFor(\"\") = %q, want %q", got, DefaultImage)
	}
	if got := DefaultFor("omarchy"); got != "omarchy" {
		t.Errorf("DefaultFor(\"omarchy\") = %q, want omarchy", got)
	}
}

// BuiltinAliasFree is a name that was never an alias, to keep the table honest.
const BuiltinAliasFree = "headless-lxqt"

// An unnamed start means "whatever this daemon was started with". The warm pool
// boots this way, so getting it wrong means every pooled desktop is the
// built-in image no matter what --image said.
func TestStartBootsTheDaemonDefaultImage(t *testing.T) {
	m := testManager(t)
	namedImage(t, m, "headless", `{"headless": true}`)
	m.cfg.Image = "headless"
	be := &captureBackend{}
	m.backend = be

	vm, err := m.start(StartSpec{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	want := filepath.Join(m.cfg.ImageDir, "headless", "vmlinux")
	if be.spec.Kernel != want {
		t.Errorf("booted kernel = %q, want %q", be.spec.Kernel, want)
	}
	if !vm.Headless {
		t.Error("vm booted from a headless image is not marked headless")
	}
}

// A VM whose process dies during boot must fail the wait immediately and say
// why. Waiting out the full timeout is what made a bad image look like nothing
// happening at all.
func TestWaitReadyFailsAsSoonAsTheVMExits(t *testing.T) {
	m := testManager(t)
	namedImage(t, m, "img", "")
	m.backend = &captureBackend{} // Launch hands back `true`, which exits at once

	vm, err := m.start(StartSpec{ImageName: "img"})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	began := time.Now()
	if _, err := m.WaitReady(ctx, vm.ID); err == nil {
		t.Fatal("WaitReady reported a VM ready whose process had exited")
	} else if !strings.Contains(err.Error(), "exited before it became ready") {
		t.Errorf("error = %v, want it to say the vm exited", err)
	}
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("took %v; it should return as soon as the process exits, not wait for the timeout", took)
	}
}

// The other side of the same coin: a guest that reports ready first is a
// success even if its process is gone by the time we look.
func TestWaitReadySucceedsWhenTheGuestReportedFirst(t *testing.T) {
	m := testManager(t)
	namedImage(t, m, "img", "")
	m.backend = &captureBackend{}

	vm, err := m.start(StartSpec{ImageName: "img"})
	if err != nil {
		t.Fatal(err)
	}
	// The guest calls back before the process ends.
	m.MarkReady(vm.ID, "192.168.64.9")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := m.WaitReady(ctx, vm.ID); err != nil {
		t.Errorf("WaitReady failed for a guest that had already reported ready: %v", err)
	}
	if !vm.wasReady() {
		t.Error("wasReady should be true after a readiness callback")
	}
}

// The guest is asked for a per-VM VNC password. The flag is not a secret; the
// password goes back over the readiness callback, so it never appears on the
// command line where every process on the host could read it.
func TestTheGuestIsAskedForAVNCPassword(t *testing.T) {
	m := testManager(t)
	namedImage(t, m, "img", "")
	be := &captureBackend{}
	m.backend = be

	if _, err := m.start(StartSpec{ImageName: "img"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(be.spec.Cmdline, "warmbox.vncauth=1") {
		t.Errorf("cmdline = %q, want it to ask for a VNC password", be.spec.Cmdline)
	}
	// The password itself must never be on the command line.
	if strings.Contains(strings.ToLower(be.spec.Cmdline), "pass") &&
		!strings.Contains(be.spec.Cmdline, "vncauth=1") {
		t.Errorf("something password-shaped reached the cmdline: %q", be.spec.Cmdline)
	}
}

// A guest reports the password it generated; until then the VM has none, which
// is also the state for an image built before the flag existed.
func TestVNCPasswordIsReportedNotAssumed(t *testing.T) {
	m := testManager(t)
	namedImage(t, m, "img", "")
	m.backend = &captureBackend{}

	vm, err := m.start(StartSpec{ImageName: "img"})
	if err != nil {
		t.Fatal(err)
	}
	if got := vm.VNCPassword(); got != "" {
		t.Errorf("a fresh VM has VNC password %q, want none", got)
	}
	vm.SetVNCPassword("s3cretpass")
	if got := vm.VNCPassword(); got != "s3cretpass" {
		t.Errorf("VNCPassword() = %q, want what the guest reported", got)
	}
}

// A guest that reports ready must never lose its password to a race with
// MarkReady — a console that connects without the password gets nothing.
func TestVNCPasswordSurvivesReadiness(t *testing.T) {
	m := testManager(t)
	namedImage(t, m, "img", "")
	m.backend = &captureBackend{}

	vm, err := m.start(StartSpec{ImageName: "img"})
	if err != nil {
		t.Fatal(err)
	}
	vm.SetVNCPassword("abcdefgh")
	m.MarkReady(vm.ID, "10.0.2.15")
	if got := vm.VNCPassword(); got != "abcdefgh" {
		t.Errorf("VNCPassword() = %q after readiness, want it kept", got)
	}
}

// A headless image is the authority on having no screen: the daemon's own
// defaults must not arm a GPU and input devices for a compositor that is never
// going to start.
func TestHeadlessImageGetsNoScreenDevices(t *testing.T) {
	m := testManager(t)
	namedImage(t, m, "headless", `{"headless": true}`)
	m.cfg.Image = "headless"
	m.cfg.GPU = "1440x900"
	m.cfg.Input = true
	be := &captureBackend{}
	m.backend = be

	if _, err := m.start(StartSpec{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if be.spec.Display != "" {
		t.Errorf("headless image got a display of %q", be.spec.Display)
	}
	if be.spec.Input {
		t.Error("headless image got virtio input devices")
	}
}

// The headless rule must not leak onto images that do have a screen.
func TestImageWithAScreenKeepsItsDevices(t *testing.T) {
	m := testManager(t)
	namedImage(t, m, "headless", `{"headless": true}`)
	namedImage(t, m, "gui", `{"gpu": "800x600"}`)
	m.cfg.GPU = "1440x900"
	m.cfg.Input = true
	be := &captureBackend{}
	m.backend = be

	vm, err := m.start(StartSpec{ImageName: "gui"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if be.spec.Display != "800x600" {
		t.Errorf("display = %q, want the image's own 800x600", be.spec.Display)
	}
	if !be.spec.Input {
		t.Error("input devices dropped for an image that did not ask for it")
	}
	if vm.Headless {
		t.Error("vm from an image with a screen marked headless")
	}
}

// A VM that dies on a host whose hypervisor refused to start must say *why*.
// "exit status 1" alone sent someone looking through a VPS by hand; the
// hypervisor had already printed the answer and nobody was keeping it.
func TestWaitReadyQuotesWhyTheHypervisorRefused(t *testing.T) {
	m := testManager(t)
	namedImage(t, m, "img", "")
	m.backend = &captureBackend{
		stderr: "qemu-system-x86_64: -machine q35,accel=kvm: Could not access KVM kernel module: No such file or directory\nqemu-system-x86_64: failed to initialize kvm: No such file or directory",
	}

	vm, err := m.start(StartSpec{ImageName: "img"})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = m.WaitReady(ctx, vm.ID)
	if err == nil {
		t.Fatal("WaitReady succeeded for a VM whose process had exited")
	}
	if !strings.Contains(err.Error(), "Could not access KVM kernel module") {
		t.Errorf("error = %v\nwant it to quote what the hypervisor said", err)
	}
	if !strings.Contains(err.Error(), "exited before it became ready") {
		t.Errorf("error = %v\nwant it to still say the vm exited", err)
	}
}

// A guest that dies takes its reason with it: the console log is the only place
// a panic or a failed init says anything, and it lives in a directory that is
// removed when the failed boot is cleaned up. Quote it while it is still there.
func TestWaitReadyQuotesTheGuestConsole(t *testing.T) {
	m := testManager(t)
	namedImage(t, m, "img", "")
	m.backend = &captureBackend{}

	vm, err := m.start(StartSpec{ImageName: "img"})
	if err != nil {
		t.Fatal(err)
	}
	// What a QEMU guest that cannot find its root leaves behind.
	console := filepath.Join(m.cfg.VMDir(vm.ID), "console.log")
	body := "[    0.9] /init: mounting root\n[    1.0] /init: no /dev/vda block device\nKernel panic - not syncing: Attempted to kill init!\n"
	if err := os.WriteFile(console, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = m.WaitReady(ctx, vm.ID)
	if err == nil {
		t.Fatal("WaitReady succeeded for a VM whose process had exited")
	}
	if !strings.Contains(err.Error(), "no /dev/vda block device") {
		t.Errorf("error = %v\nwant it to quote the guest console", err)
	}
}

// The excerpt is a tail, not a transcript: a chatty hypervisor must not grow the
// daemon without bound, and the process we are only watching must never see a
// short write.
func TestExcerptKeepsOnlyTheTail(t *testing.T) {
	e := NewExcerpt(8)
	if n, err := e.Write([]byte("0123456789")); n != 10 || err != nil {
		t.Fatalf("Write = (%d, %v), want (10, nil)", n, err)
	}
	if got := e.String(); got != "23456789" {
		t.Errorf("String() = %q, want the last 8 bytes", got)
	}
	_, _ = e.Write([]byte("XY"))
	if got := e.String(); got != "456789XY" {
		t.Errorf("String() = %q, want the newest bytes", got)
	}
	// A backend that just prints a newline has nothing to say.
	e2 := NewExcerpt(8)
	_, _ = e2.Write([]byte("  \n\n"))
	if got := e2.String(); got != "" {
		t.Errorf("String() = %q, want empty for whitespace", got)
	}
	// The zero value is usable, with a sane default bound.
	var e3 Excerpt
	_, _ = e3.Write([]byte("hello"))
	if got := e3.String(); got != "hello" {
		t.Errorf("zero-value String() = %q", got)
	}
}

// A pool that cannot boot must stop trying every tick. A failed attempt now
// fails in about a second, so without a growing delay a host with a broken
// hypervisor boots a VM per second forever.
func TestPoolBacksOffAfterAFailedBoot(t *testing.T) {
	cases := []struct {
		current, want time.Duration
	}{
		{0, 5 * time.Second},
		{5 * time.Second, 10 * time.Second},
		{30 * time.Second, time.Minute},
		{time.Minute, 2 * time.Minute},
		{2 * time.Minute, 2 * time.Minute},  // capped
		{10 * time.Minute, 2 * time.Minute}, // and stays capped
	}
	for _, c := range cases {
		if got := nextBackoff(c.current); got != c.want {
			t.Errorf("nextBackoff(%s) = %s, want %s", c.current, got, c.want)
		}
	}
}

// The pool must resume promptly once a boot works again.
func TestPoolBackoffResetsAfterASuccessfulBoot(t *testing.T) {
	p := NewPool(nil, 1, 0, io.Discard)
	p.backoff, p.retryAt = time.Minute, time.Now().Add(time.Minute)
	// What the success path does, without standing up a manager and a VM.
	p.mu.Lock()
	p.backoff, p.retryAt = 0, time.Time{}
	p.mu.Unlock()
	if p.backoff != 0 || !p.retryAt.IsZero() {
		t.Errorf("backoff = %s, retryAt = %s; want cleared", p.backoff, p.retryAt)
	}
}
