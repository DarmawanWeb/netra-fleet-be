package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"
)

// FeedAnomaly is an anomaly as the integration API reads it: with its robot name and last change.
type FeedAnomaly struct {
	Anomaly
	RobotName string
	UpdatedAt time.Time
}

// AnomalyFeedFilter selects anomalies for the integration API; empty fields do not filter.
type AnomalyFeedFilter struct {
	Statuses     []string
	Categories   []string
	RobotID      string
	Final        *bool // final (server) detection done
	Validated    *bool // decided by an operator
	DetectedFrom *time.Time
	DetectedTo   *time.Time
	// Cursor: anomalies changed after (AfterUpdated, AfterID), in that order.
	AfterUpdated time.Time
	AfterID      int64
	Limit        int
}

// FeedSettle keeps the newest changes out of the feed for a moment, so an update that commits a few
// milliseconds late is not skipped by a client whose cursor already passed its timestamp.
const FeedSettle = 2 * time.Second

// feedCols are anomalyCols on alias a, then the last change and the robot name.
const feedCols = `a.id, a.uid, a.robot_id, a.patrol_id, a.route_id, a.detected_at, a.early_category,
	a.early_confidence, a.early_note, a.final_category, a.final_confidence, a.final_note, a.x, a.y,
	a.yaw, a.stop_index, a.image_path, a.status, a.validated_by, a.validated_at, a.notified_at,
	a.created_at, a.updated_at, COALESCE(r.name, a.robot_id)`

func scanFeed(row interface{ Scan(...any) error }) (FeedAnomaly, error) {
	var f FeedAnomaly
	a := &f.Anomaly
	err := row.Scan(&a.ID, &a.UID, &a.RobotID, &a.PatrolID, &a.RouteID, &a.DetectedAt, &a.EarlyCategory,
		&a.EarlyConfidence, &a.EarlyNote, &a.FinalCategory, &a.FinalConfidence, &a.FinalNote, &a.X, &a.Y, &a.Yaw,
		&a.StopIndex, &a.ImagePath, &a.Status, &a.ValidatedBy, &a.ValidatedAt, &a.NotifiedAt, &a.CreatedAt,
		&f.UpdatedAt, &f.RobotName)
	return f, err
}

func nullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

func nullBool(b *bool) sql.NullBool {
	if b == nil {
		return sql.NullBool{}
	}
	return sql.NullBool{Bool: *b, Valid: true}
}

// AnomalyFeed returns anomalies in the order they last changed (oldest change first).
func (s *Store) AnomalyFeed(ctx context.Context, f AnomalyFeedFilter) ([]FeedAnomaly, error) {
	if f.Statuses == nil {
		f.Statuses = []string{}
	}
	if f.Categories == nil {
		f.Categories = []string{}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+feedCols+`
		FROM anomalies a LEFT JOIN robots r ON r.id = a.robot_id
		WHERE (a.updated_at, a.id) > ($1, $2)
		AND a.updated_at <= clock_timestamp() - $11 * interval '1 millisecond'
		AND (cardinality($3::text[]) = 0 OR a.status = ANY($3::text[]))
		AND (cardinality($4::text[]) = 0 OR COALESCE(a.final_category, a.early_category) = ANY($4::text[]))
		AND ($5 = '' OR a.robot_id = $5)
		AND ($6::boolean IS NULL OR (a.final_category IS NOT NULL) = $6::boolean)
		AND ($7::boolean IS NULL OR (a.validated_at IS NOT NULL) = $7::boolean)
		AND ($8::timestamptz IS NULL OR a.detected_at >= $8::timestamptz)
		AND ($9::timestamptz IS NULL OR a.detected_at < $9::timestamptz)
		ORDER BY a.updated_at, a.id
		LIMIT $10`,
		f.AfterUpdated, f.AfterID, pq.Array(f.Statuses), pq.Array(f.Categories), f.RobotID, nullBool(f.Final),
		nullBool(f.Validated), nullTime(f.DetectedFrom), nullTime(f.DetectedTo), f.Limit, FeedSettle.Milliseconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FeedAnomaly{}
	for rows.Next() {
		a, err := scanFeed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// FeedAnomalyByID returns one anomaly with its robot name and last change.
func (s *Store) FeedAnomalyByID(ctx context.Context, id int64) (FeedAnomaly, error) {
	a, err := scanFeed(s.db.QueryRowContext(ctx, `SELECT `+feedCols+`
		FROM anomalies a LEFT JOIN robots r ON r.id = a.robot_id WHERE a.id = $1`, id))
	return a, notFound(err)
}
