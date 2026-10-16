package ext

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
)

// RobotState is the last telemetry of a robot, reduced to what integrations need.
type RobotState struct {
	TS         *float64   `json:"ts"`
	NavState   string     `json:"nav_state"`
	Mode       string     `json:"mode"`
	BatteryPct *float64   `json:"battery_pct"`
	People     int        `json:"people"`
	Estop      bool       `json:"estop"`
	Pose       *Position  `json:"pose"`
	Patrol     *PatrolRef `json:"patrol"`
}

// PatrolRef is the patrol a robot is running.
type PatrolRef struct {
	PatrolID *int64 `json:"patrol_id"`
	RouteID  *int64 `json:"route_id"`
}

// Robot is a robot with its connection and last known state.
type Robot struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Online      bool        `json:"online"`
	LastSeen    *time.Time  `json:"last_seen"`
	FloorPlanID *int64      `json:"floor_plan_id"`
	State       *RobotState `json:"state"`
}

func robotState(raw json.RawMessage) *RobotState {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var t struct {
		TS   *float64 `json:"ts"`
		Mode string   `json:"mode"`
		Nav  struct {
			State string `json:"state"`
		} `json:"nav"`
		Battery *struct {
			Pct *float64 `json:"pct"`
		} `json:"battery"`
		People int                          `json:"people"`
		Estop  bool                         `json:"estop"`
		Pose   *struct{ X, Y, Yaw float64 } `json:"pose"`
		Patrol *PatrolRef                   `json:"patrol"`
	}
	if json.Unmarshal(raw, &t) != nil {
		return nil
	}
	s := &RobotState{TS: t.TS, NavState: t.Nav.State, Mode: t.Mode, People: t.People, Estop: t.Estop, Patrol: t.Patrol}
	if t.Battery != nil {
		s.BatteryPct = t.Battery.Pct
	}
	if t.Pose != nil {
		yaw := t.Pose.Yaw
		s.Pose = &Position{X: t.Pose.X, Y: t.Pose.Y, Yaw: &yaw, Frame: "map"}
	}
	return s
}

func (a *API) robots(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.ListRobots(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]Robot, 0, len(list))
	for _, rb := range list {
		out = append(out, Robot{ID: rb.ID, Name: rb.Name, Online: a.online != nil && a.online(rb.ID), LastSeen: rb.LastSeen,
			FloorPlanID: rb.FloorPlanID, State: robotState(rb.LastTelemetry)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// timeWindow reads from/to (RFC 3339), defaulting to the last 7 days; at most 93 days.
func timeWindow(r *http.Request) (time.Time, time.Time, error) {
	q := r.URL.Query()
	to := time.Now()
	from := to.Add(-7 * 24 * time.Hour)
	if t, err := timeParam(q, "to"); err != nil {
		return from, to, err
	} else if t != nil {
		to = *t
	}
	if t, err := timeParam(q, "from"); err != nil {
		return from, to, err
	} else if t != nil {
		from = *t
	} else {
		from = to.Add(-7 * 24 * time.Hour)
	}
	if !from.Before(to) || to.Sub(from) > 93*24*time.Hour {
		return from, to, fmt.Errorf("from must be before to, at most 93 days apart")
	}
	return from, to, nil
}

func (a *API) patrols(w http.ResponseWriter, r *http.Request) {
	from, to, err := timeWindow(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	list, err := a.store.ListPatrols(r.Context(), r.URL.Query().Get("robot_id"), from, to)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": list})
}

func (a *API) patrol(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no such item")
		return
	}
	p, err := a.store.GetPatrol(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// RoutePoint is one row of a recorded route: position in the map frame, heading and dwell time.
type RoutePoint struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	YawDeg float64 `json:"yaw_deg"`
	DwellS float64 `json:"dwell_s"`
}

// Route is a recorded route; Points only on the single-route endpoint.
type Route struct {
	store.Route
	Points []RoutePoint `json:"points,omitempty"`
}

func (a *API) routes(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.ListRoutes(r.Context(), r.URL.Query().Get("robot_id"), r.URL.Query().Get("archived") == "true")
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]Route, 0, len(list))
	for _, rt := range list {
		rt.Points = nil
		out = append(out, Route{Route: rt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (a *API) route(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no such item")
		return
	}
	rt, text, err := a.store.GetRoute(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	rt.Points = nil
	out := Route{Route: rt, Points: []RoutePoint{}}
	for _, p := range store.ParseRoute(text) {
		out.Points = append(out.Points, RoutePoint{X: p[0], Y: p[1], YawDeg: p[2], DwellS: p[3]})
	}
	writeJSON(w, http.StatusOK, out)
}

// FloorPlan is a floor plan with its calibration against the robot map frame.
type FloorPlan struct {
	store.FloorPlan
	FileURL string `json:"file_url"`
}

func (a *API) floorPlans(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.ListFloorPlans(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	base := baseURL(r)
	out := make([]FloorPlan, 0, len(list))
	for _, p := range list {
		out = append(out, FloorPlan{FloorPlan: p, FileURL: fmt.Sprintf("%s%s/floor-plans/%d/file", base, Prefix, p.ID)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (a *API) floorPlanFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no such item")
		return
	}
	p, err := a.store.GetFloorPlan(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", p.ContentType)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	http.ServeFile(w, r, p.FilePath)
}
