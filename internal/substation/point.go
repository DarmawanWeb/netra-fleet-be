package substation

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// PTZ is the camera pose a robot takes for an inspection point.
type PTZ struct {
	PanDeg  float64 `json:"pan_deg"`
	TiltDeg float64 `json:"tilt_deg"`
	Zoom    float64 `json:"zoom"` // optical zoom factor, 1 is the widest
}

// Settings are the measurement settings of an inspection point.
type Settings struct {
	Reference  string   `json:"reference,omitempty"`  // thermal: auto (default), similar, ambient or both
	Emissivity *float64 `json:"emissivity,omitempty"` // thermal, for the robot's camera: 0.01–1
	DistanceM  *float64 `json:"distance_m,omitempty"` // thermal, for the robot's camera: camera to target
	WatchDB    *float64 `json:"watch_db,omitempty"`   // pd: instead of the site's watch level
	AlarmDB    *float64 `json:"alarm_db,omitempty"`   // pd: instead of the site's alarm level
}

// Levels returns the partial discharge levels of a point: its own, else the site's.
func (s Settings) Levels(site PDLevels) PDLevels {
	if s.WatchDB != nil {
		site.WatchDB = *s.WatchDB
	}
	if s.AlarmDB != nil {
		site.AlarmDB = *s.AlarmDB
	}
	return site
}

// Spec is the definition of an inspection point, without its route binding.
type Spec struct {
	Name     string
	Kind     string
	PTZ      *PTZ
	Settings Settings
	Targets  []Target
}

// MaxTargets is the number of targets a thermal point may have.
const MaxTargets = 24

var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// Check validates a point definition. Asset and route references are left to the caller.
func (sp Spec) Check() error {
	if n := utf8.RuneCountInString(strings.TrimSpace(sp.Name)); n == 0 || n > 120 {
		return fmt.Errorf("name: 1 to 120 characters")
	}
	if !slices.Contains(Kinds, sp.Kind) {
		return fmt.Errorf("kind: thermal or pd")
	}
	if p := sp.PTZ; p != nil {
		switch {
		case !finite(p.PanDeg) || p.PanDeg < -360 || p.PanDeg > 360:
			return fmt.Errorf("ptz.pan_deg: -360 to 360")
		case !finite(p.TiltDeg) || p.TiltDeg < -180 || p.TiltDeg > 180:
			return fmt.Errorf("ptz.tilt_deg: -180 to 180")
		case !finite(p.Zoom) || p.Zoom < 1 || p.Zoom > 100:
			return fmt.Errorf("ptz.zoom: 1 to 100")
		}
	}
	if err := sp.Settings.check(); err != nil {
		return err
	}
	if sp.Kind == KindPD {
		if len(sp.Targets) > 0 {
			return fmt.Errorf("targets: a pd point has none (its reading belongs to the point's asset)")
		}
		return nil
	}
	if len(sp.Targets) == 0 || len(sp.Targets) > MaxTargets {
		return fmt.Errorf("targets: 1 to %d for a thermal point", MaxTargets)
	}
	seen := map[string]bool{}
	for i, t := range sp.Targets {
		at := fmt.Sprintf("targets[%d]", i)
		switch {
		case !keyPattern.MatchString(t.Key):
			return fmt.Errorf("%s.key: a-z, 0-9, _ and -, up to 32, starting with a letter or digit", at)
		case seen[t.Key]:
			return fmt.Errorf("%s.key: %q is used twice", at, t.Key)
		case utf8.RuneCountInString(t.Label) > 80:
			return fmt.Errorf("%s.label: up to 80 characters", at)
		case utf8.RuneCountInString(t.Phase) > 8:
			return fmt.Errorf("%s.phase: up to 8 characters", at)
		case utf8.RuneCountInString(t.Group) > 32:
			return fmt.Errorf("%s.group: up to 32 characters", at)
		case t.LimitC != nil && (!finite(*t.LimitC) || *t.LimitC <= -50 || *t.LimitC > 1500):
			return fmt.Errorf("%s.limit_c: above -50 up to 1500 °C", at)
		}
		if r := t.ROI; r != nil {
			const slack = 1e-9
			if !finite(r.X) || !finite(r.Y) || !finite(r.W) || !finite(r.H) || r.X < 0 || r.Y < 0 || r.W <= 0 ||
				r.H <= 0 || r.X+r.W > 1+slack || r.Y+r.H > 1+slack {
				return fmt.Errorf("%s.roi: x, y, w and h are fractions of the image and the region lies inside it", at)
			}
		}
		seen[t.Key] = true
	}
	return nil
}

func (s Settings) check() error {
	if s.Reference != "" && !slices.Contains(References, s.Reference) {
		return fmt.Errorf("settings.reference: auto, similar, ambient or both")
	}
	if v := s.Emissivity; v != nil && (!finite(*v) || *v < 0.01 || *v > 1) {
		return fmt.Errorf("settings.emissivity: 0.01 to 1")
	}
	if v := s.DistanceM; v != nil && (!finite(*v) || *v <= 0 || *v > 500) {
		return fmt.Errorf("settings.distance_m: above 0 up to 500 m")
	}
	for _, l := range []struct {
		name string
		v    *float64
	}{{"watch_db", s.WatchDB}, {"alarm_db", s.AlarmDB}} {
		if l.v != nil && (!finite(*l.v) || *l.v < -20 || *l.v > 200) {
			return fmt.Errorf("settings.%s: -20 to 200 dB", l.name)
		}
	}
	if s.WatchDB != nil && s.AlarmDB != nil && *s.AlarmDB < *s.WatchDB {
		return fmt.Errorf("settings.alarm_db: at least watch_db")
	}
	return nil
}

// CheckLevels validates site partial discharge levels.
func CheckLevels(l PDLevels) error {
	if !finite(l.WatchDB) || !finite(l.AlarmDB) || l.WatchDB < -20 || l.AlarmDB > 200 || l.AlarmDB < l.WatchDB {
		return fmt.Errorf("pd levels: -20 to 200 dB, alarm_db at least watch_db")
	}
	return nil
}
