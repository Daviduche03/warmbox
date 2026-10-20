package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain keeps the history out of the real one: the run tests record
// commands, and they must not write to the developer's history file.
func TestMain(m *testing.M) {
	historyMu.Lock()
	historyPath = filepath.Join(os.TempDir(), "warmbox-agent-test-history.json")
	historyLog = nil
	historyMu.Unlock()
	os.Exit(m.Run())
}

// resetHistory points the history at a scratch file and empties it, so a
// test never writes to the real one.
func resetHistory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "history.json")
	historyMu.Lock()
	historyPath = p
	historyLog = nil
	historyMu.Unlock()
	return p
}

// historySnapshot copies the log so assertions do not race with a run that
// is still finishing.
func historySnapshot() []historyEntry {
	historyMu.Lock()
	defer historyMu.Unlock()
	return append([]historyEntry(nil), historyLog...)
}

// findEntry returns the newest entry with the given command.
func findEntry(entries []historyEntry, cmd string) (historyEntry, bool) {
	for _, e := range entries {
		if e.Cmd == cmd {
			return e, true
		}
	}
	return historyEntry{}, false
}

func TestHistoryRecordsAndLists(t *testing.T) {
	path := resetHistory(t)

	recordHistory("echo hi", "/tmp", time.Now(), time.Now(), 0, "hi")

	rr := httptest.NewRequest("GET", "/history", nil)
	w := httptest.NewRecorder()
	handleHistory(w, rr)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var out struct {
		Entries []historyEntry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(out.Entries))
	}
	e := out.Entries[0]
	if e.Cmd != "echo hi" || e.Exit != 0 {
		t.Fatalf("entry = %+v", e)
	}
	if e.ID == "" {
		t.Fatal("entry has no id")
	}
	if e.Cwd != "/tmp" {
		t.Fatalf("cwd = %q", e.Cwd)
	}
	if e.Started == "" || e.Ended == "" {
		t.Fatalf("entry has no timestamps: %+v", e)
	}

	// The file must exist and be readable back.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("history file: %v", err)
	}
}

func TestHistoryLimitAndCap(t *testing.T) {
	resetHistory(t)

	for i := 0; i < maxHistoryEntries+20; i++ {
		recordHistory("cmd", "", time.Now(), time.Now(), i, "")
	}
	log := historySnapshot()
	if len(log) != maxHistoryEntries {
		t.Fatalf("stored %d entries, want %d", len(log), maxHistoryEntries)
	}
	// The oldest entries were dropped, the newest survived.
	if log[0].Exit != 20 {
		t.Fatalf("oldest kept entry = %d, want 20", log[0].Exit)
	}

	rr := httptest.NewRequest("GET", "/history?limit=3", nil)
	w := httptest.NewRecorder()
	handleHistory(w, rr)
	var out struct {
		Entries []historyEntry `json:"entries"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Entries) != 3 {
		t.Fatalf("returned %d entries, want 3", len(out.Entries))
	}
	// Newest first.
	if out.Entries[0].Exit != maxHistoryEntries+19 {
		t.Fatalf("first entry exit = %d, want %d", out.Entries[0].Exit, maxHistoryEntries+19)
	}
}

func TestHistoryLimitIsCapped(t *testing.T) {
	resetHistory(t)
	rr := httptest.NewRequest("GET", "/history?limit=100000", nil)
	w := httptest.NewRecorder()
	handleHistory(w, rr)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHistoryOutputTruncated(t *testing.T) {
	resetHistory(t)

	big := strings.Repeat("x", maxHistoryOutput+100)
	recordHistory("cat big", "", time.Now(), time.Now(), 0, big)
	log := historySnapshot()
	e, ok := findEntry(log, "cat big")
	if !ok {
		t.Fatal("entry not recorded")
	}
	if len(e.Output) > maxHistoryOutput+32 {
		t.Fatalf("output not truncated: %d bytes", len(e.Output))
	}
	if !strings.HasSuffix(e.Output, "(truncated)") {
		t.Fatalf("output not marked truncated: %q", e.Output[len(e.Output)-20:])
	}
}

func TestHistoryWriteIsAtomic(t *testing.T) {
	path := resetHistory(t)
	recordHistory("one", "", time.Now(), time.Now(), 0, "")
	// No temp file must be left behind next to the history.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".history-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestParseProcessLine(t *testing.T) {
	p, ok := parseProcessLine("123 1 root 00:00:12 /bin/sh -c echo hi")
	if !ok {
		t.Fatal("line did not parse")
	}
	if p.PID != 123 || p.PPID != 1 || p.User != "root" {
		t.Fatalf("process = %+v", p)
	}
	if p.Args != "/bin/sh -c echo hi" {
		t.Fatalf("args = %q", p.Args)
	}
	if _, ok := parseProcessLine("garbage"); ok {
		t.Fatal("garbage should not parse")
	}
}

func TestInputRejectsBadRequests(t *testing.T) {
	cases := []string{
		`{"type":"nope"}`,
		`{"type":"text"}`,
		`{"type":"key"}`,
		`{"type":"click","x":1,"y":2,"button":"wheel"}`,
		`{"type":"move","x":-1,"y":2}`,
	}
	for _, body := range cases {
		rr := httptest.NewRequest("POST", "/input", strings.NewReader(body))
		w := httptest.NewRecorder()
		handleInput(w, rr)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", body, w.Code)
		}
	}
}

func TestInputRejectsInvalidJSON(t *testing.T) {
	rr := httptest.NewRequest("POST", "/input", strings.NewReader("{"))
	w := httptest.NewRecorder()
	handleInput(w, rr)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
}

// The screen routes must degrade with a clear error when there is no X
// server or no capture tool — never a panic.
func TestScreenAndScreenshotDegrade(t *testing.T) {
	rr := httptest.NewRequest("GET", "/screen", nil)
	w := httptest.NewRecorder()
	handleScreen(w, rr)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Fatalf("screen status %d", w.Code)
	}

	rr = httptest.NewRequest("GET", "/screenshot", nil)
	w = httptest.NewRecorder()
	handleScreenshot(w, rr)
	switch w.Code {
	case http.StatusOK, http.StatusNotImplemented, http.StatusInternalServerError:
	default:
		t.Fatalf("screenshot status %d", w.Code)
	}
}

// The history file must survive a restart: the agent re-reads it at startup.
func TestHistorySurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "history.json")

	historyMu.Lock()
	historyPath = p
	historyLog = nil
	historyMu.Unlock()
	recordHistory("persisted", "", time.Now(), time.Now(), 7, "out")

	// A restart: the in-memory log is gone, the file is not.
	historyMu.Lock()
	historyLog = nil
	historyMu.Unlock()
	loadHistory()
	log := historySnapshot()
	e, ok := findEntry(log, "persisted")
	if !ok {
		t.Fatalf("reloaded log = %+v", log)
	}
	if e.Exit != 7 {
		t.Fatalf("reloaded entry = %+v", e)
	}
}
