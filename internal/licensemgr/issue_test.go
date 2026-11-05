package licensemgr

import (
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
)

var (
	svgLogo = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10"/></svg>`))
	bekasi  = Client{ID: "c1", Name: "PLN UPT Bekasi", Deployments: []Deployment{
		{ID: "d1", Name: "GI Cawang", Installation: "NETRA-7KQ3-M9XD-2WPA"},
		{ID: "d2", Name: "GI Pulogadung"},
	}}
)

func substationRequest() IssueRequest {
	return IssueRequest{
		ClientID: "c1", DeploymentID: "d1", Edition: "substation", Tier: "pro",
		// The pro preset without Telegram alerts, with the integration API.
		Features:  []string{license.Substation, license.ReportsExport, license.Audit, license.Integration},
		MaxRobots: 3, Validity: Validity{Mode: "days", Days: 365},
		Branding: license.Branding{Name: " NETRA Gardu ", Color: "#0F766E", Logo: svgLogo},
	}
}

func TestBuildLicense(t *testing.T) {
	l, err := buildLicense(substationRequest(), bekasi, day.Add(123*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	want := license.License{
		Product: license.Product, ID: l.ID, Client: "PLN UPT Bekasi", Edition: "substation", Tier: "pro",
		Features: []string{"audit", "integration", "reports.export", "substation"}, Map: license.MapGPS, MaxRobots: 3,
		Installation: "NETRA-7KQ3-M9XD-2WPA", IssuedAt: day, ExpiresAt: day.AddDate(0, 0, 365),
		Branding: license.Branding{Name: "NETRA Gardu", Color: "#0f766e", ColorDark: "#0f766e", Logo: svgLogo},
	}
	if !reflect.DeepEqual(l, want) {
		t.Fatalf("license:\n%+v\nwant\n%+v", l, want)
	}
	if !strings.HasPrefix(l.ID, "20261104-") || len(l.ID) != len("20261104-")+12 {
		t.Fatalf("ID %q", l.ID)
	}

	// Without a module list the preset applies; the map follows the request; no deployment: unbound.
	req := substationRequest()
	req.DeploymentID, req.Features, req.Map, req.Validity = "", nil, license.MapPGM, Validity{Mode: "none"}
	l, err = buildLicense(req, bekasi, day)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(l.Features, []string{"alerts.telegram", "audit", "reports.export", "substation"}) ||
		l.Map != license.MapPGM || l.Installation != "" || !l.ExpiresAt.IsZero() {
		t.Fatalf("preset license: %+v", l)
	}
}

func TestBuildLicenseRefusals(t *testing.T) {
	big := "data:image/png;base64," + base64.StdEncoding.EncodeToString(append(pngMagic, make([]byte, MaxLogo)...))
	cases := []struct {
		field  string
		change func(*IssueRequest)
	}{
		{"edition", func(r *IssueRequest) { r.Edition = "airport" }},
		{"tier", func(r *IssueRequest) { r.Tier = "gold" }},
		{"features", func(r *IssueRequest) { r.Features = []string{"teleport"} }},
		{"map", func(r *IssueRequest) { r.Map = "3d" }},
		{"max_robots", func(r *IssueRequest) { r.MaxRobots = -1 }},
		{"deployment_id", func(r *IssueRequest) { r.DeploymentID = "nope" }},
		{"validity.days", func(r *IssueRequest) { r.Validity.Days = 0 }},
		{"validity.until", func(r *IssueRequest) { r.Validity = Validity{Mode: "until", Until: "2026-11-01"} }},
		{"validity.mode", func(r *IssueRequest) { r.Validity.Mode = "forever" }},
		{"branding.name", func(r *IssueRequest) { r.Branding.Name = strings.Repeat("N", 41) }},
		{"branding.color", func(r *IssueRequest) { r.Branding.Color = "teal" }},
		{"branding.color_dark", func(r *IssueRequest) { r.Branding.ColorDark = "#12345" }},
		{"branding.color", func(r *IssueRequest) { r.Branding.Color, r.Branding.ColorDark = "", "#22d3ee" }},
		{"branding.logo", func(r *IssueRequest) { r.Branding.Logo = big }},
		{"branding.logo", func(r *IssueRequest) { r.Branding.Logo = "data:image/jpeg;base64,/9j/" }},
		{"branding.logo", func(r *IssueRequest) {
			r.Branding.Logo = "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("<svg/>"))
		}},
		{"branding.logo", func(r *IssueRequest) {
			r.Branding.Logo = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<html>"))
		}},
	}
	for _, c := range cases {
		req := substationRequest()
		c.change(&req)
		_, err := buildLicense(req, bekasi, day)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != c.field {
			t.Errorf("%s: %v", c.field, err)
		}
	}
	// A deployment without an installation ID yet gives an unbound license.
	req := substationRequest()
	req.DeploymentID = "d2"
	if l, err := buildLicense(req, bekasi, day); err != nil || l.Installation != "" {
		t.Fatalf("deployment without installation ID: %+v %v", l, err)
	}
}

func TestValidity(t *testing.T) {
	cases := []struct {
		v    Validity
		want time.Time
	}{
		{Validity{Days: 30}, day.AddDate(0, 0, 30)},
		{Validity{Mode: "until", Until: "2027-11-04"}, time.Date(2027, 11, 4, 23, 59, 59, 0, time.UTC)},
		{Validity{Mode: "until", Until: "2027-11-04T23:59:59+07:00"}, time.Date(2027, 11, 4, 16, 59, 59, 0, time.UTC)},
		{Validity{Mode: "none"}, time.Time{}},
	}
	for _, c := range cases {
		got, err := c.v.Expires(day)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("%+v: %v %v, want %v", c.v, got, err, c.want)
		}
	}
	if _, err := (Validity{Mode: "until", Until: "2040-01-01"}).Expires(day); err == nil {
		t.Error("an end date beyond the limit accepted")
	}
}

func TestInstallationIDs(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "",
		"NETRA-7KQ3-M9XD-2WPA":   "NETRA-7KQ3-M9XD-2WPA",
		" netra-7kq3-m9xd-2wpa ": "NETRA-7KQ3-M9XD-2WPA",
		"NETRA-OIL0-0000-0000":   "NETRA-0110-0000-0000", // look-alike letters
	} {
		if got, err := normalizeInstallation(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"NETRA-7KQ3-M9XD", "7KQ3-M9XD-2WPA-0000", "NETRA-7KQ3-M9XD-2WP!"} {
		if _, err := normalizeInstallation(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestFileName(t *testing.T) {
	for client, want := range map[string]string{
		"PLN UPT Bekasi":           "pln-upt-bekasi-20261104.lic",
		"  Mall Ciputra — Jakarta": "mall-ciputra-jakarta-20261104.lic",
		"PT. Ezra (R&D)":           "pt-ezra-r-d-20261104.lic",
		"日本":                       "netra-20261104.lic",
	} {
		if got := FileName(license.License{Client: client, IssuedAt: day}); got != want {
			t.Errorf("%q: %q, want %q", client, got, want)
		}
	}
}

func TestRenewalKeepsTheTerms(t *testing.T) {
	old, err := buildLicense(substationRequest(), bekasi, day)
	if err != nil {
		t.Fatal(err)
	}
	later := day.AddDate(0, 11, 0)
	renamed := bekasi
	renamed.Name = "PLN UID Jakarta Raya"
	l, err := renewal(old, renamed, "NETRA-AAAA-BBBB-CCCC", Validity{Mode: "until", Until: "2028-11-04"}, later)
	if err != nil {
		t.Fatal(err)
	}
	if l.ID == old.ID || !l.IssuedAt.Equal(later) || l.ExpiresAt.Year() != 2028 || l.Client != renamed.Name ||
		l.Installation != "NETRA-AAAA-BBBB-CCCC" || !reflect.DeepEqual(l.Features, old.Features) ||
		l.Map != old.Map || l.MaxRobots != old.MaxRobots || l.Branding != old.Branding {
		t.Fatalf("renewal: %+v", l)
	}
}
