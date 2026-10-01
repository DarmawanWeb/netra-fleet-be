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
		rows[i] = []string{"2026-10-02 09:00", "Sampah", "Terkonfirmasi", "x 5.0 m, y 0.1 m — très long text to clip"}
	}
	doc := Document{Title: "Laporan Patroli #12", Subtitle: "l1w-01 · Koridor Lt1", Generated: time.Now(),
		Sections: []Section{{Title: "Ringkasan", Facts: [][2]string{{"Durasi", "42 mnt"}, {"Putaran", "3"}, {"Anomali", "2"}}},
			{Title: "Anomali", Table: &Table{Header: []string{"Waktu", "Kategori", "Status", "Posisi"}, Widths: []float64{35, 35, 35, 75}, Rows: rows},
				Images: []Image{{Path: path, Caption: "Sampah · 09:00"}, {Path: path, Caption: "Tumpahan"}, {Path: "/missing.jpg"}, {Path: broken}, {Path: path, Caption: "4"}}}}}
	var buf bytes.Buffer
	if err := WritePDF(&buf, doc); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF")) || buf.Len() < 3000 {
		t.Fatalf("not a PDF (%d bytes)", buf.Len())
	}
	_ = os.WriteFile(filepath.Join(os.TempDir(), "netra_test_report.pdf"), buf.Bytes(), 0o644)
}
