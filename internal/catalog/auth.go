package catalog

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Roles rank from least to most privilege. Viewer reads, member operates,
// admin manages users, owner owns the workspace (including other owners).
const (
	RoleViewer = "viewer"
	RoleMember = "member"
	RoleAdmin  = "admin"
	RoleOwner  = "owner"
)

// RoleRank orders roles for comparison. Unknown roles rank below viewer.
func RoleRank(role string) int {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleOwner:
		return 4
	case RoleAdmin:
		return 3
	case RoleMember:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

// ParseRole normalises a role name, rejecting anything unknown.
func ParseRole(role string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleOwner:
		return RoleOwner, nil
	case RoleAdmin:
		return RoleAdmin, nil
	case RoleMember:
		return RoleMember, nil
	case RoleViewer:
		return RoleViewer, nil
	}
	return "", fmt.Errorf("catalog: unknown role %q", role)
}

// User is a login identity. Passwords never live here — only their hash.
type User struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

// Workspace groups users. Resources (volumes, desktops) are not yet scoped to
// one — memberships record who belongs where and with what role, so scoping
// can be added without reworking identity first.
type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Member is a user plus their role in a workspace.
type Member struct {
	User
	Role string `json:"role"`
}

// Session is a login session. Only the token hash is stored.
type Session struct {
	TokenHash   string    `json:"-"`
	UserID      string    `json:"user_id"`
	WorkspaceID string    `json:"workspace_id"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
}

// APIToken is a long-lived credential for CLI/scripts. The plaintext is shown
// once at creation; afterwards only the prefix identifies it.
type APIToken struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	Name       string    `json:"name"`
	Prefix     string    `json:"prefix"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func migrateAuth(db *DB) error {
	_, err := db.db.Exec(`
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL DEFAULT '',
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS workspaces (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS memberships (
    workspace_id TEXT NOT NULL,
    user_id      TEXT NOT NULL,
    role         TEXT NOT NULL DEFAULT 'member',
    created_at   TEXT NOT NULL,
    PRIMARY KEY (workspace_id, user_id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS sessions (
    token_hash   TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL DEFAULT '',
    expires_at   TEXT NOT NULL,
    created_at   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS api_tokens (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL DEFAULT '',
    prefix      TEXT NOT NULL DEFAULT '',
    token_hash  TEXT NOT NULL UNIQUE,
    created_at  TEXT NOT NULL,
    last_used_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_memberships_user ON memberships(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens(user_id);
`)
	return err
}

// --- users ---

// CountUsers reports how many login identities exist. Zero means the daemon is
// uninitialised and the setup endpoint is still open.
func (d *DB) CountUsers() (int, error) {
	var n int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts a user. Email is unique (case-insensitive); pass an
// already-hashed password — hashing policy belongs to the caller.
func (d *DB) CreateUser(name, email, passwordHash string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || passwordHash == "" {
		return nil, fmt.Errorf("catalog: email and password hash required")
	}
	u := &User{ID: newID(), Name: strings.TrimSpace(name), Email: email, CreatedAt: now()}
	_, err := d.db.Exec(`INSERT INTO users(id, name, email, password_hash, created_at)
		VALUES(?, ?, ?, ?, ?)`, u.ID, u.Name, u.Email, passwordHash, format(u.CreatedAt))
	if err != nil {
		return nil, err
	}
	return u, nil
}

// GetUserByEmail fetches a user for login. The password hash comes with it.
func (d *DB) GetUserByEmail(email string) (*User, string, error) {
	var u User
	var hash, created string
	err := d.db.QueryRow(`SELECT id, name, email, password_hash, created_at
		FROM users WHERE email = ?`, strings.ToLower(strings.TrimSpace(email))).
		Scan(&u.ID, &u.Name, &u.Email, &hash, &created)
	if err != nil {
		return nil, "", err
	}
	u.CreatedAt = parse(created)
	return &u, hash, nil
}

// GetUserByID fetches a user by id.
func (d *DB) GetUserByID(id string) (*User, error) {
	var u User
	var created string
	err := d.db.QueryRow(`SELECT id, name, email, created_at FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Name, &u.Email, &created)
	if err != nil {
		return nil, err
	}
	u.CreatedAt = parse(created)
	return &u, nil
}

// ListUsers returns every user, oldest first.
func (d *DB) ListUsers() ([]*User, error) {
	rows, err := d.db.Query(`SELECT id, name, email, created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		var u User
		var created string
		if err := rows.Scan(&u.ID, &u.Name, &u.Email, &created); err != nil {
			return nil, err
		}
		u.CreatedAt = parse(created)
		out = append(out, &u)
	}
	return out, rows.Err()
}

// SetPasswordHash replaces a user's password hash.
func (d *DB) SetPasswordHash(id, hash string) error {
	if hash == "" {
		return fmt.Errorf("catalog: password hash required")
	}
	_, err := d.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
	return err
}

// DeleteUser removes a user. Memberships, sessions and API tokens cascade.
// DeleteUser removes a user and everything pointing at them: memberships,
// sessions and API tokens. Leaving those rows behind would keep ghosts in
// member lists and owner counts (the foreign keys only cascade from workspaces
// and the users row itself).
func (d *DB) DeleteUser(id string) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM memberships WHERE user_id = ?`,
		`DELETE FROM sessions WHERE user_id = ?`,
		`DELETE FROM api_tokens WHERE user_id = ?`,
		`DELETE FROM users WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// --- workspaces ---

// CreateWorkspace inserts a workspace.
func (d *DB) CreateWorkspace(name string) (*Workspace, error) {
	w := &Workspace{ID: newID(), Name: strings.TrimSpace(name), CreatedAt: now()}
	if w.Name == "" {
		w.Name = "default"
	}
	_, err := d.db.Exec(`INSERT INTO workspaces(id, name, created_at) VALUES(?, ?, ?)`,
		w.ID, w.Name, format(w.CreatedAt))
	if err != nil {
		return nil, err
	}
	return w, nil
}

// ListWorkspaces returns every workspace, oldest first.
func (d *DB) ListWorkspaces() ([]*Workspace, error) {
	rows, err := d.db.Query(`SELECT id, name, created_at FROM workspaces ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Workspace
	for rows.Next() {
		var w Workspace
		var created string
		if err := rows.Scan(&w.ID, &w.Name, &created); err != nil {
			return nil, err
		}
		w.CreatedAt = parse(created)
		out = append(out, &w)
	}
	return out, rows.Err()
}

// --- memberships ---

// AddMember grants a role in a workspace.
func (d *DB) AddMember(workspaceID, userID, role string) error {
	role, err := ParseRole(role)
	if err != nil {
		return err
	}
	_, err = d.db.Exec(`INSERT INTO memberships(workspace_id, user_id, role, created_at)
		VALUES(?, ?, ?, ?)`, workspaceID, userID, role, format(now()))
	return err
}

// SetMemberRole changes a member's role.
func (d *DB) SetMemberRole(workspaceID, userID, role string) error {
	role, err := ParseRole(role)
	if err != nil {
		return err
	}
	res, err := d.db.Exec(`UPDATE memberships SET role = ? WHERE workspace_id = ? AND user_id = ?`,
		role, workspaceID, userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveMembership revokes workspace access.
func (d *DB) RemoveMembership(workspaceID, userID string) error {
	_, err := d.db.Exec(`DELETE FROM memberships WHERE workspace_id = ? AND user_id = ?`,
		workspaceID, userID)
	return err
}

// GetMembership returns a user's role in a workspace.
func (d *DB) GetMembership(workspaceID, userID string) (string, error) {
	var role string
	err := d.db.QueryRow(`SELECT role FROM memberships WHERE workspace_id = ? AND user_id = ?`,
		workspaceID, userID).Scan(&role)
	if err != nil {
		return "", err
	}
	return role, nil
}

// ListMembers returns every member of a workspace with their roles.
func (d *DB) ListMembers(workspaceID string) ([]*Member, error) {
	rows, err := d.db.Query(`
		SELECT u.id, u.name, u.email, u.created_at, m.role
		FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = ? ORDER BY u.created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Member
	for rows.Next() {
		var m Member
		var created string
		if err := rows.Scan(&m.ID, &m.Name, &m.Email, &created, &m.Role); err != nil {
			return nil, err
		}
		m.CreatedAt = parse(created)
		out = append(out, &m)
	}
	return out, rows.Err()
}

// MembershipsForUser lists the workspaces a user belongs to, oldest first.
func (d *DB) MembershipsForUser(userID string) ([]*Workspace, error) {
	rows, err := d.db.Query(`
		SELECT w.id, w.name, w.created_at FROM memberships m
		JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.user_id = ? ORDER BY w.created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Workspace
	for rows.Next() {
		var w Workspace
		var created string
		if err := rows.Scan(&w.ID, &w.Name, &created); err != nil {
			return nil, err
		}
		w.CreatedAt = parse(created)
		out = append(out, &w)
	}
	return out, rows.Err()
}

// CountOwners reports how many owners a workspace has. Callers refuse to drop
// below one.
func (d *DB) CountOwners(workspaceID string) (int, error) {
	var n int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM memberships
		WHERE workspace_id = ? AND role = ?`, workspaceID, RoleOwner).Scan(&n)
	return n, err
}

// --- sessions ---

// CreateSession records a login session for the token hash.
func (d *DB) CreateSession(tokenHash, userID, workspaceID string, expiresAt time.Time) error {
	_, err := d.db.Exec(`INSERT INTO sessions(token_hash, user_id, workspace_id, expires_at, created_at)
		VALUES(?, ?, ?, ?, ?)`, tokenHash, userID, workspaceID, format(expiresAt), format(now()))
	return err
}

// GetSession resolves a session, pruning it when expired.
func (d *DB) GetSession(tokenHash string) (*Session, error) {
	var s Session
	var expires, created string
	err := d.db.QueryRow(`SELECT token_hash, user_id, workspace_id, expires_at, created_at
		FROM sessions WHERE token_hash = ?`, tokenHash).
		Scan(&s.TokenHash, &s.UserID, &s.WorkspaceID, &expires, &created)
	if err != nil {
		return nil, err
	}
	s.ExpiresAt, s.CreatedAt = parse(expires), parse(created)
	if !s.ExpiresAt.After(time.Now()) {
		_, _ = d.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
		return nil, ErrNotFound
	}
	return &s, nil
}

// DeleteSession revokes one session.
func (d *DB) DeleteSession(tokenHash string) error {
	_, err := d.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteSessionsForUser revokes every session of a user.
func (d *DB) DeleteSessionsForUser(userID string) error {
	_, err := d.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// --- api tokens ---

// CreateAPIToken records a token for a user and returns its display prefix.
func (d *DB) CreateAPIToken(userID, name, tokenHash, prefix string) (*APIToken, error) {
	t := &APIToken{
		ID:        newID(),
		UserID:    userID,
		Name:      strings.TrimSpace(name),
		Prefix:    prefix,
		CreatedAt: now(),
	}
	_, err := d.db.Exec(`INSERT INTO api_tokens(id, user_id, name, prefix, token_hash, created_at)
		VALUES(?, ?, ?, ?, ?, ?)`, t.ID, t.UserID, t.Name, t.Prefix, tokenHash, format(t.CreatedAt))
	if err != nil {
		return nil, err
	}
	return t, nil
}

// ListAPITokens returns a user's tokens (hashes stay server-side).
func (d *DB) ListAPITokens(userID string) ([]*APIToken, error) {
	rows, err := d.db.Query(`SELECT id, user_id, name, prefix, created_at, last_used_at
		FROM api_tokens WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*APIToken
	for rows.Next() {
		var t APIToken
		var created, used string
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &created, &used); err != nil {
			return nil, err
		}
		t.CreatedAt, t.LastUsedAt = parse(created), parse(used)
		out = append(out, &t)
	}
	return out, rows.Err()
}

// GetAPITokenOwner resolves a token hash to its user.
func (d *DB) GetAPITokenOwner(tokenHash string) (string, error) {
	var userID string
	err := d.db.QueryRow(`SELECT user_id FROM api_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&userID)
	if err != nil {
		return "", err
	}
	return userID, nil
}

// TouchAPIToken records last use. Best effort — callers ignore errors.
func (d *DB) TouchAPIToken(tokenHash string) {
	_, _ = d.db.Exec(`UPDATE api_tokens SET last_used_at = ? WHERE token_hash = ?`,
		format(now()), tokenHash)
}

// DeleteAPIToken revokes one token. userID scopes the delete unless empty.
func (d *DB) DeleteAPIToken(id, userID string) error {
	if userID == "" {
		_, err := d.db.Exec(`DELETE FROM api_tokens WHERE id = ?`, id)
		return err
	}
	_, err := d.db.Exec(`DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, id, userID)
	return err
}
