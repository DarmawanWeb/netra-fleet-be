package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/hub"
	"github.com/DarmawanWeb/netra-fleet-be/internal/metrics"
	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
)

// robotMessage is the union of every robot WebSocket message.
type robotMessage struct {
	Type     string          `json:"type"`
	ID       json.RawMessage `json:"id"`
	OK       *bool           `json:"ok"`
	Msg      string          `json:"msg"`
	Event    string          `json:"event"`
	Lap      int             `json:"lap"`
	Reason   string          `json:"reason"`
	PatrolID *float64        `json:"patrol_id"`
	TS       float64         `json:"ts"`
	TSStart  float64         `json:"ts_start"`
	TSEnd    float64         `json:"ts_end"`
	CellSize float64         `json:"cell_size"`
	Cells    json.RawMessage `json:"cells"`
	Peak     int             `json:"peak_people"`
}

func patrolRef(v *float64) *int64 {
	if v == nil || *v <= 0 {
		return nil
	}
	id := int64(*v)
	return &id
}

func unixTime(v float64) time.Time {
	sec, frac := math.Modf(v)
	return time.Unix(int64(sec), int64(frac*1e9))
}

func (s *Server) robotWS(w http.ResponseWriter, r *http.Request) {
	robotID := robotOf(r)
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn := hub.NewConn(ws)
	s.hub.AddRobot(robotID, conn)
	metrics.Default.Set("netra_robot_online", "Robot connected (1) or not (0).", 1, "robot", robotID)
	s.hub.Broadcast(map[string]any{"type": "robot_online", "robot_id": robotID, "online": true})
	slog.Info("robot connected", "robot", robotID)
	defer func() {
		s.hub.RemoveRobot(robotID, conn)
		metrics.Default.Set("netra_robot_online", "Robot connected (1) or not (0).", 0, "robot", robotID)
		s.hub.Broadcast(map[string]any{"type": "robot_online", "robot_id": robotID, "online": false})
		slog.Info("robot disconnected", "robot", robotID)
	}()
	ws.SetReadLimit(1 << 20)
	for {
		_ = ws.SetReadDeadline(time.Now().Add(90 * time.Second))
		_, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		s.handleRobotMessage(context.Background(), robotID, data)
	}
}

func (s *Server) handleRobotMessage(ctx context.Context, robotID string, data []byte) {
	var m robotMessage
	if json.Unmarshal(data, &m) != nil {
		return
	}
	patrolID := patrolRef(m.PatrolID)
	switch m.Type {
	case "telemetry":
		s.noteClock(robotID, m.TS, time.Now())
		if err := s.store.SaveTelemetry(ctx, robotID, data); err != nil {
			slog.Error("save telemetry", "err", err)
		}
		s.logTelemetry(ctx, robotID, data, patrolID)
	case "patrol_event":
		if patrolID != nil {
			switch m.Event {
			case "started":
				_ = s.store.PatrolStatus(ctx, *patrolID, "running", "")
			case "lap":
				_ = s.store.PatrolLap(ctx, *patrolID, m.Lap)
			case "finished":
				_ = s.store.PatrolStatus(ctx, *patrolID, "finished", m.Reason)
			}
		}
		_ = s.store.InsertEvent(ctx, robotID, patrolID, m.Type, data)
	case "stop_event", "alert":
		_ = s.store.InsertEvent(ctx, robotID, patrolID, m.Type, data)
	case "crowd":
		start, end := s.robotTime(robotID, m.TSStart), s.robotTime(robotID, m.TSEnd)
		if err := s.store.InsertCrowd(ctx, robotID, patrolID, start, end, m.CellSize, m.Cells, m.Peak); err != nil {
			slog.Error("save crowd", "err", err)
		}
	case "ack":
		s.resolveCommand(ctx, string(m.ID), m.OK != nil && *m.OK, m.Msg)
	case "hello":
	default:
		return
	}
	s.hub.Broadcast(map[string]any{"type": m.Type, "robot_id": robotID, "data": json.RawMessage(data)})
	s.bus.Publish("netra.robot."+robotID+"."+m.Type, json.RawMessage(data))
	metrics.Default.Add("netra_robot_messages_total", "Robot WebSocket messages.", 1, "type", m.Type)
}

// logTelemetry keeps one history sample per robot every 5 s (track playback, battery charts).
func (s *Server) logTelemetry(ctx context.Context, robotID string, data []byte, patrolID *int64) {
	now := time.Now()
	s.mu.Lock()
	due := now.Sub(s.lastLog[robotID]) >= 5*time.Second
	if due {
		s.lastLog[robotID] = now
	}
	s.mu.Unlock()
	if !due {
		return
	}
	var t struct {
		Pose *struct{ X, Y, Yaw float64 } `json:"pose"`
		Mode string                       `json:"mode"`
		Nav  struct {
			State string `json:"state"`
		} `json:"nav"`
		Battery *struct {
			Pct *float64 `json:"pct"`
		} `json:"battery"`
		People int         `json:"people"`
		Health *store.Load `json:"health"`
	}
	if json.Unmarshal(data, &t) != nil {
		return
	}
	p := store.TrackPoint{TS: now, NavState: t.Nav.State, People: t.People}
	if t.Health != nil {
		p.Load = *t.Health
	}
	if t.Pose != nil {
		p.X, p.Y, p.Yaw = &t.Pose.X, &t.Pose.Y, &t.Pose.Yaw
	}
	if t.Battery != nil {
		p.Battery = t.Battery.Pct
		if t.Battery.Pct != nil {
			metrics.Default.Set("netra_robot_battery_percent", "Robot battery.", *t.Battery.Pct, "robot", robotID)
		}
	}
	if err := s.store.InsertTelemetry(ctx, robotID, p, t.Mode, patrolID); err != nil {
		slog.Warn("telemetry history", "err", err)
	}
}

// sendCommand sends a command to a robot and remembers it until the ack.
func (s *Server) sendCommand(robotID, name string, args map[string]any, patrolID int64) (string, error) {
	id := fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
	if err := s.hub.SendRobot(robotID, map[string]any{"type": "command", "id": id, "name": name, "args": args}); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.pending[id] = pendingCommand{patrolID: patrolID, name: name}
	s.mu.Unlock()
	return id, nil
}

func (s *Server) resolveCommand(ctx context.Context, rawID string, ok bool, msg string) {
	var id string
	if json.Unmarshal([]byte(rawID), &id) != nil {
		id = rawID
	}
	s.mu.Lock()
	cmd, found := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if found && cmd.done != nil {
		cmd.done <- ackResult{OK: ok, Msg: msg}
	}
	if !found || cmd.patrolID == 0 {
		return
	}
	switch {
	case cmd.name == "start_patrol" && !ok:
		_ = s.store.PatrolStatus(ctx, cmd.patrolID, "failed", msg)
	case cmd.name == "stop_patrol" && ok:
		_ = s.store.PatrolStatus(ctx, cmd.patrolID, "stopped", "stopped by operator")
	}
}

// StartPatrol creates a patrol and asks the robot to run it.
func (s *Server) StartPatrol(ctx context.Context, robotID string, routeID int64, scheduleID *int64) (int64, error) {
	route, _, err := s.store.GetRoute(ctx, routeID)
	if err != nil {
		return 0, err
	}
	if route.RobotID != robotID {
		return 0, fmt.Errorf("route %d belongs to %s", routeID, route.RobotID)
	}
	id, err := s.store.CreatePatrol(ctx, robotID, routeID, scheduleID)
	if err != nil {
		return 0, err
	}
	if _, err := s.sendCommand(robotID, "start_patrol", map[string]any{"route_id": routeID, "patrol_id": id}, id); err != nil {
		_ = s.store.PatrolStatus(ctx, id, "failed", err.Error())
		return id, err
	}
	s.hub.Broadcast(map[string]any{"type": "patrol_requested", "robot_id": robotID, "patrol_id": id})
	return id, nil
}

type uploadMeta struct {
	UID         string   `json:"uid"`
	TS          float64  `json:"ts"`
	Category    string   `json:"category"`
	Confidence  float64  `json:"confidence"`
	Description string   `json:"description"`
	PatrolID    *float64 `json:"patrol_id"`
	RouteID     *float64 `json:"route_id"`
	StopIndex   *int64   `json:"stop_index"`
	Pose        *struct {
		X, Y, Yaw float64
	} `json:"pose"`
	Name       string  `json:"name"`
	RecordedAt float64 `json:"recorded_at"`
	LengthM    float64 `json:"length_m"`
	Stops      int     `json:"stops"`
	Closed     bool    `json:"closed"`
}

func readUpload(r *http.Request, file string) (uploadMeta, []byte, error) {
	var meta uploadMeta
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		return meta, nil, err
	}
	if err := json.Unmarshal([]byte(r.FormValue("meta")), &meta); err != nil || meta.UID == "" {
		return meta, nil, fmt.Errorf("meta must be JSON with a uid")
	}
	f, _, err := r.FormFile(file)
	if err != nil {
		return meta, nil, fmt.Errorf("missing %s", file)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 16<<20))
	return meta, data, err
}

func (s *Server) robotAnomaly(w http.ResponseWriter, r *http.Request) {
	robotID := robotOf(r)
	meta, img, err := readUpload(r, "image.jpg")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "anomalies", time.Now().Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(w, err)
		return
	}
	path := filepath.Join(dir, filepath.Base(meta.UID)+".jpg")
	if err := os.WriteFile(path, img, 0o644); err != nil {
		fail(w, err)
		return
	}
	a := store.Anomaly{UID: meta.UID, RobotID: robotID, PatrolID: patrolRef(meta.PatrolID),
		RouteID: patrolRef(meta.RouteID), DetectedAt: s.robotTime(robotID, meta.TS), EarlyCategory: meta.Category,
		EarlyConfidence: meta.Confidence, EarlyNote: meta.Description, StopIndex: meta.StopIndex, ImagePath: path}
	if a.EarlyCategory == "" {
		a.EarlyCategory = "unknown"
	}
	if meta.Pose != nil {
		a.X, a.Y, a.Yaw = &meta.Pose.X, &meta.Pose.Y, &meta.Pose.Yaw
	}
	id, created, err := s.store.InsertAnomaly(r.Context(), a)
	if err != nil {
		fail(w, err)
		return
	}
	if created {
		s.hub.Broadcast(map[string]any{"type": "anomaly", "robot_id": robotID, "id": id, "status": "queued"})
		metrics.Default.Add("netra_anomalies_received_total", "Anomalies uploaded by robots.", 1, "category", a.EarlyCategory)
		s.bus.Publish("netra.anomaly.queued", map[string]any{"id": id, "robot_id": robotID, "category": a.EarlyCategory})
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "created": created})
}

func (s *Server) robotRoute(w http.ResponseWriter, r *http.Request) {
	robotID := robotOf(r)
	meta, csv, err := readUpload(r, "route.csv")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := meta.Name
	if name == "" {
		name = "route " + time.Now().Format("2006-01-02 15:04")
	}
	id, err := s.store.InsertRoute(r.Context(), store.Route{UID: meta.UID, RobotID: robotID, Name: name,
		RecordedAt: s.robotTime(robotID, meta.RecordedAt), LengthM: meta.LengthM, Stops: meta.Stops, Closed: meta.Closed},
		string(csv))
	if err != nil {
		fail(w, err)
		return
	}
	s.hub.Broadcast(map[string]any{"type": "route", "robot_id": robotID, "id": id})
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) routeFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad id")
		return
	}
	route, csv, err := s.store.GetRoute(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	if rid := robotOf(r); rid != "" && rid != route.RobotID {
		writeError(w, http.StatusForbidden, "route of another robot")
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=route-%d.csv", id))
	_, _ = io.WriteString(w, csv)
}
