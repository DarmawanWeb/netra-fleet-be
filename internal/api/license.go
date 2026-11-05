package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
	"github.com/DarmawanWeb/netra-fleet-be/internal/version"
)

// unlicensedRobots is how many robots a deployment without a valid license may register: enough
// to evaluate the core, not to run a fleet.
const unlicensedRobots = 1

// defaultName is the product name without branding.
const defaultName = "NETRA"

// feature refuses a request when the feature module f is not licensed. The backend is the one
// that decides; the dashboard only hides what the license does not include.
func (s *Server) feature(f string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.lic.Has(f) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "feature not licensed", "feature": f})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// robotLimit is the number of robots the license allows; 0 means no limit.
func (s *Server) robotLimit() int {
	switch s.lic.Status() {
	case license.Missing, license.Invalid:
		return unlicensedRobots
	}
	return s.lic.MaxRobots()
}

type brandingView struct {
	Name      string `json:"name"`
	Color     string `json:"color,omitempty"`
	ColorDark string `json:"color_dark,omitempty"`
	Logo      string `json:"logo,omitempty"` // URL of the logo, empty for the default
}

func (s *Server) brandingView() brandingView {
	b := s.lic.Branding()
	v := brandingView{Name: b.Name, Color: b.Color, ColorDark: b.ColorDark}
	if v.Name == "" {
		v.Name = defaultName
	}
	if _, _, ok := decodeLogo(b.Logo); ok {
		v.Logo = "/api/v1/branding/logo?v=" + logoTag(b.Logo)
	}
	return v
}

// branding is public: the login page shows the product's name, colour and logo.
func (s *Server) branding(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	writeJSON(w, http.StatusOK, s.brandingView())
}

func (s *Server) brandingLogo(w http.ResponseWriter, r *http.Request) {
	logo := s.lic.Branding().Logo
	typ, data, ok := decodeLogo(logo)
	if !ok {
		writeError(w, http.StatusNotFound, "no logo")
		return
	}
	tag := `"` + logoTag(logo) + `"`
	w.Header().Set("ETag", tag)
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// decodeLogo reads a data:image/svg+xml or data:image/png base64 URL.
func decodeLogo(url string) (string, []byte, bool) {
	head, body, ok := strings.Cut(url, ",")
	if !ok {
		return "", nil, false
	}
	typ, isBase64 := strings.CutSuffix(strings.TrimPrefix(head, "data:"), ";base64")
	if !isBase64 || (typ != "image/svg+xml" && typ != "image/png") {
		return "", nil, false
	}
	data, err := base64.StdEncoding.DecodeString(body)
	if err != nil || len(data) == 0 {
		return "", nil, false
	}
	return typ, data, true
}

func logoTag(logo string) string {
	sum := sha256.Sum256([]byte(logo))
	return hex.EncodeToString(sum[:6])
}

// licenseView is the Settings > License page: the current license, what each module is, and the
// robot count against the limit.
func (s *Server) getLicense(w http.ResponseWriter, r *http.Request) {
	robots, err := s.store.CountRobots(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"installation_id": s.lic.Installation(),
		"map_modes":       license.MapModes,
		"license":         s.lic.Info(),
		"catalog":         license.Catalog,
		"editions":        license.Editions,
		"tiers":           license.Tiers,
		"robots":          robots,
		"robot_limit":     s.robotLimit(),
	})
}

// putLicense applies a license uploaded in the dashboard and keeps it for the next start. The body
// is the license text, or JSON {"license": "..."}.
func (s *Server) putLicense(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "license too large")
		return
	}
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, "{") {
		var body struct {
			License string `json:"license"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			writeError(w, http.StatusBadRequest, "body must be the license text or {\"license\": \"...\"}")
			return
		}
		text = strings.TrimSpace(body.License)
	}
	if _, err := s.lic.Check(text); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.lic.Apply(text, "upload"); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.store.SaveLicense(r.Context(), text, userOf(r).Username); err != nil {
		fail(w, err)
		return
	}
	s.relay.LicenseChanged()
	s.relay.Broadcast(map[string]any{"type": "license", "license": s.lic.Info()})
	writeJSON(w, http.StatusOK, s.lic.Info())
}

// infoView is GET /api/v1/info: how robots reach this server, what the license includes and
// which map the deployment uses.
type infoView struct {
	PublicURL      string       `json:"public_url"`
	TimeZone       string       `json:"time_zone"`
	Version        string       `json:"version"`
	InstallationID string       `json:"installation_id"`
	Edition        string       `json:"edition"`
	Tier           string       `json:"tier"`
	Features       []string     `json:"features"`
	MapMode        string       `json:"map_mode"`
	RobotLimit     int          `json:"robot_limit"`
	License        license.Info `json:"license"`
	Branding       brandingView `json:"branding"`
}

func (s *Server) infoView() infoView {
	info := s.lic.Info()
	return infoView{
		PublicURL: s.cfg.PublicURL, TimeZone: s.cfg.TimeZone.String(), Version: version.String(),
		InstallationID: s.lic.Installation(), Edition: info.Edition, Tier: info.Tier, Features: info.Features,
		MapMode: s.lic.MapMode(), RobotLimit: s.robotLimit(), License: info, Branding: s.brandingView(),
	}
}
