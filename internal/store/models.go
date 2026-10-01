package store

import (
	"encoding/json"
	"time"
)

// Robot is a registered robot and its last telemetry.
type Robot struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	FloorPlanID   *int64          `json:"floor_plan_id"`
	LastSeen      *time.Time      `json:"last_seen"`
	LastTelemetry json.RawMessage `json:"last_telemetry"`
	CreatedAt     time.Time       `json:"created_at"`
}

// Route is a recorded teach-and-repeat route.
type Route struct {
	ID         int64        `json:"id"`
	UID        string       `json:"uid"`
	RobotID    string       `json:"robot_id"`
	Name       string       `json:"name"`
	RecordedAt time.Time    `json:"recorded_at"`
	LengthM    float64      `json:"length_m"`
	Stops      int          `json:"stops"`
	Closed     bool         `json:"closed"`
	Archived   bool         `json:"archived"`
	CreatedAt  time.Time    `json:"created_at"`
	Points     [][4]float64 `json:"points,omitempty"`
}

// Schedule starts a patrol on given weekdays at a time of day.
type Schedule struct {
	ID        int64   `json:"id"`
	RobotID   string  `json:"robot_id"`
	RouteID   int64   `json:"route_id"`
	Name      string  `json:"name"`
	Days      []int64 `json:"days"`
	TimeOfDay string  `json:"time_of_day"`
	Enabled   bool    `json:"enabled"`
	LastRunOn *string `json:"last_run_on"`
	OnceOn    *string `json:"once_on"` // YYYY-MM-DD: run once on that date instead of weekly
}

// Patrol is one run of a route.
type Patrol struct {
	ID         int64      `json:"id"`
	RobotID    string     `json:"robot_id"`
	RouteID    *int64     `json:"route_id"`
	ScheduleID *int64     `json:"schedule_id"`
	Status     string     `json:"status"`
	Message    string     `json:"message"`
	Laps       int        `json:"laps"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Anomaly is a robot detection on its way through final detection and validation.
type Anomaly struct {
	ID              int64      `json:"id"`
	UID             string     `json:"uid"`
	RobotID         string     `json:"robot_id"`
	PatrolID        *int64     `json:"patrol_id"`
	RouteID         *int64     `json:"route_id"`
	DetectedAt      time.Time  `json:"detected_at"`
	EarlyCategory   string     `json:"early_category"`
	EarlyConfidence float64    `json:"early_confidence"`
	EarlyNote       string     `json:"early_note"`
	FinalCategory   *string    `json:"final_category"`
	FinalConfidence *float64   `json:"final_confidence"`
	FinalNote       string     `json:"final_note"`
	X               *float64   `json:"x"`
	Y               *float64   `json:"y"`
	Yaw             *float64   `json:"yaw"`
	StopIndex       *int64     `json:"stop_index"`
	ImagePath       string     `json:"-"`
	Status          string     `json:"status"`
	ValidatedBy     *string    `json:"validated_by"`
	ValidatedAt     *time.Time `json:"validated_at"`
	NotifiedAt      *time.Time `json:"notified_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

// Rule decides what happens to an anomaly category.
type Rule struct {
	Category           string  `json:"category"`
	Label              string  `json:"label"`
	RequiresValidation bool    `json:"requires_validation"`
	NotifyTelegram     bool    `json:"notify_telegram"`
	TelegramChatID     string  `json:"telegram_chat_id"`
	MinConfidence      float64 `json:"min_confidence"`
}

// FloorPlan is a vector or raster mall layout calibrated against the robot map frame.
type FloorPlan struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	FilePath    string    `json:"-"`
	ContentType string    `json:"content_type"`
	WidthPx     float64   `json:"width_px"`
	HeightPx    float64   `json:"height_px"`
	OriginXPx   float64   `json:"origin_x_px"`
	OriginYPx   float64   `json:"origin_y_px"`
	PxPerM      float64   `json:"px_per_m"`
	RotationRad float64   `json:"rotation_rad"`
	CreatedAt   time.Time `json:"created_at"`
}

// Event is a logged robot event (stop point, alert, patrol lifecycle).
type Event struct {
	ID       int64           `json:"id"`
	RobotID  string          `json:"robot_id"`
	PatrolID *int64          `json:"patrol_id"`
	Type     string          `json:"type"`
	Payload  json.RawMessage `json:"payload"`
	TS       time.Time       `json:"ts"`
}

// HeatCell is one aggregated crowd cell.
type HeatCell struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Size  float64 `json:"size"`
	Peak  int     `json:"peak"`
	Mean  float64 `json:"mean"`
	Count int     `json:"samples"`
}

// TimelinePoint is the peak number of people in one time bucket.
type TimelinePoint struct {
	Bucket time.Time `json:"bucket"`
	Peak   int       `json:"peak"`
}
