package api

import (
	"bytes"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/i18n"
	"github.com/DarmawanWeb/netra-fleet-be/internal/report"
	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
)

func (s *Server) local(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.In(s.cfg.TimeZone).Format("02 Jan 2006 15:04")
}

func pdfLabels(lang string) report.Labels {
	return report.Labels{Generated: i18n.T(lang, "generated"), Page: i18n.T(lang, "page"), NoData: i18n.T(lang, "no_data")}
}

func duration(from, to *time.Time) string {
	if from == nil {
		return "-"
	}
	end := time.Now()
	if to != nil {
		end = *to
	}
	d := end.Sub(*from).Round(time.Minute)
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

// labeler returns the request language and a function translating anomaly category codes.
func (s *Server) labeler(r *http.Request) (string, func(string) string) {
	lang := i18n.Lang(r.URL.Query().Get("lang"))
	rules := map[string]string{}
	if list, err := s.store.ListRules(r.Context()); err == nil {
		for _, rule := range list {
			rules[rule.Category] = rule.Label
		}
	}
	return lang, func(code string) string { return i18n.Category(lang, code, rules[code]) }
}

func category(a store.Anomaly) string {
	if a.FinalCategory != nil {
		return *a.FinalCategory
	}
	return a.EarlyCategory
}

func position(a store.Anomaly) string {
	if a.X == nil || a.Y == nil {
		return "-"
	}
	return fmt.Sprintf("x %.1f m, y %.1f m", *a.X, *a.Y)
}

func send(w http.ResponseWriter, name, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	_, _ = w.Write(body)
}

const xlsxType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

func (s *Server) patrolReportPDF(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	p, err := s.store.GetPatrol(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	recap, _ := s.store.PatrolRecap(r.Context(), id)
	anomalies, _ := s.store.ListAnomalies(r.Context(), "", "", "", id, time.Unix(0, 0), time.Now().Add(time.Hour))
	events, _ := s.store.ListEvents(r.Context(), "", id)
	lang, label := s.labeler(r)
	t := func(key string) string { return i18n.T(lang, key) }
	routeName := "-"
	if p.RouteID != nil {
		if route, _, err := s.store.GetRoute(r.Context(), *p.RouteID); err == nil {
			routeName = route.Name
		}
	}
	rows, images := [][]string{}, []report.Image{}
	for _, a := range anomalies {
		cat := label(category(a))
		rows = append(rows, []string{s.local(&a.DetectedAt), cat, t(a.Status), position(a)})
		if a.Status != "rejected" && a.Status != "dismissed" && len(images) < 12 {
			images = append(images, report.Image{Path: a.ImagePath, Caption: cat + " · " + a.DetectedAt.In(s.cfg.TimeZone).Format("15:04")})
		}
	}
	timeline := [][]string{}
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		timeline = append(timeline, []string{e.TS.In(s.cfg.TimeZone).Format("15:04:05"), e.Type, compact(string(e.Payload), 90)})
	}
	stops, _ := recap["events"].(map[string]int)
	doc := report.Document{
		Title:     fmt.Sprintf(t("patrol_report"), p.ID),
		Subtitle:  fmt.Sprintf("%s · %s · %s", p.RobotID, routeName, s.local(p.StartedAt)),
		Generated: time.Now().In(s.cfg.TimeZone),
		Labels:    pdfLabels(lang),
		Sections: []report.Section{
			{Title: t("summary"), Facts: [][2]string{
				{t("status"), t(p.Status)}, {t("robot"), p.RobotID},
				{t("started"), s.local(p.StartedAt)}, {t("finished"), s.local(p.FinishedAt)},
				{t("duration"), duration(p.StartedAt, p.FinishedAt)}, {t("laps"), fmt.Sprint(p.Laps)},
				{t("stops_inspected"), fmt.Sprint(stops["inspected"])}, {t("stops_skipped"), fmt.Sprint(stops["skipped"])},
				{t("anomalies"), fmt.Sprint(len(rows))}, {t("crowd_peak"), fmt.Sprint(recap["crowd_peak"])},
			}},
			{Title: t("anomalies"), Table: &report.Table{Header: []string{t("time"), t("category"), t("status"), t("position")},
				Widths: []float64{38, 42, 35, 65}, Rows: rows}, Images: images},
			{Title: t("timeline"), Table: &report.Table{Header: []string{t("time"), t("event"), t("detail")},
				Widths: []float64{22, 28, 130}, Rows: timeline}},
		},
	}
	var buf bytes.Buffer
	if err := report.WritePDF(&buf, doc); err != nil {
		fail(w, err)
		return
	}
	send(w, fmt.Sprintf("patrol-%d.pdf", p.ID), "application/pdf", buf.Bytes())
}

func compact(s string, n int) string {
	s = strings.NewReplacer(`"`, "", "{", "", "}", "").Replace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func (s *Server) summaryReport(w http.ResponseWriter, r *http.Request) {
	from, to := timeRange(r)
	robotID := r.URL.Query().Get("robot_id")
	patrols, err := s.store.ListPatrols(r.Context(), robotID, from, to)
	if err != nil {
		fail(w, err)
		return
	}
	anomalies, _ := s.store.ListAnomalies(r.Context(), "", "", robotID, 0, from, to)
	timeline, _ := s.store.CrowdTimeline(r.Context(), robotID, "day", s.cfg.TimeZone.String(), from, to)
	lang, label := s.labeler(r)
	t := func(key string) string { return i18n.T(lang, key) }

	byCategory := map[string]int{}
	for _, a := range anomalies {
		if a.Status != "rejected" && a.Status != "dismissed" {
			byCategory[label(category(a))]++
		}
	}
	categories := make([]string, 0, len(byCategory))
	for c := range byCategory {
		categories = append(categories, c)
	}
	sort.Strings(categories)
	catRows := [][]string{}
	for _, c := range categories {
		catRows = append(catRows, []string{c, fmt.Sprint(byCategory[c])})
	}
	patrolRows := [][]string{}
	finished := 0
	for _, p := range patrols {
		if p.Status == "finished" {
			finished++
		}
		patrolRows = append(patrolRows, []string{fmt.Sprint(p.ID), p.RobotID, s.local(p.StartedAt),
			duration(p.StartedAt, p.FinishedAt), fmt.Sprint(p.Laps), t(p.Status)})
	}
	crowdRows := [][]string{}
	for _, t := range timeline {
		crowdRows = append(crowdRows, []string{t.Bucket.In(s.cfg.TimeZone).Format("Mon, 02 Jan 2006"), fmt.Sprint(t.Peak)})
	}

	if r.URL.Query().Get("format") == "xlsx" {
		patrolHeader := []string{t("id"), t("robot"), t("started"), t("duration"), t("laps"), t("status")}
		sheets := []report.Sheet{
			{Name: t("patrols"), Header: patrolHeader, Rows: anyRows(patrolRows)},
			{Name: t("anomalies_by_category"), Header: []string{t("category"), t("count")}, Rows: anyRows(catRows)},
			{Name: t("daily_crowd"), Header: []string{t("day"), t("peak_people")}, Rows: anyRows(crowdRows)},
		}
		var buf bytes.Buffer
		if err := report.WriteXLSX(&buf, sheets); err != nil {
			fail(w, err)
			return
		}
		send(w, "netra-summary.xlsx", xlsxType, buf.Bytes())
		return
	}
	period := fmt.Sprintf("%s – %s", from.In(s.cfg.TimeZone).Format("02 Jan 2006"), to.In(s.cfg.TimeZone).Format("02 Jan 2006"))
	robotLabel := robotID
	if robotID == "" {
		robotLabel = t("all")
	}
	doc := report.Document{
		Title: t("summary_report"), Subtitle: period, Generated: time.Now().In(s.cfg.TimeZone), Labels: pdfLabels(lang),
		Sections: []report.Section{
			{Title: t("summary"), Facts: [][2]string{{t("patrols"), fmt.Sprint(len(patrols))}, {t("finished"), fmt.Sprint(finished)},
				{t("anomalies"), fmt.Sprint(len(anomalies))}, {t("robot"), robotLabel}}},
			{Title: t("anomalies_by_category"), Table: &report.Table{Header: []string{t("category"), t("count")},
				Widths: []float64{120, 60}, Rows: catRows}},
			{Title: t("daily_crowd"), Table: &report.Table{Header: []string{t("day"), t("peak_people")},
				Widths: []float64{120, 60}, Rows: crowdRows}},
			{Title: t("patrols"), Table: &report.Table{Header: []string{t("id"), t("robot"), t("started"), t("duration"), t("laps"),
				t("status")}, Widths: []float64{15, 30, 45, 30, 20, 40}, Rows: patrolRows}},
		},
	}
	var buf bytes.Buffer
	if err := report.WritePDF(&buf, doc); err != nil {
		fail(w, err)
		return
	}
	send(w, "netra-summary.pdf", "application/pdf", buf.Bytes())
}

func anyRows(rows [][]string) [][]any {
	out := make([][]any, len(rows))
	for i, row := range rows {
		out[i] = make([]any, len(row))
		for j, v := range row {
			out[i][j] = v
		}
	}
	return out
}

func (s *Server) anomaliesExport(w http.ResponseWriter, r *http.Request) {
	from, to := timeRange(r)
	q := r.URL.Query()
	anomalies, err := s.store.ListAnomalies(r.Context(), q.Get("status"), q.Get("category"), q.Get("robot_id"), 0, from, to)
	if err != nil {
		fail(w, err)
		return
	}
	lang, label := s.labeler(r)
	t := func(key string) string { return i18n.T(lang, key) }
	rows := [][]any{}
	for _, a := range anomalies {
		final := any(nil)
		if a.FinalConfidence != nil {
			final = *a.FinalConfidence
		}
		rows = append(rows, []any{a.ID, a.DetectedAt.In(s.cfg.TimeZone), a.RobotID, label(a.EarlyCategory), a.EarlyConfidence,
			label(category(a)), final, t(a.Status), position(a), a.FinalNote + a.EarlyNote, deref(a.ValidatedBy)})
	}
	var buf bytes.Buffer
	header := []string{t("id"), t("time"), t("robot"), t("early_detection"), t("early_confidence"), t("final_detection"),
		t("final_confidence"), t("status"), t("position"), t("note"), t("validated_by")}
	if err := report.WriteXLSX(&buf, []report.Sheet{{Name: t("anomalies"), Header: header, Rows: rows}}); err != nil {
		fail(w, err)
		return
	}
	send(w, "anomalies.xlsx", xlsxType, buf.Bytes())
}

func (s *Server) patrolsExport(w http.ResponseWriter, r *http.Request) {
	from, to := timeRange(r)
	patrols, err := s.store.ListPatrols(r.Context(), r.URL.Query().Get("robot_id"), from, to)
	if err != nil {
		fail(w, err)
		return
	}
	lang := i18n.Lang(r.URL.Query().Get("lang"))
	t := func(key string) string { return i18n.T(lang, key) }
	rows := [][]any{}
	for _, p := range patrols {
		rows = append(rows, []any{p.ID, p.RobotID, s.local(p.StartedAt), s.local(p.FinishedAt), duration(p.StartedAt, p.FinishedAt),
			p.Laps, t(p.Status), p.Message})
	}
	header := []string{t("id"), t("robot"), t("started"), t("finished"), t("duration"), t("laps"), t("status"), t("note")}
	var buf bytes.Buffer
	if err := report.WriteXLSX(&buf, []report.Sheet{{Name: t("patrols"), Header: header, Rows: rows}}); err != nil {
		fail(w, err)
		return
	}
	send(w, "patrols.xlsx", xlsxType, buf.Bytes())
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
