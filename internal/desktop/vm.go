package desktop

import (
	"net"
	"os/exec"
	"strconv"
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
	StateDead   State = "dead"
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

	forwards map[int]string
	cmd      *exec.Cmd
	// inst is the launched process's handle, including the backend control
	// endpoint used to pause and resume it.
	inst *Instance
	dir  string
	// pausedFrom remembers the state to restore when a paused VM resumes.
	pausedFrom State
	ready      chan struct{}
	readyMu    sync.Once
	mu         sync.Mutex
}

// Ready returns a channel closed once the guest reports readiness.
func (v *VM) Ready() <-chan struct{} { return v.ready }

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
	}
}
