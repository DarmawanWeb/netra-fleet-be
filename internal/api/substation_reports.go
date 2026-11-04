package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/i18n"
	"github.com/DarmawanWeb/netra-fleet-be/internal/report"
	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
	"github.com/DarmawanWeb/netra-fleet-be/internal/substation"
)

// num is a nullable number as an Excel cell.
func num(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func priorityText(t func(string) string, p int) string { return t("priority_" + strconv.Itoa(p)) }

// assetTags maps asset ids to their tags, archived assets included.
func (s *Server) assetTags(ctx context.Context) map[int64]string {
	out := map[int64]string{}
	if assets, err := s.store.ListAssets(ctx, true); err == nil {
		for _, a := range assets {
			out[a.ID] = a.Tag
		}
	}
	return out
}

// findingsExport is the Excel list of findings, with the filters of GET /substation/findings.
func (s *Server) findingsExport(w http.ResponseWriter, r *http.Request) {
	f, err := s.findingFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f.Limit = 5000
	list, err := s.store.ListFindings(r.Context(), f)
	if err != nil {
		fail(w, err)
		return
	}
	lang := i18n.Lang(r.URL.Query().Get("lang"))
	t := func(key string) string { return i18n.T(lang, key) }
	rows := make([][]any, 0, len(list))
	for _, fd := range list {
		rows = append(rows, []any{fd.ID, fd.Priority, priorityText(t, fd.Priority), t(fd.Status), deref(fd.AssetTag),
			deref(fd.AssetName), fd.PointName, fd.Label, t(fd.Kind), t("basis_" + fd.Basis), num(fd.LastValue),
			num(fd.LastDeltaC), fd.FirstSeen.In(s.cfg.TimeZone), fd.LastSeen.In(s.cfg.TimeZone), fd.Occurrences,
			deref(fd.AcknowledgedBy), deref(fd.ResolvedBy), fd.Note})
	}
	header := []string{t("id"), t("priority"), t("action"), t("status"), t("asset"), t("name"), t("point"), t("target"),
		t("kind"), t("basis"), t("value"), t("delta_t"), t("first_seen"), t("last_seen"), t("occurrences"),
		t("acknowledged_by"), t("resolved_by"), t("note")}
	var buf bytes.Buffer
	if err := report.WriteXLSX(&buf, []report.Sheet{{Name: t("findings"), Header: header, Rows: rows}}); err != nil {
		fail(w, err)
		return
	}
	send(w, "findings.xlsx", xlsxType, buf.Bytes())
}

// inspectionsExport is the Excel list of readings in a period: a row per thermal target or pd reading.
func (s *Server) inspectionsExport(w http.ResponseWriter, r *http.Request) {
	from, to := timeRange(r)
	list, err := s.store.ListInspections(r.Context(), store.InspectionFilter{PointID: intQuery(r, "point_id"),
		PatrolID: intQuery(r, "patrol_id"), RobotID: r.URL.Query().Get("robot_id"), From: from, To: to,
		MaxPriority: int(intQuery(r, "priority")), Limit: 5000, Ascending: true})
	if err != nil {
		fail(w, err)
		return
	}
	lang := i18n.Lang(r.URL.Query().Get("lang"))
	t := func(key string) string { return i18n.T(lang, key) }
	tags := s.assetTags(r.Context())
	tag := func(id *int64) string {
		if id == nil {
			return ""
		}
		return tags[*id]
	}
	yes := func(b bool) string {
		if b {
			return t("yes")
		}
		return t("no")
	}
	rows := [][]any{}
	for _, in := range list {
		at := in.TakenAt.In(s.cfg.TimeZone)
		robot, patrol := deref(in.RobotID), any(nil)
		if in.PatrolID != nil {
			patrol = *in.PatrolID
		}
		if in.PD != nil {
			rows = append(rows, []any{at, in.PointName, t("pd"), "", t("pd"), in.PD.DB, nil, nil, nil, in.PD.Priority,
				t("basis_pd"), yes(in.PD.Detected), in.PD.Kind, robot, patrol})
		}
		for _, res := range in.Thermal {
			rows = append(rows, []any{at, in.PointName, res.Label, tag(res.AssetID), t("thermal"), res.MaxC,
				num(in.AmbientC), num(res.DeltaSimilarC), num(res.DeltaAmbientC), res.Priority, basisText(t, res.Basis), "",
				"", robot, patrol})
		}
	}
	header := []string{t("time"), t("point"), t("target"), t("asset"), t("kind"), t("value"), t("ambient_c"),
		t("delta_similar"), t("delta_ambient"), t("priority"), t("basis"), t("detected"), t("pd_kind"), t("robot"),
		t("patrol")}
	var buf bytes.Buffer
	if err := report.WriteXLSX(&buf, []report.Sheet{{Name: t("readings"), Header: header, Rows: rows}}); err != nil {
		fail(w, err)
		return
	}
	send(w, "readings.xlsx", xlsxType, buf.Bytes())
}

func basisText(t func(string) string, basis string) string {
	if basis == "" {
		return ""
	}
	return t("basis_" + basis)
}

// inspectionSection is the patrol report's part on substation inspections: coverage of the route's
// plan, the readings, and the images of what was rated. Nil when the patrol inspected nothing and
// planned nothing.
func (s *Server) inspectionSection(ctx context.Context, lang string, patrolID int64) *report.Section {
	t := func(key string) string { return i18n.T(lang, key) }
	cov, err := s.store.PatrolCoverage(ctx, patrolID)
	if err != nil || (cov.Planned == 0 && cov.Inspections == 0) {
		return nil
	}
	list, err := s.store.ListInspections(ctx, store.InspectionFilter{PatrolID: patrolID, From: time.Unix(0, 0),
		To: time.Now().Add(time.Hour), Limit: 2000, Ascending: true})
	if err != nil {
		return nil
	}
	worst := t("priority_0")
	if cov.Priority != substation.None {
		worst = fmt.Sprintf("P%d · %s", cov.Priority, priorityText(t, cov.Priority))
	}
	sec := report.Section{Title: t("inspections"), Facts: [][2]string{
		{t("points_planned"), fmt.Sprint(cov.Planned)}, {t("points_inspected"), fmt.Sprint(cov.Done)},
		{t("points_missed"), fmt.Sprint(len(cov.Missed))}, {t("worst_priority"), worst},
	}}
	rows, images := [][]string{}, []report.Image{}
	rating := func(p int) string {
		if p == substation.None {
			return "-"
		}
		return fmt.Sprintf("P%d", p)
	}
	for _, in := range list {
		at := in.TakenAt.In(s.cfg.TimeZone).Format("15:04")
		if in.PD != nil {
			rows = append(rows, []string{at, in.PointName, t("pd"), fmt.Sprintf("%.1f dB", in.PD.DB), "-",
				rating(in.PD.Priority)})
		}
		for _, res := range in.Thermal {
			delta := "-"
			switch {
			case res.Basis == substation.BasisAmbient && res.DeltaAmbientC != nil:
				delta = fmt.Sprintf("%.1f", *res.DeltaAmbientC)
			case res.DeltaSimilarC != nil:
				delta = fmt.Sprintf("%.1f", *res.DeltaSimilarC)
			case res.DeltaAmbientC != nil:
				delta = fmt.Sprintf("%.1f", *res.DeltaAmbientC)
			}
			rows = append(rows, []string{at, in.PointName, res.Label, fmt.Sprintf("%.1f °C", res.MaxC), delta,
				rating(res.Priority)})
		}
		if in.Priority != substation.None && len(images) < 12 {
			path := in.Images["thermal"]
			if in.Kind == substation.KindPD {
				path = in.Images["acoustic"]
			}
			if path == "" {
				path = in.Images["visual"]
			}
			if path != "" {
				images = append(images, report.Image{Path: path,
					Caption: fmt.Sprintf("%s · P%d · %s", in.PointName, in.Priority, at)})
			}
		}
	}
	// The PDF fonts have no Δ: the rise column says so in words.
	sec.Table = &report.Table{Header: []string{t("time"), t("point"), t("target"), t("value"), t("rise_c"), t("priority")},
		Widths: []float64{16, 56, 44, 24, 18, 22}, Rows: rows}
	sec.Images = images
	return &sec
}
