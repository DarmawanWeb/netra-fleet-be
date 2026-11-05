package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/config"
	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
)

// licensed returns a license state holding a license with the given modules.
func licensed(t *testing.T, mutate func(*license.License)) *license.State {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	l := license.License{ID: "test", Client: "Test", Edition: "custom", Tier: "basic", Features: []string{},
		IssuedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(24 * time.Hour)}
	if mutate != nil {
		mutate(&l)
	}
	text, err := license.Sign(l, priv)
	if err != nil {
		t.Fatal(err)
	}
	st := license.NewState([]ed25519.PublicKey{pub}, "NETRA-TEST-0000-0000")
	if _, err := st.Apply(text, "file"); err != nil {
		t.Fatal(err)
	}
	return st
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestUnlicensedModulesAreRefused(t *testing.T) {
	s := New(testConfig(), nil, nil, nil, nil, nil, nil)
	h := s.Handler()
	for _, path := range []string{
		"/api/v1/anomalies", "/api/v1/anomalies/1/image", "/api/v1/rules", "/api/v1/inspection/models",
		"/api/v1/crowd/heatmap", "/api/v1/crowd/timeline", "/api/v1/reports/summary",
		"/api/v1/reports/anomalies", "/api/v1/audit", "/api/v1/api-clients",
	} {
		rec := get(h, path)
		var body map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusForbidden || body["error"] != "feature not licensed" || body["feature"] == "" {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	if rec := get(h, "/api/v1/branding"); rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"name":"NETRA"`)) {
		t.Errorf("branding without license: %d %s", rec.Code, rec.Body)
	}
	if rec := get(h, "/api/v1/branding/logo"); rec.Code != http.StatusNotFound {
		t.Errorf("logo without license: %d", rec.Code)
	}
}

func TestFeatureGateFollowsTheLicense(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	s := &Server{lic: licensed(t, func(l *license.License) { l.Features = []string{license.Anomaly} })}
	if rec := get(s.feature(license.Anomaly, ok), "/x"); rec.Code != http.StatusTeapot {
		t.Fatalf("licensed module refused: %d", rec.Code)
	}
	if rec := get(s.feature(license.Crowd, ok), "/x"); rec.Code != http.StatusForbidden {
		t.Fatalf("unlicensed module served: %d", rec.Code)
	}
}

func TestRobotLimit(t *testing.T) {
	cases := []struct {
		lic  *license.State
		want int
	}{
		{nil, unlicensedRobots},
		{license.NewState(nil, ""), unlicensedRobots},
		{licensed(t, func(l *license.License) { l.MaxRobots = 3 }), 3},
		{licensed(t, nil), 0},
		// Expired beyond the grace period: modules off, but the limit of the license stays.
		{licensed(t, func(l *license.License) {
			l.MaxRobots, l.ExpiresAt = 5, time.Now().Add(-license.Grace-time.Hour)
		}), 5},
	}
	for i, c := range cases {
		if got := (&Server{lic: c.lic}).robotLimit(); got != c.want {
			t.Errorf("case %d: limit %d, want %d", i, got, c.want)
		}
	}
}

func TestUnlicensedRobotDataIsDropped(t *testing.T) {
	s := &Server{} // no store, no hub: reaching either would panic
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("meta", `{"uid":"a1","category":"trash","confidence":0.9}`)
	part, _ := mw.CreateFormFile("image.jpg", "image.jpg")
	_, _ = part.Write([]byte{0xff, 0xd8, 0xff})
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/robot/anomalies", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	s.robotAnomaly(rec, req)
	if rec.Code != http.StatusAccepted || !bytes.Contains(rec.Body.Bytes(), []byte("not licensed")) {
		t.Fatalf("anomaly upload without the module: %d %s", rec.Code, rec.Body)
	}
	s.handleRobotMessage(context.Background(), "l1w-01", []byte(`{"type":"crowd","cells":[],"peak_people":3}`))
}

func TestBrandingFromTheLicense(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)
	s := New(testConfig(), nil, nil, nil, nil, nil, licensed(t, func(l *license.License) {
		l.Features = []string{license.Substation}
		l.Edition, l.Tier = "substation", "pro"
		l.Branding = license.Branding{Name: "NETRA Gardu", Color: "#0f766e",
			Logo: "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(svg)}
	}))
	h := s.Handler()
	var b brandingView
	if err := json.Unmarshal(get(h, "/api/v1/branding").Body.Bytes(), &b); err != nil || b.Name != "NETRA Gardu" ||
		b.Color != "#0f766e" || b.Logo == "" {
		t.Fatalf("branding %+v %v", b, err)
	}
	rec := get(h, b.Logo)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/svg+xml" || !bytes.Equal(rec.Body.Bytes(), svg) {
		t.Fatalf("logo: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	req := httptest.NewRequest(http.MethodGet, b.Logo, nil)
	req.Header.Set("If-None-Match", rec.Header().Get("ETag"))
	again := httptest.NewRecorder()
	h.ServeHTTP(again, req)
	if again.Code != http.StatusNotModified {
		t.Fatalf("logo revalidation: %d", again.Code)
	}
	info := s.infoView()
	if info.Edition != "substation" || len(info.Features) != 1 || info.Branding.Name != "NETRA Gardu" ||
		info.License.Status != license.Valid {
		t.Fatalf("info %+v", info)
	}
}

func TestDecodeLogo(t *testing.T) {
	for _, bad := range []string{"", "data:text/html;base64,PGI+", "data:image/png,raw", "data:image/png;base64,!!"} {
		if _, _, ok := decodeLogo(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func testConfig() config.Config { return config.Config{TimeZone: time.UTC} }
