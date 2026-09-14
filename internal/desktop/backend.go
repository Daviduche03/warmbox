package desktop

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
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

// Instance is a launched VM. VNCAddr is where the manager should dial the
// guest's VNC server — empty means "use the guest IP reported at readiness"
// (the vfkit/NAT case); set means the backend already provides a forwarding
// hop (the QEMU user-networking case).
type Instance struct {
	Cmd     *exec.Cmd
	VNCAddr string
}

// Backend boots microVMs on a specific hypervisor. The manager owns the process
// lifecycle (start/wait/kill); a backend's job is to turn a LaunchSpec into the
// right command for its hypervisor.
type Backend interface {
	Name() string
	Launch(spec LaunchSpec) (*Instance, error)
	Capabilities() Caps
	// GuestHostAddr is the address the guest uses to reach this host (the
	// readiness callback / API). Empty falls back to Config.HostAddr.
	GuestHostAddr() string
}

// newBackend returns the named backend. vfkit (macOS) and qemu (Linux) exist.
func newBackend(name, vfkitPath string) (Backend, error) {
	switch name {
	case "", "vfkit":
		return &vfkitBackend{path: vfkitPath}, nil
	case "qemu":
		return &qemuBackend{}, nil
	default:
		return nil, fmt.Errorf("unknown backend %q (supported: vfkit, qemu)", name)
	}
}

// --- vfkit (Apple Virtualization.framework, macOS) ---

type vfkitBackend struct {
	path string // vfkit binary; empty => "vfkit" from PATH
}

func (b *vfkitBackend) Name() string          { return "vfkit" }
func (b *vfkitBackend) GuestHostAddr() string { return "192.168.64.1" }
func (b *vfkitBackend) Capabilities() Caps    { return Caps{GUI: true, SharedFS: true, Snapshot: false} }

func (b *vfkitBackend) Launch(spec LaunchSpec) (*Instance, error) {
	bin := b.path
	if bin == "" {
		bin = "vfkit"
	}
	args := []string{
		"--cpus", fmt.Sprint(spec.CPUs),
		"--memory", fmt.Sprint(spec.MemMiB),
		"--kernel", spec.Kernel,
		"--initrd", spec.Initrd,
		"--kernel-cmdline", "console=hvc0 " + spec.Cmdline,
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
	return &Instance{Cmd: exec.Command(bin, args...)}, nil
}

// --- QEMU (KVM, Linux) ---

type qemuBackend struct {
	path string // qemu-system-* binary; empty => pick by host arch
}

func (b *qemuBackend) Name() string          { return "qemu" }
func (b *qemuBackend) GuestHostAddr() string { return "10.0.2.2" }
func (b *qemuBackend) Capabilities() Caps    { return Caps{GUI: true, SharedFS: true, Snapshot: false} }

func qemuSystemBinary() string {
	if runtime.GOARCH == "arm64" {
		return "qemu-system-aarch64"
	}
	return "qemu-system-x86_64"
}

func (b *qemuBackend) Launch(spec LaunchSpec) (*Instance, error) {
	bin := b.path
	if bin == "" {
		bin = qemuSystemBinary()
	}

	// QEMU user networking can't be reached host->guest, so forward a host port
	// to the guest's VNC server and tell the manager to dial that instead.
	vncPort, err := freePort()
	if err != nil {
		return nil, err
	}

	machine, cpu := "q35", "host"
	if runtime.GOARCH == "arm64" {
		machine = "virt"
	}
	args := []string{
		"-machine", machine + ",accel=kvm",
		"-cpu", cpu,
		"-smp", fmt.Sprint(spec.CPUs),
		"-m", fmt.Sprint(spec.MemMiB),
		"-nodefaults", "-no-reboot",
		"-kernel", spec.Kernel,
		"-initrd", spec.Initrd,
		"-append", "console=ttyS0 " + spec.Cmdline,
		"-serial", "file:" + spec.Console,
		"-device", "virtio-rng-pci",
		"-netdev", fmt.Sprintf("user,id=n0,hostfwd=tcp:127.0.0.1:%d-:5900", vncPort),
		"-device", "virtio-net-pci,netdev=n0",
	}
	for i, d := range spec.Disks {
		id := fmt.Sprintf("d%d", i)
		drive := "file=" + d.Path + ",if=none,id=" + id + ",format=raw"
		if d.ReadOnly {
			drive += ",readonly=on"
		}
		args = append(args, "-drive", drive, "-device", "virtio-blk-pci,drive="+id)
	}
	for i, s := range spec.Shares {
		args = append(args, "-virtfs",
			fmt.Sprintf("local,path=%s,mount_tag=%s,security_model=none,id=fs%d", s.Dir, s.Tag, i))
	}
	if spec.PidFile != "" {
		args = append(args, "-pidfile", spec.PidFile)
	}
	return &Instance{
		Cmd:     exec.Command(bin, args...),
		VNCAddr: fmt.Sprintf("127.0.0.1:%d", vncPort),
	}, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
