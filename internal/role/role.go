// Package role names the parts of NETRA a process runs: api (dashboards, integration API),
// gateway (robot connections and uploads) and worker (pipeline, scheduler, backups, alerts).
// One process runs all of them unless NETRA_ROLES splits them across processes.
package role

import (
	"fmt"
	"strings"
)

// Set is a set of roles.
type Set uint8

// The roles.
const (
	API     Set = 1 << iota // dashboard REST and WebSocket, dashboard files, integration API
	Gateway                 // robot WebSocket and uploads under /api/v1/robot/
	Worker                  // anomaly pipeline, scheduler, daily backups, Telegram alerts

	All = API | Gateway | Worker
)

var names = []struct {
	name string
	role Set
}{{"api", API}, {"gateway", Gateway}, {"worker", Worker}}

// Parse reads "all" or a comma list of api, gateway and worker (blank means all).
func Parse(v string) (Set, error) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" || v == "all" {
		return All, nil
	}
	var s Set
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "all" {
			return 0, fmt.Errorf("roles %q: all cannot be combined with other roles", v)
		}
		r := byName(part)
		if r == 0 {
			return 0, fmt.Errorf("roles %q: unknown role %q (use all, or api, gateway and worker)", v, part)
		}
		s |= r
	}
	if s == 0 {
		return 0, fmt.Errorf("roles %q: name at least one of api, gateway, worker", v)
	}
	return s, nil
}

func byName(name string) Set {
	for _, n := range names {
		if n.name == name {
			return n.role
		}
	}
	return 0
}

// Has reports whether every role of r is in s.
func (s Set) Has(r Set) bool { return r != 0 && s&r == r }

// All reports whether s holds every role: the single-process installation.
func (s Set) All() bool { return s == All }

// String is "all" or the comma list of the roles.
func (s Set) String() string {
	if s.All() {
		return "all"
	}
	var out []string
	for _, n := range names {
		if s.Has(n.role) {
			out = append(out, n.name)
		}
	}
	return strings.Join(out, ",")
}
