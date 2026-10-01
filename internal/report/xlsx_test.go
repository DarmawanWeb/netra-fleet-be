package report

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestWriteXLSX(t *testing.T) {
	var buf bytes.Buffer
	sheets := []Sheet{{Name: "Anomalies", Header: []string{"ID", "Time", "Category", "Confidence"},
		Rows: [][]any{{int64(1), time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC), "Trash <A&B>", 0.82}}}}
	if err := WriteXLSX(&buf, sheets); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, f := range z.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		found[f.Name] = string(b)
	}
	sheet := found["xl/worksheets/sheet1.xml"]
	for _, want := range []string{"Trash &lt;A&amp;B&gt;", "<v>0.82</v>", "2026-10-02 09:00:00", `autoFilter ref="A1:D2"`} {
		if !strings.Contains(sheet, want) {
			t.Errorf("sheet missing %q", want)
		}
	}
	if _, ok := found["[Content_Types].xml"]; !ok || column(27) != "AB" {
		t.Error("workbook structure")
	}
}
