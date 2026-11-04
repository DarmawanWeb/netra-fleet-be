// Package geo places the robot map frame on the earth. A Transform is a 2D similarity (scale,
// rotation, translation) from map metres to east/north metres in the tangent plane at a reference
// latitude and longitude, and from that plane onto the WGS84 ellipsoid. It is fitted from control
// points, places known in both frames, by least squares, or from one point and a heading.
package geo

import "math"

// WGS84 ellipsoid.
const (
	wgsA  = 6378137.0
	wgsF  = 1 / 298.257223563
	wgsE2 = wgsF * (2 - wgsF)
)

const deg = math.Pi / 180

// ControlPoint is one place known in both frames: robot map metres and WGS84 degrees.
type ControlPoint struct {
	Label string  `json:"label"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
}

// Transform maps the robot map frame to WGS84:
//
//	east  = scale·(cos θ·x − sin θ·y) + east0
//	north = scale·(sin θ·x + cos θ·y) + north0
//
// in the tangent plane at (RefLat, RefLon), then onto the ellipsoid along its normal.
type Transform struct {
	RefLat   float64 `json:"ref_lat"`
	RefLon   float64 `json:"ref_lon"`
	Scale    float64 `json:"scale"`
	Rotation float64 `json:"rotation_rad"` // counter-clockwise from east to the map x axis
	East     float64 `json:"east_m"`       // the map origin in the tangent plane
	North    float64 `json:"north_m"`
}

// ToLatLon converts a map position (metres) to WGS84 degrees.
func (t Transform) ToLatLon(x, y float64) (lat, lon float64) {
	e, n := t.local(x, y)
	return newPlane(t.RefLat, t.RefLon).geodetic(e, n)
}

// ToMap converts WGS84 degrees to a map position (metres).
func (t Transform) ToMap(lat, lon float64) (x, y float64) {
	e, n := newPlane(t.RefLat, t.RefLon).local(lat, lon)
	c, s := math.Cos(t.Rotation), math.Sin(t.Rotation)
	de, dn := (e-t.East)/t.Scale, (n-t.North)/t.Scale
	return c*de + s*dn, -s*de + c*dn
}

// HeadingDeg is the compass heading (degrees clockwise from north, 0 to 360) of a map yaw
// (radians counter-clockwise from the map x axis).
func (t Transform) HeadingDeg(yaw float64) float64 {
	return normDeg(90 - (t.Rotation+yaw)/deg)
}

// local is the tangent-plane position of a map position.
func (t Transform) local(x, y float64) (e, n float64) {
	c, s := math.Cos(t.Rotation), math.Sin(t.Rotation)
	return t.Scale*(c*x-s*y) + t.East, t.Scale*(s*x+c*y) + t.North
}

func normDeg(d float64) float64 {
	d = math.Mod(d, 360)
	if d < 0 {
		d += 360
	}
	return d
}

// plane is the east/north/up tangent plane at a point of the ellipsoid.
type plane struct {
	sinLat, cosLat, sinLon, cosLon float64
	x0, y0, z0                     float64 // the point, earth-centred earth-fixed
}

func newPlane(lat, lon float64) plane {
	p := plane{sinLat: math.Sin(lat * deg), cosLat: math.Cos(lat * deg), sinLon: math.Sin(lon * deg), cosLon: math.Cos(lon * deg)}
	p.x0, p.y0, p.z0 = ecef(lat, lon, 0)
	return p
}

// local is the east/north position of a point of the ellipsoid surface.
func (p plane) local(lat, lon float64) (e, n float64) {
	x, y, z := ecef(lat, lon, 0)
	dx, dy, dz := x-p.x0, y-p.y0, z-p.z0
	e = -p.sinLon*dx + p.cosLon*dy
	n = -p.sinLat*p.cosLon*dx - p.sinLat*p.sinLon*dy + p.cosLat*dz
	return e, n
}

// geodetic is the point of the ellipsoid surface whose east/north position is (e, n): the plane
// point is moved along the local vertical until its height is zero, so local undoes it exactly.
func (p plane) geodetic(e, n float64) (lat, lon float64) {
	u := 0.0
	for i := 0; i < 5; i++ {
		x := p.x0 - p.sinLon*e - p.sinLat*p.cosLon*n + p.cosLat*p.cosLon*u
		y := p.y0 + p.cosLon*e - p.sinLat*p.sinLon*n + p.cosLat*p.sinLon*u
		z := p.z0 + p.cosLat*n + p.sinLat*u
		var h float64
		lat, lon, h = fromECEF(x, y, z)
		if math.Abs(h) < 1e-9 {
			break
		}
		u -= h
	}
	return lat, lon
}

// ecef converts geodetic degrees and height (metres) to earth-centred earth-fixed metres.
func ecef(lat, lon, h float64) (x, y, z float64) {
	sinLat, cosLat := math.Sin(lat*deg), math.Cos(lat*deg)
	n := wgsA / math.Sqrt(1-wgsE2*sinLat*sinLat)
	return (n + h) * cosLat * math.Cos(lon*deg), (n + h) * cosLat * math.Sin(lon*deg), (n*(1-wgsE2) + h) * sinLat
}

// fromECEF converts earth-centred earth-fixed metres to geodetic degrees and height.
func fromECEF(x, y, z float64) (lat, lon, h float64) {
	p := math.Hypot(x, y)
	phi := math.Atan2(z, p*(1-wgsE2))
	for i := 0; i < 10; i++ {
		s := math.Sin(phi)
		n := wgsA / math.Sqrt(1-wgsE2*s*s)
		next := math.Atan2(z+wgsE2*n*s, p)
		if math.Abs(next-phi) < 1e-15 {
			phi = next
			break
		}
		phi = next
	}
	s, c := math.Sin(phi), math.Cos(phi)
	h = p*c + z*s - wgsA*math.Sqrt(1-wgsE2*s*s)
	return phi / deg, math.Atan2(y, x) / deg, h
}
