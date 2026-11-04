package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func keyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func sample(features ...string) License {
	return License{
		ID: "lic-1", Client: "PLN UPT Bekasi", Edition: "substation", Tier: "pro", Features: features,
		MaxRobots: 2, IssuedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		ExpiresAt: time.Date(2027, 11, 1, 0, 0, 0, 0, time.UTC),
		Branding:  Branding{Name: "NETRA Gardu", Color: "#0f766e"},
	}
}

func TestSignAndVerify(t *testing.T) {
	pub, priv := keyPair(t)
	other, _ := keyPair(t)
	text, err := Sign(sample(Substation), priv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, Prefix) {
		t.Fatalf("no prefix: %s", text)
	}
	l, err := Verify(text+"\n", []ed25519.PublicKey{other, pub})
	if err != nil || l.Client != "PLN UPT Bekasi" || l.Branding.Name != "NETRA Gardu" {
		t.Fatalf("verify: %+v %v", l, err)
	}
	if _, err := Verify(text, []ed25519.PublicKey{other}); !errors.Is(err, ErrSignature) {
		t.Fatalf("untrusted key accepted: %v", err)
	}
}

func TestTamperedPayloadIsRefused(t *testing.T) {
	pub, priv := keyPair(t)
	text, _ := Sign(sample(Substation), priv)
	forged, _ := Sign(sample(Substation, Integration, MultiSite), priv)
	// The payload of one license with the signature of another.
	body := strings.Split(strings.TrimPrefix(forged, Prefix), ".")[0]
	sig := strings.Split(strings.TrimPrefix(text, Prefix), ".")[1]
	if _, err := Verify(Prefix+body+"."+sig, []ed25519.PublicKey{pub}); !errors.Is(err, ErrSignature) {
		t.Fatalf("tampered license accepted: %v", err)
	}
	for _, bad := range []string{"", "hello", Prefix + "abc", Prefix + "abc.!!"} {
		if _, err := Verify(bad, []ed25519.PublicKey{pub}); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestFeaturesFromEditionAndTier(t *testing.T) {
	got, err := Features("substation", "pro", []string{Integration}, []string{Telegram})
	want := []string{Audit, Integration, ReportsExport, Substation}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("features = %v %v, want %v", got, err, want)
	}
	if _, err := Features("hospital", "pro", nil, nil); err == nil {
		t.Fatal("unknown edition accepted")
	}
	if _, err := Features("mall", "gold", nil, nil); err == nil {
		t.Fatal("unknown tier accepted")
	}
	if _, err := Features("mall", "basic", []string{"teleport"}, nil); err == nil {
		t.Fatal("unknown feature accepted")
	}
	for _, list := range []map[string][]string{Editions, Tiers} {
		for name, fs := range list {
			for _, f := range fs {
				if !Known(f) {
					t.Fatalf("%s lists unknown feature %s", name, f)
				}
			}
		}
	}
}

func TestStatusAndGrace(t *testing.T) {
	pub, priv := keyPair(t)
	text, _ := Sign(sample(Substation, Audit), priv)
	s := NewState([]ed25519.PublicKey{pub})
	if s.Status() != Missing || s.Has(Substation) || len(s.Features()) != 0 {
		t.Fatal("no license must mean core only")
	}
	if _, err := s.Apply(text, "file"); err != nil {
		t.Fatal(err)
	}
	expires := sample().ExpiresAt
	for _, c := range []struct {
		at     time.Time
		status Status
		has    bool
	}{
		{expires.Add(-time.Hour), Valid, true},
		{expires.Add(time.Hour), InGrace, true},
		{expires.Add(Grace + time.Hour), Expired, false},
	} {
		s.now = func() time.Time { return c.at }
		if s.Status() != c.status || s.Has(Substation) != c.has || s.Has(Crowd) {
			t.Fatalf("at %v: status %s has %v", c.at, s.Status(), s.Has(Substation))
		}
	}
	// Expired: the modules are off, the robot limit and the branding stay.
	if s.MaxRobots() != 2 || s.Branding().Name != "NETRA Gardu" || len(s.Info().Features) != 0 {
		t.Fatalf("expired license info: %+v", s.Info())
	}
}

func TestApplyKeepsTheCurrentLicenseOnRefusal(t *testing.T) {
	pub, priv := keyPair(t)
	_, stranger := keyPair(t)
	s := NewState([]ed25519.PublicKey{pub})
	s.now = func() time.Time { return sample().IssuedAt }
	current, _ := Sign(sample(Substation), priv)
	if _, err := s.Apply(current, "file"); err != nil {
		t.Fatal(err)
	}
	foreign, _ := Sign(sample(Substation, MultiSite), stranger)
	if _, err := s.Apply(foreign, "upload"); err == nil {
		t.Fatal("foreign license applied")
	}
	older := sample(Substation, MultiSite)
	older.IssuedAt = older.IssuedAt.Add(-24 * time.Hour)
	olderText, _ := Sign(older, priv)
	if _, err := s.Apply(olderText, "upload"); err == nil {
		t.Fatal("an older license replaced the current one")
	}
	if !s.Has(Substation) || s.Has(MultiSite) || s.Info().Error == "" || s.Status() != Valid {
		t.Fatalf("state after refusals: %+v", s.Info())
	}
}

func TestLoadPicksTheNewest(t *testing.T) {
	pub, priv := keyPair(t)
	s := NewState([]ed25519.PublicKey{pub})
	s.now = func() time.Time { return sample().IssuedAt }
	old, _ := Sign(sample(Substation), priv)
	newer := sample(Substation, Telegram)
	newer.ID, newer.IssuedAt = "lic-2", newer.IssuedAt.Add(time.Hour)
	newText, _ := Sign(newer, priv)
	s.Load(Candidate{newText, "upload"}, Candidate{old, "file"}, Candidate{"garbage", "env"})
	if info := s.Info(); info.ID != "lic-2" || info.Source != "upload" || !s.Has(Telegram) || info.Error != "" {
		t.Fatalf("loaded %+v", info)
	}
	s.Load(Candidate{"garbage", "file"})
	if s.Status() != Invalid || s.Has(Substation) || !strings.HasPrefix(s.Info().Error, "file: ") {
		t.Fatalf("invalid only: %+v", s.Info())
	}
}

func TestEmbeddedKeysParse(t *testing.T) {
	if _, err := TrustedKeys(); err != nil {
		t.Fatal(err)
	}
}

func TestKeyText(t *testing.T) {
	if _, err := ParsePublicKey("bm90IGEga2V5"); err == nil {
		t.Fatal("short key accepted")
	}
	if _, err := ParsePrivateKey("bm90IGEga2V5"); err == nil {
		t.Fatal("short private key accepted")
	}
}
