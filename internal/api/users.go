package api

import (
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/DarmawanRobotics/netra-fleet-be/internal/auth"
	"github.com/DarmawanRobotics/netra-fleet-be/internal/store"
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

// loginLimiter blocks an IP after repeated failed logins.
type loginLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{failures: map[string][]time.Time{}} }

const (
	maxFailures   = 8
	failureWindow = 10 * time.Minute
)

func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.failures[ip][:0]
	for _, t := range l.failures[ip] {
		if time.Since(t) < failureWindow {
			recent = append(recent, t)
		}
	}
	l.failures[ip] = recent
	return len(recent) >= maxFailures
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	l.failures[ip] = append(l.failures[ip], time.Now())
	l.mu.Unlock()
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	delete(l.failures, ip)
	l.mu.Unlock()
}

func validPassword(p string) bool { return len(p) >= 8 }

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.limiter.blocked(ip) {
		writeError(w, http.StatusTooManyRequests, "too many failed logins, try again in 10 minutes")
		return
	}
	var body struct{ Username, Password string }
	if decode(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "username and password required")
		return
	}
	id, hash, role, disabled, err := s.store.UserLogin(r.Context(), body.Username)
	if err != nil || disabled || !auth.CheckPassword(body.Password, hash) {
		s.limiter.fail(ip)
		s.auditAs(body.Username, "login_failed", ip)
		time.Sleep(400 * time.Millisecond)
		writeError(w, http.StatusUnauthorized, "wrong username or password")
		return
	}
	s.limiter.reset(ip)
	token := auth.NewToken()
	expires := time.Now().Add(s.cfg.SessionTTL)
	if err := s.store.CreateSessionFor(r.Context(), auth.HashToken(token), id, expires, ip); err != nil {
		fail(w, err)
		return
	}
	s.auditAs(body.Username, "login", ip)
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expires_at": expires, "username": body.Username,
		"role": role, "permissions": auth.PermissionsOf(role)})
}

func (s *Server) auditAs(username, action, ip string) {
	go func() {
		_ = s.store.InsertAudit(contextBackground(), store.AuditEntry{Username: username, Action: action, IP: ip})
	}()
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	_ = s.store.DeleteSession(r.Context(), auth.HashToken(bearer(r)))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	writeJSON(w, http.StatusOK, map[string]any{"username": u.Username, "display_name": u.DisplayName, "role": u.Role,
		"permissions": auth.PermissionsOf(u.Role), "timezone": s.cfg.TimeZone.String()})
}

func (s *Server) changeOwnPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	u := userOf(r)
	_, hash, _, _, err := s.store.UserLogin(r.Context(), u.Username)
	if decode(r, &body) != nil || err != nil || !auth.CheckPassword(body.Current, hash) {
		writeError(w, http.StatusBadRequest, "current password is wrong")
		return
	}
	if !validPassword(body.New) {
		writeError(w, http.StatusBadRequest, "new password needs at least 8 characters")
		return
	}
	if err := s.store.SetPassword(r.Context(), u.ID, auth.HashPassword(body.New), auth.HashToken(bearer(r))); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		Password    string `json:"password"`
	}
	if decode(r, &body) != nil || !usernamePattern.MatchString(body.Username) || !auth.ValidRole(body.Role) ||
		!validPassword(body.Password) {
		writeError(w, http.StatusBadRequest, "username (3-32 of a-z 0-9 . _ -), role and a password of 8+ characters required")
		return
	}
	id, err := s.store.CreateUser(r.Context(), store.User{Username: body.Username, DisplayName: body.DisplayName,
		Role: body.Role}, auth.HashPassword(body.Password))
	if err != nil {
		writeError(w, http.StatusConflict, "username already exists")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// keepOneAdmin refuses changes that would leave no enabled admin.
func (s *Server) keepOneAdmin(r *http.Request, id int64, newRole string, disabled bool) bool {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		return false
	}
	for _, u := range users {
		if u.ID == id && u.Role == "admin" && !u.Disabled && (newRole != "admin" || disabled) {
			n, err := s.store.CountAdmins(r.Context())
			return err == nil && n > 1
		}
	}
	return true
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	var body struct {
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		Disabled    bool   `json:"disabled"`
	}
	if decode(r, &body) != nil || !auth.ValidRole(body.Role) {
		writeError(w, http.StatusBadRequest, "role must be viewer, operator, supervisor or admin")
		return
	}
	if !s.keepOneAdmin(r, id, body.Role, body.Disabled) {
		writeError(w, http.StatusConflict, "at least one enabled admin must remain")
		return
	}
	if err := s.store.UpdateUser(r.Context(), store.User{ID: id, DisplayName: body.DisplayName, Role: body.Role,
		Disabled: body.Disabled}); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	if id == userOf(r).ID || !s.keepOneAdmin(r, id, "", true) {
		writeError(w, http.StatusConflict, "cannot delete yourself or the last admin")
		return
	}
	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	var body struct {
		Password string `json:"password"`
	}
	if decode(r, &body) != nil || !validPassword(body.Password) {
		writeError(w, http.StatusBadRequest, "password needs at least 8 characters")
		return
	}
	if err := s.store.SetPassword(r.Context(), id, auth.HashPassword(body.Password), ""); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	from, to := timeRange(r)
	q := r.URL.Query()
	entries, err := s.store.ListAudit(r.Context(), q.Get("username"), q.Get("action"), from, to, 1000)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) robotTrack(w http.ResponseWriter, r *http.Request) {
	from, to := timeRange(r)
	points, err := s.store.Track(r.Context(), r.PathValue("id"), from, to, 3000)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, points)
}
