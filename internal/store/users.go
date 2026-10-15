package store

import (
	"context"
	"encoding/json"
	"time"
)

// User is a dashboard account.
type User struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	Role        string     `json:"role"`
	Disabled    bool       `json:"disabled"`
	LastLogin   *time.Time `json:"last_login"`
	CreatedAt   time.Time  `json:"created_at"`
}

// AuditEntry is one recorded user action.
type AuditEntry struct {
	TS       time.Time       `json:"ts"`
	Username string          `json:"username"`
	Action   string          `json:"action"`
	Target   string          `json:"target"`
	Detail   json.RawMessage `json:"detail"`
	IP       string          `json:"ip"`
}

// TrackPoint is one telemetry sample.
type TrackPoint struct {
	TS       time.Time `json:"ts"`
	X        *float64  `json:"x"`
	Y        *float64  `json:"y"`
	Yaw      *float64  `json:"yaw"`
	Battery  *float64  `json:"battery"`
	NavState string    `json:"nav_state"`
	People   int       `json:"people"`
	Load
}

// Load is the robot computer load from telemetry health (null when the robot does not report it).
type Load struct {
	CPUPercent    *float64 `json:"cpu_percent"`
	GPUPercent    *float64 `json:"gpu_percent"`
	MemoryPercent *float64 `json:"memory_percent"`
	MemoryUsedGB  *float64 `json:"memory_used_gb"`
	MemoryTotalGB *float64 `json:"memory_total_gb"`
	CPUTempC      *float64 `json:"cpu_temp_c"`
}

// SessionUser returns the user of a live, enabled session.
func (s *Store) SessionUser(ctx context.Context, tokenHash string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `SELECT u.id, u.username, u.display_name, u.role, u.disabled, u.last_login,
		u.created_at FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND NOT u.disabled`, tokenHash).
		Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Disabled, &u.LastLogin, &u.CreatedAt)
	return u, notFound(err)
}

// DeleteSession ends a session.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// UserLogin returns id, hash, role and disabled flag for a login attempt.
func (s *Store) UserLogin(ctx context.Context, username string) (int64, string, string, bool, error) {
	var id int64
	var hash, role string
	var disabled bool
	err := s.db.QueryRowContext(ctx, `SELECT id, password_hash, role, disabled FROM users WHERE username = $1`,
		username).Scan(&id, &hash, &role, &disabled)
	return id, hash, role, disabled, notFound(err)
}

// CreateSessionFor stores a session and stamps the login time.
func (s *Store) CreateSessionFor(ctx context.Context, tokenHash string, userID int64, expires time.Time, ip string) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at, ip) VALUES ($1, $2, $3, $4)`,
		tokenHash, userID, expires, ip); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET last_login = now() WHERE id = $1`, userID)
	return err
}

// ListUsers returns every user.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, display_name, role, disabled, last_login, created_at
		FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Disabled, &u.LastLogin, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CreateUser adds a user.
func (s *Store) CreateUser(ctx context.Context, u User, hash string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO users (username, display_name, role, password_hash)
		VALUES ($1, $2, $3, $4) RETURNING id`, u.Username, u.DisplayName, u.Role, hash).Scan(&id)
	return id, err
}

// UpdateUser changes name, role and disabled flag; disabling ends the user's sessions.
func (s *Store) UpdateUser(ctx context.Context, u User) error {
	if err := s.execOne(ctx, `UPDATE users SET display_name = $2, role = $3, disabled = $4 WHERE id = $1`,
		u.ID, u.DisplayName, u.Role, u.Disabled); err != nil {
		return err
	}
	if u.Disabled {
		_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = $1`, u.ID)
		return err
	}
	return nil
}

// SetPassword replaces a password hash and ends the user's other sessions.
func (s *Store) SetPassword(ctx context.Context, id int64, hash, keepSession string) error {
	if err := s.execOne(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, id, hash); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2`, id, keepSession)
	return err
}

// DeleteUser removes a user.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	return s.execOne(ctx, `DELETE FROM users WHERE id = $1`, id)
}

// CountAdmins returns the number of enabled admins.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role = 'admin' AND NOT disabled`).Scan(&n)
	return n, err
}

// InsertAudit records a user action.
func (s *Store) InsertAudit(ctx context.Context, e AuditEntry) error {
	detail := e.Detail
	if len(detail) == 0 {
		detail = json.RawMessage(`{}`)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_log (username, action, target, detail, ip) VALUES ($1, $2, $3, $4, $5)`,
		e.Username, e.Action, e.Target, []byte(detail), e.IP)
	return err
}

// ListAudit returns audit entries in a time range, newest first.
func (s *Store) ListAudit(ctx context.Context, username, action string, from, to time.Time, limit int) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ts, username, action, target, detail, ip FROM audit_log
		WHERE ($1 = '' OR username = $1) AND ($2 = '' OR action = $2) AND ts BETWEEN $3 AND $4
		ORDER BY ts DESC LIMIT $5`, username, action, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var detail []byte
		if err := rows.Scan(&e.TS, &e.Username, &e.Action, &e.Target, &detail, &e.IP); err != nil {
			return nil, err
		}
		e.Detail = detail
		out = append(out, e)
	}
	return out, rows.Err()
}

// InsertTelemetry stores one history sample.
func (s *Store) InsertTelemetry(ctx context.Context, robotID string, p TrackPoint, mode string, patrolID *int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO telemetry (ts, robot_id, x, y, yaw, battery, nav_state, mode, people, patrol_id,
		cpu_percent, gpu_percent, memory_percent, memory_used_gb, memory_total_gb, cpu_temp_c)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		p.TS, robotID, p.X, p.Y, p.Yaw, p.Battery, p.NavState, mode, p.People, patrolID,
		p.CPUPercent, p.GPUPercent, p.MemoryPercent, p.MemoryUsedGB, p.MemoryTotalGB, p.CPUTempC)
	return err
}

// Track returns telemetry history, evenly thinned to at most maxPoints.
func (s *Store) Track(ctx context.Context, robotID string, from, to time.Time, maxPoints int) ([]TrackPoint, error) {
	rows, err := s.db.QueryContext(ctx, `WITH t AS (
			SELECT ts, x, y, yaw, battery, nav_state, people, cpu_percent, gpu_percent, memory_percent, memory_used_gb,
				memory_total_gb, cpu_temp_c, row_number() OVER (ORDER BY ts) AS n, count(*) OVER () AS total
			FROM telemetry WHERE robot_id = $1 AND ts BETWEEN $2 AND $3)
		SELECT ts, x, y, yaw, battery, nav_state, people, cpu_percent, gpu_percent, memory_percent, memory_used_gb,
			memory_total_gb, cpu_temp_c FROM t
		WHERE total <= $4 OR n % ceil(total::float / $4)::int = 0 ORDER BY ts`, robotID, from, to, maxPoints)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrackPoint{}
	for rows.Next() {
		var p TrackPoint
		if err := rows.Scan(&p.TS, &p.X, &p.Y, &p.Yaw, &p.Battery, &p.NavState, &p.People, &p.CPUPercent, &p.GPUPercent,
			&p.MemoryPercent, &p.MemoryUsedGB, &p.MemoryTotalGB, &p.CPUTempC); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
