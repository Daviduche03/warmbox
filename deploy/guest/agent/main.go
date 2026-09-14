// Command warmbox-agent runs inside the guest VM and exposes a small HTTP API
// the host daemon proxies to: run a command, and read/write/list files. It binds
// localhost only; the daemon is the only client.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var (
	// The guest's network is NAT (vfkit) or user-mode (QEMU): only the host can
	// reach it, so binding the guest interface is fine and is what the host
	// proxies to.
	addr      = envOr("WARMBOX_AGENT_ADDR", "0.0.0.0:7077")
	root      = envOr("WARMBOX_AGENT_ROOT", "/")
	maxOutput = int64(1 << 20) // 1 MiB per stream
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	root = filepath.Clean(root)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	mux.HandleFunc("POST /exec", handleExec)
	mux.HandleFunc("GET /files", handleList)
	mux.HandleFunc("GET /file", handleRead)
	mux.HandleFunc("PUT /file", handleWrite)
	mux.HandleFunc("DELETE /file", handleDelete)
	mux.HandleFunc("POST /file/move", handleMove)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warmbox-agent:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "warmbox-agent listening on", addr, "root", root)
	_ = (&http.Server{Handler: mux}).Serve(ln)
}

// resolve maps a client path into the agent root and rejects escapes.
func resolve(p string) (string, error) {
	if p == "" {
		p = "/"
	}
	if !filepath.IsAbs(p) {
		p = "/" + p
	}
	full := filepath.Clean(filepath.Join(root, p))
	prefix := root
	if !strings.HasSuffix(prefix, string(os.PathSeparator)) {
		prefix += string(os.PathSeparator)
	}
	if full != root && !strings.HasPrefix(full, prefix) {
		return "", errors.New("path escapes agent root")
	}
	return full, nil
}

func fail(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// --- exec ---

type execReq struct {
	Cmd       string            `json:"cmd"`
	Argv      []string          `json:"argv"`
	Cwd       string            `json:"cwd"`
	Env       map[string]string `json:"env"`
	TimeoutMS int               `json:"timeout_ms"`
	Stdin     string            `json:"stdin"`
}

type execResp struct {
	Exit       int    `json:"exit"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMS int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
}

// capWriter keeps the first maxOutput bytes and notes truncation.
type capWriter struct {
	buf       bytes.Buffer
	max       int64
	truncated bool
}

func (c *capWriter) Write(p []byte) (int, error) {
	if int64(c.buf.Len()) < c.max {
		room := c.max - int64(c.buf.Len())
		if int64(len(p)) > room {
			c.buf.Write(p[:room])
			c.truncated = true
		} else {
			c.buf.Write(p)
		}
	} else {
		c.truncated = true
	}
	return len(p), nil
}

func (c *capWriter) String() string {
	s := c.buf.String()
	if c.truncated {
		s += "\n… (truncated)"
	}
	return s
}

func handleExec(w http.ResponseWriter, r *http.Request) {
	var req execReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("invalid json: %w", err))
		return
	}
	var cmd *exec.Cmd
	if len(req.Argv) > 0 {
		cmd = exec.Command(req.Argv[0], req.Argv[1:]...)
	} else {
		if strings.TrimSpace(req.Cmd) == "" {
			fail(w, http.StatusBadRequest, errors.New("cmd or argv required"))
			return
		}
		cmd = exec.Command("/bin/sh", "-c", req.Cmd)
	}
	if req.Cwd != "" {
		full, err := resolve(req.Cwd)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		cmd.Dir = full
	}
	cmd.Env = os.Environ()
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if req.Stdin != "" {
		cmd.Stdin = strings.NewReader(req.Stdin)
	}
	out, errw := &capWriter{max: maxOutput}, &capWriter{max: maxOutput}
	cmd.Stdout, cmd.Stderr = out, errw
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if err := cmd.Start(); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timedOut := false
	select {
	case <-done:
	case <-time.After(timeout):
		timedOut = true
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(execResp{
		Exit: code, Stdout: out.String(), Stderr: errw.String(),
		DurationMS: time.Since(start).Milliseconds(), TimedOut: timedOut,
	})
}

// --- files ---

type fileInfo struct {
	Name  string `json:"name"`
	Type  string `json:"type"` // file | dir | other
	Size  int64  `json:"size"`
	Mode  string `json:"mode"`
	Mtime string `json:"mtime"`
}

func handleList(w http.ResponseWriter, r *http.Request) {
	full, err := resolve(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	out := make([]fileInfo, 0, len(entries))
	for _, e := range entries {
		info, _ := e.Info()
		typ := "other"
		var size int64
		var mode, mtime string
		if info != nil {
			size = info.Size()
			mode = info.Mode().String()
			mtime = info.ModTime().UTC().Format(time.RFC3339)
			switch {
			case info.IsDir():
				typ = "dir"
			case info.Mode().IsRegular():
				typ = "file"
			}
		}
		out = append(out, fileInfo{Name: e.Name(), Type: typ, Size: size, Mode: mode, Mtime: mtime})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func handleRead(w http.ResponseWriter, r *http.Request) {
	full, err := resolve(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

func handleWrite(w http.ResponseWriter, r *http.Request) {
	full, err := resolve(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	f, err := os.Create(full)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	defer f.Close()
	n, err := io.Copy(f, r.Body)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"written": n})
}

func handleDelete(w http.ResponseWriter, r *http.Request) {
	full, err := resolve(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if err := os.RemoveAll(full); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	_, _ = io.WriteString(w, `{"status":"deleted"}`)
}

func handleMove(w http.ResponseWriter, r *http.Request) {
	var req struct{ From, To string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	from, err := resolve(req.From)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	to, err := resolve(req.To)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.Rename(from, to); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	_, _ = io.WriteString(w, `{"status":"moved"}`)
}
