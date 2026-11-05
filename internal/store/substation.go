package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/substation"
	"github.com/lib/pq"
)

// ErrConflict means a value that must be unique is taken.
var ErrConflict = errors.New("already exists")

// ErrStatus means the record is not in a status that allows the change.
var ErrStatus = errors.New("not allowed in this status")

// InUseError means a record is still referenced and cannot be deleted.
type InUseError struct{ By string }

func (e InUseError) Error() string { return "still used by " + e.By }

func uniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// SettingSubstation keeps the substation settings (substation.SiteSettings as JSON).
const SettingSubstation = "substation"

// ---- assets

// Asset is a piece of substation equipment.
type Asset struct {
	ID        int64     `json:"id"`
	Tag       string    `json:"tag"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Bay       string    `json:"bay"`
	VoltageKV *float64  `json:"voltage_kv"`
	Phase     string    `json:"phase"`
	X         *float64  `json:"x"`
	Y         *float64  `json:"y"`
	Notes     string    `json:"notes"`
	Archived  bool      `json:"archived"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Its condition: the most severe priority of its findings not resolved (null: none), how many
	// there are, and the latest inspection of a point that measures it.
	Priority      *int       `json:"priority"`
	OpenFindings  int        `json:"open_findings"`
	LastInspected *time.Time `json:"last_inspected"`
}

const assetQuery = `SELECT a.id, a.tag, a.name, a.kind, a.bay, a.voltage_kv, a.phase, a.x, a.y, a.notes,
	a.archived, a.created_at, a.updated_at, f.priority, COALESCE(f.n, 0), li.last
	FROM assets a
	LEFT JOIN LATERAL (SELECT min(priority) AS priority, count(*) AS n FROM findings
		WHERE asset_id = a.id AND status <> 'resolved') f ON true
	LEFT JOIN LATERAL (SELECT max(i.taken_at) AS last FROM inspections i
		JOIN inspection_points p ON p.id = i.point_id
		WHERE p.asset_id = a.id OR p.targets @> jsonb_build_array(jsonb_build_object('asset_id', a.id))) li ON true`

func scanAsset(row interface{ Scan(...any) error }) (Asset, error) {
	var a Asset
	var priority sql.NullInt64
	err := row.Scan(&a.ID, &a.Tag, &a.Name, &a.Kind, &a.Bay, &a.VoltageKV, &a.Phase, &a.X, &a.Y, &a.Notes, &a.Archived,
		&a.CreatedAt, &a.UpdatedAt, &priority, &a.OpenFindings, &a.LastInspected)
	a.Priority = intPtr(priority)
	return a, err
}

func intPtr(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}

// ListAssets returns the assets with their condition by bay and tag; archived ones when asked.
func (s *Store) ListAssets(ctx context.Context, archived bool) ([]Asset, error) {
	rows, err := s.db.QueryContext(ctx, assetQuery+` WHERE $1 OR NOT a.archived ORDER BY a.bay, a.tag`, archived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Asset{}
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAsset returns one asset with its condition.
func (s *Store) GetAsset(ctx context.Context, id int64) (Asset, error) {
	a, err := scanAsset(s.db.QueryRowContext(ctx, assetQuery+` WHERE a.id = $1`, id))
	return a, notFound(err)
}

// CreateAsset stores an asset; ErrConflict when its tag is taken (ignoring case).
func (s *Store) CreateAsset(ctx context.Context, a Asset) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO assets (tag, name, kind, bay, voltage_kv, phase, x, y, notes, archived)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		a.Tag, a.Name, a.Kind, a.Bay, a.VoltageKV, a.Phase, a.X, a.Y, a.Notes, a.Archived).Scan(&id)
	if uniqueViolation(err) {
		return 0, ErrConflict
	}
	return id, err
}

// UpdateAsset changes an asset; ErrConflict when its new tag is taken.
func (s *Store) UpdateAsset(ctx context.Context, a Asset) error {
	err := s.execOne(ctx, `UPDATE assets SET tag = $2, name = $3, kind = $4, bay = $5, voltage_kv = $6, phase = $7,
		x = $8, y = $9, notes = $10, archived = $11, updated_at = now() WHERE id = $1`,
		a.ID, a.Tag, a.Name, a.Kind, a.Bay, a.VoltageKV, a.Phase, a.X, a.Y, a.Notes, a.Archived)
	if uniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// DeleteAsset deletes an asset that no inspection point or finding refers to (InUseError otherwise:
// archive it instead).
func (s *Store) DeleteAsset(ctx context.Context, id int64) error {
	var points, findings int
	if err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM inspection_points WHERE asset_id = $1
			OR targets @> jsonb_build_array(jsonb_build_object('asset_id', $1::bigint))),
		(SELECT count(*) FROM findings WHERE asset_id = $1)`, id).Scan(&points, &findings); err != nil {
		return err
	}
	switch {
	case points > 0:
		return InUseError{fmt.Sprintf("%d inspection point(s)", points)}
	case findings > 0:
		return InUseError{fmt.Sprintf("%d finding(s)", findings)}
	}
	return s.execOne(ctx, `DELETE FROM assets WHERE id = $1`, id)
}

// MissingAssets returns those of ids that are no asset.
func (s *Store) MissingAssets(ctx context.Context, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT u.id FROM unnest($1::bigint[]) AS u(id)
		WHERE NOT EXISTS (SELECT 1 FROM assets a WHERE a.id = u.id)`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---- inspection points

// InspectionPoint is where a robot inspects: a stop of a route, a camera pose, and what it measures.
type InspectionPoint struct {
	ID        int64               `json:"id"`
	Name      string              `json:"name"`
	Kind      string              `json:"kind"`
	AssetID   *int64              `json:"asset_id"`
	RouteID   *int64              `json:"route_id"`
	StopIndex *int                `json:"stop_index"`
	Seq       int                 `json:"seq"`
	PTZ       *substation.PTZ     `json:"ptz"`
	Settings  substation.Settings `json:"settings"`
	Targets   []substation.Target `json:"targets"`
	Enabled   bool                `json:"enabled"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
	// The latest inspection and the most severe priority of its findings not resolved.
	LastInspected *time.Time `json:"last_inspected"`
	Priority      *int       `json:"priority"`
}

const pointQuery = `SELECT p.id, p.name, p.kind, p.asset_id, p.route_id, p.stop_index, p.seq, p.ptz, p.settings,
	p.targets, p.enabled, p.created_at, p.updated_at, li.last, f.priority
	FROM inspection_points p
	LEFT JOIN LATERAL (SELECT max(taken_at) AS last FROM inspections WHERE point_id = p.id) li ON true
	LEFT JOIN LATERAL (SELECT min(priority) AS priority FROM findings
		WHERE point_id = p.id AND status <> 'resolved') f ON true`

func scanPoint(row interface{ Scan(...any) error }) (InspectionPoint, error) {
	var p InspectionPoint
	var stop, priority sql.NullInt64
	var ptz, settings, targets []byte
	if err := row.Scan(&p.ID, &p.Name, &p.Kind, &p.AssetID, &p.RouteID, &stop, &p.Seq, &ptz, &settings, &targets,
		&p.Enabled, &p.CreatedAt, &p.UpdatedAt, &p.LastInspected, &priority); err != nil {
		return p, err
	}
	p.StopIndex, p.Priority = intPtr(stop), intPtr(priority)
	if len(ptz) > 0 && string(ptz) != "null" {
		p.PTZ = &substation.PTZ{}
		if err := json.Unmarshal(ptz, p.PTZ); err != nil {
			return p, err
		}
	}
	if err := json.Unmarshal(settings, &p.Settings); err != nil {
		return p, err
	}
	if err := json.Unmarshal(targets, &p.Targets); err != nil {
		return p, err
	}
	if p.Targets == nil {
		p.Targets = []substation.Target{}
	}
	return p, nil
}

// ListPoints returns inspection points by route, stop and order: of one route when routeID is set,
// enabled ones only when asked.
func (s *Store) ListPoints(ctx context.Context, routeID int64, enabledOnly bool) ([]InspectionPoint, error) {
	rows, err := s.db.QueryContext(ctx, pointQuery+` WHERE ($1 = 0 OR p.route_id = $1) AND (NOT $2 OR p.enabled)
		ORDER BY p.route_id NULLS LAST, p.stop_index NULLS LAST, p.seq, p.id`, routeID, enabledOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InspectionPoint{}
	for rows.Next() {
		p, err := scanPoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPoint returns one inspection point.
func (s *Store) GetPoint(ctx context.Context, id int64) (InspectionPoint, error) {
	p, err := scanPoint(s.db.QueryRowContext(ctx, pointQuery+` WHERE p.id = $1`, id))
	return p, notFound(err)
}

func pointJSON(p InspectionPoint) (ptz, settings, targets []byte, err error) {
	if p.PTZ != nil {
		if ptz, err = json.Marshal(p.PTZ); err != nil {
			return
		}
	}
	if settings, err = json.Marshal(p.Settings); err != nil {
		return
	}
	if p.Targets == nil {
		p.Targets = []substation.Target{}
	}
	targets, err = json.Marshal(p.Targets)
	return
}

// CreatePoint stores an inspection point.
func (s *Store) CreatePoint(ctx context.Context, p InspectionPoint) (int64, error) {
	ptz, settings, targets, err := pointJSON(p)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `INSERT INTO inspection_points (name, kind, asset_id, route_id, stop_index, seq,
		ptz, settings, targets, enabled) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		p.Name, p.Kind, p.AssetID, p.RouteID, p.StopIndex, p.Seq, nullJSON(ptz), settings, targets, p.Enabled).Scan(&id)
	return id, err
}

// UpdatePoint changes an inspection point. Its kind stays: inspections and findings depend on it.
func (s *Store) UpdatePoint(ctx context.Context, p InspectionPoint) error {
	ptz, settings, targets, err := pointJSON(p)
	if err != nil {
		return err
	}
	return s.execOne(ctx, `UPDATE inspection_points SET name = $2, asset_id = $3, route_id = $4, stop_index = $5,
		seq = $6, ptz = $7, settings = $8, targets = $9, enabled = $10, updated_at = now() WHERE id = $1`,
		p.ID, p.Name, p.AssetID, p.RouteID, p.StopIndex, p.Seq, nullJSON(ptz), settings, targets, p.Enabled)
}

// DeletePoint deletes an inspection point without inspections (InUseError otherwise: disable it to
// keep its history).
func (s *Store) DeletePoint(ctx context.Context, id int64) error {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM inspections WHERE point_id = $1`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return InUseError{fmt.Sprintf("%d inspection(s)", n)}
	}
	return s.execOne(ctx, `DELETE FROM inspection_points WHERE id = $1`, id)
}

func nullJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// ---- inspections

// Inspection is one capture at one inspection point with its evaluation.
type Inspection struct {
	ID        int64               `json:"id"`
	UID       string              `json:"uid"`
	PointID   int64               `json:"point_id"`
	PointName string              `json:"point_name"`
	RobotID   *string             `json:"robot_id"`
	PatrolID  *int64              `json:"patrol_id"`
	TakenAt   time.Time           `json:"taken_at"`
	Kind      string              `json:"kind"`
	Reference string              `json:"reference,omitempty"`
	AmbientC  *float64            `json:"ambient_c"`
	Thermal   []substation.Result `json:"thermal,omitempty"`
	PD        *PDResult           `json:"pd,omitempty"`
	Priority  int                 `json:"priority"`
	X         *float64            `json:"x"`
	Y         *float64            `json:"y"`
	Yaw       *float64            `json:"yaw"`
	Images    map[string]string   `json:"-"`      // image name (thermal, visual, acoustic) to file path
	ImageList []string            `json:"images"` // the image names, for GET .../image/{name}
	CreatedAt time.Time           `json:"created_at"`
}

// PDResult is a partial discharge reading with its priority.
type PDResult struct {
	substation.PDReading
	Priority int `json:"priority"`
}

const inspectionQuery = `SELECT i.id, i.uid, i.point_id, p.name, i.robot_id, i.patrol_id, i.taken_at, i.kind,
	i.reference, i.ambient_c, i.thermal, i.pd, i.priority, i.x, i.y, i.yaw, i.images, i.created_at
	FROM inspections i JOIN inspection_points p ON p.id = i.point_id`

func scanInspection(row interface{ Scan(...any) error }) (Inspection, error) {
	var in Inspection
	var thermal, pd, images []byte
	if err := row.Scan(&in.ID, &in.UID, &in.PointID, &in.PointName, &in.RobotID, &in.PatrolID, &in.TakenAt, &in.Kind,
		&in.Reference, &in.AmbientC, &thermal, &pd, &in.Priority, &in.X, &in.Y, &in.Yaw, &images, &in.CreatedAt); err != nil {
		return in, err
	}
	if len(thermal) > 0 {
		if err := json.Unmarshal(thermal, &in.Thermal); err != nil {
			return in, err
		}
	}
	if len(pd) > 0 && string(pd) != "null" {
		in.PD = &PDResult{}
		if err := json.Unmarshal(pd, in.PD); err != nil {
			return in, err
		}
	}
	if err := json.Unmarshal(images, &in.Images); err != nil {
		return in, err
	}
	in.ImageList = []string{}
	for _, name := range []string{"thermal", "visual", "acoustic"} {
		if in.Images[name] != "" {
			in.ImageList = append(in.ImageList, name)
		}
	}
	return in, nil
}

// InspectionFilter selects inspections; zero values do not filter.
type InspectionFilter struct {
	PointID     int64
	PatrolID    int64
	RobotID     string
	From, To    time.Time
	MaxPriority int // 1–4: only inspections rated this or more severe
	Limit       int
	Ascending   bool // oldest first (trends)
}

// ListInspections returns inspections, the newest first unless asked otherwise.
func (s *Store) ListInspections(ctx context.Context, f InspectionFilter) ([]Inspection, error) {
	if f.Limit <= 0 || f.Limit > 5000 {
		f.Limit = 500
	}
	order := "DESC"
	if f.Ascending {
		order = "ASC"
	}
	rows, err := s.db.QueryContext(ctx, inspectionQuery+` WHERE ($1 = 0 OR i.point_id = $1)
		AND ($2 = 0 OR i.patrol_id = $2) AND ($3 = '' OR i.robot_id = $3) AND i.taken_at BETWEEN $4 AND $5
		AND ($6 = 0 OR i.priority BETWEEN 1 AND $6) ORDER BY i.taken_at `+order+`, i.id `+order+` LIMIT $7`,
		f.PointID, f.PatrolID, f.RobotID, f.From, f.To, f.MaxPriority, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Inspection{}
	for rows.Next() {
		in, err := scanInspection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// GetInspection returns one inspection.
func (s *Store) GetInspection(ctx context.Context, id int64) (Inspection, error) {
	in, err := scanInspection(s.db.QueryRowContext(ctx, inspectionQuery+` WHERE i.id = $1`, id))
	return in, notFound(err)
}

// Observation is one evaluated target of an inspection (a thermal target, or "pd"), for its finding.
type Observation struct {
	TargetKey string
	Label     string
	AssetID   *int64
	Priority  int // substation.None when normal
	Basis     string
	Value     *float64 // °C or dB
	DeltaC    *float64
}

// FindingChange is what recording an inspection did to a finding: opened, escalated (rated more
// severe than before; an acknowledged finding opens again), seen (abnormal again) or normal (the
// latest reading did not open a finding, the finding stays until an operator resolves it).
type FindingChange struct {
	Finding Finding
	Event   string
	Before  int // the priority before; 0 when it opened
}

// RecordInspection stores an inspection once per uid and updates the findings of its targets in
// one transaction. A target rated at least as severe as the settings' finding priority opens a
// finding or updates the one not resolved yet. It returns the inspection id, whether it is new,
// and the findings it changed (none for a repeated upload).
func (s *Store) RecordInspection(ctx context.Context, in Inspection, obs []Observation,
	site substation.SiteSettings) (int64, bool, []FindingChange, error) {
	var thermal, pd any
	if in.Thermal != nil {
		b, err := json.Marshal(in.Thermal)
		if err != nil {
			return 0, false, nil, err
		}
		thermal = b
	}
	if in.PD != nil {
		b, err := json.Marshal(in.PD)
		if err != nil {
			return 0, false, nil, err
		}
		pd = b
	}
	if in.Images == nil {
		in.Images = map[string]string{}
	}
	images, err := json.Marshal(in.Images)
	if err != nil {
		return 0, false, nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO inspections (uid, point_id, robot_id, patrol_id, taken_at, kind,
		reference, ambient_c, thermal, pd, priority, x, y, yaw, images)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (uid) DO NOTHING RETURNING id`,
		in.UID, in.PointID, in.RobotID, in.PatrolID, in.TakenAt, in.Kind, in.Reference, in.AmbientC, thermal, pd,
		in.Priority, in.X, in.Y, in.Yaw, images).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.QueryRowContext(ctx, `SELECT id FROM inspections WHERE uid = $1`, in.UID).Scan(&id); err != nil {
			return 0, false, nil, err
		}
		return id, false, nil, tx.Commit()
	}
	if err != nil {
		return 0, false, nil, err
	}
	changes := []FindingChange{}
	for _, o := range obs {
		ch, err := recordObservation(ctx, tx, in, id, o, site)
		if err != nil {
			return 0, false, nil, err
		}
		if ch != nil {
			changes = append(changes, *ch)
		}
	}
	return id, true, changes, tx.Commit()
}

func recordObservation(ctx context.Context, tx *sql.Tx, in Inspection, inspectionID int64, o Observation,
	site substation.SiteSettings) (*FindingChange, error) {
	abnormal := site.Opens(o.Priority)
	for attempt := 0; attempt < 2; attempt++ {
		var id int64
		var before int
		err := tx.QueryRowContext(ctx, `SELECT id, priority FROM findings
			WHERE point_id = $1 AND target_key = $2 AND status <> 'resolved' FOR UPDATE`, in.PointID, o.TargetKey).
			Scan(&id, &before)
		event := ""
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if !abnormal {
				return nil, nil
			}
			err = tx.QueryRowContext(ctx, `INSERT INTO findings (point_id, target_key, label, asset_id, kind, priority,
				basis, last_priority, first_inspection_id, last_inspection_id, first_seen, last_seen, last_value,
				last_delta_c) VALUES ($1, $2, $3, $4, $5, $6, $7, $6, $8, $8, $9, $9, $10, $11)
				ON CONFLICT (point_id, target_key) WHERE status <> 'resolved' DO NOTHING RETURNING id`,
				in.PointID, o.TargetKey, o.Label, o.AssetID, in.Kind, o.Priority, o.Basis, inspectionID, in.TakenAt,
				o.Value, o.DeltaC).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				continue // opened by a concurrent upload: update that one
			}
			event, before = "opened", substation.None
		case err != nil:
			return nil, err
		case abnormal:
			event = "seen"
			if o.Priority < before {
				event = "escalated"
			}
			_, err = tx.ExecContext(ctx, `UPDATE findings SET priority = LEAST(priority, $2),
				basis = CASE WHEN $2 < priority THEN $3 ELSE basis END,
				status = CASE WHEN $2 < priority THEN 'open' ELSE status END,
				last_priority = $2, last_inspection_id = $4, last_seen = $5, last_value = $6, last_delta_c = $7,
				occurrences = occurrences + 1, label = $8, asset_id = $9, updated_at = now() WHERE id = $1`,
				id, o.Priority, o.Basis, inspectionID, in.TakenAt, o.Value, o.DeltaC, o.Label, o.AssetID)
		default:
			event = "normal"
			_, err = tx.ExecContext(ctx, `UPDATE findings SET last_priority = $2, last_inspection_id = $3, last_seen = $4,
				last_value = $5, last_delta_c = $6, updated_at = now() WHERE id = $1`,
				id, o.Priority, inspectionID, in.TakenAt, o.Value, o.DeltaC)
		}
		if err != nil {
			return nil, err
		}
		f, err := getFinding(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		return &FindingChange{Finding: f, Event: event, Before: before}, nil
	}
	return nil, errors.New("finding changed concurrently, retry the upload")
}

// ---- findings

// Finding is a deficiency of one target followed from its first abnormal reading until an
// operator resolves it.
type Finding struct {
	ID                int64      `json:"id"`
	PointID           int64      `json:"point_id"`
	PointName         string     `json:"point_name"`
	TargetKey         string     `json:"target_key"`
	Label             string     `json:"label"`
	AssetID           *int64     `json:"asset_id"`
	AssetTag          *string    `json:"asset_tag"`
	AssetName         *string    `json:"asset_name"`
	Kind              string     `json:"kind"`
	Status            string     `json:"status"`
	Priority          int        `json:"priority"`
	Basis             string     `json:"basis"`
	LastPriority      int        `json:"last_priority"`
	FirstInspectionID int64      `json:"first_inspection_id"`
	LastInspectionID  int64      `json:"last_inspection_id"`
	FirstSeen         time.Time  `json:"first_seen"`
	LastSeen          time.Time  `json:"last_seen"`
	LastValue         *float64   `json:"last_value"`
	LastDeltaC        *float64   `json:"last_delta_c"`
	Occurrences       int        `json:"occurrences"`
	Note              string     `json:"note"`
	AcknowledgedBy    *string    `json:"acknowledged_by"`
	AcknowledgedAt    *time.Time `json:"acknowledged_at"`
	ResolvedBy        *string    `json:"resolved_by"`
	ResolvedAt        *time.Time `json:"resolved_at"`
	NotifiedAt        *time.Time `json:"notified_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

const findingQuery = `SELECT f.id, f.point_id, p.name, f.target_key, f.label, f.asset_id, a.tag, a.name, f.kind,
	f.status, f.priority, f.basis, f.last_priority, f.first_inspection_id, f.last_inspection_id, f.first_seen,
	f.last_seen, f.last_value, f.last_delta_c, f.occurrences, f.note, f.acknowledged_by, f.acknowledged_at,
	f.resolved_by, f.resolved_at, f.notified_at, f.created_at, f.updated_at
	FROM findings f JOIN inspection_points p ON p.id = f.point_id LEFT JOIN assets a ON a.id = f.asset_id`

func scanFinding(row interface{ Scan(...any) error }) (Finding, error) {
	var f Finding
	err := row.Scan(&f.ID, &f.PointID, &f.PointName, &f.TargetKey, &f.Label, &f.AssetID, &f.AssetTag, &f.AssetName,
		&f.Kind, &f.Status, &f.Priority, &f.Basis, &f.LastPriority, &f.FirstInspectionID, &f.LastInspectionID,
		&f.FirstSeen, &f.LastSeen, &f.LastValue, &f.LastDeltaC, &f.Occurrences, &f.Note, &f.AcknowledgedBy,
		&f.AcknowledgedAt, &f.ResolvedBy, &f.ResolvedAt, &f.NotifiedAt, &f.CreatedAt, &f.UpdatedAt)
	return f, err
}

func getFinding(ctx context.Context, q queryer, id int64) (Finding, error) {
	f, err := scanFinding(q.QueryRowContext(ctx, findingQuery+` WHERE f.id = $1`, id))
	return f, notFound(err)
}

// GetFinding returns one finding.
func (s *Store) GetFinding(ctx context.Context, id int64) (Finding, error) {
	return getFinding(ctx, s.db, id)
}

// FindingFilter selects findings; zero values do not filter.
type FindingFilter struct {
	Status      string // open, acknowledged, resolved, or active (open and acknowledged)
	MaxPriority int    // 1–4: only findings rated this or more severe
	AssetID     int64
	PointID     int64
	From, To    time.Time // by last seen; zero: any time
	Limit       int
}

// ListFindings returns findings, the most severe and latest first.
func (s *Store) ListFindings(ctx context.Context, f FindingFilter) ([]Finding, error) {
	if f.Limit <= 0 || f.Limit > 5000 {
		f.Limit = 500
	}
	if f.To.IsZero() {
		f.To = time.Now().Add(24 * time.Hour)
	}
	rows, err := s.db.QueryContext(ctx, findingQuery+` WHERE ($1 = '' OR f.status = $1
		OR ($1 = 'active' AND f.status <> 'resolved')) AND ($2 = 0 OR f.priority <= $2) AND ($3 = 0 OR f.asset_id = $3)
		AND ($4 = 0 OR f.point_id = $4) AND f.last_seen BETWEEN $5 AND $6
		ORDER BY (f.status = 'resolved'), f.priority, f.last_seen DESC LIMIT $7`,
		f.Status, f.MaxPriority, f.AssetID, f.PointID, f.From, f.To, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Finding{}
	for rows.Next() {
		fd, err := scanFinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, fd)
	}
	return out, rows.Err()
}

// AcknowledgeFinding records that an operator took an open finding in hand; ErrStatus unless open.
func (s *Store) AcknowledgeFinding(ctx context.Context, id int64, user, note string) error {
	return s.changeFinding(ctx, id, `UPDATE findings SET status = 'acknowledged', acknowledged_by = $2,
		acknowledged_at = now(), note = CASE WHEN $3 = '' THEN note ELSE $3 END, updated_at = now()
		WHERE id = $1 AND status = 'open'`, user, note)
}

// ResolveFinding closes a finding (repaired, or not a deficiency); ErrStatus when it is resolved.
// A later abnormal reading of its target opens a new finding.
func (s *Store) ResolveFinding(ctx context.Context, id int64, user, note string) error {
	return s.changeFinding(ctx, id, `UPDATE findings SET status = 'resolved', resolved_by = $2, resolved_at = now(),
		note = CASE WHEN $3 = '' THEN note ELSE $3 END, updated_at = now() WHERE id = $1 AND status <> 'resolved'`,
		user, note)
}

func (s *Store) changeFinding(ctx context.Context, id int64, query, user, note string) error {
	err := s.execOne(ctx, query, id, user, note)
	if errors.Is(err, ErrNotFound) {
		if _, err := s.GetFinding(ctx, id); err != nil {
			return err
		}
		return ErrStatus
	}
	return err
}

// MarkFindingNotified stamps the time a finding was sent to Telegram.
func (s *Store) MarkFindingNotified(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE findings SET notified_at = now() WHERE id = $1`, id)
	return err
}

// ---- overview

// SubstationSummary is the state of the site at a glance.
type SubstationSummary struct {
	Assets            int            `json:"assets"`              // not archived
	Points            int            `json:"points"`              // enabled
	Findings          map[string]int `json:"findings"`            // not resolved, by priority "1" to "4"
	Unacknowledged    int            `json:"unacknowledged"`      // open findings nobody took in hand
	AssetsByCondition map[string]int `json:"assets_by_condition"` // by the most severe open priority, "ok" for none
	Inspections24h    int            `json:"inspections_24h"`
	LastInspection    *time.Time     `json:"last_inspection"`
}

// Summary returns the state of the site.
func (s *Store) Summary(ctx context.Context) (SubstationSummary, error) {
	out := SubstationSummary{Findings: map[string]int{}, AssetsByCondition: map[string]int{}}
	if err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM assets WHERE NOT archived),
		(SELECT count(*) FROM inspection_points WHERE enabled),
		(SELECT count(*) FROM findings WHERE status = 'open'),
		(SELECT count(*) FROM inspections WHERE taken_at > now() - interval '24 hours'),
		(SELECT max(taken_at) FROM inspections)`).
		Scan(&out.Assets, &out.Points, &out.Unacknowledged, &out.Inspections24h, &out.LastInspection); err != nil {
		return out, err
	}
	count := func(query string, into map[string]int) error {
		rows, err := s.db.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				return err
			}
			into[k] = n
		}
		return rows.Err()
	}
	if err := count(`SELECT priority::text, count(*) FROM findings WHERE status <> 'resolved' GROUP BY 1`,
		out.Findings); err != nil {
		return out, err
	}
	return out, count(`SELECT COALESCE(p::text, 'ok'), count(*) FROM (SELECT (SELECT min(priority) FROM findings f
		WHERE f.asset_id = a.id AND f.status <> 'resolved') AS p FROM assets a WHERE NOT a.archived) x GROUP BY 1`,
		out.AssetsByCondition)
}

// InspectionCoverage tells how much of its route's inspection plan a patrol did.
type InspectionCoverage struct {
	Planned     int     `json:"planned"`     // enabled points of the patrol's route
	Done        int     `json:"done"`        // of them, the points inspected during the patrol
	Inspections int     `json:"inspections"` // captures during the patrol
	Priority    int     `json:"priority"`    // the most severe rating of its captures, 0 none
	Missed      []int64 `json:"missed"`      // planned points not inspected
}

// PatrolCoverage returns the inspection coverage of a patrol.
func (s *Store) PatrolCoverage(ctx context.Context, patrolID int64) (InspectionCoverage, error) {
	c := InspectionCoverage{Missed: []int64{}}
	if err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM inspection_points p JOIN patrols t ON t.route_id = p.route_id WHERE t.id = $1 AND p.enabled),
		(SELECT count(DISTINCT i.point_id) FROM inspections i JOIN inspection_points p ON p.id = i.point_id
			JOIN patrols t ON t.route_id = p.route_id WHERE i.patrol_id = $1 AND t.id = $1 AND p.enabled),
		(SELECT count(*) FROM inspections WHERE patrol_id = $1),
		(SELECT COALESCE(min(priority) FILTER (WHERE priority > 0), 0) FROM inspections WHERE patrol_id = $1)`,
		patrolID).Scan(&c.Planned, &c.Done, &c.Inspections, &c.Priority); err != nil {
		return c, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.id FROM inspection_points p JOIN patrols t ON t.route_id = p.route_id
		WHERE t.id = $1 AND p.enabled AND NOT EXISTS (SELECT 1 FROM inspections i WHERE i.patrol_id = $1 AND i.point_id = p.id)
		ORDER BY p.stop_index NULLS LAST, p.seq, p.id`, patrolID)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return c, err
		}
		c.Missed = append(c.Missed, id)
	}
	return c, rows.Err()
}
