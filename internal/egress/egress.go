// Package egress is a small forward proxy that enforces a per-host network
// policy for guest VMs. It sits on the host boundary on purpose: the guest runs
// as root and could undo anything enforced inside it, so the allow/deny
// decision is made where the traffic leaves.
package egress

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Policy is a domain allow/deny list. When Allow is non-empty the policy is
// default-deny: a host must match Allow (and not Deny) to pass. Patterns may be
// an exact host ("api.openai.com"), a domain suffix ("example.com" also matches
// "a.example.com"), or a wildcard ("*.example.com").
type Policy struct {
	Allow []string
	Deny  []string
}

// DefaultDeny reports whether only explicitly allowed hosts may pass.
func (p Policy) DefaultDeny() bool { return len(p.Allow) > 0 }

func match(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		return strings.HasSuffix(host, pattern[1:]) // "*.example.com" -> ".example.com"
	}
	return host == pattern || strings.HasSuffix(host, "."+pattern)
}

// Decision is the outcome of evaluating a host.
type Decision struct {
	Allowed bool
	Reason  string
}

// Check evaluates host (which may carry a :port) against the policy.
func (p Policy) Check(host string) Decision {
	h := strings.ToLower(strings.SplitN(host, ":", 2)[0])
	for _, d := range p.Deny {
		if match(d, h) {
			return Decision{false, "denied by rule " + d}
		}
	}
	if !p.DefaultDeny() {
		return Decision{true, "no allowlist"}
	}
	for _, a := range p.Allow {
		if match(a, h) {
			return Decision{true, "allowed by rule " + a}
		}
	}
	return Decision{false, "not in allowlist"}
}

// Proxy is an HTTP(S) forward proxy. It handles CONNECT (HTTPS) and absolute-URI
// (plain HTTP) requests, evaluates each against the policy, logs the decision,
// and only then opens the upstream connection.
type Proxy struct {
	policy  Policy
	resolve func(srcIP string) (Policy, bool) // optional per-client policy
	log     io.Writer
	srv     *http.Server

	mu    sync.Mutex
	stats map[string]int
}

// New builds a proxy with a single policy. resolve may be nil.
func New(addr string, p Policy, log io.Writer, resolve func(string) (Policy, bool)) *Proxy {
	if log == nil {
		log = io.Discard
	}
	pr := &Proxy{policy: p, resolve: resolve, log: log, stats: map[string]int{}}
	pr.srv = &http.Server{Addr: addr, Handler: pr, ReadHeaderTimeout: 10 * time.Second}
	return pr
}

// Start begins serving. Non-blocking.
func (pr *Proxy) Start() error {
	ln, err := net.Listen("tcp", pr.srv.Addr)
	if err != nil {
		return err
	}
	fmt.Fprintf(pr.log, "egress: proxy listening on %s\n", pr.srv.Addr)
	go func() { _ = pr.srv.Serve(ln) }()
	return nil
}

// Stats returns a snapshot of "ALLOW/DENY host" counts.
func (pr *Proxy) Stats() map[string]int {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	out := make(map[string]int, len(pr.stats))
	for k, v := range pr.stats {
		out[k] = v
	}
	return out
}

func (pr *Proxy) policyFor(r *http.Request) Policy {
	if pr.resolve != nil {
		if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			if p, ok := pr.resolve(ip); ok {
				return p
			}
		}
	}
	return pr.policy
}

func (pr *Proxy) record(dec, host string) {
	pr.mu.Lock()
	pr.stats[dec+" "+host]++
	pr.mu.Unlock()
	fmt.Fprintf(pr.log, "egress: %s %s\n", dec, host)
}

// ServeHTTP implements http.Handler.
func (pr *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	policy := pr.policyFor(r)
	host := r.Host

	if r.Method == http.MethodConnect {
		if d := policy.Check(host); !d.Allowed {
			pr.record("DENY", host)
			http.Error(w, "blocked by warmbox egress policy: "+d.Reason, http.StatusForbidden)
			return
		}
		pr.record("ALLOW", host)
		pr.tunnel(w, host)
		return
	}

	if d := policy.Check(host); !d.Allowed {
		pr.record("DENY", host)
		http.Error(w, "blocked by warmbox egress policy: "+d.Reason, http.StatusForbidden)
		return
	}
	pr.record("ALLOW", host)
	r.RequestURI = ""
	r.Header.Del("Proxy-Connection")
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (pr *Proxy) tunnel(w http.ResponseWriter, host string) {
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "443")
	}
	dst, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		dst.Close()
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		dst.Close()
		return
	}
	_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	go func() { _, _ = io.Copy(dst, client); dst.Close() }()
	_, _ = io.Copy(client, dst)
	client.Close()
}

// ParseList splits a comma-separated flag value into trimmed entries.
func ParseList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
