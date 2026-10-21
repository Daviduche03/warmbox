package catalog

import (
	"database/sql"
	"fmt"
	"time"
)

// Two small tables sit beside the desktop records. One remembers what a
// create answered, so a retry that lost the answer returns the same desktop
// instead of a second one that bills twice. The other remembers when a
// desktop should stop by itself.

// IdempotencyRecord is the answer a create gave, kept so the same key can be
// replayed safely.
type IdempotencyRecord struct {
	Workspace string
	Key       string
	BodyHash  string
	Response  string
	CreatedAt time.Time
}

// PutIdempotency stores the answer to a create, keyed by workspace and key.
func (d *DB) PutIdempotency(rec IdempotencyRecord) error {
	if rec.Key == "" {
		return fmt.Errorf("catalog: idempotency key required")
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now()
	}
	_, err := d.db.Exec(`
INSERT INTO idempotency(workspace_id, key, body_hash, response, created_at)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(workspace_id, key) DO UPDATE SET
    body_hash=excluded.body_hash, response=excluded.response, created_at=excluded.created_at`,
		rec.Workspace, rec.Key, rec.BodyHash, rec.Response, format(rec.CreatedAt))
	return err
}

// Idempotency returns the stored answer for a key, if there is one.
func (d *DB) Idempotency(workspace, key string) (IdempotencyRecord, bool) {
	if key == "" {
		return IdempotencyRecord{}, false
	}
	var rec IdempotencyRecord
	var created string
	err := d.db.QueryRow(`SELECT workspace_id, key, body_hash, response, created_at
		FROM idempotency WHERE workspace_id = ? AND key = ?`, workspace, key).
		Scan(&rec.Workspace, &rec.Key, &rec.BodyHash, &rec.Response, &created)
	if err != nil {
		return IdempotencyRecord{}, false
	}
	rec.CreatedAt = parse(created)
	return rec, true
}

// PruneIdempotency drops the answers of keys older than the given age. A
// client that retries for a day should get the same desktop; after that, the
// record is history.
func (d *DB) PruneIdempotency(olderThan time.Time) (int, error) {
	res, err := d.db.Exec(`DELETE FROM idempotency WHERE created_at < ?`, format(olderThan))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// SetDesktopDeadline sets when a desktop stops by itself. A zero time means
// it never stops on its own.
func (d *DB) SetDesktopDeadline(id string, at time.Time) error {
	if id == "" {
		return fmt.Errorf("catalog: desktop id required")
	}
	if at.IsZero() {
		return d.ClearDesktopDeadline(id)
	}
	_, err := d.db.Exec(`
INSERT INTO desktop_deadlines(desktop_id, expires_at) VALUES(?, ?)
ON CONFLICT(desktop_id) DO UPDATE SET expires_at=excluded.expires_at`,
		id, format(at))
	return err
}

// ClearDesktopDeadline forgets a desktop's deadline: it never stops by itself.
func (d *DB) ClearDesktopDeadline(id string) error {
	_, err := d.db.Exec(`DELETE FROM desktop_deadlines WHERE desktop_id = ?`, id)
	return err
}

// DesktopDeadline returns when a desktop stops by itself.
func (d *DB) DesktopDeadline(id string) (time.Time, bool) {
	var at string
	err := d.db.QueryRow(`SELECT expires_at FROM desktop_deadlines WHERE desktop_id = ?`, id).Scan(&at)
	if err != nil {
		return time.Time{}, false
	}
	t := parse(at)
	return t, !t.IsZero()
}

// ExpiredDesktops lists the desktops whose deadline has passed.
func (d *DB) ExpiredDesktops(now time.Time) ([]string, error) {
	rows, err := d.db.Query(`SELECT desktop_id FROM desktop_deadlines WHERE expires_at <= ?`, format(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ensure the compiler keeps sql imported for the helpers above
var _ = sql.ErrNoRows
