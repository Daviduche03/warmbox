package desktop

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Reap kills hypervisor processes left behind by an earlier daemon and clears
// their VM directories, returning how many it cleaned up.
//
// The manager destroys its VMs on a clean shutdown, so anything still running
// when a new daemon starts belongs to a crashed or killed run. Left alone it
// would keep holding RAM, its control socket, and — worst of all — its volume
// images attached read-write while the new daemon boots fresh VMs.
func Reap(cfg *Config, log io.Writer) int {
	root := filepath.Join(cfg.WorkDir, "vms")
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	reaped := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if killLeftover(dir, log) {
			reaped++
		}
		_ = os.RemoveAll(dir)
	}
	// A restart is a clean slate (running VMs are killed above, and the new
	// manager starts with no records), so memory checkpoints are unrestorable
	// by definition: their VMs are gone. Hibernated desktops do not survive a
	// restart either — same rule as running ones. Clear them rather than
	// leaking RAM-sized files with live guest secrets inside.
	if snaps, err := os.ReadDir(filepath.Join(cfg.WorkDir, "snapshots")); err == nil {
		for _, e := range snaps {
			_ = os.RemoveAll(filepath.Join(cfg.WorkDir, "snapshots", e.Name()))
		}
		if len(snaps) > 0 {
			fmt.Fprintf(log, "warmbox: cleared %d leftover snapshot(s) from a previous run\n", len(snaps))
		}
	}
	return reaped
}

// killLeftover stops the hypervisor recorded in dir/vm.pid. It verifies the pid
// first: pids get reused, and killing an unrelated process would be far worse
// than leaking one.
func killLeftover(dir string, log io.Writer) bool {
	raw, err := os.ReadFile(filepath.Join(dir, "vm.pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		return false
	}
	if !isVMBackend(pid, dir) {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = proc.Signal(syscall.SIGTERM)
	for i := 0; i < 30; i++ {
		if !alive(pid) {
			fmt.Fprintf(log, "desktop: reaped leftover vm %s (pid %d)\n", filepath.Base(dir), pid)
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = proc.Signal(syscall.SIGKILL)
	for i := 0; i < 20 && alive(pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Fprintf(log, "desktop: killed leftover vm %s (pid %d, ignored SIGTERM)\n", filepath.Base(dir), pid)
	return true
}

// isVMBackend reports whether pid is the hypervisor for this VM: its command
// line names a backend binary and this VM's directory (every launch embeds the
// directory in its --pidfile / --device paths). `ps` keeps the check portable
// across macOS and Linux.
func isVMBackend(pid int, dir string) bool {
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	cmdline := string(out)
	if !strings.Contains(cmdline, dir) {
		return false
	}
	return strings.Contains(cmdline, "vfkit") || strings.Contains(cmdline, "qemu-system")
}

// alive reports whether a pid still exists (signal 0 probes without delivering).
func alive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
