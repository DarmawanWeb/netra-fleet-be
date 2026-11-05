package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
	"github.com/DarmawanWeb/netra-fleet-be/internal/substation"
)

// The substation module: equipment (assets), the inspection points robots take on their routes,
// what they measured (inspections, rated in internal/substation), and the findings operators
// follow up. Robots fetch their route's plan and upload inspections in inspections.go.

// siteSettings returns the saved substation settings, the defaults for what is not saved.
func (s *Server) siteSettings(ctx context.Context) substation.SiteSettings {
	out := substation.DefaultSiteSettings
	if v, err := s.store.Setting(ctx, store.SettingSubstation); err == nil {
		_ = json.Unmarshal([]byte(v), &out)
	}
	return out
}

// substationFail answers the store errors of the module: 409 for a taken tag, a record still in
// use or a finding in another status.
func substationFail(w http.ResponseWriter, err error) {
	var inUse store.InUseError
	switch {
	case errors.As(err, &inUse):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "tag already used by another asset")
	case errors.Is(err, store.ErrStatus):
		writeError(w, http.StatusConflict, "the finding is not in a status that allows this")
	default:
		fail(w, err)
	}
}

func (s *Server) substationCriteria(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"thermal": substation.Criteria, "asset_kinds": substation.AssetKinds,
		"point_kinds": substation.Kinds, "references": substation.References})
}

func (s *Server) substationSummary(w http.ResponseWriter, r *http.Request) {
	sum, err := s.store.Summary(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) getSubstationSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.siteSettings(r.Context()))
}

func (s *Server) putSubstationSettings(w http.ResponseWriter, r *http.Request) {
	body := s.siteSettings(r.Context())
	if decode(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	body.TelegramChatID = strings.TrimSpace(body.TelegramChatID)
	if err := body.Check(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	raw, _ := json.Marshal(body)
	if err := s.store.SetSetting(r.Context(), store.SettingSubstation, string(raw)); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// ---- assets

type assetBody struct {
	Tag       string   `json:"tag"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Bay       string   `json:"bay"`
	VoltageKV *float64 `json:"voltage_kv"`
	Phase     string   `json:"phase"`
	X         *float64 `json:"x"`
	Y         *float64 `json:"y"`
	Notes     string   `json:"notes"`
	Archived  bool     `json:"archived"`
}

func runes(s string) int { return utf8.RuneCountInString(s) }

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func (b assetBody) asset(id int64) (store.Asset, error) {
	a := store.Asset{ID: id, Tag: strings.TrimSpace(b.Tag), Name: strings.TrimSpace(b.Name), Kind: b.Kind,
		Bay: strings.TrimSpace(b.Bay), VoltageKV: b.VoltageKV, Phase: strings.TrimSpace(b.Phase), X: b.X, Y: b.Y,
		Notes: strings.TrimSpace(b.Notes), Archived: b.Archived}
	switch {
	case runes(a.Tag) == 0 || runes(a.Tag) > 40 || strings.ContainsAny(a.Tag, "\r\n\t"):
		return a, errors.New("tag: 1 to 40 characters on one line")
	case runes(a.Name) == 0 || runes(a.Name) > 120:
		return a, errors.New("name: 1 to 120 characters")
	case !slices.Contains(substation.AssetKinds, a.Kind):
		return a, fmt.Errorf("kind: one of %s", strings.Join(substation.AssetKinds, ", "))
	case runes(a.Bay) > 80:
		return a, errors.New("bay: up to 80 characters")
	case a.VoltageKV != nil && (!finite(*a.VoltageKV) || *a.VoltageKV <= 0 || *a.VoltageKV > 1200):
		return a, errors.New("voltage_kv: above 0 up to 1200")
	case runes(a.Phase) > 8:
		return a, errors.New("phase: up to 8 characters")
	case (a.X == nil) != (a.Y == nil):
		return a, errors.New("x and y: both or neither")
	case a.X != nil && (!finite(*a.X) || !finite(*a.Y) || math.Abs(*a.X) > 1e6 || math.Abs(*a.Y) > 1e6):
		return a, errors.New("x and y: metres in the map frame")
	case runes(a.Notes) > 2000:
		return a, errors.New("notes: up to 2000 characters")
	}
	return a, nil
}

func (s *Server) listAssets(w http.ResponseWriter, r *http.Request) {
	assets, err := s.store.ListAssets(r.Context(), r.URL.Query().Get("archived") == "true")
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, assets)
}

func (s *Server) getAsset(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	a, err := s.store.GetAsset(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) saveAsset(w http.ResponseWriter, r *http.Request) {
	id, update := pathID(r)
	var body assetBody
	if decode(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	a, err := body.asset(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if update {
		err = s.store.UpdateAsset(r.Context(), a)
	} else {
		a.ID, err = s.store.CreateAsset(r.Context(), a)
	}
	if err != nil {
		substationFail(w, err)
		return
	}
	saved, err := s.store.GetAsset(r.Context(), a.ID)
	if err != nil {
		fail(w, err)
		return
	}
	status := http.StatusOK
	if !update {
		status = http.StatusCreated
	}
	writeJSON(w, status, saved)
}

func (s *Server) deleteAsset(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	if err := s.store.DeleteAsset(r.Context(), id); err != nil {
		var inUse store.InUseError
		if errors.As(err, &inUse) {
			writeError(w, http.StatusConflict, err.Error()+": archive the asset instead")
			return
		}
		substationFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- inspection points

type pointBody struct {
	Name      string              `json:"name"`
	Kind      string              `json:"kind"`
	AssetID   *int64              `json:"asset_id"`
	RouteID   *int64              `json:"route_id"`
	StopIndex *int                `json:"stop_index"`
	Seq       int                 `json:"seq"`
	PTZ       *substation.PTZ     `json:"ptz"`
	Settings  substation.Settings `json:"settings"`
	Targets   []substation.Target `json:"targets"`
	Enabled   *bool               `json:"enabled"`
}

// stance is where the robot stands for a point: its route's stop, metres and degrees in the map frame.
type stance struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	YawDeg float64 `json:"yaw_deg"`
}

// inspectionPointView is an inspection point with the robot's stance at its stop.
type inspectionPointView struct {
	store.InspectionPoint
	RobotID string  `json:"robot_id,omitempty"` // the robot of its route
	Stance  *stance `json:"stance"`
}

// routeStops reads routes once per request: their robot and their stops (rows with a dwell).
type routeStops map[int64]*struct {
	robot string
	stops [][4]float64
}

func (rs routeStops) get(ctx context.Context, st *store.Store, id int64) (string, [][4]float64, error) {
	if c, ok := rs[id]; ok {
		return c.robot, c.stops, nil
	}
	route, csv, err := st.GetRoute(ctx, id)
	if err != nil {
		return "", nil, err
	}
	stops := [][4]float64{}
	for _, p := range parseRoute(csv) {
		if p[3] > 0 {
			stops = append(stops, p)
		}
	}
	rs[id] = &struct {
		robot string
		stops [][4]float64
	}{route.RobotID, stops}
	return route.RobotID, stops, nil
}

func (s *Server) viewPoint(ctx context.Context, rs routeStops, p store.InspectionPoint) inspectionPointView {
	v := inspectionPointView{InspectionPoint: p}
	if p.RouteID == nil {
		return v
	}
	robot, stops, err := rs.get(ctx, s.store, *p.RouteID)
	if err != nil {
		return v
	}
	v.RobotID = robot
	if p.StopIndex != nil && *p.StopIndex < len(stops) {
		st := stops[*p.StopIndex]
		v.Stance = &stance{X: st[0], Y: st[1], YawDeg: st[2]}
	}
	return v
}

func (s *Server) listPoints(w http.ResponseWriter, r *http.Request) {
	routeID, _ := strconv.ParseInt(r.URL.Query().Get("route_id"), 10, 64)
	points, err := s.store.ListPoints(r.Context(), routeID, r.URL.Query().Get("enabled") == "true")
	if err != nil {
		fail(w, err)
		return
	}
	rs, out := routeStops{}, make([]inspectionPointView, 0, len(points))
	for _, p := range points {
		out = append(out, s.viewPoint(r.Context(), rs, p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getPoint(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	p, err := s.store.GetPoint(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.viewPoint(r.Context(), routeStops{}, p))
}

// point checks a point body: the definition, its route and stop, and the assets it names.
func (s *Server) point(ctx context.Context, id int64, b pointBody) (store.InspectionPoint, int, error) {
	b.Name = strings.TrimSpace(b.Name)
	if err := (substation.Spec{Name: b.Name, Kind: b.Kind, PTZ: b.PTZ, Settings: b.Settings, Targets: b.Targets}).Check(); err != nil {
		return store.InspectionPoint{}, http.StatusBadRequest, err
	}
	p := store.InspectionPoint{ID: id, Name: b.Name, Kind: b.Kind, AssetID: b.AssetID, RouteID: b.RouteID,
		StopIndex: b.StopIndex, Seq: b.Seq, PTZ: b.PTZ, Settings: b.Settings, Targets: b.Targets, Enabled: true}
	if b.Enabled != nil {
		p.Enabled = *b.Enabled
	}
	if p.Kind == substation.KindPD {
		p.Targets = []substation.Target{}
	}
	for i := range p.Targets {
		t := &p.Targets[i]
		t.Label, t.Phase, t.Group = strings.TrimSpace(t.Label), strings.TrimSpace(t.Phase), strings.TrimSpace(t.Group)
		if t.Label == "" {
			t.Label = t.Key
		}
	}
	if p.Seq < 0 || p.Seq > 1000 {
		return p, http.StatusBadRequest, errors.New("seq: 0 to 1000")
	}
	switch {
	case p.RouteID == nil && p.StopIndex != nil:
		return p, http.StatusBadRequest, errors.New("stop_index: needs a route_id")
	case p.RouteID != nil && p.StopIndex == nil:
		return p, http.StatusBadRequest, errors.New("stop_index: the stop of the route where the robot takes it")
	case p.RouteID != nil:
		_, stops, err := routeStops{}.get(ctx, s.store, *p.RouteID)
		if errors.Is(err, store.ErrNotFound) {
			return p, http.StatusBadRequest, fmt.Errorf("route_id: no route %d", *p.RouteID)
		}
		if err != nil {
			return p, http.StatusInternalServerError, err
		}
		if *p.StopIndex < 0 || *p.StopIndex >= len(stops) {
			return p, http.StatusBadRequest, fmt.Errorf("stop_index: the route has %d stop(s), counted from 0", len(stops))
		}
	}
	ids := []int64{}
	if p.AssetID != nil {
		ids = append(ids, *p.AssetID)
	}
	for _, t := range p.Targets {
		if t.AssetID != nil {
			ids = append(ids, *t.AssetID)
		}
	}
	missing, err := s.store.MissingAssets(ctx, ids)
	if err != nil {
		return p, http.StatusInternalServerError, err
	}
	if len(missing) > 0 {
		return p, http.StatusBadRequest, fmt.Errorf("asset %d not found", missing[0])
	}
	return p, 0, nil
}

func (s *Server) savePoint(w http.ResponseWriter, r *http.Request) {
	id, update := pathID(r)
	var body pointBody
	if decode(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if update {
		old, err := s.store.GetPoint(r.Context(), id)
		if err != nil {
			fail(w, err)
			return
		}
		if body.Kind == "" {
			body.Kind = old.Kind
		}
		if body.Kind != old.Kind {
			writeError(w, http.StatusConflict, "kind cannot change: its inspections and findings are of the old kind")
			return
		}
	}
	p, status, err := s.point(r.Context(), id, body)
	if err != nil {
		if status == http.StatusInternalServerError {
			fail(w, err)
		} else {
			writeError(w, status, err.Error())
		}
		return
	}
	if update {
		err = s.store.UpdatePoint(r.Context(), p)
	} else {
		p.ID, err = s.store.CreatePoint(r.Context(), p)
	}
	if err != nil {
		fail(w, err)
		return
	}
	saved, err := s.store.GetPoint(r.Context(), p.ID)
	if err != nil {
		fail(w, err)
		return
	}
	status = http.StatusOK
	if !update {
		status = http.StatusCreated
	}
	writeJSON(w, status, s.viewPoint(r.Context(), routeStops{}, saved))
}

func (s *Server) deletePoint(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	if err := s.store.DeletePoint(r.Context(), id); err != nil {
		var inUse store.InUseError
		if errors.As(err, &inUse) {
			writeError(w, http.StatusConflict, err.Error()+": disable the point to keep its history")
			return
		}
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// inspectPoint asks the robot of a point's route to take it now; the robot answers the command
// and uploads the inspection (without a patrol).
func (s *Server) inspectPoint(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	p, err := s.store.GetPoint(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	if p.RouteID == nil {
		writeError(w, http.StatusConflict, "the point has no route, so no robot takes it")
		return
	}
	route, _, err := s.store.GetRoute(r.Context(), *p.RouteID)
	if err != nil {
		fail(w, err)
		return
	}
	args := map[string]any{"point": s.planPoint(s.siteSettings(r.Context()), p)}
	res, err := s.commandAndWait(route.RobotID, "inspect_point", args)
	s.replyAck(w, r, "substation.inspect", route.RobotID, map[string]any{"point_id": p.ID}, res, err)
}

// pointTrend returns a point's readings over time for charts: per capture, the hottest temperature
// of each target (or the pd level under "pd"), the ambient and the rating.
func (s *Server) pointTrend(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	p, err := s.store.GetPoint(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	from, to := timeRange(r)
	if r.URL.Query().Get("from") == "" {
		from = to.AddDate(0, 0, -30)
	}
	list, err := s.store.ListInspections(r.Context(), store.InspectionFilter{PointID: id, From: from, To: to,
		Limit: 2000, Ascending: true})
	if err != nil {
		fail(w, err)
		return
	}
	type series struct {
		Key   string `json:"key"`
		Label string `json:"label"`
	}
	type sample struct {
		InspectionID int64              `json:"inspection_id"`
		TakenAt      time.Time          `json:"taken_at"`
		AmbientC     *float64           `json:"ambient_c"`
		Priority     int                `json:"priority"`
		Values       map[string]float64 `json:"values"`
	}
	keys := []series{}
	if p.Kind == substation.KindPD {
		keys = append(keys, series{"pd", p.Name})
	}
	for _, t := range p.Targets {
		keys = append(keys, series{t.Key, t.Label})
	}
	samples := make([]sample, 0, len(list))
	for _, in := range list {
		sm := sample{InspectionID: in.ID, TakenAt: in.TakenAt, AmbientC: in.AmbientC, Priority: in.Priority,
			Values: map[string]float64{}}
		for _, res := range in.Thermal {
			sm.Values[res.Key] = res.MaxC
		}
		if in.PD != nil {
			sm.Values["pd"] = in.PD.DB
		}
		samples = append(samples, sm)
	}
	writeJSON(w, http.StatusOK, map[string]any{"point_id": p.ID, "kind": p.Kind, "series": keys, "samples": samples,
		"from": from, "to": to})
}

// ---- inspections and findings

func intQuery(r *http.Request, name string) int64 {
	v, _ := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	return v
}

func (s *Server) listInspections(w http.ResponseWriter, r *http.Request) {
	from, to := timeRange(r)
	list, err := s.store.ListInspections(r.Context(), store.InspectionFilter{PointID: intQuery(r, "point_id"),
		PatrolID: intQuery(r, "patrol_id"), RobotID: r.URL.Query().Get("robot_id"), From: from, To: to,
		MaxPriority: int(intQuery(r, "priority")), Limit: int(intQuery(r, "limit"))})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getInspection(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	in, err := s.store.GetInspection(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) inspectionImage(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	in, err := s.store.GetInspection(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	path := in.Images[r.PathValue("name")]
	if path == "" {
		writeError(w, http.StatusNotFound, "no such image")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if filepath.Ext(path) == ".png" {
		w.Header().Set("Content-Type", "image/png")
	} else {
		w.Header().Set("Content-Type", "image/jpeg")
	}
	http.ServeFile(w, r, path)
}

func (s *Server) findingFilter(r *http.Request) (store.FindingFilter, error) {
	q := r.URL.Query()
	f := store.FindingFilter{Status: q.Get("status"), MaxPriority: int(intQuery(r, "priority")),
		AssetID: intQuery(r, "asset_id"), PointID: intQuery(r, "point_id"), Limit: int(intQuery(r, "limit"))}
	if f.Status != "" && !slices.Contains([]string{"open", "acknowledged", "resolved", "active"}, f.Status) {
		return f, errors.New("status: open, acknowledged, resolved or active")
	}
	if q.Get("from") != "" || q.Get("to") != "" {
		f.From, f.To = timeRange(r)
	}
	return f, nil
}

func (s *Server) listFindings(w http.ResponseWriter, r *http.Request) {
	f, err := s.findingFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	list, err := s.store.ListFindings(r.Context(), f)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getFinding(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	f, err := s.store.GetFinding(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// changeFinding acknowledges or resolves a finding with an optional note.
func (s *Server) changeFinding(resolve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := pathID(r)
		var body struct {
			Note string `json:"note"`
		}
		if err := decode(r, &body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		body.Note = strings.TrimSpace(body.Note)
		if runes(body.Note) > 2000 {
			writeError(w, http.StatusBadRequest, "note: up to 2000 characters")
			return
		}
		user := userOf(r).Username
		var err error
		if resolve {
			err = s.store.ResolveFinding(r.Context(), id, user, body.Note)
		} else {
			err = s.store.AcknowledgeFinding(r.Context(), id, user, body.Note)
		}
		if err != nil {
			substationFail(w, err)
			return
		}
		f, err := s.store.GetFinding(r.Context(), id)
		if err != nil {
			fail(w, err)
			return
		}
		s.broadcastFinding(f, f.Status)
		writeJSON(w, http.StatusOK, f)
	}
}

// broadcastFinding tells dashboards that a finding changed.
func (s *Server) broadcastFinding(f store.Finding, event string) {
	s.hub.Broadcast(map[string]any{"type": "finding", "id": f.ID, "event": event, "status": f.Status,
		"priority": f.Priority, "last_priority": f.LastPriority, "point_id": f.PointID, "asset_id": f.AssetID})
}
