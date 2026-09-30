// Runs and sessions: a command started asynchronously, with live output, stdin,
// and a kill switch. A "session" is just a run of an interactive shell, so both
// share one engine. Output is buffered so a client can attach late and still
// read the tail; the stream endpoint tails it live.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const defaultRunMaxBytes = int64(4 << 20) // 4 MiB of scrollback per run

func newRunID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type run struct {
	id    string
	cmd   *exec.Cmd
	stdin io.WriteCloser

	mu        sync.Mutex
	out       []byte // stdout+stderr, interleaved
	truncated bool
	done      bool
	exit      int
	signal    string
	timedOut  bool
	started   time.Time
	duration  time.Duration
	maxOut    int64
}

// Write implements io.Writer so the command's output lands in the run buffer.
func (r *run) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if int64(len(r.out)) >= r.maxOut {
		r.truncated = true
		return len(p), nil
	}
	room := r.maxOut - int64(len(r.out))
	if int64(len(p)) > room {
		p = p[:room]
		r.truncated = true
	}
	r.out = append(r.out, p...)
	return len(p), nil
}

func (r *run) metaLocked() map[string]any {
	m := map[string]any{
		"id":           r.id,
		"running":      !r.done,
		"started":      r.started.UTC().Format(time.RFC3339Nano),
		"output_bytes": int64(len(r.out)),
	}
	if r.truncated {
		m["truncated"] = true
	}
	if r.done {
		m["exit"] = r.exit
		m["duration_ms"] = r.duration.Milliseconds()
		if r.signal != "" {
			m["signal"] = r.signal
		}
		if r.timedOut {
			m["timed_out"] = true
		}
	}
	return m
}

// snapshot returns output after the absolute offset from, plus the next offset.
func (r *run) snapshot(from int64) (data []byte, next int64, done bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if from < 0 {
		from = 0
	}
	if from > int64(len(r.out)) {
		from = int64(len(r.out))
	}
	return append([]byte(nil), r.out[from:]...), int64(len(r.out)), r.done
}

func (r *run) meta() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.metaLocked()
}

func (r *run) finish(err error, timedOut bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.done = true
	r.duration = time.Since(r.started)
	r.timedOut = r.timedOut || timedOut
	if r.cmd.ProcessState != nil {
		r.exit = r.cmd.ProcessState.ExitCode()
		if ws, ok := r.cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			r.signal = ws.Signal().String()
		}
	} else if err != nil {
		r.exit = -1
	}
}

// kill terminates the whole process group.
func (r *run) kill(sig syscall.Signal) {
	if r.cmd != nil && r.cmd.Process != nil {
		_ = syscall.Kill(-r.cmd.Process.Pid, sig)
	}
}

// --- registry ---

var (
	runsMu sync.Mutex
	runs   = map[string]*run{}
)

func storeRun(r *run) {
	runsMu.Lock()
	runs[r.id] = r
	runsMu.Unlock()
}

func getRun(id string) *run {
	runsMu.Lock()
	defer runsMu.Unlock()
	return runs[id]
}

func listRuns() []map[string]any {
	runsMu.Lock()
	rs := make([]*run, 0, len(runs))
	for _, r := range runs {
		rs = append(rs, r)
	}
	runsMu.Unlock()
	out := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.meta())
	}
	return out
}

// --- start ---

type runReq struct {
	Cmd         string            `json:"cmd"`
	Argv        []string          `json:"argv"`
	Cwd         string            `json:"cwd"`
	Env         map[string]string `json:"env"`
	Shell       string            `json:"shell"`       // start an interactive shell
	Interactive bool              `json:"interactive"` // keep stdin open
	Stdin       string            `json:"stdin"`
	TimeoutMS   int               `json:"timeout_ms"`
	MaxBytes    int64             `json:"max_bytes"`
}

func startRun(req runReq) (*run, error) {
	var cmd *exec.Cmd
	switch {
	case len(req.Argv) > 0:
		cmd = exec.Command(req.Argv[0], req.Argv[1:]...)
	case strings.TrimSpace(req.Shell) != "":
		cmd = exec.Command(req.Shell)
	default:
		if strings.TrimSpace(req.Cmd) == "" {
			return nil, fmt.Errorf("cmd, argv or shell required")
		}
		cmd = exec.Command("/bin/sh", "-c", req.Cmd)
	}
	if req.Cwd != "" {
		full, err := resolve(req.Cwd)
		if err != nil {
			return nil, err
		}
		cmd.Dir = full
	}
	cmd.Env = os.Environ()
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	r := &run{started: time.Now(), maxOut: defaultRunMaxBytes}
	if req.MaxBytes > 0 {
		r.maxOut = req.MaxBytes
	}
	r.id = newRunID()
	cmd.Stdout, cmd.Stderr = r, r
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	r.stdin = stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	r.cmd = cmd
	storeRun(r)

	if req.Stdin != "" {
		_, _ = io.WriteString(stdin, req.Stdin)
	}
	if !req.Interactive {
		_ = stdin.Close()
	}
	if req.TimeoutMS > 0 {
		time.AfterFunc(time.Duration(req.TimeoutMS)*time.Millisecond, func() {
			r.mu.Lock()
			if !r.done {
				r.timedOut = true
			}
			r.mu.Unlock()
			r.kill(syscall.SIGKILL)
		})
	}
	go func() {
		err := cmd.Wait()
		r.finish(err, false)
	}()
	return r, nil
}

// --- handlers ---

func handleRunsCreate(w http.ResponseWriter, r *http.Request) {
	var req runReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("invalid json: %w", err))
		return
	}
	rn, err := startRun(req)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"id": rn.id, "running": true})
}

func handleRunsList(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"runs": listRuns()})
}

func handleRunGet(w http.ResponseWriter, r *http.Request) {
	rn := getRun(r.PathValue("id"))
	if rn == nil {
		fail(w, http.StatusNotFound, fmt.Errorf("unknown run"))
		return
	}
	meta := rn.meta()
	if tail, err := strconv.Atoi(r.URL.Query().Get("tail")); err == nil && tail > 0 {
		rn.mu.Lock()
		n := len(rn.out)
		start := n - tail
		if start < 0 {
			start = 0
		}
		meta["output"] = string(rn.out[start:])
		rn.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(meta)
}

func handleRunStdin(w http.ResponseWriter, r *http.Request) {
	rn := getRun(r.PathValue("id"))
	if rn == nil {
		fail(w, http.StatusNotFound, fmt.Errorf("unknown run"))
		return
	}
	if rn.stdin == nil {
		fail(w, http.StatusConflict, fmt.Errorf("stdin is closed"))
		return
	}
	if _, err := io.Copy(rn.stdin, r.Body); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	_, _ = io.WriteString(w, `{"status":"written"}`)
}

func handleRunKill(w http.ResponseWriter, r *http.Request) {
	rn := getRun(r.PathValue("id"))
	if rn == nil {
		fail(w, http.StatusNotFound, fmt.Errorf("unknown run"))
		return
	}
	rn.kill(syscall.SIGKILL)
	_, _ = io.WriteString(w, `{"status":"killed"}`)
}

// handleRunStream tails a run's output as Server-Sent Events, from ?from=N,
// ending with an exit event. This is what makes long builds and dev servers
// watchable instead of a one-shot timeout.
func handleRunStream(w http.ResponseWriter, r *http.Request) {
	rn := getRun(r.PathValue("id"))
	if rn == nil {
		fail(w, http.StatusNotFound, fmt.Errorf("unknown run"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	from, _ := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
	emit := func(v any) bool {
		b, err := json.Marshal(v)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	for {
		data, next, done := rn.snapshot(from)
		if len(data) > 0 {
			if !emit(map[string]any{"out": string(data)}) {
				return
			}
			from = next
		}
		if done {
			emit(map[string]any{"exit": rn.meta()})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// --- sessions: a run of an interactive shell (cwd/env persist between inputs) ---

func handleSessionCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cwd   string            `json:"cwd"`
		Env   map[string]string `json:"env"`
		Shell string            `json:"shell"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.Shell == "" {
		req.Shell = "/bin/sh"
	}
	rn, err := startRun(runReq{Cwd: req.Cwd, Env: req.Env, Shell: req.Shell, Interactive: true})
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"id": rn.id})
}

func handleSessionInput(w http.ResponseWriter, r *http.Request) {
	rn := getRun(r.PathValue("id"))
	if rn == nil {
		fail(w, http.StatusNotFound, fmt.Errorf("unknown session"))
		return
	}
	if rn.stdin == nil {
		fail(w, http.StatusConflict, fmt.Errorf("session is closed"))
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	// A session input is a shell line: append a newline unless it already ends
	// with one.
	if len(body) > 0 && body[len(body)-1] != '\n' {
		body = append(body, '\n')
	}
	if _, err := rn.stdin.Write(body); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	_, _ = io.WriteString(w, `{"status":"sent"}`)
}
