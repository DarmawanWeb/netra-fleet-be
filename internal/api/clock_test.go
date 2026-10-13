package api

import (
	"testing"
	"time"
)

func newClockServer() *Server { return &Server{skew: map[string]time.Duration{}} }

func unix(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }

func TestRobotTimeCorrectsAClockThatIsBehind(t *testing.T) {
	s := newClockServer()
	now := time.Now()
	robotNow := now.Add(-3 * time.Hour) // robot booted without NTP
	s.noteClock("l1w-01", unix(robotNow), now)

	detected := robotNow.Add(-10 * time.Second) // anomaly seen 10 s ago on the robot clock
	got := s.robotTime("l1w-01", unix(detected))
	if want := now.Add(-10 * time.Second); got.Sub(want).Abs() > 10*time.Millisecond {
		t.Fatalf("robotTime = %v, want %v", got, want)
	}
	if skew := s.clockSkew("l1w-01"); skew == nil || *skew != 10800 {
		t.Fatalf("clockSkew = %v, want 10800", skew)
	}
}

func TestRobotTimeKeepsASyncedClock(t *testing.T) {
	s := newClockServer()
	now := time.Now()
	s.noteClock("l1w-01", unix(now.Add(-300*time.Millisecond)), now) // latency, not skew
	ts := now.Add(-time.Minute)
	if got := s.robotTime("l1w-01", unix(ts)); got.Sub(ts).Abs() > time.Millisecond {
		t.Fatalf("a synced robot clock was shifted: %v vs %v", got, ts)
	}
}

func TestRobotTimeWithoutTelemetryOrTimestamp(t *testing.T) {
	s := newClockServer()
	ts := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	if got := s.robotTime("unknown", unix(ts)); !got.Equal(ts) {
		t.Fatalf("no measurement yet: got %v, want the robot time %v", got, ts)
	}
	if got := s.robotTime("unknown", 0); time.Since(got) > time.Second {
		t.Fatalf("missing timestamp must mean now, got %v", got)
	}
	if s.clockSkew("unknown") != nil {
		t.Fatal("skew reported for a robot that never sent telemetry")
	}
	s.noteClock("unknown", 0, time.Now()) // telemetry without ts must not record a skew
	if s.clockSkew("unknown") != nil {
		t.Fatal("telemetry without a timestamp recorded a skew")
	}
}
