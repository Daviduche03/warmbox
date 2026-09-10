// Package api exposes the orchestrator over HTTP: desktop provisioning plus
// the noVNC assets and the WebSocket-to-VNC bridge.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"runmesh/workspace/internal/desktop"
	"runmesh/workspace/internal/pool"
	"runmesh/workspace/internal/vnc"
)

// Server wires the manager, warm pool and VNC bridge into an http.Handler.
type Server struct {
	mgr  *desktop.Manager
	pool *pool.Pool
	cfg  *desktop.Config
	log  io.Writer
}

// New builds a Server.
func New(mgr *desktop.Manager, p *pool.Pool, cfg *desktop.Config, log io.Writer) *Server {
	if log == nil {
		log = io.Discard
	}
	return &Server{mgr: mgr, pool: p, cfg: cfg, log: log}
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/desktops", s.create)
	mux.HandleFunc("GET /api/desktops", s.list)
	mux.HandleFunc("GET /api/desktops/{id}", s.get)
	mux.HandleFunc("DELETE /api/desktops/{id}", s.destroy)

	// Guest readiness callback (guest -> host).
	mux.HandleFunc("GET /internal/ready", s.ready)

	// WebSocket bridge to the guest VNC server.
	mux.HandleFunc("GET /websockify/{id}", s.websockify)

	// Static noVNC assets, one namespace per VM.
	mux.HandleFunc("GET /vnc/{id}/", s.novnc)

	mux.HandleFunc("GET /", s.index)
	if s.cfg.Token == "" {
		return mux
	}
	return s.withAuth(mux)
}

// withAuth gates every route except the guest readiness callback behind a
// shared token. Accepting the token as a query parameter lets noVNC's
// autoconnect URL work; on first use we drop an HttpOnly cookie so the static
// assets and the WebSocket inherit it without the token in every URL.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/ready" {
			next.ServeHTTP(w, r)
			return
		}
		token := r.URL.Query().Get("token")
		if token == "" {
			if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
				token = strings.TrimPrefix(a, "Bearer ")
			}
		}
		if token == "" {
			if c, err := r.Cookie("warmbox_token"); err == nil {
				token = c.Value
			}
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("token") != "" {
			http.SetCookie(w, &http.Cookie{
				Name:     "warmbox_token",
				Value:    token,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	vm, err := s.pool.Acquire(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":  vm.ID,
		"vnc": "/vnc/" + vm.ID + "/vnc.html?autoconnect=1&resize=scale&path=/websockify/" + vm.ID + s.tokenSuffix(),
		"ws":  "/websockify/" + vm.ID,
	})
}

// tokenSuffix returns "&token=..." when token auth is enabled so the noVNC
// landing page can seed the auth cookie, or "" otherwise.
func (s *Server) tokenSuffix() string {
	if s.cfg.Token == "" {
		return ""
	}
	return "&token=" + url.QueryEscape(s.cfg.Token)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"desktops": s.mgr.List()})
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	vm, ok := s.mgr.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown desktop"})
		return
	}
	writeJSON(w, http.StatusOK, vm.Info())
}

func (s *Server) destroy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.mgr.Destroy(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "destroyed", "id": id})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	ip := r.URL.Query().Get("ip")
	if !s.mgr.MarkReady(id, ip) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown vm"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) websockify(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	vm, ok := s.mgr.Get(id)
	if !ok {
		http.Error(w, "unknown desktop", http.StatusNotFound)
		return
	}
	ip := vm.IP()
	if ip == "" {
		http.Error(w, "desktop not ready", http.StatusServiceUnavailable)
		return
	}
	target := net.JoinHostPort(ip, strconv.Itoa(s.cfg.GuestVNCPort))
	vnc.Proxy(w, r, target)
}

func (s *Server) novnc(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.mgr.Get(id); !ok {
		http.NotFound(w, r)
		return
	}
	prefix := "/vnc/" + id + "/"
	http.StripPrefix(prefix, http.FileServer(http.Dir(s.cfg.NoVNCDir))).ServeHTTP(w, r)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	idle, pending := s.pool.Stats()
	fmt.Fprintf(w, "warmbox orchestrator\n\nwarm pool: %d idle, %d booting\n", idle, pending)
	fmt.Fprintf(w, "POST /api/desktops to create a desktop\n")
}
