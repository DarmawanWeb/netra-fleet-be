package api

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"time"

	"github.com/DarmawanRobotics/netra-fleet-be/internal/detect"
	"github.com/DarmawanRobotics/netra-fleet-be/internal/i18n"
	"github.com/DarmawanRobotics/netra-fleet-be/internal/metrics"
	"github.com/DarmawanRobotics/netra-fleet-be/internal/store"
)

// RunPipeline processes queued anomalies: final detection, then the category rule.
func (s *Server) RunPipeline(ctx context.Context) {
	if err := s.store.RequeueStale(ctx); err != nil {
		slog.Error("requeue anomalies", "err", err)
	}
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		for {
			a, err := s.store.ClaimQueuedAnomaly(ctx)
			if errors.Is(err, store.ErrNotFound) {
				break
			}
			if err != nil {
				slog.Error("claim anomaly", "err", err)
				break
			}
			s.process(ctx, a)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-tick.C:
		}
	}
}

func (s *Server) process(ctx context.Context, a store.Anomaly) {
	early := detect.Result{Category: a.EarlyCategory, Confidence: a.EarlyConfidence, Description: a.EarlyNote}
	final, err := s.detector.Classify(ctx, a.ImagePath, early)
	if err != nil {
		slog.Warn("final detection failed, using the robot result", "anomaly", a.ID, "err", err)
		final = early
	}
	if final.Category == "" {
		final.Category = early.Category
	}
	rule, err := s.store.Rule(ctx, final.Category)
	if err != nil {
		slog.Error("anomaly rule", "err", err)
		return
	}
	status := "needs_validation"
	switch {
	case final.Category == "none" || final.Confidence < rule.MinConfidence:
		status = "dismissed"
	case !rule.RequiresValidation:
		status = "confirmed"
	}
	if err := s.store.SetFinal(ctx, a.ID, final.Category, final.Confidence, final.Description, status); err != nil {
		slog.Error("save final detection", "err", err)
		return
	}
	s.hub.Broadcast(map[string]any{"type": "anomaly", "robot_id": a.RobotID, "id": a.ID, "status": status,
		"category": final.Category})
	s.bus.Publish("netra.anomaly."+status, map[string]any{"id": a.ID, "robot_id": a.RobotID, "category": final.Category,
		"confidence": final.Confidence})
	metrics.Default.Add("netra_anomalies_processed_total", "Anomalies through final detection.", 1, "status", status)
	if status == "confirmed" {
		s.notifyConfirmed(a.ID)
	}
}

// notifyConfirmed sends a confirmed anomaly to Telegram when its rule asks for it.
func (s *Server) notifyConfirmed(id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	a, err := s.store.GetAnomaly(ctx, id)
	if err != nil || a.NotifiedAt != nil {
		return
	}
	category := a.EarlyCategory
	if a.FinalCategory != nil {
		category = *a.FinalCategory
	}
	rule, err := s.store.Rule(ctx, category)
	if err != nil || !rule.NotifyTelegram || !s.telegram.Enabled() {
		return
	}
	if err := s.telegram.SendPhoto(ctx, rule.TelegramChatID, s.caption(a, rule), a.ImagePath); err != nil {
		slog.Warn("telegram", "anomaly", a.ID, "err", err)
		return
	}
	_ = s.store.MarkNotified(ctx, a.ID, a.Status)
	s.hub.Broadcast(map[string]any{"type": "anomaly", "id": a.ID, "status": a.Status, "notified": true})
}

func (s *Server) caption(a store.Anomaly, rule store.Rule) string {
	lang := s.cfg.Lang
	where := i18n.T(lang, "unknown_position")
	if a.X != nil && a.Y != nil {
		where = fmt.Sprintf("x %.1f m, y %.1f m", *a.X, *a.Y)
	}
	note := a.FinalNote
	if note == "" {
		note = a.EarlyNote
	}
	text := fmt.Sprintf(i18n.T(lang, "detected_by")+"\n%s\n%s",
		html.EscapeString(i18n.Category(lang, rule.Category, rule.Label)), html.EscapeString(a.RobotID),
		a.DetectedAt.In(s.cfg.TimeZone).Format("02 Jan 2006 15:04"), where)
	if note != "" {
		text += "\n" + html.EscapeString(note)
	}
	if s.cfg.PublicURL != "" {
		text += fmt.Sprintf("\n%s/anomalies/%d", s.cfg.PublicURL, a.ID)
	}
	return text
}
