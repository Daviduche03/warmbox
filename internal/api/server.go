// Package api exposes the orchestrator over HTTP: desktop provisioning plus
// the noVNC assets and the WebSocket-to-VNC bridge.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"runmesh/workspace/internal/catalog"
	"runmesh/workspace/internal/desktop"
	"runmesh/workspace/internal/netutil"
	"runmesh/workspace/internal/pool"
	"runmesh/workspace/internal/vnc"
	"runmesh/workspace/internal/volume"
)

// Server wires the manager, warm pool, volume store and VNC bridge into an
// http.Handler.
type Server struct {
	mgr     *desktop.Manager
	pool    *pool.Pool
	cfg     *desktop.Config
	volumes *volume.Store
	catalog *catalog.DB
	log     io.Writer
}

// New builds a Server. volumes and cat may be nil when those are disabled.
func New(mgr *desktop.Manager, p *pool.Pool, cfg *desktop.Config, volumes *volume.Store, cat *catalog.DB, log io.Writer) *Server {
	if log == nil {
		log = io.Discard
	}
	return &Server{mgr: mgr, pool: p, cfg: cfg, volumes: volumes, catalog: cat, log: log}
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/desktops", s.create)
	mux.HandleFunc("GET /api/desktops", s.list)
	mux.HandleFunc("GET /api/desktops/{id}", s.get)
	mux.HandleFunc("DELETE /api/desktops/{id}", s.destroy)

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

	// Guest readiness callback (guest -> host).
	mux.HandleFunc("GET /internal/ready", s.ready)

	// WebSocket bridge to the guest VNC server.
	mux.HandleFunc("GET /websockify/{id}", s.websockify)

	// Publish a guest HTTP server (e.g. an agent-built dashboard) at a link.
	mux.HandleFunc("GET /p/{id}/{port}/", s.expose)

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
	var req struct {
		Volume string `json:"volume"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.Volume != "" {
		s.createWithVolume(w, r, req.Volume)
		return
	}

	vm, err := s.pool.Acquire(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	s.recordDesktop(vm.ID, "", "busy", "", 0)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":  vm.ID,
		"vnc": "/vnc/" + vm.ID + "/vnc.html?autoconnect=1&resize=scale&path=/websockify/" + vm.ID + s.tokenSuffix(),
		"ws":  "/websockify/" + vm.ID,
	})
}

// createWithVolume boots a VM whose writable layer is a persistent volume.
// Volume-backed VMs cannot come from the warm pool (the disk must be attached
// before boot), so they cold-boot.
func (s *Server) createWithVolume(w http.ResponseWriter, r *http.Request, name string) {
	if !s.volumesEnabled(w) {
		return
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
	s.recordDesktop(id, name, "booting", "", 0)
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
		"vnc":    "/vnc/" + vm.ID + "/vnc.html?autoconnect=1&resize=scale&path=/websockify/" + vm.ID + s.tokenSuffix(),
		"ws":     "/websockify/" + vm.ID,
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
	vm, ok := s.mgr.Get(id)
	if !ok {
		http.Error(w, "unknown desktop", http.StatusNotFound)
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
				return netutil.Dial(addr)
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
				return netutil.Dial(addr)
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

// --- volumes ---

// recordDesktop keeps the SQLite catalog in sync with the in-memory manager.
func (s *Server) recordDesktop(id, vol, state, ip string, pid int) {
	if s.catalog == nil || id == "" {
		return
	}
	_ = s.catalog.UpsertDesktop(&catalog.Desktop{ID: id, Volume: vol, State: state, GuestIP: ip, PID: pid})
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
	if s.catalog != nil {
		_ = s.catalog.UpsertVolume(&catalog.Volume{
			Name:      m.Name,
			Size:      m.Size,
			ChunkSize: m.ChunkSize,
			Remote:    s.cfg.VolumePrefix + "/" + m.Name,
			From:      m.From,
			CreatedAt: m.Created,
		})
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) volumeList(w http.ResponseWriter, r *http.Request) {
	if !s.volumesEnabled(w) {
		return
	}
	if s.catalog != nil {
		vs, err := s.catalog.ListVolumes()
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
		if err != nil {
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
	m, err := s.volumes.Clone(r.Context(), r.PathValue("name"), req.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if s.catalog != nil {
		_ = s.catalog.UpsertVolume(&catalog.Volume{
			Name:      m.Name,
			Size:      m.Size,
			ChunkSize: m.ChunkSize,
			Remote:    s.cfg.VolumePrefix + "/" + m.Name,
			From:      m.From,
			CreatedAt: m.Created,
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
	snap, err := s.volumes.Snapshot(r.Context(), req.Volume, req.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if s.catalog != nil {
		_ = s.catalog.UpsertSnapshot(&catalog.Snapshot{
			ID: snap.ID, Volume: snap.Volume, Size: snap.Size,
			ChunkSize: snap.ChunkSize, CreatedAt: snap.Created,
		})
	}
	writeJSON(w, http.StatusCreated, snap)
}

func (s *Server) snapshotList(w http.ResponseWriter, r *http.Request) {
	if !s.volumesEnabled(w) {
		return
	}
	if s.catalog != nil {
		snaps, err := s.catalog.ListSnapshots(r.URL.Query().Get("volume"))
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
