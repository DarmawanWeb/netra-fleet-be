package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

var export = fstest.MapFS{
	"index.html":                 {Data: []byte("index")},
	"dashboard.html":             {Data: []byte("dashboard")},
	"dashboard.txt":              {Data: []byte("dashboard payload")},
	"anomalies.html":             {Data: []byte("anomaly list")},
	"anomalies/0.html":           {Data: []byte("anomaly detail")},
	"anomalies/0.txt":            {Data: []byte("anomaly payload")},
	"robots/0.html":              {Data: []byte("robot detail")},
	"substation/findings/0.html": {Data: []byte("finding detail")},
	"substation/findings/0.txt":  {Data: []byte("finding payload")},
	"404.html":                   {Data: []byte("not found page")},
	"_next/static/app.js":        {Data: []byte("js")},
	"logos/ezra.svg":             {Data: []byte("<svg/>")},
	"pdf.worker.min.mjs":         {Data: []byte("worker")},
	"settings/notes.html":        {Data: []byte("nested")},
	"floor-plans.html":           {Data: []byte("plans")},
	"floor-plans/index.md":       {Data: []byte("not served as a page")},
}

func TestResolve(t *testing.T) {
	cases := map[string]string{
		"/dashboard":                 "dashboard.html",
		"/dashboard.txt":             "dashboard.txt",
		"/anomalies":                 "anomalies.html",
		"/anomalies/12":              "anomalies/0.html",
		"/anomalies/12.txt":          "anomalies/0.txt",
		"/robots/l1w-01":             "robots/0.html",
		"/substation/findings/7":     "substation/findings/0.html",
		"/substation/findings/7.txt": "substation/findings/0.txt",
		"/substation/assets/3":       "", // not exported in this fixture
		"/substation/other/3":        "",
		"/logos/ezra.svg":            "logos/ezra.svg",
		"/settings/notes":            "settings/notes.html",
		"/floor-plans":               "floor-plans.html",
		"/../../etc/passwd":          "",
		"/patrols/3":                 "", // not exported in this fixture
		"/nope":                      "",
		"/_next/static/app.js":       "_next/static/app.js",
	}
	for in, want := range cases {
		got, ok := Resolve(export, in)
		if (want == "") == ok || got != want {
			t.Errorf("%s -> %q %v, want %q", in, got, ok, want)
		}
	}
}

func get(h http.Handler, method, target string, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandler(t *testing.T) {
	h := Handler(export)
	if rec := get(h, http.MethodGet, "/?x=1"); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/dashboard?x=1" {
		t.Fatalf("root: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec := get(h, http.MethodGet, "/anomalies/42")
	if rec.Code != http.StatusOK || rec.Body.String() != "anomaly detail" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("detail: %d %q %q", rec.Code, rec.Body, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
		rec.Header().Get("Cache-Control") != "no-cache" || rec.Header().Get("ETag") == "" {
		t.Fatalf("page headers: %v", rec.Header())
	}
	if again := get(h, http.MethodGet, "/anomalies/42", "If-None-Match", rec.Header().Get("ETag")); again.Code != http.StatusNotModified {
		t.Fatalf("revalidation: %d", again.Code)
	}
	if rec := get(h, http.MethodGet, "/anomalies/42.txt?_rsc=abc"); rec.Body.String() != "anomaly payload" ||
		rec.Header().Get("Content-Type") != "text/x-component" {
		t.Fatalf("payload: %q %q", rec.Body, rec.Header().Get("Content-Type"))
	}
	if rec := get(h, http.MethodGet, "/_next/static/app.js"); !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset cache: %q", rec.Header().Get("Cache-Control"))
	}
	if rec := get(h, http.MethodGet, "/missing"); rec.Code != http.StatusNotFound || rec.Body.String() != "not found page" {
		t.Fatalf("404: %d %q", rec.Code, rec.Body)
	}
	if rec := get(h, http.MethodGet, "/api/v1/unknown"); rec.Code != http.StatusNotFound ||
		!strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("api 404: %d %q", rec.Code, rec.Body)
	}
	if rec := get(h, http.MethodPost, "/dashboard"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post: %d", rec.Code)
	}
	if rec := get(h, http.MethodHead, "/dashboard"); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("head: %d %d", rec.Code, rec.Body.Len())
	}
}

func TestFiles(t *testing.T) {
	if _, ok, err := Files(""); ok || err != nil {
		t.Fatalf("no dashboard without the webui tag: %v %v", ok, err)
	}
	dir := t.TempDir()
	if _, ok, err := Files(dir); !ok || err != nil {
		t.Fatalf("directory: %v %v", ok, err)
	}
	if _, _, err := Files(dir + "/missing"); err == nil {
		t.Fatal("missing directory accepted")
	}
}
