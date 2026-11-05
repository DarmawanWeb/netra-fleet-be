package rosmap

import (
	"image"
	"image/color"
	"math"
)

// Occupancy values, as in nav_msgs/OccupancyGrid.
const (
	Unknown  = -1
	Free     = 0
	Occupied = 100
)

// Colours of the rendered map: free space light, obstacles dark, unknown a mid grey. The dashboard
// shows them as they are in the light theme and inverted (hue kept) in the dark one, where free
// space becomes a deep navy, obstacles a pale slate and unknown a grey between them.
var (
	FreeColor     = color.RGBA{241, 245, 249, 255}
	OccupiedColor = color.RGBA{30, 41, 59, 255}
	UnknownColor  = color.RGBA{180, 189, 202, 255}
)

// Occupancy is the occupancy (Unknown, or Free to Occupied) of a pixel with value v of maxval and
// the given alpha, as nav2 map_server reads it (raw mode ignores negate there too).
func (m Meta) Occupancy(v, maxval int, alpha uint8) int {
	shade := float64(v) / float64(maxval) // 0 black to 1 white
	if m.Mode == Raw {
		if p := int(math.Round(shade * 255)); p <= Occupied {
			return p
		}
		return Unknown
	}
	occ := 1 - shade
	if m.Negate {
		occ = shade
	}
	switch {
	case m.Mode == Scale && alpha < 255:
		return Unknown
	case occ > m.OccupiedThresh:
		return Occupied
	case occ < m.FreeThresh:
		return Free
	case m.Mode == Scale:
		return int(math.RoundToEven((occ - m.FreeThresh) / (m.OccupiedThresh - m.FreeThresh) * 100))
	default:
		return Unknown
	}
}

// palette holds the colours of occupancy 0 to 100 (free to occupied) and, last, unknown.
var palette = func() color.Palette {
	p := make(color.Palette, Occupied+2)
	for i := 0; i <= Occupied; i++ {
		k := float64(i) / Occupied
		mix := func(a, b uint8) uint8 { return uint8(math.Round(float64(a) + (float64(b)-float64(a))*k)) }
		p[i] = color.RGBA{mix(FreeColor.R, OccupiedColor.R), mix(FreeColor.G, OccupiedColor.G), mix(FreeColor.B, OccupiedColor.B), 255}
	}
	p[Occupied+1] = UnknownColor
	return p
}()

// Render draws the map with its occupancy: in trinary mode three colours, in scale and raw mode
// the shades between free and occupied as well.
func Render(g *Gray, m Meta) *image.Paletted {
	out := image.NewPaletted(image.Rect(0, 0, g.Width, g.Height), palette)
	// Most maps use a few pixel values: decide each value once.
	cache := map[int]uint8{}
	for i, v := range g.Pix {
		alpha := uint8(255)
		if g.Alpha != nil {
			alpha = g.Alpha[i]
		}
		key := int(v)<<8 | int(alpha)
		idx, ok := cache[key]
		if !ok {
			idx = Occupied + 1
			if occ := m.Occupancy(int(v), g.Max, alpha); occ != Unknown {
				idx = byteOf(occ)
			}
			cache[key] = idx
		}
		out.Pix[i] = idx
	}
	return out
}

// Calibration places the rendered map on the robot map frame with the floor plan transform of
// NETRA: pixel = origin + scale·R(rotation)·(x, −y).
type Calibration struct {
	OriginXPx, OriginYPx, PxPerM, RotationRad float64
}

// Calibration of the map image of the given height. map.yaml gives the map frame pose of the
// lower-left corner of the image, so a map position w lands at R(−yaw)(w − origin)/resolution
// from that corner, x along the columns and y up the rows.
func (m Meta) Calibration(height int) Calibration {
	s := 1 / m.Resolution
	ox, oy, yaw := m.Origin[0], m.Origin[1], m.Origin[2]
	c, sn := math.Cos(yaw), math.Sin(yaw)
	return Calibration{
		OriginXPx:   -s * (c*ox + sn*oy),
		OriginYPx:   float64(height) + s*(c*oy-sn*ox),
		PxPerM:      s,
		RotationRad: yaw,
	}
}

// Pixel is where a map position lands on the image, in pixels from its top-left corner (pixel
// column i covers i to i+1): the floor plan formula of docs/api.md, which the dashboard applies.
func (c Calibration) Pixel(x, y float64) (px, py float64) {
	cr, sr := math.Cos(c.RotationRad), math.Sin(c.RotationRad)
	return c.OriginXPx + c.PxPerM*(cr*x+sr*y), c.OriginYPx + c.PxPerM*(sr*x-cr*y)
}
