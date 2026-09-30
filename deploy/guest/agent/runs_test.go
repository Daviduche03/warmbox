package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /runs", handleRunsCreate)
	mux.HandleFunc("GET /runs", handleRunsList)
	mux.HandleFunc("GET /runs/{id}", handleRunGet)
	mux.HandleFunc("GET /runs/{id}/stream", handleRunStream)
	mux.HandleFunc("POST /runs/{id}/stdin", handleRunStdin)
	mux.HandleFunc("DELETE /runs/{id}", handleRunKill)
	mux.HandleFunc("POST /sessions", handleSessionCreate)
	mux.HandleFunc("POST /sessions/{id}/input", handleSessionInput)
	mux.HandleFunc("GET /sessions/{id}/output", handleRunStream)
	return mux
}

func createRun(t *testing.T, ts *httptest.Server, body string) string {
	t.Helper()
	resp, err := http.Post(ts.URL+"/runs", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create run: status %d", resp.StatusCode)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out.ID
}

// waitFor polls the run until it reports done, or the deadline passes.
func waitFor(t *testing.T, ts *httptest.Server, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(ts.URL + "/runs/" + id)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		resp.Body.Close()
		if m["running"] == false {
			return m
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run %s did not finish", id)
	return nil
}

func TestRunCapturesOutputAndExit(t *testing.T) {
	ts := httptest.NewServer(newTestMux())
	defer ts.Close()

	id := createRun(t, ts, `{"cmd":"echo hello; echo oops >&2; exit 3"}`)
	waitFor(t, ts, id)

	resp, _ := http.Get(ts.URL + "/runs/" + id + "?tail=1000")
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()

	out, _ := m["output"].(string)
	if !strings.Contains(out, "hello") || !strings.Contains(out, "oops") {
		t.Fatalf("output missing stdout/stderr: %q", out)
	}
	if m["exit"].(float64) != 3 {
		t.Fatalf("exit = %v, want 3", m["exit"])
	}
}

func TestRunStreamsLiveOutput(t *testing.T) {
	ts := httptest.NewServer(newTestMux())
	defer ts.Close()

	// A run that emits a line, waits, then another: the stream must deliver the
	// first before the process exits (proves it isn't buffering to completion).
	id := createRun(t, ts, `{"argv":["sh","-c","echo first; sleep 1; echo second"]}`)

	resp, err := http.Get(ts.URL + "/runs/" + id + "/stream")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}

	sc := bufio.NewScanner(resp.Body)
	var sawFirst, sawExit bool
	start := time.Now()
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m); err != nil {
			continue
		}
		if s, ok := m["out"].(string); ok && strings.Contains(s, "first") && !sawFirst {
			sawFirst = true
			if time.Since(start) > 900*time.Millisecond {
				t.Fatalf("first output arrived only with the exit (%v)", time.Since(start))
			}
		}
		if _, ok := m["exit"]; ok {
			sawExit = true
			break
		}
	}
	if !sawFirst {
		t.Fatal("never saw the first line")
	}
	if !sawExit {
		t.Fatal("never saw the exit event")
	}
}

func TestRunKill(t *testing.T) {
	ts := httptest.NewServer(newTestMux())
	defer ts.Close()

	id := createRun(t, ts, `{"argv":["sleep","30"]}`)
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/runs/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("kill: %v", err)
	}
	resp.Body.Close()
	meta := waitFor(t, ts, id)
	if meta["signal"] == nil {
		t.Fatalf("killed run should report a signal, got %v", meta)
	}
}

func TestSessionPersistsStateAcrossInputs(t *testing.T) {
	ts := httptest.NewServer(newTestMux())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/sessions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	var s struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&s)
	resp.Body.Close()

	// cd in one input, read it back in the next: proves the shell (and its cwd)
	// persists, which one-shot exec cannot do.
	in := func(line string) {
		r, err := http.Post(ts.URL+"/sessions/"+s.ID+"/input", "text/plain", strings.NewReader(line))
		if err != nil {
			t.Fatalf("input: %v", err)
		}
		r.Body.Close()
	}
	in("cd /etc")
	in("echo MARK=$(pwd)")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, _ := http.Get(ts.URL + "/runs/" + s.ID + "?tail=2000")
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		r.Body.Close()
		if out, _ := m["output"].(string); strings.Contains(out, "MARK=/etc") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("session did not persist cwd across inputs")
}
