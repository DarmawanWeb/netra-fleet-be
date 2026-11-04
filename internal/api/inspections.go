package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/i18n"
	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
	"github.com/DarmawanWeb/netra-fleet-be/internal/metrics"
	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
	"github.com/DarmawanWeb/netra-fleet-be/internal/substation"
)

// planPoint is an inspection point as a robot needs it: where (the stop of its route), the camera
// pose, the measurement settings and, for thermal points, the regions to measure.
type planPoint struct {
	ID        int64               `json:"id"`
	Name      string              `json:"name"`
	Kind      string              `json:"kind"`
	StopIndex *int                `json:"stop_index"`
	Seq       int                 `json:"seq"`
	PTZ       *substation.PTZ     `json:"ptz"`
	Settings  substation.Settings `json:"settings"`
	Targets   []substation.Target `json:"targets"`
}

// planPoint fills a point's own settings with the site's: the robot sees the levels in force.
func (s *Server) planPoint(site substation.SiteSettings, p store.InspectionPoint) planPoint {
	pp := planPoint{ID: p.ID, Name: p.Name, Kind: p.Kind, StopIndex: p.StopIndex, Seq: p.Seq, PTZ: p.PTZ,
		Settings: p.Settings, Targets: p.Targets}
	if pp.Settings.Reference == "" && p.Kind == substation.KindThermal {
		pp.Settings.Reference = substation.RefAuto
	}
	if p.Kind == substation.KindPD {
		l := p.Settings.Levels(site.PD)
		pp.Settings.WatchDB, pp.Settings.AlarmDB = &l.WatchDB, &l.AlarmDB
	}
	return pp
}

// robotInspectionPlan answers a robot's question at the start of a patrol: which points to take at
// which stop of the route. Without the substation module the plan is empty.
func (s *Server) robotInspectionPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad id")
		return
	}
	route, _, err := s.store.GetRoute(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	if route.RobotID != robotOf(r) {
		writeError(w, http.StatusForbidden, "route of another robot")
		return
	}
	plan := []planPoint{}
	if s.lic.Has(license.Substation) {
		points, err := s.store.ListPoints(r.Context(), id, true)
		if err != nil {
			fail(w, err)
			return
		}
		site := s.siteSettings(r.Context())
		for _, p := range points {
			if p.StopIndex != nil {
				plan = append(plan, s.planPoint(site, p))
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"route_id": id, "points": plan})
}

// inspectionMeta is the meta part of a robot's inspection upload.
type inspectionMeta struct {
	UID      string                       `json:"uid"`
	PointID  int64                        `json:"point_id"`
	TS       float64                      `json:"ts"`
	PatrolID *float64                     `json:"patrol_id"`
	Pose     *struct{ X, Y, Yaw float64 } `json:"pose"`
	AmbientC *float64                     `json:"ambient_c"`
	Thermal  []substation.Reading         `json:"thermal"`
	PD       *substation.PDReading        `json:"pd"`
}

var uidPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// The images an inspection may carry, as multipart file fields.
var inspectionImages = []string{"thermal", "visual", "acoustic"}

const (
	maxInspectionUpload = 56 << 20 // three images of 16 MiB and the meta
	maxInspectionImage  = 16 << 20
)

func (m inspectionMeta) check(p store.InspectionPoint) error {
	switch {
	case !uidPattern.MatchString(m.UID):
		return errors.New("uid: 1 to 128 of A-Z a-z 0-9 . _ -")
	case m.AmbientC != nil && (!finite(*m.AmbientC) || *m.AmbientC < -60 || *m.AmbientC > 80):
		return errors.New("ambient_c: -60 to 80 °C")
	case m.Pose != nil && (!finite(m.Pose.X) || !finite(m.Pose.Y) || !finite(m.Pose.Yaw)):
		return errors.New("pose: numbers")
	}
	if p.Kind == substation.KindPD {
		if m.PD == nil || !finite(m.PD.DB) || m.PD.DB < -20 || m.PD.DB > 200 || runes(m.PD.Kind) > 32 {
			return errors.New("pd: {db: -20 to 200, detected, kind} for a pd point")
		}
		return nil
	}
	if len(m.Thermal) == 0 || len(m.Thermal) > substation.MaxTargets {
		return fmt.Errorf("thermal: 1 to %d readings for a thermal point", substation.MaxTargets)
	}
	for i, t := range m.Thermal {
		for _, v := range []*float64{&t.MaxC, t.AvgC, t.MinC} {
			if v != nil && (!finite(*v) || *v < -60 || *v > 2000) {
				return fmt.Errorf("thermal[%d]: temperatures -60 to 2000 °C", i)
			}
		}
	}
	return nil
}

// robotInspectionUpload stores what a robot measured at an inspection point: multipart meta (JSON)
// and the images thermal, visual and acoustic (JPEG or PNG, each optional). Idempotent on uid.
// Without the substation module it is accepted and dropped, so the robot does not keep retrying.
func (s *Server) robotInspectionUpload(w http.ResponseWriter, r *http.Request) {
	robotID := robotOf(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxInspectionUpload)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "multipart form with meta required (at most 56 MiB)")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	if !s.lic.Has(license.Substation) {
		writeJSON(w, http.StatusAccepted, map[string]any{"id": 0, "created": false, "dropped": "feature not licensed"})
		return
	}
	var meta inspectionMeta
	if err := json.Unmarshal([]byte(r.FormValue("meta")), &meta); err != nil {
		writeError(w, http.StatusBadRequest, "meta must be JSON with uid and point_id")
		return
	}
	p, err := s.store.GetPoint(r.Context(), meta.PointID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("point_id: no inspection point %d", meta.PointID))
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	if p.RouteID != nil {
		if route, _, err := s.store.GetRoute(r.Context(), *p.RouteID); err == nil && route.RobotID != robotID {
			writeError(w, http.StatusForbidden, "point of another robot's route")
			return
		}
	}
	if err := meta.check(p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	images, err := s.saveInspectionImages(r, meta.UID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	site := s.siteSettings(r.Context())
	in, obs := evaluate(p, meta, site)
	in.RobotID, in.PatrolID, in.TakenAt, in.Images = &robotID, patrolRef(meta.PatrolID), s.robotTime(robotID, meta.TS), images
	if meta.TS == 0 {
		in.TakenAt = time.Now()
	}
	id, created, changes, err := s.store.RecordInspection(r.Context(), in, obs, site)
	if err != nil {
		fail(w, err)
		return
	}
	if created {
		s.hub.Broadcast(map[string]any{"type": "inspection", "id": id, "point_id": p.ID, "robot_id": robotID,
			"priority": in.Priority, "kind": p.Kind})
		s.bus.Publish("netra.substation.inspection", map[string]any{"id": id, "point_id": p.ID, "robot_id": robotID,
			"priority": in.Priority})
		metrics.Default.Add("netra_inspections_received_total", "Substation inspections uploaded by robots.", 1,
			"kind", p.Kind, "priority", strconv.Itoa(in.Priority))
		for _, ch := range changes {
			s.broadcastFinding(ch.Finding, ch.Event)
			s.bus.Publish("netra.substation.finding."+ch.Event, map[string]any{"id": ch.Finding.ID,
				"priority": ch.Finding.Priority, "point_id": p.ID})
			if ch.Event == "opened" {
				metrics.Default.Add("netra_findings_opened_total", "Substation findings opened.", 1,
					"priority", strconv.Itoa(ch.Finding.Priority))
			}
			if (ch.Event == "opened" || ch.Event == "escalated") && site.Alerts(ch.Finding.Priority) {
				go s.alertFinding(ch.Finding.ID, id, site)
			}
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "created": created, "priority": in.Priority})
}

// saveInspectionImages keeps the images of an upload under the data folder, by day and uid.
func (s *Server) saveInspectionImages(r *http.Request, uid string) (map[string]string, error) {
	out := map[string]string{}
	dir := filepath.Join(s.cfg.DataDir, "inspections", time.Now().Format("2006-01-02"))
	for _, name := range inspectionImages {
		f, hdr, err := r.FormFile(name)
		if errors.Is(err, http.ErrMissingFile) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
		data, err := io.ReadAll(io.LimitReader(f, maxInspectionImage+1))
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
		if len(data) > maxInspectionImage || hdr.Size > maxInspectionImage {
			return nil, fmt.Errorf("%s: at most 16 MiB", name)
		}
		ext := ""
		switch http.DetectContentType(data) {
		case "image/jpeg":
			ext = ".jpg"
		case "image/png":
			ext = ".png"
		default:
			return nil, fmt.Errorf("%s: JPEG or PNG", name)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		path := filepath.Join(dir, uid+"-"+name+ext)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return nil, err
		}
		out[name] = path
	}
	return out, nil
}

// evaluate rates an upload against its point: the inspection to store and the observation of each
// target (thermal) or of the point ("pd") for its finding.
func evaluate(p store.InspectionPoint, m inspectionMeta, site substation.SiteSettings) (store.Inspection, []store.Observation) {
	in := store.Inspection{UID: m.UID, PointID: p.ID, Kind: p.Kind, AmbientC: m.AmbientC}
	if m.Pose != nil {
		in.X, in.Y, in.Yaw = &m.Pose.X, &m.Pose.Y, &m.Pose.Yaw
	}
	obs := []store.Observation{}
	if p.Kind == substation.KindPD {
		pd := *m.PD
		priority := substation.PDPriority(pd, p.Settings.Levels(site.PD))
		in.PD, in.Priority = &store.PDResult{PDReading: pd, Priority: priority}, priority
		obs = append(obs, store.Observation{TargetKey: "pd", Label: p.Name, AssetID: p.AssetID, Priority: priority,
			Basis: substation.BasisPD, Value: &pd.DB})
		return in, obs
	}
	in.Reference = p.Settings.Reference
	if in.Reference == "" {
		in.Reference = substation.RefAuto
	}
	in.Thermal = substation.EvaluateThermal(p.Targets, m.Thermal, m.AmbientC, in.Reference)
	for _, res := range in.Thermal {
		in.Priority = substation.Worse(in.Priority, res.Priority)
		asset := res.AssetID
		if asset == nil {
			asset = p.AssetID
		}
		delta := res.DeltaSimilarC
		if res.Basis == substation.BasisAmbient || delta == nil {
			delta = res.DeltaAmbientC
		}
		value := res.MaxC
		obs = append(obs, store.Observation{TargetKey: res.Key, Label: res.Label, AssetID: asset, Priority: res.Priority,
			Basis: res.Basis, Value: &value, DeltaC: delta})
	}
	return in, obs
}

// alertFinding sends a finding to Telegram with the image of the inspection that rated it.
func (s *Server) alertFinding(findingID, inspectionID int64, site substation.SiteSettings) {
	if !s.telegram.Enabled() || !s.lic.Has(license.Telegram) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	f, err := s.store.GetFinding(ctx, findingID)
	if err != nil {
		return
	}
	in, err := s.store.GetInspection(ctx, inspectionID)
	if err != nil {
		return
	}
	text := s.findingCaption(f)
	image := in.Images["thermal"]
	if f.Kind == substation.KindPD {
		image = in.Images["acoustic"]
	}
	if image == "" {
		image = in.Images["visual"]
	}
	if image != "" {
		err = s.telegram.SendPhoto(ctx, site.TelegramChatID, text, image)
	} else {
		err = s.telegram.SendMessage(ctx, site.TelegramChatID, text)
	}
	if err != nil {
		slog.Warn("telegram", "finding", f.ID, "err", err)
		return
	}
	_ = s.store.MarkFindingNotified(ctx, f.ID)
	s.hub.Broadcast(map[string]any{"type": "finding", "id": f.ID, "event": "notified", "status": f.Status,
		"priority": f.Priority, "point_id": f.PointID, "asset_id": f.AssetID})
}

// findingCaption is the Telegram text of a finding, in the server's language.
func (s *Server) findingCaption(f store.Finding) string {
	lang := s.cfg.Lang
	t := func(key string) string { return i18n.T(lang, key) }
	what := f.Label
	if f.AssetTag != nil {
		what = *f.AssetTag + " · " + what
	}
	text := fmt.Sprintf("<b>P%d · %s</b>\n%s\n%s", f.Priority, html.EscapeString(t(fmt.Sprintf("priority_%d", f.Priority))),
		html.EscapeString(what), html.EscapeString(f.PointName))
	switch {
	case f.Kind == substation.KindPD && f.LastValue != nil:
		text += fmt.Sprintf("\n%s %.1f dB", t("pd_level"), *f.LastValue)
	case f.LastValue != nil:
		text += fmt.Sprintf("\n%.1f °C", *f.LastValue)
		if f.LastDeltaC != nil {
			text += fmt.Sprintf(", ΔT %.1f °C %s", *f.LastDeltaC, t("basis_"+f.Basis))
		} else if f.Basis == substation.BasisLimit {
			text += ", " + t("basis_limit")
		}
	}
	text += "\n" + f.LastSeen.In(s.cfg.TimeZone).Format("02 Jan 2006 15:04")
	if s.cfg.PublicURL != "" {
		text += fmt.Sprintf("\n%s/substation/findings/%d", s.cfg.PublicURL, f.ID)
	}
	return text
}
