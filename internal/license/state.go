package license

import (
	"crypto/ed25519"
	"embed"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
)

//go:embed keys
var keyFiles embed.FS

// BuildKey is one more trusted public key (base64) given at build time with
// -ldflags "-X github.com/DarmawanWeb/netra-fleet-be/internal/license.BuildKey=...". CI sets it to
// a throwaway key to test the licensed modules; release images do not.
var BuildKey string

// TrustedKeys returns the public keys compiled into netra-api (keys/*.pub and BuildKey).
func TrustedKeys() ([]ed25519.PublicKey, error) {
	entries, err := keyFiles.ReadDir("keys")
	if err != nil {
		return nil, err
	}
	var keys []ed25519.PublicKey
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pub") {
			continue
		}
		raw, err := keyFiles.ReadFile(path.Join("keys", e.Name()))
		if err != nil {
			return nil, err
		}
		k, err := ParsePublicKey(string(raw))
		if err != nil {
			return nil, fmt.Errorf("keys/%s: %w", e.Name(), err)
		}
		keys = append(keys, k)
	}
	if BuildKey != "" {
		k, err := ParsePublicKey(BuildKey)
		if err != nil {
			return nil, fmt.Errorf("BuildKey: %w", err)
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// State is the license a running server applies. The zero value has no license: core only.
type State struct {
	mu     sync.RWMutex
	keys   []ed25519.PublicKey
	lic    *License
	source string // "file", "upload"
	err    string // why the last license given was refused
	now    func() time.Time
}

// NewState verifies licenses against keys.
func NewState(keys []ed25519.PublicKey) *State {
	return &State{keys: keys, now: time.Now}
}

// Apply verifies text and, when it is valid and not older than the current license, makes it
// the current one. A refused license leaves the current one in place.
func (s *State) Apply(text, source string) (License, error) {
	l, err := Verify(text, s.keys)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.err = err.Error()
		return l, err
	}
	if s.lic != nil && l.IssuedAt.Before(s.lic.IssuedAt) {
		err = fmt.Errorf("license %s was issued before the current one (%s)", l.ID, s.lic.ID)
		s.err = err.Error()
		return l, err
	}
	s.lic, s.source, s.err = &l, source, ""
	return l, nil
}

// Candidate is a license text and where it came from.
type Candidate struct{ Text, Source string }

// Load applies the newest valid license among candidates (at start: the license file and the one
// uploaded in the dashboard). When none is valid, the first error is kept for the dashboard.
func (s *State) Load(cands ...Candidate) {
	var best *License
	var bestSource, firstErr string
	for _, c := range cands {
		if strings.TrimSpace(c.Text) == "" {
			continue
		}
		l, err := Verify(c.Text, s.keys)
		if err != nil {
			if firstErr == "" {
				firstErr = c.Source + ": " + err.Error()
			}
			continue
		}
		if best == nil || l.IssuedAt.After(best.IssuedAt) {
			best, bestSource = &l, c.Source
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if best != nil {
		s.lic, s.source, s.err = best, bestSource, ""
		return
	}
	s.lic, s.source, s.err = nil, "", firstErr
}

// Check verifies text without applying it.
func (s *State) Check(text string) (License, error) {
	if s == nil {
		return License{}, ErrSignature
	}
	return Verify(text, s.keys)
}

// Status of the current license.
func (s *State) Status() Status {
	if s == nil {
		return Missing
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.statusLocked()
}

func (s *State) statusLocked() Status {
	switch {
	case s.lic != nil:
		return s.lic.StatusAt(s.now())
	case s.err != "":
		return Invalid
	default:
		return Missing
	}
}

// Has reports whether the feature module f is licensed and the license still in force
// (valid or in its grace period).
func (s *State) Has(f string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.statusLocked()
	return s.lic != nil && (st == Valid || st == InGrace) && slices.Contains(s.lic.Features, f)
}

// Features lists the licensed modules in force.
func (s *State) Features() []string {
	if s == nil {
		return []string{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.statusLocked()
	if s.lic == nil || (st != Valid && st != InGrace) {
		return []string{}
	}
	return append([]string{}, s.lic.Features...)
}

// MaxRobots is the robot limit of the license; 0 means no limit. Without a license in force the
// limit stays that of the last license, so an expired deployment keeps its robots but cannot add.
func (s *State) MaxRobots() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lic == nil {
		return 0
	}
	return s.lic.MaxRobots
}

// Branding of the current license, also after it expired (the product keeps its name).
func (s *State) Branding() Branding {
	if s == nil {
		return Branding{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lic == nil {
		return Branding{}
	}
	return s.lic.Branding
}

// Info is what the dashboard is told about the license.
type Info struct {
	Status    Status     `json:"status"`
	ID        string     `json:"id,omitempty"`
	Client    string     `json:"client,omitempty"`
	Edition   string     `json:"edition,omitempty"`
	Tier      string     `json:"tier,omitempty"`
	Features  []string   `json:"features"`
	MaxRobots int        `json:"max_robots"`
	IssuedAt  *time.Time `json:"issued_at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	GraceEnds *time.Time `json:"grace_ends,omitempty"`
	Source    string     `json:"source,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// Info describes the current license.
func (s *State) Info() Info {
	if s == nil {
		return Info{Status: Missing, Features: []string{}}
	}
	features := s.Features()
	s.mu.RLock()
	defer s.mu.RUnlock()
	info := Info{Status: s.statusLocked(), Features: features, Error: s.err}
	if l := s.lic; l != nil {
		info.ID, info.Client, info.Edition, info.Tier = l.ID, l.Client, l.Edition, l.Tier
		info.MaxRobots, info.Source = l.MaxRobots, s.source
		issued := l.IssuedAt
		info.IssuedAt = &issued
		if !l.ExpiresAt.IsZero() {
			exp, grace := l.ExpiresAt, l.ExpiresAt.Add(Grace)
			info.ExpiresAt, info.GraceEnds = &exp, &grace
		}
	}
	return info
}
