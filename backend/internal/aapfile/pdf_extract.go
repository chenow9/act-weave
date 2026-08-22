package aapfile

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// ExtractedPDF is the page-bounded text extraction result.
type ExtractedPDF struct {
	Text        string
	PageCount   int
	StartPage   int
	EndPage     int
	Truncated   bool
	NoTextLayer bool
}

// ParsePDFPageRange resolves a pages spec against pageCount (1-indexed).
// Empty spec → 1..min(10, pageCount). Max 20 pages per call.
func ParsePDFPageRange(spec string, pageCount int) (start, end int, err error) {
	if pageCount < 1 {
		return 0, 0, fmt.Errorf("%s: pdf has no pages", ErrorCodeInvalid)
	}
	spec = strings.TrimSpace(spec)
	if spec == "" {
		end = PDFDefaultEndPage
		if end > pageCount {
			end = pageCount
		}
		return 1, end, nil
	}
	start, end, err = parsePagesSpec(spec, pageCount)
	if err != nil {
		return 0, 0, err
	}
	if start < 1 {
		start = 1
	}
	if start > pageCount {
		return pageCount, pageCount, nil
	}
	if end < start {
		end = start
	}
	if end > pageCount {
		end = pageCount
	}
	if end-start+1 > PDFMaxPagesPerCall {
		end = start + PDFMaxPagesPerCall - 1
		if end > pageCount {
			end = pageCount
		}
	}
	return start, end, nil
}

func parsePagesSpec(spec string, pageCount int) (int, int, error) {
	if strings.Contains(spec, "-") {
		parts := strings.SplitN(spec, "-", 2)
		left := strings.TrimSpace(parts[0])
		right := strings.TrimSpace(parts[1])
		if left == "" {
			return 0, 0, fmt.Errorf("%s: invalid pages", ErrorCodeInvalid)
		}
		start, err := parsePositiveInt(left)
		if err != nil {
			return 0, 0, err
		}
		if right == "" {
			end := start + PDFMaxPagesPerCall - 1
			if end > pageCount {
				end = pageCount
			}
			return start, end, nil
		}
		end, err := parsePositiveInt(right)
		if err != nil {
			return 0, 0, err
		}
		return start, end, nil
	}
	page, err := parsePositiveInt(spec)
	if err != nil {
		return 0, 0, err
	}
	return page, page, nil
}

func parsePositiveInt(raw string) (int, error) {
	n := 0
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%s: invalid pages", ErrorCodeInvalid)
		}
		n = n*10 + int(r-'0')
		if n > 1_000_000 {
			return 0, fmt.Errorf("%s: invalid pages", ErrorCodeInvalid)
		}
	}
	if n < 1 {
		return 0, fmt.Errorf("%s: invalid pages", ErrorCodeInvalid)
	}
	return n, nil
}

// ExtractPDFText pulls UTF-8 text from selected pages of an uncompressed or
// Flate-decoded PDF. Caller supplies a timeout via ctx.
func ExtractPDFText(ctx context.Context, body []byte, pagesSpec string, maxTextBytes int) (ExtractedPDF, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if maxTextBytes <= 0 {
		maxTextBytes = MaxReadTextBytes
	}
	type result struct {
		out ExtractedPDF
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := extractPDFTextSync(body, pagesSpec, maxTextBytes)
		done <- result{out: out, err: err}
	}()
	select {
	case <-ctx.Done():
		return ExtractedPDF{}, fmt.Errorf("%s: pdf extract timed out", ErrorCodeProcessingFailed)
	case got := <-done:
		return got.out, got.err
	}
}

func extractPDFTextSync(body []byte, pagesSpec string, maxTextBytes int) (ExtractedPDF, error) {
	if len(body) == 0 {
		return ExtractedPDF{}, fmt.Errorf("%s: empty pdf", ErrorCodeInvalid)
	}
	reader, err := pdf.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return ExtractedPDF{}, fmt.Errorf("%s: %v", ErrorCodeProcessingFailed, err)
	}
	pageCount := reader.NumPage()
	start, end, err := ParsePDFPageRange(pagesSpec, pageCount)
	if err != nil {
		return ExtractedPDF{}, err
	}
	var b strings.Builder
	for i := start; i <= end; i++ {
		if i > pageCount {
			break
		}
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		plain, err := page.GetPlainText(nil)
		if err != nil {
			return ExtractedPDF{}, fmt.Errorf("%s: page %d: %v", ErrorCodeProcessingFailed, i, err)
		}
		if b.Len() > 0 && strings.TrimSpace(plain) != "" {
			b.WriteByte('\n')
		}
		b.WriteString(plain)
	}
	text := b.String()
	truncated := false
	if len(text) > maxTextBytes {
		text = truncateUTF8(text, maxTextBytes)
		truncated = true
	}
	return ExtractedPDF{
		Text:        text,
		PageCount:   pageCount,
		StartPage:   start,
		EndPage:     end,
		Truncated:   truncated,
		NoTextLayer: strings.TrimSpace(text) == "",
	}, nil
}

func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	s = s[:maxBytes]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s
}
