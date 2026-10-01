package api

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DarmawanRobotics/netra-fleet-be/internal/auth"
	"github.com/DarmawanRobotics/netra-fleet-be/internal/hub"
	"github.com/DarmawanRobotics/netra-fleet-be/internal/store"
)

var (
	robotIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,31}$`)
	timePattern    = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
)

func (s *Server) dashboardWS(w http.ResponseWriter, r *http.Request) {
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn := hub.NewConn(ws)
	s.hub.AddDashboard(conn)
	defer s.hub.RemoveDashboard(conn)
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}
	}
}

// ---- robots

type robotView struct {
	store.Robot
	Online bool `json:"online"`
}

func (s *Server) listRobots(w http.ResponseWriter, r *http.Request) {
	robots, err := s.store.ListRobots(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]robotView, len(robots))
	for i, rb := range robots {
		out[i] = robotView{rb, s.hub.Online(rb.ID)}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createRobot(w http.ResponseWriter, r *http.Request) {
	var body struct{ ID, Name string }
	if decode(r, &body) != nil || !robotIDPattern.MatchString(body.ID) {
		writeError(w, http.StatusBadRequest, "id must be 2-32 chars of a-z, 0-9, _ or -")
		return
	}
	if body.Name == "" {
		body.Name = body.ID
	}
	token := auth.NewToken()
	if err := s.store.CreateRobot(r.Context(), body.ID, body.Name, auth.HashToken(token)); err != nil {
		writeError(w, http.StatusConflict, "robot id already exists")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": body.ID, "token": token})
}

func (s *Server) updateRobot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		FloorPlanID *int64 `json:"floor_plan_id"`
	}
	if decode(r, &body) != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if err := s.store.UpdateRobot(r.Context(), r.PathValue("id"), body.Name, body.FloorPlanID); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteRobot(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRobot(r.Context(), r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rotateRobotToken(w http.ResponseWriter, r *http.Request) {
	token := auth.NewToken()
	if err := s.store.SetRobotToken(r.Context(), r.PathValue("id"), auth.HashToken(token)); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": r.PathValue("id"), "token": token})
}

func (s *Server) robotCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	allowed := map[string]bool{"pause": true, "resume": true, "localize": true, "stop_patrol": true}
	if decode(r, &body) != nil || !allowed[body.Name] {
		writeError(w, http.StatusBadRequest, "name must be pause, resume, localize or stop_patrol")
		return
	}
	id, err := s.sendCommand(r.PathValue("id"), body.Name, body.Args, 0)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"command_id": id})
}

// ---- routes

// parseRoute reads x,y,yaw_deg,dwell_sec rows.
func parseRoute(text string) [][4]float64 {
	rd := csv.NewReader(strings.NewReader(text))
	rd.FieldsPerRecord = -1
	rd.Comment = '#'
	out := [][4]float64{}
	for {
		row, err := rd.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || len(row) < 2 {
			continue
		}
		var p [4]float64
		valid := true
		for i := 0; i < 4 && i < len(row); i++ {
			v, err := strconv.ParseFloat(strings.TrimSpace(row[i]), 64)
			if err != nil {
				valid = false
				break
			}
			p[i] = v
		}
		if valid {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) listRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := s.store.ListRoutes(r.Context(), r.URL.Query().Get("robot_id"), r.URL.Query().Get("archived") == "true")
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, routes)
}

func (s *Server) getRoute(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	route, text, err := s.store.GetRoute(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	route.Points = parseRoute(text)
	writeJSON(w, http.StatusOK, route)
}

func (s *Server) updateRoute(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	var body struct {
		Name     string `json:"name"`
		Archived bool   `json:"archived"`
	}
	if decode(r, &body) != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if err := s.store.UpdateRoute(r.Context(), id, body.Name, body.Archived); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- schedules

func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSchedules(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) saveSchedule(w http.ResponseWriter, r *http.Request) {
	var c store.Schedule
	if decode(r, &c) != nil || c.RobotID == "" || c.RouteID == 0 || !timePattern.MatchString(c.TimeOfDay) {
		writeError(w, http.StatusBadRequest, "robot_id, route_id and time_of_day (HH:MM) required")
		return
	}
	if c.OnceOn != nil {
		if _, err := time.Parse("2006-01-02", *c.OnceOn); err != nil {
			writeError(w, http.StatusBadRequest, "once_on must be YYYY-MM-DD")
			return
		}
		c.Days = []int64{}
	} else if len(c.Days) == 0 {
		writeError(w, http.StatusBadRequest, "days (0-6) or once_on required")
		return
	}
	for _, d := range c.Days {
		if d < 0 || d > 6 {
			writeError(w, http.StatusBadRequest, "days are 0 (Sunday) to 6 (Saturday)")
			return
		}
	}
	if r.Method == http.MethodPut {
		c.ID, _ = pathID(r)
	} else {
		c.ID = 0
	}
	if c.Name == "" {
		c.Name = "Patrol " + c.TimeOfDay
	}
	id, err := s.store.SaveSchedule(r.Context(), c)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	if err := s.store.DeleteSchedule(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- patrols and events

func (s *Server) listPatrols(w http.ResponseWriter, r *http.Request) {
	from, to := timeRange(r)
	list, err := s.store.ListPatrols(r.Context(), r.URL.Query().Get("robot_id"), from, to)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createPatrol(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RobotID string `json:"robot_id"`
		RouteID int64  `json:"route_id"`
	}
	if decode(r, &body) != nil || body.RobotID == "" || body.RouteID == 0 {
		writeError(w, http.StatusBadRequest, "robot_id and route_id required")
		return
	}
	id, err := s.StartPatrol(r.Context(), body.RobotID, body.RouteID, nil)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int64{"id": id})
}

func (s *Server) getPatrol(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	patrol, err := s.store.GetPatrol(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	recap, err := s.store.PatrolRecap(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"patrol": patrol, "recap": recap})
}

func (s *Server) stopPatrol(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	patrol, err := s.store.GetPatrol(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	if _, err := s.sendCommand(patrol.RobotID, "stop_patrol", nil, id); err != nil {
		_ = s.store.PatrolStatus(r.Context(), id, "stopped", "robot offline")
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	patrolID, _ := strconv.ParseInt(r.URL.Query().Get("patrol_id"), 10, 64)
	events, err := s.store.ListEvents(r.Context(), r.URL.Query().Get("robot_id"), patrolID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// ---- anomalies and rules

func (s *Server) listAnomalies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to := timeRange(r)
	patrolID, _ := strconv.ParseInt(q.Get("patrol_id"), 10, 64)
	list, err := s.store.ListAnomalies(r.Context(), q.Get("status"), q.Get("category"), q.Get("robot_id"),
		patrolID, from, to)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getAnomaly(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	a, err := s.store.GetAnomaly(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) anomalyImage(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	a, err := s.store.GetAnomaly(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, a.ImagePath)
}

func (s *Server) validateAnomaly(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	var body struct {
		Decision string `json:"decision"`
		Category string `json:"category"`
		Note     string `json:"note"`
	}
	if decode(r, &body) != nil || (body.Decision != "confirm" && body.Decision != "reject") {
		writeError(w, http.StatusBadRequest, "decision must be confirm or reject")
		return
	}
	status := map[string]string{"confirm": "confirmed", "reject": "rejected"}[body.Decision]
	if err := s.store.Validate(r.Context(), id, status, body.Category, body.Note, userOf(r).Username); err != nil {
		fail(w, err)
		return
	}
	if status == "confirmed" {
		go s.notifyConfirmed(id)
	}
	s.hub.Broadcast(map[string]any{"type": "anomaly", "id": id, "status": status})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.store.ListRules(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

func (s *Server) saveRule(w http.ResponseWriter, r *http.Request) {
	var rule store.Rule
	if decode(r, &rule) != nil || rule.Label == "" {
		writeError(w, http.StatusBadRequest, "label required")
		return
	}
	rule.Category = r.PathValue("category")
	if err := s.store.SaveRule(r.Context(), rule); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRule(r.Context(), r.PathValue("category")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- crowd

func (s *Server) heatmap(w http.ResponseWriter, r *http.Request) {
	from, to := timeRange(r)
	cells, err := s.store.Heatmap(r.Context(), r.URL.Query().Get("robot_id"), from, to)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cells)
}

func (s *Server) timeline(w http.ResponseWriter, r *http.Request) {
	from, to := timeRange(r)
	points, err := s.store.CrowdTimeline(r.Context(), r.URL.Query().Get("robot_id"), r.URL.Query().Get("bucket"),
		s.cfg.TimeZone.String(), from, to)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, points)
}

// ---- floor plans

var planTypes = map[string]string{
	".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".pdf": "application/pdf",
}

func (s *Server) listFloorPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.store.ListFloorPlans(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plans)
}

func (s *Server) uploadFloorPlan(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "multipart form with a file required")
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file required")
		return
	}
	defer f.Close()
	ext := strings.ToLower(filepath.Ext(header.Filename))
	ctype, ok := planTypes[ext]
	if !ok {
		writeError(w, http.StatusBadRequest, "use SVG, PNG, JPG or PDF")
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "floorplans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(w, err)
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("%d%s", time.Now().UnixNano(), ext))
	out, err := os.Create(path)
	if err != nil {
		fail(w, err)
		return
	}
	defer out.Close()
	if _, err := io.Copy(out, io.LimitReader(f, 32<<20)); err != nil {
		fail(w, err)
		return
	}
	name := r.FormValue("name")
	if name == "" {
		name = strings.TrimSuffix(header.Filename, ext)
	}
	width, _ := strconv.ParseFloat(r.FormValue("width_px"), 64)
	height, _ := strconv.ParseFloat(r.FormValue("height_px"), 64)
	id, err := s.store.InsertFloorPlan(r.Context(), store.FloorPlan{Name: name, FilePath: path, ContentType: ctype,
		WidthPx: width, HeightPx: height})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) calibrateFloorPlan(w http.ResponseWriter, r *http.Request) {
	var p store.FloorPlan
	if decode(r, &p) != nil || p.PxPerM <= 0 || p.Name == "" {
		writeError(w, http.StatusBadRequest, "name and px_per_m > 0 required")
		return
	}
	p.ID, _ = pathID(r)
	if err := s.store.CalibrateFloorPlan(r.Context(), p); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteFloorPlan(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	path, err := s.store.DeleteFloorPlan(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	_ = os.Remove(path)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) floorPlanFile(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	p, err := s.store.GetFloorPlan(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", p.ContentType)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	http.ServeFile(w, r, p.FilePath)
}
