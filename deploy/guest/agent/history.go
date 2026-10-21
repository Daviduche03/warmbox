// Command history: a bounded, on-disk log of what the host ran in the guest.
// It lives on the writable root so it survives a guest restart, and it is
// best-effort throughout — a failure to record must never fail the command.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// The history is a convenience, not an audit log: keep the file small
	// enough that it never becomes a problem for the guest's disk.
	maxHistoryEntries = 200
	defaultHistoryLen = 50
	// Output is a preview, not the full transcript.
	maxHistoryOutput = 8 << 10
)

type historyEntry struct {
	ID      string `json:"id"`
	Cmd     string `json:"cmd"`
	Cwd     string `json:"cwd,omitempty"`
	Started string `json:"started"`
	Ended   string `json:"ended"`
	Exit    int    `json:"exit"`
	Output  string `json:"output,omitempty"`
}

var (
	historyMu   sync.Mutex
	historyPath = pickHistoryPath()
	historyLog  []historyEntry
)

// pickHistoryPath prefers the persistent root; a read-only or full root falls
// back to tmpfs, which at least survives until the next boot.
func pickHistoryPath() string {
	dir := "/var/lib/warmbox-agent"
	if err := os.MkdirAll(dir, 0o755); err == nil {
		p := filepath.Join(dir, "history.json")
		// Prove the file itself can be created, not just the directory.
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			_ = f.Close()
			return p
		}
	}
	dir = "/tmp/warmbox-agent"
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "history.json")
}

// loadHistory reads the stored history once, at startup.
func loadHistory() {
	historyMu.Lock()
	defer historyMu.Unlock()
	b, err := os.ReadFile(historyPath)
	if err != nil {
		return
	}
	var entries []historyEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return
	}
	if len(entries) > maxHistoryEntries {
		entries = entries[len(entries)-maxHistoryEntries:]
	}
	historyLog = entries
}

// recordHistory appends an entry and rewrites the file. Every failure here is
// swallowed: the command already ran, and losing the record is far better
// than failing it.
func recordHistory(cmd, cwd string, started, ended time.Time, exit int, output string) {
	historyMu.Lock()
	defer historyMu.Unlock()

	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return
	}
	e := historyEntry{
		ID:      hex.EncodeToString(b),
		Cmd:     cmd,
		Cwd:     cwd,
		Started: started.UTC().Format(time.RFC3339Nano),
		Ended:   ended.UTC().Format(time.RFC3339Nano),
		Exit:    exit,
	}
	if len(output) > maxHistoryOutput {
		e.Output = output[:maxHistoryOutput] + "\n… (truncated)"
	} else {
		e.Output = output
	}
	historyLog = append(historyLog, e)
	if len(historyLog) > maxHistoryEntries {
		historyLog = historyLog[len(historyLog)-maxHistoryEntries:]
	}
	writeHistoryLocked()
}

// writeHistoryLocked rewrites the file atomically (temp file + rename) so a
// crash mid-write cannot leave a truncated file behind.
func writeHistoryLocked() {
	b, err := json.Marshal(historyLog)
	if err != nil {
		return
	}
	dir := filepath.Dir(historyPath)
	tmp, err := os.CreateTemp(dir, ".history-*.json")
	if err != nil {
		return
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return
	}
	if err := os.Rename(name, historyPath); err != nil {
		_ = os.Remove(name)
	}
}

// handleHistory serves the stored history, newest first.
func handleHistory(w http.ResponseWriter, r *http.Request) {
	limit := defaultHistoryLen
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxHistoryEntries {
		limit = maxHistoryEntries
	}

	historyMu.Lock()
	entries := make([]historyEntry, 0, len(historyLog))
	// Newest first: the host almost always wants the most recent commands.
	for i := len(historyLog) - 1; i >= 0 && len(entries) < limit; i-- {
		entries = append(entries, historyLog[i])
	}
	historyMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string][]historyEntry{"entries": entries})
}

// --- processes ---

type processInfo struct {
	PID   int    `json:"pid"`
	PPID  int    `json:"ppid"`
	User  string `json:"user"`
	ETime string `json:"etime"`
	Args  string `json:"args"`
}

// handleProcesses lists the guest's processes. The ps output is parsed
// leniently: an unexpected line yields the raw text rather than an error,
// because a process list is only ever informational.
func handleProcesses(w http.ResponseWriter, _ *http.Request) {
	// procps understands -eo; busybox ps does not, so fall back to its own
	// default listing rather than reporting no processes at all.
	out, err := runTool("ps", "-eo", "pid,ppid,user,etime,args")
	if err != nil {
		out, err = runTool("ps")
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	procs := make([]processInfo, 0, len(lines))
	raw := make([]string, 0)
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i == 0 && strings.Contains(strings.ToLower(line), "pid") {
			continue // header
		}
		p, ok := parseProcessLine(line)
		if !ok {
			raw = append(raw, line)
			continue
		}
		procs = append(procs, p)
	}
	resp := map[string]any{"processes": procs}
	if len(raw) > 0 {
		resp["raw"] = raw
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// parseProcessLine splits "pid ppid user etime args", where args is the rest
// of the line and may contain spaces.
func parseProcessLine(line string) (processInfo, bool) {
	f := strings.Fields(line)
	if len(f) < 5 {
		return processInfo{}, false
	}
	pid, err1 := strconv.Atoi(f[0])
	ppid, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil {
		return processInfo{}, false
	}
	return processInfo{
		PID:   pid,
		PPID:  ppid,
		User:  f[2],
		ETime: f[3],
		Args:  strings.Join(f[4:], " "),
	}, true
}

// describeExec renders a command for the history: the shell form verbatim, the
// argv form joined for readability.
func describeExec(cmd string, argv []string) string {
	if len(argv) > 0 {
		return strings.Join(argv, " ")
	}
	return cmd
}

// execCwd reports the working directory a command ran in: the one it asked
// for, or the agent's own when it did not ask for one.
func execCwd(dir string) string {
	if dir != "" {
		return dir
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}
