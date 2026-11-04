// Package rosmap reads the maps ROS map_server saves, map.yaml and its PGM image, renders them for
// the dashboard with the occupancy semantics of map_server, and places them on the robot map frame.
package rosmap

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Modes of map.yaml: how pixel values become occupancy.
const (
	Trinary = "trinary"
	Scale   = "scale"
	Raw     = "raw"
)

// Meta is map.yaml.
type Meta struct {
	Image          string
	Resolution     float64    // metres per pixel
	Origin         [3]float64 // x, y (metres) and yaw (radians) of the lower-left pixel in the map frame
	Negate         bool
	OccupiedThresh float64
	FreeThresh     float64
	Mode           string
}

// Defaults of the thresholds when map.yaml leaves them out: those ROS map_saver writes, with which
// its unknown grey (205) stays unknown.
const (
	DefaultOccupied = 0.65
	DefaultFree     = 0.196
)

// ParseYAML reads the flat keys of map.yaml: image, resolution, origin, negate, occupied_thresh,
// free_thresh and mode. Other keys are ignored.
func ParseYAML(text string) (Meta, error) {
	m := Meta{OccupiedThresh: DefaultOccupied, FreeThresh: DefaultFree, Mode: Trinary}
	values := map[string]string{}
	lines := map[string]int{}
	var list string // key whose value continues as a block list ("origin:" then "- x" lines)
	for i, raw := range strings.Split(strings.TrimPrefix(text, "\ufeff"), "\n") {
		line := strings.TrimRight(stripComment(raw), " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "---" || trimmed == "..." {
			continue
		}
		if item, ok := strings.CutPrefix(trimmed, "- "); ok && list != "" && line != trimmed {
			values[list] += strings.TrimSpace(item) + ","
			continue
		}
		list = ""
		if line != trimmed {
			continue // nested value of a key we do not read
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return m, fmt.Errorf("line %d: expected key: value", i+1)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		lines[key] = i + 1
		if value == "" {
			list, values[key] = key, ""
			continue
		}
		values[key] = value
	}

	fail := func(key, what string) error { return fmt.Errorf("line %d: %s %s", lines[key], key, what) }
	if v, ok := values["image"]; ok {
		m.Image = unquote(v)
	}
	if m.Image == "" {
		return m, errors.New("image is missing")
	}
	v, ok := values["resolution"]
	if !ok {
		return m, errors.New("resolution is missing")
	}
	res, err := number(v)
	if err != nil || res <= 0 {
		return m, fail("resolution", "must be a number of metres per pixel above 0")
	}
	m.Resolution = res
	v, ok = values["origin"]
	if !ok {
		return m, errors.New("origin is missing")
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSuffix(v, ","), "["), "]"), ",")
	if len(parts) < 2 || len(parts) > 3 {
		return m, fail("origin", "must be [x, y, yaw]")
	}
	for j, p := range parts {
		if m.Origin[j], err = number(p); err != nil {
			return m, fail("origin", "must be [x, y, yaw] numbers")
		}
	}
	if v, ok := values["negate"]; ok {
		switch strings.ToLower(unquote(v)) {
		case "0", "false", "no", "off":
		case "1", "true", "yes", "on":
			m.Negate = true
		default:
			return m, fail("negate", "must be 0 or 1")
		}
	}
	for _, t := range []struct {
		key string
		dst *float64
	}{{"occupied_thresh", &m.OccupiedThresh}, {"free_thresh", &m.FreeThresh}} {
		if v, ok := values[t.key]; ok {
			if *t.dst, err = number(v); err != nil || *t.dst < 0 || *t.dst > 1 {
				return m, fail(t.key, "must be a number from 0 to 1")
			}
		}
	}
	if m.FreeThresh >= m.OccupiedThresh {
		return m, fmt.Errorf("free_thresh %g must be below occupied_thresh %g", m.FreeThresh, m.OccupiedThresh)
	}
	if v, ok := values["mode"]; ok {
		m.Mode = strings.ToLower(unquote(v))
		if m.Mode != Trinary && m.Mode != Scale && m.Mode != Raw {
			return m, fail("mode", "must be trinary, scale or raw")
		}
	}
	return m, nil
}

// stripComment removes a # comment that is not inside quotes.
func stripComment(line string) string {
	var quote rune
	for i, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return line[:i]
		}
	}
	return line
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

func number(v string) (float64, error) {
	f, err := strconv.ParseFloat(unquote(v), 64)
	if err == nil && (math.IsNaN(f) || math.IsInf(f, 0)) {
		err = errors.New("not finite")
	}
	return f, err
}
