package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func headerFor(t *testing.T, s *Server, path string) http.Header {
	t.Helper()
	rec := httptest.NewRecorder()
	s.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Header()
}

// The dashboard keeps its CSP.
func TestSecurityHeadersCoverTheDashboard(t *testing.T) {
	s := &Server{}
	h := headerFor(t, s, "/")
	if h.Get("Content-Security-Policy") == "" {
		t.Error("the dashboard shell has no Content-Security-Policy")
	}
	for _, name := range []string{"Referrer-Policy", "X-Content-Type-Options", "X-Frame-Options"} {
		if h.Get(name) == "" {
			t.Errorf("the dashboard shell has no %s", name)
		}
	}
}

// noVNC must not inherit it.
//
// vnc.html boots from an inline module script, which `script-src 'self'`
// refuses. Blocked, noVNC's UI never starts, so it never opens the websocket —
// and the daemon has nothing to report because nothing ever arrived. The page
// simply sits on its loading screen forever, which is exactly how this looked
// on a VPS for a day while every layer underneath checked out healthy.
func TestNoVNCPageIsExemptFromTheCSP(t *testing.T) {
	s := &Server{}
	h := headerFor(t, s, "/vnc/abc123/vnc.html")
	if got := h.Get("Content-Security-Policy"); got != "" {
		t.Errorf("noVNC's page would be served with %q, which blocks its bootstrap", got)
	}
	// The other headers are harmless there and still worth having.
	if h.Get("X-Content-Type-Options") == "" {
		t.Error("noVNC's page lost its nosniff header too")
	}
	// Anything that is not noVNC keeps the CSP.
	if h := headerFor(t, s, "/d/abc123"); h.Get("Content-Security-Policy") == "" {
		t.Error("the console page lost its CSP")
	}
}
