package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Caps describes what a hypervisor backend can do.
type Caps struct {
	// GUI reports whether the backend can stream a graphical desktop.
	GUI bool
	// SharedFS reports virtiofs host-directory sharing support.
	SharedFS bool
	// Snapshot reports in-memory VM snapshot/restore support.
	Snapshot bool
	// Pause reports whether running VMs can be frozen and resumed in place.
	// A paused VM keeps its memory (the host still owns those pages); pausing
	// saves CPU, not RAM.
	Pause bool
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
	// EFI boots via firmware from the first disk (vfkit --bootloader efi),
	// ignoring Kernel/Initrd/Cmdline. EFIVars is the per-VM variable store
	// path; a fresh store is created when the file is missing.
	EFI     bool
	EFIVars string
	// Display, when non-empty ("1440x900"), attaches a virtio-gpu device so a
	// Wayland compositor in the guest has an output to render to. Input adds
	// virtio keyboard and pointing devices.
	Display string
	Input   bool
	Disks   []Disk
	Shares  []Share
	Console string // serial console log path
	PidFile string
	// ControlDir, when non-empty, is a per-VM directory where the backend may
	// open its control channel (pause/resume). Backends that need no channel
	// ignore it.
	ControlDir string
}

// Instance is a launched VM. Forwards maps a guest port to a host dial address
// (host:port) when the host cannot reach the guest directly (QEMU user
// networking); when empty the manager dials the guest IP + guest port (vfkit
// NAT). The VNC port (5900) and agent port (7077) are the ones used today.
type Instance struct {
	Cmd      *exec.Cmd
	Forwards map[int]string
	// Control is the backend's live control endpoint: a unix socket path
	// (vfkit's REST API, QEMU's QMP monitor). Empty when unavailable.
	Control string
}

// Backend boots microVMs on a specific hypervisor. The manager owns the process
// lifecycle (start/wait/kill); a backend's job is to turn a LaunchSpec into the
// right command for its hypervisor.
type Backend interface {
	Name() string
	Launch(spec LaunchSpec) (*Instance, error)
	Capabilities() Caps
	// Pause freezes the VM's vCPUs and Resume thaws them. Guest memory stays
	// allocated throughout — pause saves CPU, not RAM. Only called when
	// Capabilities().Pause is set.
	Pause(inst *Instance) error
	Resume(inst *Instance) error
	// GuestHostAddr is the address the guest uses to reach this host (the
	// readiness callback / API). Empty falls back to Config.HostAddr.
	GuestHostAddr() string
}

// newBackend returns the named backend. vfkit (macOS) and qemu (Linux) exist.
func newBackend(cfg *Config) (Backend, error) {
	switch cfg.Backend {
	case "", "vfkit":
		return &vfkitBackend{path: cfg.VfkitPath, gui: cfg.GUI}, nil
	case "qemu":
		return &qemuBackend{}, nil
	default:
		return nil, fmt.Errorf("unknown backend %q (supported: vfkit, qemu)", cfg.Backend)
	}
}

// --- vfkit (Apple Virtualization.framework, macOS) ---

type vfkitBackend struct {
	path string // vfkit binary; empty => "vfkit" from PATH
	gui  bool   // open the native window (bring-up aid)
}

func (b *vfkitBackend) Name() string          { return "vfkit" }
func (b *vfkitBackend) GuestHostAddr() string { return "192.168.64.1" }
func (b *vfkitBackend) Capabilities() Caps {
	return Caps{GUI: true, SharedFS: true, Snapshot: false, Pause: true}
}

func (b *vfkitBackend) Launch(spec LaunchSpec) (*Instance, error) {
	bin := b.path
	if bin == "" {
		bin = "vfkit"
	}
	args := []string{
		"--cpus", fmt.Sprint(spec.CPUs),
		"--memory", fmt.Sprint(spec.MemMiB),
	}
	if spec.EFI {
		args = append(args, "--bootloader", "efi,variable-store="+spec.EFIVars+",create")
	} else {
		args = append(args,
			"--kernel", spec.Kernel,
			"--initrd", spec.Initrd,
			"--kernel-cmdline", "console=hvc0 "+spec.Cmdline,
		)
	}
	args = append(args,
		"--device", "virtio-serial,logFilePath="+spec.Console,
		"--device", "virtio-net,nat",
		"--device", "virtio-rng",
	)
	if spec.Display != "" {
		w, h, err := parseDisplay(spec.Display)
		if err != nil {
			return nil, err
		}
		args = append(args, "--device", fmt.Sprintf("virtio-gpu,width=%d,height=%d", w, h))
	}
	if spec.Input {
		args = append(args,
			"--device", "virtio-input,keyboard",
			"--device", "virtio-input,pointing",
		)
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
	// Open vfkit's REST API on a unix socket — the API is unauthenticated, so
	// it lives in the per-VM directory (mode 0700) rather than on a loopback
	// port any local process could reach. The manager freezes and thaws the VM
	// through it (POST /vm/state).
	control := ""
	if spec.ControlDir != "" {
		control = filepath.Join(spec.ControlDir, "vfkit.sock")
		args = append(args, "--restful-uri", "unix://"+control)
	}
	if b.gui {
		args = append(args, "--gui")
	}
	cmd := exec.Command(bin, args...)
	// Keep vfkit's own diagnostics with the VM (next to the guest serial log)
	// rather than dropping them or spamming the daemon.
	if spec.Console != "" {
		if f, err := os.OpenFile(filepath.Join(filepath.Dir(spec.Console), "vfkit.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			cmd.Stderr = f
		}
	}
	return &Instance{Cmd: cmd, Control: control}, nil
}

// Pause freezes the guest's vCPUs; Resume thaws them. The guest's memory stays
// in the host process, so this buys CPU back, not RAM.
func (b *vfkitBackend) Pause(inst *Instance) error  { return vfkitSetState(inst, "Pause") }
func (b *vfkitBackend) Resume(inst *Instance) error { return vfkitSetState(inst, "Resume") }

// vfkitSetState posts a state transition to vfkit's REST API (valid: Pause,
// Resume, Stop, HardStop) over its unix socket.
func vfkitSetState(inst *Instance, state string) error {
	if inst == nil || inst.Control == "" {
		return fmt.Errorf("vm has no control channel")
	}
	req, err := http.NewRequest(http.MethodPost, "http://vfkit/vm/state",
		strings.NewReader(`{"state":"`+state+`"}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", inst.Control)
			},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("vfkit %s: %w", state, err)
	}
	defer resp.Body.Close()
	// vfkit accepts a state change asynchronously and answers 202.
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("vfkit %s: %s %s", state, resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

// parseDisplay splits a "WIDTHxHEIGHT" string.
func parseDisplay(s string) (int, int, error) {
	parts := strings.SplitN(strings.ToLower(s), "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("bad display %q (want WIDTHxHEIGHT, e.g. 1440x900)", s)
	}
	w, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("bad display width in %q", s)
	}
	h, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("bad display height in %q", s)
	}
	return w, h, nil
}

// --- QEMU (KVM, Linux) ---

type qemuBackend struct {
	path string // qemu-system-* binary; empty => pick by host arch
}

func (b *qemuBackend) Name() string          { return "qemu" }
func (b *qemuBackend) GuestHostAddr() string { return "10.0.2.2" }
func (b *qemuBackend) Capabilities() Caps {
	return Caps{GUI: true, SharedFS: true, Snapshot: false, Pause: true}
}

func qemuSystemBinary() string {
	if runtime.GOARCH == "arm64" {
		return "qemu-system-aarch64"
	}
	return "qemu-system-x86_64"
}

func (b *qemuBackend) Launch(spec LaunchSpec) (*Instance, error) {
	if spec.EFI {
		return nil, fmt.Errorf("efi boot is not supported on the qemu backend yet (needs OVMF)")
	}
	bin := b.path
	if bin == "" {
		bin = qemuSystemBinary()
	}

	// QEMU user networking can't be reached host->guest, so forward host ports to
	// the guest's VNC (5900) and agent (7077) servers and dial those instead.
	vncPort, err := freePort()
	if err != nil {
		return nil, err
	}
	agentPort, err := freePort()
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
		"-netdev", fmt.Sprintf("user,id=n0,hostfwd=tcp:127.0.0.1:%d-:5900,hostfwd=tcp:127.0.0.1:%d-:7077", vncPort, agentPort),
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
	// A QMP monitor socket lets the manager freeze and thaw this VM.
	control := ""
	if spec.ControlDir != "" {
		control = filepath.Join(spec.ControlDir, "qmp.sock")
		args = append(args, "-qmp", "unix:"+control+",server,nowait")
	}
	if spec.PidFile != "" {
		args = append(args, "-pidfile", spec.PidFile)
	}
	return &Instance{
		Cmd: exec.Command(bin, args...),
		Forwards: map[int]string{
			5900: fmt.Sprintf("127.0.0.1:%d", vncPort),
			7077: fmt.Sprintf("127.0.0.1:%d", agentPort),
		},
		Control: control,
	}, nil
}

// Pause freezes the guest's vCPUs; Resume thaws them. Guest memory stays in the
// QEMU process, so this buys CPU back, not RAM.
func (b *qemuBackend) Pause(inst *Instance) error  { return qmpExec(inst, "stop") }
func (b *qemuBackend) Resume(inst *Instance) error { return qmpExec(inst, "cont") }

// qmpExec runs a single QMP command against a VM's monitor socket and reports
// the monitor's error, if any.
func qmpExec(inst *Instance, command string) error {
	if inst == nil || inst.Control == "" {
		return fmt.Errorf("vm has no control channel")
	}
	conn, err := net.DialTimeout("unix", inst.Control, 3*time.Second)
	if err != nil {
		return fmt.Errorf("qmp dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	dec := json.NewDecoder(conn)
	var greeting map[string]json.RawMessage
	if err := dec.Decode(&greeting); err != nil {
		return fmt.Errorf("qmp greeting: %w", err)
	}
	var ack json.RawMessage
	for _, cmd := range []string{"qmp_capabilities", command} {
		if _, err := io.WriteString(conn, fmt.Sprintf("{\"execute\":%q}\n", cmd)); err != nil {
			return fmt.Errorf("qmp %s: %w", cmd, err)
		}
		if err := dec.Decode(&ack); err != nil {
			return fmt.Errorf("qmp %s: %w", cmd, err)
		}
		var out struct {
			Error *struct {
				Desc string `json:"desc"`
			} `json:"error"`
		}
		if err := json.Unmarshal(ack, &out); err == nil && out.Error != nil {
			return fmt.Errorf("qmp %s: %s", cmd, out.Error.Desc)
		}
	}
	return nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
