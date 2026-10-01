package report

import (
	"fmt"
	"image"
	_ "image/jpeg" // decoders for decodable()
	_ "image/png"
	"io"
	"os"
	"time"

	"github.com/go-pdf/fpdf"
)

// Table is a simple bordered table.
type Table struct {
	Header []string
	Widths []float64 // mm, should add up to ~180
	Rows   [][]string
}

// Image is a captioned picture (JPEG/PNG file).
type Image struct {
	Path    string
	Caption string
}

// Section is a titled block with key/value facts, a table and pictures.
type Section struct {
	Title  string
	Facts  [][2]string
	Table  *Table
	Images []Image
}

// Document is a branded A4 report.
type Document struct {
	Title     string
	Subtitle  string
	Sections  []Section
	Generated time.Time
	Labels    Labels
}

// Labels are the fixed texts of the page frame.
type Labels struct {
	Generated, Page, NoData string
}

const pageWidth = 180.0

// WritePDF renders the document.
func WritePDF(w io.Writer, d Document) error {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(15, 18, 15)
	pdf.SetAutoPageBreak(true, 18)
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pdf.SetHeaderFunc(func() {
		pdf.SetFillColor(10, 16, 28)
		pdf.Rect(0, 0, 210, 12, "F")
		pdf.SetFont("Helvetica", "B", 10)
		pdf.SetTextColor(34, 211, 238)
		pdf.SetXY(15, 3.5)
		pdf.CellFormat(90, 5, "NETRA", "", 0, "L", false, 0, "")
		pdf.SetTextColor(200, 210, 220)
		pdf.SetFont("Helvetica", "", 8)
		pdf.CellFormat(90, 5, tr("Patrol Command Center"), "", 0, "R", false, 0, "")
		pdf.SetY(18)
		pdf.SetTextColor(20, 20, 20)
	})
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont("Helvetica", "", 7)
		pdf.SetTextColor(120, 120, 120)
		pdf.CellFormat(90, 5, tr(or(d.Labels.Generated, "Generated")+" "+d.Generated.Format("02 Jan 2006 15:04")), "", 0, "L", false, 0, "")
		pdf.CellFormat(90, 5, fmt.Sprintf("%s %d/{nb}", or(d.Labels.Page, "Page"), pdf.PageNo()), "", 0, "R", false, 0, "")
	})
	pdf.AliasNbPages("")
	pdf.AddPage()
	pdf.SetFont("Helvetica", "B", 18)
	pdf.MultiCell(pageWidth, 8, tr(d.Title), "", "L", false)
	if d.Subtitle != "" {
		pdf.SetFont("Helvetica", "", 10)
		pdf.SetTextColor(90, 90, 90)
		pdf.MultiCell(pageWidth, 5, tr(d.Subtitle), "", "L", false)
		pdf.SetTextColor(20, 20, 20)
	}
	pdf.Ln(3)
	for _, s := range d.Sections {
		writeSection(pdf, tr, s, or(d.Labels.NoData, "No data"))
	}
	if err := pdf.Error(); err != nil {
		return err
	}
	return pdf.Output(w)
}

func writeSection(pdf *fpdf.Fpdf, tr func(string) string, s Section, noData string) {
	if pdf.GetY() > 250 {
		pdf.AddPage()
	}
	pdf.Ln(2)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.SetTextColor(14, 116, 144)
	pdf.CellFormat(pageWidth, 7, tr(s.Title), "B", 1, "L", false, 0, "")
	pdf.SetTextColor(20, 20, 20)
	pdf.Ln(1.5)
	for i := 0; i < len(s.Facts); i += 2 {
		for j := i; j < i+2 && j < len(s.Facts); j++ {
			pdf.SetFont("Helvetica", "", 8.5)
			pdf.SetTextColor(100, 100, 100)
			pdf.CellFormat(35, 5.5, tr(s.Facts[j][0]), "", 0, "L", false, 0, "")
			pdf.SetFont("Helvetica", "B", 9)
			pdf.SetTextColor(20, 20, 20)
			pdf.CellFormat(55, 5.5, tr(s.Facts[j][1]), "", 0, "L", false, 0, "")
		}
		pdf.Ln(5.5)
	}
	if s.Table != nil {
		pdf.Ln(1)
		writeTable(pdf, tr, *s.Table, noData)
	}
	if len(s.Images) > 0 {
		pdf.Ln(2)
		writeImages(pdf, tr, s.Images)
	}
	pdf.Ln(2)
}

func writeTable(pdf *fpdf.Fpdf, tr func(string) string, t Table, noData string) {
	widths := t.Widths
	if len(widths) != len(t.Header) {
		widths = make([]float64, len(t.Header))
		for i := range widths {
			widths[i] = pageWidth / float64(len(t.Header))
		}
	}
	header := func() {
		pdf.SetFont("Helvetica", "B", 8)
		pdf.SetFillColor(14, 116, 144)
		pdf.SetTextColor(255, 255, 255)
		for i, h := range t.Header {
			pdf.CellFormat(widths[i], 6.5, tr(h), "", 0, "L", true, 0, "")
		}
		pdf.Ln(-1)
		pdf.SetTextColor(20, 20, 20)
		pdf.SetFont("Helvetica", "", 8)
	}
	header()
	if len(t.Rows) == 0 {
		pdf.CellFormat(pageWidth, 6, tr(noData), "", 1, "L", false, 0, "")
		return
	}
	for r, row := range t.Rows {
		if pdf.GetY() > 270 {
			pdf.AddPage()
			header()
		}
		fill := r%2 == 1
		pdf.SetFillColor(240, 246, 248)
		for i := range t.Header {
			text := ""
			if i < len(row) {
				text = row[i]
			}
			pdf.CellFormat(widths[i], 6, tr(clip(pdf, text, widths[i]-2)), "", 0, "L", fill, 0, "")
		}
		pdf.Ln(-1)
	}
}

func clip(pdf *fpdf.Fpdf, s string, width float64) string {
	for pdf.GetStringWidth(s) > width && len(s) > 3 {
		s = s[:len(s)-4] + "..."
	}
	return s
}

func writeImages(pdf *fpdf.Fpdf, tr func(string) string, images []Image) {
	const w, h, gap = 58.0, 43.5, 3.0
	usable := images[:0:0]
	for _, img := range images {
		if decodable(img.Path) {
			usable = append(usable, img)
		}
	}
	if len(usable) == 0 {
		return
	}
	images = usable
	for i, img := range images {
		col := i % 3
		if col == 0 && i > 0 {
			pdf.SetY(pdf.GetY() + h + 9)
		}
		if pdf.GetY()+h+9 > 280 {
			pdf.AddPage()
		}
		x, y := 15+float64(col)*(w+gap), pdf.GetY()
		pdf.ImageOptions(img.Path, x, y, w, h, false, fpdf.ImageOptions{ReadDpi: false}, 0, "")
		pdf.SetXY(x, y+h+0.5)
		pdf.SetFont("Helvetica", "", 7)
		pdf.CellFormat(w, 4, tr(clip(pdf, img.Caption, w)), "", 0, "L", false, 0, "")
		pdf.SetXY(15, y)
	}
	pdf.SetY(pdf.GetY() + h + 9)
}

// decodable reports whether path is a readable JPEG or PNG (broken uploads must not break a report).
func decodable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	_, _, err = image.DecodeConfig(f)
	return err == nil
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
