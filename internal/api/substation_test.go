package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
	"github.com/DarmawanWeb/netra-fleet-be/internal/substation"
)

var substationRoutes = []struct{ method, path string }{
	{http.MethodGet, "/api/v1/substation/summary"},
	{http.MethodGet, "/api/v1/substation/criteria"},
	{http.MethodPut, "/api/v1/substation/settings"},
	{http.MethodGet, "/api/v1/substation/assets"},
	{http.MethodPost, "/api/v1/substation/assets"},
	{http.MethodDelete, "/api/v1/substation/assets/3"},
	{http.MethodPost, "/api/v1/substation/points"},
	{http.MethodGet, "/api/v1/substation/points/2/trend"},
	{http.MethodPost, "/api/v1/substation/points/2/inspect"},
	{http.MethodGet, "/api/v1/substation/inspections/9/image/thermal"},
	{http.MethodGet, "/api/v1/substation/findings"},
	{http.MethodPost, "/api/v1/substation/findings/4/resolve"},
}

func TestSubstationRoutesFollowTheLicense(t *testing.T) {
	call := func(h http.Handler, method, path string) (int, map[string]string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		var body map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}
	without := New(testConfig(), nil, nil, nil, nil, nil, nil).Handler()
	with := New(testConfig(), nil, nil, nil, nil, nil,
		licensed(t, func(l *license.License) { l.Features = []string{license.Substation} })).Handler()
	for _, rt := range substationRoutes {
		if code, body := call(without, rt.method, rt.path); code != http.StatusForbidden || body["feature"] != license.Substation {
			t.Errorf("unlicensed %s %s: %d %v", rt.method, rt.path, code, body)
		}
		// Licensed, the session check answers.
		if code, _ := call(with, rt.method, rt.path); code != http.StatusUnauthorized {
			t.Errorf("licensed %s %s: %d, want 401", rt.method, rt.path, code)
		}
	}
	// The exports need the export module as well.
	for _, path := range []string{"/api/v1/reports/substation/findings", "/api/v1/reports/substation/inspections"} {
		if code, body := call(with, http.MethodGet, path); code != http.StatusForbidden || body["feature"] != license.ReportsExport {
			t.Errorf("%s without exports: %d %v", path, code, body)
		}
	}
}

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

func TestEvaluateThermalUpload(t *testing.T) {
	p := store.InspectionPoint{ID: 5, Kind: substation.KindThermal, AssetID: i64(1), Targets: []substation.Target{
		{Key: "r", Label: "Clamp R", Group: "clamp", AssetID: i64(7)},
		{Key: "s", Label: "Clamp S", Group: "clamp", AssetID: i64(7)},
		{Key: "t", Label: "Clamp T", Group: "clamp", AssetID: i64(7)},
		{Key: "body", Label: "Body"},
	}}
	m := inspectionMeta{UID: "u1", PointID: 5, AmbientC: f64(30), Pose: &struct{ X, Y, Yaw float64 }{1, 2, 0.5},
		Thermal: []substation.Reading{{Key: "r", MaxC: 40}, {Key: "s", MaxC: 41}, {Key: "t", MaxC: 58}, {Key: "body", MaxC: 45}}}
	if err := m.check(p); err != nil {
		t.Fatal(err)
	}
	in, obs := evaluate(p, m, substation.DefaultSiteSettings)
	if in.Priority != substation.P1 || in.Reference != substation.RefAuto || len(in.Thermal) != 4 || *in.X != 1 {
		t.Fatalf("inspection: %+v", in)
	}
	byKey := map[string]store.Observation{}
	for _, o := range obs {
		byKey[o.TargetKey] = o
	}
	// T: 18 °C over phase R.
	if o := byKey["t"]; o.Priority != substation.P1 || o.Basis != substation.BasisSimilar || *o.DeltaC != 18 ||
		*o.Value != 58 || *o.AssetID != 7 {
		t.Errorf("phase T: %+v", o)
	}
	// Body: no similar component, 15 °C over ambient is priority 3; its asset is the point's.
	if o := byKey["body"]; o.Priority != substation.P3 || o.Basis != substation.BasisAmbient || *o.DeltaC != 15 ||
		*o.AssetID != 1 {
		t.Errorf("body: %+v", o)
	}
	if o := byKey["r"]; o.Priority != substation.None || o.Basis != "" {
		t.Errorf("phase R: %+v", o)
	}
}

func TestEvaluatePDUpload(t *testing.T) {
	p := store.InspectionPoint{ID: 6, Name: "Bushing PD", Kind: substation.KindPD, AssetID: i64(2),
		Settings: substation.Settings{AlarmDB: f64(30)}}
	m := inspectionMeta{UID: "u2", PointID: 6, PD: &substation.PDReading{DB: 25, Detected: true, Kind: "corona"}}
	if err := m.check(p); err != nil {
		t.Fatal(err)
	}
	in, obs := evaluate(p, m, substation.DefaultSiteSettings) // watch 10 from the site, alarm 30 from the point
	if in.Priority != substation.P3 || in.PD == nil || in.PD.Priority != substation.P3 || len(obs) != 1 ||
		obs[0].TargetKey != "pd" || *obs[0].AssetID != 2 || *obs[0].Value != 25 || obs[0].Label != "Bushing PD" {
		t.Fatalf("pd: %+v %+v", in, obs)
	}
}

func TestInspectionMetaCheck(t *testing.T) {
	thermal := store.InspectionPoint{Kind: substation.KindThermal}
	pd := store.InspectionPoint{Kind: substation.KindPD}
	cases := []struct {
		p    store.InspectionPoint
		m    inspectionMeta
		want string
	}{
		{thermal, inspectionMeta{UID: "a b"}, "uid"},
		{thermal, inspectionMeta{UID: "ok"}, "thermal: 1 to"},
		{thermal, inspectionMeta{UID: "ok", Thermal: []substation.Reading{{Key: "r", MaxC: 5000}}}, "thermal[0]"},
		{thermal, inspectionMeta{UID: "ok", AmbientC: f64(95), Thermal: []substation.Reading{{Key: "r", MaxC: 50}}}, "ambient_c"},
		{pd, inspectionMeta{UID: "ok"}, "pd:"},
		{pd, inspectionMeta{UID: "ok", PD: &substation.PDReading{DB: 500}}, "pd:"},
	}
	for _, c := range cases {
		err := c.m.check(c.p)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.m, err, c.want)
		}
	}
}

func TestPlanPointFillsTheSiteLevels(t *testing.T) {
	s := &Server{}
	site := substation.DefaultSiteSettings
	pd := s.planPoint(site, store.InspectionPoint{ID: 1, Kind: substation.KindPD, Settings: substation.Settings{WatchDB: f64(14)}})
	if *pd.Settings.WatchDB != 14 || *pd.Settings.AlarmDB != site.PD.AlarmDB {
		t.Errorf("pd plan: %+v", pd.Settings)
	}
	th := s.planPoint(site, store.InspectionPoint{ID: 2, Kind: substation.KindThermal})
	if th.Settings.Reference != substation.RefAuto || th.Settings.WatchDB != nil {
		t.Errorf("thermal plan: %+v", th.Settings)
	}
}

func TestFindingCaption(t *testing.T) {
	cfg := testConfig()
	cfg.Lang, cfg.PublicURL = "en", "https://netra.example"
	s := &Server{cfg: cfg}
	tag := "PMS-1"
	seen := time.Date(2026, 11, 4, 7, 30, 0, 0, time.UTC)
	thermal := store.Finding{ID: 12, Priority: 1, Kind: substation.KindThermal, Label: "Clamp T", AssetTag: &tag,
		PointName: "Line 1 disconnector", Basis: substation.BasisSimilar, LastValue: f64(58.3), LastDeltaC: f64(18.1),
		LastSeen: seen}
	got := s.findingCaption(thermal)
	for _, want := range []string{"<b>P1 · Major discrepancy; repair immediately</b>", "PMS-1 · Clamp T",
		"Line 1 disconnector", "58.3 °C, ΔT 18.1 °C over a similar component", "04 Nov 2026 07:30",
		"https://netra.example/substation/findings/12"} {
		if !strings.Contains(got, want) {
			t.Errorf("thermal caption %q lacks %q", got, want)
		}
	}
	s.cfg.Lang = "id"
	pd := store.Finding{ID: 3, Priority: 2, Kind: substation.KindPD, Label: "Bushing <PD>", PointName: "Trafo 1",
		Basis: substation.BasisPD, LastValue: f64(25), LastSeen: seen}
	got = s.findingCaption(pd)
	for _, want := range []string{"P2 · Pantau sampai tindakan perbaikan dapat dilakukan", "Bushing &lt;PD&gt;",
		"Partial discharge 25.0 dB"} {
		if !strings.Contains(got, want) {
			t.Errorf("pd caption %q lacks %q", got, want)
		}
	}
}
