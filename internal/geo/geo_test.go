package geo

import (
	"math"
	"strings"
	"testing"
)

// site is a transform near Jakarta: the map rotated 35° from east, slightly off scale.
var site = Transform{RefLat: -6.2, RefLon: 106.8, Scale: 1.003, Rotation: 35 * deg, East: 41.5, North: -17.25}

// groundDistance is the distance on the ground (metres) between two WGS84 positions.
func groundDistance(lat1, lon1, lat2, lon2 float64) float64 {
	e, n := newPlane(lat1, lon1).local(lat2, lon2)
	return math.Hypot(e, n)
}

func points(t Transform, xy ...[2]float64) []ControlPoint {
	out := make([]ControlPoint, len(xy))
	for i, p := range xy {
		lat, lon := t.ToLatLon(p[0], p[1])
		out[i] = ControlPoint{X: p[0], Y: p[1], Lat: lat, Lon: lon}
	}
	return out
}

func TestRoundTrip(t *testing.T) {
	for _, p := range [][2]float64{{0, 0}, {120.5, -33.2}, {-800, 950}, {3000, 2500}} {
		lat, lon := site.ToLatLon(p[0], p[1])
		x, y := site.ToMap(lat, lon)
		if math.Abs(x-p[0]) > 1e-6 || math.Abs(y-p[1]) > 1e-6 {
			t.Errorf("%v -> %.8f,%.8f -> %.9f,%.9f", p, lat, lon, x, y)
		}
	}
}

func TestDistancesOnTheGround(t *testing.T) {
	// 100 m along the map x axis is 100.3 m on the ground (scale 1.003), at 35° from east.
	lat0, lon0 := site.ToLatLon(0, 0)
	lat1, lon1 := site.ToLatLon(100, 0)
	if d := groundDistance(lat0, lon0, lat1, lon1); math.Abs(d-100.3) > 1e-3 {
		t.Errorf("distance %.4f m, want 100.3", d)
	}
	e, n := newPlane(lat0, lon0).local(lat1, lon1)
	if a := math.Atan2(n, e) / deg; math.Abs(a-35) > 1e-3 {
		t.Errorf("direction %.4f° from east, want 35", a)
	}
	// A thousandth of a degree of latitude at the equator is 110.574 m (WGS84 meridian radius).
	if d := groundDistance(0, 0, 0.001, 0); math.Abs(d-wgsA*(1-wgsE2)*0.001*deg) > 1e-3 {
		t.Errorf("0.001° of latitude: %.4f m", d)
	}
	if h := site.HeadingDeg(0); math.Abs(h-55) > 1e-9 {
		t.Errorf("heading of the map x axis %.6f, want 55", h)
	}
}

func TestFitRecoversTheTransform(t *testing.T) {
	cps := points(site, [2]float64{0, 0}, [2]float64{80, 5}, [2]float64{40, 60}, [2]float64{-15, 25})
	fit, err := FitPoints(cps)
	if err != nil {
		t.Fatal(err)
	}
	if fit.Method != MethodPoints || math.Abs(fit.Scale-site.Scale) > 1e-6 || math.Abs(fit.Rotation-site.Rotation) > 1e-5 {
		t.Fatalf("fit %+v, want scale %v rotation %v", fit.Transform, site.Scale, site.Rotation)
	}
	// The fit works in the tangent plane at the points' centre, not at the site's reference: the
	// two planes agree to micrometres over a site.
	if fit.RMS > 1e-4 || len(fit.Residuals) != len(cps) {
		t.Fatalf("rms %g residuals %v", fit.RMS, fit.Residuals)
	}
	// Same places on the ground, also away from the control points.
	for _, p := range [][2]float64{{0, 0}, {200, -150}, {-300, 400}} {
		la, lo := site.ToLatLon(p[0], p[1])
		lb, lob := fit.ToLatLon(p[0], p[1])
		if d := groundDistance(la, lo, lb, lob); d > 1e-3 {
			t.Errorf("%v off by %.6f m", p, d)
		}
	}
}

func TestFitReportsResiduals(t *testing.T) {
	cps := points(site, [2]float64{0, 0}, [2]float64{100, 0}, [2]float64{100, 100}, [2]float64{0, 100})
	// Move point 3 two metres north on the ground.
	e, n := newPlane(site.RefLat, site.RefLon).local(cps[2].Lat, cps[2].Lon)
	cps[2].Lat, cps[2].Lon = newPlane(site.RefLat, site.RefLon).geodetic(e, n+2)
	fit, err := FitPoints(cps)
	if err != nil {
		t.Fatal(err)
	}
	// Least squares spreads the error over the square: half of it stays on the moved point,
	// √2/4 on its neighbours, none on the opposite corner.
	want := []float64{0, math.Sqrt2 / 2, 1, math.Sqrt2 / 2}
	for i, r := range fit.Residuals {
		if math.Abs(r-want[i]) > 0.005 {
			t.Fatalf("residuals %v, want %v", fit.Residuals, want)
		}
	}
	if math.Abs(fit.RMS-math.Sqrt(0.5)) > 0.005 {
		t.Fatalf("rms %.4f", fit.RMS)
	}
	if again := RMS(Residuals(fit.Transform, cps)); math.Abs(again-fit.RMS) > 1e-9 {
		t.Fatalf("Residuals %.6f differs from the fit %.6f", again, fit.RMS)
	}
}

func TestFitHeading(t *testing.T) {
	gate := ControlPoint{Label: "gate", X: 10, Y: 0, Lat: -6.25, Lon: 106.85}
	for _, c := range []struct {
		heading    float64
		east, nrth float64 // ground direction of +1 m along map x
	}{
		{90, 1, 0}, {0, 0, 1}, {180, 0, -1}, {-90, -1, 0}, {450, 1, 0},
	} {
		fit, err := FitHeading(gate, c.heading)
		if err != nil {
			t.Fatal(err)
		}
		lat, lon := fit.ToLatLon(gate.X, gate.Y)
		if d := groundDistance(lat, lon, gate.Lat, gate.Lon); d > 1e-6 || fit.Scale != 1 || fit.Method != MethodHeading {
			t.Fatalf("heading %v: the point lands %.9f m away, scale %v", c.heading, d, fit.Scale)
		}
		lat2, lon2 := fit.ToLatLon(gate.X+1, gate.Y)
		e, n := newPlane(gate.Lat, gate.Lon).local(lat2, lon2)
		if math.Abs(e-c.east) > 1e-6 || math.Abs(n-c.nrth) > 1e-6 {
			t.Errorf("heading %v: +x goes %.6f east %.6f north", c.heading, e, n)
		}
		if h := fit.HeadingDeg(0); math.Abs(h-normDeg(c.heading)) > 1e-9 {
			t.Errorf("heading %v: HeadingDeg %v", c.heading, h)
		}
	}
}

func TestSolve(t *testing.T) {
	two := points(site, [2]float64{0, 0}, [2]float64{50, 0})
	heading := 30.0
	if f, err := Solve(two, &heading); err != nil || f.Method != MethodPoints {
		t.Fatalf("two points: %v %v", f.Method, err)
	}
	if f, err := Solve(two[:1], &heading); err != nil || f.Method != MethodHeading {
		t.Fatalf("one point and a heading: %v %v", f.Method, err)
	}
	for _, c := range []struct {
		pts     []ControlPoint
		heading *float64
	}{{nil, nil}, {nil, &heading}, {two[:1], nil}} {
		if _, err := Solve(c.pts, c.heading); err != ErrTooFew {
			t.Errorf("%d points: %v", len(c.pts), err)
		}
	}
}

func TestDegenerateInput(t *testing.T) {
	far, farLon := site.ToLatLon(50000, 0)
	cases := map[string][]ControlPoint{
		"same map position":               {{X: 1, Y: 1, Lat: -6.2, Lon: 106.8}, {X: 1, Y: 1, Lat: -6.2005, Lon: 106.8}},
		"same latitude and longitude":     {{X: 0, Y: 0, Lat: -6.2, Lon: 106.8}, {X: 30, Y: 0, Lat: -6.2, Lon: 106.8}},
		"scale of 9.":                     {{X: 0, Y: 0, Lat: -6.2, Lon: 106.8}, {X: 10, Y: 0, Lat: -6.2, Lon: 106.80082}},
		"latitude 100.000000 is not":      {{X: 0, Y: 0, Lat: 100, Lon: 106.8}, {X: 30, Y: 0, Lat: -6.2, Lon: 106.8}},
		"longitude 190.000000 is not":     {{X: 0, Y: 0, Lat: -6.2, Lon: 190}, {X: 30, Y: 0, Lat: -6.2, Lon: 106.8}},
		"every position must be a number": {{X: math.NaN(), Y: 0, Lat: -6.2, Lon: 106.8}, {X: 30, Y: 0, Lat: -6.2, Lon: 106.8}},
		"within 20 km":                    {{X: 0, Y: 0, Lat: -6.2, Lon: 106.8}, {X: 50000, Y: 0, Lat: far, Lon: farLon}},
		"at least two":                    {{X: 0, Y: 0, Lat: -6.2, Lon: 106.8}},
	}
	for want, cps := range cases {
		if _, err := FitPoints(cps); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	if _, err := FitHeading(ControlPoint{Lat: -6.2, Lon: 106.8}, math.Inf(1)); err == nil {
		t.Error("infinite heading accepted")
	}
	if _, err := FitPoints(make([]ControlPoint, MaxPoints+1)); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("too many points: %v", err)
	}
}

func TestAcrossTheAntimeridian(t *testing.T) {
	east := Transform{RefLat: -17.7, RefLon: 179.9995, Scale: 1, Rotation: 0}
	cps := points(east, [2]float64{0, 0}, [2]float64{120, 0}, [2]float64{0, 80})
	if cps[1].Lon > 0 {
		t.Fatalf("test points should cross the antimeridian: %v", cps)
	}
	fit, err := FitPoints(cps)
	if err != nil || fit.RMS > 1e-4 || math.Abs(fit.Rotation) > 1e-5 || math.Abs(fit.Scale-1) > 1e-6 {
		t.Fatalf("fit %+v rms %g: %v", fit.Transform, fit.RMS, err)
	}
	if d := groundDistance(fit.RefLat, fit.RefLon, -17.7, 179.9995); d > 150 {
		t.Fatalf("reference %.6f,%.6f is %.0f m from the points", fit.RefLat, fit.RefLon, d)
	}
}
