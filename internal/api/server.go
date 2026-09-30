// Package api exposes the orchestrator over HTTP: desktop provisioning plus
// the noVNC assets and the WebSocket-to-VNC bridge.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"runmesh/workspace/internal/catalog"
	"runmesh/workspace/internal/desktop"
	"runmesh/workspace/internal/egress"
	"runmesh/workspace/internal/vnc"
	"runmesh/workspace/internal/volume"
	"runmesh/workspace/web"
)

// Server wires the manager, warm pool, volume store and VNC bridge into an
// http.Handler.
type Server struct {
	mgr     *desktop.Manager
	pool    *desktop.Pool
	cfg     *desktop.Config
	volumes *volume.Store
	catalog *catalog.DB
	log     io.Writer
	started time.Time
	info    StatusInfo
	web     http.Handler

	polMu    sync.Mutex
	policies map[string]egress.Policy // per-desktop egress policy
}

// New builds a Server. volumes and cat may be nil when those are disabled.
func New(mgr *desktop.Manager, p *desktop.Pool, cfg *desktop.Config, volumes *volume.Store, cat *catalog.DB, log io.Writer) *Server {
	if log == nil {
		log = io.Discard
	}
	return &Server{mgr: mgr, pool: p, cfg: cfg, volumes: volumes, catalog: cat, log: log, started: time.Now(), web: web.Handler(), policies: map[string]egress.Policy{}}
}

// effectivePolicy merges a request's allow/deny with the daemon defaults. A
// request with neither inherits the daemon policy.
func (s *Server) effectivePolicy(allow, deny []string) egress.Policy {
	if len(allow) == 0 && len(deny) == 0 {
		return egress.Policy{Allow: s.cfg.Allow, Deny: s.cfg.Deny}
	}
	return egress.Policy{Allow: allow, Deny: deny}
}

func (s *Server) setPolicy(id string, p egress.Policy) {
	s.polMu.Lock()
	s.policies[id] = p
	s.polMu.Unlock()
}

func (s *Server) policyFor(id string) egress.Policy {
	s.polMu.Lock()
	defer s.polMu.Unlock()
	return s.policies[id]
}

// EgressPolicyForIP resolves the policy for a guest by its address. The egress
// proxy calls this for every connection, so a per-desktop policy is enforced
// without the guest knowing anything about it.
func (s *Server) EgressPolicyForIP(ip string) (egress.Policy, bool) {
	for _, d := range s.mgr.List() {
		if d.GuestIP == ip {
			return s.policyFor(d.ID), true
		}
	}
	return egress.Policy{}, false
}

// setDesktopPolicy updates a desktop's egress policy after creation.
func (s *Server) setDesktopPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.visibleVM(w, r, id); !ok {
		return
	}
	var req struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	p := egress.Policy{Allow: req.Allow, Deny: req.Deny}
	s.setPolicy(id, p)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "allow": p.Allow, "deny": p.Deny})
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/desktops", s.create)
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/images", s.images)
	mux.HandleFunc("GET /api/desktops", s.list)
	mux.HandleFunc("GET /api/desktops/{id}", s.get)
	mux.HandleFunc("DELETE /api/desktops/{id}", s.destroy)
	mux.HandleFunc("POST /api/desktops/{id}/policy", s.setDesktopPolicy)

	// Agent API: exec + files inside the guest (proxied to warmbox-agent).
	mux.HandleFunc("POST /api/desktops/{id}/exec", s.agentExec)
	mux.HandleFunc("GET /api/desktops/{id}/files", s.agentFiles)
	mux.HandleFunc("GET /api/desktops/{id}/file", s.agentFileGet)
	mux.HandleFunc("PUT /api/desktops/{id}/file", s.agentFilePut)
	mux.HandleFunc("DELETE /api/desktops/{id}/file", s.agentFileDelete)
	mux.HandleFunc("POST /api/desktops/{id}/file/move", s.agentFileMove)

	// Volumes: portable, cloud-backed disks.
	mux.HandleFunc("POST /api/volumes", s.volumeCreate)
	mux.HandleFunc("GET /api/volumes", s.volumeList)
	mux.HandleFunc("GET /api/volumes/{name}", s.volumeGet)
	mux.HandleFunc("DELETE /api/volumes/{name}", s.volumeDelete)
	mux.HandleFunc("POST /api/volumes/{name}/clone", s.volumeClone)

	// Snapshots: frozen volume manifests.
	mux.HandleFunc("POST /api/snapshots", s.snapshotCreate)
	mux.HandleFunc("GET /api/snapshots", s.snapshotList)
	mux.HandleFunc("DELETE /api/snapshots/{id}", s.snapshotDelete)

	// Identity: first-run setup, login, sessions, users, workspaces, API tokens.
	// Setup/login are public by necessity (gated inside withAuth); the rest
	// require a session or token with sufficient rank.
	mux.HandleFunc("GET /api/setup/status", s.setupStatus)
	mux.HandleFunc("POST /api/setup", s.setup)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("PATCH /api/me/password", s.changePassword)
	mux.HandleFunc("GET /api/users", s.listUsers)
	mux.HandleFunc("POST /api/users", s.createUser)
	mux.HandleFunc("PATCH /api/users/{id}", s.updateUser)
	mux.HandleFunc("DELETE /api/users/{id}", s.deleteUser)
	mux.HandleFunc("GET /api/workspaces", s.listWorkspaces)
	mux.HandleFunc("POST /api/workspaces", s.createWorkspace)
	mux.HandleFunc("POST /api/workspaces/{id}/members", s.addWorkspaceMember)
	mux.HandleFunc("DELETE /api/workspaces/{id}/members/{userId}", s.removeWorkspaceMember)
	mux.HandleFunc("GET /api/tokens", s.listTokens)
	mux.HandleFunc("POST /api/tokens", s.createToken)
	mux.HandleFunc("DELETE /api/tokens/{id}", s.deleteToken)

	// Guest readiness callback (guest -> host).
	mux.HandleFunc("GET /internal/ready", s.ready)

	// WebSocket bridge to the guest VNC server.
	mux.HandleFunc("GET /websockify/{id}", s.websockify)

	// Publish a guest HTTP server (e.g. an agent-built dashboard) at a link.
	mux.HandleFunc("GET /p/{id}/{port}/", s.expose)

	// Short desktop URL: serves noVNC full-screen, no token in the address bar.
	mux.HandleFunc("GET /d/{id}", s.dash)

	// Static noVNC assets, one namespace per VM.
	mux.HandleFunc("GET /vnc/{id}/", s.novnc)

	mux.HandleFunc("GET /", s.webui)
	// The middleware itself decides what is public (shell, setup, login,
	// readiness) and what needs an identity, so it always wraps the mux —
	// even with no shared token configured.
	return s.withAuth(mux)
}

// withAuth gates every stateful route. The dashboard shell stays public — it
// carries no data, and the client-side setup/login screens are what onboard a
// first-time visitor. Setup and login are public by necessity (rate limited);
// everything else needs an identity, and mutating or membership-changing routes
// additionally need the rank minRoleFor assigns.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/ready" {
			next.ServeHTTP(w, r)
			return
		}
		if isShellPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/setup/status" && r.Method == http.MethodGet {
			s.setupStatus(w, r)
			return
		}
		if r.URL.Path == "/api/setup" && r.Method == http.MethodPost {
			if s.usersExist() {
				http.NotFound(w, r)
				return
			}
			s.setup(w, r)
			return
		}
		if r.URL.Path == "/api/login" && r.Method == http.MethodPost {
			s.login(w, r)
			return
		}
		ac, ok := s.authenticate(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if ac.userID != "" && catalog.RoleRank(ac.role) < minRoleFor(r.Method, r.URL.Path) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !checkOrigin(r, ac.via == "session") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, ac)))
	})
}

// isShellPath reports whether a request is for the dashboard bundle itself —
// index.html or a file the build dropped next to it (assets, favicon). API,
// WebSocket and console routes are pinned explicitly so a dotted name can
// never slip through the gate.
func isShellPath(p string) bool {
	for _, prefix := range []string{"/api/", "/internal/", "/websockify/", "/p/", "/d/", "/vnc/"} {
		if strings.HasPrefix(p, prefix) {
			return false
		}
	}
	if p == "/" || p == "/index.html" {
		return true
	}
	base := p[strings.LastIndexByte(p, '/')+1:]
	return strings.Contains(base, ".")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// callerScope returns the request workspace and whether the caller administers
// (admin or owner — admins see every workspace).
func (s *Server) callerScope(r *http.Request) (string, bool) {
	ac := authOf(r)
	if ac == nil {
		return "", false
	}
	return ac.workspaceID, catalog.RoleRank(ac.role) >= catalog.RoleRank(catalog.RoleAdmin)
}

// wsVisible reports whether a tagged resource is visible to the caller.
// Untagged rows predate scoping (or are unleased pool capacity) and stay
// visible everywhere; tagged rows only to their workspace, or to admins.
// Callers answer 404 for hidden rows so one workspace cannot probe another's
// inventory.
func wsVisible(rowWS, ws string, admin bool) bool {
	return admin || rowWS == "" || rowWS == ws
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Volume string   `json:"volume"`
		Image  string   `json:"image"`
		Allow  []string `json:"allow"`
		Deny   []string `json:"deny"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	pol := s.effectivePolicy(req.Allow, req.Deny)
	if req.Volume != "" {
		s.createWithVolume(w, r, req.Volume, pol)
		return
	}
	if req.Image != "" {
		s.createImage(w, r, req.Image, pol)
		return
	}

	vm, err := s.pool.Acquire(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	ws, _ := s.callerScope(r)
	vm.SetWorkspace(ws)
	s.setPolicy(vm.ID, pol)
	s.recordDesktop(vm.ID, "", "busy", "", 0, ws)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":  vm.ID,
		"vnc": "/d/" + vm.ID,
		"ws":  "/websockify/" + vm.ID,
	})
}

// createImage provisions a desktop from a named image. If it is the daemon's
// default image it can come from the warm pool (instant); other images boot on
// demand (each is a different disk).
func (s *Server) createImage(w http.ResponseWriter, r *http.Request, name string, pol egress.Policy) {
	if desktop.SameImage(name, s.cfg.Image) {
		vm, err := s.pool.Acquire(r.Context())
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		ws, _ := s.callerScope(r)
		vm.SetWorkspace(ws)
		s.setPolicy(vm.ID, pol)
		s.recordDesktop(vm.ID, "", "busy", "", 0, ws)
		writeJSON(w, http.StatusCreated, map[string]any{
			"id":  vm.ID,
			"vnc": "/d/" + vm.ID,
			"ws":  "/websockify/" + vm.ID,
		})
		return
	}

	id := desktop.NewID()
	vm, err := s.mgr.StartImage(id, "", "", name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ws, _ := s.callerScope(r)
	vm.SetWorkspace(ws)
	s.setPolicy(id, pol)
	s.recordDesktop(id, "", "booting", "", 0, ws)
	wctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	if _, err := s.mgr.WaitReady(wctx, vm.ID); err != nil {
		_ = s.mgr.Destroy(vm.ID)
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":  vm.ID,
		"vnc": "/d/" + vm.ID,
		"ws":  "/websockify/" + vm.ID,
	})
}

// images lists the guest images the daemon can boot.
func (s *Server) images(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"images": s.mgr.Images()})
}

// createWithVolume boots a VM whose writable layer is a persistent volume.
// Volume-backed VMs cannot come from the warm pool (the disk must be attached
// before boot), so they cold-boot. The volume must be visible to the caller.
func (s *Server) createWithVolume(w http.ResponseWriter, r *http.Request, name string, pol egress.Policy) {
	if !s.volumesEnabled(w) {
		return
	}
	ws, admin := s.callerScope(r)
	if s.catalog != nil {
		if v, err := s.catalog.GetVolume(name); err != nil || !wsVisible(v.WorkspaceID, ws, admin) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown volume"})
			return
		}
	}
	ctx := r.Context()
	image, err := s.volumes.EnsureLocal(ctx, name)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "preparing volume: " + err.Error()})
		return
	}
	id := desktop.NewID()
	if err := s.lockVolume(name, id); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	vm, err := s.mgr.StartWithVolume(id, name, image)
	if err != nil {
		s.unlockVolume(name, id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	vm.SetWorkspace(ws)
	s.setPolicy(id, pol)
	s.recordDesktop(id, name, "booting", "", 0, ws)
	wctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if _, err := s.mgr.WaitReady(wctx, vm.ID); err != nil {
		_ = s.mgr.Destroy(vm.ID) // destroy hook commits + releases the volume
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":     vm.ID,
		"volume": name,
		"vnc":    "/d/" + vm.ID,
		"ws":     "/websockify/" + vm.ID,
	})
}

// dashHTML is the short desktop page: noVNC fills the viewport. The iframe
// inherits the session cookie, so the URL carries no credential.
const dashHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>warmbox</title>
<style>html,body{margin:0;height:100%%;background:#111}
iframe{border:0;width:100vw;height:100vh;display:block}</style></head>
<body><iframe allow="clipboard-read;clipboard-write"
 src="/vnc/%s/vnc.html?autoconnect=1&resize=scale&show_dot=1&path=/websockify/%s"></iframe>
</body></html>`

// dash serves the short desktop URL. A request that still carries ?token= is
// redirected to the bare path only when a login cookie already proves the
// session — otherwise the query token IS the credential (API tokens don't set
// cookies) and stripping it would lock the client out.
func (s *Server) dash(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.visibleVM(w, r, id); !ok {
		return
	}
	if r.URL.Query().Get("token") != "" && s.hasLoginCookie(r) {
		http.Redirect(w, r, "/d/"+id, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, dashHTML, id, id)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	ws, admin := s.callerScope(r)
	var infos []desktop.Info
	for _, info := range s.mgr.List() {
		if !wsVisible(info.Workspace, ws, admin) {
			continue
		}
		p := s.policyFor(info.ID)
		info.Allow, info.Deny = p.Allow, p.Deny
		infos = append(infos, info)
	}
	if infos == nil {
		infos = []desktop.Info{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"desktops": infos})
}

// visibleVM fetches a desktop the caller may see. Hidden ones 404 so one
// workspace cannot probe another's inventory.
func (s *Server) visibleVM(w http.ResponseWriter, r *http.Request, id string) (*desktop.VM, bool) {
	ws, admin := s.callerScope(r)
	vm, ok := s.mgr.Get(id)
	if !ok || !wsVisible(vm.Info().Workspace, ws, admin) {
		http.Error(w, "unknown desktop", http.StatusNotFound)
		return nil, false
	}
	return vm, true
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	vm, ok := s.visibleVM(w, r, id)
	if !ok {
		return
	}
	info := vm.Info()
	p := s.policyFor(id)
	info.Allow, info.Deny = p.Allow, p.Deny
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) destroy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.visibleVM(w, r, id); !ok {
		return
	}
	if err := s.mgr.Destroy(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if s.catalog != nil {
		_ = s.catalog.DeleteDesktop(id)
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
	if s.catalog != nil && id != "" {
		if x, err := s.catalog.GetDesktop(id); err == nil {
			x.State = "ready"
			x.GuestIP = ip
			_ = s.catalog.UpsertDesktop(x)
		}
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
	target := vm.Target(s.cfg.GuestVNCPort)
	if target == "" {
		http.Error(w, "desktop not ready", http.StatusServiceUnavailable)
		return
	}
	vnc.Proxy(w, r, target)
}

// agentProxy forwards a request to warmbox-agent inside the guest, preserving
// the method, body and query (the agent exposes /exec, /files, /file, ...).
func (s *Server) agentProxy(w http.ResponseWriter, r *http.Request, id, agentPath string) {
	vm, ok := s.visibleVM(w, r, id)
	if !ok {
		return
	}
	target := vm.Target(s.cfg.AgentPort)
	if target == "" {
		http.Error(w, "desktop not ready", http.StatusServiceUnavailable)
		return
	}
	u := &url.URL{Scheme: "http", Host: target, Path: agentPath, RawQuery: r.URL.RawQuery}
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL = u
			req.Host = target
		},
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
				return vnc.Dial(addr)
			},
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "agent error: "+err.Error(), http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) agentExec(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/exec")
}
func (s *Server) agentFiles(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/files")
}
func (s *Server) agentFileGet(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/file")
}
func (s *Server) agentFilePut(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/file")
}
func (s *Server) agentFileDelete(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/file")
}
func (s *Server) agentFileMove(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/file/move")
}

// expose reverse-proxies an HTTP request to a port inside a guest, so a server
// an agent runs there (a dashboard, an app) can be opened and shared as a URL.
// GET /p/<id>/<port>/... -> http://<guest-ip>:<port>/...
func (s *Server) expose(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	port := r.PathValue("port")
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
	if p, err := strconv.Atoi(port); err != nil || p <= 0 || p > 65535 {
		http.Error(w, "invalid port", http.StatusBadRequest)
		return
	}
	target := net.JoinHostPort(ip, port)
	prefix := "/p/" + id + "/" + port

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = target
			req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
			if req.URL.Path == "" {
				req.URL.Path = "/"
			}
			req.Host = target
		},
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
				return vnc.Dial(addr)
			},
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "proxy error: "+err.Error(), http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) novnc(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.visibleVM(w, r, id); !ok {
		return
	}
	prefix := "/vnc/" + id + "/"
	http.StripPrefix(prefix, http.FileServer(http.Dir(s.cfg.NoVNCDir))).ServeHTTP(w, r)
}

// webui serves the embedded dashboard (React SPA). Unknown non-API paths fall
// back to index.html so the client router can resolve them. When the binary was
// built without the web assets (dist absent), it degrades to a short text page.
func (s *Server) webui(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/internal/") {
		http.NotFound(w, r)
		return
	}
	if s.web == nil {
		idle, pending := s.pool.Stats()
		fmt.Fprintf(w, "warmbox orchestrator\n\nwarm pool: %d idle, %d booting\n", idle, pending)
		fmt.Fprintf(w, "dashboard not built — run `make web` and rebuild\n")
		return
	}
	s.web.ServeHTTP(w, r)
}

// --- volumes ---

// recordDesktop keeps the SQLite catalog in sync with the in-memory manager.
func (s *Server) recordDesktop(id, vol, state, ip string, pid int, ws string) {
	if s.catalog == nil || id == "" {
		return
	}
	_ = s.catalog.UpsertDesktop(&catalog.Desktop{ID: id, Volume: vol, State: state, GuestIP: ip, PID: pid, WorkspaceID: ws})
}

// lockVolume takes the single-writer lock for a volume (a catalog lease when
// available, otherwise the in-process store lock).
func (s *Server) lockVolume(name, owner string) error {
	if s.catalog != nil {
		return s.catalog.AcquireLease(name, owner)
	}
	return s.volumes.Attach(name, owner)
}

func (s *Server) unlockVolume(name, owner string) {
	if s.catalog != nil {
		_ = s.catalog.ReleaseLease(name, owner)
		return
	}
	s.volumes.Release(name, owner)
}

func (s *Server) volumeOwner(name string) string {
	if s.catalog != nil {
		o, _ := s.catalog.LeaseOwner(name)
		return o
	}
	return s.volumes.AttachedTo(name)
}

func (s *Server) volumesEnabled(w http.ResponseWriter) bool {
	if s.volumes == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "volumes are not configured"})
		return false
	}
	return true
}

func (s *Server) volumeCreate(w http.ResponseWriter, r *http.Request) {
	if !s.volumesEnabled(w) {
		return
	}
	var req struct {
		Name         string `json:"name"`
		Size         string `json:"size"`
		From         string `json:"from"`
		FromSnapshot string `json:"from_snapshot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	size, err := parseSize(req.Size)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var m *volume.Meta
	if req.FromSnapshot != "" {
		m, err = s.volumes.CreateFromSnapshot(r.Context(), req.Name, req.FromSnapshot)
	} else {
		m, err = s.volumes.Create(r.Context(), req.Name, size, req.From)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ws, _ := s.callerScope(r)
	if s.catalog != nil {
		_ = s.catalog.UpsertVolume(&catalog.Volume{
			Name:        m.Name,
			Size:        m.Size,
			ChunkSize:   m.ChunkSize,
			Remote:      s.cfg.VolumePrefix + "/" + m.Name,
			From:        m.From,
			WorkspaceID: ws,
			CreatedAt:   m.Created,
		})
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) volumeList(w http.ResponseWriter, r *http.Request) {
	if !s.volumesEnabled(w) {
		return
	}
	if s.catalog != nil {
		ws, admin := s.callerScope(r)
		var vs []*catalog.Volume
		var err error
		if admin {
			vs, err = s.catalog.ListVolumes()
		} else {
			vs, err = s.catalog.ListVolumesInWorkspace(ws)
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if vs == nil {
			vs = []*catalog.Volume{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"volumes": vs})
		return
	}
	vols, err := s.volumes.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if vols == nil {
		vols = []*volume.Meta{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"volumes": vols})
}

func (s *Server) volumeGet(w http.ResponseWriter, r *http.Request) {
	if !s.volumesEnabled(w) {
		return
	}
	if s.catalog != nil {
		m, err := s.catalog.GetVolume(r.PathValue("name"))
		ws, admin := s.callerScope(r)
		if err != nil || !wsVisible(m.WorkspaceID, ws, admin) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown volume"})
			return
		}
		writeJSON(w, http.StatusOK, m)
		return
	}
	m, err := s.volumes.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown volume"})
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) volumeDelete(w http.ResponseWriter, r *http.Request) {
	if !s.volumesEnabled(w) {
		return
	}
	name := r.PathValue("name")
	if s.catalog != nil {
		ws, admin := s.callerScope(r)
		if v, err := s.catalog.GetVolume(name); err != nil || !wsVisible(v.WorkspaceID, ws, admin) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown volume"})
			return
		}
	}
	if owner := s.volumeOwner(name); owner != "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "volume is attached to " + owner})
		return
	}
	if err := s.volumes.Delete(r.Context(), name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if s.catalog != nil {
		_ = s.catalog.DeleteVolume(name)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "name": name})
}

func (s *Server) volumeClone(w http.ResponseWriter, r *http.Request) {
	if !s.volumesEnabled(w) {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	ws, admin := s.callerScope(r)
	if s.catalog != nil {
		if v, err := s.catalog.GetVolume(r.PathValue("name")); err != nil || !wsVisible(v.WorkspaceID, ws, admin) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown volume"})
			return
		}
	}
	m, err := s.volumes.Clone(r.Context(), r.PathValue("name"), req.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if s.catalog != nil {
		_ = s.catalog.UpsertVolume(&catalog.Volume{
			Name:        m.Name,
			Size:        m.Size,
			ChunkSize:   m.ChunkSize,
			Remote:      s.cfg.VolumePrefix + "/" + m.Name,
			From:        m.From,
			WorkspaceID: ws,
			CreatedAt:   m.Created,
		})
	}
	writeJSON(w, http.StatusCreated, m)
}

// --- snapshots ---

func (s *Server) snapshotCreate(w http.ResponseWriter, r *http.Request) {
	if !s.volumesEnabled(w) {
		return
	}
	var req struct {
		Volume string `json:"volume"`
		Name   string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	ws, admin := s.callerScope(r)
	if s.catalog != nil {
		if v, err := s.catalog.GetVolume(req.Volume); err != nil || !wsVisible(v.WorkspaceID, ws, admin) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown volume"})
			return
		}
	}
	snap, err := s.volumes.Snapshot(r.Context(), req.Volume, req.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if s.catalog != nil {
		_ = s.catalog.UpsertSnapshot(&catalog.Snapshot{
			ID: snap.ID, Volume: snap.Volume, Size: snap.Size,
			ChunkSize: snap.ChunkSize, WorkspaceID: ws, CreatedAt: snap.Created,
		})
	}
	writeJSON(w, http.StatusCreated, snap)
}

func (s *Server) snapshotList(w http.ResponseWriter, r *http.Request) {
	if !s.volumesEnabled(w) {
		return
	}
	if s.catalog != nil {
		ws, admin := s.callerScope(r)
		var snaps []*catalog.Snapshot
		var err error
		if admin {
			snaps, err = s.catalog.ListSnapshots(r.URL.Query().Get("volume"))
		} else {
			snaps, err = s.catalog.ListSnapshotsInWorkspace(ws, r.URL.Query().Get("volume"))
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if snaps == nil {
			snaps = []*catalog.Snapshot{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"snapshots": snaps})
		return
	}
	snaps, err := s.volumes.ListSnapshots(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if snaps == nil {
		snaps = []*volume.Snapshot{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snaps})
}

func (s *Server) snapshotDelete(w http.ResponseWriter, r *http.Request) {
	if !s.volumesEnabled(w) {
		return
	}
	id := r.PathValue("id")
	if err := s.volumes.DeleteSnapshot(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if s.catalog != nil {
		_ = s.catalog.DeleteSnapshot(id)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

// parseSize parses "8G", "512M", "1024" (bytes) into a byte count. An empty
// string yields 0 (meaning "use the base image size").
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	up := strings.ToUpper(s)
	mult := int64(1)
	switch {
	case strings.HasSuffix(up, "T"):
		mult, s = 1<<40, s[:len(s)-1]
	case strings.HasSuffix(up, "G"):
		mult, s = 1<<30, s[:len(s)-1]
	case strings.HasSuffix(up, "M"):
		mult, s = 1<<20, s[:len(s)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return n * mult, nil
}
