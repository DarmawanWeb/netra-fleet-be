// Package i18n holds the server-side texts (reports, notifications) in English and Indonesian.
package i18n

// Lang returns a supported language code, English by default.
func Lang(code string) string {
	if code == "id" {
		return "id"
	}
	return "en"
}

var messages = map[string]map[string]string{
	"en": {
		"generated": "Generated", "page": "Page", "no_data": "No data",
		"patrol_report": "Patrol report #%d", "summary_report": "Operations summary",
		"summary": "Summary", "anomalies": "Anomalies", "timeline": "Timeline", "patrols": "Patrols",
		"anomalies_by_category": "Anomalies by category", "daily_crowd": "Daily crowd peak",
		"status": "Status", "robot": "Robot", "started": "Started", "finished": "Finished", "duration": "Duration",
		"laps": "Laps", "stops_inspected": "Stops inspected", "stops_skipped": "Stops skipped", "crowd_peak": "Crowd peak",
		"time": "Time", "category": "Category", "position": "Position", "event": "Event", "detail": "Detail",
		"count": "Count", "day": "Day", "peak_people": "Peak people", "all": "All", "id": "ID",
		"early_detection": "Early detection", "early_confidence": "Early confidence", "final_detection": "Final detection",
		"final_confidence": "Final confidence", "note": "Note", "validated_by": "Validated by",
		"detected_by": "<b>%s</b> detected by %s", "unknown_position": "position unknown",
		"queued": "Queued", "analyzing": "Analyzing", "needs_validation": "Needs validation", "confirmed": "Confirmed",
		"rejected": "Rejected", "dismissed": "Dismissed", "requested": "Requested", "running": "Running",
		"stopped": "Stopped", "failed": "Failed", "patrol_at": "Patrol %s",
	},
	"id": {
		"generated": "Dibuat", "page": "Halaman", "no_data": "Tidak ada data",
		"patrol_report": "Laporan patroli #%d", "summary_report": "Ringkasan operasi",
		"summary": "Ringkasan", "anomalies": "Anomali", "timeline": "Linimasa", "patrols": "Patroli",
		"anomalies_by_category": "Anomali per kategori", "daily_crowd": "Puncak keramaian harian",
		"status": "Status", "robot": "Robot", "started": "Mulai", "finished": "Selesai", "duration": "Durasi",
		"laps": "Putaran", "stops_inspected": "Titik diinspeksi", "stops_skipped": "Titik dilewati", "crowd_peak": "Puncak keramaian",
		"time": "Waktu", "category": "Kategori", "position": "Posisi", "event": "Kejadian", "detail": "Detail",
		"count": "Jumlah", "day": "Hari", "peak_people": "Puncak orang", "all": "Semua", "id": "ID",
		"early_detection": "Deteksi awal", "early_confidence": "Yakin awal", "final_detection": "Deteksi final",
		"final_confidence": "Yakin final", "note": "Keterangan", "validated_by": "Divalidasi oleh",
		"detected_by": "<b>%s</b> terdeteksi oleh %s", "unknown_position": "posisi tidak diketahui",
		"queued": "Antri", "analyzing": "Dianalisis", "needs_validation": "Perlu validasi", "confirmed": "Terkonfirmasi",
		"rejected": "Ditolak", "dismissed": "Diabaikan", "requested": "Dikirim", "running": "Berjalan",
		"stopped": "Dihentikan", "failed": "Gagal", "patrol_at": "Patroli %s",
	},
}

// categoryLabels translate the built-in anomaly categories; custom categories use their rule label.
var categoryLabels = map[string]map[string]string{
	"en": {"trash": "Trash", "spill": "Spill", "floor_damage": "Floor damage", "fallen_person": "Fallen person", "unknown": "Other"},
	"id": {"trash": "Sampah", "spill": "Tumpahan", "floor_damage": "Lantai rusak", "fallen_person": "Orang terjatuh", "unknown": "Lainnya"},
}

// T returns the text for key in lang (falls back to English, then to the key).
func T(lang, key string) string {
	if s, ok := messages[Lang(lang)][key]; ok {
		return s
	}
	if s, ok := messages["en"][key]; ok {
		return s
	}
	return key
}

// Category returns a category label: translated for built-in codes, otherwise the rule label.
func Category(lang, code, ruleLabel string) string {
	if s, ok := categoryLabels[Lang(lang)][code]; ok {
		return s
	}
	if ruleLabel != "" {
		return ruleLabel
	}
	return code
}
