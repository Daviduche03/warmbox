package desktop

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Memory checkpoints: a running VM's RAM and device state, saved to disk with
// QMP migrate-to-file and restored with QEMU -incoming. Booting headless takes
// ~9s on KVM; restoring a checkpoint takes ~1s with the guest none the wiser
// (its uptime continues). Hibernate is a checkpoint plus a stopped process:
// no RAM, no CPU, resumable on demand.
//
// A checkpoint is crash-consistent, not application-consistent: an attached
// volume keeps mutating under the guest, so restoring RAM from T0 against a
// disk from T1 is exactly like power-loss recovery — the filesystem journals
// back, app state may not match. Documented, not prevented.

const (
	// hibernateSlot is the implicit checkpoint a hibernate writes. Dot-named
	// so it never appears in user snapshots and can never collide with one.
	hibernateSlot = ".hibernate"
	// maxCheckpointsPerVM bounds disk use: checkpoints are RAM-sized. The
	// hibernate slot is exempt (it belongs to hibernate/wake, not to the user).
	maxCheckpointsPerVM = 5
	// migrateTimeout bounds a live snapshot; 768MB writes in seconds locally.
	migrateTimeout = 5 * time.Minute
	// agentReadyTimeout bounds the post-restore health check. A restored guest
	// serves the moment QEMU resumes, so this is generosity, not expectation.
	agentReadyTimeout = 30 * time.Second
)

// SnapshotInfo describes one memory checkpoint on disk.
type SnapshotInfo struct {
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	Size    int64     `json:"size"`
	Image   string    `json:"image"`
}

var validSnapshotName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func validateSnapshotName(name string) error {
	if !validSnapshotName.MatchString(name) {
		return fmt.Errorf("bad snapshot name %q (letters, digits, _ . -, up to 64 chars, must start alphanumerically)", name)
	}
	return nil
}

func defaultCheckpointName() string {
	return "ck-" + time.Now().UTC().Format("20060102-150405")
}

func (m *Manager) snapshotFile(id, name string) string {
	return filepath.Join(m.cfg.SnapshotDir(id), name+".mem")
}

func (m *Manager) snapshotMetaFile(id, name string) string {
	return filepath.Join(m.cfg.SnapshotDir(id), name+".json")
}

// Snapshots lists a VM's named checkpoints, oldest first. The hibernate slot
// is internal (dot-named) and never listed.
func (m *Manager) Snapshots(id string) ([]SnapshotInfo, error) {
	dir := m.cfg.SnapshotDir(id)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []SnapshotInfo{}, nil
		}
		return nil, err
	}
	var out []SnapshotInfo
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".mem") {
			continue
		}
		info := SnapshotInfo{Name: strings.TrimSuffix(name, ".mem")}
		if meta, err := os.ReadFile(filepath.Join(dir, info.Name+".json")); err == nil {
			_ = json.Unmarshal(meta, &info)
		} else {
			// Meta missing (hand-placed file?): report what the file says.
			if st, err := os.Stat(filepath.Join(dir, name)); err == nil {
				info.Created = st.ModTime()
				info.Size = st.Size()
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	if out == nil {
		out = []SnapshotInfo{}
	}
	return out, nil
}

// DeleteSnapshot removes one named checkpoint. The hibernate slot is not
// deletable through here (its name fails validation); hibernate overwrites it.
func (m *Manager) DeleteSnapshot(id, name string) error {
	if err := validateSnapshotName(name); err != nil {
		return err
	}
	if _, err := os.Stat(m.snapshotFile(id, name)); err != nil {
		return fmt.Errorf("no snapshot %q for vm %s", name, id)
	}
	_ = os.Remove(m.snapshotMetaFile(id, name))
	if err := os.Remove(m.snapshotFile(id, name)); err != nil {
		return err
	}
	fmt.Fprintf(m.log, "desktop: deleted snapshot %s/%s\n", id, name)
	return nil
}

// snapshotCount counts user checkpoints (the hibernate slot excluded).
func (m *Manager) snapshotCount(id string) (int, error) {
	snaps, err := m.Snapshots(id)
	if err != nil {
		return 0, err
	}
	return len(snaps), nil
}

// Checkpoint live-snapshots a running VM's memory to disk. The guest keeps
// running throughout; the returned info is only valid once this returns.
func (m *Manager) Checkpoint(id, name string) (SnapshotInfo, error) {
	vm, ok := m.Get(id)
	if !ok {
		return SnapshotInfo{}, fmt.Errorf("unknown vm %s", id)
	}
	switch st := vm.Info().State; st {
	case StateReady, StateBusy, StatePaused:
		// Live memory to snapshot (paused is ideal: nothing moves under it).
	default:
		return SnapshotInfo{}, fmt.Errorf("cannot checkpoint vm %s in state %s", id, st)
	}
	if !m.backend.Capabilities().Snapshot {
		return SnapshotInfo{}, fmt.Errorf("backend %s cannot checkpoint vms", m.backend.Name())
	}
	if name == "" {
		name = defaultCheckpointName()
	}
	if err := validateSnapshotName(name); err != nil {
		return SnapshotInfo{}, err
	}
	if n, err := m.snapshotCount(id); err != nil {
		return SnapshotInfo{}, err
	} else if n >= maxCheckpointsPerVM {
		return SnapshotInfo{}, fmt.Errorf("vm %s already holds %d checkpoints: delete one first", id, n)
	}
	dir := m.cfg.SnapshotDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return SnapshotInfo{}, fmt.Errorf("creating snapshot dir: %w", err)
	}
	path := m.snapshotFile(id, name)
	// Pre-create 0600: the file holds live RAM (guest secrets included) and
	// `cat >` below would otherwise inherit the umask.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return SnapshotInfo{}, fmt.Errorf("creating snapshot file: %w", err)
	}
	if err := qmpMigrateToFile(vm.inst, path, m.log, migrateTimeout); err != nil {
		_ = os.Remove(path)
		return SnapshotInfo{}, fmt.Errorf("checkpoint vm %s: %w", id, err)
	}
	st, err := os.Stat(path)
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("checkpoint vm %s: %w", id, err)
	}
	info := SnapshotInfo{
		Name:    name,
		Created: time.Now(),
		Size:    st.Size(),
		Image:   vm.image,
	}
	meta, _ := json.Marshal(info)
	_ = os.WriteFile(m.snapshotMetaFile(id, name), meta, 0o600)
	fmt.Fprintf(m.log, "desktop: checkpointed vm %s as %q (%d bytes)\n", id, name, st.Size())
	return info, nil
}

// hibernateCheckpoint is Checkpoint without the quota or the state rules: the
// hibernate slot belongs to hibernate/wake, overwrites freely.
func (m *Manager) hibernateCheckpoint(vm *VM) error {
	dir := m.cfg.SnapshotDir(vm.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating snapshot dir: %w", err)
	}
	path := m.snapshotFile(vm.ID, hibernateSlot)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return fmt.Errorf("creating snapshot file: %w", err)
	}
	if err := qmpMigrateToFile(vm.inst, path, m.log, migrateTimeout); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("hibernate vm %s: %w", vm.ID, err)
	}
	return nil
}

// Hibernate checkpoints a VM and then stops its process: no RAM, no CPU, with
// the desktop record, its VM directory, and the checkpoint all kept for Wake.
// Volumes stay attached (nothing is committed — that happens on Destroy).
func (m *Manager) Hibernate(id string) error {
	vm, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("unknown vm %s", id)
	}
	switch st := vm.Info().State; st {
	case StateReady, StateBusy, StatePaused:
		// Something running to freeze.
	case StateHibernated:
		return nil
	default:
		return fmt.Errorf("cannot hibernate vm %s in state %s", id, st)
	}
	if !m.backend.Capabilities().Snapshot {
		return fmt.Errorf("backend %s cannot hibernate vms", m.backend.Name())
	}
	if err := m.hibernateCheckpoint(vm); err != nil {
		return err
	}
	m.killVM(vm)
	vm.setState(StateHibernated)
	fmt.Fprintf(m.log, "desktop: hibernated vm %s (process gone, memory on disk)\n", id)
	return nil
}

// Wake restores a hibernated VM from its hibernate checkpoint.
func (m *Manager) Wake(id string) (*VM, error) {
	vm, ok := m.Get(id)
	if !ok {
		return nil, fmt.Errorf("unknown vm %s", id)
	}
	if st := vm.Info().State; st != StateHibernated {
		return nil, fmt.Errorf("vm %s is not hibernated (state %s)", id, st)
	}
	return m.relaunchFromSnapshot(vm, m.snapshotFile(id, hibernateSlot), "woke")
}

// Restore relaunches a running VM from one of its named checkpoints, in
// place: same id, same directory, same workspace and volume. Empty name
// restores the most recent checkpoint.
func (m *Manager) Restore(id, name string) (*VM, error) {
	vm, ok := m.Get(id)
	if !ok {
		return nil, fmt.Errorf("unknown vm %s", id)
	}
	switch st := vm.Info().State; st {
	case StateReady, StateBusy, StatePaused:
		// A live machine to roll back.
	default:
		return nil, fmt.Errorf("cannot restore vm %s in state %s (hibernated vms wake instead)", id, st)
	}
	if name == "" {
		snaps, err := m.Snapshots(id)
		if err != nil {
			return nil, err
		}
		if len(snaps) == 0 {
			return nil, fmt.Errorf("vm %s has no checkpoints", id)
		}
		name = snaps[len(snaps)-1].Name
	}
	return m.relaunchFromSnapshot(vm, m.snapshotFile(id, name), "restored")
}

// relaunchFromSnapshot kills the VM's process (if any) and boots the identical
// machine from a memory checkpoint instead of from the image: same id, same
// directory, same workspace, volume, VNC password and guest IP (it is the same
// guest — its uptime continues). Fresh host ports are allocated; anything
// holding the old VNC/agent addresses reconnects.
func (m *Manager) relaunchFromSnapshot(vm *VM, snapPath, verb string) (*VM, error) {
	id := vm.ID
	if !m.backend.Capabilities().Snapshot {
		return nil, fmt.Errorf("backend %s cannot restore vms", m.backend.Name())
	}
	if _, err := os.Stat(snapPath); err != nil {
		return nil, fmt.Errorf("no checkpoint for vm %s (hibernate it first)", id)
	}
	old := vm.Info()
	vm.setState(StateBooting)
	tKill := time.Now()
	m.killVM(vm)
	// A dead hypervisor leaves its control socket and pidfile behind; the new
	// process must bind the socket path itself.
	if vm.inst != nil {
		_ = os.Remove(vm.inst.Control)
	}
	_ = os.Remove(filepath.Join(vm.dir, "vm.pid"))
	killMs := time.Since(tKill).Milliseconds()

	spec := vm.launch
	spec.Incoming = "exec:cat " + snapPath
	tLaunch := time.Now()
	inst, err := m.backend.Launch(spec)
	if err != nil {
		return nil, fmt.Errorf("relaunching vm %s: %w", id, err)
	}
	cmd := inst.Cmd
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", m.backend.Name(), err)
	}
	launchMs := time.Since(tLaunch).Milliseconds()
	tQmp := time.Now()
	if err := waitQMPRunning(inst.Control, 60*time.Second); err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("vm %s restored but never ran: %w", id, err)
	}
	qmpMs := time.Since(tQmp).Milliseconds()
	newVM := &VM{
		ID:        id,
		State:     StateBooting,
		Started:   old.Started,
		Volume:    old.Volume,
		Workspace: old.Workspace,
		Headless:  old.Headless,
		vncPass:   vm.VNCPassword(),
		forwards:  inst.Forwards,
		cmd:       cmd,
		inst:      inst,
		dir:       vm.dir,
		launch:    vm.launch,
		image:     vm.image,
		ready:     make(chan struct{}),
		done:      make(chan struct{}),
	}
	m.mu.Lock()
	m.vms[id] = newVM
	m.mu.Unlock()
	m.watch(id, newVM)
	fmt.Fprintf(m.log, "desktop: relaunching vm %s from checkpoint via %s (pid %d)\n",
		id, m.backend.Name(), cmd.Process.Pid)

	// The guest serves the moment QEMU resumes; still, trust the agent, not
	// the process table: only mark ready once it answers.
	addr := inst.Forwards[7077]
	if addr == "" && old.GuestIP != "" {
		addr = old.GuestIP + ":7077"
	}
	tAgent := time.Now()
	if addr != "" {
		if err := waitAgentReady(addr, agentReadyTimeout); err != nil {
			_ = cmd.Process.Kill()
			newVM.setState(StateDead)
			return nil, fmt.Errorf("vm %s restored but the guest never answered: %w", id, err)
		}
	}
	agentMs := time.Since(tAgent).Milliseconds()
	newVM.markReady(old.GuestIP)
	if newVM.Info().State == StateBooting {
		// The guest answered but never reported an IP (it only reports once,
		// at boot): it is up, so say so rather than sitting in booting.
		newVM.setState(StateReady)
	}
	fmt.Fprintf(m.log, "desktop: %s vm %s from checkpoint (kill=%dms launch=%dms qmp=%dms agent=%dms)\n",
		verb, id, killMs, launchMs, qmpMs, agentMs)
	return newVM, nil
}

// waitAgentReady polls the guest agent's health endpoint until it answers.
// The interval is tight on purpose: a restored guest serves the moment QEMU
// resumes, and a 500ms poll would add up to half a second of pure waiting.
func waitAgentReady(addr string, timeout time.Duration) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for {
		resp, err := client.Get("http://" + addr + "/healthz")
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
			resp.Body.Close()
			if resp.StatusCode == 200 && strings.TrimSpace(string(body)) == "ok" {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("agent at %s did not answer within %s", addr, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitQMPRunning waits for a fresh QEMU (typically mid -incoming restore) to
// reach running state, nudging it with cont if it sits paused.
func waitQMPRunning(control string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(control); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("control socket never appeared")
		}
		time.Sleep(50 * time.Millisecond)
	}
	probe := &Instance{Control: control}
	for {
		raw, err := qmpCommand(probe, "query-status", nil)
		if err == nil {
			var st struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(raw, &st) == nil {
				switch st.Status {
				case "running":
					return nil
				case "paused":
					_, _ = qmpCommand(probe, "cont", nil)
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("vm never reached running")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
