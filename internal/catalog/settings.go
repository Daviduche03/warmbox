package catalog

import "database/sql"

// Daemon-wide settings the dashboard can change. They live in the catalog so
// they survive restarts, like everything else here.
const (
	// SettingMaxDesktopsPerWorkspace caps how many desktops one workspace may
	// hold at once. "0" (the default) means no cap.
	SettingMaxDesktopsPerWorkspace = "max_desktops_per_workspace"
)

func migrateSettings(d *DB) error {
	_, err := d.db.Exec(`CREATE TABLE IF NOT EXISTS settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`)
	return err
}

// GetSetting returns a setting's value, or "" when it was never set.
func (d *DB) GetSetting(key string) (string, error) {
	var v string
	err := d.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// SetSetting writes a setting, replacing it when it already exists.
func (d *DB) SetSetting(key, value string) error {
	_, err := d.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
