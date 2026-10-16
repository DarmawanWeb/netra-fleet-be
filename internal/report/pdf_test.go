package report

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWritePDF(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	img.Set(1, 1, color.White)
	path := filepath.Join(dir, "a.jpg")
	f, _ := os.Create(path)
	_ = jpeg.Encode(f, img, nil)
	f.Close()
	broken := filepath.Join(dir, "broken.jpg")
	_ = os.WriteFile(broken, []byte("\xff\xd8not a jpeg"), 0o644)
	rows := make([][]string, 80)
	for i := range rows {
		rows[i] = []string{"2026-10-02 09:00", "Trash", "Confirmed", "x 5.0 m, y 0.1 m — a very long text that must be clipped"}
	}
	doc := Document{Title: "Patrol report #12", Subtitle: "l1w-01 · Corridor L1", Generated: time.Now(),
		Sections: []Section{{Title: "Summary", Facts: [][2]string{{"Duration", "42 min"}, {"Laps", "3"}, {"Anomalies", "2"}}},
			{Title: "Anomalies", Table: &Table{Header: []string{"Time", "Category", "Status", "Position"}, Widths: []float64{35, 35, 35, 75}, Rows: rows},
				Images: []Image{{Path: path, Caption: "Trash · 09:00"}, {Path: path, Caption: "Spill"}, {Path: "/missing.jpg"}, {Path: broken}, {Path: path, Caption: "4"}}}}}
	var buf bytes.Buffer
	if err := WritePDF(&buf, doc); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF")) || buf.Len() < 3000 {
		t.Fatalf("not a PDF (%d bytes)", buf.Len())
	}
	_ = os.WriteFile(filepath.Join(t.TempDir(), "netra_test_report.pdf"), buf.Bytes(), 0o600)
}
