package desktop

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateSnapshotName(t *testing.T) {
	ok := []string{"a", "ck-20260101-120000", "before-upgrade", "a.b-c_d", strings.Repeat("x", 64)}
	for _, n := range ok {
		if err := validateSnapshotName(n); err != nil {
			t.Errorf("name %q rejected: %v", n, err)
		}
	}
	bad := []string{"", ".hibernate", "-lead", "a/b", "with space", strings.Repeat("x", 65)}
	for _, n := range bad {
		if err := validateSnapshotName(n); err == nil {
			t.Errorf("name %q accepted", n)
		}
	}
}

func TestDefaultCheckpointName(t *testing.T) {
	n := defaultCheckpointName()
	if err := validateSnapshotName(n); err != nil {
		t.Fatalf("default name %q invalid: %v", n, err)
	}
	if !strings.HasPrefix(n, "ck-") {
		t.Fatalf("default name %q has no ck- prefix", n)
	}
}

// testManager returns a Manager rooted at a temp workdir (no backend needed
// for store-level operations).
func testSnapManager(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	m := &Manager{cfg: &Config{WorkDir: dir}, log: io.Discard}
	return m, dir
}

func placeSnapshot(t *testing.T, m *Manager, id, name string, size int) {
	t.Helper()
	dir := m.cfg.SnapshotDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".mem"), make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(SnapshotInfo{Name: name, Created: time.Now(), Size: int64(size), Image: "headless"})
	if err := os.WriteFile(filepath.Join(dir, name+".json"), meta, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotsListAndDelete(t *testing.T) {
	m, _ := testSnapManager(t)
	// Empty (missing dir) lists clean.
	got, err := m.Snapshots("nobody")
	if err != nil || len(got) != 0 {
		t.Fatalf("empty list = %v, %v", got, err)
	}
	placeSnapshot(t, m, "vm1", "before-upgrade", 100)
	placeSnapshot(t, m, "vm1", "after-upgrade", 200)
	// The hibernate slot and stray files never list.
	os.WriteFile(filepath.Join(m.cfg.SnapshotDir("vm1"), ".hibernate.mem"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(m.cfg.SnapshotDir("vm1"), "notes.txt"), []byte("x"), 0o600)

	got, err = m.Snapshots("vm1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "before-upgrade" || got[1].Name != "after-upgrade" {
		t.Fatalf("list = %+v", got)
	}
	if got[0].Size != 100 || got[0].Image != "headless" {
		t.Fatalf("meta not read back: %+v", got[0])
	}
	if err := m.DeleteSnapshot("vm1", "before-upgrade"); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Snapshots("vm1"); len(got) != 1 {
		t.Fatalf("after delete = %+v", got)
	}
	if err := m.DeleteSnapshot("vm1", "missing"); err == nil {
		t.Fatal("deleting a missing snapshot succeeded")
	}
	if err := m.DeleteSnapshot("vm1", ".hibernate"); err == nil {
		t.Fatal("hibernate slot deletable")
	}
}

// fakeQMP serves a scripted QMP conversation: greeting, then one reply per
// expected command. Replies may be values (returned) or "EVENT:<name>" (an
// async event frame, to prove the client skips them).
type fakeQMP struct {
	t      *testing.T
	cmds   []string
	reps   []string
	seenUR string
}

func (f *fakeQMP) serve(l net.Listener) {
	defer l.Close()
	at := 0
	// The client dials fresh per command, so the script plays out across
	// connections: greet each one, serve whatever it asks until it hangs up.
	for at < len(f.cmds) {
		c, err := l.Accept()
		if err != nil {
			return
		}
		enc := json.NewEncoder(c)
		dec := json.NewDecoder(c)
		enc.Encode(map[string]any{"QMP": map[string]any{"version": map[string]any{}}})
		for at < len(f.cmds) {
			var msg map[string]any
			if err := dec.Decode(&msg); err != nil {
				break // client went away; continue the script on its next dial
			}
			want := f.cmds[at]
			if msg["execute"] != want {
				f.t.Errorf("fakeqmp cmd %d = %v, want %s", at, msg["execute"], want)
			}
			if want == "migrate" {
				if args, ok := msg["arguments"].(map[string]any); ok {
					f.seenUR, _ = args["uri"].(string)
				}
			}
			rep := f.reps[at]
			at++
			if strings.HasPrefix(rep, "EVENT:") {
				enc.Encode(map[string]any{"event": strings.TrimPrefix(rep, "EVENT:")})
				enc.Encode(map[string]any{"return": map[string]any{}})
				continue
			}
			var v any
			if err := json.Unmarshal([]byte(rep), &v); err != nil {
				f.t.Errorf("fakeqmp bad reply fixture: %v", err)
				c.Close()
				return
			}
			enc.Encode(map[string]any{"return": v})
		}
		c.Close()
	}
}

func runFakeQMP(t *testing.T, f *fakeQMP) (sock string, done chan struct{}) {
	t.Helper()
	sock = filepath.Join(t.TempDir(), "qmp.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	done = make(chan struct{})
	go func() { f.serve(l); close(done) }()
	return sock, done
}

func TestQMPMigrateToFile(t *testing.T) {
	f := &fakeQMP{t: t,
		cmds: []string{"qmp_capabilities", "migrate", "qmp_capabilities", "query-migrate", "qmp_capabilities", "query-migrate"},
		reps: []string{`{}`, `{}`, `{}`, `EVENT:MIGRATION`, `{}`, `{"status":"completed"}`},
	}
	sock, done := runFakeQMP(t, f)
	defer func() { <-done }()
	inst := &Instance{Control: sock}
	if err := qmpMigrateToFile(inst, "/tmp/wb-test.snap", io.Discard, 10*time.Second); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if f.seenUR != "exec:cat > /tmp/wb-test.snap" {
		t.Fatalf("uri = %q", f.seenUR)
	}
}

func TestQMPMigrateFailed(t *testing.T) {
	f := &fakeQMP{t: t,
		cmds: []string{"qmp_capabilities", "migrate", "qmp_capabilities", "query-migrate"},
		reps: []string{`{}`, `{}`, `{}`, `{"status":"failed"}`},
	}
	sock, done := runFakeQMP(t, f)
	defer func() { <-done }()
	if err := qmpMigrateToFile(&Instance{Control: sock}, "/tmp/wb-test.snap", io.Discard, 10*time.Second); err == nil {
		t.Fatal("failed migration reported success")
	}
}

func TestQMPMigrateNoControl(t *testing.T) {
	if err := qmpMigrateToFile(&Instance{}, "/tmp/wb-test.snap", io.Discard, time.Second); err == nil {
		t.Fatal("migrate without a control channel succeeded")
	}
}

func TestLaunchIncomingArgs(t *testing.T) {
	b := &qemuBackend{accel: "tcg"}
	inst, err := b.Launch(LaunchSpec{
		ID: "abc", CPUs: 2, MemMiB: 768,
		Kernel: "vmlinux", Initrd: "initrd", Cmdline: "warmbox.id=abc",
		Console: "", ControlDir: t.TempDir(),
		Disks:   []Disk{{Path: "rootfs", ReadOnly: true}},
		Incoming: "exec:cat /snap/vm.mem",
	})
	if err != nil {
		t.Fatal(err)
	}
	args := inst.Cmd.Args
	found := false
	for i, a := range args {
		if a == "-incoming" && i+1 < len(args) && args[i+1] == "exec:cat /snap/vm.mem" {
			found = true
		}
	}
	if !found {
		t.Fatalf("-incoming missing from %v", args)
	}
	// Without Incoming the flag must be absent (normal boots unaffected).
	inst2, err := b.Launch(LaunchSpec{ID: "x", CPUs: 1, MemMiB: 256, ControlDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range inst2.Cmd.Args {
		if a == "-incoming" {
			t.Fatalf("-incoming leaked into a normal boot: %v", inst2.Cmd.Args)
		}
	}
}
