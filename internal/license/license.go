// Package license verifies the signed license that decides what a NETRA deployment may use: its
// edition and tier, the feature modules, the number of robots and the client's branding.
//
// A license is the text NETRA-LIC-1.<payload>.<signature>: the payload is the JSON of License in
// base64url, the signature an Ed25519 signature of that base64url text by a key whose public half
// is embedded in netra-api (keys/*.pub). Verification is offline, so on-premise servers without
// internet check it the same way. The core (robots, live map, commands, routes, patrols, schedules,
// floor plans, users, patrol reports) works with or without a license; modules need one.
package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// Prefix starts every license text.
const Prefix = "NETRA-LIC-1."

// Product is the product a NETRA license is for; a license for another product is refused.
const Product = "netra"

// Grace is how long the modules keep working after a license expires.
const Grace = 14 * 24 * time.Hour

// Feature modules a license can grant.
const (
	Anomaly       = "anomaly"        // robot anomaly uploads, the validation queue, category rules, inspection settings
	Crowd         = "crowd"          // people detection, crowd heatmap and timeline
	ReportsExport = "reports.export" // period summary PDF/XLSX, anomaly and patrol exports
	Telegram      = "alerts.telegram"
	Audit         = "audit"       // the audit log page (actions are always recorded)
	AIFinal       = "ai.final"    // final anomaly detection with netra-ai
	Integration   = "integration" // the read-only /api/ext/v1 for third-party applications
	Substation    = "substation"  // substation inspection: assets, thermal, partial discharge
	MultiSite     = "multisite"   // on-premise sites synchronised to a central NETRA
)

// Catalog describes every feature module, in display order.
var Catalog = []struct{ ID, Description string }{
	{Anomaly, "Anomaly queue: robot detections, validation, category rules, inspection settings"},
	{Crowd, "People detection, crowd heatmap and timeline"},
	{Substation, "Substation inspection: assets, inspection points, thermal ΔT (NETA), partial discharge"},
	{ReportsExport, "Period summary PDF/XLSX, anomaly and patrol exports"},
	{Telegram, "Telegram alerts"},
	{Audit, "Audit log page"},
	{AIFinal, "Final anomaly detection with netra-ai"},
	{Integration, "Integration API for third-party applications"},
	{MultiSite, "On-premise sites synchronised to a central NETRA"},
}

// Map modes: the kind of map a deployment draws its robots on. The license fixes it; the
// dashboard cannot change it.
const (
	MapFloorPlan = "floorplan" // an indoor floor plan (SVG, PNG, JPG, PDF) calibrated to the robot map
	MapPGM       = "pgm"       // the robot's own occupancy grid (map.yaml + map.pgm), aligned by its origin
	MapGPS       = "gps"       // outdoor: a basemap in latitude and longitude, robots by GPS or georeference
)

// MapModes lists the map modes in display order.
var MapModes = []string{MapFloorPlan, MapPGM, MapGPS}

// EditionMaps is the map mode of an edition when the license does not name one.
var EditionMaps = map[string]string{"mall": MapFloorPlan, "substation": MapGPS, "custom": MapFloorPlan}

// DefaultMap returns the map mode of an edition (floorplan for an unknown one).
func DefaultMap(edition string) string {
	if m, ok := EditionMaps[edition]; ok {
		return m
	}
	return MapFloorPlan
}

// ValidMap reports whether m is a map mode.
func ValidMap(m string) bool { return slices.Contains(MapModes, m) }

// Editions add the modules of a product line; tiers add the premium modules.
var (
	Editions = map[string][]string{
		"mall":       {Anomaly, Crowd},
		"substation": {Substation},
		"custom":     {},
	}
	Tiers = map[string][]string{
		"basic":      {},
		"pro":        {ReportsExport, Telegram, Audit},
		"enterprise": {ReportsExport, Telegram, Audit, AIFinal, Integration, MultiSite},
	}
)

// Branding is how the dashboard, the login page and the reports present the product.
type Branding struct {
	Name      string `json:"name,omitempty"`       // application name, e.g. "NETRA Gardu"
	Color     string `json:"color,omitempty"`      // primary colour, #rrggbb
	ColorDark string `json:"color_dark,omitempty"` // primary colour in the dark theme
	Logo      string `json:"logo,omitempty"`       // data:image/svg+xml or data:image/png URL
}

// License is the signed payload.
type License struct {
	Product   string   `json:"product,omitempty"` // "netra"; empty in licenses from before products
	ID        string   `json:"id"`
	Client    string   `json:"client"`
	Edition   string   `json:"edition"`
	Tier      string   `json:"tier"`
	Features  []string `json:"features"`
	Map       string   `json:"map,omitempty"` // map mode; empty: the edition's
	MaxRobots int      `json:"max_robots"`    // 0: no limit
	// Installation binds the license to one server (its installation ID); empty: any server.
	Installation string    `json:"installation,omitempty"`
	IssuedAt     time.Time `json:"issued_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Branding     Branding  `json:"branding"`
}

// MapMode is the license's map mode, or its edition's when it names none.
func (l License) MapMode() string {
	if ValidMap(l.Map) {
		return l.Map
	}
	return DefaultMap(l.Edition)
}

// Status of a license at a moment.
type Status string

const (
	Missing Status = "missing"
	Invalid Status = "invalid"
	Valid   Status = "valid"
	InGrace Status = "grace" // expired, modules still on until ExpiresAt + Grace
	Expired Status = "expired"
)

// Features returns the modules of an edition and a tier plus extra ones, minus removed ones, sorted.
func Features(edition, tier string, add, remove []string) ([]string, error) {
	ed, ok := Editions[edition]
	if !ok {
		return nil, fmt.Errorf("unknown edition %q", edition)
	}
	tr, ok := Tiers[tier]
	if !ok {
		return nil, fmt.Errorf("unknown tier %q", tier)
	}
	set := map[string]bool{}
	for _, f := range append(append(append([]string{}, ed...), tr...), add...) {
		if !Known(f) {
			return nil, fmt.Errorf("unknown feature %q", f)
		}
		set[f] = true
	}
	for _, f := range remove {
		delete(set, f)
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out, nil
}

// Known reports whether f is a feature module of the catalog.
func Known(f string) bool {
	return slices.ContainsFunc(Catalog, func(c struct{ ID, Description string }) bool { return c.ID == f })
}

// Sign returns the license text of l signed with key.
func Sign(l License, key ed25519.PrivateKey) (string, error) {
	payload, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	sig := ed25519.Sign(key, []byte(body))
	return Prefix + body + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// ErrSignature means no trusted key signed the license.
var ErrSignature = errors.New("license signature is not from a trusted key")

// Verify checks text against the trusted public keys and returns its license.
func Verify(text string, keys []ed25519.PublicKey) (License, error) {
	var l License
	rest, ok := strings.CutPrefix(strings.TrimSpace(text), Prefix)
	if !ok {
		return l, errors.New("not a NETRA license (no " + Prefix + " prefix)")
	}
	body, sigText, ok := strings.Cut(rest, ".")
	if !ok {
		return l, errors.New("license has no signature")
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigText)
	if err != nil {
		return l, fmt.Errorf("license signature: %w", err)
	}
	trusted := slices.ContainsFunc(keys, func(k ed25519.PublicKey) bool {
		return len(k) == ed25519.PublicKeySize && ed25519.Verify(k, []byte(body), sig)
	})
	if !trusted {
		return l, ErrSignature
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return l, fmt.Errorf("license payload: %w", err)
	}
	if err := json.Unmarshal(payload, &l); err != nil {
		return l, fmt.Errorf("license payload: %w", err)
	}
	if l.Product != "" && l.Product != Product {
		return l, fmt.Errorf("license is for the product %q, not %s", l.Product, Product)
	}
	return l, nil
}

// ErrInstallation means the license is bound to another server.
var ErrInstallation = errors.New("license is for another installation")

// crockford is Crockford's base32 alphabet: no I, L, O or U, so an ID reads well over the phone.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewInstallationID returns a random installation ID such as NETRA-7KQ3-M9XD-2WPA (60 bits).
func NewInstallationID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	var sb strings.Builder
	sb.WriteString("NETRA")
	for i, v := range b {
		if i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(crockford[int(v)%len(crockford)])
	}
	return sb.String()
}

// NormalizeInstallation upper-cases an installation ID and maps the look-alike letters
// (I, L to 1; O to 0) so a typed ID matches the one shown.
func NormalizeInstallation(id string) string {
	id = strings.ToUpper(strings.TrimSpace(id))
	head, rest, ok := strings.Cut(id, "-")
	if !ok {
		return id
	}
	return head + "-" + strings.NewReplacer("I", "1", "L", "1", "O", "0").Replace(rest)
}

// StatusAt tells whether l is valid, in its grace period or expired at now.
func (l License) StatusAt(now time.Time) Status {
	switch {
	case l.ExpiresAt.IsZero() || now.Before(l.ExpiresAt):
		return Valid
	case now.Before(l.ExpiresAt.Add(Grace)):
		return InGrace
	default:
		return Expired
	}
}

// ParsePublicKey reads a base64 Ed25519 public key (the content of a .pub file).
func ParsePublicKey(text string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("not a base64 Ed25519 public key")
	}
	return ed25519.PublicKey(raw), nil
}

// ParsePrivateKey reads a base64 Ed25519 private key (the content of a .key file).
func ParsePrivateKey(text string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, errors.New("not a base64 Ed25519 private key")
	}
	return ed25519.PrivateKey(raw), nil
}
