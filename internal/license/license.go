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
	ID        string    `json:"id"`
	Client    string    `json:"client"`
	Edition   string    `json:"edition"`
	Tier      string    `json:"tier"`
	Features  []string  `json:"features"`
	MaxRobots int       `json:"max_robots"` // 0: no limit
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Branding  Branding  `json:"branding"`
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
	return l, nil
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
