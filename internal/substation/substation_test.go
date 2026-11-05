package substation

import (
	"math"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestNETABands(t *testing.T) {
	similar := []struct {
		dt   float64
		want int
	}{
		{-3, None}, {0, None}, {0.9, None}, {1, P4}, {3, P4}, {3.5, P4}, {4, P3}, {10, P3}, {15, P3},
		{15.1, P1}, {60, P1}, {math.NaN(), None},
	}
	for _, c := range similar {
		if got := SimilarPriority(c.dt); got != c.want {
			t.Errorf("similar %.1f °C: priority %d, want %d", c.dt, got, c.want)
		}
	}
	ambient := []struct {
		dt   float64
		want int
	}{
		{0.5, None}, {1, P4}, {10, P4}, {10.9, P4}, {11, P3}, {20, P3}, {20.9, P3}, {21, P2}, {40, P2},
		{40.1, P1}, {math.Inf(1), P1},
	}
	for _, c := range ambient {
		if got := AmbientPriority(c.dt); got != c.want {
			t.Errorf("ambient %.1f °C: priority %d, want %d", c.dt, got, c.want)
		}
	}
	if len(Criteria) != 4 || Criteria[0].Priority != P1 || Criteria[1].Similar != "" || Criteria[3].Ambient != "1–10 °C" {
		t.Errorf("criteria table: %+v", Criteria)
	}
}

func TestWorse(t *testing.T) {
	for _, c := range [][3]int{{None, None, None}, {None, P3, P3}, {P4, None, P4}, {P2, P3, P2}, {P4, P1, P1}} {
		if got := Worse(c[0], c[1]); got != c[2] {
			t.Errorf("Worse(%d, %d) = %d, want %d", c[0], c[1], got, c[2])
		}
	}
}

// threePhase is a disconnector's three clamps and a bushing outside any group with a rated limit.
var threePhase = []Target{
	{Key: "r", Label: "Clamp R", Phase: "R", Group: "clamp", AssetID: ptr(int64(7))},
	{Key: "s", Label: "Clamp S", Phase: "S", Group: "clamp", AssetID: ptr(int64(7))},
	{Key: "t", Label: "Clamp T", Phase: "T", Group: "clamp", AssetID: ptr(int64(7))},
	{Key: "bushing", Label: "Bushing", LimitC: ptr(90.0)},
}

func byKey(rs []Result) map[string]Result {
	out := map[string]Result{}
	for _, r := range rs {
		out[r.Key] = r
	}
	return out
}

func TestEvaluateThermalAuto(t *testing.T) {
	readings := []Reading{{Key: "r", MaxC: 41.2}, {Key: "s", MaxC: 42.0}, {Key: "t", MaxC: 59.3},
		{Key: "bushing", MaxC: 52}, {Key: "unknown", MaxC: 99}, {Key: "r", MaxC: 300}}
	got := byKey(EvaluateThermal(threePhase, readings, ptr(31.0), RefAuto))
	if len(got) != 4 {
		t.Fatalf("results %v, want the four defined targets once", got)
	}
	// T against the coolest other phase (R): 18.1 °C, over 15: priority 1.
	if r := got["t"]; r.RefKey != "r" || *r.DeltaSimilarC != 18.1 || r.Priority != P1 || r.Basis != BasisSimilar ||
		*r.DeltaAmbientC != 28.3 || r.Label != "Clamp T" || *r.AssetID != 7 {
		t.Errorf("phase T: %+v", r)
	}
	// R against S: cooler, nothing; with a similar component the ambient rise (10.2) does not rate it.
	if r := got["r"]; r.RefKey != "s" || *r.DeltaSimilarC != -0.8 || r.Priority != None || r.Basis != "" {
		t.Errorf("phase R: %+v", r)
	}
	// S against R: 0.8, under 1 °C.
	if r := got["s"]; *r.DeltaSimilarC != 0.8 || r.Priority != None {
		t.Errorf("phase S: %+v", r)
	}
	// No similar component: the ambient rise (21) rates it, priority 2.
	if r := got["bushing"]; r.DeltaSimilarC != nil || *r.DeltaAmbientC != 21 || r.Priority != P2 || r.Basis != BasisAmbient {
		t.Errorf("bushing: %+v", r)
	}
}

func TestEvaluateThermalReferences(t *testing.T) {
	readings := []Reading{{Key: "r", MaxC: 40}, {Key: "s", MaxC: 46}, {Key: "t", MaxC: 41}, {Key: "bushing", MaxC: 95}}
	cases := []struct {
		ref     string
		ambient *float64
		key     string
		want    int
		basis   string
	}{
		{RefAuto, ptr(20.0), "s", P3, BasisSimilar},        // 6 over R
		{RefSimilar, ptr(20.0), "s", P3, BasisSimilar},     // ambient ignored
		{RefAmbient, ptr(20.0), "s", P2, BasisAmbient},     // 26 over ambient
		{RefBoth, ptr(20.0), "s", P2, BasisAmbient},        // the worse of P3 and P2
		{RefBoth, ptr(30.0), "s", P3, BasisSimilar},        // 16 over ambient is P3 too: the first basis stays
		{RefAmbient, nil, "s", None, ""},                   // no ambient, nothing to compare with
		{RefSimilar, ptr(20.0), "bushing", P1, BasisLimit}, // 95 at or above its 90 °C rating
		{RefAuto, nil, "bushing", P1, BasisLimit},
	}
	for _, c := range cases {
		r := byKey(EvaluateThermal(threePhase, readings, c.ambient, c.ref))[c.key]
		if r.Priority != c.want || r.Basis != c.basis {
			t.Errorf("%s, ambient %v, %s: priority %d basis %q, want %d %q", c.ref, c.ambient, c.key, r.Priority, r.Basis,
				c.want, c.basis)
		}
	}
	// One phase read only: no similar comparison, auto falls back to the ambient air.
	r := EvaluateThermal(threePhase, []Reading{{Key: "t", MaxC: 45}}, ptr(30.0), RefAuto)
	if len(r) != 1 || r[0].DeltaSimilarC != nil || r[0].Priority != P3 || r[0].Basis != BasisAmbient {
		t.Errorf("single phase: %+v", r)
	}
	// Readings that are not numbers are left out, and so is an ambient that is not.
	r = EvaluateThermal(threePhase, []Reading{{Key: "r", MaxC: math.NaN()}, {Key: "s", MaxC: 50}}, ptr(math.Inf(1)), RefBoth)
	if len(r) != 1 || r[0].Key != "s" || r[0].DeltaAmbientC != nil || r[0].DeltaSimilarC != nil || r[0].Priority != None {
		t.Errorf("not numbers: %+v", r)
	}
}

func TestPDPriority(t *testing.T) {
	levels := PDLevels{WatchDB: 10, AlarmDB: 20}
	cases := []struct {
		r    PDReading
		want int
	}{
		{PDReading{DB: 35, Detected: false}, None},
		{PDReading{DB: 4, Detected: true}, P4},
		{PDReading{DB: 10, Detected: true}, P3},
		{PDReading{DB: 19.9, Detected: true}, P3},
		{PDReading{DB: 20, Detected: true, Kind: "corona"}, P2},
		{PDReading{DB: math.NaN(), Detected: true}, None},
	}
	for _, c := range cases {
		if got := PDPriority(c.r, levels); got != c.want {
			t.Errorf("%+v: priority %d, want %d", c.r, got, c.want)
		}
	}
	own := Settings{AlarmDB: ptr(30.0)}.Levels(levels)
	if own.WatchDB != 10 || own.AlarmDB != 30 || PDPriority(PDReading{DB: 25, Detected: true}, own) != P3 {
		t.Errorf("point levels: %+v", own)
	}
}

func TestSpecCheck(t *testing.T) {
	good := Spec{Name: "Bay 1 disconnector", Kind: KindThermal, PTZ: &PTZ{PanDeg: -45, TiltDeg: 10, Zoom: 4},
		Settings: Settings{Reference: RefAuto, Emissivity: ptr(0.95)},
		Targets: []Target{{Key: "r", Group: "clamp", ROI: &Rect{X: 0.1, Y: 0.2, W: 0.2, H: 0.3}},
			{Key: "s-2", Group: "clamp", ROI: &Rect{X: 0.8, Y: 0.7, W: 0.2, H: 0.3}, LimitC: ptr(90.0)}}}
	if err := good.Check(); err != nil {
		t.Fatalf("valid point refused: %v", err)
	}
	pd := Spec{Name: "Bushing PD", Kind: KindPD, Settings: Settings{WatchDB: ptr(12.0), AlarmDB: ptr(25.0)}}
	if err := pd.Check(); err != nil {
		t.Fatalf("valid pd point refused: %v", err)
	}
	bad := map[string]func(*Spec){
		"name":         func(s *Spec) { s.Name = "  " },
		"kind":         func(s *Spec) { s.Kind = "gauge" },
		"ptz.zoom":     func(s *Spec) { s.PTZ.Zoom = 0 },
		"ptz.pan_deg":  func(s *Spec) { s.PTZ.PanDeg = math.NaN() },
		"reference":    func(s *Spec) { s.Settings.Reference = "phase" },
		"emissivity":   func(s *Spec) { s.Settings.Emissivity = ptr(1.2) },
		"alarm_db":     func(s *Spec) { s.Settings.WatchDB, s.Settings.AlarmDB = ptr(20.0), ptr(10.0) },
		"targets: 1":   func(s *Spec) { s.Targets = nil },
		"key":          func(s *Spec) { s.Targets[0].Key = "Phase R" },
		"used twice":   func(s *Spec) { s.Targets[1].Key = "r" },
		"roi":          func(s *Spec) { s.Targets[1].ROI.W = 0.3 },
		"limit_c":      func(s *Spec) { s.Targets[0].LimitC = ptr(-60.0) },
		"group":        func(s *Spec) { s.Targets[0].Group = strings.Repeat("g", 33) },
		"a pd point":   func(s *Spec) { s.Kind = KindPD },
		"targets[0].k": func(s *Spec) { s.Targets[0].Key = "" },
	}
	for want, change := range bad {
		s := good
		s.PTZ = &PTZ{PanDeg: -45, TiltDeg: 10, Zoom: 4}
		s.Targets = []Target{good.Targets[0], good.Targets[1]}
		r0, r1 := *good.Targets[0].ROI, *good.Targets[1].ROI
		s.Targets[0].ROI, s.Targets[1].ROI = &r0, &r1
		change(&s)
		err := s.Check()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
	if CheckLevels(DefaultPDLevels) != nil || CheckLevels(PDLevels{WatchDB: 30, AlarmDB: 20}) == nil {
		t.Error("site levels")
	}
}
