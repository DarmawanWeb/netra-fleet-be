package store

import (
	"context"
	"errors"
)

// Setting keys.
const (
	SettingInstallation = "installation_id"
)

// Setting returns a setting, or ErrNotFound.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&v)
	return v, notFound(err)
}

// SetSetting stores a setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, value)
	return err
}

// EnsureSetting returns a setting, storing value first when the key has none; concurrent callers
// get the same value.
func (s *Store) EnsureSetting(ctx context.Context, key, value string) (string, error) {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO NOTHING`, key, value); err != nil {
		return "", err
	}
	return s.Setting(ctx, key)
}

// ErrSetupDone means the first administrator exists already.
var ErrSetupDone = errors.New("setup is done: an administrator exists")

// HasUsers reports whether any user exists (no user: the first-run setup is open).
func (s *Store) HasUsers(ctx context.Context) (bool, error) {
	var any bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&any)
	return any, err
}

// CreateFirstAdmin creates the first administrator, only while there is no user at all; two
// setups at once cannot both succeed.
func (s *Store) CreateFirstAdmin(ctx context.Context, u User, hash string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return 0, err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists); err != nil {
		return 0, err
	}
	if exists {
		return 0, ErrSetupDone
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO users (username, display_name, role, password_hash)
		VALUES ($1, $2, 'admin', $3) RETURNING id`, u.Username, u.DisplayName, hash).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
