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
		"priority_0": "Normal", "priority_1": "Major discrepancy; repair immediately",
		"priority_2": "Monitor until corrective measures can be accomplished",
		"priority_3": "Probable deficiency; repair as time permits", "priority_4": "Possible deficiency; warrants investigation",
		"basis_similar": "over a similar component", "basis_ambient": "over ambient", "basis_limit": "at or above its rated temperature",
		"basis_pd": "partial discharge", "pd_level": "Partial discharge",
		"inspections": "Inspections", "findings": "Findings", "readings": "Readings", "point": "Point", "target": "Target",
		"asset": "Asset", "bay": "Bay", "kind": "Kind", "max_c": "Max °C", "ambient_c": "Ambient °C",
		"delta_similar": "ΔT similar °C", "delta_ambient": "ΔT ambient °C", "delta_t": "ΔT °C", "priority": "Priority",
		"basis": "Basis", "value": "Value", "first_seen": "First seen", "last_seen": "Last seen", "occurrences": "Occurrences",
		"acknowledged_by": "Acknowledged by", "resolved_by": "Resolved by", "open": "Open", "acknowledged": "Acknowledged",
		"resolved": "Resolved", "points_planned": "Inspection points planned", "points_inspected": "Inspection points done",
		"points_missed": "Inspection points missed", "rise_c": "Rise °C", "worst_priority": "Most severe", "thermal": "Thermal", "pd": "Partial discharge",
		"detected": "Detected", "pd_kind": "Type", "yes": "Yes", "no": "No", "patrol": "Patrol",
		"findings_report": "Substation findings", "inspections_report": "Substation readings",
		"action": "Recommended action", "name": "Name",
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
		"priority_0": "Normal", "priority_1": "Ketidaksesuaian besar; perbaiki segera",
		"priority_2":    "Pantau sampai tindakan perbaikan dapat dilakukan",
		"priority_3":    "Kemungkinan ada kerusakan; perbaiki bila waktu memungkinkan",
		"priority_4":    "Mungkin ada kerusakan; perlu diselidiki",
		"basis_similar": "di atas komponen sejenis", "basis_ambient": "di atas suhu lingkungan",
		"basis_limit": "mencapai suhu batasnya", "basis_pd": "partial discharge", "pd_level": "Partial discharge",
		"inspections": "Inspeksi", "findings": "Temuan", "readings": "Pembacaan", "point": "Titik", "target": "Target",
		"asset": "Aset", "bay": "Bay", "kind": "Jenis", "max_c": "Maks °C", "ambient_c": "Lingkungan °C",
		"delta_similar": "ΔT sejenis °C", "delta_ambient": "ΔT lingkungan °C", "delta_t": "ΔT °C", "priority": "Prioritas",
		"basis": "Dasar", "value": "Nilai", "first_seen": "Pertama terlihat", "last_seen": "Terakhir terlihat",
		"occurrences": "Kemunculan", "acknowledged_by": "Ditangani oleh", "resolved_by": "Diselesaikan oleh",
		"open": "Terbuka", "acknowledged": "Ditangani", "resolved": "Selesai", "points_planned": "Titik inspeksi direncanakan",
		"points_inspected": "Titik inspeksi selesai", "points_missed": "Titik inspeksi terlewat", "rise_c": "Kenaikan °C", "worst_priority": "Paling parah",
		"thermal": "Termal", "pd": "Partial discharge", "detected": "Terdeteksi", "pd_kind": "Tipe", "yes": "Ya",
		"no": "Tidak", "patrol": "Patroli", "findings_report": "Temuan gardu induk",
		"inspections_report": "Pembacaan gardu induk", "action": "Tindakan yang disarankan", "name": "Nama",
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
