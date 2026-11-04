package licensemgr

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/auth"
	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
)

// CSRFHeader must be on every request that changes something. A form cannot set it, and a script
// of another site needs a CORS preflight for it, which this server never grants.
const CSRFHeader = "X-Netra-Manager"

const (
	sessionCookie = "netra_lm_session"
	sessionTTL    = 12 * time.Hour
	maxBody       = 2 << 20 // an issue form with a 256 KB logo fits
	minPassword   = 10

	maxFailures   = 8
	failureWindow = 10 * time.Minute
)

const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

// Server serves the manager: the JSON API under /api/ (behind a session) and the web UI.
type Server struct {
	store    *Store
	keys     *Keyring
	sessions *sessions
	limiter  *limiter
	static   http.Handler

	edit sync.Mutex // one change of clients or licenses at a time

	// Replaced by tests.
	now        func() time.Time
	trusted    func() ([]ed25519.PublicKey, error)
	iterations int
	failDelay  time.Duration // after a wrong password
}

// New opens the data folder dir (created when missing) and returns its server.
func New(dir string) (*Server, error) {
	st, err := OpenStore(dir)
	if err != nil {
		return nil, err
	}
	s := &Server{store: st, sessions: &sessions{m: map[string]sessionInfo{}}, limiter: newLimiter(),
		static: uiHandler(), now: time.Now, trusted: license.TrustedKeys, iterations: KDFIterations,
		failDelay: 300 * time.Millisecond}
	s.keys = newKeyring(IdleLock, func() time.Time { return s.now() }, func(reason string) {
		s.record(nil, "-", "key.lock", "", map[string]any{"reason": reason})
	})
	return s, nil
}

// Close locks the signing key and releases the data folder.
func (s *Server) Close() error {
	s.keys.Lock()
	return s.store.Close()
}

// Handler routes the API and the UI.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", s.static)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { replyError(w, http.StatusNotFound, "no such endpoint") })
	mux.HandleFunc("GET /api/session", s.sessionState)
	mux.HandleFunc("POST /api/setup", s.setup)
	mux.HandleFunc("POST /api/login", s.login)

	api := func(pattern string, h userHandler) { mux.HandleFunc(pattern, s.authed(h)) }
	api("POST /api/logout", s.logout)
	api("POST /api/password", s.changePassword)
	api("GET /api/operators", s.listOperators)
	api("POST /api/operators", s.addOperator)
	api("DELETE /api/operators/{name}", s.removeOperator)

	api("GET /api/meta", s.meta)
	api("GET /api/dashboard", s.dashboard)
	api("GET /api/audit", s.auditLog)

	api("GET /api/key", s.keyStatus)
	api("GET /api/key/public", s.keyPublic)
	api("POST /api/key/generate", s.keyGenerate)
	api("POST /api/key/import", s.keyImport)
	api("POST /api/key/unlock", s.keyUnlock)
	api("POST /api/key/lock", s.keyLock)
	api("POST /api/key/passphrase", s.keyPassphrase)

	api("GET /api/clients", s.listClients)
	api("POST /api/clients", s.createClient)
	api("GET /api/clients/{id}", s.getClient)
	api("PUT /api/clients/{id}", s.updateClient)
	api("DELETE /api/clients/{id}", s.deleteClient)
	api("POST /api/clients/{id}/deployments", s.saveDeployment)
	api("PUT /api/clients/{id}/deployments/{dep}", s.saveDeployment)
	api("DELETE /api/clients/{id}/deployments/{dep}", s.deleteDeployment)

	api("GET /api/licenses", s.listLicenses)
	api("POST /api/licenses", s.issue)
	api("GET /api/licenses/{id}", s.getLicense)
	api("GET /api/licenses/{id}/file", s.licenseFile)
	api("POST /api/licenses/{id}/renew", s.renew)
	api("POST /api/verify", s.verify)
	return s.guard(mux)
}

// guard sets the security headers and refuses cross-site requests that change something.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			replyError(w, http.StatusForbidden, "cross-site request refused")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin requires the manager's header, and an Origin (when the browser sends one) of this
// host.
func sameOrigin(r *http.Request) bool {
	if r.Header.Get(CSRFHeader) != "1" {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

// Sessions.

type sessionInfo struct {
	user    string
	expires time.Time
}

// sessions are in memory: a restart signs everyone out (and locks the key).
type sessions struct {
	mu sync.Mutex
	m  map[string]sessionInfo // by token digest
}

func (ss *sessions) create(user string, now time.Time) string {
	token := auth.NewToken()
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for h, x := range ss.m {
		if now.After(x.expires) {
			delete(ss.m, h)
		}
	}
	ss.m[auth.HashToken(token)] = sessionInfo{user: user, expires: now.Add(sessionTTL)}
	return token
}

func (ss *sessions) user(token string, now time.Time) (string, bool) {
	if token == "" {
		return "", false
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	x, ok := ss.m[auth.HashToken(token)]
	if !ok || now.After(x.expires) {
		return "", false
	}
	return x.user, true
}

func (ss *sessions) drop(token string) {
	ss.mu.Lock()
	delete(ss.m, auth.HashToken(token))
	ss.mu.Unlock()
}

// dropUser ends the sessions of user except keep.
func (ss *sessions) dropUser(user, keep string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for h, x := range ss.m {
		if x.user == user && (keep == "" || h != auth.HashToken(keep)) {
			delete(ss.m, h)
		}
	}
}

func sessionToken(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil {
		return c.Value
	}
	return ""
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user string) {
	token := s.sessions.create(user, s.now())
	setCookie(w, r, token, int(sessionTTL/time.Second))
}

// setCookie writes the session cookie; maxAge < 0 deletes it.
func setCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: maxAge, HttpOnly: true,
		Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
}

type userHandler func(w http.ResponseWriter, r *http.Request, user string)

func (s *Server) authed(h userHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.sessions.user(sessionToken(r), s.now())
		if ok {
			_, ok = s.store.User(user)
		}
		if !ok {
			replyError(w, http.StatusUnauthorized, "login required")
			return
		}
		h(w, r, user)
	}
}

// limiter counts failed logins and unlocks per client address.
type limiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
}

func newLimiter() *limiter { return &limiter{failures: map[string][]time.Time{}} }

func (l *limiter) blocked(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.failures[key][:0]
	for _, t := range l.failures[key] {
		if now.Sub(t) < failureWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(l.failures, key)
	} else {
		l.failures[key] = recent
	}
	return len(recent) >= maxFailures
}

func (l *limiter) fail(key string, now time.Time) {
	l.mu.Lock()
	l.failures[key] = append(l.failures[key], now)
	l.mu.Unlock()
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.failures, key)
	l.mu.Unlock()
}

// Helpers.

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// record appends to the audit log; a failure to write it is logged, the action stands.
func (s *Server) record(r *http.Request, user, action, target string, detail map[string]any) {
	e := AuditEntry{Time: s.now().UTC(), User: user, Action: action, Target: target, Detail: detail}
	if r != nil {
		e.IP = clientIP(r)
	}
	if err := s.store.Audit(e); err != nil {
		slog.Error("audit log", "err", err, "action", action)
	}
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func replyError(w http.ResponseWriter, status int, msg string) {
	reply(w, status, map[string]string{"error": msg})
}

// replyErr answers a form error: 400 with its field, or the status of a known error.
func replyErr(w http.ResponseWriter, err error) {
	var fe *FieldError
	switch {
	case errors.As(err, &fe):
		reply(w, http.StatusBadRequest, map[string]string{"error": fe.Message, "field": fe.Field})
	case errors.Is(err, ErrNotFound):
		replyError(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrLocked):
		reply(w, http.StatusLocked, map[string]string{"error": ErrLocked.Error(), "code": "key_locked"})
	case errors.Is(err, ErrNoKey):
		reply(w, http.StatusConflict, map[string]string{"error": ErrNoKey.Error(), "code": "no_key"})
	default:
		slog.Error("request failed", "err", err)
		replyError(w, http.StatusInternalServerError, err.Error())
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v); err != nil {
		return fieldErr("", "the request is not valid JSON")
	}
	return nil
}
