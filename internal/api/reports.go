package api

import (
	"bytes"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/DarmawanRobotics/netra-fleet-be/internal/report"
	"github.com/DarmawanRobotics/netra-fleet-be/internal/store"
)

var statusLabel = map[string]string{
	"queued": "Antri", "analyzing": "Dianalisis", "needs_validation": "Perlu validasi", "confirmed": "Terkonfirmasi",
	"rejected": "Ditolak", "dismissed": "Diabaikan", "requested": "Dikirim", "running": "Berjalan",
	"finished": "Selesai", "stopped": "Dihentikan", "failed": "Gagal",
}

func (s *Server) local(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.In(s.cfg.TimeZone).Format("02 Jan 2006 15:04")
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
	return fmt.Sprintf("%dj %02dm", int(d.Hours()), int(d.Minutes())%60)
}

func (s *Server) ruleLabels(r *http.Request) map[string]string {
	labels := map[string]string{}
	if rules, err := s.store.ListRules(r.Context()); err == nil {
		for _, rule := range rules {
			labels[rule.Category] = rule.Label
		}
	}
	return labels
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
	labels := s.ruleLabels(r)
	routeName := "-"
	if p.RouteID != nil {
		if route, _, err := s.store.GetRoute(r.Context(), *p.RouteID); err == nil {
			routeName = route.Name
		}
	}
	rows, images := [][]string{}, []report.Image{}
	for _, a := range anomalies {
		label := labels[category(a)]
		rows = append(rows, []string{s.local(&a.DetectedAt), label, statusLabel[a.Status], position(a)})
		if a.Status != "rejected" && a.Status != "dismissed" && len(images) < 12 {
			images = append(images, report.Image{Path: a.ImagePath, Caption: label + " · " + a.DetectedAt.In(s.cfg.TimeZone).Format("15:04")})
		}
	}
	timeline := [][]string{}
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		timeline = append(timeline, []string{e.TS.In(s.cfg.TimeZone).Format("15:04:05"), e.Type, compact(string(e.Payload), 90)})
	}
	stops, _ := recap["events"].(map[string]int)
	doc := report.Document{
		Title:     fmt.Sprintf("Laporan Patroli #%d", p.ID),
		Subtitle:  fmt.Sprintf("%s · %s · %s", p.RobotID, routeName, s.local(p.StartedAt)),
		Generated: time.Now().In(s.cfg.TimeZone),
		Sections: []report.Section{
			{Title: "Ringkasan", Facts: [][2]string{
				{"Status", statusLabel[p.Status]}, {"Robot", p.RobotID},
				{"Mulai", s.local(p.StartedAt)}, {"Selesai", s.local(p.FinishedAt)},
				{"Durasi", duration(p.StartedAt, p.FinishedAt)}, {"Putaran", fmt.Sprint(p.Laps)},
				{"Titik diinspeksi", fmt.Sprint(stops["inspected"])}, {"Titik dilewati", fmt.Sprint(stops["skipped"])},
				{"Anomali", fmt.Sprint(len(rows))}, {"Puncak keramaian", fmt.Sprint(recap["crowd_peak"])},
			}},
			{Title: "Anomali", Table: &report.Table{Header: []string{"Waktu", "Kategori", "Status", "Posisi"},
				Widths: []float64{38, 42, 35, 65}, Rows: rows}, Images: images},
			{Title: "Linimasa", Table: &report.Table{Header: []string{"Jam", "Kejadian", "Detail"},
				Widths: []float64{22, 28, 130}, Rows: timeline}},
		},
	}
	var buf bytes.Buffer
	if err := report.WritePDF(&buf, doc); err != nil {
		fail(w, err)
		return
	}
	send(w, fmt.Sprintf("patroli-%d.pdf", p.ID), "application/pdf", buf.Bytes())
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
	labels := s.ruleLabels(r)

	byCategory := map[string]int{}
	for _, a := range anomalies {
		if a.Status != "rejected" && a.Status != "dismissed" {
			byCategory[labels[category(a)]]++
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
			duration(p.StartedAt, p.FinishedAt), fmt.Sprint(p.Laps), statusLabel[p.Status]})
	}
	crowdRows := [][]string{}
	for _, t := range timeline {
		crowdRows = append(crowdRows, []string{t.Bucket.In(s.cfg.TimeZone).Format("Mon, 02 Jan 2006"), fmt.Sprint(t.Peak)})
	}

	if r.URL.Query().Get("format") == "xlsx" {
		sheets := []report.Sheet{
			{Name: "Patroli", Header: []string{"ID", "Robot", "Mulai", "Durasi", "Putaran", "Status"}, Rows: anyRows(patrolRows)},
			{Name: "Anomali per kategori", Header: []string{"Kategori", "Jumlah"}, Rows: anyRows(catRows)},
			{Name: "Keramaian harian", Header: []string{"Hari", "Puncak orang"}, Rows: anyRows(crowdRows)},
		}
		var buf bytes.Buffer
		if err := report.WriteXLSX(&buf, sheets); err != nil {
			fail(w, err)
			return
		}
		send(w, "ringkasan-netra.xlsx", xlsxType, buf.Bytes())
		return
	}
	period := fmt.Sprintf("%s – %s", from.In(s.cfg.TimeZone).Format("02 Jan 2006"), to.In(s.cfg.TimeZone).Format("02 Jan 2006"))
	doc := report.Document{
		Title: "Laporan Ringkasan Operasi", Subtitle: period, Generated: time.Now().In(s.cfg.TimeZone),
		Sections: []report.Section{
			{Title: "Ringkasan", Facts: [][2]string{{"Patroli", fmt.Sprint(len(patrols))}, {"Selesai", fmt.Sprint(finished)},
				{"Anomali", fmt.Sprint(len(anomalies))}, {"Robot", map[bool]string{true: "Semua", false: robotID}[robotID == ""]}}},
			{Title: "Anomali per kategori", Table: &report.Table{Header: []string{"Kategori", "Jumlah"}, Widths: []float64{120, 60}, Rows: catRows}},
			{Title: "Keramaian harian (puncak)", Table: &report.Table{Header: []string{"Hari", "Puncak orang"}, Widths: []float64{120, 60}, Rows: crowdRows}},
			{Title: "Daftar patroli", Table: &report.Table{Header: []string{"ID", "Robot", "Mulai", "Durasi", "Putaran", "Status"},
				Widths: []float64{15, 30, 45, 30, 20, 40}, Rows: patrolRows}},
		},
	}
	var buf bytes.Buffer
	if err := report.WritePDF(&buf, doc); err != nil {
		fail(w, err)
		return
	}
	send(w, "ringkasan-netra.pdf", "application/pdf", buf.Bytes())
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
	labels := s.ruleLabels(r)
	rows := [][]any{}
	for _, a := range anomalies {
		final := any(nil)
		if a.FinalConfidence != nil {
			final = *a.FinalConfidence
		}
		rows = append(rows, []any{a.ID, a.DetectedAt.In(s.cfg.TimeZone), a.RobotID, labels[a.EarlyCategory], a.EarlyConfidence,
			labels[category(a)], final, statusLabel[a.Status], position(a), a.FinalNote + a.EarlyNote, deref(a.ValidatedBy)})
	}
	var buf bytes.Buffer
	if err := report.WriteXLSX(&buf, []report.Sheet{{Name: "Anomali", Header: []string{"ID", "Waktu", "Robot", "Deteksi awal",
		"Yakin awal", "Deteksi final", "Yakin final", "Status", "Posisi", "Keterangan", "Divalidasi oleh"}, Rows: rows}}); err != nil {
		fail(w, err)
		return
	}
	send(w, "anomali.xlsx", xlsxType, buf.Bytes())
}

func (s *Server) patrolsExport(w http.ResponseWriter, r *http.Request) {
	from, to := timeRange(r)
	patrols, err := s.store.ListPatrols(r.Context(), r.URL.Query().Get("robot_id"), from, to)
	if err != nil {
		fail(w, err)
		return
	}
	rows := [][]any{}
	for _, p := range patrols {
		rows = append(rows, []any{p.ID, p.RobotID, s.local(p.StartedAt), s.local(p.FinishedAt), duration(p.StartedAt, p.FinishedAt),
			p.Laps, statusLabel[p.Status], p.Message})
	}
	var buf bytes.Buffer
	if err := report.WriteXLSX(&buf, []report.Sheet{{Name: "Patroli", Header: []string{"ID", "Robot", "Mulai", "Selesai",
		"Durasi", "Putaran", "Status", "Catatan"}, Rows: rows}}); err != nil {
		fail(w, err)
		return
	}
	send(w, "patroli.xlsx", xlsxType, buf.Bytes())
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
