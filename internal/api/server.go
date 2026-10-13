// Package api serves the NETRA HTTP and WebSocket API.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DarmawanRobotics/netra-fleet-be/internal/auth"
	"github.com/DarmawanRobotics/netra-fleet-be/internal/bus"
	"github.com/DarmawanRobotics/netra-fleet-be/internal/config"
	"github.com/DarmawanRobotics/netra-fleet-be/internal/detect"
	"github.com/DarmawanRobotics/netra-fleet-be/internal/hub"
	"github.com/DarmawanRobotics/netra-fleet-be/internal/metrics"
	"github.com/DarmawanRobotics/netra-fleet-be/internal/notify"
	"github.com/DarmawanRobotics/netra-fleet-be/internal/store"
	"github.com/gorilla/websocket"
)

type ctxKey int

const (
	userKey ctxKey = iota
	robotKey
)

// Server holds the dependencies of every handler.
type Server struct {
	cfg      config.Config
	store    *store.Store
	hub      *hub.Hub
	telegram *notify.Telegram
	detector detect.Detector
	bus      *bus.Publisher
	limiter  *loginLimiter
	upgrader websocket.Upgrader
	wake     chan struct{}

	mu      sync.Mutex
	pending map[string]pendingCommand
	lastLog map[string]time.Time     // last telemetry history sample per robot
	skew    map[string]time.Duration // robot clock offset (server - robot), see clock.go
}

type pendingCommand struct {
	patrolID int64
	name     string
}

// New wires a server.
func New(cfg config.Config, st *store.Store, h *hub.Hub, tg *notify.Telegram, det detect.Detector, b *bus.Publisher) *Server {
	return &Server{
		cfg: cfg, store: st, hub: h, telegram: tg, detector: det, bus: b, limiter: newLoginLimiter(),
		lastLog:  map[string]time.Time{},
		skew:     map[string]time.Duration{},
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		wake:     make(chan struct{}, 1),
		pending:  map[string]pendingCommand{},
	}
}

// Handler returns the routed HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /api/v1/auth/login", s.login)

	route := func(p auth.Permission, pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireUser(p, h)) }
	view, operate, configure := auth.PermView, auth.PermOperate, auth.PermConfigure
	route(view, "GET /api/v1/me", s.me)
	route(view, "GET /api/v1/info", s.info)
	route(view, "POST /api/v1/auth/logout", s.logout)
	route(view, "POST /api/v1/me/password", s.changeOwnPassword)
	route(view, "GET /api/v1/ws", s.dashboardWS)
	route(view, "GET /api/v1/robots", s.listRobots)
	route(view, "GET /api/v1/robots/{id}/track", s.robotTrack)
	route(configure, "POST /api/v1/robots", s.createRobot)
	route(configure, "PUT /api/v1/robots/{id}", s.updateRobot)
	route(configure, "DELETE /api/v1/robots/{id}", s.deleteRobot)
	route(configure, "POST /api/v1/robots/{id}/token", s.rotateRobotToken)
	route(operate, "POST /api/v1/robots/{id}/command", s.robotCommand)
	route(view, "GET /api/v1/routes", s.listRoutes)
	route(view, "GET /api/v1/routes/{id}", s.getRoute)
	route(configure, "PUT /api/v1/routes/{id}", s.updateRoute)
	route(view, "GET /api/v1/routes/{id}/file", s.routeFile)
	route(view, "GET /api/v1/schedules", s.listSchedules)
	route(operate, "POST /api/v1/schedules", s.saveSchedule)
	route(operate, "PUT /api/v1/schedules/{id}", s.saveSchedule)
	route(operate, "DELETE /api/v1/schedules/{id}", s.deleteSchedule)
	route(view, "GET /api/v1/patrols", s.listPatrols)
	route(operate, "POST /api/v1/patrols", s.createPatrol)
	route(view, "GET /api/v1/patrols/{id}", s.getPatrol)
	route(operate, "POST /api/v1/patrols/{id}/stop", s.stopPatrol)
	route(view, "GET /api/v1/events", s.listEvents)
	route(view, "GET /api/v1/anomalies", s.listAnomalies)
	route(view, "GET /api/v1/anomalies/{id}", s.getAnomaly)
	route(view, "GET /api/v1/anomalies/{id}/image", s.anomalyImage)
	route(operate, "POST /api/v1/anomalies/{id}/validate", s.validateAnomaly)
	route(view, "GET /api/v1/rules", s.listRules)
	route(configure, "PUT /api/v1/rules/{category}", s.saveRule)
	route(configure, "DELETE /api/v1/rules/{category}", s.deleteRule)
	route(view, "GET /api/v1/crowd/heatmap", s.heatmap)
	route(view, "GET /api/v1/crowd/timeline", s.timeline)
	route(view, "GET /api/v1/floorplans", s.listFloorPlans)
	route(configure, "POST /api/v1/floorplans", s.uploadFloorPlan)
	route(configure, "PUT /api/v1/floorplans/{id}", s.calibrateFloorPlan)
	route(configure, "DELETE /api/v1/floorplans/{id}", s.deleteFloorPlan)
	route(view, "GET /api/v1/floorplans/{id}/file", s.floorPlanFile)
	route(view, "GET /api/v1/reports/patrols/{id}/pdf", s.patrolReportPDF)
	route(view, "GET /api/v1/reports/summary", s.summaryReport)
	route(view, "GET /api/v1/reports/anomalies", s.anomaliesExport)
	route(view, "GET /api/v1/reports/patrols", s.patrolsExport)
	route(auth.PermUsers, "GET /api/v1/users", s.listUsers)
	route(auth.PermUsers, "POST /api/v1/users", s.createUser)
	route(auth.PermUsers, "PUT /api/v1/users/{id}", s.updateUser)
	route(auth.PermUsers, "DELETE /api/v1/users/{id}", s.deleteUser)
	route(auth.PermUsers, "POST /api/v1/users/{id}/password", s.resetPassword)
	route(auth.PermAudit, "GET /api/v1/audit", s.listAudit)
	mux.Handle("GET /metrics", metrics.Default)

	robot := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireRobot(h)) }
	robot("GET /api/v1/robot/ws", s.robotWS)
	robot("POST /api/v1/robot/anomalies", s.robotAnomaly)
	robot("POST /api/v1/robot/routes", s.robotRoute)
	robot("GET /api/v1/robot/routes/{id}/file", s.routeFile)

	return metrics.Middleware(mux, s.cors(mux))
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.cfg.AllowedOrigin)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearer returns the token from the Authorization header or the ?token= query (WebSocket, images).
func bearer(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

func (s *Server) requireUser(perm auth.Permission, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := s.store.SessionUser(r.Context(), auth.HashToken(bearer(r)))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		if !auth.Can(user.Role, perm) {
			writeError(w, http.StatusForbidden, "your role cannot do this")
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), userKey, user))
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next(rec, r)
		if rec.status < 400 {
			s.audit(r, r.Method+" "+r.URL.Path, "", nil)
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) { s.status = code; s.ResponseWriter.WriteHeader(code) }

// audit records a user action without delaying the response.
func (s *Server) audit(r *http.Request, action, target string, detail any) {
	entry := store.AuditEntry{Username: userOf(r).Username, Action: action, Target: target, IP: clientIP(r)}
	if entry.Username == "" {
		entry.Username = "-"
	}
	if detail != nil {
		entry.Detail, _ = json.Marshal(detail)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.store.InsertAudit(ctx, entry); err != nil {
			slog.Warn("audit", "err", err)
		}
	}()
}

// clientIP prefers the proxy header set by Caddy.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.TrimSpace(strings.Split(ip, ",")[0])
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

func (s *Server) requireRobot(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		robot, err := s.store.RobotByToken(r.Context(), auth.HashToken(bearer(r)))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unknown robot token")
			return
		}
		if id := r.Header.Get("X-Robot-Id"); id != "" && id != robot.ID {
			writeError(w, http.StatusForbidden, "token does not belong to "+id)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), robotKey, robot.ID)))
	})
}

func userOf(r *http.Request) store.User { v, _ := r.Context().Value(userKey).(store.User); return v }
func robotOf(r *http.Request) string    { v, _ := r.Context().Value(robotKey).(string); return v }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail maps store errors to HTTP answers.
func fail(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	slog.Error("request failed", "err", err)
	writeError(w, http.StatusInternalServerError, err.Error())
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

// timeRange reads ?from=&to= (RFC 3339), defaulting to the last 7 days.
func timeRange(r *http.Request) (time.Time, time.Time) {
	to, err := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	if err != nil {
		to = time.Now()
	}
	from, err := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	if err != nil {
		from = to.Add(-7 * 24 * time.Hour)
	}
	return from, to
}

func contextBackground() context.Context { return context.Background() }

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	return dec.Decode(v)
}
