package store

import (
	"encoding/csv"
	"errors"
	"io"
	"strconv"
	"strings"
)

// ParseRoute reads the x,y,yaw_deg,dwell_sec rows of a route CSV, skipping comments and bad rows.
func ParseRoute(text string) [][4]float64 {
	rd := csv.NewReader(strings.NewReader(text))
	rd.FieldsPerRecord = -1
	rd.Comment = '#'
	out := [][4]float64{}
	for {
		row, err := rd.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || len(row) < 2 {
			continue
		}
		var p [4]float64
		valid := true
		for i := 0; i < 4 && i < len(row); i++ {
			v, err := strconv.ParseFloat(strings.TrimSpace(row[i]), 64)
			if err != nil {
				valid = false
				break
			}
			p[i] = v
		}
		if valid {
			out = append(out, p)
		}
	}
	return out
}
