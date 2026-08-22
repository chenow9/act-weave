package aapfile

import (
	"bytes"
	"fmt"
	"strings"
)

// BuildTextPDF returns a minimal uncompressed PDF whose pages contain the given
// Latin-1-safe text lines (one page per string). Used by tests and as a
// known-text-layer fixture for ExtractPDFText.
func BuildTextPDF(pages []string) []byte {
	if len(pages) == 0 {
		pages = []string{""}
	}
	var objs []string
	// 1: Catalog
	objs = append(objs, "<< /Type /Catalog /Pages 2 0 R >>")
	// 2: Pages
	kids := make([]string, 0, len(pages))
	pageObj := 3
	for i := 0; i < len(pages); i++ {
		kids = append(kids, fmt.Sprintf("%d 0 R", pageObj+i*2))
	}
	objs = append(objs, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages)))
	fontObj := 3 + len(pages)*2
	for i, text := range pages {
		contentObj := 4 + i*2
		pageN := 3 + i*2
		_ = pageN
		escaped := pdfLiteral(text)
		stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", escaped)
		objs = append(objs, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 %d 0 R >> >> >>",
			contentObj, fontObj,
		))
		objs = append(objs, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}
	objs = append(objs, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	var body bytes.Buffer
	body.WriteString("%PDF-1.4\n")
	offsets := make([]int, 0, len(objs)+1)
	offsets = append(offsets, 0)
	for i, obj := range objs {
		offsets = append(offsets, body.Len())
		fmt.Fprintf(&body, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	startxref := body.Len()
	fmt.Fprintf(&body, "xref\n0 %d\n", len(objs)+1)
	body.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(objs); i++ {
		fmt.Fprintf(&body, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&body, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, startxref)
	return body.Bytes()
}

func pdfLiteral(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "(", "\\(")
	s = strings.ReplaceAll(s, ")", "\\)")
	s = strings.Map(func(r rune) rune {
		if r < 32 || r > 126 {
			return ' '
		}
		return r
	}, s)
	return s
}
