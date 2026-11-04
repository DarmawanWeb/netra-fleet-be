package api

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DarmawanWeb/netra-fleet-be/internal/geo"
	"github.com/DarmawanWeb/netra-fleet-be/internal/rosmap"
	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
)

// Upload limits: a robot map (map.yaml and its image) and a site overlay image.
const (
	maxRobotMapUpload = 64 << 20
	maxOverlayUpload  = 64 << 20
)

// mapModes refuses a request unless the deployment draws on one of modes, which its license fixes:
// 409 "this deployment uses <mode> maps".
func (s *Server) mapModes(modes []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m := s.lic.MapMode(); !slices.Contains(modes, m) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "this deployment uses " + m + " maps", "map_mode": m})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- robot maps (pgm)

// uploadRobotMap stores a robot map: map.yaml (field yaml, as text or a file) and its image (field
// image, PGM or PNG), rendered to PNG and placed on the map frame by the yaml's origin and resolution.
func (s *Server) uploadRobotMap(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRobotMapUpload)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "multipart form with yaml and image required (at most 64 MiB)")
		return
	}
	text, yamlName, err := formText(r, "yaml")
	if err != nil {
		writeError(w, http.StatusBadRequest, "yaml (the map.yaml text) required")
		return
	}
	meta, err := rosmap.ParseYAML(text)
	if err != nil {
		writeError(w, http.StatusBadRequest, "map.yaml: "+err.Error())
		return
	}
	f, header, err := r.FormFile("image")
	if err != nil {
		writeError(w, http.StatusBadRequest, "image (the map.pgm or a PNG) required")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		writeError(w, http.StatusBadRequest, "image could not be read")
		return
	}
	img, err := rosmap.Decode(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "map image: "+err.Error())
		return
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, rosmap.Render(img, meta)); err != nil {
		fail(w, err)
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "floorplans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(w, err)
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("%d.png", time.Now().UnixNano()))
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		fail(w, err)
		return
	}
	name := firstName(r.FormValue("name"), baseName(yamlName), baseName(header.Filename), "Robot map")
	cal := meta.Calibration(img.Height)
	res, origin := meta.Resolution, meta.Origin
	p := store.FloorPlan{Name: name, Kind: store.PlanPGM, FilePath: path, ContentType: "image/png",
		WidthPx: float64(img.Width), HeightPx: float64(img.Height), OriginXPx: cal.OriginXPx, OriginYPx: cal.OriginYPx,
		PxPerM: cal.PxPerM, RotationRad: cal.RotationRad, Resolution: &res, Origin: &origin}
	id, err := s.store.InsertRobotMap(r.Context(), p)
	if err != nil {
		_ = os.Remove(path)
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": name, "width_px": p.WidthPx, "height_px": p.HeightPx,
		"resolution": res, "origin": origin})
}

// formText reads a form field sent either as text or as a file, with the file's name.
func formText(r *http.Request, field string) (string, string, error) {
	if v := r.FormValue(field); v != "" {
		return v, "", nil
	}
	f, header, err := r.FormFile(field)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1<<20))
	return string(b), header.Filename, err
}

// baseName is a file name without its directory and extension.
func baseName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	if name == "." || name == "/" {
		return ""
	}
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// firstName is the first non-empty name, at most 120 characters.
func firstName(names ...string) string {
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			if utf8.RuneCountInString(n) > 120 {
				n = string([]rune(n)[:120])
			}
			return n
		}
	}
	return ""
}

// ---- georeference (gps)

// basemapView is a tile layer: a URL template with {z}, {x} and {y}, its attribution and the
// deepest zoom it has tiles for; none has no URL.
type basemapView struct {
	Kind        string `json:"kind"`
	URL         string `json:"url"`
	Attribution string `json:"attribution"`
	MaxZoom     int    `json:"max_zoom"`
}

// basemapPresets are the tile layers a site picks by name; both need internet in the browser.
var basemapPresets = []basemapView{
	{Kind: "osm", URL: "https://tile.openstreetmap.org/{z}/{x}/{y}.png", Attribution: "© OpenStreetMap contributors", MaxZoom: 19},
	{Kind: "satellite", URL: "https://server.arcgisonline.com/ArcGIS/rest/services/World_Imagery/MapServer/tile/{z}/{y}/{x}",
		Attribution: "Tiles © Esri — Source: Esri, Maxar, Earthstar Geographics, and the GIS User Community", MaxZoom: 19},
}

// noBasemapZoom is the deepest zoom of a site without tiles.
const noBasemapZoom = 22

func basemapOf(b store.Basemap) basemapView {
	for _, p := range basemapPresets {
		if p.Kind == b.Kind {
			return p
		}
	}
	if b.Kind == "custom" {
		return basemapView{Kind: b.Kind, URL: b.URL, Attribution: b.Attribution, MaxZoom: b.MaxZoom}
	}
	return basemapView{Kind: "none", MaxZoom: noBasemapZoom}
}

// checkBasemap validates a basemap choice; a custom URL may also be a path on this host (an
// offline tile server behind the same proxy).
func checkBasemap(b basemapView) (store.Basemap, error) {
	switch b.Kind {
	case "", "osm", "satellite", "none":
		kind := b.Kind
		if kind == "" {
			kind = "osm"
		}
		return store.Basemap{Kind: kind, MaxZoom: basemapOf(store.Basemap{Kind: kind}).MaxZoom}, nil
	case "custom":
	default:
		return store.Basemap{}, errors.New("basemap kind must be osm, satellite, custom or none")
	}
	u := strings.TrimSpace(b.URL)
	switch {
	case !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") && (!strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//")):
		return store.Basemap{}, errors.New("basemap url must start with https://, http:// or / (a tile server on this host)")
	case len(u) > 1024 || strings.ContainsAny(u, " \t\r\n\"'<>\\`"):
		return store.Basemap{}, errors.New("basemap url must be a URL template of at most 1024 characters without spaces or quotes")
	case !strings.Contains(u, "{z}") || !strings.Contains(u, "{x}") || (!strings.Contains(u, "{y}") && !strings.Contains(u, "{-y}")):
		return store.Basemap{}, errors.New("basemap url must contain {z}, {x} and {y}")
	}
	attribution := strings.TrimSpace(b.Attribution)
	if utf8.RuneCountInString(attribution) > 300 {
		return store.Basemap{}, errors.New("basemap attribution is at most 300 characters")
	}
	zoom := b.MaxZoom
	if zoom == 0 {
		zoom = 19
	}
	if zoom < 1 || zoom > 24 {
		return store.Basemap{}, errors.New("basemap max_zoom must be from 1 to 24")
	}
	return store.Basemap{Kind: "custom", URL: u, Attribution: attribution, MaxZoom: zoom}, nil
}

type pointView struct {
	geo.ControlPoint
	ResidualM *float64 `json:"residual_m"` // ground distance to where the transform puts it; null without a fit
}

type transformView struct {
	geo.Transform
	Method     string  `json:"method"`
	RMSM       float64 `json:"rms_m"`
	HeadingDeg float64 `json:"heading_deg"` // compass heading of the map x axis
}

type overlayView struct {
	URL         string    `json:"url"`
	ContentType string    `json:"content_type"`
	North       float64   `json:"north"`
	South       float64   `json:"south"`
	East        float64   `json:"east"`
	West        float64   `json:"west"`
	Opacity     float64   `json:"opacity"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// geoView is GET /api/v1/geo.
type geoView struct {
	Points     []pointView    `json:"points"`
	HeadingDeg *float64       `json:"heading_deg"`
	Transform  *transformView `json:"transform"`
	Basemap    basemapView    `json:"basemap"`
	Overlay    *overlayView   `json:"overlay"`
	Presets    []basemapView  `json:"presets"`
	UpdatedBy  string         `json:"updated_by"`
	UpdatedAt  *time.Time     `json:"updated_at"`
}

// fitView is POST /api/v1/geo/fit: the fit of the points without saving it.
type fitView struct {
	Points    []pointView    `json:"points"`
	Transform *transformView `json:"transform"`
}

func pointsWith(points []geo.ControlPoint, residuals []float64) []pointView {
	out := make([]pointView, len(points))
	for i, p := range points {
		out[i] = pointView{ControlPoint: p}
		if i < len(residuals) {
			out[i].ResidualM = &residuals[i]
		}
	}
	return out
}

func transformOf(t geo.Transform, method string, rms float64) *transformView {
	return &transformView{Transform: t, Method: method, RMSM: rms, HeadingDeg: t.HeadingDeg(0)}
}

func geoViewOf(g store.GeoSite) geoView {
	v := geoView{HeadingDeg: g.HeadingDeg, Basemap: basemapOf(g.Basemap), Presets: basemapPresets, UpdatedBy: g.UpdatedBy,
		UpdatedAt: g.UpdatedAt}
	var residuals []float64
	if f := g.Fit; f != nil {
		residuals = geo.Residuals(f.Transform, g.Points)
		v.Transform = transformOf(f.Transform, f.Method, f.RMS)
	}
	v.Points = pointsWith(g.Points, residuals)
	if o := g.Overlay; o != nil {
		version := strings.TrimSuffix(filepath.Base(o.Path), filepath.Ext(o.Path))
		v.Overlay = &overlayView{URL: "/api/v1/geo/overlay/file?v=" + version, ContentType: o.ContentType,
			North: o.North, South: o.South, East: o.East, West: o.West, Opacity: o.Opacity, UpdatedAt: o.UpdatedAt}
	}
	return v
}

func (s *Server) writeGeo(w http.ResponseWriter, r *http.Request) {
	g, err := s.store.GeoSite(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, geoViewOf(g))
}

func (s *Server) getGeo(w http.ResponseWriter, r *http.Request) { s.writeGeo(w, r) }

// geoInput is the body of PUT /api/v1/geo and POST /api/v1/geo/fit (which ignores the basemap).
type geoInput struct {
	Points     []geo.ControlPoint `json:"points"`
	HeadingDeg *float64           `json:"heading_deg"`
	Basemap    basemapView        `json:"basemap"`
}

// fitInput validates the points and fits them: no points and no heading is no georeference.
func fitInput(body geoInput) ([]geo.ControlPoint, *float64, *geo.Fit, error) {
	if len(body.Points) > geo.MaxPoints {
		return nil, nil, nil, fmt.Errorf("at most %d control points", geo.MaxPoints)
	}
	points := make([]geo.ControlPoint, len(body.Points))
	for i, p := range body.Points {
		p.Label = strings.TrimSpace(p.Label)
		if utf8.RuneCountInString(p.Label) > 64 {
			return nil, nil, nil, fmt.Errorf("control point %d: the label is at most 64 characters", i+1)
		}
		points[i] = p
	}
	heading := body.HeadingDeg
	if heading != nil {
		if math.IsNaN(*heading) || math.IsInf(*heading, 0) {
			return nil, nil, nil, errors.New("heading_deg must be a number of degrees")
		}
		h := math.Mod(math.Mod(*heading, 360)+360, 360)
		heading = &h
	}
	if len(points) == 0 && heading == nil {
		return points, nil, nil, nil
	}
	fit, err := geo.Solve(points, heading)
	if err != nil {
		return nil, nil, nil, err
	}
	return points, heading, &fit, nil
}

func (s *Server) putGeo(w http.ResponseWriter, r *http.Request) {
	var body geoInput
	if decode(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "body must be {points, heading_deg, basemap}")
		return
	}
	basemap, err := checkBasemap(body.Basemap)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	points, heading, fit, err := fitInput(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	g, err := s.store.GeoSite(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	g.Points, g.HeadingDeg, g.Basemap, g.Fit = points, heading, basemap, nil
	if fit != nil {
		g.Fit = &store.GeoFit{Transform: fit.Transform, Method: fit.Method, RMS: fit.RMS}
	}
	if err := s.store.SaveGeoSite(r.Context(), g, userOf(r).Username); err != nil {
		fail(w, err)
		return
	}
	s.relay.Broadcast(map[string]any{"type": "geo"})
	s.writeGeo(w, r)
}

func (s *Server) fitGeo(w http.ResponseWriter, r *http.Request) {
	var body geoInput
	if decode(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "body must be {points, heading_deg}")
		return
	}
	points, _, fit, err := fitInput(body)
	if err == nil && fit == nil {
		err = geo.ErrTooFew
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, fitView{Points: pointsWith(points, fit.Residuals), Transform: transformOf(fit.Transform, fit.Method, fit.RMS)})
}

// overlayTypes are the overlay images the dashboard can draw, by sniffed content type.
var overlayTypes = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp"}

// overlayBounds reads north, south, east, west (degrees) and opacity (0.05 to 1, default 1).
func overlayBounds(r *http.Request) (store.GeoOverlay, error) {
	var o store.GeoOverlay
	for _, f := range []struct {
		key string
		dst *float64
	}{{"north", &o.North}, {"south", &o.South}, {"east", &o.East}, {"west", &o.West}} {
		v, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue(f.key)), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return o, errors.New("north, south, east and west (degrees) required")
		}
		*f.dst = v
	}
	switch {
	case o.South < -85 || o.North > 85 || o.South >= o.North:
		return o, errors.New("south must be below north, both between -85 and 85")
	case o.West < -180 || o.East > 180 || o.West >= o.East:
		return o, errors.New("west must be below east, both between -180 and 180")
	case o.North-o.South > 1 || o.East-o.West > 1:
		return o, errors.New("an overlay spans at most one degree")
	}
	o.Opacity = 1
	if v := strings.TrimSpace(r.FormValue("opacity")); v != "" {
		op, err := strconv.ParseFloat(v, 64)
		if err != nil || op < 0.05 || op > 1 {
			return o, errors.New("opacity must be from 0.05 to 1")
		}
		o.Opacity = op
	}
	return o, nil
}

// uploadGeoOverlay stores an overlay image (field image: PNG, JPEG or WebP, north up) over its
// bounds. Without an image (or as a plain form) the bounds and opacity of the current one change.
func (s *Server) uploadGeoOverlay(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOverlayUpload)
	// Multipart with the image; a plain form changes the bounds of the current image.
	err := r.ParseMultipartForm(32 << 20)
	if err != nil && !errors.Is(err, http.ErrNotMultipart) {
		writeError(w, http.StatusBadRequest, "form with image, north, south, east and west required (at most 64 MiB)")
		return
	}
	o, err := overlayBounds(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var f multipart.File
	err = http.ErrMissingFile
	if r.MultipartForm != nil {
		f, _, err = r.FormFile("image")
	}
	switch {
	case errors.Is(err, http.ErrMissingFile):
		g, err := s.store.GeoSite(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		if g.Overlay == nil {
			writeError(w, http.StatusBadRequest, "image required")
			return
		}
		o.Path, o.ContentType = g.Overlay.Path, g.Overlay.ContentType
	case err != nil:
		writeError(w, http.StatusBadRequest, "image could not be read")
		return
	default:
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			writeError(w, http.StatusBadRequest, "image could not be read")
			return
		}
		ctype := http.DetectContentType(data)
		ext, ok := overlayTypes[ctype]
		if !ok {
			writeError(w, http.StatusBadRequest, "use a PNG, JPEG or WebP image")
			return
		}
		dir := filepath.Join(s.cfg.DataDir, "geo")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fail(w, err)
			return
		}
		o.Path, o.ContentType = filepath.Join(dir, fmt.Sprintf("overlay-%d%s", time.Now().UnixNano(), ext)), ctype
		if err := os.WriteFile(o.Path, data, 0o644); err != nil {
			fail(w, err)
			return
		}
	}
	old, err := s.store.SetGeoOverlay(r.Context(), &o, userOf(r).Username)
	if err != nil {
		fail(w, err)
		return
	}
	if old != "" {
		_ = os.Remove(old)
	}
	s.relay.Broadcast(map[string]any{"type": "geo"})
	s.writeGeo(w, r)
}

func (s *Server) geoOverlayFile(w http.ResponseWriter, r *http.Request) {
	g, err := s.store.GeoSite(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	if g.Overlay == nil {
		writeError(w, http.StatusNotFound, "no overlay")
		return
	}
	w.Header().Set("Content-Type", g.Overlay.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	http.ServeFile(w, r, g.Overlay.Path)
}

func (s *Server) deleteGeoOverlay(w http.ResponseWriter, r *http.Request) {
	old, err := s.store.SetGeoOverlay(r.Context(), nil, userOf(r).Username)
	if err != nil {
		fail(w, err)
		return
	}
	if old != "" {
		_ = os.Remove(old)
	}
	s.relay.Broadcast(map[string]any{"type": "geo"})
	w.WriteHeader(http.StatusNoContent)
}
