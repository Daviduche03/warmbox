package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"warmbox/internal/desktop"
)

// Whether an address is reachable from the network decides whether the daemon
// refuses to start, so it has to be right for the shapes people actually type —
// including the accidental one, a bare ":7070", which is every interface.
// A session stream is Server-Sent Events ending in an exit event. Feed the
// parser canned streams: no daemon, no VM, no network beyond localhost.
func TestReadSSEStream(t *testing.T) {
	cases := []struct {
		name string
		body string
		out  string
		code int
	}{
		{
			"out then exit",
			"data: {\"out\":\"hello\\n\"}\n\ndata: {\"exit\":{\"exit\":0}}\n\n",
			"hello\n", 0,
		},
		{
			"nonzero exit reported",
			"data: {\"exit\":{\"exit\":3}}\n\n",
			"", 3,
		},
		{
			"blank lines and comments ignored",
			"\n: ping\n\ndata: {\"out\":\"x\"}\n\ndata: {\"exit\":{\"exit\":0}}\n\n",
			"x", 0,
		},
		{
			"malformed event skipped, stream continues",
			"data: not-json\n\ndata: {\"out\":\"ok\"}\n\ndata: {\"exit\":{\"exit\":0}}\n\n",
			"ok", 0,
		},
		{
			"EOF without exit is a broken stream",
			"data: {\"out\":\"half\"}\n\n",
			"half", -1,
		},
		{
			"empty stream",
			"",
			"", -1,
		},
	}
	for _, c := range cases {
		var out strings.Builder
		if code := readSSEStream(strings.NewReader(c.body), &out); code != c.code {
			t.Errorf("%s: code = %d, want %d", c.name, code, c.code)
		} else if out.String() != c.out {
			t.Errorf("%s: out = %q, want %q", c.name, out.String(), c.out)
		}
	}
}

// A stalled daemon must fail fast, not hang forever: this server answers
// nothing, so the default 30s timeout (not the multi-minute hang the old
// un-timed client produced) fires, and the error names the address.
func TestDoAuthedTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()
	start := time.Now()
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := doAuthedTimeout(req, 300*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if time.Since(start) > 4*time.Second {
		t.Fatalf("took %v, timeout did not fire", time.Since(start))
	}
	if !strings.Contains(err.Error(), "is the daemon at") {
		t.Fatalf("error should name the daemon address, got %v", err)
	}
}

// postSessionInput is a thin POST; the one thing that must not regress is that
// the daemon's {"error": ...} body surfaces instead of a bare status.
func TestPostSessionInputError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":"desktop is hibernated — wake it first"}`)
	}))
	defer srv.Close()
	err := postSessionInput(srv.URL, "", "echo hi\n")
	if err == nil || !strings.Contains(err.Error(), "hibernated") {
		t.Fatalf("expected the daemon error, got %v", err)
	}
}

// postSessionResize must send cols/rows as JSON; the guest sizes itself off
// exactly those keys.
func TestPostSessionResize(t *testing.T) {
	var got struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		fmt.Fprint(w, `{"status":"resized"}`)
	}))
	defer srv.Close()
	if err := postSessionResize(srv.URL, "", 100, 40); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if got.Cols != 100 || got.Rows != 40 {
		t.Fatalf("sent %dx%d, want 100x40", got.Cols, got.Rows)
	}
}
func TestBindIsLoopback(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:7070", true},
		{"localhost:7070", true},
		{"[::1]:7070", true},
		{"127.0.0.2:7070", true}, // the whole 127/8 is loopback
		{":7070", false},         // every interface, and easy to type by mistake
		{"0.0.0.0:7070", false},
		{"[::]:7070", false},
		{"192.168.1.10:7070", false},
		{"10.0.0.5:7070", false},
		{"", false},
		{"not-an-address", false},
	}
	for _, c := range cases {
		if got := bindIsLoopback(c.addr); got != c.want {
			t.Errorf("bindIsLoopback(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

// A service has to remember the sizes it was installed with. It used to accept
// --mem and --cpus and silently drop them, leaving the daemon on the built-in
// 4 GiB and 4 vCPUs — on a small host, more memory than the machine has.
func TestServiceFlagsCarrySizing(t *testing.T) {
	cfg := desktop.DefaultConfig()
	cfg.MemMiB = 768
	cfg.CPUs = 1
	args := appendServiceFlags([]string{"daemon"}, cfg)
	if !hasPair(args, "--mem", "768") {
		t.Errorf("no --mem 768 in %v", args)
	}
	if !hasPair(args, "--cpus", "1") {
		t.Errorf("no --cpus 1 in %v", args)
	}
}

// …but it must not invent flags nobody asked for: a unit that pins every value
// freezes today's defaults for the people who never chose them.
func TestServiceFlagsLeaveDefaultsAlone(t *testing.T) {
	cfg := desktop.DefaultConfig()
	args := appendServiceFlags([]string{"daemon"}, cfg)
	for _, flag := range []string{"--mem", "--cpus", "--gpu", "--input", "--accel", "--tls-cert", "--insecure"} {
		for _, a := range args {
			if a == flag {
				t.Errorf("%s was written for an unconfigured service: %v", flag, args)
			}
		}
	}
}

// A service bound to the network needs its certificate remembered, or the unit
// would start the daemon without one and it would refuse to run.
func TestServiceFlagsCarryTLS(t *testing.T) {
	cfg := desktop.DefaultConfig()
	cfg.TLSCert = "/etc/warmbox/cert.pem"
	cfg.TLSKey = "/etc/warmbox/key.pem"
	args := appendServiceFlags([]string{"daemon"}, cfg)
	if !hasPair(args, "--tls-cert", cfg.TLSCert) {
		t.Errorf("no --tls-cert in %v", args)
	}
	if !hasPair(args, "--tls-key", cfg.TLSKey) {
		t.Errorf("no --tls-key in %v", args)
	}
}

func hasPair(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}
