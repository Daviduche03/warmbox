package desktop

import (
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// State is the lifecycle state of a guest VM.
type State string

const (
	StateBooting State = "booting"
	StateReady   State = "ready"
	// StateBusy is reserved for long-running in-guest operations. A lease does
	// not set it — VM.Workspace marks which workspace holds a desktop.
	StateBusy State = "busy"
	// StatePaused is a frozen VM: its vCPUs are stopped but its memory is
	// still held by the host process (pause saves CPU, not RAM).
	StatePaused State = "paused"
	// StateHibernated is a VM whose process is gone but whose memory lives on
	// in a checkpoint file: no RAM, no CPU, resumable with Wake. Hibernate is
	// the state-layer answer to the pool's RAM cost.
	StateHibernated State = "hibernated"
	StateDead       State = "dead"
)

// VM is a single guest microVM managed by the orchestrator.
type VM struct {
	ID      string
	State   State
	GuestIP string
	Started time.Time
	Volume  string
	// Workspace tags which workspace leased this VM. Empty means unleased
	// warm-pool capacity: the daemon's own spare, reported in the pool stats
	// and deliberately kept out of the desktop list.
	Workspace string
	// Headless is true when the image this VM booted declares no screen (see
	// ImageMeta.Headless): there is nothing behind /d/ to stream.
	Headless bool

	// vncPass is the password Xvnc in this guest was started with. The guest
	// generates it and reports it over the readiness callback; empty means the
	// guest serves without auth (an image built before the flag existed).
	vncPass string

	forwards map[int]string
	cmd      *exec.Cmd
	// inst is the launched process's handle, including the backend control
	// endpoint used to pause and resume it.
	inst *Instance
	dir  string
	// launch is the spec this VM booted from, kept so a restore can relaunch
	// the identical machine (same image, disks, shares, cmdline) with only an
	// -incoming URI added. image names the guest image for checkpoint metadata.
	launch LaunchSpec
	image  string
	// pausedFrom remembers the state to restore when a paused VM resumes.
	pausedFrom State
	ready      chan struct{}
	readyMu    sync.Once
	// done is closed when the VM process exits, so a caller waiting for
	// readiness stops waiting when there is nothing left to wait for. exitErr
	// is why it ended (guarded by mu).
	done    chan struct{}
	doneMu  sync.Once
	exitErr error
	mu      sync.Mutex
}

// Ready returns a channel closed once the guest reports readiness.
func (v *VM) Ready() <-chan struct{} { return v.ready }

// Done returns a channel closed once the VM process has exited.
func (v *VM) Done() <-chan struct{} { return v.done }

// wasReady reports whether the guest ever reported readiness, even if the VM
// has since exited.
func (v *VM) wasReady() bool {
	select {
	case <-v.ready:
		return true
	default:
		return false
	}
}

// markExited records how the VM process ended and wakes anyone waiting on it.
func (v *VM) markExited(err error) {
	v.mu.Lock()
	v.exitErr = err
	v.mu.Unlock()
	v.doneMu.Do(func() { close(v.done) })
}

// SetVNCPassword records the password the guest generated for its VNC server.
// It arrives once, with readiness.
func (v *VM) SetVNCPassword(pass string) {
	v.mu.Lock()
	v.vncPass = pass
	v.mu.Unlock()
}

// VNCPassword is what a VNC client must present, or "" when the guest serves
// without auth.
func (v *VM) VNCPassword() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.vncPass
}

// ExitError is why the VM process ended, or nil if it ended cleanly or has not
// ended yet.
func (v *VM) ExitError() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.exitErr
}

// exitDetail explains a boot that died: what the hypervisor said, and what the
// guest last managed to print.
//
// "exit status 1" is not a diagnosis. A hypervisor that refuses to start says
// why on its own stderr; a guest that panics writes it to the serial console —
// and with -no-reboot on QEMU, a guest whose init dies makes QEMU exit 1 while
// saying nothing at all. The VM directory holding that console log is removed
// when the failed boot is cleaned up, so if it is not quoted here it is gone.
func (v *VM) exitDetail() string {
	var parts []string
	if v.inst != nil && v.inst.Stderr != nil {
		if s := lastLines(v.inst.Stderr.String(), 2); s != "" {
			parts = append(parts, s)
		}
	}
	if s := tailOfFile(filepath.Join(v.dir, "console.log"), 4<<10, 3); s != "" {
		parts = append(parts, "guest console: "+s)
	}
	if len(parts) == 0 {
		return ""
	}
	quoted := strings.Join(parts, "; ")
	if len(quoted) > 500 {
		quoted = quoted[:500] + "…"
	}
	return ": " + quoted
}

// lastLines is the last n non-empty lines of text, joined with ": ".
func lastLines(text string, n int) string {
	if text == "" {
		return ""
	}
	var tail []string
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0 && len(tail) < n; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			tail = append([]string{s}, tail...)
		}
	}
	return strings.Join(tail, ": ")
}

// tailOfFile is the last few non-empty lines of a file, reading only its end so
// that a chatty boot does not cost the whole log. Missing files are not an
// error: most VMs have no console log worth quoting.
func tailOfFile(path string, maxBytes int64, lines int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	if offset := st.Size() - maxBytes; offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return ""
		}
	}
	b, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return ""
	}
	return lastLines(string(b), lines)
}

// markReady records the guest IP and signals readiness (idempotent).
func (v *VM) markReady(ip string) {
	v.mu.Lock()
	if v.State == StateBooting {
		v.GuestIP = ip
		v.State = StateReady
	}
	v.mu.Unlock()
	v.readyMu.Do(func() { close(v.ready) })
}

func (v *VM) setState(s State) {
	v.mu.Lock()
	v.State = s
	v.mu.Unlock()
}

// markPaused records a frozen VM, remembering the state to restore on resume.
func (v *VM) markPaused() {
	v.mu.Lock()
	if v.State != StatePaused {
		v.pausedFrom = v.State
		v.State = StatePaused
	}
	v.mu.Unlock()
}

// markResumed returns a paused VM to the state it held before it was paused.
func (v *VM) markResumed() {
	v.mu.Lock()
	if v.pausedFrom != "" && v.pausedFrom != StatePaused {
		v.State = v.pausedFrom
	} else {
		v.State = StateReady
	}
	v.pausedFrom = ""
	v.mu.Unlock()
}

// SetState updates the VM lifecycle state.
func (v *VM) SetState(s State) { v.setState(s) }

// SetWorkspace tags which workspace leased this VM. Called once on hand-out;
// the pool boots VMs untagged.
func (v *VM) SetWorkspace(ws string) {
	v.mu.Lock()
	v.Workspace = ws
	v.mu.Unlock()
}

// IP returns the guest IP reported at readiness (empty until then).
func (v *VM) IP() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.GuestIP
}

// Target returns the address to dial for a guest port: a backend-provided
// forwarding address (e.g. a QEMU hostfwd port), or, when the host can reach the
// guest directly (vfkit NAT), the guest IP + port. Empty until the VM is ready.
func (v *VM) Target(guestPort int) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if addr, ok := v.forwards[guestPort]; ok {
		return addr
	}
	if v.GuestIP == "" {
		return ""
	}
	return net.JoinHostPort(v.GuestIP, strconv.Itoa(guestPort))
}

// Info is a serialisable snapshot of a VM (no locks, safe to copy).
type Info struct {
	ID      string    `json:"id"`
	State   State     `json:"state"`
	GuestIP string    `json:"guest_ip,omitempty"`
	Started time.Time `json:"started"`
	Volume  string    `json:"volume,omitempty"`
	// Workspace tags the leasing workspace; empty means unleased pool capacity.
	Workspace string `json:"workspace,omitempty"`
	// Headless reports an image with no screen: there is no /d/ to open.
	Headless bool `json:"headless,omitempty"`
	// Allow and Deny are the desktop's egress policy (set by the API).
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// Info returns a copy safe for serialisation.
func (v *VM) Info() Info {
	v.mu.Lock()
	defer v.mu.Unlock()
	return Info{
		ID:        v.ID,
		State:     v.State,
		GuestIP:   v.GuestIP,
		Started:   v.Started,
		Volume:    v.Volume,
		Workspace: v.Workspace,
		Headless:  v.Headless,
	}
}
