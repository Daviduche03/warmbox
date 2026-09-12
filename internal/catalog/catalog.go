// Package catalog is warmbox's metadata store: which volumes, desktops and
// leases a node knows about. It is SQLite-backed — ACID, one file, no server.
//
// Bytes never live here. The object store (via internal/cloudstore) holds the
// volume chunks and manifest; the catalog holds the *catalog*: what exists, who
// owns it, and the single-writer leases.
package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("catalog: not found")

// DB is the catalog handle.
type DB struct {
	db *sql.DB
}

// Volume is the metadata for a cloud-backed volume.
type Volume struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	ChunkSize int64     `json:"chunk_size"`
	Remote    string    `json:"remote,omitempty"`
	From      string    `json:"from,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Desktop is the metadata for a VM instance.
type Desktop struct {
	ID        string    `json:"id"`
	Volume    string    `json:"volume,omitempty"`
	State     string    `json:"state"`
	GuestIP   string    `json:"guest_ip,omitempty"`
	PID       int       `json:"pid,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Snapshot is a frozen volume manifest.
type Snapshot struct {
	ID        string    `json:"id"`
	Volume    string    `json:"volume"`
	Size      int64     `json:"size"`
	ChunkSize int64     `json:"chunk_size"`
	CreatedAt time.Time `json:"created_at"`
}

// Open opens (or creates) the catalog at path. An empty path uses an in-memory
// database (useful for tests).
func Open(path string) (*DB, error) {
	dsn := path
	if dsn == "" {
		dsn = ":memory:"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite writers serialize; one connection avoids "database is locked".
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;`); err != nil {
		db.Close()
		return nil, err
	}
	d := &DB{db: db}
	if err := d.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

// Close releases the database.
func (d *DB) Close() error { return d.db.Close() }

func (d *DB) migrate() error {
	_, err := d.db.Exec(`
CREATE TABLE IF NOT EXISTS volumes (
    name        TEXT PRIMARY KEY,
    size        INTEGER NOT NULL,
    chunk_size  INTEGER NOT NULL,
    remote      TEXT NOT NULL DEFAULT '',
    from_volume TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS desktops (
    id         TEXT PRIMARY KEY,
    volume     TEXT NOT NULL DEFAULT '',
    state      TEXT NOT NULL DEFAULT '',
    guest_ip   TEXT NOT NULL DEFAULT '',
    pid        INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS leases (
    volume      TEXT PRIMARY KEY,
    owner       TEXT NOT NULL,
    acquired_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS snapshots (
    id         TEXT PRIMARY KEY,
    volume     TEXT NOT NULL DEFAULT '',
    size       INTEGER NOT NULL,
    chunk_size INTEGER NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_desktops_volume ON desktops(volume);
CREATE INDEX IF NOT EXISTS idx_snapshots_volume ON snapshots(volume);
`)
	return err
}

func now() time.Time { return time.Now().UTC() }

func format(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parse(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// --- volumes ---

// UpsertVolume inserts or updates a volume record.
func (d *DB) UpsertVolume(v *Volume) error {
	if v.Name == "" {
		return fmt.Errorf("catalog: volume name required")
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now()
	}
	v.UpdatedAt = now()
	_, err := d.db.Exec(`
INSERT INTO volumes(name, size, chunk_size, remote, from_volume, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
    size=excluded.size, chunk_size=excluded.chunk_size, remote=excluded.remote,
    from_volume=excluded.from_volume, updated_at=excluded.updated_at`,
		v.Name, v.Size, v.ChunkSize, v.Remote, v.From, format(v.CreatedAt), format(v.UpdatedAt))
	return err
}

// GetVolume returns a volume by name.
func (d *DB) GetVolume(name string) (*Volume, error) {
	row := d.db.QueryRow(`SELECT name, size, chunk_size, remote, from_volume, created_at, updated_at
		FROM volumes WHERE name = ?`, name)
	return scanVolume(row)
}

// ListVolumes returns every volume, ordered by name.
func (d *DB) ListVolumes() ([]*Volume, error) {
	rows, err := d.db.Query(`SELECT name, size, chunk_size, remote, from_volume, created_at, updated_at
		FROM volumes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Volume
	for rows.Next() {
		v, err := scanVolume(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DeleteVolume removes a volume record.
func (d *DB) DeleteVolume(name string) error {
	_, err := d.db.Exec(`DELETE FROM volumes WHERE name = ?`, name)
	return err
}

type scanner interface{ Scan(dest ...any) error }

func scanVolume(s scanner) (*Volume, error) {
	var v Volume
	var created, updated string
	if err := s.Scan(&v.Name, &v.Size, &v.ChunkSize, &v.Remote, &v.From, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	v.CreatedAt, v.UpdatedAt = parse(created), parse(updated)
	return &v, nil
}

// --- desktops ---

// UpsertDesktop inserts or updates a desktop record.
func (d *DB) UpsertDesktop(x *Desktop) error {
	if x.ID == "" {
		return fmt.Errorf("catalog: desktop id required")
	}
	if x.CreatedAt.IsZero() {
		x.CreatedAt = now()
	}
	x.UpdatedAt = now()
	_, err := d.db.Exec(`
INSERT INTO desktops(id, volume, state, guest_ip, pid, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    volume=excluded.volume, state=excluded.state, guest_ip=excluded.guest_ip,
    pid=excluded.pid, updated_at=excluded.updated_at`,
		x.ID, x.Volume, x.State, x.GuestIP, x.PID, format(x.CreatedAt), format(x.UpdatedAt))
	return err
}

// GetDesktop returns a desktop by id.
func (d *DB) GetDesktop(id string) (*Desktop, error) {
	row := d.db.QueryRow(`SELECT id, volume, state, guest_ip, pid, created_at, updated_at
		FROM desktops WHERE id = ?`, id)
	return scanDesktop(row)
}

// ListDesktops returns every desktop, newest first.
func (d *DB) ListDesktops() ([]*Desktop, error) {
	rows, err := d.db.Query(`SELECT id, volume, state, guest_ip, pid, created_at, updated_at
		FROM desktops ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Desktop
	for rows.Next() {
		x, err := scanDesktop(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// DeleteDesktop removes a desktop record.
func (d *DB) DeleteDesktop(id string) error {
	_, err := d.db.Exec(`DELETE FROM desktops WHERE id = ?`, id)
	return err
}

func scanDesktop(s scanner) (*Desktop, error) {
	var x Desktop
	var created, updated string
	if err := s.Scan(&x.ID, &x.Volume, &x.State, &x.GuestIP, &x.PID, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	x.CreatedAt, x.UpdatedAt = parse(created), parse(updated)
	return &x, nil
}

// --- snapshots ---

// UpsertSnapshot inserts or updates a snapshot record.
func (d *DB) UpsertSnapshot(s *Snapshot) error {
	if s.ID == "" {
		return fmt.Errorf("catalog: snapshot id required")
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now()
	}
	_, err := d.db.Exec(`
INSERT INTO snapshots(id, volume, size, chunk_size, created_at) VALUES(?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET volume=excluded.volume, size=excluded.size,
    chunk_size=excluded.chunk_size`, s.ID, s.Volume, s.Size, s.ChunkSize, format(s.CreatedAt))
	return err
}

// GetSnapshot returns a snapshot by id.
func (d *DB) GetSnapshot(id string) (*Snapshot, error) {
	row := d.db.QueryRow(`SELECT id, volume, size, chunk_size, created_at FROM snapshots WHERE id = ?`, id)
	return scanSnapshot(row)
}

// ListSnapshots returns snapshots, newest first; empty volume means all.
func (d *DB) ListSnapshots(volume string) ([]*Snapshot, error) {
	q := `SELECT id, volume, size, chunk_size, created_at FROM snapshots`
	var args []any
	if volume != "" {
		q += ` WHERE volume = ?`
		args = append(args, volume)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := d.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Snapshot
	for rows.Next() {
		s, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteSnapshot removes a snapshot record.
func (d *DB) DeleteSnapshot(id string) error {
	_, err := d.db.Exec(`DELETE FROM snapshots WHERE id = ?`, id)
	return err
}

func scanSnapshot(s scanner) (*Snapshot, error) {
	var x Snapshot
	var created string
	if err := s.Scan(&x.ID, &x.Volume, &x.Size, &x.ChunkSize, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	x.CreatedAt = parse(created)
	return &x, nil
}

// --- leases (single writer) ---

// AcquireLease claims a volume for owner. It fails if another owner holds it.
func (d *DB) AcquireLease(volume, owner string) error {
	res, err := d.db.Exec(`
INSERT INTO leases(volume, owner, acquired_at) VALUES(?, ?, ?)
ON CONFLICT(volume) DO UPDATE SET owner=excluded.owner, acquired_at=excluded.acquired_at
WHERE leases.owner = excluded.owner`, volume, owner, format(now()))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("volume %q is already leased", volume)
	}
	return nil
}

// ReleaseLease frees a volume's lease when held by owner.
func (d *DB) ReleaseLease(volume, owner string) error {
	_, err := d.db.Exec(`DELETE FROM leases WHERE volume = ? AND owner = ?`, volume, owner)
	return err
}

// LeaseOwner returns the current owner of a volume, or "".
func (d *DB) LeaseOwner(volume string) (string, error) {
	var owner string
	err := d.db.QueryRow(`SELECT owner FROM leases WHERE volume = ?`, volume).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return owner, nil
}

// ListLeases returns volume -> owner for every held lease.
func (d *DB) ListLeases() (map[string]string, error) {
	rows, err := d.db.Query(`SELECT volume, owner FROM leases`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var v, o string
		if err := rows.Scan(&v, &o); err != nil {
			return nil, err
		}
		out[v] = o
	}
	return out, rows.Err()
}
