// Package substation evaluates substation inspections: the priority of a thermal reading after
// ANSI/NETA MTS Table 100.18 (temperature rise over a similar component or over the ambient air),
// a component's rated temperature, and the level of a partial discharge reading. It also checks
// the definition of an inspection point. It has no database or HTTP code.
package substation

import (
	"math"
)

// Kinds of inspection point.
const (
	KindThermal = "thermal" // a radiometric thermal image: the hottest temperature of each target
	KindPD      = "pd"      // an acoustic camera: the partial discharge level
)

// Kinds lists the inspection point kinds.
var Kinds = []string{KindThermal, KindPD}

// AssetKinds are the kinds of substation equipment an asset can be.
var AssetKinds = []string{"transformer", "breaker", "disconnector", "current_transformer", "voltage_transformer",
	"surge_arrester", "busbar", "bushing", "connector", "cable_termination", "capacitor", "reactor", "insulator",
	"other"}

// Priorities run from 1, the most severe, to 4; None means no deficiency.
const (
	None = 0
	P1   = 1 // major discrepancy; repair immediately
	P2   = 2 // monitor until corrective measures can be accomplished
	P3   = 3 // indicates probable deficiency; repair as time permits
	P4   = 4 // possible deficiency; warrants investigation
)

// Bases of a priority: the comparison that gave it.
const (
	BasisSimilar = "similar" // the rise over a similar component under similar load
	BasisAmbient = "ambient" // the rise over the ambient air
	BasisLimit   = "limit"   // at or above the component's rated temperature
	BasisPD      = "pd"      // the partial discharge level
)

// References: which comparisons rate a thermal point.
const (
	RefAuto    = "auto"    // a similar component when one was read, otherwise the ambient air
	RefSimilar = "similar" // similar components only
	RefAmbient = "ambient" // the ambient air only
	RefBoth    = "both"    // both, the more severe counts
)

// References lists the reference choices.
var References = []string{RefAuto, RefSimilar, RefAmbient, RefBoth}

// Criterion is one row of NETA MTS Table 100.18, for display.
type Criterion struct {
	Priority int    `json:"priority"`
	Similar  string `json:"similar"` // rise over a similar component; empty when the row has none
	Ambient  string `json:"ambient"` // rise over the ambient air
	Action   string `json:"action"`
}

// Criteria is NETA MTS Table 100.18, "Thermographic Survey Suggested Actions Based on Temperature
// Rise", the most severe first.
var Criteria = []Criterion{
	{P1, "> 15 °C", "> 40 °C", "Major discrepancy; repair immediately"},
	{P2, "", "21–40 °C", "Monitor until corrective measures can be accomplished"},
	{P3, "4–15 °C", "11–20 °C", "Indicates probable deficiency; repair as time permits"},
	{P4, "1–3 °C", "1–10 °C", "Possible deficiency; warrants investigation"},
}

// SimilarPriority rates a temperature rise over a similar component under similar load. A band
// starts at its lower bound, so a rise between two rows of the table (3.5 °C) takes the less
// severe one.
func SimilarPriority(dt float64) int {
	switch {
	case dt > 15:
		return P1
	case dt >= 4:
		return P3
	case dt >= 1:
		return P4
	}
	return None
}

// AmbientPriority rates a temperature rise over the ambient air.
func AmbientPriority(dt float64) int {
	switch {
	case dt > 40:
		return P1
	case dt >= 21:
		return P2
	case dt >= 11:
		return P3
	case dt >= 1:
		return P4
	}
	return None
}

// Worse returns the more severe of two priorities.
func Worse(a, b int) int {
	if a == None || (b != None && b < a) {
		return b
	}
	return a
}

// Rect is a region of an image as fractions of its width and height, from the top left corner.
type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Target is one component measured at a thermal inspection point.
type Target struct {
	Key     string   `json:"key"`      // unique in the point: a-z, 0-9, _ and -, up to 32
	Label   string   `json:"label"`    // e.g. "Clamp phase R"
	AssetID *int64   `json:"asset_id"` // the asset it belongs to
	Phase   string   `json:"phase"`    // R, S, T, N or another label
	Group   string   `json:"group"`    // similar components under similar load, compared with each other
	ROI     *Rect    `json:"roi"`      // where it is in the thermal image; the robot measures inside
	LimitC  *float64 `json:"limit_c"`  // its rated temperature: at or above it, priority 1
}

// Reading is what a robot measured on one target, in °C.
type Reading struct {
	Key  string   `json:"key"`
	MaxC float64  `json:"max_c"`
	AvgC *float64 `json:"avg_c,omitempty"`
	MinC *float64 `json:"min_c,omitempty"`
}

// Result is a thermal reading with its evaluation.
type Result struct {
	Reading
	Label         string   `json:"label"`
	AssetID       *int64   `json:"asset_id"`
	RefKey        string   `json:"ref_key,omitempty"` // the similar component compared with
	DeltaSimilarC *float64 `json:"delta_similar_c"`
	DeltaAmbientC *float64 `json:"delta_ambient_c"`
	Priority      int      `json:"priority"`
	Basis         string   `json:"basis,omitempty"`
}

func (r *Result) rate(p int, basis string) {
	if p != None && (r.Priority == None || p < r.Priority) {
		r.Priority, r.Basis = p, basis
	}
}

// EvaluateThermal rates the readings of a thermal inspection point.
//
// A target of a group is compared with the coolest other member of its group read in the same
// capture; every target is compared with the ambient air when the robot reported it. Both rises
// are kept, rounded to 0.1 °C, and the reference decides which of them rate the target (auto: the
// similar component when there is one, otherwise the ambient air). A reading at or above the
// target's rated temperature is priority 1 whatever the reference. Readings of keys the point
// does not define, and repeated keys, are left out.
func EvaluateThermal(targets []Target, readings []Reading, ambientC *float64, reference string) []Result {
	byKey := map[string]Reading{}
	for _, r := range readings {
		if _, dup := byKey[r.Key]; !dup && finite(r.MaxC) {
			byKey[r.Key] = r
		}
	}
	if ambientC != nil && !finite(*ambientC) {
		ambientC = nil
	}
	out := []Result{}
	for _, t := range targets {
		r, ok := byKey[t.Key]
		if !ok {
			continue
		}
		res := Result{Reading: r, Label: t.Label, AssetID: t.AssetID}
		if t.Group != "" {
			ref, refKey := math.Inf(1), ""
			for _, o := range targets {
				if o.Key == t.Key || o.Group != t.Group {
					continue
				}
				if or, ok := byKey[o.Key]; ok && or.MaxC < ref {
					ref, refKey = or.MaxC, o.Key
				}
			}
			if refKey != "" {
				d := round1(r.MaxC - ref)
				res.DeltaSimilarC, res.RefKey = &d, refKey
			}
		}
		if ambientC != nil {
			d := round1(r.MaxC - *ambientC)
			res.DeltaAmbientC = &d
		}
		useSimilar := res.DeltaSimilarC != nil && reference != RefAmbient
		useAmbient := res.DeltaAmbientC != nil && (reference == RefAmbient || reference == RefBoth ||
			(reference != RefSimilar && res.DeltaSimilarC == nil))
		if useSimilar {
			res.rate(SimilarPriority(*res.DeltaSimilarC), BasisSimilar)
		}
		if useAmbient {
			res.rate(AmbientPriority(*res.DeltaAmbientC), BasisAmbient)
		}
		if t.LimitC != nil && r.MaxC >= *t.LimitC {
			res.rate(P1, BasisLimit)
		}
		out = append(out, res)
	}
	return out
}

// PDReading is what a robot's acoustic camera measured at a partial discharge point.
type PDReading struct {
	DB       float64 `json:"db"`
	Detected bool    `json:"detected"`
	Kind     string  `json:"kind,omitempty"` // corona, surface, floating, internal or unknown, when the camera tells
}

// PDLevels are the levels of a partial discharge reading, in the acoustic camera's dB.
type PDLevels struct {
	WatchDB float64 `json:"watch_db"`
	AlarmDB float64 `json:"alarm_db"`
}

// DefaultPDLevels are the site levels until an administrator sets them.
var DefaultPDLevels = PDLevels{WatchDB: 10, AlarmDB: 20}

// PDPriority rates a partial discharge reading: nothing without a discharge; a discharge below
// the watch level 4, from the watch level 3, from the alarm level 2. The scale is NETRA's own: no
// standard sets acoustic levels, so they are set per site and instrument.
func PDPriority(r PDReading, l PDLevels) int {
	switch {
	case !r.Detected || !finite(r.DB):
		return None
	case r.DB >= l.AlarmDB:
		return P2
	case r.DB >= l.WatchDB:
		return P3
	}
	return P4
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func round1(v float64) float64 { return math.Round(v*10) / 10 }
