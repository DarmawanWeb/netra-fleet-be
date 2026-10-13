package api

import (
	"math"
	"time"
)

// A robot without internet (a demo on the robot's own Wi-Fi, a basement without NTP) keeps
// whatever clock it booted with, often hours or days behind. Its anomaly, crowd and route
// timestamps would then land outside "today" on the dashboard. The server measures the offset
// from every telemetry message (sent at 1 Hz, latency is negligible next to the threshold) and
// shifts the robot's timestamps onto its own clock.

// clockSkewMin is the offset below which robot timestamps are used as they are.
const clockSkewMin = 2 * time.Second

// noteClock records the offset of the robot clock from a telemetry timestamp (Unix seconds).
func (s *Server) noteClock(robotID string, ts float64, now time.Time) {
	if ts <= 0 || math.IsNaN(ts) || math.IsInf(ts, 0) {
		return
	}
	s.mu.Lock()
	s.skew[robotID] = now.Sub(unixTime(ts))
	s.mu.Unlock()
}

// robotTime converts a robot timestamp to server time; a missing timestamp means now.
func (s *Server) robotTime(robotID string, ts float64) time.Time {
	if ts <= 0 || math.IsNaN(ts) || math.IsInf(ts, 0) {
		return time.Now()
	}
	t := unixTime(ts)
	s.mu.Lock()
	skew, known := s.skew[robotID]
	s.mu.Unlock()
	if known && (skew > clockSkewMin || skew < -clockSkewMin) {
		return t.Add(skew)
	}
	return t
}

// clockSkew returns the measured offset in seconds (positive: robot behind), or nil if unknown.
func (s *Server) clockSkew(robotID string) *float64 {
	s.mu.Lock()
	skew, known := s.skew[robotID]
	s.mu.Unlock()
	if !known {
		return nil
	}
	v := math.Round(skew.Seconds()*10) / 10
	return &v
}
