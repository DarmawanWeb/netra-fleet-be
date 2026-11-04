package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DarmawanWeb/netra-fleet-be/internal/geo"
	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
)

// mapRoutes are the routes that exist in some map modes only, with the modes that serve them.
var mapRoutes = []struct {
	method, path string
	modes        []string
}{
	{http.MethodPost, "/api/v1/floorplans", []string{license.MapFloorPlan}},
	{http.MethodPost, "/api/v1/floorplans/pgm", []string{license.MapPGM}},
	{http.MethodGet, "/api/v1/geo", []string{license.MapGPS}},
	{http.MethodPut, "/api/v1/geo", []string{license.MapGPS}},
	{http.MethodPost, "/api/v1/geo/fit", []string{license.MapGPS}},
	{http.MethodPost, "/api/v1/geo/overlay", []string{license.MapGPS}},
	{http.MethodGet, "/api/v1/geo/overlay/file?token=x", []string{license.MapGPS}},
	{http.MethodDelete, "/api/v1/geo/overlay", []string{license.MapGPS}},
}

func TestMapModeGatesRoutes(t *testing.T) {
	states := map[string]*license.State{"": nil} // no license: floor plans
	for _, m := range license.MapModes {
		mode := m
		states[mode] = licensed(t, func(l *license.License) { l.Map = mode })
	}
	for name, st := range states {
		mode := st.MapMode()
		h := New(testConfig(), nil, nil, nil, nil, nil, st).Handler()
		for _, rt := range mapRoutes {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(rt.method, rt.path, nil))
			served := false
			for _, m := range rt.modes {
				served = served || m == mode
			}
			if served {
				// Past the map mode check, the session check answers.
				if rec.Code != http.StatusUnauthorized {
					t.Errorf("license %q (%s): %s %s: %d %s, want 401", name, mode, rt.method, rt.path, rec.Code, rec.Body)
				}
				continue
			}
			var body map[string]string
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if rec.Code != http.StatusConflict || body["error"] != "this deployment uses "+mode+" maps" || body["map_mode"] != mode {
				t.Errorf("license %q (%s): %s %s: %d %s, want 409", name, mode, rt.method, rt.path, rec.Code, rec.Body)
			}
		}
		// Existing plans stay viewable in every mode.
		if rec := get(h, "/api/v1/floorplans"); rec.Code != http.StatusUnauthorized {
			t.Errorf("license %q: GET /api/v1/floorplans: %d", name, rec.Code)
		}
	}
}

func TestExpiredLicenseKeepsTheMapMode(t *testing.T) {
	st := licensed(t, func(l *license.License) {
		l.Map, l.ExpiresAt = license.MapPGM, l.IssuedAt.Add(-license.Grace-1)
	})
	h := New(testConfig(), nil, nil, nil, nil, nil, st).Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/floorplans", nil))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "pgm maps") {
		t.Fatalf("expired pgm license: %d %s", rec.Code, rec.Body)
	}
}

func TestCheckBasemap(t *testing.T) {
	ok := map[string]basemapView{
		"osm":                 {},
		"satellite":           {Kind: "satellite", URL: "ignored"},
		"none":                {Kind: "none"},
		"https://t.example/z": {Kind: "custom", URL: " https://t.example/{z}/{x}/{y}.png ", Attribution: "© Site", MaxZoom: 21},
		"/tiles/{z}":          {Kind: "custom", URL: "/tiles/{z}/{x}/{-y}.jpg"},
	}
	for name, in := range ok {
		b, err := checkBasemap(in)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if in.Kind == "custom" && (b.URL != strings.TrimSpace(in.URL) || b.MaxZoom == 0) {
			t.Errorf("%s: %+v", name, b)
		}
		if in.Kind != "custom" && (b.URL != "" || b.Kind == "") {
			t.Errorf("%s: preset stored with %+v", name, b)
		}
	}
	for want, in := range map[string]basemapView{
		"kind must be":           {Kind: "google"},
		"must start with":        {Kind: "custom", URL: "javascript:alert(1)//{z}{x}{y}"},
		"start with https://":    {Kind: "custom", URL: "//cdn.example/{z}/{x}/{y}"},
		"without spaces":         {Kind: "custom", URL: `https://t.example/{z}/{x}/{y}" onerror="x`},
		"must contain {z}":       {Kind: "custom", URL: "https://t.example/{x}/{y}"},
		"max_zoom must be":       {Kind: "custom", URL: "https://t.example/{z}/{x}/{y}", MaxZoom: 30},
		"attribution is at most": {Kind: "custom", URL: "https://t.example/{z}/{x}/{y}", Attribution: strings.Repeat("a", 301)},
	} {
		if _, err := checkBasemap(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	if v := basemapOf(store.Basemap{Kind: "satellite"}); !strings.Contains(v.URL, "World_Imagery") || v.MaxZoom != 19 {
		t.Errorf("satellite preset %+v", v)
	}
}

func TestFitInput(t *testing.T) {
	if points, heading, fit, err := fitInput(geoInput{}); err != nil || fit != nil || heading != nil || len(points) != 0 {
		t.Fatalf("no points: %v %v %v", points, fit, err)
	}
	h := -90.0
	points, heading, fit, err := fitInput(geoInput{Points: []geo.ControlPoint{{Label: "  gate ", Lat: -6.2, Lon: 106.8}}, HeadingDeg: &h})
	if err != nil || fit == nil || fit.Method != geo.MethodHeading || *heading != 270 || points[0].Label != "gate" {
		t.Fatalf("one point with a heading: %+v %v %v", fit, heading, err)
	}
	if _, _, _, err := fitInput(geoInput{Points: []geo.ControlPoint{{Lat: -6.2, Lon: 106.8}}}); err != geo.ErrTooFew {
		t.Fatalf("one point without a heading: %v", err)
	}
	long := geoInput{Points: []geo.ControlPoint{{Label: strings.Repeat("x", 65)}, {}}}
	if _, _, _, err := fitInput(long); err == nil || !strings.Contains(err.Error(), "64 characters") {
		t.Fatalf("long label: %v", err)
	}
}

func TestRobotMapUploadRefusesBadFiles(t *testing.T) {
	s := &Server{cfg: testConfig()} // the refusals come before anything is stored
	upload := func(yaml string, image []byte) *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		if yaml != "" {
			_ = mw.WriteField("yaml", yaml)
		}
		if image != nil {
			part, _ := mw.CreateFormFile("image", "map.pgm")
			_, _ = part.Write(image)
		}
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/floorplans/pgm", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		s.uploadRobotMap(rec, req)
		return rec
	}
	good := "image: map.pgm\nresolution: 0.05\norigin: [-1, -1, 0]\n"
	pgm := append([]byte("P5 2 2 255\n"), 0, 254, 205, 0)
	for want, rec := range map[string]*httptest.ResponseRecorder{
		"yaml (the map.yaml text) required":     upload("", pgm),
		"map.yaml: line 2: resolution must be":  upload("image: map.pgm\nresolution: x\norigin: [0, 0, 0]\n", pgm),
		"image (the map.pgm or a PNG) required": upload(good, nil),
		"map image: PGM data is truncated":      upload(good, []byte("P5 4 4 255\n\x00")),
	} {
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d %s", want, rec.Code, rec.Body)
		}
	}
}

func TestOverlayBounds(t *testing.T) {
	form := func(v string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(v))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return req
	}
	o, err := overlayBounds(form("north=-6.19&south=-6.21&east=106.82&west=106.80&opacity=0.7"))
	if err != nil || o.North != -6.19 || o.West != 106.80 || o.Opacity != 0.7 {
		t.Fatalf("%+v %v", o, err)
	}
	if o, err := overlayBounds(form("north=1&south=0&east=1&west=0")); err != nil || o.Opacity != 1 {
		t.Fatalf("default opacity: %+v %v", o, err)
	}
	for want, v := range map[string]string{
		"required":            "north=1&south=0&east=1",
		"south must be below": "north=0&south=1&east=1&west=0",
		"west must be below":  "north=1&south=0&east=0&west=1",
		"at most one degree":  "north=3&south=0&east=1&west=0",
		"opacity must be":     "north=1&south=0&east=1&west=0&opacity=0",
	} {
		if _, err := overlayBounds(form(v)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

func TestGPSFix(t *testing.T) {
	var fix struct {
		GPS *gpsFix `json:"gps"`
	}
	for text, want := range map[string]bool{
		`{"gps": {"lat": -6.2, "lon": 106.8, "fix": "rtk_fixed", "hdop": 0.7}}`: true,
		`{"gps": {"lat": -6.2, "lon": 106.8}}`:                                  true,
		`{"gps": {"lat": -6.2, "lon": 106.8, "fix": "none"}}`:                   false,
		`{"gps": {"lat": 96.2, "lon": 106.8}}`:                                  false,
		`{"gps": {"lon": 106.8}}`:                                               false,
		`{}`:                                                                    false,
	} {
		fix.GPS = nil
		if err := json.Unmarshal([]byte(text), &fix); err != nil || fix.GPS.valid() != want {
			t.Errorf("%s: %v %v", text, fix.GPS.valid(), err)
		}
	}
}
