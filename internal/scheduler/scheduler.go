// Package scheduler starts patrols from weekly schedules.
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/DarmawanRobotics/netra-fleet-be/internal/store"
)

// StartFunc starts a patrol for a schedule.
type StartFunc func(ctx context.Context, robotID string, routeID int64, scheduleID *int64) (int64, error)

// Due reports whether a schedule should fire at now (inside a 5-minute window after its time).
func Due(s store.Schedule, now time.Time) bool {
	if !s.Enabled {
		return false
	}
	today := false
	if s.OnceOn != nil {
		today = now.Format("2006-01-02") == *s.OnceOn
	} else {
		for _, d := range s.Days {
			if int(now.Weekday()) == int(d) {
				today = true
			}
		}
	}
	at, err := time.ParseInLocation("15:04", s.TimeOfDay, now.Location())
	if !today || err != nil {
		return false
	}
	start := time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, now.Location())
	return !now.Before(start) && now.Sub(start) < 5*time.Minute
}

// Run checks the schedules every 20 s until ctx is cancelled.
func Run(ctx context.Context, st *store.Store, tz *time.Location, start StartFunc) {
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		now := time.Now().In(tz)
		schedules, err := st.ListSchedules(ctx)
		if err != nil {
			slog.Error("list schedules", "err", err)
		}
		for _, s := range schedules {
			if !Due(s, now) {
				continue
			}
			first, err := st.MarkScheduleRun(ctx, s.ID, now.Format("2006-01-02"))
			if err != nil || !first {
				continue
			}
			id := s.ID
			if _, err := start(ctx, s.RobotID, s.RouteID, &id); err != nil {
				slog.Warn("scheduled patrol failed", "schedule", s.Name, "err", err)
			} else {
				slog.Info("scheduled patrol started", "schedule", s.Name, "robot", s.RobotID)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
