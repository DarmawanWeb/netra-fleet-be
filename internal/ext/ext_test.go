package ext

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/auth"
	"github.com/DarmawanWeb/netra-fleet-be/internal/config"
	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
)

// fakeStore keeps clients and tokens in memory and returns fixed anomalies.
type fakeStore struct {
	clients map[string]*store.APIClient
	tokens  map[string]fakeToken
	feed    []store.FeedAnomaly
	filter  store.AnomalyFeedFilter

	mu     sync.Mutex // audits are written from a goroutine
	audits []store.AuditEntry
}

// waitAudits waits for n audit entries (they are written asynchronously).
func (f *fakeStore) waitAudits(n int) []store.AuditEntry {
	for i := 0; i < 100; i++ {
		f.mu.Lock()
		got := append([]store.AuditEntry(nil), f.audits...)
		f.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

type fakeToken struct {
	clientID string
	scopes   []string
	expires  time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{clients: map[string]*store.APIClient{}, tokens: map[string]fakeToken{}}
}

func (f *fakeStore) addClient(id, secret string, scopes ...string) *store.APIClient {
	c := &store.APIClient{ID: int64(len(f.clients) + 1), ClientID: id, Name: "Partner " + id, Scopes: scopes,
		Enabled: true, SecretHash: auth.HashToken(secret)}
	f.clients[id] = c
	return c
}

func (f *fakeStore) APIClientByClientID(_ context.Context, id string) (store.APIClient, error) {
	if c, ok := f.clients[id]; ok {
		return *c, nil
	}
	return store.APIClient{}, store.ErrNotFound
}

func (f *fakeStore) CreateAPIToken(_ context.Context, hash string, pk int64, scopes []string, exp time.Time) error {
	for _, c := range f.clients {
		if c.ID == pk {
			f.tokens[hash] = fakeToken{clientID: c.ClientID, scopes: scopes, expires: exp}
		}
	}
	return nil
}

func (f *fakeStore) APITokenClient(_ context.Context, hash string) (store.APIClient, []string, error) {
	t, ok := f.tokens[hash]
	if !ok || time.Now().After(t.expires) || !f.clients[t.clientID].Enabled {
		return store.APIClient{}, nil, store.ErrNotFound
	}
	return *f.clients[t.clientID], t.scopes, nil
}

func (f *fakeStore) TouchAPIClient(context.Context, int64) error { return nil }
func (f *fakeStore) InsertAudit(_ context.Context, e store.AuditEntry) error {
	f.mu.Lock()
	f.audits = append(f.audits, e)
	f.mu.Unlock()
	return nil
}

func (f *fakeStore) AnomalyFeed(_ context.Context, filter store.AnomalyFeedFilter) ([]store.FeedAnomaly, error) {
	f.filter = filter
	out := []store.FeedAnomaly{}
	for _, a := range f.feed {
		after := a.UpdatedAt.After(filter.AfterUpdated) ||
			(a.UpdatedAt.Equal(filter.AfterUpdated) && a.ID > filter.AfterID)
		if after && len(out) < filter.Limit {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeStore) FeedAnomalyByID(_ context.Context, id int64) (store.FeedAnomaly, error) {
	for _, a := range f.feed {
		if a.ID == id {
			return a, nil
		}
	}
	return store.FeedAnomaly{}, store.ErrNotFound
}

func (f *fakeStore) ListRules(context.Context) ([]store.Rule, error) {
	return []store.Rule{{Category: "trash", Label: "Trash"}}, nil
}
func (f *fakeStore) ListRobots(context.Context) ([]store.Robot, error) {
	return []store.Robot{{ID: "l1w-01", Name: "L1W 01",
		LastTelemetry: json.RawMessage(`{"ts":1,"mode":"auto","nav":{"state":"FOLLOWING"},"battery":{"pct":76},` +
			`"people":3,"pose":{"x":1.5,"y":-2,"yaw":0.5},"patrol":{"patrol_id":9,"route_id":5}}`)}}, nil
}
func (f *fakeStore) ListPatrols(context.Context, string, time.Time, time.Time) ([]store.Patrol, error) {
	return []store.Patrol{}, nil
}
func (f *fakeStore) GetPatrol(context.Context, int64) (store.Patrol, error) {
	return store.Patrol{}, store.ErrNotFound
}
func (f *fakeStore) ListRoutes(context.Context, string, bool) ([]store.Route, error) {
	return []store.Route{{ID: 5, Name: "Corridor"}}, nil
}
func (f *fakeStore) GetRoute(context.Context, int64) (store.Route, string, error) {
	return store.Route{ID: 5, Name: "Corridor"}, "# x,y,yaw_deg,dwell_sec\n0,0,0,0\n3,4,90,15\n", nil
}
func (f *fakeStore) ListFloorPlans(context.Context) ([]store.FloorPlan, error) {
	return []store.FloorPlan{{ID: 1, Name: "LG", ContentType: "image/svg+xml", PxPerM: 10}}, nil
}
func (f *fakeStore) GetFloorPlan(context.Context, int64) (store.FloorPlan, error) {
	return store.FloorPlan{}, store.ErrNotFound
}

func testAPI(t *testing.T) (*fakeStore, http.Handler) {
	t.Helper()
	fs := newFakeStore()
	fs.addClient("nc_partner", "ncs_secret", ScopeAnomalies, ScopeRobots, ScopePatrols)
	mux := http.NewServeMux()
	New(config.Config{ExtTokenTTL: time.Hour}, fs, func(id string) bool { return id == "l1w-01" }).Register(mux)
	return fs, mux
}

func do(h http.Handler, method, target string, body string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func tokenFor(t *testing.T, h http.Handler, scope string) string {
	t.Helper()
	form := url.Values{"grant_type": {"client_credentials"}, "scope": {scope}}
	req := httptest.NewRequest(http.MethodPost, Prefix+"/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("nc_partner", "ncs_secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("token: %d %s", rec.Code, rec.Body.String())
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		Scope       string `json:"scope"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &tok)
	if tok.TokenType != "Bearer" || tok.ExpiresIn != 3600 || !strings.HasPrefix(tok.AccessToken, TokenPrefix) {
		t.Fatalf("token response %+v", tok)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("token response must not be cached")
	}
	return tok.AccessToken
}

func bearerHeader(tok string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + tok}
}

func TestTokenEndpoint(t *testing.T) {
	fs, h := testAPI(t)
	formCT := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}

	// form fields instead of Basic, all scopes by default
	rec := do(h, http.MethodPost, Prefix+"/oauth/token",
		"grant_type=client_credentials&client_id=nc_partner&client_secret=ncs_secret", formCT)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"scope":"anomalies:read robots:read patrols:read"`) {
		t.Fatalf("form credentials: %d %s", rec.Code, rec.Body.String())
	}
	// JSON body
	rec = do(h, http.MethodPost, Prefix+"/oauth/token",
		`{"grant_type":"client_credentials","client_id":"nc_partner","client_secret":"ncs_secret","scope":"robots:read"}`,
		map[string]string{"Content-Type": "application/json"})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"scope":"robots:read"`) {
		t.Fatalf("json credentials: %d %s", rec.Code, rec.Body.String())
	}
	if audits := fs.waitAudits(2); len(audits) != 2 || audits[0].Username != "api:nc_partner" || audits[0].Action != "ext.token" {
		t.Fatalf("token issuance not audited: %+v", audits)
	}

	cases := []struct {
		body, code string
		status     int
	}{
		{"grant_type=password&client_id=nc_partner&client_secret=ncs_secret", "unsupported_grant_type", 400},
		{"grant_type=client_credentials&client_id=nc_partner", "invalid_request", 400},
		{"grant_type=client_credentials&client_id=nc_partner&client_secret=wrong", "invalid_client", 401},
		{"grant_type=client_credentials&client_id=nc_nobody&client_secret=ncs_secret", "invalid_client", 401},
		{"grant_type=client_credentials&client_id=nc_partner&client_secret=ncs_secret&scope=maps:read", "invalid_scope", 400},
	}
	for _, c := range cases {
		rec := do(h, http.MethodPost, Prefix+"/oauth/token", c.body, formCT)
		if rec.Code != c.status || !strings.Contains(rec.Body.String(), `"error":"`+c.code+`"`) {
			t.Errorf("%s: %d %s, want %d %s", c.body, rec.Code, rec.Body.String(), c.status, c.code)
		}
	}

	fs.clients["nc_partner"].Enabled = false
	rec = do(h, http.MethodPost, Prefix+"/oauth/token",
		"grant_type=client_credentials&client_id=nc_partner&client_secret=ncs_secret", formCT)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled client got a token: %d", rec.Code)
	}
}

func TestTokenEndpointBlocksGuessing(t *testing.T) {
	_, h := testAPI(t)
	formCT := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	for i := 0; i < maxFailures; i++ {
		do(h, http.MethodPost, Prefix+"/oauth/token", "grant_type=client_credentials&client_id=nc_partner&client_secret=x", formCT)
	}
	rec := do(h, http.MethodPost, Prefix+"/oauth/token",
		"grant_type=client_credentials&client_id=nc_partner&client_secret=ncs_secret", formCT)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("after %d failures: %d", maxFailures, rec.Code)
	}
}

func TestScopesAndTokens(t *testing.T) {
	fs, h := testAPI(t)
	if rec := do(h, http.MethodGet, Prefix+"/robots", "", nil); rec.Code != 401 || rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, Prefix+"/robots", "", bearerHeader("nat_forged")); rec.Code != 401 {
		t.Fatalf("forged token: %d", rec.Code)
	}
	tok := tokenFor(t, h, "robots:read")
	rec := do(h, http.MethodGet, Prefix+"/robots", "", bearerHeader(tok))
	if rec.Code != 200 {
		t.Fatalf("robots: %d %s", rec.Code, rec.Body.String())
	}
	var robots struct{ Data []Robot }
	_ = json.Unmarshal(rec.Body.Bytes(), &robots)
	if len(robots.Data) != 1 || !robots.Data[0].Online || robots.Data[0].State == nil ||
		*robots.Data[0].State.BatteryPct != 76 || robots.Data[0].State.Pose.X != 1.5 || *robots.Data[0].State.Patrol.RouteID != 5 {
		t.Fatalf("robot state: %s", rec.Body.String())
	}
	// the token was asked for robots:read only
	if rec := do(h, http.MethodGet, Prefix+"/anomalies", "", bearerHeader(tok)); rec.Code != 403 ||
		!strings.Contains(rec.Body.String(), "insufficient_scope") {
		t.Fatalf("scope not enforced: %d", rec.Code)
	}
	// the administrator removes robots:read from the client: the live token loses it too
	fs.clients["nc_partner"].Scopes = []string{ScopeAnomalies}
	if rec := do(h, http.MethodGet, Prefix+"/robots", "", bearerHeader(tok)); rec.Code != 403 {
		t.Fatalf("revoked scope still works: %d", rec.Code)
	}
}

func TestRateLimit(t *testing.T) {
	_, h := testAPI(t)
	tok := tokenFor(t, h, "patrols:read")
	limited := 0
	for i := 0; i < rateBurst+5; i++ {
		rec := do(h, http.MethodGet, Prefix+"/routes", "", bearerHeader(tok))
		if rec.Code == http.StatusTooManyRequests {
			limited++
			if rec.Header().Get("Retry-After") != "1" {
				t.Fatalf("Retry-After %q", rec.Header().Get("Retry-After"))
			}
		}
	}
	if limited == 0 {
		t.Fatal("no request was rate limited")
	}
}

func TestBucketRefills(t *testing.T) {
	b := newBuckets(10, 2)
	now := time.Unix(0, 0)
	b.now = func() time.Time { return now }
	for i := 0; i < 2; i++ {
		if b.take(1) != 0 {
			t.Fatal("burst not available")
		}
	}
	if wait := b.take(1); wait <= 0 || wait > 100*time.Millisecond {
		t.Fatalf("empty bucket wait %v", wait)
	}
	now = now.Add(150 * time.Millisecond)
	if b.take(1) != 0 {
		t.Fatal("bucket did not refill")
	}
}

func feedRow(id int64, updated time.Time, status string, final *string, image string) store.FeedAnomaly {
	x, y := 4.0, -2.5
	conf := 0.9
	a := store.FeedAnomaly{RobotName: "L1W 01", UpdatedAt: updated}
	a.ID, a.UID, a.RobotID, a.Status, a.DetectedAt = id, "uid", "l1w-01", status, updated.Add(-time.Minute)
	a.EarlyCategory, a.EarlyConfidence, a.EarlyNote = "trash", 0.7, "a bottle"
	a.FinalCategory, a.X, a.Y, a.ImagePath = final, &x, &y, image
	if final != nil {
		a.FinalConfidence = &conf
	}
	return a
}

func TestAnomalyFeed(t *testing.T) {
	fs, h := testAPI(t)
	spill := "spill"
	t0 := time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)
	for i := int64(1); i <= 5; i++ {
		var final *string
		status := "queued"
		if i%2 == 0 {
			final, status = &spill, "needs_validation"
		}
		fs.feed = append(fs.feed, feedRow(i, t0.Add(time.Duration(i)*time.Second), status, final, ""))
	}
	validatedAt := t0.Add(time.Hour)
	fs.feed[3].Status, fs.feed[3].ValidatedAt = "confirmed", &validatedAt

	tok := tokenFor(t, h, "anomalies:read")
	hdr := bearerHeader(tok)
	hdr["X-Forwarded-Proto"] = "https"
	hdr["X-Forwarded-Host"] = "netra.example.com"

	var page AnomalyPage
	rec := do(h, http.MethodGet, Prefix+"/anomalies?limit=2", "", hdr)
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if rec.Code != 200 || len(page.Data) != 2 || !page.HasMore || page.Data[0].ID != 1 {
		t.Fatalf("first page: %d %s", rec.Code, rec.Body.String())
	}
	if page.Data[0].ImageURL != "https://netra.example.com/api/ext/v1/anomalies/1/image" {
		t.Fatalf("image_url %q", page.Data[0].ImageURL)
	}
	if fs.filter.Limit != 3 {
		t.Fatalf("asks one more row than the page to know has_more, asked %d", fs.filter.Limit)
	}
	second := page.Data[1]
	if second.Category != "spill" || second.FinalDetection == nil || second.FinalDetection.Confidence != 0.9 ||
		second.EarlyDetection.Category != "trash" || second.Position == nil || second.Position.Frame != "map" {
		t.Fatalf("representation: %+v", second)
	}

	// follow the cursor to the end
	seen := []int64{page.Data[0].ID, page.Data[1].ID}
	for page.HasMore {
		rec = do(h, http.MethodGet, Prefix+"/anomalies?limit=2&cursor="+page.NextCursor, "", hdr)
		page = AnomalyPage{}
		_ = json.Unmarshal(rec.Body.Bytes(), &page)
		for _, a := range page.Data {
			seen = append(seen, a.ID)
			if a.ID == 4 && (a.Validation == nil || a.Validation.Result != "confirmed") {
				t.Fatalf("validation missing: %+v", a)
			}
		}
	}
	if !slices.Equal(seen, []int64{1, 2, 3, 4, 5}) {
		t.Fatalf("feed order %v", seen)
	}
	// up to date: the same cursor returns nothing and keeps the position
	last := page.NextCursor
	rec = do(h, http.MethodGet, Prefix+"/anomalies?cursor="+last, "", hdr)
	page = AnomalyPage{}
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Data) != 0 || page.HasMore || page.NextCursor != last {
		t.Fatalf("empty poll: %s", rec.Body.String())
	}

	// filters reach the store
	do(h, http.MethodGet, Prefix+"/anomalies?status=confirmed,rejected&final=true&validated=false&category=trash"+
		"&robot_id=l1w-01&detected_from=2026-10-16T08:00:00%2B07:00", "", hdr)
	f := fs.filter
	if !slices.Equal(f.Statuses, []string{"confirmed", "rejected"}) || f.Final == nil || !*f.Final ||
		f.Validated == nil || *f.Validated || f.RobotID != "l1w-01" || f.DetectedFrom == nil ||
		!f.DetectedFrom.Equal(time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("filter %+v", f)
	}
	for _, bad := range []string{"status=open", "final=maybe", "limit=0", "limit=201", "cursor=%25%25",
		"detected_from=yesterday", "cursor=" + last + "&updated_since=2026-10-16T00:00:00Z"} {
		if rec := do(h, http.MethodGet, Prefix+"/anomalies?"+bad, "", hdr); rec.Code != 400 ||
			!strings.Contains(rec.Body.String(), "invalid_request") {
			t.Errorf("%s: %d %s", bad, rec.Code, rec.Body.String())
		}
	}
}

func TestCursorRoundTrip(t *testing.T) {
	c := Cursor{Updated: time.Date(2026, 10, 16, 1, 2, 3, 456789000, time.UTC), ID: 42}
	got, err := DecodeCursor(EncodeCursor(c))
	if err != nil || !got.Updated.Equal(c.Updated) || got.ID != 42 {
		t.Fatalf("round trip %v %v", got, err)
	}
	zero, err := DecodeCursor(EncodeCursor(Cursor{}))
	if err != nil || !zero.Updated.Equal(time.Time{}.Truncate(time.Microsecond)) {
		t.Fatalf("zero cursor %v %v", zero, err)
	}
}

func TestAnomalyImage(t *testing.T) {
	fs, h := testAPI(t)
	img := filepath.Join(t.TempDir(), "a.jpg")
	if err := os.WriteFile(img, []byte("\xff\xd8\xff\xe0 jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs.feed = []store.FeedAnomaly{feedRow(7, time.Now(), "confirmed", nil, img), feedRow(8, time.Now(), "queued", nil, "/nope.jpg")}
	tok := tokenFor(t, h, "anomalies:read")
	rec := do(h, http.MethodGet, Prefix+"/anomalies/7/image", "", bearerHeader(tok))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || !strings.HasPrefix(rec.Body.String(), "\xff\xd8") {
		t.Fatalf("image: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := do(h, http.MethodGet, Prefix+"/anomalies/8/image", "", bearerHeader(tok)); rec.Code != 404 {
		t.Fatalf("missing file: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, Prefix+"/anomalies/99", "", bearerHeader(tok)); rec.Code != 404 ||
		!strings.Contains(rec.Body.String(), "not_found") {
		t.Fatalf("unknown anomaly: %d", rec.Code)
	}
}

func TestRoutePoints(t *testing.T) {
	_, h := testAPI(t)
	tok := tokenFor(t, h, "patrols:read")
	rec := do(h, http.MethodGet, Prefix+"/routes/5", "", bearerHeader(tok))
	var r struct {
		Name   string
		Points []RoutePoint
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &r)
	if rec.Code != 200 || len(r.Points) != 2 || r.Points[1] != (RoutePoint{X: 3, Y: 4, YawDeg: 90, DwellS: 15}) {
		t.Fatalf("route: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(h, http.MethodGet, Prefix+"/patrols?from=2026-10-16T00:00:00Z&to=2026-01-01T00:00:00Z", "", bearerHeader(tok)); rec.Code != 400 {
		t.Fatalf("reversed window: %d", rec.Code)
	}
}

func TestDocs(t *testing.T) {
	_, h := testAPI(t)
	for path, want := range map[string]string{
		Prefix + "/openapi.yaml":             "application/yaml",
		"/api/ext/docs":                      "text/html",
		"/api/ext/docs/swagger-ui-bundle.js": "text/javascript",
		"/api/ext/docs/swagger-ui.css":       "text/css",
		"/api/ext/docs/init.js":              "text/javascript",
		"/api/ext/docs/favicon-32x32.png":    "image/png",
	} {
		rec := do(h, http.MethodGet, path, "", nil)
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), want) || rec.Body.Len() == 0 {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	for _, path := range []string{"/api/ext/docs/LICENSE", "/api/ext/docs/..%2Fopenapi.yaml"} {
		if rec := do(h, http.MethodGet, path, "", nil); rec.Code != 404 {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	spec := do(h, http.MethodGet, Prefix+"/openapi.yaml", "", nil).Body.String()
	for _, p := range []string{"/oauth/token:", "/anomalies:", "/anomalies/{id}/image:", "/robots:", "/patrols:",
		"/routes/{id}:", "/floor-plans/{id}/file:", "/categories:"} {
		if !strings.Contains(spec, "\n  "+p) {
			t.Errorf("spec misses %s", p)
		}
	}
}
