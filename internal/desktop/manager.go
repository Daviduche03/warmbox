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

// Start launches a new microVM from the default image and returns it (state =
// booting). Callers wait for readiness with WaitReady.
func (m *Manager) Start(id string) (*VM, error) { return m.start(id, "", "", m.cfg.Image) }

// StartWithVolume launches a microVM whose writable layer is the given volume
// image (a raw ext4 disk) attached read-write as /dev/vdb. volumeName names it
// for later commit/release.
func (m *Manager) StartWithVolume(id, volumeName, image string) (*VM, error) {
	return m.start(id, volumeName, image, m.cfg.Image)
}

// StartImage launches a microVM from a named image under cfg.ImageDir,
// optionally attached to a persistent volume.
func (m *Manager) StartImage(id, volumeName, volumeImage, imageName string) (*VM, error) {
	return m.start(id, volumeName, volumeImage, imageName)
}

func (m *Manager) start(id, volumeName, volumeImage, imageName string) (*VM, error) {
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

	// Resolve the boot image. A named image is either an EFI disk or an overlay
	// (squashfs) image; otherwise fall back to the built-in image
	// (overlay > disk > initramfs).
	var img resolved
	haveImg := false
	switch {
	case imageName != "" && !isBuiltinImage(imageName):
		r, err := m.resolveImage(imageName)
		if err != nil {
			return nil, err
		}
		img, haveImg = r, true
	case !isBuiltinImage(imageName) && m.cfg.EFIDisk != "":
		img, haveImg = resolved{kind: "efi", disk: m.cfg.EFIDisk, vars: m.cfg.EFIVars}, true
	}
	meta := img.meta

	// Per-image overrides win over the daemon defaults.
	cpus, mem := m.cfg.CPUs, m.cfg.MemMiB
	display, input := m.cfg.GPU, m.cfg.Input
	if meta.CPUs > 0 {
		cpus = meta.CPUs
	}
	if meta.MemMiB > 0 {
		mem = meta.MemMiB
	}
	if meta.GPU != "" {
		display = meta.GPU
	}
	if meta.Input {
		input = true
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

	spec := LaunchSpec{
		ID:         id,
		CPUs:       cpus,
		MemMiB:     mem,
		Console:    consolePath,
		PidFile:    pidPath,
		Display:    display,
		Input:      input,
		ControlDir: dir,
	}
	switch {
	case haveImg && img.kind == "efi":
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
			if err := copyFile(img.vars, varPath); err != nil {
				return nil, fmt.Errorf("seeding efi vars: %w", err)
			}
		} else {
			_ = os.Remove(varPath)
		}
		spec.EFI = true
		spec.EFIVars = varPath
		spec.Disks = append(spec.Disks, Disk{Path: diskPath})
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
		spec.Shares = append(spec.Shares, Share{Dir: cfgDir, Tag: "warmbox-config"})
	case haveImg && img.kind == "overlay":
		// Startup is booted directly by warmbox, so the guest gets its identity
		// on the kernel cmdline (as the built-in image does).
		spec.Kernel = img.kernel
		spec.Cmdline = cmdline
		spec.Initrd = img.initrd
		spec.Disks = append(spec.Disks, Disk{Path: img.squash, ReadOnly: true})
	case m.overlayAvailable():
		spec.Kernel = m.cfg.KernelPath
		spec.Cmdline = cmdline
		spec.Initrd = m.cfg.OverlayInitrdPath
		spec.Disks = append(spec.Disks, Disk{Path: m.cfg.SquashPath, ReadOnly: true})
	case m.diskAvailable():
		diskPath := filepath.Join(dir, "rootfs.img")
		if err := cloneFile(m.cfg.DiskPath, diskPath); err != nil {
			return nil, fmt.Errorf("cloning rootfs image: %w", err)
		}
		spec.Kernel = m.cfg.KernelPath
		spec.Cmdline = cmdline + " root=/dev/vda rootfstype=ext4 rootwait rw"
		spec.Initrd = m.cfg.BootInitrdPath
		spec.Disks = append(spec.Disks, Disk{Path: diskPath})
	default:
		spec.Kernel = m.cfg.KernelPath
		spec.Cmdline = cmdline
		spec.Initrd = m.cfg.InitrdPath
	}
	if volumeImage != "" {
		spec.Disks = append(spec.Disks, Disk{Path: volumeImage})
	}
	if m.cfg.ShareDir != "" && m.cfg.ShareTag != "" {
		spec.Shares = append(spec.Shares, Share{Dir: m.cfg.ShareDir, Tag: m.cfg.ShareTag})
	}

	inst, err := m.backend.Launch(spec)
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
		forwards: inst.Forwards,
		cmd:      cmd,
		inst:     inst,
		dir:      dir,
		ready:    make(chan struct{}),
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
