package geo

import (
	"errors"
	"fmt"
	"math"
)

// Fit methods.
const (
	MethodPoints  = "points"  // least squares over two or more control points
	MethodHeading = "heading" // one control point and the heading of the map x axis, scale 1
)

// Limits of a fit. The robot map is in metres, so a scale far from 1 means control points whose
// map and earth positions are not the same places; a site spans a few kilometres at most.
const (
	MinScale   = 0.5
	MaxScale   = 2.0
	MaxRadiusM = 20000.0 // how far a control point may be from the centre of all of them
	MaxPoints  = 64
)

// minSpreadM is how far apart control points must be, in either frame, to define a rotation.
const minSpreadM = 0.01

// ErrTooFew means there is nothing to fit from.
var ErrTooFew = errors.New("at least two control points are needed, or one with a heading")

// Fit is a fitted transform and how well it matches its control points.
type Fit struct {
	Transform
	Method    string
	RMS       float64   // root mean square of the residuals, metres
	Residuals []float64 // distance on the ground between each control point and where the transform puts it, metres
}

// Solve fits from two or more points by least squares (the heading is then not used), or from
// one point and the heading of the map x axis (degrees clockwise from north).
func Solve(points []ControlPoint, headingDeg *float64) (Fit, error) {
	switch {
	case len(points) >= 2:
		return FitPoints(points)
	case len(points) == 1 && headingDeg != nil:
		return FitHeading(points[0], *headingDeg)
	default:
		return Fit{}, ErrTooFew
	}
}

// FitPoints fits the similarity that best maps the control points' map positions onto their
// earth positions (least squares in the tangent plane at their centre).
func FitPoints(points []ControlPoint) (Fit, error) {
	if len(points) < 2 {
		return Fit{}, ErrTooFew
	}
	if err := check(points); err != nil {
		return Fit{}, err
	}
	refLat, refLon := centre(points)
	p := newPlane(refLat, refLon)
	n := float64(len(points))
	es, ns := make([]float64, len(points)), make([]float64, len(points))
	var mx, my, me, mn float64
	for i, c := range points {
		es[i], ns[i] = p.local(c.Lat, c.Lon)
		if d := math.Hypot(es[i], ns[i]); d > MaxRadiusM {
			return Fit{}, fmt.Errorf("control point %s is %.1f km from the centre of the control points; they must lie within %.0f km of it",
				name(i, c), d/1000, MaxRadiusM/1000)
		}
		mx, my, me, mn = mx+c.X/n, my+c.Y/n, me+es[i]/n, mn+ns[i]/n
	}
	// Closed form with both sets centred: e' = a·x' − b·y', n' = b·x' + a·y'.
	var sxx, sa, sb, see float64
	for i, c := range points {
		x, y, e, nn := c.X-mx, c.Y-my, es[i]-me, ns[i]-mn
		sxx += x*x + y*y
		see += e*e + nn*nn
		sa += x*e + y*nn
		sb += x*nn - y*e
	}
	if math.Sqrt(sxx/n) < minSpreadM {
		return Fit{}, errors.New("the control points are all at the same map position")
	}
	if math.Sqrt(see/n) < minSpreadM {
		return Fit{}, errors.New("the control points all have the same latitude and longitude")
	}
	a, b := sa/sxx, sb/sxx
	scale := math.Hypot(a, b)
	if scale < MinScale || scale > MaxScale {
		return Fit{}, fmt.Errorf("the control points give a scale of %.2f ground metres per map metre; the robot map is in "+
			"metres, so check that each point's map position and latitude/longitude are the same place", scale)
	}
	t := Transform{RefLat: refLat, RefLon: refLon, Scale: scale, Rotation: math.Atan2(b, a),
		East: me - (a*mx - b*my), North: mn - (b*mx + a*my)}
	return withResiduals(t, MethodPoints, points), nil
}

// FitHeading places the map with one control point and the compass heading of the map x axis
// (degrees clockwise from north); the scale is 1.
func FitHeading(c ControlPoint, headingDeg float64) (Fit, error) {
	if err := check([]ControlPoint{c}); err != nil {
		return Fit{}, err
	}
	if math.IsNaN(headingDeg) || math.IsInf(headingDeg, 0) {
		return Fit{}, errors.New("the heading must be a number of degrees")
	}
	rot := (90 - normDeg(headingDeg)) * deg
	cr, sr := math.Cos(rot), math.Sin(rot)
	t := Transform{RefLat: c.Lat, RefLon: c.Lon, Scale: 1, Rotation: math.Atan2(sr, cr),
		East: -(cr*c.X - sr*c.Y), North: -(sr*c.X + cr*c.Y)}
	return withResiduals(t, MethodHeading, []ControlPoint{c}), nil
}

// Residuals is the ground distance (metres) between each control point and where t puts its
// map position.
func Residuals(t Transform, points []ControlPoint) []float64 {
	p := newPlane(t.RefLat, t.RefLon)
	out := make([]float64, len(points))
	for i, c := range points {
		e, n := p.local(c.Lat, c.Lon)
		pe, pn := t.local(c.X, c.Y)
		out[i] = math.Hypot(pe-e, pn-n)
	}
	return out
}

// RMS is the root mean square of residuals.
func RMS(residuals []float64) float64 {
	if len(residuals) == 0 {
		return 0
	}
	var sum float64
	for _, r := range residuals {
		sum += r * r
	}
	return math.Sqrt(sum / float64(len(residuals)))
}

func withResiduals(t Transform, method string, points []ControlPoint) Fit {
	res := Residuals(t, points)
	return Fit{Transform: t, Method: method, RMS: RMS(res), Residuals: res}
}

// check refuses positions that are not numbers or not on the earth.
func check(points []ControlPoint) error {
	if len(points) > MaxPoints {
		return fmt.Errorf("at most %d control points", MaxPoints)
	}
	for i, c := range points {
		for _, v := range []float64{c.X, c.Y, c.Lat, c.Lon} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("control point %s: every position must be a number", name(i, c))
			}
		}
		if c.Lat < -90 || c.Lat > 90 {
			return fmt.Errorf("control point %s: latitude %.6f is not between -90 and 90", name(i, c), c.Lat)
		}
		if c.Lon < -180 || c.Lon > 180 {
			return fmt.Errorf("control point %s: longitude %.6f is not between -180 and 180", name(i, c), c.Lon)
		}
	}
	return nil
}

// centre is the point of the ellipsoid under the mean of the points, which also works across
// the antimeridian.
func centre(points []ControlPoint) (lat, lon float64) {
	var sx, sy, sz float64
	n := float64(len(points))
	for _, c := range points {
		x, y, z := ecef(c.Lat, c.Lon, 0)
		sx, sy, sz = sx+x/n, sy+y/n, sz+z/n
	}
	lat, lon, _ = fromECEF(sx, sy, sz)
	return lat, lon
}

func name(i int, c ControlPoint) string {
	if c.Label != "" {
		return fmt.Sprintf("%d (%s)", i+1, c.Label)
	}
	return fmt.Sprint(i + 1)
}
