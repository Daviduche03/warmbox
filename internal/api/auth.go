package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"runmesh/workspace/internal/catalog"
)

// Authentication model.
//
//   - Fresh installs have no users. Until the first account is created the
//     daemon behaves exactly as before (shared token from cfg.Token), and the
//     setup endpoints are public.
//   - The first account is created through POST /api/setup with an
//     organisation/workspace name. That user becomes owner, and from then on
//     the shared token is ignored everywhere.
//   - Browsers authenticate with an opaque session cookie (the WebSocket and
//     console routes cannot send headers, so they inherit it like before).
//     CLI and scripts use per-user API tokens as Bearer credentials, which
//     also work as ?token= on console URLs.
//
// Roles rank viewer < member < admin < owner. Reads need viewer, resource
// mutations need member, user management needs admin, and touching owners
// needs owner.
const (
	sessionCookie   = "warmbox_session"
	legacyCookie    = "warmbox_token"
	sessionTTL      = 30 * 24 * time.Hour
	minPasswordLen  = 8
	bcryptCost      = 12
	loginMaxPerMin  = 10
	setupMaxPerHour = 20
)

// authCtx is the identity resolved for a request.
type authCtx struct {
	userID      string
	email       string
	name        string
	role        string
	workspaceID string
	// via is "session", "token" or "legacy".
	via string
}

type ctxKey struct{}

// authOf returns the identity attached by the middleware, if any.
func authOf(r *http.Request) *authCtx {
	ac, _ := r.Context().Value(ctxKey{}).(*authCtx)
	return ac
}

// usersExist reports whether anyone has completed setup. A lookup failure
// falls back to legacy behaviour rather than bricking the daemon.
func (s *Server) usersExist() bool {
	if s.catalog == nil {
		return false
	}
	n, err := s.catalog.CountUsers()
	if err != nil {
		return false
	}
	return n > 0
}

// authenticate resolves who is calling. It returns false when nobody valid did.
func (s *Server) authenticate(r *http.Request) (*authCtx, bool) {
	if s.catalog != nil && s.usersExist() {
		return s.authenticateUsers(r)
	}
	return s.authenticateLegacy(r)
}

// authenticateUsers validates session cookies and per-user API tokens.
func (s *Server) authenticateUsers(r *http.Request) (*authCtx, bool) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if ac, ok := s.authSession(r, c.Value); ok {
			ac.via = "session"
			return ac, true
		}
	}
	token := bearerToken(r)
	queryToken := r.URL.Query().Get("token")
	if token == "" {
		token = queryToken
	}
	if token == "" {
		return nil, false
	}
	if ac, ok := s.authAPIToken(r, token); ok {
		ac.via = "token"
		return ac, true
	}
	return nil, false
}

// authSession validates a session cookie and loads its user and role.
func (s *Server) authSession(r *http.Request, raw string) (*authCtx, bool) {
	sum := sha256.Sum256([]byte(raw))
	sess, err := s.catalog.GetSession(hex.EncodeToString(sum[:]))
	if err != nil {
		return nil, false
	}
	return s.ctxForUser(r, sess.UserID, sess.WorkspaceID)
}

// authAPIToken validates a user API token.
func (s *Server) authAPIToken(r *http.Request, raw string) (*authCtx, bool) {
	sum := sha256.Sum256([]byte(raw))
	userID, err := s.catalog.GetAPITokenOwner(hex.EncodeToString(sum[:]))
	if err != nil {
		return nil, false
	}
	s.catalog.TouchAPIToken(hex.EncodeToString(sum[:]))
	ws := r.Header.Get("X-Workspace-ID")
	if ws == "" {
		memberships, err := s.catalog.MembershipsForUser(userID)
		if err != nil || len(memberships) == 0 {
			return nil, false
		}
		ws = memberships[0].ID
	}
	return s.ctxForUser(r, userID, ws)
}

// ctxForUser builds the request identity, honouring an explicit workspace
// switch when the caller belongs to it.
func (s *Server) ctxForUser(r *http.Request, userID, workspaceID string) (*authCtx, bool) {
	u, err := s.catalog.GetUserByID(userID)
	if err != nil {
		return nil, false
	}
	if want := r.Header.Get("X-Workspace-ID"); want != "" {
		if _, err := s.catalog.GetMembership(want, userID); err == nil {
			workspaceID = want
		}
	}
	role, err := s.catalog.GetMembership(workspaceID, userID)
	if err != nil {
		return nil, false
	}
	return &authCtx{
		userID: u.ID, email: u.Email, name: u.Name,
		role: role, workspaceID: workspaceID,
	}, true
}

// authenticateLegacy is the pre-setup behaviour: the shared token from query,
// header or cookie. An empty configured token leaves the daemon open, as before.
func (s *Server) authenticateLegacy(r *http.Request) (*authCtx, bool) {
	if s.cfg.Token == "" {
		return &authCtx{via: "legacy"}, true
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		token = bearerToken(r)
	}
	if token == "" {
		if c, err := r.Cookie(legacyCookie); err == nil {
			token = c.Value
		}
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Token)) != 1 {
		return nil, false
	}
	return &authCtx{via: "legacy"}, true
}

func bearerToken(r *http.Request) string {
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
		return strings.TrimPrefix(a, "Bearer ")
	}
	return ""
}

// hasLoginCookie reports whether the request carries any cookie that proves a
// login (session or legacy). The console redirect uses it to decide whether
// stripping ?token= from the URL is safe.
func (s *Server) hasLoginCookie(r *http.Request) bool {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		sum := sha256.Sum256([]byte(c.Value))
		if _, err := s.catalog.GetSession(hex.EncodeToString(sum[:])); err == nil {
			return true
		}
	}
	if s.catalog == nil || !s.usersExist() {
		if c, err := r.Cookie(legacyCookie); err == nil && c.Value != "" {
			return subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.cfg.Token)) == 1
		}
	}
	return false
}

// minRoleFor returns the role rank a route needs. Console routes need member
// because opening a guest console is full control of that desktop, not reading.
func minRoleFor(method, path string) int {
	switch {
	case strings.HasPrefix(path, "/api/users"):
		return catalog.RoleRank(catalog.RoleAdmin)
	case path == "/api/workspaces" || strings.HasPrefix(path, "/api/workspaces/"):
		return catalog.RoleRank(catalog.RoleViewer)
	case strings.HasPrefix(path, "/d/") ||
		strings.HasPrefix(path, "/websockify/") ||
		strings.HasPrefix(path, "/vnc/") ||
		strings.HasPrefix(path, "/p/"):
		return catalog.RoleRank(catalog.RoleMember)
	case strings.HasPrefix(path, "/api/"):
		if method == "GET" || method == "HEAD" || method == "OPTIONS" {
			return catalog.RoleRank(catalog.RoleViewer)
		}
		return catalog.RoleRank(catalog.RoleMember)
	}
	return catalog.RoleRank(catalog.RoleViewer)
}

// checkOrigin blocks cross-site form posts against cookie sessions. Bearer and
// query credentials are unaffected (curl and scripts send no Origin), and
// requests without any origin headers pass — browsers always send one on
// credentialed mutations.
func checkOrigin(r *http.Request, viaCookie bool) bool {
	if !viaCookie || r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		if ref := r.Header.Get("Referer"); ref != "" {
			origin = ref
		} else {
			return true
		}
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	reqHost := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		reqHost = h
	}
	return strings.EqualFold(host, reqHost)
}

// --- rate limiting (login + setup only) ---

type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	max    int
	window time.Duration
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{hits: map[string][]time.Time{}, max: max, window: window}
}

func (l *rateLimiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

var (
	loginLimiter = newRateLimiter(loginMaxPerMin, time.Minute)
	setupLimiter = newRateLimiter(setupMaxPerHour, time.Hour)
)

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if i := strings.Index(fwd, ","); i >= 0 {
			return strings.TrimSpace(fwd[:i])
		}
		return strings.TrimSpace(fwd)
	}
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// --- token minting ---

func mintToken(nbytes int) (raw, hash string, err error) {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(sum[:]), nil
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, raw string) {
	c := &http.Cookie{
		Name:     sessionCookie,
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	}
	if r.TLS != nil {
		c.Secure = true
	}
	http.SetCookie(w, c)
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// openSession creates a session for a user and drops the cookie.
func (s *Server) openSession(w http.ResponseWriter, r *http.Request, userID, workspaceID string) error {
	raw, hash, err := mintToken(32)
	if err != nil {
		return err
	}
	if err := s.catalog.CreateSession(hash, userID, workspaceID, time.Now().Add(sessionTTL)); err != nil {
		return err
	}
	setSessionCookie(w, r, raw)
	return nil
}

// --- handlers ---

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func validEmail(email string) bool {
	email = strings.TrimSpace(email)
	at := strings.Index(email, "@")
	return at > 0 && strings.Contains(email[at:], ".")
}

// GET /api/setup/status — public. Reports whether first-run setup is open.
func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"needs_setup": !s.usersExist()})
}

// POST /api/setup — public only while no users exist. Creates the workspace,
// the owner account and a session in one step.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "account store unavailable"})
		return
	}
	if s.usersExist() {
		http.NotFound(w, r)
		return
	}
	if !setupLimiter.allow(clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts, try again later"})
		return
	}
	var req struct {
		Name      string `json:"name"`
		Email     string `json:"email"`
		Password  string `json:"password"`
		Workspace string `json:"workspace"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.TrimSpace(req.Email)
	req.Workspace = strings.TrimSpace(req.Workspace)
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if !validEmail(req.Email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a valid email is required"})
		return
	}
	if len(req.Password) < minPasswordLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		return
	}
	if req.Workspace == "" {
		req.Workspace = "default"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not secure password"})
		return
	}
	ws, err := s.catalog.CreateWorkspace(req.Workspace)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create workspace"})
		return
	}
	u, err := s.catalog.CreateUser(req.Name, req.Email, string(hash))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not create account"})
		return
	}
	if err := s.catalog.AddMember(ws.ID, u.ID, catalog.RoleOwner); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not grant ownership"})
		return
	}
	if err := s.openSession(w, r, u.ID, ws.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not start session"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"user":      map[string]string{"id": u.ID, "name": u.Name, "email": u.Email},
		"workspace": map[string]string{"id": ws.ID, "name": ws.Name},
		"role":      catalog.RoleOwner,
	})
}

// POST /api/login — public, rate limited. Errors never say which half failed.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "account store unavailable"})
		return
	}
	if !loginLimiter.allow(clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts, try again later"})
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	u, hash, err := s.catalog.GetUserByEmail(req.Email)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
		return
	}
	memberships, err := s.catalog.MembershipsForUser(u.ID)
	if err != nil || len(memberships) == 0 {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "account has no workspace"})
		return
	}
	ws := memberships[0]
	role, err := s.catalog.GetMembership(ws.ID, u.ID)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "account has no workspace"})
		return
	}
	if err := s.openSession(w, r, u.ID, ws.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not start session"})
		return
	}
	writeJSON(w, http.StatusOK, meShape(u, ws, role, memberships))
}

// POST /api/logout — revokes the session.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		sum := sha256.Sum256([]byte(c.Value))
		_ = s.catalog.DeleteSession(hex.EncodeToString(sum[:]))
	}
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

func meShape(u *catalog.User, ws *catalog.Workspace, role string, all []*catalog.Workspace) map[string]any {
	wss := make([]map[string]string, 0, len(all))
	for _, x := range all {
		wss = append(wss, map[string]string{"id": x.ID, "name": x.Name})
	}
	return map[string]any{
		"user":       map[string]string{"id": u.ID, "name": u.Name, "email": u.Email},
		"workspace":  map[string]string{"id": ws.ID, "name": ws.Name},
		"role":       role,
		"workspaces": wss,
	}
}

// GET /api/me — who am I, where, and as what.
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	ac := authOf(r)
	if ac == nil || ac.userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	u, err := s.catalog.GetUserByID(ac.userID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	memberships, _ := s.catalog.MembershipsForUser(u.ID)
	var ws *catalog.Workspace
	for _, x := range memberships {
		if x.ID == ac.workspaceID {
			ws = x
		}
	}
	if ws == nil && len(memberships) > 0 {
		ws = memberships[0]
	}
	if ws == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "account has no workspace"})
		return
	}
	role, _ := s.catalog.GetMembership(ws.ID, u.ID)
	writeJSON(w, http.StatusOK, meShape(u, ws, role, memberships))
}

// PATCH /api/me/password — {current, next}.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	ac := authOf(r)
	if ac == nil || ac.userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if len(req.Next) < minPasswordLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		return
	}
	_, hash, err := s.catalog.GetUserByEmail(ac.email)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Current)) != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "current password is incorrect"})
		return
	}
	next, err := bcrypt.GenerateFromPassword([]byte(req.Next), bcryptCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not secure password"})
		return
	}
	if err := s.catalog.SetPasswordHash(ac.userID, string(next)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update password"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "password updated"})
}

// GET /api/users — admin+. Members of the caller's workspace.
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	ac := authOf(r)
	members, err := s.catalog.ListMembers(ac.workspaceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list users"})
		return
	}
	out := make([]map[string]string, 0, len(members))
	for _, m := range members {
		out = append(out, map[string]string{
			"id": m.ID, "name": m.Name, "email": m.Email, "role": m.Role,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

// POST /api/users — admin+. New users join the caller's workspace with a role
// strictly below the creator's (owners excepted), so admins can grow the team
// but cannot mint peers or owners.
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	ac := authOf(r)
	var req struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	role, err := catalog.ParseRole(req.Role)
	if err != nil {
		role = catalog.RoleMember
	}
	if catalog.RoleRank(role) >= catalog.RoleRank(ac.role) && ac.role != catalog.RoleOwner {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot grant a role at or above your own"})
		return
	}
	if !validEmail(req.Email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a valid email is required"})
		return
	}
	if len(req.Password) < minPasswordLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not secure password"})
		return
	}
	u, err := s.catalog.CreateUser(req.Name, req.Email, string(hash))
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "email is already registered"})
		return
	}
	if err := s.catalog.AddMember(ac.workspaceID, u.ID, role); err != nil {
		_ = s.catalog.DeleteUser(u.ID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not add to workspace"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"user": map[string]string{"id": u.ID, "name": u.Name, "email": u.Email, "role": role},
	})
}

// targetGuard loads the membership being changed and enforces the hierarchy:
// nobody touches a higher rank, only owners touch owners, and the workspace
// always keeps at least one owner.
func (s *Server) targetGuard(w http.ResponseWriter, r *http.Request, ac *authCtx, verb string) (targetID, targetRole string, ok bool) {
	targetID = r.PathValue("id")
	if targetID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user id required"})
		return "", "", false
	}
	if targetID == ac.userID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "use your own account settings instead"})
		return "", "", false
	}
	var err error
	targetRole, err = s.catalog.GetMembership(ac.workspaceID, targetID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return "", "", false
	}
	if catalog.RoleRank(targetRole) > catalog.RoleRank(ac.role) ||
		(targetRole == catalog.RoleOwner && ac.role != catalog.RoleOwner) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot " + verb + " a user above your role"})
		return "", "", false
	}
	return targetID, targetRole, true
}

// PATCH /api/users/{id} — {role}. Admin+, owner-only for the owner rank, and
// the last owner cannot be demoted.
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	ac := authOf(r)
	targetID, targetRole, ok := s.targetGuard(w, r, ac, "change")
	if !ok {
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	role, err := catalog.ParseRole(req.Role)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown role"})
		return
	}
	if role == catalog.RoleOwner && ac.role != catalog.RoleOwner {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only owners can grant ownership"})
		return
	}
	if catalog.RoleRank(role) >= catalog.RoleRank(ac.role) && ac.role != catalog.RoleOwner {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot grant a role at or above your own"})
		return
	}
	if targetRole == catalog.RoleOwner && role != catalog.RoleOwner {
		if n, _ := s.catalog.CountOwners(ac.workspaceID); n <= 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the workspace needs at least one owner"})
			return
		}
	}
	if err := s.catalog.SetMemberRole(ac.workspaceID, targetID, role); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update role"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "role updated", "role": role})
}

// DELETE /api/users/{id} — admin+. Same hierarchy, plus the last owner stays.
func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	ac := authOf(r)
	targetID, targetRole, ok := s.targetGuard(w, r, ac, "remove")
	if !ok {
		return
	}
	if targetRole == catalog.RoleOwner {
		if n, _ := s.catalog.CountOwners(ac.workspaceID); n <= 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the workspace needs at least one owner"})
			return
		}
	}
	if err := s.catalog.DeleteUser(targetID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not remove user"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "user removed"})
}

// GET /api/workspaces — workspaces the caller belongs to.
func (s *Server) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	ac := authOf(r)
	memberships, err := s.catalog.MembershipsForUser(ac.userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list workspaces"})
		return
	}
	out := make([]map[string]string, 0, len(memberships))
	for _, x := range memberships {
		role, _ := s.catalog.GetMembership(x.ID, ac.userID)
		out = append(out, map[string]string{"id": x.ID, "name": x.Name, "role": role})
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": out})
}

// GET /api/tokens — the caller's own API tokens (hashes never leave).
func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	ac := authOf(r)
	tokens, err := s.catalog.ListAPITokens(ac.userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list tokens"})
		return
	}
	out := make([]map[string]any, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, map[string]any{
			"id": t.ID, "name": t.Name, "prefix": t.Prefix,
			"created_at": t.CreatedAt, "last_used_at": nullableTime(t.LastUsedAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

func nullableTime(t interface{ IsZero() bool }) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// POST /api/tokens — {name}. Returns the plaintext once; it is never shown again.
func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	ac := authOf(r)
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	raw, _, err := mintToken(24)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not mint token"})
		return
	}
	plaintext := "wb_" + raw
	sum := sha256Sum(plaintext)
	t, err := s.catalog.CreateAPIToken(ac.userID, req.Name, sum, plaintext[:12])
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not store token"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": t.ID, "name": t.Name, "prefix": t.Prefix, "token": plaintext,
	})
}

func sha256Sum(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// DELETE /api/tokens/{id} — revoke your own token; admins may revoke anyone's.
func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	ac := authOf(r)
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token id required"})
		return
	}
	scope := ac.userID
	if catalog.RoleRank(ac.role) >= catalog.RoleRank(catalog.RoleAdmin) {
		scope = ""
	}
	if err := s.catalog.DeleteAPIToken(id, scope); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not revoke token"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "token revoked"})
}
