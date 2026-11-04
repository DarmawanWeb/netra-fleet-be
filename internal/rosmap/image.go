package rosmap

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"strconv"
)

// Size limits of a map image: 10 000 px a side and 64 megapixels (a 400 m square at 5 cm).
const (
	MaxSide   = 10000
	MaxPixels = 64 << 20
)

// Gray is a map image: Pix holds one value per pixel from 0 (black) to Max (white), row by row
// from the top; Alpha is nil for opaque images.
type Gray struct {
	Width, Height int
	Max           int
	Pix           []uint16
	Alpha         []uint8
}

// Decode reads a map image: PGM (P5 with 8- or 16-bit samples, or plain P2) or PNG.
func Decode(data []byte) (*Gray, error) {
	switch {
	case bytes.HasPrefix(data, []byte("P5")), bytes.HasPrefix(data, []byte("P2")):
		return DecodePGM(data)
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return DecodePNG(data)
	}
	return nil, errors.New("not a PGM (P5 or P2) or PNG image")
}

// DecodePGM reads a binary (P5, 8-bit or big-endian 16-bit) or plain (P2) PGM.
func DecodePGM(data []byte) (*Gray, error) {
	if len(data) < 2 || (data[0] != 'P' || (data[1] != '5' && data[1] != '2')) {
		return nil, errors.New("not a P5 or P2 PGM")
	}
	plain := data[1] == '2'
	pos := 2
	var header [3]int
	for i := range header {
		tok, next, err := token(data, pos)
		if err != nil {
			return nil, fmt.Errorf("PGM header: %w", err)
		}
		pos = next
		if header[i], err = strconv.Atoi(tok); err != nil || header[i] <= 0 {
			return nil, fmt.Errorf("PGM header: %q is not a positive number", tok)
		}
	}
	g := &Gray{Width: header[0], Height: header[1], Max: header[2]}
	if g.Max > 65535 {
		return nil, fmt.Errorf("PGM maxval %d is above 65535", g.Max)
	}
	if err := checkSize(g.Width, g.Height); err != nil {
		return nil, err
	}
	n := g.Width * g.Height
	g.Pix = make([]uint16, n)
	if plain {
		for i := 0; i < n; i++ {
			tok, next, err := token(data, pos)
			if err != nil {
				return nil, fmt.Errorf("PGM data: %d of %d values", i, n)
			}
			pos = next
			v, err := strconv.Atoi(tok)
			if err != nil || v < 0 {
				return nil, fmt.Errorf("PGM data: %q is not a pixel value", tok)
			}
			g.Pix[i] = clamp(v, g.Max)
		}
		return g, nil
	}
	// One whitespace character separates the header from the samples.
	if pos >= len(data) || !space(data[pos]) {
		return nil, errors.New("PGM header: no whitespace before the data")
	}
	pos++
	bps := 1
	if g.Max > 255 {
		bps = 2
	}
	if have := len(data) - pos; have < n*bps {
		return nil, fmt.Errorf("PGM data is truncated: %d of %d bytes", have, n*bps)
	}
	raster := data[pos:]
	for i := range g.Pix {
		v := int(raster[i])
		if bps == 2 {
			v = int(raster[2*i])<<8 | int(raster[2*i+1])
		}
		g.Pix[i] = clamp(v, g.Max)
	}
	return g, nil
}

// DecodePNG reads a PNG map: the value of a pixel is the mean of its red, green and blue.
func DecodePNG(data []byte) (*Gray, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("PNG: %w", err)
	}
	if err := checkSize(cfg.Width, cfg.Height); err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("PNG: %w", err)
	}
	b := img.Bounds()
	g := &Gray{Width: b.Dx(), Height: b.Dy(), Max: 65535, Pix: make([]uint16, b.Dx()*b.Dy())}
	switch src := img.(type) {
	case *image.Gray:
		g.Max = 255
		for y := 0; y < g.Height; y++ {
			for x := 0; x < g.Width; x++ {
				g.Pix[y*g.Width+x] = uint16(src.Pix[y*src.Stride+x])
			}
		}
	case *image.Gray16:
		for y := 0; y < g.Height; y++ {
			for x := 0; x < g.Width; x++ {
				i := y*src.Stride + 2*x
				g.Pix[y*g.Width+x] = uint16(src.Pix[i])<<8 | uint16(src.Pix[i+1])
			}
		}
	default:
		opaque := true
		alpha := make([]uint8, len(g.Pix))
		for y := 0; y < g.Height; y++ {
			for x := 0; x < g.Width; x++ {
				r, gr, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				if a > 0 && a < 0xffff { // undo the alpha premultiplication
					r, gr, bl = r*0xffff/a, gr*0xffff/a, bl*0xffff/a
				}
				g.Pix[y*g.Width+x] = clamp(int((r+gr+bl)/3), math.MaxUint16)
				alpha[y*g.Width+x] = byteOf(int(a >> 8))
				opaque = opaque && a == 0xffff
			}
		}
		if !opaque {
			g.Alpha = alpha
		}
	}
	return g, nil
}

func checkSize(w, h int) error {
	if w <= 0 || h <= 0 || w > MaxSide || h > MaxSide || w*h > MaxPixels {
		return fmt.Errorf("map image of %d×%d px is larger than %d px a side or %d megapixels", w, h, MaxSide, MaxPixels>>20)
	}
	return nil
}

// token returns the next whitespace-separated token from pos, skipping # comments.
func token(data []byte, pos int) (string, int, error) {
	for pos < len(data) {
		switch {
		case space(data[pos]):
			pos++
		case data[pos] == '#':
			for pos < len(data) && data[pos] != '\n' && data[pos] != '\r' {
				pos++
			}
		default:
			start := pos
			for pos < len(data) && !space(data[pos]) && data[pos] != '#' {
				pos++
			}
			return string(data[start:pos]), pos, nil
		}
	}
	return "", pos, errors.New("unexpected end of file")
}

func space(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// clamp is v as a sample of at most limit.
func clamp(v, limit int) uint16 {
	v = min(v, limit)
	if v < 0 || v > math.MaxUint16 {
		return 0
	}
	return uint16(v)
}

// byteOf is v as a byte (0 when it does not fit).
func byteOf(v int) uint8 {
	if v < 0 || v > math.MaxUint8 {
		return 0
	}
	return uint8(v)
}
