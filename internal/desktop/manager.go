package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Manager boots and tracks guest microVMs.
type Manager struct {
	cfg     *Config
	log     io.Writer
	backend Backend

	onDestroy func(*VM)

	mu  sync.Mutex
	vms map[string]*VM
}

// NewManager creates a Manager using the backend named in cfg (default vfkit).
func NewManager(cfg *Config, log io.Writer) *Manager {
	if log == nil {
		log = io.Discard
	}
	backend, err := newBackend(cfg)
	if err != nil {
		fmt.Fprintf(log, "desktop: %v; using vfkit\n", err)
		backend, _ = newBackend(&Config{Backend: "vfkit", VfkitPath: cfg.VfkitPath, GUI: cfg.GUI})
	}
	return &Manager{cfg: cfg, log: log, backend: backend, vms: map[string]*VM{}}
}

// Backend returns the manager's hypervisor backend.
func (m *Manager) Backend() Backend { return m.backend }

// SetOnDestroy registers a callback invoked for each VM as it is destroyed
// (after the process has exited), e.g. to commit and release its volume.
func (m *Manager) SetOnDestroy(fn func(*VM)) { m.onDestroy = fn }

// Config returns the manager's config.
func (m *Manager) Config() *Config { return m.cfg }

// NewID returns a short random VM id.
func NewID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// StartSpec describes the desktop to boot. Zero fields fall back to the
// daemon's defaults (or the image's own meta.json overrides).
type StartSpec struct {
	ID string
	// VolumeName/VolumeImage attach a persistent volume (its name for later
	// commit, and the resolved local image path).
	VolumeName  string
	VolumeImage string
	// ImageName is a named guest image under ImageDir; empty means the
	// daemon's default.
	ImageName string
	// CPUs and MemMiB override the daemon's per-VM defaults when non-zero.
	CPUs   uint
	MemMiB uint
}

// Start launches a new microVM from the default image and returns it (state =
// booting). Callers wait for readiness with WaitReady.
//
// Everything else goes through StartDesktop with a StartSpec: a convenience
// wrapper per option was how a volume-backed create ended up silently ignoring
// the image the caller asked for.
func (m *Manager) Start(id string) (*VM, error) { return m.start(StartSpec{ID: id}) }

// StartDesktop boots exactly what the caller asked for, including explicit
// vCPU/memory requests (which rule out a warm-pool VM: those are sized when
// they are booted).
func (m *Manager) StartDesktop(spec StartSpec) (*VM, error) { return m.start(spec) }

func (m *Manager) start(spec StartSpec) (*VM, error) {
	id, volumeName, volumeImage := spec.ID, spec.VolumeName, spec.VolumeImage
	// Every VM boots a named image: a caller that names none gets this daemon's
	// default. The warm pool boots this way, so getting it wrong would hand out
	// one image no matter what --image the daemon was started with.
	imageName := CanonicalImage(spec.ImageName, m.cfg.Image)
	if id == "" {
		id = NewID()
	}
	dir := m.cfg.VMDir(id)
	// 0700: the directory holds this VM's control socket, which is
	// unauthenticated by design (vfkit's REST API has no auth).
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating vm dir: %w", err)
	}

	consolePath := filepath.Join(dir, "console.log")
	pidPath := filepath.Join(dir, "vm.pid")

	// Resolve the boot image: an EFI disk or an overlay (squashfs) image. Both
	// kinds are named images under ImageDir; there is no fallback, because a
	// daemon with no bootable image has nothing to boot and should say so.
	img, err := m.resolveImage(imageName)
	if err != nil {
		return nil, err
	}
	meta := img.meta

	// Per-image overrides win over the daemon defaults; an explicit request
	// wins over both (the caller asked for that size).
	cpus, mem := m.cfg.CPUs, m.cfg.MemMiB
	display, input := m.cfg.GPU, m.cfg.Input
	if meta.CPUs > 0 {
		cpus = meta.CPUs
	}
	if meta.MemMiB > 0 {
		mem = meta.MemMiB
	}
	if spec.CPUs > 0 {
		cpus = spec.CPUs
	}
	if spec.MemMiB > 0 {
		mem = spec.MemMiB
	}
	if meta.Headless {
		// The image declares it has no screen, so don't arm a GPU or input
		// devices for a compositor that is never coming up — whatever the
		// daemon's defaults happen to be. The image wins here.
		display, input = "", false
	} else {
		if meta.GPU != "" {
			display = meta.GPU
		}
		if meta.Input {
			input = true
		}
	}

	hostAddr := m.backend.GuestHostAddr()
	if hostAddr == "" {
		hostAddr = m.cfg.HostAddr
	}
	cmdline := fmt.Sprintf(
		"warmbox.id=%s warmbox.host=%s warmbox.port=%s",
		id, hostAddr, portOf(m.cfg.APIAddr),
	)
	if m.cfg.ShareDir != "" && m.cfg.ShareTag != "" {
		cmdline += " warmbox.share=" + m.cfg.ShareTag
	}
	if volumeImage != "" {
		cmdline += " warmbox.volume=1"
	}
	if m.cfg.EgressAddr != "" {
		cmdline += fmt.Sprintf(" warmbox.proxy=%s:%s", hostAddr, portOf(m.cfg.EgressAddr))
	}

	launch := LaunchSpec{
		ID:         id,
		CPUs:       cpus,
		MemMiB:     mem,
		Console:    consolePath,
		PidFile:    pidPath,
		Display:    display,
		Input:      input,
		ControlDir: dir,
	}
	switch img.kind {
	case "efi":
		if _, err := os.Stat(img.disk); err != nil {
			return nil, fmt.Errorf("efi disk %s: %w", img.disk, err)
		}
		diskPath := filepath.Join(dir, "disk.raw")
		if err := cloneFile(img.disk, diskPath); err != nil {
			return nil, fmt.Errorf("cloning efi disk: %w", err)
		}
		// Seed the boot entry from the machine that installed the disk when a
		// variable store is provided; otherwise let vfkit create a fresh one.
		varPath := filepath.Join(dir, "efi-vars.fd")
		if img.vars != "" {
			if err := CopyFile(img.vars, varPath); err != nil {
				return nil, fmt.Errorf("seeding efi vars: %w", err)
			}
		} else {
			_ = os.Remove(varPath)
		}
		launch.EFI = true
		launch.EFIVars = varPath
		launch.Disks = append(launch.Disks, Disk{Path: diskPath})
		// EFI boot has no kernel cmdline, so hand the guest its identity over a
		// virtiofs share instead: the image's warmbox-ready unit reads it and
		// reports readiness back to the daemon.
		cfgDir := filepath.Join(dir, "config")
		if err := os.MkdirAll(cfgDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating config share: %w", err)
		}
		confMap := map[string]string{
			"id":   id,
			"host": hostAddr,
			"port": portOf(m.cfg.APIAddr),
		}
		if volumeImage != "" {
			confMap["volume"] = "1"
		}
		if m.cfg.EgressAddr != "" {
			confMap["proxy"] = hostAddr + ":" + portOf(m.cfg.EgressAddr)
		}
		conf, _ := json.Marshal(confMap)
		if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), conf, 0o644); err != nil {
			return nil, fmt.Errorf("writing config.json: %w", err)
		}
		launch.Shares = append(launch.Shares, Share{Dir: cfgDir, Tag: "warmbox-config"})
	case "overlay":
		// warmbox boots the kernel directly, so the guest gets its identity on
		// the kernel cmdline and the rootfs is a shared read-only squashfs.
		launch.Kernel = img.kernel
		launch.Cmdline = cmdline
		launch.Initrd = img.initrd
		launch.Disks = append(launch.Disks, Disk{Path: img.squash, ReadOnly: true})
	default:
		return nil, fmt.Errorf("image %q has an unknown boot kind %q", img.name, img.kind)
	}
	if volumeImage != "" {
		launch.Disks = append(launch.Disks, Disk{Path: volumeImage})
	}
	if m.cfg.ShareDir != "" && m.cfg.ShareTag != "" {
		launch.Shares = append(launch.Shares, Share{Dir: m.cfg.ShareDir, Tag: m.cfg.ShareTag})
	}

	inst, err := m.backend.Launch(launch)
	if err != nil {
		return nil, err
	}
	cmd := inst.Cmd
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", m.backend.Name(), err)
	}

	vm := &VM{
		ID:       id,
		State:    StateBooting,
		Started:  time.Now(),
		Volume:   volumeName,
		Headless: meta.Headless,
		forwards: inst.Forwards,
		cmd:      cmd,
		inst:     inst,
		dir:      dir,
		ready:    make(chan struct{}),
		done:     make(chan struct{}),
	}

	m.mu.Lock()
	m.vms[id] = vm
	m.mu.Unlock()

	fmt.Fprintf(m.log, "desktop: booting vm %s via %s (pid %d)\n", id, m.backend.Name(), cmd.Process.Pid)

	go func() {
		err := cmd.Wait()
		vm.setState(StateDead)
		vm.markExited(err)
		if err != nil {
			fmt.Fprintf(m.log, "desktop: vm %s exited: %v\n", id, err)
		} else {
			fmt.Fprintf(m.log, "desktop: vm %s exited\n", id)
		}
	}()

	return vm, nil
}

// MarkReady is called by the API when a guest reports readiness.
func (m *Manager) MarkReady(id, ip string) bool {
	m.mu.Lock()
	vm, ok := m.vms[id]
	m.mu.Unlock()
	if !ok {
		return false
	}
	vm.markReady(ip)
	fmt.Fprintf(m.log, "desktop: vm %s ready at %s\n", id, ip)
	return true
}

// WaitReady blocks until the VM is ready or ctx is done.
func (m *Manager) WaitReady(ctx context.Context, id string) (*VM, error) {
	m.mu.Lock()
	vm, ok := m.vms[id]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown vm %s", id)
	}
	select {
	case <-vm.Ready():
		return vm, nil
	case <-vm.Done():
		// The process is gone. Waiting out the rest of the timeout would only
		// hide why: a vfkit that could not open its kernel should read as "the
		// vm exited", not as a boot that was merely slow.
		if vm.wasReady() {
			// It did come up first; the caller gets the VM and can see its
			// state. Reporting this as a failed boot would be a lie.
			return vm, nil
		}
		if err := vm.ExitError(); err != nil {
			return nil, fmt.Errorf("the vm exited before it became ready: %w%s", err, vm.exitDetail())
		}
		return nil, fmt.Errorf("the vm exited before it became ready%s", vm.exitDetail())
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Get returns a VM by id.
func (m *Manager) Get(id string) (*VM, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	return vm, ok
}

// List returns serialisable snapshots of all VMs, oldest first.
//
// The VMs live in a map, and Go randomises map iteration order on every pass —
// without a sort the dashboard would see the rows reshuffle on every poll.
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.vms))
	for _, vm := range m.vms {
		out = append(out, vm.Info())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Started.Equal(out[j].Started) {
			return out[i].ID < out[j].ID
		}
		return out[i].Started.Before(out[j].Started)
	})
	return out
}

// Destroy stops and removes a VM.
func (m *Manager) Destroy(id string) error {
	m.mu.Lock()
	vm, ok := m.vms[id]
	if ok {
		delete(m.vms, id)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown vm %s", id)
	}

	if vm.cmd != nil && vm.cmd.Process != nil {
		_ = vm.cmd.Process.Kill()
		// Wait briefly for vfkit to exit so an attached volume image is stable
		// before the destroy hook commits it.
		for i := 0; i < 30 && vm.Info().State != StateDead; i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if m.onDestroy != nil {
		m.onDestroy(vm)
	}
	_ = os.RemoveAll(vm.dir)
	vm.setState(StateDead)
	fmt.Fprintf(m.log, "desktop: destroyed vm %s\n", id)
	return nil
}

// Pause freezes a VM's vCPUs in place. The guest keeps its memory (and with it
// its running session); the host simply stops scheduling it, so pause reclaims
// CPU rather than RAM. Use Destroy to hand the memory back.
func (m *Manager) Pause(id string) error {
	vm, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("unknown vm %s", id)
	}
	switch st := vm.Info().State; st {
	case StatePaused:
		return nil
	case StateReady, StateBusy:
		// Fine to freeze.
	default:
		return fmt.Errorf("cannot pause vm %s in state %s", id, st)
	}
	if !m.backend.Capabilities().Pause {
		return fmt.Errorf("backend %s cannot pause vms", m.backend.Name())
	}
	if err := m.backend.Pause(vm.inst); err != nil {
		return err
	}
	vm.markPaused()
	fmt.Fprintf(m.log, "desktop: paused vm %s (memory retained)\n", id)
	return nil
}

// Resume thaws a paused VM. Its guest session continues where it left off.
func (m *Manager) Resume(id string) error {
	vm, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("unknown vm %s", id)
	}
	if st := vm.Info().State; st != StatePaused {
		return fmt.Errorf("vm %s is not paused (state %s)", id, st)
	}
	if !m.backend.Capabilities().Pause {
		return fmt.Errorf("backend %s cannot resume vms", m.backend.Name())
	}
	if err := m.backend.Resume(vm.inst); err != nil {
		return err
	}
	vm.markResumed()
	fmt.Fprintf(m.log, "desktop: resumed vm %s\n", id)
	return nil
}

// Shutdown destroys every VM.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.vms))
	for id := range m.vms {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.Destroy(id)
	}
}

// cloneFile makes a copy-on-write clone when the filesystem supports it
// (APFS "cp -c"), otherwise a full copy.
func cloneFile(src, dst string) error {
	if err := exec.Command("cp", "-c", src, dst).Run(); err == nil {
		return nil
	}
	return CopyFile(src, dst)
}

// CopyFile copies src to dst. Exporting it keeps main.go from growing its own
// version of the same ten lines.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// portOf extracts the port from an address like ":7070".
func portOf(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[i+1:]
		}
	}
	return "7070"
}
