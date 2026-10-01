// Package api exposes the orchestrator over HTTP: desktop provisioning plus
// the noVNC assets and the WebSocket-to-VNC bridge.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"warmbox/internal/catalog"
	"warmbox/internal/desktop"
	"warmbox/internal/egress"
	"warmbox/internal/imagecfg"
	"warmbox/internal/release"
	"warmbox/internal/update"
	"warmbox/internal/vnc"
	"warmbox/internal/volume"
	"warmbox/web"
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

	// updater is the background "is there a newer release?" check surfaced by
	// GET /api/status; nil when the daemon did not wire one (tests).
	updater *update.Manager

	// pulls tracks guest-image downloads: the one in flight, and how the last
	// one for each image ended.
	pulls *pullRegistry

	polMu    sync.Mutex
	policies map[string]egress.Policy // per-desktop egress policy
}

// New builds a Server. volumes and cat may be nil when those are disabled.
func New(mgr *desktop.Manager, p *desktop.Pool, cfg *desktop.Config, volumes *volume.Store, cat *catalog.DB, log io.Writer) *Server {
	if log == nil {
		log = io.Discard
	}
	return &Server{
		mgr: mgr, pool: p, cfg: cfg, volumes: volumes, catalog: cat, log: log,
		started: time.Now(), web: web.Handler(), policies: map[string]egress.Policy{},
		pulls: newPullRegistry(),
	}
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
	mux.HandleFunc("POST /api/images/{name}/pull", s.imagePull)

	// Cloud storage (where volume bytes live) — owner-only, see minRoleFor.
	mux.HandleFunc("GET /api/cloud", s.cloudGet)
	mux.HandleFunc("PUT /api/cloud", s.cloudPut)
	mux.HandleFunc("DELETE /api/cloud", s.cloudDelete)
	mux.HandleFunc("GET /api/desktops", s.list)
	mux.HandleFunc("GET /api/desktops/{id}", s.get)
	mux.HandleFunc("DELETE /api/desktops/{id}", s.destroy)
	mux.HandleFunc("POST /api/desktops/{id}/pause", s.pause)
	mux.HandleFunc("POST /api/desktops/{id}/resume", s.resume)
	mux.HandleFunc("POST /api/desktops/{id}/policy", s.setDesktopPolicy)

	// Agent API: exec + files inside the guest (proxied to warmbox-agent).
	mux.HandleFunc("POST /api/desktops/{id}/exec", s.agentExec)
	mux.HandleFunc("GET /api/desktops/{id}/files", s.agentFiles)
	mux.HandleFunc("GET /api/desktops/{id}/file", s.agentFileGet)
	mux.HandleFunc("PUT /api/desktops/{id}/file", s.agentFilePut)
	mux.HandleFunc("DELETE /api/desktops/{id}/file", s.agentFileDelete)
	mux.HandleFunc("POST /api/desktops/{id}/file/move", s.agentFileMove)

	// Agent API: runs (background/streaming exec) and sessions.
	mux.HandleFunc("POST /api/desktops/{id}/runs", s.agentRunCreate)
	mux.HandleFunc("GET /api/desktops/{id}/runs", s.agentRunList)
	mux.HandleFunc("GET /api/desktops/{id}/runs/{run}", s.agentRunGet)
	mux.HandleFunc("GET /api/desktops/{id}/runs/{run}/stream", s.agentRunStream)
	mux.HandleFunc("POST /api/desktops/{id}/runs/{run}/stdin", s.agentRunStdin)
	mux.HandleFunc("DELETE /api/desktops/{id}/runs/{run}", s.agentRunKill)
	mux.HandleFunc("POST /api/desktops/{id}/sessions", s.agentSessionCreate)
	mux.HandleFunc("GET /api/desktops/{id}/sessions", s.agentRunList)
	mux.HandleFunc("GET /api/desktops/{id}/sessions/{run}", s.agentRunGet)
	mux.HandleFunc("GET /api/desktops/{id}/sessions/{run}/output", s.agentSessionOutput)
	mux.HandleFunc("POST /api/desktops/{id}/sessions/{run}/input", s.agentSessionInput)
	mux.HandleFunc("DELETE /api/desktops/{id}/sessions/{run}", s.agentRunKill)

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
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PATCH /api/settings", s.updateSettings)
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
	return s.securityHeaders(s.withAuth(mux))
}

// GuestHandler serves just the callback guests make (readiness), for the
// guest-facing listener. Guests hold no credentials, so nothing here may read
// or change user state, and the source is still checked.
func (s *Server) GuestHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/ready", func(w http.ResponseWriter, r *http.Request) {
		if !s.guestSource(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		s.ready(w, r)
	})
	return mux
}

// securityHeaders applies conservative browser defaults to every response: no
// framing of the dashboard, no MIME sniffing, no referrer leakage, and a CSP
// the app satisfies (same-origin scripts, inline styles for the charts,
// same-origin websockets for the console, blob workers for noVNC).
//
// noVNC is the one exception, and it is a deliberate one. vnc.html boots from an
// inline module script — `import UI from "./app/ui.js"; …` — which script-src
// 'self' refuses. A refused bootstrap is completely silent: noVNC's UI never
// starts, so it never opens the websocket, so the daemon has nothing to log and
// the page sits on its loading screen forever. Allowing that script by hash
// would work exactly until a noVNC release changed it, and then break the
// console just as silently, so the CSP stops at our own documents: noVNC is a
// vendored app we serve, not a page we wrote.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if !strings.HasPrefix(r.URL.Path, "/vnc/") {
			h.Set("Content-Security-Policy",
				"default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'self'; "+
					"img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; "+
					"script-src 'self'; worker-src 'self' blob:; connect-src 'self' ws: wss:")
		}
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		next.ServeHTTP(w, r)
	})
}

// withAuth gates every stateful route. The dashboard shell stays public — it
// carries no data, and the client-side setup/login screens are what onboard a
// first-time visitor. Setup and login are public by necessity (rate limited);
// everything else needs an identity, and mutating or membership-changing routes
// additionally need the rank minRoleFor assigns.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/ready" {
			// A guest has no credentials, so this route stays open — but only
			// to the networks a guest can dial from. Otherwise anything on the
			// LAN could mark a VM ready at an address of its choosing, and the
			// daemon would then dial that address for VNC and agent traffic.
			if !s.guestSource(r) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
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

// isShellPath reports whether a request may reach the dashboard without an
// identity. That is the bundle itself (index.html, assets, favicon) and every
// client-side route — /snapshots, /settings, … — because the SPA only renders
// chrome there; the data still comes from the authenticated /api. API, WebSocket
// and console routes are pinned explicitly so they can never slip through the
// gate: everything else falls to webui, which serves a real file if one exists
// and the SPA shell otherwise.
func isShellPath(p string) bool {
	for _, prefix := range []string{"/api/", "/internal/", "/websockify/", "/p/", "/d/", "/vnc/"} {
		if strings.HasPrefix(p, prefix) {
			return false
		}
	}
	return true
}

// fail reports an operational failure without echoing internals: the detail
// goes to the daemon log, the caller gets a generic message. Use it for
// store/db/fs errors; our own validation messages are safe to return verbatim.
func (s *Server) fail(w http.ResponseWriter, code int, msg string, err error) {
	if err != nil && s.log != nil {
		fmt.Fprintf(s.log, "api: %s: %v\n", msg, err)
	}
	writeJSON(w, code, map[string]string{"error": msg})
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
// Untagged rows predate scoping and stay visible everywhere; tagged rows only
// to their workspace, or to admins. (Desktops are stricter: an untagged one is
// unleased warm-pool capacity and is not listed at all — see Server.list.)
// Callers answer 404 for hidden rows so one workspace cannot probe another's
// inventory.
func wsVisible(rowWS, ws string, admin bool) bool {
	return admin || rowWS == "" || rowWS == ws
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Volume string   `json:"volume"`
		Image  string   `json:"image"`
		CPUs   uint     `json:"cpus"`
		MemMiB uint     `json:"mem_mib"`
		Allow  []string `json:"allow"`
		Deny   []string `json:"deny"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if !validResources(w, req.CPUs, req.MemMiB) {
		return
	}
	if !s.desktopQuotaOK(w, r) {
		return
	}
	pol := s.effectivePolicy(req.Allow, req.Deny)
	custom := req.CPUs > 0 || req.MemMiB > 0
	switch {
	case req.Volume != "":
		s.createWithVolume(w, r, req.Volume, req.Image, pol, req.CPUs, req.MemMiB)
	case req.Image != "":
		s.createImage(w, r, req.Image, pol, req.CPUs, req.MemMiB)
	case custom:
		// Default image, explicitly sized: boot it rather than hand out a
		// default-sized warm VM.
		s.createImage(w, r, "", pol, req.CPUs, req.MemMiB)
	default:
		vm, err := s.pool.Acquire(r.Context())
		if err != nil {
			s.fail(w, http.StatusServiceUnavailable, "could not start a desktop", err)
			return
		}
		ws, _ := s.callerScope(r)
		vm.SetWorkspace(ws)
		s.setPolicy(vm.ID, pol)
		s.recordDesktop(vm.ID, "", "ready", "", 0, ws)
		out := map[string]any{"id": vm.ID}
		screenLinks(out, vm)
		writeJSON(w, http.StatusCreated, out)
	}
}

// Resource requests are bounded: a member may size their own desktop, not hand
// the host something absurd.
const (
	maxDesktopCPUs   = 32
	maxDesktopMemMiB = 65536
	minDesktopMemMiB = 256
)

func validResources(w http.ResponseWriter, cpus, mem uint) bool {
	if cpus > maxDesktopCPUs {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": fmt.Sprintf("cpus must be 1-%d", maxDesktopCPUs)})
		return false
	}
	if mem != 0 && (mem < minDesktopMemMiB || mem > maxDesktopMemMiB) {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": fmt.Sprintf("memory must be %d-%d MiB", minDesktopMemMiB, maxDesktopMemMiB)})
		return false
	}
	return true
}

// desktopQuotaOK enforces the per-workspace desktop limit set in Settings
// (0 means unlimited).
func (s *Server) desktopQuotaOK(w http.ResponseWriter, r *http.Request) bool {
	limit := s.desktopLimit()
	if limit <= 0 {
		return true
	}
	ws, _ := s.callerScope(r)
	n := 0
	for _, info := range s.mgr.List() {
		if info.Workspace != "" && info.Workspace == ws {
			n++
		}
	}
	if n >= limit {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": fmt.Sprintf("this workspace already holds %d desktop(s), its limit; destroy one or raise the limit in Settings", limit),
		})
		return false
	}
	return true
}

// desktopLimit reads the configured cap; 0 (the default) is unlimited.
func (s *Server) desktopLimit() int {
	if s.catalog == nil {
		return 0
	}
	v, err := s.catalog.GetSetting(catalog.SettingMaxDesktopsPerWorkspace)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// createImage provisions a desktop from a named image. If it is the daemon's
// default image and the caller asked for no particular size it can come from
// the warm pool (instant); anything else boots on demand.
func (s *Server) createImage(w http.ResponseWriter, r *http.Request, name string, pol egress.Policy, cpus, mem uint) {
	// An unnamed create — or "default" — asks for the daemon's default image.
	// Resolve it before the pool check below, so "is this the default?" compares
	// like with like and an unnamed create can be served from the warm pool.
	deflt := desktop.DefaultFor(s.cfg.Image)
	name = desktop.CanonicalImage(name, s.cfg.Image)
	if name == deflt && cpus == 0 && mem == 0 {
		vm, err := s.pool.Acquire(r.Context())
		if err != nil {
			s.fail(w, http.StatusServiceUnavailable, "could not start a desktop", err)
			return
		}
		ws, _ := s.callerScope(r)
		vm.SetWorkspace(ws)
		s.setPolicy(vm.ID, pol)
		s.recordDesktop(vm.ID, "", "ready", "", 0, ws)
		out := map[string]any{"id": vm.ID}
		screenLinks(out, vm)
		writeJSON(w, http.StatusCreated, out)
		return
	}

	id := desktop.NewID()
	vm, err := s.mgr.StartDesktop(desktop.StartSpec{
		ID: id, ImageName: name, CPUs: cpus, MemMiB: mem,
	})
	if err != nil {
		// Include the cause: "could not boot that image" alone does not tell a
		// user that they typo'd a name or asked for one that is not installed.
		s.fail(w, http.StatusBadRequest, "could not boot that image: "+err.Error(), err)
		return
	}
	ws, _ := s.callerScope(r)
	vm.SetWorkspace(ws)
	s.setPolicy(id, pol)
	s.recordDesktop(id, "", "booting", "", 0, ws)
	wctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	if !s.waitOrFail(w, wctx, vm) {
		return
	}
	out := map[string]any{"id": vm.ID}
	screenLinks(out, vm)
	writeJSON(w, http.StatusCreated, out)
}

// screenLinks adds a desktop's streaming endpoints to a create response.
// A headless image has no screen, so the keys are omitted rather than handed
// out as links to a page that can never load.
func screenLinks(out map[string]any, vm *desktop.VM) {
	info := vm.Info()
	if info.Headless {
		return
	}
	out["vnc"] = "/d/" + info.ID
	out["ws"] = "/websockify/" + info.ID
}

// waitOrFail blocks until a freshly booted VM is ready, and on failure says
// which of the three things happened — the client gave up, the boot timed out,
// or the VM died — instead of one vague message for all of them. A VM that
// exits during boot is the common one (a missing kernel, a bad image), and it
// used to take the full timeout to report, which is why a failure looked like
// nothing happening at all.
func (s *Server) waitOrFail(w http.ResponseWriter, ctx context.Context, vm *desktop.VM) bool {
	if _, err := s.mgr.WaitReady(ctx, vm.ID); err != nil {
		_ = s.mgr.Destroy(vm.ID)
		switch {
		case errors.Is(err, context.Canceled):
			s.fail(w, http.StatusGatewayTimeout, "the desktop was still booting when the client disconnected", err)
		case errors.Is(err, context.DeadlineExceeded):
			s.fail(w, http.StatusGatewayTimeout, "the desktop did not become ready in time", err)
		default:
			s.fail(w, http.StatusBadGateway, "the desktop failed to boot: "+err.Error(), err)
		}
		return false
	}
	return true
}

// ImageStatus is one row of the Images page: an image this build knows about,
// whether it is installed, and what is happening to it.
type ImageStatus struct {
	Name     string `json:"name"`
	Headless bool   `json:"headless"`
	// Installed means the daemon can boot it right now.
	Installed bool `json:"installed"`
	// Pullable is false when there is nothing to download: either the image is
	// not published, or it was built on this machine and this binary has never
	// heard of it.
	Pullable bool `json:"pullable"`
	// State is "installed", "available", "pulling" or "failed".
	State string `json:"state"`
	// Detail explains a failure; Done and Total describe a download in flight.
	Detail string `json:"detail,omitempty"`
	Done   int64  `json:"done,omitempty"`
	Total  int64  `json:"total,omitempty"`
}

// imageStatuses merges the two things that describe an image: whether it is
// installed on this machine, and whether the registry this binary shipped with
// knows about it.
//
// The union is the point. The registry alone would hide an image built here from
// a local config; the installed list alone is why the page had nothing to say
// about an image that exists and could be fetched, but has not been.
func (s *Server) imageStatuses() []ImageStatus {
	installed := s.mgr.ImageMetas()
	out := make([]ImageStatus, 0, len(installed))
	known := make(map[string]bool, len(installed))

	for _, c := range imagecfg.Catalogue() {
		known[c.Name] = true
		st := ImageStatus{Name: c.Name, Headless: c.Headless, Pullable: c.Publish}
		if meta, ok := installed[c.Name]; ok {
			// The installed image is the authority on what it is: its meta.json
			// is what the daemon actually acts on.
			st.Installed, st.Headless, st.State = true, meta.Headless, "installed"
		} else {
			st.State = "available"
		}
		out = append(out, st)
	}
	for name, meta := range installed {
		if known[name] {
			continue
		}
		out = append(out, ImageStatus{
			Name: name, Headless: meta.Headless, Installed: true, State: "installed",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	for i := range out {
		st, ok := s.pulls.state(out[i].Name)
		if !ok {
			continue
		}
		if st.active {
			out[i].State, out[i].Done, out[i].Total = "pulling", st.done, st.total
			continue
		}
		if st.err != nil && !out[i].Installed {
			out[i].State, out[i].Detail = "failed", st.err.Error()
		}
	}
	return out
}

// imagePull starts a background download of a published image.
//
// It returns as soon as the job is registered: an image is hundreds of MB, and a
// request that blocks for minutes is a request that looks like nothing happening
// — which is exactly how a failed create read before the wait learned to fail
// fast.
func (s *Server) imagePull(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, admin := s.callerScope(r); !admin {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "installing an image changes the daemon for everyone; ask an admin",
		})
		return
	}
	c, ok := imagecfg.Find(imagecfg.Catalogue(), name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("no image called %q in this build", name),
		})
		return
	}
	if !c.Publish {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("%q is not published — build it from a checkout with `warmbox image build %s`", name, name),
		})
		return
	}
	if r.URL.Query().Get("force") == "" && slices.Contains(s.mgr.Images(), name) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("%q is already installed (add ?force=1 to replace it)", name),
		})
		return
	}
	job, started := s.pulls.start(name)
	if !started {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("%q is already downloading", name),
		})
		return
	}
	fmt.Fprintf(s.log, "api: pulling image %s for %s\n", name, runtime.GOARCH)
	go s.runImagePull(job, c)
	w.WriteHeader(http.StatusAccepted)
}

// runImagePull is the work behind POST /api/images/{name}/pull: resolve the
// published artifact and its checksum, then download and expand it where the
// daemon will find it.
func (s *Server) runImagePull(job *imagePull, c imagecfg.Config) {
	url := release.ImageURL(c.Name, runtime.GOARCH)
	sha, err := desktop.FetchChecksum(url + ".sha256")
	if err != nil {
		err = fmt.Errorf("no published image %q for %s/%s", c.Name, runtime.GOOS, runtime.GOARCH)
	} else {
		err = desktop.FetchImageProgress(s.cfg.ImageDir, c.Name, url, sha, job.report, nil)
	}
	if err != nil {
		fmt.Fprintf(s.log, "image %s: pull failed: %v\n", c.Name, err)
	} else {
		fmt.Fprintf(s.log, "image %s: installed\n", c.Name)
	}
	job.finish(err)
}

// pullRegistry tracks image downloads: at most one in flight per image, with the
// last outcome kept so the dashboard can show it.
type pullRegistry struct {
	mu   sync.Mutex
	jobs map[string]*imagePull
}

func newPullRegistry() *pullRegistry {
	return &pullRegistry{jobs: map[string]*imagePull{}}
}

// start registers a pull for name, or reports false when one is already running.
func (r *pullRegistry) start(name string) (*imagePull, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.jobs[name]; ok && p.snapshot().active {
		return nil, false
	}
	p := &imagePull{name: name, active: true}
	r.jobs[name] = p
	return p, true
}

// state reports what is known about this image's current or last pull.
func (r *pullRegistry) state(name string) (pullState, bool) {
	r.mu.Lock()
	p, ok := r.jobs[name]
	r.mu.Unlock()
	if !ok {
		return pullState{}, false
	}
	return p.snapshot(), true
}

// imagePull is one download: its progress while it runs, and how it ended.
type imagePull struct {
	name string

	mu     sync.Mutex
	done   int64
	total  int64
	active bool
	err    error
}

// pullState is a snapshot of a pull, safe to hand out.
type pullState struct {
	active bool
	done   int64
	total  int64
	err    error
}

func (p *imagePull) snapshot() pullState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return pullState{active: p.active, done: p.done, total: p.total, err: p.err}
}

// report records download progress (called from the download loop).
func (p *imagePull) report(done, total int64) {
	p.mu.Lock()
	p.done, p.total = done, total
	p.mu.Unlock()
}

// finish ends the job, successfully or not.
func (p *imagePull) finish(err error) {
	p.mu.Lock()
	p.active, p.err = false, err
	p.mu.Unlock()
}

// images lists the guest images the daemon can boot, plus the ones this build
// knows about but that are not installed. images and image_meta are the plain
// shape older clients read; catalogue is what the dashboard renders.
func (s *Server) images(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"images":     s.mgr.Images(),
		"image_meta": s.mgr.ImageMetas(),
		"catalogue":  s.imageStatuses(),
	})
}

// createWithVolume boots a VM whose writable layer is a persistent volume.
// Volume-backed VMs cannot come from the warm pool (the disk must be attached
// before boot), so they cold-boot. The volume must be visible to the caller.
//
// imageName is the guest image the caller asked for: attaching a volume does not
// mean "whatever the daemon defaults to", and quietly booting a different image
// than the one on screen is the kind of thing nobody notices until they are
// staring at the wrong desktop.
func (s *Server) createWithVolume(w http.ResponseWriter, r *http.Request, name, imageName string, pol egress.Policy, cpus, mem uint) {
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
	volumeImage, err := s.volumes.EnsureLocal(ctx, name)
	if err != nil {
		s.fail(w, http.StatusBadGateway, "could not prepare the volume", err)
		return
	}
	id := desktop.NewID()
	if err := s.lockVolume(name, id); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	vm, err := s.mgr.StartDesktop(desktop.StartSpec{
		ID: id, VolumeName: name, VolumeImage: volumeImage,
		ImageName: imageName, CPUs: cpus, MemMiB: mem,
	})
	if err != nil {
		s.unlockVolume(name, id)
		s.fail(w, http.StatusBadRequest, "could not boot that image: "+err.Error(), err)
		return
	}
	vm.SetWorkspace(ws)
	s.setPolicy(id, pol)
	s.recordDesktop(id, name, "booting", "", 0, ws)
	wctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if !s.waitOrFail(w, wctx, vm) {
		// The destroy hook commits and releases the volume.
		return
	}
	out := map[string]any{"id": vm.ID, "volume": name}
	screenLinks(out, vm)
	writeJSON(w, http.StatusCreated, out)
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

// headlessHTML is what /d/ shows for a desktop with no screen: the link is
// still honoured rather than 404'd (bookmarks and API clients hold it), but
// there is nothing to stream.
const headlessHTML = `<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>warmbox</title>
<style>html,body{margin:0;height:100%%;background:#111;color:#c9d1d9;font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
main{max-width:34rem;margin:0 auto;padding:14vh 1.5rem 0}
h1{font-size:1.05rem;font-weight:600;color:#fff;margin:0 0 .6rem}
p{margin:0 0 .8rem}
code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;background:#1c2128;padding:.1rem .35rem;border-radius:.25rem}
a{color:#58a6ff}</style></head>
<body><main>
<h1>This desktop has no screen</h1>
<p><code>%s</code> booted a headless image, so there is no desktop to stream.</p>
<p>It is still fully usable through the agent API: run commands with
<code>POST /api/desktops/%s/exec</code>, move files with <code>/files</code>,
or start something long-lived with <code>/runs</code>.</p>
<p><a href="/desktops">Back to desktops</a></p>
</main></body></html>`

// dash serves the short desktop URL. A request that still carries ?token= is
// redirected to the bare path only when a login cookie already proves the
// session — otherwise the query token IS the credential (API tokens don't set
// cookies) and stripping it would lock the client out.
func (s *Server) dash(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	vm, ok := s.visibleVM(w, r, id)
	if !ok {
		return
	}
	if vm.Headless {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, headlessHTML, id, id)
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
		// Unleased VMs are warm-pool capacity, not desktops: they show up in the
		// daemon's pool stats, not in anyone's desktop list.
		if info.Workspace == "" {
			continue
		}
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
	vm, ok := s.visibleVM(w, r, id)
	if !ok {
		return
	}
	// Warm-pool capacity is the daemon's, not a user's: tearing it down would
	// just make the pool boot another one.
	if vm.Info().Workspace == "" {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "that vm is warm-pool capacity, not one of your desktops",
		})
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

// pause freezes a desktop's vCPUs. Its memory (and running session) survive;
// what is reclaimed is CPU. Destroy is what hands RAM back.
func (s *Server) pause(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	vm, ok := s.visibleVM(w, r, id)
	if !ok {
		return
	}
	// Unleased VMs are warm-pool capacity: the pool hands them out on the next
	// create, so freezing one would deliver a frozen desktop.
	if vm.Info().Workspace == "" {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "that vm is warm-pool capacity, not one of your desktops",
		})
		return
	}
	if err := s.mgr.Pause(id); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "paused", "id": id})
}

// resume thaws a paused desktop; the guest continues where it left off.
func (s *Server) resume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.visibleVM(w, r, id); !ok {
		return
	}
	if err := s.mgr.Resume(id); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "running", "id": id})
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
	vm, ok := s.visibleVM(w, r, id)
	if !ok {
		return
	}
	// Nothing to proxy — the guest runs no X server, so this will never become
	// ready. 409 rather than 503 so a client can tell the two apart.
	if vm.Headless {
		http.Error(w, "this desktop is headless: there is no screen to stream", http.StatusConflict)
		return
	}
	target := vm.Target(s.cfg.GuestVNCPort)
	if target == "" {
		http.Error(w, "desktop not ready", http.StatusServiceUnavailable)
		return
	}
	vnc.Proxy(w, r, target, s.log)
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
		// Flush immediately so Server-Sent Events (run/session output) stream
		// instead of being buffered until the response ends.
		FlushInterval: -1,
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
			s.fail(w, http.StatusBadGateway, "could not reach the desktop's agent", err)
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

// --- runs / sessions: proxy to the guest agent, workspace-scoped via visibleVM ---

func (s *Server) agentRunCreate(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/runs")
}
func (s *Server) agentRunList(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/runs")
}
func (s *Server) agentRunGet(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/runs/"+r.PathValue("run"))
}
func (s *Server) agentRunKill(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/runs/"+r.PathValue("run"))
}
func (s *Server) agentRunStdin(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/runs/"+r.PathValue("run")+"/stdin")
}
func (s *Server) agentRunStream(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/runs/"+r.PathValue("run")+"/stream")
}
func (s *Server) agentSessionCreate(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/sessions")
}
func (s *Server) agentSessionInput(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/sessions/"+r.PathValue("run")+"/input")
}
func (s *Server) agentSessionOutput(w http.ResponseWriter, r *http.Request) {
	s.agentProxy(w, r, r.PathValue("id"), "/sessions/"+r.PathValue("run")+"/output")
}

// expose reverse-proxies an HTTP request to a port inside a guest, so a server
// an agent runs there (a dashboard, an app) can be opened and shared as a URL.
// GET /p/<id>/<port>/... -> http://<guest-ip>:<port>/...
func (s *Server) expose(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	port := r.PathValue("port")
	vm, ok := s.visibleVM(w, r, id)
	if !ok {
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
			s.fail(w, http.StatusBadGateway, "could not reach that port on the desktop", err)
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
	// Cloning copies the source's bytes, so the source has to belong to the
	// caller: without this a member could clone another workspace's volume (or
	// snapshot) into their own and then read the files.
	ws, admin := s.callerScope(r)
	if s.catalog != nil {
		if req.From != "" {
			v, err := s.catalog.GetVolume(req.From)
			if err != nil || !wsVisible(v.WorkspaceID, ws, admin) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown volume"})
				return
			}
		}
		if req.FromSnapshot != "" {
			sn, err := s.catalog.GetSnapshot(req.FromSnapshot)
			if err != nil || !wsVisible(sn.WorkspaceID, ws, admin) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown snapshot"})
				return
			}
		}
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
			s.fail(w, http.StatusInternalServerError, "could not list volumes", err)
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
		s.fail(w, http.StatusInternalServerError, "could not list volumes", err)
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
		s.fail(w, http.StatusInternalServerError, "could not delete volume", err)
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
			s.fail(w, http.StatusInternalServerError, "could not list snapshots", err)
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
		s.fail(w, http.StatusInternalServerError, "could not list snapshots", err)
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
	// Deleting is destructive, so prove the snapshot is the caller's before
	// touching it; a hidden one 404s like every other scoped resource.
	if s.catalog != nil {
		ws, admin := s.callerScope(r)
		sn, err := s.catalog.GetSnapshot(id)
		if err != nil || !wsVisible(sn.WorkspaceID, ws, admin) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown snapshot"})
			return
		}
	}
	if err := s.volumes.DeleteSnapshot(r.Context(), id); err != nil {
		s.fail(w, http.StatusInternalServerError, "could not delete snapshot", err)
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
