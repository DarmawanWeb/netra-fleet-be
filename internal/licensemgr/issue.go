package licensemgr

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
)

// Limits of the issue form.
const (
	MaxLogo      = 256 << 10 // bytes of the logo file
	MaxRobots    = 10_000
	MaxDays      = 3650
	maxBrandName = 40
	// Expiring is how long before its expiry a license counts as expiring.
	Expiring = 30 * 24 * time.Hour
)

var (
	hexColor     = regexp.MustCompile(`^#[0-9a-f]{6}$`)
	installation = regexp.MustCompile(`^NETRA-[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}$`)
	pngMagic     = []byte("\x89PNG\r\n\x1a\n")
)

// FieldError is a refused form field.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Message }

func fieldErr(field, format string, args ...any) error {
	return &FieldError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// IssueRequest is the issue form.
type IssueRequest struct {
	ClientID     string           `json:"client_id"`
	DeploymentID string           `json:"deployment_id"` // binds the license to its installation
	Edition      string           `json:"edition"`
	Tier         string           `json:"tier"`
	Features     []string         `json:"features"` // the modules; nil: the edition's and the tier's
	Map          string           `json:"map"`      // empty: the edition's
	MaxRobots    int              `json:"max_robots"`
	Validity     Validity         `json:"validity"`
	Branding     license.Branding `json:"branding"`
	Note         string           `json:"note"`       // kept by the manager, not in the license
	Supersedes   string           `json:"supersedes"` // the license this one changes
}

// Validity is how long a license is valid: a number of days, until a moment, or without expiry.
type Validity struct {
	Mode  string `json:"mode"` // days, until, none
	Days  int    `json:"days"`
	Until string `json:"until"` // RFC 3339, or a date: the end of that day in UTC
}

// Expires is the expiry of a license issued at issued; zero for none.
func (v Validity) Expires(issued time.Time) (time.Time, error) {
	switch v.Mode {
	case "days", "":
		if v.Days < 1 || v.Days > MaxDays {
			return time.Time{}, fieldErr("validity.days", "days of validity: 1 to %d", MaxDays)
		}
		return issued.AddDate(0, 0, v.Days), nil
	case "until":
		t, err := time.Parse(time.RFC3339, v.Until)
		if err != nil {
			d, derr := time.Parse(time.DateOnly, v.Until)
			if derr != nil {
				return time.Time{}, fieldErr("validity.until", "choose an end date")
			}
			t = d.Add(24*time.Hour - time.Second)
		}
		t = t.UTC().Truncate(time.Second)
		switch {
		case !t.After(issued):
			return time.Time{}, fieldErr("validity.until", "the end date has passed")
		case t.After(issued.AddDate(0, 0, MaxDays+1)):
			return time.Time{}, fieldErr("validity.until", "at most %d days ahead; use no expiry for a perpetual license", MaxDays)
		}
		return t, nil
	case "none":
		return time.Time{}, nil
	}
	return time.Time{}, fieldErr("validity.mode", "validity: days, until or none")
}

// newLicenseID is a license ID like the CLI's: the issue date and 48 random bits.
func newLicenseID(issued time.Time) string { return issued.Format("20060102") + "-" + newID(6) }

// buildLicense makes the payload of the issue form for client c.
func buildLicense(req IssueRequest, c Client, now time.Time) (license.License, error) {
	l := license.License{Product: license.Product, Client: c.Name, Edition: req.Edition, Tier: req.Tier}
	if _, ok := license.Editions[req.Edition]; !ok {
		return l, fieldErr("edition", "choose an edition")
	}
	if _, ok := license.Tiers[req.Tier]; !ok {
		return l, fieldErr("tier", "choose a tier")
	}
	features, err := modules(req.Edition, req.Tier, req.Features)
	if err != nil {
		return l, err
	}
	l.Features = features
	l.Map = req.Map
	if l.Map == "" {
		l.Map = license.DefaultMap(req.Edition)
	}
	if !license.ValidMap(l.Map) {
		return l, fieldErr("map", "map mode: %s", strings.Join(license.MapModes, ", "))
	}
	if req.MaxRobots < 0 || req.MaxRobots > MaxRobots {
		return l, fieldErr("max_robots", "robot limit: 0 (no limit) to %d", MaxRobots)
	}
	l.MaxRobots = req.MaxRobots
	if req.DeploymentID != "" {
		d, ok := c.Deployment(req.DeploymentID)
		if !ok {
			return l, fieldErr("deployment_id", "the deployment is not one of %s", c.Name)
		}
		if l.Installation, err = normalizeInstallation(d.Installation); err != nil {
			return l, fieldErr("deployment_id", "%s: %v", d.Name, err)
		}
	}
	l.IssuedAt = now.UTC().Truncate(time.Second)
	if l.ExpiresAt, err = req.Validity.Expires(l.IssuedAt); err != nil {
		return l, err
	}
	if l.Branding, err = cleanBranding(req.Branding); err != nil {
		return l, err
	}
	l.ID = newLicenseID(l.IssuedAt)
	return l, nil
}

// modules is the feature list of the form: the edition's and the tier's preset, plus what was
// ticked beyond it, minus what was unticked (license.Features keeps the result sorted).
func modules(edition, tier string, ticked []string) ([]string, error) {
	preset, err := license.Features(edition, tier, nil, nil)
	if err != nil {
		return nil, err
	}
	if ticked == nil {
		return preset, nil
	}
	var add, remove []string
	for _, f := range ticked {
		if !license.Known(f) {
			return nil, fieldErr("features", "unknown module %q", f)
		}
		if !slices.Contains(preset, f) {
			add = append(add, f)
		}
	}
	for _, f := range preset {
		if !slices.Contains(ticked, f) {
			remove = append(remove, f)
		}
	}
	out, err := license.Features(edition, tier, add, remove)
	if err != nil {
		return nil, fieldErr("features", "%v", err)
	}
	return out, nil
}

// normalizeInstallation checks an installation ID, accepting look-alike letters and lower case.
func normalizeInstallation(id string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", nil
	}
	n := license.NormalizeInstallation(id)
	if !installation.MatchString(n) {
		return "", fmt.Errorf("%q is not an installation ID (NETRA-XXXX-XXXX-XXXX)", strings.TrimSpace(id))
	}
	return n, nil
}

// cleanBranding validates the branding of the form. The dark colour defaults to the light one,
// as with netra-license issue.
func cleanBranding(b license.Branding) (license.Branding, error) {
	b.Name = strings.TrimSpace(b.Name)
	if utf8.RuneCountInString(b.Name) > maxBrandName || strings.ContainsFunc(b.Name, unicode.IsControl) {
		return b, fieldErr("branding.name", "application name: at most %d characters", maxBrandName)
	}
	b.Color = strings.ToLower(strings.TrimSpace(b.Color))
	b.ColorDark = strings.ToLower(strings.TrimSpace(b.ColorDark))
	for _, c := range [][2]string{{"branding.color", b.Color}, {"branding.color_dark", b.ColorDark}} {
		if c[1] != "" && !hexColor.MatchString(c[1]) {
			return b, fieldErr(c[0], "%q is not a colour #rrggbb", c[1])
		}
	}
	if b.Color == "" && b.ColorDark != "" {
		return b, fieldErr("branding.color", "set the light colour too: the dashboard needs both")
	}
	if b.ColorDark == "" {
		b.ColorDark = b.Color
	}
	if b.Logo != "" {
		if err := checkLogo(b.Logo); err != nil {
			return b, fieldErr("branding.logo", "%v", err)
		}
	}
	return b, nil
}

// checkLogo accepts what NETRA serves as the logo: an SVG or PNG base64 data URL.
func checkLogo(url string) error {
	head, body, _ := strings.Cut(url, ",")
	if head != "data:image/svg+xml;base64" && head != "data:image/png;base64" {
		return fmt.Errorf("the logo must be an SVG or PNG file")
	}
	data, err := base64.StdEncoding.DecodeString(body)
	switch {
	case err != nil || len(data) == 0:
		return fmt.Errorf("the logo is damaged")
	case len(data) > MaxLogo:
		return fmt.Errorf("the logo is %d KB; at most %d KB", (len(data)+1023)>>10, MaxLogo>>10)
	case head == "data:image/png;base64" && !bytes.HasPrefix(data, pngMagic):
		return fmt.Errorf("the logo is not a PNG file")
	case head == "data:image/svg+xml;base64" && !bytes.Contains(bytes.ToLower(data[:min(len(data), 4096)]), []byte("<svg")):
		return fmt.Errorf("the logo is not an SVG file")
	}
	return nil
}

// renewal is old with new dates and ID, the client's current name and its deployment's current
// installation.
func renewal(old license.License, c Client, installation string, v Validity, now time.Time) (license.License, error) {
	l := old
	l.Product, l.Client, l.Installation = license.Product, c.Name, installation
	l.Features = append([]string{}, old.Features...)
	l.IssuedAt = now.UTC().Truncate(time.Second)
	var err error
	if l.ExpiresAt, err = v.Expires(l.IssuedAt); err != nil {
		return l, err
	}
	l.ID = newLicenseID(l.IssuedAt)
	return l, nil
}

// slug is a file name part made of a name: lower case letters, digits and dashes.
func slug(name string) string {
	var sb strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			sb.WriteRune(r)
			dash = false
		} else if !dash && sb.Len() > 0 {
			sb.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(sb.String(), "-")
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	if s == "" {
		return "netra"
	}
	return s
}

// FileName is the download name of a license: <client-slug>-<YYYYMMDD>.lic.
func FileName(l license.License) string {
	return slug(l.Client) + "-" + l.IssuedAt.UTC().Format("20060102") + ".lic"
}
