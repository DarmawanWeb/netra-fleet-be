package store

import (
	"context"
	"time"

	"github.com/lib/pq"
)

// APIClient is a third-party application that reads data through the integration API.
type APIClient struct {
	ID         int64      `json:"id"`
	ClientID   string     `json:"client_id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	Enabled    bool       `json:"enabled"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	SecretHash string     `json:"-"`
}

const clientCols = `id, client_id, name, scopes, enabled, created_by, created_at, last_used_at, secret_hash`

func scanClient(row interface{ Scan(...any) error }) (APIClient, error) {
	var c APIClient
	err := row.Scan(&c.ID, &c.ClientID, &c.Name, pq.Array(&c.Scopes), &c.Enabled, &c.CreatedBy, &c.CreatedAt,
		&c.LastUsedAt, &c.SecretHash)
	if c.Scopes == nil {
		c.Scopes = []string{}
	}
	return c, err
}

// CreateAPIClient stores a client with the hash of its secret.
func (s *Store) CreateAPIClient(ctx context.Context, c APIClient) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO api_clients (client_id, name, secret_hash, scopes, enabled, created_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		c.ClientID, c.Name, c.SecretHash, pq.Array(c.Scopes), c.Enabled, c.CreatedBy).Scan(&id)
	return id, err
}

// ListAPIClients returns every client, newest first.
func (s *Store) ListAPIClients(ctx context.Context) ([]APIClient, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+clientCols+` FROM api_clients ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIClient{}
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetAPIClient returns a client by its row id.
func (s *Store) GetAPIClient(ctx context.Context, id int64) (APIClient, error) {
	c, err := scanClient(s.db.QueryRowContext(ctx, `SELECT `+clientCols+` FROM api_clients WHERE id = $1`, id))
	return c, notFound(err)
}

// APIClientByClientID returns a client by its public client_id.
func (s *Store) APIClientByClientID(ctx context.Context, clientID string) (APIClient, error) {
	c, err := scanClient(s.db.QueryRowContext(ctx, `SELECT `+clientCols+` FROM api_clients WHERE client_id = $1`, clientID))
	return c, notFound(err)
}

// UpdateAPIClient renames a client, changes its scopes or turns it on or off; turning it off revokes its tokens.
func (s *Store) UpdateAPIClient(ctx context.Context, id int64, name string, scopes []string, enabled bool) error {
	if err := s.execOne(ctx, `UPDATE api_clients SET name = $2, scopes = $3, enabled = $4 WHERE id = $1`,
		id, name, pq.Array(scopes), enabled); err != nil {
		return err
	}
	if !enabled {
		_, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE client_pk = $1`, id)
		return err
	}
	return nil
}

// SetAPIClientSecret replaces the secret and revokes every token issued with the old one.
func (s *Store) SetAPIClientSecret(ctx context.Context, id int64, hash string) error {
	if err := s.execOne(ctx, `UPDATE api_clients SET secret_hash = $2 WHERE id = $1`, id, hash); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE client_pk = $1`, id)
	return err
}

// DeleteAPIClient removes a client and its tokens.
func (s *Store) DeleteAPIClient(ctx context.Context, id int64) error {
	return s.execOne(ctx, `DELETE FROM api_clients WHERE id = $1`, id)
}

// CreateAPIToken stores an access token hash and drops expired tokens.
func (s *Store) CreateAPIToken(ctx context.Context, hash string, clientPK int64, scopes []string, expires time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE expires_at < now()`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO api_tokens (token_hash, client_pk, scopes, expires_at) VALUES ($1, $2, $3, $4)`,
		hash, clientPK, pq.Array(scopes), expires)
	return err
}

// APITokenClient returns the enabled client of a valid token and the scopes granted to the token.
func (s *Store) APITokenClient(ctx context.Context, hash string) (APIClient, []string, error) {
	var granted []string
	row := s.db.QueryRowContext(ctx, `SELECT c.id, c.client_id, c.name, c.scopes, c.enabled, c.created_by, c.created_at,
			c.last_used_at, c.secret_hash, t.scopes
		FROM api_tokens t JOIN api_clients c ON c.id = t.client_pk
		WHERE t.token_hash = $1 AND t.expires_at > now() AND c.enabled`, hash)
	var c APIClient
	err := row.Scan(&c.ID, &c.ClientID, &c.Name, pq.Array(&c.Scopes), &c.Enabled, &c.CreatedBy, &c.CreatedAt,
		&c.LastUsedAt, &c.SecretHash, pq.Array(&granted))
	return c, granted, notFound(err)
}

// TouchAPIClient records that a client used the API.
func (s *Store) TouchAPIClient(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE api_clients SET last_used_at = now() WHERE id = $1`, id)
	return err
}
