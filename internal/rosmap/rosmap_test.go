package rosmap

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"
)

func TestParseYAML(t *testing.T) {
	nav2 := "image: map.pgm\nmode: trinary\nresolution: 0.05\norigin: [-10, -5.5, 0.1]\nnegate: 0\n" +
		"occupied_thresh: 0.65\nfree_thresh: 0.25\n"
	m, err := ParseYAML(nav2)
	if err != nil {
		t.Fatal(err)
	}
	want := Meta{Image: "map.pgm", Resolution: 0.05, Origin: [3]float64{-10, -5.5, 0.1}, OccupiedThresh: 0.65,
		FreeThresh: 0.25, Mode: Trinary}
	if m != want {
		t.Fatalf("%+v", m)
	}

	ros1 := "\ufeff# saved by map_saver\r\nimage: \"lab #2.pgm\"   # the image\r\nresolution: 0.025000\r\norigin:\r\n" +
		"  - -3.2\r\n  - 4.0\r\n  - 0.0\r\nnegate: 1\r\noccupied_thresh: 0.65\r\nmeta:\r\n  robot: l1w\r\n"
	m, err = ParseYAML(ros1)
	if err != nil {
		t.Fatal(err)
	}
	if m.Image != "lab #2.pgm" || m.Resolution != 0.025 || m.Origin != [3]float64{-3.2, 4, 0} || !m.Negate ||
		m.FreeThresh != DefaultFree || m.Mode != Trinary {
		t.Fatalf("%+v", m)
	}
	if m, err := ParseYAML("image: m.png\nresolution: 0.1\norigin: [1, 2]\nmode: Scale\n"); err != nil ||
		m.Origin != [3]float64{1, 2, 0} || m.Mode != Scale {
		t.Fatalf("two-value origin and scale mode: %+v %v", m, err)
	}

	for text, msg := range map[string]string{
		"resolution: 0.05\norigin: [0, 0, 0]\n":                                     "image is missing",
		"image: m.pgm\norigin: [0, 0, 0]\n":                                         "resolution is missing",
		"image: m.pgm\nresolution: 0.05\n":                                          "origin is missing",
		"image: m.pgm\nresolution: fast\norigin: [0, 0, 0]\n":                       "line 2: resolution must be a number",
		"image: m.pgm\nresolution: -1\norigin: [0, 0, 0]\n":                         "line 2: resolution must be a number",
		"image: m.pgm\nresolution: 0.05\norigin: [0, a, 0]\n":                       "line 3: origin must be [x, y, yaw] numbers",
		"image: m.pgm\nresolution: 0.05\norigin: [0]\n":                             "line 3: origin must be [x, y, yaw]",
		"image: m.pgm\nresolution: 0.05\norigin: [0, 0, 0]\nnegate: 2\n":            "line 4: negate must be 0 or 1",
		"image: m.pgm\nresolution: 0.05\norigin: [0, 0, 0]\nmode: color\n":          "line 4: mode must be trinary, scale or raw",
		"image: m.pgm\nresolution: 0.05\norigin: [0, 0, 0]\nfree_thresh: 0.7\n":     "free_thresh 0.7 must be below occupied_thresh 0.65",
		"image: m.pgm\nresolution: 0.05\norigin: [0, 0, 0]\noccupied_thresh: 1.5\n": "line 4: occupied_thresh must be a number from 0 to 1",
		"image: m.pgm\nresolution 0.05\n":                                           "line 2: expected key: value",
	} {
		if _, err := ParseYAML(text); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%q: %v, want %q", text, err, msg)
		}
	}
}

func TestDecodePGM(t *testing.T) {
	// P5 8-bit, with comments between the header tokens.
	p5 := append([]byte("P5\n# CREATOR: map_saver\n3 2\n# max\n255\n"), 0, 205, 254, 10, 20, 255)
	g, err := Decode(p5)
	if err != nil {
		t.Fatal(err)
	}
	if g.Width != 3 || g.Height != 2 || g.Max != 255 || fmt.Sprint(g.Pix) != "[0 205 254 10 20 255]" || g.Alpha != nil {
		t.Fatalf("%+v", g)
	}
	// P5 16-bit samples are big-endian.
	p16 := append([]byte("P5 2 1 65535 "), 0x01, 0x02, 0xff, 0xfe)
	if g, err := Decode(p16); err != nil || g.Max != 65535 || g.Pix[0] != 0x0102 || g.Pix[1] != 0xfffe {
		t.Fatalf("16-bit: %+v %v", g, err)
	}
	// P2, values above maxval clamped.
	if g, err := Decode([]byte("P2\n2 2\n# c\n100\n0 50\n100 120\n")); err != nil || fmt.Sprint(g.Pix) != "[0 50 100 100]" {
		t.Fatalf("P2: %+v %v", g, err)
	}
	for data, msg := range map[string]string{
		"P6\n1 1\n255\n\x00\x00\x00": "not a PGM (P5 or P2) or PNG",
		"P5\n3 2\n255\n\x00\x01":     "truncated: 2 of 6 bytes",
		"P5\n0 2\n255\n":             "not a positive number",
		"P5\n2 2\n70000\n":           "above 65535",
		"P5\n2":                      "unexpected end of file",
		"P2\n2 1\n255\n1 x\n":        `"x" is not a pixel value`,
		"P2\n2 1\n255\n1\n":          "1 of 2 values",
		"P5\n20000 2\n255\n":         "larger than",
	} {
		if _, err := Decode([]byte(data)); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%q: %v, want %q", data, err, msg)
		}
	}
}

func TestDecodePNG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.NRGBA{30, 60, 90, 255})
	img.Set(1, 0, color.NRGBA{255, 255, 255, 0})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	g, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if g.Max != 65535 || g.Pix[0] != 60*257 || g.Alpha == nil || g.Alpha[0] != 255 || g.Alpha[1] != 0 {
		t.Fatalf("%+v", g)
	}
	gray := image.NewGray(image.Rect(0, 0, 2, 2))
	gray.Pix = []uint8{0, 254, 205, 7}
	buf.Reset()
	_ = png.Encode(&buf, gray)
	if g, err := Decode(buf.Bytes()); err != nil || g.Max != 255 || fmt.Sprint(g.Pix) != "[0 254 205 7]" || g.Alpha != nil {
		t.Fatalf("gray: %+v %v", g, err)
	}
}

func TestOccupancy(t *testing.T) {
	saver := Meta{OccupiedThresh: 0.65, FreeThresh: 0.196, Mode: Trinary}
	for v, want := range map[int]int{254: Free, 255: Free, 0: Occupied, 205: Unknown, 80: Occupied, 100: Unknown} {
		if got := saver.Occupancy(v, 255, 255); got != want {
			t.Errorf("trinary %d: %d, want %d", v, got, want)
		}
	}
	negate := saver
	negate.Negate = true
	for v, want := range map[int]int{254: Occupied, 0: Free, 205: Occupied, 30: Free} {
		if got := negate.Occupancy(v, 255, 255); got != want {
			t.Errorf("negate %d: %d, want %d", v, got, want)
		}
	}
	// 16-bit: the share of maxval counts, not the byte.
	if saver.Occupancy(65534, 65535, 255) != Free || saver.Occupancy(52685, 65535, 255) != Unknown ||
		saver.Occupancy(300, 65535, 255) != Occupied {
		t.Error("16-bit values")
	}
	scale := Meta{OccupiedThresh: 0.6, FreeThresh: 0.2, Mode: Scale}
	if got := scale.Occupancy(153, 255, 255); got != 50 {
		t.Errorf("scale mid value: %d", got)
	}
	if scale.Occupancy(153, 255, 100) != Unknown || scale.Occupancy(250, 255, 255) != Free || scale.Occupancy(10, 255, 255) != Occupied {
		t.Error("scale alpha and thresholds")
	}
	raw := Meta{OccupiedThresh: 0.65, FreeThresh: 0.196, Mode: Raw, Negate: true}
	if raw.Occupancy(0, 255, 255) != 0 || raw.Occupancy(42, 255, 255) != 42 || raw.Occupancy(100, 255, 255) != 100 ||
		raw.Occupancy(101, 255, 255) != Unknown {
		t.Error("raw values")
	}
}

func TestRender(t *testing.T) {
	g := &Gray{Width: 3, Height: 1, Max: 255, Pix: []uint16{254, 0, 205}}
	out := Render(g, Meta{OccupiedThresh: 0.65, FreeThresh: 0.196, Mode: Trinary})
	for i, want := range []color.Color{FreeColor, OccupiedColor, UnknownColor} {
		if got := out.At(i, 0); got != want {
			t.Errorf("pixel %d: %v, want %v", i, got, want)
		}
	}
	out = Render(&Gray{Width: 1, Height: 1, Max: 255, Pix: []uint16{153}}, Meta{OccupiedThresh: 0.6, FreeThresh: 0.2, Mode: Scale})
	mid := out.At(0, 0).(color.RGBA)
	if mid.R >= FreeColor.R || mid.R <= OccupiedColor.R || mid.A != 255 {
		t.Errorf("scale shade %v", mid)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		t.Fatal(err)
	}
}

func TestCalibrationPlacesMapPoints(t *testing.T) {
	m := Meta{Resolution: 0.05, Origin: [3]float64{-10, -5, 0}}
	c := m.Calibration(200)
	if c.PxPerM != 20 || c.RotationRad != 0 {
		t.Fatalf("%+v", c)
	}
	// The map origin is 10 m right of and 5 m above the lower-left corner of the 200 px tall image.
	if px, py := c.Pixel(0, 0); math.Abs(px-200) > 1e-9 || math.Abs(py-100) > 1e-9 {
		t.Fatalf("origin at %.3f,%.3f", px, py)
	}
	// The centre of the lower-left cell is the centre of the bottom-left pixel.
	if px, py := c.Pixel(-10+0.025, -5+0.025); math.Abs(px-0.5) > 1e-9 || math.Abs(py-199.5) > 1e-9 {
		t.Fatalf("lower-left cell at %.3f,%.3f", px, py)
	}

	// With a yaw, every cell centre map_server places at origin + R(yaw)·(cell + ½)·resolution
	// lands on the centre of its pixel.
	m = Meta{Resolution: 0.1, Origin: [3]float64{2, 3, 0.3}}
	const height = 50
	c = m.Calibration(height)
	for _, cell := range [][2]int{{0, 0}, {7, 3}, {39, 49}, {12, 30}} {
		col, row := cell[0], cell[1] // row counted from the bottom, as in the occupancy grid
		lx, ly := (float64(col)+0.5)*m.Resolution, (float64(row)+0.5)*m.Resolution
		cy, sy := math.Cos(m.Origin[2]), math.Sin(m.Origin[2])
		x, y := m.Origin[0]+cy*lx-sy*ly, m.Origin[1]+sy*lx+cy*ly
		px, py := c.Pixel(x, y)
		if math.Abs(px-(float64(col)+0.5)) > 1e-9 || math.Abs(py-(float64(height-1-row)+0.5)) > 1e-9 {
			t.Errorf("cell %v at %.4f,%.4f, want pixel %d,%d", cell, px, py, col, height-1-row)
		}
	}
}
