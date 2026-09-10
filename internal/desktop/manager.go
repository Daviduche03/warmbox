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
	cfg *Config
	log io.Writer

	mu  sync.Mutex
	vms map[string]*VM
}

// NewManager creates a Manager.
func NewManager(cfg *Config, log io.Writer) *Manager {
	if log == nil {
		log = io.Discard
	}
	return &Manager{cfg: cfg, log: log, vms: map[string]*VM{}}
}

// Config returns the manager's config.
func (m *Manager) Config() *Config { return m.cfg }

// NewID returns a short random VM id.
func NewID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Start launches a new microVM and returns it (state = booting). Callers wait
// for readiness with WaitReady.
func (m *Manager) Start(id string) (*VM, error) {
	if id == "" {
		id = NewID()
	}
	dir := m.cfg.VMDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating vm dir: %w", err)
	}

	consolePath := filepath.Join(dir, "console.log")
	pidPath := filepath.Join(dir, "vfkit.pid")

	// Boot mode selection:
	//   overlay  - shared read-only squashfs base + tmpfs overlay (no per-VM copy)
	//   disk     - per-VM clone of an ext4 image
	//   initramfs- the whole rootfs loaded into RAM
	overlayBoot := m.overlayAvailable()
	diskBoot := !overlayBoot && m.diskAvailable()
	var diskPath string
	if diskBoot {
		diskPath = filepath.Join(dir, "rootfs.img")
		if err := cloneFile(m.cfg.DiskPath, diskPath); err != nil {
			return nil, fmt.Errorf("cloning rootfs image: %w", err)
		}
	}

	cmdline := fmt.Sprintf(
		"console=hvc0 warmbox.id=%s warmbox.host=%s warmbox.port=%s",
		id, m.cfg.HostAddr, portOf(m.cfg.APIAddr),
	)
	if m.cfg.ShareDir != "" && m.cfg.ShareTag != "" {
		cmdline += " warmbox.share=" + m.cfg.ShareTag
	}
	initrd := m.cfg.InitrdPath
	switch {
	case overlayBoot:
		initrd = m.cfg.OverlayInitrdPath
	case diskBoot:
		cmdline += " root=/dev/vda rootfstype=ext4 rootwait rw"
		initrd = m.cfg.BootInitrdPath
	}

	args := []string{
		"--cpus", fmt.Sprint(m.cfg.CPUs),
		"--memory", fmt.Sprint(m.cfg.MemMiB),
		"--kernel", m.cfg.KernelPath,
		"--initrd", initrd,
		"--kernel-cmdline", cmdline,
		"--device", "virtio-serial,logFilePath=" + consolePath,
		"--device", "virtio-net,nat",
		"--device", "virtio-rng",
	}
	switch {
	case overlayBoot:
		args = append(args, "--device", "virtio-blk,path="+m.cfg.SquashPath+",readonly")
	case diskBoot:
		args = append(args, "--device", "virtio-blk,path="+diskPath)
	}
	if m.cfg.ShareDir != "" && m.cfg.ShareTag != "" {
		args = append(args,
			"--device",
			"virtio-fs,sharedDir="+m.cfg.ShareDir+",mountTag="+m.cfg.ShareTag,
		)
	}
	args = append(args, "--pidfile", pidPath)

	cmd := exec.Command(m.cfg.VfkitPath, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting vfkit: %w", err)
	}

	vm := &VM{
		ID:      id,
		State:   StateBooting,
		Started: time.Now(),
		cmd:     cmd,
		dir:     dir,
		ready:   make(chan struct{}),
	}

	m.mu.Lock()
	m.vms[id] = vm
	m.mu.Unlock()

	fmt.Fprintf(m.log, "desktop: booting vm %s (pid %d)\n", id, cmd.Process.Pid)

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
