package storage

import "strconv"

// GetSetting returns the value for a key from the settings table.
func (db *DB) GetSetting(key string) (string, error) {
	var val string
	err := db.rw.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&val)
	return val, err
}

// SetSetting upserts a key-value pair in the settings table.
func (db *DB) SetSetting(key, value string) error {
	_, err := db.rw.Exec(`
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, now())
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = now()
	`, key, value)
	return err
}

// GetSettingBool returns a bool setting, returning defaultVal on any error or absence.
func (db *DB) GetSettingBool(key string, defaultVal bool) bool {
	val, err := db.GetSetting(key)
	if err != nil {
		return defaultVal
	}
	b, err := strconv.ParseBool(val)
	if err != nil {
		return defaultVal
	}
	return b
}
