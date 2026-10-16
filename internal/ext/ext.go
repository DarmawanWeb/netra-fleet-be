// Package ext serves the integration API (/api/ext/v1) for third-party applications: OAuth2 client
// credentials, read-only access to anomalies (with their photos), robots, patrols, routes and floor
// plans, and the OpenAPI description with Swagger UI at /api/ext/docs.
package ext

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/auth"
	"github.com/DarmawanWeb/netra-fleet-be/internal/config"
	"github.com/DarmawanWeb/netra-fleet-be/internal/metrics"
	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
)

// Scopes a client can be granted. Every endpoint needs exactly one of them.
const (
	ScopeAnomalies = "anomalies:read" // anomalies, their photos, categories
	ScopeRobots    = "robots:read"    // robots and their live state
	ScopePatrols   = "patrols:read"   // patrols and recorded routes
	ScopeMaps      = "maps:read"      // floor plans, calibration and files
)

// Scopes lists every scope in display order.
var Scopes = []string{ScopeAnomalies, ScopeRobots, ScopePatrols, ScopeMaps}

// ValidScopes reports whether every entry is a known scope.
func ValidScopes(list []string) bool {
	for _, s := range list {
		if !slices.Contains(Scopes, s) {
			return false
		}
	}
	return true
}

// Prefix is the path every integration endpoint lives under.
const Prefix = "/api/ext/v1"

// Rate limit per client: sustained requests per second and burst.
const (
	ratePerSecond = 10
	rateBurst     = 30
)

// Store is the part of the database the integration API reads.
type Store interface {
	APIClientByClientID(ctx context.Context, clientID string) (store.APIClient, error)
	CreateAPIToken(ctx context.Context, hash string, clientPK int64, scopes []string, expires time.Time) error
	APITokenClient(ctx context.Context, hash string) (store.APIClient, []string, error)
	TouchAPIClient(ctx context.Context, id int64) error
	InsertAudit(ctx context.Context, e store.AuditEntry) error
	AnomalyFeed(ctx context.Context, f store.AnomalyFeedFilter) ([]store.FeedAnomaly, error)
	FeedAnomalyByID(ctx context.Context, id int64) (store.FeedAnomaly, error)
	ListRules(ctx context.Context) ([]store.Rule, error)
	ListRobots(ctx context.Context) ([]store.Robot, error)
	ListPatrols(ctx context.Context, robotID string, from, to time.Time) ([]store.Patrol, error)
	GetPatrol(ctx context.Context, id int64) (store.Patrol, error)
	ListRoutes(ctx context.Context, robotID string, archived bool) ([]store.Route, error)
	GetRoute(ctx context.Context, id int64) (store.Route, string, error)
	ListFloorPlans(ctx context.Context) ([]store.FloorPlan, error)
	GetFloorPlan(ctx context.Context, id int64) (store.FloorPlan, error)
}

// API holds the integration endpoints.
type API struct {
	cfg      config.Config
	store    Store
	online   func(robotID string) bool
	buckets  *buckets
	failures *failures

	mu      sync.Mutex
	touched map[int64]time.Time // last time last_used_at was written, per client
}

// New wires the integration API; online reports whether a robot is connected right now.
func New(cfg config.Config, st Store, online func(string) bool) *API {
	return &API{
		cfg: cfg, store: st, online: online,
		buckets: newBuckets(ratePerSecond, rateBurst), failures: newFailures(),
		touched: map[int64]time.Time{},
	}
}

// Register adds the integration endpoints to mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST "+Prefix+"/oauth/token", a.token)
	mux.HandleFunc("GET "+Prefix+"/openapi.yaml", a.openAPI)
	mux.HandleFunc("GET /api/ext/docs", a.docsPage)
	mux.HandleFunc("GET /api/ext/docs/{file}", a.docsAsset)

	route := func(scope, pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, a.require(scope, strings.TrimPrefix(pattern, "GET "+Prefix), h))
	}
	route(ScopeAnomalies, "GET "+Prefix+"/anomalies", a.listAnomalies)
	route(ScopeAnomalies, "GET "+Prefix+"/anomalies/{id}", a.getAnomaly)
	route(ScopeAnomalies, "GET "+Prefix+"/anomalies/{id}/image", a.anomalyImage)
	route(ScopeAnomalies, "GET "+Prefix+"/categories", a.categories)
	route(ScopeRobots, "GET "+Prefix+"/robots", a.robots)
	route(ScopePatrols, "GET "+Prefix+"/patrols", a.patrols)
	route(ScopePatrols, "GET "+Prefix+"/patrols/{id}", a.patrol)
	route(ScopePatrols, "GET "+Prefix+"/routes", a.routes)
	route(ScopePatrols, "GET "+Prefix+"/routes/{id}", a.route)
	route(ScopeMaps, "GET "+Prefix+"/floor-plans", a.floorPlans)
	route(ScopeMaps, "GET "+Prefix+"/floor-plans/{id}/file", a.floorPlanFile)
}

type ctxKey int

const clientKey ctxKey = iota

// require checks the bearer token, the scope and the client's rate limit.
func (a *API) require(scope, endpoint string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || token == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="netra"`)
			writeErr(w, http.StatusUnauthorized, "invalid_token", "send Authorization: Bearer <access_token> from POST "+Prefix+"/oauth/token")
			return
		}
		client, granted, err := a.store.APITokenClient(r.Context(), auth.HashToken(strings.TrimSpace(token)))
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				slog.Error("integration token", "err", err)
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="netra", error="invalid_token"`)
			writeErr(w, http.StatusUnauthorized, "invalid_token", "the access token is unknown, expired or revoked")
			return
		}
		// A token keeps only the scopes its client still has (an admin may have removed some).
		if !slices.Contains(granted, scope) || !slices.Contains(client.Scopes, scope) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="netra", error="insufficient_scope", scope="`+scope+`"`)
			writeErr(w, http.StatusForbidden, "insufficient_scope", "this endpoint needs the scope "+scope)
			return
		}
		if wait := a.buckets.take(client.ID); wait > 0 {
			w.Header().Set("Retry-After", retryAfter(wait))
			writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
			return
		}
		a.touch(client.ID)
		metrics.Default.Add("netra_ext_requests_total", "Integration API requests.", 1, "client", client.ClientID,
			"endpoint", endpoint)
		next(w, r.WithContext(context.WithValue(r.Context(), clientKey, client)))
	})
}

// touch records the client's last use at most once a minute.
func (a *API) touch(id int64) {
	a.mu.Lock()
	due := time.Since(a.touched[id]) > time.Minute
	if due {
		a.touched[id] = time.Now()
	}
	a.mu.Unlock()
	if !due {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.store.TouchAPIClient(ctx, id); err != nil {
			slog.Warn("integration client last use", "err", err)
		}
	}()
}

func (a *API) audit(r *http.Request, client store.APIClient, action string, detail any) {
	entry := store.AuditEntry{Username: "api:" + client.ClientID, Action: action, Target: client.Name, IP: clientIP(r)}
	if detail != nil {
		entry.Detail, _ = json.Marshal(detail)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.store.InsertAudit(ctx, entry); err != nil {
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

// baseURL is the scheme and host the client used to reach the API (behind Caddy and Cloudflare).
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = strings.TrimSpace(strings.Split(p, ",")[0])
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = strings.TrimSpace(strings.Split(h, ",")[0])
	}
	return scheme + "://" + host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr answers in the OAuth2 error format used by every integration endpoint.
func writeErr(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": description})
}

// fail maps store errors to answers.
func fail(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "no such item")
		return
	}
	slog.Error("integration request failed", "err", err)
	writeErr(w, http.StatusInternalServerError, "server_error", "internal error, try again later")
}
