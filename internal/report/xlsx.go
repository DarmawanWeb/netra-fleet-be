// Package report renders PDF and Excel exports.
package report

import (
	"archive/zip"
	"fmt"
	"html"
	"io"
	"strings"
	"time"
)

// Sheet is one worksheet: a bold header row followed by data rows.
type Sheet struct {
	Name   string
	Header []string
	Rows   [][]any
}

func column(i int) string {
	name := ""
	for i++; i > 0; i = (i - 1) / 26 {
		name = string(rune('A'+(i-1)%26)) + name
	}
	return name
}

func cell(ref string, v any, style int) string {
	switch x := v.(type) {
	case nil:
		return ""
	case int:
		return fmt.Sprintf(`<c r="%s" s="%d"><v>%d</v></c>`, ref, style, x)
	case int64:
		return fmt.Sprintf(`<c r="%s" s="%d"><v>%d</v></c>`, ref, style, x)
	case float64:
		return fmt.Sprintf(`<c r="%s" s="%d"><v>%g</v></c>`, ref, style, x)
	case time.Time:
		return cell(ref, x.Format("2006-01-02 15:04:05"), style)
	default:
		return fmt.Sprintf(`<c r="%s" s="%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`,
			ref, style, html.EscapeString(fmt.Sprint(x)))
	}
}

func sheetXML(s Sheet) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" state="frozen"/></sheetView></sheetViews>`)
	b.WriteString(`<cols>`)
	for i := range s.Header {
		fmt.Fprintf(&b, `<col min="%d" max="%d" width="20" customWidth="1"/>`, i+1, i+1)
	}
	b.WriteString(`</cols><sheetData>`)
	rows := append([][]any{toAny(s.Header)}, s.Rows...)
	for r, row := range rows {
		fmt.Fprintf(&b, `<row r="%d">`, r+1)
		style := 0
		if r == 0 {
			style = 1
		}
		for c, v := range row {
			b.WriteString(cell(fmt.Sprintf("%s%d", column(c), r+1), v, style))
		}
		b.WriteString(`</row>`)
	}
	if len(s.Header) > 0 {
		fmt.Fprintf(&b, `</sheetData><autoFilter ref="A1:%s%d"/></worksheet>`, column(len(s.Header)-1), len(rows))
	} else {
		b.WriteString(`</sheetData></worksheet>`)
	}
	return b.String()
}

func toAny(h []string) []any {
	out := make([]any, len(h))
	for i, v := range h {
		out[i] = v
	}
	return out
}

// WriteXLSX writes a workbook with the given sheets.
func WriteXLSX(w io.Writer, sheets []Sheet) error {
	z := zip.NewWriter(w)
	add := func(name, body string) error {
		f, err := z.Create(name)
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, body)
		return err
	}
	var types, wbSheets, rels strings.Builder
	for i, s := range sheets {
		n := i + 1
		fmt.Fprintf(&types, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, n)
		fmt.Fprintf(&wbSheets, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, html.EscapeString(truncate(s.Name, 31)), n, n)
		fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, n, n)
		if err := add(fmt.Sprintf("xl/worksheets/sheet%d.xml", n), sheetXML(s)); err != nil {
			return err
		}
	}
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
			`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` + types.String() + `</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` +
			`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>` + wbSheets.String() + `</sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			rels.String() + fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`, len(sheets)+1) + `</Relationships>`,
		"xl/styles.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
			`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><color rgb="FFFFFFFF"/><name val="Calibri"/></font></fonts>` +
			`<fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill>` +
			`<fill><patternFill patternType="solid"><fgColor rgb="FF0E7490"/><bgColor indexed="64"/></patternFill></fill></fills>` +
			`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
			`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
			`<cellXfs count="2"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0" applyFont="1" applyFill="1"/></cellXfs>` +
			`</styleSheet>`,
	}
	for name, body := range files {
		if err := add(name, body); err != nil {
			return err
		}
	}
	return z.Close()
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
