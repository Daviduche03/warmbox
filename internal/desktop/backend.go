package desktop

import (
	"fmt"
	"os/exec"
)

// Caps describes what a hypervisor backend can do.
type Caps struct {
	// GUI reports whether the backend can stream a graphical desktop.
	GUI bool
	// SharedFS reports virtiofs host-directory sharing support.
	SharedFS bool
	// Snapshot reports in-memory VM snapshot/restore support.
	Snapshot bool
}

// Disk is a block device to attach. Order matters: the first disk is /dev/vda.
type Disk struct {
	Path     string
	ReadOnly bool
}

// Share is a host directory exposed to the guest over virtiofs.
type Share struct {
	Dir string
	Tag string
}

// LaunchSpec is a hypervisor-agnostic description of a microVM to boot.
type LaunchSpec struct {
	ID      string
	CPUs    uint
	MemMiB  uint
	Kernel  string
	Initrd  string
	Cmdline string
	Disks   []Disk
	Shares  []Share
	Console string // serial console log path
	PidFile string
}

// Backend boots microVMs on a specific hypervisor. The manager owns the process
// lifecycle (start/wait/kill); a backend's job is to turn a LaunchSpec into the
// right command for its hypervisor.
type Backend interface {
	Name() string
	Launch(spec LaunchSpec) (*exec.Cmd, error)
	Capabilities() Caps
}

// newBackend returns the named backend. Only vfkit exists today; more
// (cloud-hypervisor, qemu, firecracker) are additive.
func newBackend(name, vfkitPath string) (Backend, error) {
	switch name {
	case "", "vfkit":
		return &vfkitBackend{path: vfkitPath}, nil
	default:
		return nil, fmt.Errorf("unknown backend %q (supported: vfkit)", name)
	}
}

// vfkitBackend boots via vfkit on Apple's Virtualization.framework.
type vfkitBackend struct {
	path string // vfkit binary; empty => "vfkit" from PATH
}

func (b *vfkitBackend) Name() string { return "vfkit" }

func (b *vfkitBackend) Capabilities() Caps {
	return Caps{GUI: true, SharedFS: true, Snapshot: false}
}

func (b *vfkitBackend) Launch(spec LaunchSpec) (*exec.Cmd, error) {
	bin := b.path
	if bin == "" {
		bin = "vfkit"
	}
	args := []string{
		"--cpus", fmt.Sprint(spec.CPUs),
		"--memory", fmt.Sprint(spec.MemMiB),
		"--kernel", spec.Kernel,
		"--initrd", spec.Initrd,
		"--kernel-cmdline", spec.Cmdline,
		"--device", "virtio-serial,logFilePath=" + spec.Console,
		"--device", "virtio-net,nat",
		"--device", "virtio-rng",
	}
	for _, d := range spec.Disks {
		dev := "virtio-blk,path=" + d.Path
		if d.ReadOnly {
			dev += ",readonly"
		}
		args = append(args, "--device", dev)
	}
	for _, s := range spec.Shares {
		args = append(args, "--device", "virtio-fs,sharedDir="+s.Dir+",mountTag="+s.Tag)
	}
	if spec.PidFile != "" {
		args = append(args, "--pidfile", spec.PidFile)
	}
	return exec.Command(bin, args...), nil
}
