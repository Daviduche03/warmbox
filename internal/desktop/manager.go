package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	backend, err := newBackend(cfg.Backend, cfg.VfkitPath)
	if err != nil {
		fmt.Fprintf(log, "desktop: %v; using vfkit\n", err)
		backend, _ = newBackend("vfkit", cfg.VfkitPath)
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

// Start launches a new microVM and returns it (state = booting). Callers wait
// for readiness with WaitReady. The writable layer is ephemeral (tmpfs).
func (m *Manager) Start(id string) (*VM, error) { return m.start(id, "", "") }

// StartWithVolume launches a microVM whose writable layer is the given volume
// image (a raw ext4 disk) attached read-write as /dev/vdb. volumeName names it
// for later commit/release.
func (m *Manager) StartWithVolume(id, volumeName, image string) (*VM, error) {
	return m.start(id, volumeName, image)
}

func (m *Manager) start(id, volumeName, volumeImage string) (*VM, error) {
	if id == "" {
		id = NewID()
	}
	dir := m.cfg.VMDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating vm dir: %w", err)
	}

	consolePath := filepath.Join(dir, "console.log")
	pidPath := filepath.Join(dir, "vm.pid")

	// Boot mode selection:
	//   overlay  - shared read-only squashfs base + tmpfs overlay (no per-VM copy)
	//   disk     - per-VM clone of an ext4 image
	//   initramfs- the whole rootfs loaded into RAM
	overlayBoot := m.overlayAvailable()
	diskBoot := !overlayBoot && m.diskAvailable()

	cmdline := fmt.Sprintf(
		"console=hvc0 warmbox.id=%s warmbox.host=%s warmbox.port=%s",
		id, m.cfg.HostAddr, portOf(m.cfg.APIAddr),
	)
	if m.cfg.ShareDir != "" && m.cfg.ShareTag != "" {
		cmdline += " warmbox.share=" + m.cfg.ShareTag
	}
	if volumeImage != "" {
		cmdline += " warmbox.volume=1"
	}

	spec := LaunchSpec{
		ID:      id,
		CPUs:    m.cfg.CPUs,
		MemMiB:  m.cfg.MemMiB,
		Kernel:  m.cfg.KernelPath,
		Cmdline: cmdline,
		Console: consolePath,
		PidFile: pidPath,
	}
	switch {
	case overlayBoot:
		spec.Initrd = m.cfg.OverlayInitrdPath
		spec.Disks = append(spec.Disks, Disk{Path: m.cfg.SquashPath, ReadOnly: true})
	case diskBoot:
		diskPath := filepath.Join(dir, "rootfs.img")
		if err := cloneFile(m.cfg.DiskPath, diskPath); err != nil {
			return nil, fmt.Errorf("cloning rootfs image: %w", err)
		}
		spec.Cmdline += " root=/dev/vda rootfstype=ext4 rootwait rw"
		spec.Initrd = m.cfg.BootInitrdPath
		spec.Disks = append(spec.Disks, Disk{Path: diskPath})
	default:
		spec.Initrd = m.cfg.InitrdPath
	}
	if volumeImage != "" {
		spec.Disks = append(spec.Disks, Disk{Path: volumeImage})
	}
	if m.cfg.ShareDir != "" && m.cfg.ShareTag != "" {
		spec.Shares = append(spec.Shares, Share{Dir: m.cfg.ShareDir, Tag: m.cfg.ShareTag})
	}

	cmd, err := m.backend.Launch(spec)
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", m.backend.Name(), err)
	}

	vm := &VM{
		ID:      id,
		State:   StateBooting,
		Started: time.Now(),
		Volume:  volumeName,
		cmd:     cmd,
		dir:     dir,
		ready:   make(chan struct{}),
	}

	m.mu.Lock()
	m.vms[id] = vm
	m.mu.Unlock()

	fmt.Fprintf(m.log, "desktop: booting vm %s via %s (pid %d)\n", id, m.backend.Name(), cmd.Process.Pid)

	go func() {
		err := cmd.Wait()
		vm.setState(StateDead)
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

// List returns serialisable snapshots of all VMs.
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.vms))
	for _, vm := range m.vms {
		out = append(out, vm.Info())
	}
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

// overlayAvailable reports whether overlay boot is configured and both the
// shared squashfs base and its boot initramfs exist.
func (m *Manager) overlayAvailable() bool {
	if m.cfg.SquashPath == "" || m.cfg.OverlayInitrdPath == "" {
		return false
	}
	if _, err := os.Stat(m.cfg.SquashPath); err != nil {
		return false
	}
	if _, err := os.Stat(m.cfg.OverlayInitrdPath); err != nil {
		return false
	}
	return true
}

// diskAvailable reports whether disk boot is configured and both the base image
// and the boot initramfs exist.
func (m *Manager) diskAvailable() bool {
	if m.cfg.DiskPath == "" || m.cfg.BootInitrdPath == "" {
		return false
	}
	if _, err := os.Stat(m.cfg.DiskPath); err != nil {
		return false
	}
	if _, err := os.Stat(m.cfg.BootInitrdPath); err != nil {
		return false
	}
	return true
}

// cloneFile makes a copy-on-write clone when the filesystem supports it
// (APFS "cp -c"), otherwise a full copy.
func cloneFile(src, dst string) error {
	if err := exec.Command("cp", "-c", src, dst).Run(); err == nil {
		return nil
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
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
