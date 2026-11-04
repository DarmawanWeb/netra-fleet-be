package licensemgr

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
)

type testEnv struct {
	t    *testing.T
	srv  *Server
	ts   *httptest.Server
	http *http.Client

	mu   sync.Mutex
	keys []ed25519.PublicKey // the keys "built into this build"
}

// trust makes keys the keys built into this build.
func (e *testEnv) trust(keys ...ed25519.PublicKey) {
	e.mu.Lock()
	e.keys = keys
	e.mu.Unlock()
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	srv, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv.iterations = 1000 // KDFIterations is tested in key_test.go; here it would only be slow
	srv.failDelay = 0
	e := &testEnv{t: t, srv: srv}
	srv.trusted = func() ([]ed25519.PublicKey, error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.keys, nil
	}
	e.ts = httptest.NewServer(srv.Handler())
	jar, _ := cookiejar.New(nil)
	e.http = &http.Client{Jar: jar}
	t.Cleanup(func() {
		e.ts.Close()
		_ = srv.Close()
	})
	return e
}

// call sends a request as the UI does (with the manager's header and this Origin) and returns
// the status and body.
func (e *testEnv) call(method, path string, body any, header ...string) (int, []byte) {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, r)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(CSRFHeader, "1")
	req.Header.Set("Origin", e.ts.URL)
	for i := 0; i+1 < len(header); i += 2 {
		if header[i+1] == "" {
			req.Header.Del(header[i])
		} else {
			req.Header.Set(header[i], header[i+1])
		}
	}
	resp, err := e.http.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// must sends a request that must answer want, decoding the answer into out.
func (e *testEnv) must(method, path string, body any, want int, out any) {
	e.t.Helper()
	status, raw := e.call(method, path, body)
	if status != want {
		e.t.Fatalf("%s %s: %d %s, want %d", method, path, status, raw, want)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			e.t.Fatalf("%s %s: %v: %s", method, path, err, raw)
		}
	}
}

// refused checks that a request fails with status and, for a form error, on field.
func (e *testEnv) refused(method, path string, body any, status int, field string) {
	e.t.Helper()
	got, raw := e.call(method, path, body)
	var answer struct{ Error, Field, Code string }
	_ = json.Unmarshal(raw, &answer)
	if got != status || answer.Field != field || answer.Error == "" {
		e.t.Fatalf("%s %s: %d %s, want %d on %q", method, path, got, raw, status, field)
	}
}

func (e *testEnv) setup() {
	e.t.Helper()
	e.must("POST", "/api/setup", map[string]string{"username": "agus", "password": "a-long-password"}, http.StatusCreated, nil)
}

func TestAPIRequiresLogin(t *testing.T) {
	e := newEnv(t)
	var state struct {
		Setup bool            `json:"setup"`
		User  json.RawMessage `json:"user"`
	}
	e.must("GET", "/api/session", nil, http.StatusOK, &state)
	if !state.Setup || string(state.User) != "null" {
		t.Fatalf("first run: %+v", state)
	}
	e.setup()
	e.must("POST", "/api/logout", nil, http.StatusNoContent, nil)
	for _, path := range []string{"/api/dashboard", "/api/meta", "/api/clients", "/api/clients/abc", "/api/licenses",
		"/api/licenses/20261104-0123456789ab", "/api/licenses/20261104-0123456789ab/file", "/api/key",
		"/api/key/public", "/api/audit", "/api/operators"} {
		e.refused("GET", path, nil, http.StatusUnauthorized, "")
	}
	for _, path := range []string{"/api/licenses", "/api/clients", "/api/key/generate", "/api/key/unlock", "/api/verify",
		"/api/operators", "/api/licenses/20261104-0123456789ab/renew"} {
		e.refused("POST", path, map[string]string{}, http.StatusUnauthorized, "")
	}
	// The setup is closed once an operator exists.
	e.refused("POST", "/api/setup", map[string]string{"username": "mallory", "password": "another-password"}, http.StatusConflict, "")
	e.refused("POST", "/api/login", map[string]string{"username": "agus", "password": "wrong-password"}, http.StatusUnauthorized, "")
	e.must("POST", "/api/login", map[string]string{"username": "agus", "password": "a-long-password"}, http.StatusOK, nil)
	e.must("GET", "/api/dashboard", nil, http.StatusOK, nil)
}

func TestSessionCookie(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/setup", strings.NewReader(`{"username":"agus","password":"a-long-password"}`))
	req.Header.Set(CSRFHeader, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	cookies := resp.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Secure {
		t.Fatalf("cookie: %+v", cookies)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("CSP: %q", csp)
	}
}

func TestCSRFIsRefused(t *testing.T) {
	e := newEnv(t)
	e.setup()
	client := map[string]string{"name": "PLN UPT Bekasi"}
	for _, header := range [][]string{
		{CSRFHeader, ""},                        // a plain form or a script without the header
		{"Origin", "https://evil.example"},      // another site
		{"Origin", "null"},                      // a sandboxed frame
		{"Sec-Fetch-Site", "cross-site"},        // the browser says so
		{CSRFHeader, "1", "Origin", "http://x"}, // header, wrong origin
	} {
		if status, raw := e.call("POST", "/api/clients", client, header...); status != http.StatusForbidden {
			t.Errorf("%v: %d %s", header, status, raw)
		}
	}
	// A form post to the login without the header, too.
	resp, err := e.http.Post(e.ts.URL+"/api/login", "application/x-www-form-urlencoded", strings.NewReader("username=agus&password=x"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("form login: %d", resp.StatusCode)
	}
	var clients []clientView
	e.must("GET", "/api/clients", nil, http.StatusOK, &clients)
	if len(clients) != 0 {
		t.Fatalf("a refused request created a client: %+v", clients)
	}
	// Without an Origin header (not a browser): the custom header is enough.
	if status, raw := e.call("POST", "/api/clients", client, "Origin", ""); status != http.StatusCreated {
		t.Fatalf("same-origin request: %d %s", status, raw)
	}
}

func TestLoginIsThrottled(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.must("POST", "/api/logout", nil, http.StatusNoContent, nil)
	for range maxFailures {
		e.refused("POST", "/api/login", map[string]string{"username": "agus", "password": "wrong-password"}, http.StatusUnauthorized, "")
	}
	e.refused("POST", "/api/login", map[string]string{"username": "agus", "password": "a-long-password"}, http.StatusTooManyRequests, "")
}

func TestIssueFlow(t *testing.T) {
	e := newEnv(t)
	e.setup()

	var c clientView
	e.must("POST", "/api/clients", map[string]string{"name": "PLN UPT Bekasi", "contact": "Budi"}, http.StatusCreated, &c)
	var dep Deployment
	e.must("POST", "/api/clients/"+c.ID+"/deployments",
		map[string]string{"name": "GI Cawang", "installation": " netra-7kq3-m9xd-2wpa "}, http.StatusCreated, &dep)
	if dep.Installation != "NETRA-7KQ3-M9XD-2WPA" {
		t.Fatalf("installation not normalised: %+v", dep)
	}
	e.refused("POST", "/api/clients/"+c.ID+"/deployments", map[string]string{"name": "Twice", "installation": "NETRA-7KQ3-M9XD-2WPA"},
		http.StatusBadRequest, "installation")
	e.refused("POST", "/api/clients/"+c.ID+"/deployments", map[string]string{"name": "Bad", "installation": "NETRA-12"},
		http.StatusBadRequest, "installation")

	issue := map[string]any{
		"client_id": c.ID, "deployment_id": dep.ID, "edition": "substation", "tier": "pro",
		"features": []string{"substation", "reports.export", "audit"}, "map": "pgm", "max_robots": 3,
		"validity": map[string]any{"mode": "days", "days": 365},
		"branding": map[string]any{"name": "NETRA Gardu", "color": "#0f766e", "color_dark": "#2dd4bf", "logo": svgLogo},
		"note":     "PO 17/2026",
	}
	e.refused("POST", "/api/licenses", issue, http.StatusConflict, "") // no key yet

	e.refused("POST", "/api/key/generate", map[string]string{"passphrase": "short"}, http.StatusBadRequest, "passphrase")
	var key keyView
	e.must("POST", "/api/key/generate", map[string]string{"passphrase": "correct horse battery"}, http.StatusCreated, &key)
	if !key.Exists || !key.Unlocked || key.Trusted || key.Fingerprint == "" || key.Iterations != 1000 {
		t.Fatalf("key: %+v", key)
	}
	e.refused("POST", "/api/key/generate", map[string]string{"passphrase": "correct horse battery"}, http.StatusConflict, "")
	pub, err := license.ParsePublicKey(key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	var issued licenseView
	e.must("POST", "/api/licenses", issue, http.StatusCreated, &issued)
	if issued.Kind != KindIssue || issued.IssuedBy != "agus" || issued.Status != license.Valid || issued.Text == "" ||
		issued.Deployment != "GI Cawang" || issued.Note != "PO 17/2026" {
		t.Fatalf("issued: %+v", issued)
	}

	// The download is the signed text, accepted with the manager's key, as requested.
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/licenses/"+issued.ID+"/file", nil)
	resp, err := e.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	file, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	_, params, _ := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	if want := "pln-upt-bekasi-" + time.Now().UTC().Format("20060102") + ".lic"; params["filename"] != want {
		t.Fatalf("file name %q, want %q", params["filename"], want)
	}
	l, err := license.Verify(string(file), []ed25519.PublicKey{pub})
	if err != nil {
		t.Fatal(err)
	}
	if l.Product != license.Product || l.Client != "PLN UPT Bekasi" || l.Edition != "substation" || l.Tier != "pro" ||
		!reflect.DeepEqual(l.Features, []string{"audit", "reports.export", "substation"}) || l.Map != license.MapPGM ||
		l.MaxRobots != 3 || l.Installation != "NETRA-7KQ3-M9XD-2WPA" || l.ExpiresAt.Sub(l.IssuedAt) != 365*24*time.Hour ||
		l.Branding.Name != "NETRA Gardu" || l.Branding.ColorDark != "#2dd4bf" || l.Branding.Logo != svgLogo {
		t.Fatalf("signed license: %+v", l)
	}
	// NETRA accepts it on that installation only.
	if _, err := license.NewState([]ed25519.PublicKey{pub}, "NETRA-7KQ3-M9XD-2WPA").Apply(string(file), "upload"); err != nil {
		t.Fatalf("bound server: %v", err)
	}
	if _, err := license.NewState([]ed25519.PublicKey{pub}, "NETRA-AAAA-BBBB-CCCC").Apply(string(file), "upload"); err == nil {
		t.Fatal("another server accepted a bound license")
	}

	// A locked key signs nothing; a wrong passphrase does not unlock it.
	e.must("POST", "/api/key/lock", nil, http.StatusOK, &key)
	if key.Unlocked {
		t.Fatal("still unlocked")
	}
	status, raw := e.call("POST", "/api/licenses", issue)
	if status != http.StatusLocked || !bytes.Contains(raw, []byte("key_locked")) {
		t.Fatalf("issue with a locked key: %d %s", status, raw)
	}
	e.refused("POST", "/api/key/unlock", map[string]string{"passphrase": "wrong horse battery"}, http.StatusBadRequest, "passphrase")
	e.must("POST", "/api/key/unlock", map[string]string{"passphrase": "correct horse battery"}, http.StatusOK, &key)

	// Renew: the same terms, new dates, replacing the first license.
	var renewed licenseView
	e.must("POST", "/api/licenses/"+issued.ID+"/renew", map[string]any{"validity": map[string]any{"mode": "days", "days": 20}},
		http.StatusCreated, &renewed)
	if renewed.Kind != KindRenew || renewed.Supersedes != issued.ID || !renewed.Expiring ||
		!reflect.DeepEqual(renewed.License.Features, l.Features) || renewed.License.Installation != l.Installation {
		t.Fatalf("renewed: %+v", renewed)
	}
	var old licenseView
	e.must("GET", "/api/licenses/"+issued.ID, nil, http.StatusOK, &old)
	if old.SupersededBy != renewed.ID {
		t.Fatalf("old license not replaced: %+v", old)
	}

	// Change: new terms replacing the renewal.
	issue["supersedes"], issue["tier"], issue["features"] = renewed.ID, "enterprise", nil
	var changed licenseView
	e.must("POST", "/api/licenses", issue, http.StatusCreated, &changed)
	if changed.Kind != KindChange || changed.Supersedes != renewed.ID || len(changed.License.Features) != 7 {
		t.Fatalf("changed: %+v", changed)
	}
	issue["supersedes"] = "20200101-000000000000"
	e.refused("POST", "/api/licenses", issue, http.StatusBadRequest, "supersedes")

	var list []licenseView
	e.must("GET", "/api/licenses?current=1", nil, http.StatusOK, &list)
	if len(list) != 1 || list[0].ID != changed.ID {
		t.Fatalf("current licenses: %+v", list)
	}
	e.must("GET", "/api/licenses?status=expiring", nil, http.StatusOK, &list)
	if len(list) != 1 || list[0].ID != renewed.ID {
		t.Fatalf("expiring licenses: %+v", list)
	}
	e.must("GET", "/api/licenses?edition=mall", nil, http.StatusOK, &list)
	if len(list) != 0 {
		t.Fatalf("mall licenses: %+v", list)
	}
	e.must("GET", "/api/licenses?client="+c.ID+"&status=valid", nil, http.StatusOK, &list)
	if len(list) != 3 || list[0].Text != "" {
		t.Fatalf("client licenses: %+v", list)
	}

	// Verify: the manager's key signs it; this build has no key built in yet.
	var check verifyView
	e.must("POST", "/api/verify", map[string]string{"text": string(file), "installation": "netra-7kq3-m9xd-2wpa"}, http.StatusOK, &check)
	if !check.Manager.OK || check.BuiltIn.OK || check.Status != license.Valid || check.Record == nil || !check.Record.SameText ||
		check.Installation == nil || !check.Installation.Match || check.Warning != "" {
		t.Fatalf("verify: %+v", check)
	}
	e.trust(pub)
	wrapped := strings.TrimSpace(string(file))
	wrapped = wrapped[:60] + "\n" + wrapped[60:]
	e.must("POST", "/api/verify", map[string]string{"text": wrapped, "installation": "NETRA-AAAA-BBBB-CCCC"}, http.StatusOK, &check)
	if !check.BuiltIn.OK || check.Warning == "" || check.Installation.Match {
		t.Fatalf("verify a wrapped text: %+v", check)
	}
	// More robots in the payload, the signature of the original.
	forged := l
	forged.MaxRobots = 50
	payload, _ := json.Marshal(forged)
	sig := strings.TrimSpace(string(file))[strings.LastIndex(strings.TrimSpace(string(file)), ".")+1:]
	tampered := license.Prefix + base64.RawURLEncoding.EncodeToString(payload) + "." + sig
	e.must("POST", "/api/verify", map[string]string{"text": tampered}, http.StatusOK, &check)
	if check.Manager.OK || check.BuiltIn.OK || check.Error != "" || check.License == nil || check.License.MaxRobots != 50 {
		t.Fatalf("verify a tampered text: %+v", check)
	}
	e.must("POST", "/api/verify", map[string]string{"text": "hello"}, http.StatusOK, &check)
	if check.Error == "" {
		t.Fatal("verify accepted garbage")
	}
	e.must("GET", "/api/key", nil, http.StatusOK, &key)
	if !key.Trusted {
		t.Fatal("the build's key is not recognised")
	}

	var dash struct {
		Counts    counts        `json:"counts"`
		Attention []licenseView `json:"attention"`
		Recent    []AuditEntry  `json:"recent"`
	}
	e.must("GET", "/api/dashboard", nil, http.StatusOK, &dash)
	if dash.Counts.Clients != 1 || dash.Counts.Deployments != 1 || dash.Counts.Licenses != 3 || dash.Counts.Current != 1 ||
		dash.Counts.Valid != 1 || len(dash.Attention) != 0 || len(dash.Recent) == 0 {
		t.Fatalf("dashboard: %+v", dash)
	}

	// A client or deployment with licenses stays.
	e.refused("DELETE", "/api/clients/"+c.ID, nil, http.StatusConflict, "")
	e.refused("DELETE", "/api/clients/"+c.ID+"/deployments/"+dep.ID, nil, http.StatusConflict, "")

	var log []AuditEntry
	e.must("GET", "/api/audit", nil, http.StatusOK, &log)
	seen := map[string]bool{}
	for _, entry := range log {
		seen[entry.Action] = true
		if entry.Action != "key.lock" && entry.User != "agus" {
			t.Errorf("audit entry without its operator: %+v", entry)
		}
	}
	for _, action := range []string{"setup", "client.create", "deployment.create", "key.generate", "license.issue",
		"key.lock", "key.unlock_failed", "key.unlock", "license.renew", "license.change"} {
		if !seen[action] {
			t.Errorf("audit log without %s", action)
		}
	}

	// A reinstalled server has a new installation ID: the deployment gets it and a renewal follows it.
	e.must("PUT", "/api/clients/"+c.ID+"/deployments/"+dep.ID, map[string]string{"name": "GI Cawang", "installation": "NETRA-AAAA-BBBB-CCCC"},
		http.StatusOK, nil)
	var moved licenseView
	e.must("POST", "/api/licenses/"+changed.ID+"/renew", map[string]any{"validity": map[string]any{"mode": "none"}}, http.StatusCreated, &moved)
	if moved.License.Installation != "NETRA-AAAA-BBBB-CCCC" || moved.Binding != "NETRA-AAAA-BBBB-CCCC" || !moved.License.ExpiresAt.IsZero() {
		t.Fatalf("renewal after a reinstall: %+v", moved)
	}

	// The data folder: the record and the text of each license, the key file without the key in clear.
	dir := e.srv.store.Dir()
	if raw, err := os.ReadFile(filepath.Join(dir, licensesDir, issued.ID+".lic")); err != nil || string(raw) != string(file) {
		t.Fatalf(".lic: %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(dir, keyFileName)); err != nil || !bytes.Contains(raw, []byte(key.PublicKey)) {
		t.Fatalf("key.enc: %v", err)
	}
}

func TestOperators(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.refused("POST", "/api/operators", map[string]string{"username": "Bad Name", "password": "a-long-password"}, http.StatusBadRequest, "username")
	e.refused("POST", "/api/operators", map[string]string{"username": "dewi", "password": "short"}, http.StatusBadRequest, "password")
	e.must("POST", "/api/operators", map[string]string{"username": "dewi", "password": "another-password"}, http.StatusCreated, nil)
	e.refused("POST", "/api/operators", map[string]string{"username": "dewi", "password": "another-password"}, http.StatusBadRequest, "username")
	e.refused("DELETE", "/api/operators/agus", nil, http.StatusConflict, "")
	e.refused("POST", "/api/password", map[string]string{"current": "nope", "new": "a-newer-password"}, http.StatusBadRequest, "current")
	e.must("POST", "/api/password", map[string]string{"current": "a-long-password", "new": "a-newer-password"}, http.StatusNoContent, nil)
	e.must("DELETE", "/api/operators/dewi", nil, http.StatusNoContent, nil)
	var ops []operatorView
	e.must("GET", "/api/operators", nil, http.StatusOK, &ops)
	if len(ops) != 1 || ops[0].Username != "agus" || ops[0].LastLogin == nil {
		t.Fatalf("operators: %+v", ops)
	}
}

func TestImportAndChangePassphrase(t *testing.T) {
	e := newEnv(t)
	e.setup()
	pub, priv := newKey(t)
	e.refused("POST", "/api/key/import", map[string]string{"key": "garbage", "passphrase": "correct horse battery"}, http.StatusBadRequest, "key")
	var key keyView
	e.must("POST", "/api/key/import", map[string]string{"key": base64Key(priv), "passphrase": "correct horse battery"}, http.StatusCreated, &key)
	if key.PublicKey != base64Key(pub) || key.Source != "imported" || !key.Unlocked {
		t.Fatalf("imported key: %+v", key)
	}
	e.refused("POST", "/api/key/passphrase", map[string]string{"current": "wrong horse battery", "new": "a brand new passphrase"},
		http.StatusBadRequest, "current")
	e.must("POST", "/api/key/passphrase", map[string]string{"current": "correct horse battery", "new": "a brand new passphrase"},
		http.StatusOK, nil)
	e.must("POST", "/api/key/lock", nil, http.StatusOK, nil)
	e.refused("POST", "/api/key/unlock", map[string]string{"passphrase": "correct horse battery"}, http.StatusBadRequest, "passphrase")
	e.must("POST", "/api/key/unlock", map[string]string{"passphrase": "a brand new passphrase"}, http.StatusOK, &key)
	if key.PublicKey != base64Key(pub) {
		t.Fatal("the key changed with the passphrase")
	}
	status, raw := e.call("GET", "/api/key/public", nil)
	if status != http.StatusOK || strings.TrimSpace(string(raw)) != base64Key(pub) {
		t.Fatalf(".pub: %d %s", status, raw)
	}
}

func TestUIIsServed(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/", "/app.css", "/js/main.js"} {
		status, raw := e.call("GET", path, nil)
		if status != http.StatusOK || len(raw) == 0 {
			t.Errorf("%s: %d", path, status)
		}
	}
	if status, _ := e.call("GET", "/js/", nil); status != http.StatusNotFound {
		t.Errorf("folder listing: %d", status)
	}
}
