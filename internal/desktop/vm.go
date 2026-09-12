package desktop

import (
	"os/exec"
	"sync"
	"time"
)

// State is the lifecycle state of a guest VM.
type State string

const (
	StateBooting State = "booting"
	StateReady   State = "ready"
	StateBusy    State = "busy"
	StateDead    State = "dead"
)

// VM is a single guest microVM managed by the orchestrator.
type VM struct {
	ID      string
	State   State
	GuestIP string
	Started time.Time
	Volume  string

	cmd     *exec.Cmd
	dir     string
	ready   chan struct{}
	readyMu sync.Once
	mu      sync.Mutex
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

// SetState updates the VM lifecycle state.
func (v *VM) SetState(s State) { v.setState(s) }

// IP returns the guest IP reported at readiness (empty until then).
func (v *VM) IP() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.GuestIP
}

// Info is a serialisable snapshot of a VM (no locks, safe to copy).
type Info struct {
	ID      string    `json:"id"`
	State   State     `json:"state"`
	GuestIP string    `json:"guest_ip,omitempty"`
	Started time.Time `json:"started"`
	Volume  string    `json:"volume,omitempty"`
}

// Info returns a copy safe for serialisation.
func (v *VM) Info() Info {
	v.mu.Lock()
	defer v.mu.Unlock()
	return Info{
		ID:      v.ID,
		State:   v.State,
		GuestIP: v.GuestIP,
		Started: v.Started,
		Volume:  v.Volume,
	}
}
