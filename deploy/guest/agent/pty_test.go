package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func requireLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("pseudoterminals need linux")
	}
}

// The pair must exist and the size must round-trip: this is the whole
// contract the session code relies on.
func TestPTYAllocAndWinsize(t *testing.T) {
	requireLinux(t)
	master, slave, err := openPTY()
	if err != nil {
		t.Fatalf("openPTY: %v", err)
	}
	defer master.Close()
	if _, err := os.Stat(slave); err != nil {
		t.Fatalf("slave %s: %v", slave, err)
	}
	if err := setWinsize(master, 100, 40); err != nil {
		t.Fatalf("setWinsize: %v", err)
	}
	cols, rows, err := getWinsize(master)
	if err != nil {
		t.Fatalf("getWinsize: %v", err)
	}
	if cols != 100 || rows != 40 {
		t.Fatalf("size = %dx%d, want 100x40", cols, rows)
	}
	if err := setWinsize(master, 0, -3); err == nil {
		t.Fatal("nonsense size accepted")
	}
}

func createPtySession(t *testing.T, ts *httptest.Server, body string) (id string, pty bool) {	t.Helper()
	resp, err := http.Post(ts.URL+"/sessions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create session: status %d", resp.StatusCode)
	}
	var out struct {
		ID  string `json:"id"`
		Pty bool   `json:"pty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out.ID, out.Pty
}

func sessionInput(t *testing.T, ts *httptest.Server, id, line string) {
	t.Helper()
	resp, err := http.Post(ts.URL+"/sessions/"+id+"/input", "application/octet-stream", strings.NewReader(line))
	if err != nil {
		t.Fatalf("input: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("input: status %d", resp.StatusCode)
	}
}

// waitSessionOutput polls the tail until it contains mark (the shell answers
// fast locally; the deadline is generosity for loaded CI).
func waitSessionOutput(t *testing.T, ts *httptest.Server, id, mark string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(ts.URL + "/sessions/" + id + "?tail=65536")
		if err != nil {
			t.Fatalf("get session: %v", err)
		}
		var m struct {
			Output string `json:"output"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&m)
		resp.Body.Close()
		if strings.Contains(m.Output, mark) {
			return m.Output
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("output never contained %q", mark)
	return ""
}

// A pty session must look like a terminal from the inside: test -t passes,
// stty reports the size the client asked for, and a later resize lands.
func TestPtySessionIsATTY(t *testing.T) {
	requireLinux(t)
	ts := httptest.NewServer(newTestMux())
	defer ts.Close()

	id, pty := createPtySession(t, ts, `{"shell":"/bin/sh","pty":true,"cols":100,"rows":40}`)
	if !pty {
		t.Fatal("pty not granted")
	}
	sessionInput(t, ts, id, "test -t 0 && test -t 1 && echo TTY-YES\n")
	waitSessionOutput(t, ts, id, "TTY-YES")
	sessionInput(t, ts, id, "stty size\n")
	waitSessionOutput(t, ts, id, "40 100")

	resp, err := http.Post(ts.URL+"/sessions/"+id+"/resize", "application/json",
		strings.NewReader(`{"cols":120,"rows":30}`))
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resize: status %d", resp.StatusCode)
	}
	sessionInput(t, ts, id, "stty size\n")
	waitSessionOutput(t, ts, id, "30 120")
	sessionInput(t, ts, id, "exit\n")
}

// Pipe sessions are unchanged: input still gets its newline, and resize is
// refused (there is no terminal to size).
func TestResizeRejectsPipeSession(t *testing.T) {
	ts := httptest.NewServer(newTestMux())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/sessions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	var out struct {
		ID  string `json:"id"`
		Pty bool   `json:"pty"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out.Pty {
		t.Fatal("plain session granted a pty")
	}
	resp, err = http.Post(ts.URL+"/sessions/"+out.ID+"/resize", "application/json",
		strings.NewReader(`{"cols":80,"rows":24}`))
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("resize on pipe session: status %d, want 409", resp.StatusCode)
	}
	sessionInput(t, ts, out.ID, "exit\n")
}

// Children must die on SIGINT even when the process that started them had it
// ignored (the guest init's disposition, reproduced here): ignored
// dispositions survive exec, and os/signal.Reset does not undo an inherited
// ignore — only the raw reset does. The victim is sleep itself, which never
// touches signal handling; if it survives, the inheritance chain is broken.
func TestChildInterruptibleDespiteIgnoredSIGINT(t *testing.T) {
	requireLinux(t)
	signal.Ignore(syscall.SIGINT)
	defer signal.Reset(syscall.SIGINT)
	if err := resetsignals(); err != nil {
		t.Fatalf("resetsignals: %v", err)
	}
	r, err := startRun(runReq{Argv: []string{"sleep", "30"}, TimeoutMS: 8000})
	if err != nil {
		t.Fatalf("startRun: %v", err)
	}
	pid := r.cmd.Process.Pid
	deadline := time.Now().Add(10 * time.Second)
	killed := false
	for {
		r.mu.Lock()
		done := r.done
		r.mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		if !killed {
			killed = true
			_ = syscall.Kill(pid, syscall.SIGINT)
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.done {
		t.Fatal("child survived SIGINT (and the timeout had to kill it)")
	}
	if r.timedOut {
		t.Fatal("child survived SIGINT; only the timeout stopped it")
	}
	if r.signal != "interrupt" {
		t.Fatalf("signal = %q, want interrupt", r.signal)
	}
}
