package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"time"

	"github.com/lib/pq"
)

// ---- users and sessions

// EnsureUser creates or updates a user with the given password hash.
func (s *Store) EnsureUser(ctx context.Context, username, hash, role string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3)
		ON CONFLICT (username) DO UPDATE SET password_hash = EXCLUDED.password_hash, role = EXCLUDED.role`,
		username, hash, role)
	return err
}

// UserHash returns a user's id and password hash.
func (s *Store) UserHash(ctx context.Context, username string) (int64, string, error) {
	var id int64
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT id, password_hash FROM users WHERE username = $1`, username).
		Scan(&id, &hash)
	return id, hash, notFound(err)
}

// CreateSession stores a hashed session token.
func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, expires)
	return err
}

// ---- robots

const robotCols = `id, name, floor_plan_id, last_seen, last_telemetry, created_at`

func scanRobot(row interface{ Scan(...any) error }) (Robot, error) {
	var r Robot
	var tel []byte
	err := row.Scan(&r.ID, &r.Name, &r.FloorPlanID, &r.LastSeen, &tel, &r.CreatedAt)
	if tel != nil {
		r.LastTelemetry = tel
	}
	return r, err
}

// CreateRobot registers a robot with a hashed token.
func (s *Store) CreateRobot(ctx context.Context, id, name, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO robots (id, name, token_hash) VALUES ($1, $2, $3)`, id, name, tokenHash)
	return err
}

// SetRobotToken replaces a robot token.
func (s *Store) SetRobotToken(ctx context.Context, id, tokenHash string) error {
	return s.execOne(ctx, `UPDATE robots SET token_hash = $2 WHERE id = $1`, id, tokenHash)
}

// UpdateRobot changes the name and floor plan of a robot.
func (s *Store) UpdateRobot(ctx context.Context, id, name string, floorPlanID *int64) error {
	return s.execOne(ctx, `UPDATE robots SET name = $2, floor_plan_id = $3 WHERE id = $1`, id, name, floorPlanID)
}

// DeleteRobot removes a robot and its data.
func (s *Store) DeleteRobot(ctx context.Context, id string) error {
	return s.execOne(ctx, `DELETE FROM robots WHERE id = $1`, id)
}

// RobotByToken returns the robot owning a token hash.
func (s *Store) RobotByToken(ctx context.Context, tokenHash string) (Robot, error) {
	r, err := scanRobot(s.db.QueryRowContext(ctx, `SELECT `+robotCols+` FROM robots WHERE token_hash = $1`, tokenHash))
	return r, notFound(err)
}

// ListRobots returns every robot.
func (s *Store) ListRobots(ctx context.Context) ([]Robot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+robotCols+` FROM robots ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Robot{}
	for rows.Next() {
		r, err := scanRobot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveTelemetry stores the latest telemetry of a robot.
func (s *Store) SaveTelemetry(ctx context.Context, id string, telemetry []byte) error {
	_, err := s.db.ExecContext(ctx, `UPDATE robots SET last_seen = now(), last_telemetry = $2 WHERE id = $1`,
		id, telemetry)
	return err
}

// ---- routes

const routeCols = `id, uid, robot_id, name, recorded_at, length_m, stops, closed, archived, created_at`

func scanRoute(row interface{ Scan(...any) error }) (Route, error) {
	var r Route
	err := row.Scan(&r.ID, &r.UID, &r.RobotID, &r.Name, &r.RecordedAt, &r.LengthM, &r.Stops, &r.Closed,
		&r.Archived, &r.CreatedAt)
	return r, err
}

// InsertRoute stores a route once per uid and returns its id.
func (s *Store) InsertRoute(ctx context.Context, r Route, csv string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO routes (uid, robot_id, name, recorded_at, length_m, stops, closed, csv)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (uid) DO UPDATE SET uid = EXCLUDED.uid RETURNING id`,
		r.UID, r.RobotID, r.Name, r.RecordedAt, r.LengthM, r.Stops, r.Closed, csv).Scan(&id)
	return id, err
}

// ListRoutes returns routes, newest first, optionally for one robot.
func (s *Store) ListRoutes(ctx context.Context, robotID string, archived bool) ([]Route, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+routeCols+` FROM routes
		WHERE ($1 = '' OR robot_id = $1) AND archived = $2 ORDER BY recorded_at DESC`, robotID, archived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Route{}
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRoute returns a route with its CSV.
func (s *Store) GetRoute(ctx context.Context, id int64) (Route, string, error) {
	var csv string
	var r Route
	err := s.db.QueryRowContext(ctx, `SELECT `+routeCols+`, csv FROM routes WHERE id = $1`, id).Scan(
		&r.ID, &r.UID, &r.RobotID, &r.Name, &r.RecordedAt, &r.LengthM, &r.Stops, &r.Closed, &r.Archived,
		&r.CreatedAt, &csv)
	return r, csv, notFound(err)
}

// UpdateRoute renames or archives a route.
func (s *Store) UpdateRoute(ctx context.Context, id int64, name string, archived bool) error {
	return s.execOne(ctx, `UPDATE routes SET name = $2, archived = $3 WHERE id = $1`, id, name, archived)
}

// ---- schedules

const scheduleCols = `id, robot_id, route_id, name, days, time_of_day, enabled, last_run_on::text, once_on::text`

// ListSchedules returns all schedules.
func (s *Store) ListSchedules(ctx context.Context) ([]Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+scheduleCols+` FROM schedules ORDER BY time_of_day`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		var c Schedule
		if err := rows.Scan(&c.ID, &c.RobotID, &c.RouteID, &c.Name, pq.Array(&c.Days), &c.TimeOfDay, &c.Enabled,
			&c.LastRunOn, &c.OnceOn); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveSchedule inserts (ID 0) or updates a schedule and returns its id.
func (s *Store) SaveSchedule(ctx context.Context, c Schedule) (int64, error) {
	if c.ID == 0 {
		err := s.db.QueryRowContext(ctx, `INSERT INTO schedules (robot_id, route_id, name, days, time_of_day, enabled, once_on)
			VALUES ($1, $2, $3, $4, $5, $6, $7::date) RETURNING id`,
			c.RobotID, c.RouteID, c.Name, pq.Array(c.Days), c.TimeOfDay, c.Enabled, c.OnceOn).Scan(&c.ID)
		return c.ID, err
	}
	return c.ID, s.execOne(ctx, `UPDATE schedules SET robot_id = $2, route_id = $3, name = $4, days = $5,
		time_of_day = $6, enabled = $7, once_on = $8::date WHERE id = $1`,
		c.ID, c.RobotID, c.RouteID, c.Name, pq.Array(c.Days), c.TimeOfDay, c.Enabled, c.OnceOn)
}

// DeleteSchedule removes a schedule.
func (s *Store) DeleteSchedule(ctx context.Context, id int64) error {
	return s.execOne(ctx, `DELETE FROM schedules WHERE id = $1`, id)
}

// MarkScheduleRun records that a schedule fired on a date (YYYY-MM-DD); false if it already did.
func (s *Store) MarkScheduleRun(ctx context.Context, id int64, date string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE schedules SET last_run_on = $2::date
		WHERE id = $1 AND (last_run_on IS NULL OR last_run_on < $2::date)`, id, date)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ---- patrols

const patrolCols = `id, robot_id, route_id, schedule_id, status, message, laps, started_at, finished_at, created_at`

func scanPatrol(row interface{ Scan(...any) error }) (Patrol, error) {
	var p Patrol
	err := row.Scan(&p.ID, &p.RobotID, &p.RouteID, &p.ScheduleID, &p.Status, &p.Message, &p.Laps, &p.StartedAt,
		&p.FinishedAt, &p.CreatedAt)
	return p, err
}

// CreatePatrol opens a patrol in the requested state.
func (s *Store) CreatePatrol(ctx context.Context, robotID string, routeID int64, scheduleID *int64) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO patrols (robot_id, route_id, schedule_id) VALUES ($1, $2, $3)
		RETURNING id`, robotID, routeID, scheduleID).Scan(&id)
	return id, err
}

// PatrolStatus moves a patrol to a status, stamping start/finish times.
func (s *Store) PatrolStatus(ctx context.Context, id int64, status, message string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE patrols SET status = $2, message = $3,
		started_at = CASE WHEN $2 = 'running' AND started_at IS NULL THEN now() ELSE started_at END,
		finished_at = CASE WHEN $2 IN ('finished', 'stopped', 'failed') THEN now() ELSE finished_at END
		WHERE id = $1`, id, status, message)
	return err
}

// PatrolLap records the number of completed laps.
func (s *Store) PatrolLap(ctx context.Context, id int64, lap int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE patrols SET laps = GREATEST(laps, $2) WHERE id = $1`, id, lap)
	return err
}

// ActivePatrol returns the running or requested patrol of a robot.
func (s *Store) ActivePatrol(ctx context.Context, robotID string) (Patrol, error) {
	p, err := scanPatrol(s.db.QueryRowContext(ctx, `SELECT `+patrolCols+` FROM patrols
		WHERE robot_id = $1 AND status IN ('requested', 'running') ORDER BY created_at DESC LIMIT 1`, robotID))
	return p, notFound(err)
}

// ListPatrols returns patrols in a time range, newest first.
func (s *Store) ListPatrols(ctx context.Context, robotID string, from, to time.Time) ([]Patrol, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+patrolCols+` FROM patrols
		WHERE ($1 = '' OR robot_id = $1) AND created_at BETWEEN $2 AND $3 ORDER BY created_at DESC LIMIT 500`,
		robotID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Patrol{}
	for rows.Next() {
		p, err := scanPatrol(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPatrol returns one patrol.
func (s *Store) GetPatrol(ctx context.Context, id int64) (Patrol, error) {
	p, err := scanPatrol(s.db.QueryRowContext(ctx, `SELECT `+patrolCols+` FROM patrols WHERE id = $1`, id))
	return p, notFound(err)
}

// PatrolRecap summarises a patrol: anomalies per category, events per type and crowd peak.
func (s *Store) PatrolRecap(ctx context.Context, id int64) (map[string]any, error) {
	recap := map[string]any{}
	count := func(query string) (map[string]int, error) {
		rows, err := s.db.QueryContext(ctx, query, id)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := map[string]int{}
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, rows.Err()
	}
	var err error
	if recap["anomalies"], err = count(`SELECT COALESCE(final_category, early_category), count(*) FROM anomalies
		WHERE patrol_id = $1 AND status <> 'rejected' GROUP BY 1`); err != nil {
		return nil, err
	}
	if recap["events"], err = count(`SELECT CASE WHEN type = 'stop_event' THEN payload->>'event' ELSE type END,
		count(*) FROM events WHERE patrol_id = $1 GROUP BY 1`); err != nil {
		return nil, err
	}
	var peak sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT max(peak_people) FROM crowd_samples WHERE patrol_id = $1`, id).
		Scan(&peak); err != nil {
		return nil, err
	}
	recap["crowd_peak"] = peak.Int64
	return recap, nil
}

// ---- events

// InsertEvent logs a robot event.
func (s *Store) InsertEvent(ctx context.Context, robotID string, patrolID *int64, kind string, payload []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO events (robot_id, patrol_id, type, payload) VALUES ($1, $2, $3, $4)`,
		robotID, patrolID, kind, payload)
	return err
}

// ListEvents returns the events of a patrol, or the latest events of a robot when patrolID is 0.
func (s *Store) ListEvents(ctx context.Context, robotID string, patrolID int64) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, robot_id, patrol_id, type, payload, ts FROM events
		WHERE ($1 = 0 OR patrol_id = $1) AND ($2 = '' OR robot_id = $2) ORDER BY ts DESC LIMIT 500`,
		patrolID, robotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var payload []byte
		if err := rows.Scan(&e.ID, &e.RobotID, &e.PatrolID, &e.Type, &payload, &e.TS); err != nil {
			return nil, err
		}
		e.Payload = payload
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- anomalies and rules

const anomalyCols = `id, uid, robot_id, patrol_id, route_id, detected_at, early_category, early_confidence, early_note,
	final_category, final_confidence, final_note, x, y, yaw, stop_index, image_path, status, validated_by,
	validated_at, notified_at, created_at`

func scanAnomaly(row interface{ Scan(...any) error }) (Anomaly, error) {
	var a Anomaly
	err := row.Scan(&a.ID, &a.UID, &a.RobotID, &a.PatrolID, &a.RouteID, &a.DetectedAt, &a.EarlyCategory,
		&a.EarlyConfidence, &a.EarlyNote, &a.FinalCategory, &a.FinalConfidence, &a.FinalNote, &a.X, &a.Y, &a.Yaw,
		&a.StopIndex, &a.ImagePath, &a.Status, &a.ValidatedBy, &a.ValidatedAt, &a.NotifiedAt, &a.CreatedAt)
	return a, err
}

// InsertAnomaly stores an anomaly once per uid; returns its id and whether it was new.
func (s *Store) InsertAnomaly(ctx context.Context, a Anomaly) (int64, bool, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO anomalies (uid, robot_id, patrol_id, route_id, detected_at,
		early_category, early_confidence, early_note, x, y, yaw, stop_index, image_path)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (uid) DO NOTHING RETURNING id`,
		a.UID, a.RobotID, a.PatrolID, a.RouteID, a.DetectedAt, a.EarlyCategory, a.EarlyConfidence, a.EarlyNote,
		a.X, a.Y, a.Yaw, a.StopIndex, a.ImagePath).Scan(&id)
	if err == sql.ErrNoRows {
		err = s.db.QueryRowContext(ctx, `SELECT id FROM anomalies WHERE uid = $1`, a.UID).Scan(&id)
		return id, false, err
	}
	return id, err == nil, err
}

// ClaimQueuedAnomaly moves the oldest queued anomaly to analyzing and returns it.
func (s *Store) ClaimQueuedAnomaly(ctx context.Context) (Anomaly, error) {
	a, err := scanAnomaly(s.db.QueryRowContext(ctx, `UPDATE anomalies SET status = 'analyzing'
		WHERE id = (SELECT id FROM anomalies WHERE status = 'queued' ORDER BY created_at LIMIT 1
		FOR UPDATE SKIP LOCKED) RETURNING `+anomalyCols))
	return a, notFound(err)
}

// RequeueStale returns anomalies stuck in analyzing (e.g. after a restart) to the queue.
func (s *Store) RequeueStale(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE anomalies SET status = 'queued' WHERE status = 'analyzing'`)
	return err
}

// SetFinal stores the final detection and the next status.
func (s *Store) SetFinal(ctx context.Context, id int64, category string, confidence float64, note, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE anomalies SET final_category = $2, final_confidence = $3,
		final_note = $4, status = $5 WHERE id = $1`, id, category, confidence, note, status)
	return err
}

// MarkNotified stamps the notification time and moves the anomaly to status.
func (s *Store) MarkNotified(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE anomalies SET notified_at = now(), status = $2 WHERE id = $1`, id, status)
	return err
}

// Validate records an operator decision.
func (s *Store) Validate(ctx context.Context, id int64, status, category, note, user string) error {
	return s.execOne(ctx, `UPDATE anomalies SET status = $2, final_category = COALESCE(NULLIF($3, ''), final_category),
		final_note = CASE WHEN $4 = '' THEN final_note ELSE $4 END, validated_by = $5, validated_at = now()
		WHERE id = $1`, id, status, category, note, user)
}

// GetAnomaly returns one anomaly.
func (s *Store) GetAnomaly(ctx context.Context, id int64) (Anomaly, error) {
	a, err := scanAnomaly(s.db.QueryRowContext(ctx, `SELECT `+anomalyCols+` FROM anomalies WHERE id = $1`, id))
	return a, notFound(err)
}

// ListAnomalies filters anomalies by status, category, robot, patrol and time.
func (s *Store) ListAnomalies(ctx context.Context, status, category, robotID string, patrolID int64,
	from, to time.Time) ([]Anomaly, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+anomalyCols+` FROM anomalies
		WHERE ($1 = '' OR status = $1) AND ($2 = '' OR COALESCE(final_category, early_category) = $2)
		AND ($3 = '' OR robot_id = $3) AND ($4 = 0 OR patrol_id = $4) AND detected_at BETWEEN $5 AND $6
		ORDER BY detected_at DESC LIMIT 500`, status, category, robotID, patrolID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Anomaly{}
	for rows.Next() {
		a, err := scanAnomaly(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListRules returns every anomaly rule.
func (s *Store) ListRules(ctx context.Context) ([]Rule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT category, label, requires_validation, notify_telegram,
		telegram_chat_id, min_confidence FROM anomaly_rules ORDER BY category`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.Category, &r.Label, &r.RequiresValidation, &r.NotifyTelegram, &r.TelegramChatID,
			&r.MinConfidence); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Rule returns the rule of a category, falling back to "unknown".
func (s *Store) Rule(ctx context.Context, category string) (Rule, error) {
	var r Rule
	err := s.db.QueryRowContext(ctx, `SELECT category, label, requires_validation, notify_telegram,
		telegram_chat_id, min_confidence FROM anomaly_rules WHERE category IN ($1, 'unknown')
		ORDER BY category = $1 DESC LIMIT 1`, category).
		Scan(&r.Category, &r.Label, &r.RequiresValidation, &r.NotifyTelegram, &r.TelegramChatID, &r.MinConfidence)
	return r, notFound(err)
}

// SaveRule inserts or updates a rule.
func (s *Store) SaveRule(ctx context.Context, r Rule) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO anomaly_rules (category, label, requires_validation,
		notify_telegram, telegram_chat_id, min_confidence) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (category) DO UPDATE SET label = $2, requires_validation = $3, notify_telegram = $4,
		telegram_chat_id = $5, min_confidence = $6`,
		r.Category, r.Label, r.RequiresValidation, r.NotifyTelegram, r.TelegramChatID, r.MinConfidence)
	return err
}

// DeleteRule removes a rule (except the "unknown" fallback).
func (s *Store) DeleteRule(ctx context.Context, category string) error {
	return s.execOne(ctx, `DELETE FROM anomaly_rules WHERE category = $1 AND category <> 'unknown'`, category)
}

// ---- crowd

// InsertCrowd stores one crowd window.
func (s *Store) InsertCrowd(ctx context.Context, robotID string, patrolID *int64, start, end time.Time,
	cellSize float64, cells []byte, peak int) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO crowd_samples (robot_id, patrol_id, ts_start, ts_end, cell_size,
		cells, peak_people) VALUES ($1, $2, $3, $4, $5, $6, $7)`, robotID, patrolID, start, end, cellSize, cells, peak)
	return err
}

// Heatmap aggregates crowd cells in a time range: peak and mean people per cell.
func (s *Store) Heatmap(ctx context.Context, robotID string, from, to time.Time) ([]HeatCell, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT cell_size, cells FROM crowd_samples
		WHERE ($1 = '' OR robot_id = $1) AND ts_start BETWEEN $2 AND $3`, robotID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ x, y, size float64 }
	acc := map[key]*HeatCell{}
	for rows.Next() {
		var size float64
		var raw []byte
		if err := rows.Scan(&size, &raw); err != nil {
			return nil, err
		}
		var cells [][3]float64
		if json.Unmarshal(raw, &cells) != nil {
			continue
		}
		for _, c := range cells {
			k := key{c[0] * size, c[1] * size, size}
			h, ok := acc[k]
			if !ok {
				h = &HeatCell{X: k.x, Y: k.y, Size: size}
				acc[k] = h
			}
			h.Peak = int(math.Max(float64(h.Peak), c[2]))
			h.Mean += c[2]
			h.Count++
		}
	}
	out := make([]HeatCell, 0, len(acc))
	for _, h := range acc {
		h.Mean = math.Round(h.Mean/float64(h.Count)*100) / 100
		out = append(out, *h)
	}
	return out, rows.Err()
}

// CrowdTimeline returns the peak people per bucket ("hour" or "day") in the server time zone.
func (s *Store) CrowdTimeline(ctx context.Context, robotID, bucket, tz string, from, to time.Time) ([]TimelinePoint, error) {
	if bucket != "day" {
		bucket = "hour"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT date_trunc($1, ts_start AT TIME ZONE $2) AT TIME ZONE $2 AS b,
		max(peak_people) FROM crowd_samples WHERE ($3 = '' OR robot_id = $3) AND ts_start BETWEEN $4 AND $5
		GROUP BY b ORDER BY b`, bucket, tz, robotID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TimelinePoint{}
	for rows.Next() {
		var p TimelinePoint
		if err := rows.Scan(&p.Bucket, &p.Peak); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- floor plans

const planCols = `id, name, file_path, content_type, width_px, height_px, origin_x_px, origin_y_px, px_per_m,
	rotation_rad, created_at`

func scanPlan(row interface{ Scan(...any) error }) (FloorPlan, error) {
	var p FloorPlan
	err := row.Scan(&p.ID, &p.Name, &p.FilePath, &p.ContentType, &p.WidthPx, &p.HeightPx, &p.OriginXPx, &p.OriginYPx,
		&p.PxPerM, &p.RotationRad, &p.CreatedAt)
	return p, err
}

// InsertFloorPlan stores an uploaded plan.
func (s *Store) InsertFloorPlan(ctx context.Context, p FloorPlan) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO floor_plans (name, file_path, content_type, width_px, height_px)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, p.Name, p.FilePath, p.ContentType, p.WidthPx, p.HeightPx).Scan(&id)
	return id, err
}

// ListFloorPlans returns every plan.
func (s *Store) ListFloorPlans(ctx context.Context) ([]FloorPlan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+planCols+` FROM floor_plans ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FloorPlan{}
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetFloorPlan returns one plan.
func (s *Store) GetFloorPlan(ctx context.Context, id int64) (FloorPlan, error) {
	p, err := scanPlan(s.db.QueryRowContext(ctx, `SELECT `+planCols+` FROM floor_plans WHERE id = $1`, id))
	return p, notFound(err)
}

// CalibrateFloorPlan stores the map-to-plan transform and plan size.
func (s *Store) CalibrateFloorPlan(ctx context.Context, p FloorPlan) error {
	return s.execOne(ctx, `UPDATE floor_plans SET name = $2, width_px = $3, height_px = $4, origin_x_px = $5,
		origin_y_px = $6, px_per_m = $7, rotation_rad = $8 WHERE id = $1`,
		p.ID, p.Name, p.WidthPx, p.HeightPx, p.OriginXPx, p.OriginYPx, p.PxPerM, p.RotationRad)
}

// DeleteFloorPlan removes a plan and returns its file path.
func (s *Store) DeleteFloorPlan(ctx context.Context, id int64) (string, error) {
	var path string
	err := s.db.QueryRowContext(ctx, `DELETE FROM floor_plans WHERE id = $1 RETURNING file_path`, id).Scan(&path)
	return path, notFound(err)
}

func (s *Store) execOne(ctx context.Context, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
