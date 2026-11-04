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

// noteClock records the offset of the robot clock from a telemetry timestamp (Unix seconds). It
// reports whether the offset is new or moved by more than a second.
func (s *Server) noteClock(robotID string, ts float64, now time.Time) bool {
	if ts <= 0 || math.IsNaN(ts) || math.IsInf(ts, 0) {
		return false
	}
	skew := now.Sub(unixTime(ts))
	s.mu.Lock()
	old, known := s.skew[robotID]
	s.skew[robotID] = skew
	s.mu.Unlock()
	return !known || (skew-old).Abs() > time.Second
}

// robotTime converts a robot timestamp to server time; a missing timestamp means now.
func (s *Server) robotTime(robotID string, ts float64) time.Time {
	if ts <= 0 || math.IsNaN(ts) || math.IsInf(ts, 0) {
		return time.Now()
	}
	t := unixTime(ts)
	skew, known := s.offset(robotID)
	if !known && s.relay != nil {
		// a split installation with several gateways: the robot's telemetry may reach another one
		skew, known = s.relay.ClockSkew(robotID)
	}
	if known && (skew > clockSkewMin || skew < -clockSkewMin) {
		return t.Add(skew)
	}
	return t
}

// offset is the measured offset of a robot clock (positive: robot behind).
func (s *Server) offset(robotID string) (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	skew, known := s.skew[robotID]
	return skew, known
}

// clockSkew returns the measured offset in seconds (positive: robot behind), or nil if unknown.
func (s *Server) clockSkew(robotID string) *float64 { return skewSeconds(s.offset(robotID)) }

// skewSeconds rounds an offset to a tenth of a second for the dashboard, nil when unknown.
func skewSeconds(skew time.Duration, known bool) *float64 {
	if !known {
		return nil
	}
	v := math.Round(skew.Seconds()*10) / 10
	return &v
}
