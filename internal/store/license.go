package store

import (
	"context"
	"database/sql"
	"errors"
)

// UploadedLicense returns the license text uploaded in the dashboard, or "" when there is none.
func (s *Store) UploadedLicense(ctx context.Context) (string, error) {
	var text string
	err := s.db.QueryRowContext(ctx, `SELECT text FROM license WHERE id = 1`).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return text, err
}

// SaveLicense stores the license text uploaded in the dashboard.
func (s *Store) SaveLicense(ctx context.Context, text, username string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO license (id, text, uploaded_by) VALUES (1, $1, $2)
		ON CONFLICT (id) DO UPDATE SET text = EXCLUDED.text, uploaded_by = EXCLUDED.uploaded_by, uploaded_at = now()`,
		text, username)
	return err
}

// CountRobots returns the number of registered robots.
func (s *Store) CountRobots(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM robots`).Scan(&n)
	return n, err
}
