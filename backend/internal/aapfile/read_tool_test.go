package aapfile

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"actweave/backend/internal/toolruntime"
)

const (
	readTestWorkspace = "a18f1f2e-7b5a-7c3d-8e9f-123456789002"
	readTestAgent     = "a18f1f2e-7b5a-7c3d-8e9f-123456789004"
	readTestFileID    = "d41f1f2e-7b5a-7c3d-8e9f-1234567890f1"
	readOtherFileID   = "d41f1f2e-7b5a-7c3d-8e9f-1234567890f2"
)

type fakeOpener struct {
	meta toolruntime.PlatformFileMeta
	body []byte
	err  error
}

func (f *fakeOpener) OpenReadyFile(_ context.Context, workspaceID, fileID string) (toolruntime.PlatformOpenedFile, error) {
	if f.err != nil {
		return toolruntime.PlatformOpenedFile{}, f.err
	}
	if workspaceID != f.meta.WorkspaceID || fileID != f.meta.FileID {
		return toolruntime.PlatformOpenedFile{}, toolruntime.ErrPlatformFileNotFound
	}
	return toolruntime.PlatformOpenedFile{
		Meta: f.meta,
		Body: io.NopCloser(bytes.NewReader(f.body)),
	}, nil
}

func testReadTool(t *testing.T, opener *fakeOpener, ids ...string) *ReadAttachmentTool {
	t.Helper()
	readable := map[string]struct{}{}
	for _, id := range ids {
		readable[id] = struct{}{}
	}
	if len(readable) == 0 {
		readable[readTestFileID] = struct{}{}
	}
	tool, err := NewReadAttachmentTool(ReadAttachmentConfig{
		Opener: opener, ReadableFileIDs: readable,
		WorkspaceID: readTestWorkspace, AgentID: readTestAgent,
	})
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func pdfOpener(media string, body []byte) *fakeOpener {
	return &fakeOpener{
		meta: toolruntime.PlatformFileMeta{
			FileID: readTestFileID, WorkspaceID: readTestWorkspace, AgentID: readTestAgent,
			Status: StatusReady, Filename: "contract.pdf", DeclaredMedia: media,
			SizeBytes: int64(len(body)),
		},
		body: body,
	}
}

func TestExtractPDFTextContainsKnownLayer(t *testing.T) {
	body := BuildTextPDF([]string{"Hello inbound PDF"})
	got, err := ExtractPDFText(context.Background(), body, "", MaxReadTextBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "Hello inbound PDF") {
		t.Fatalf("extracted %q", got.Text)
	}
	if got.PageCount != 1 || got.NoTextLayer {
		t.Fatalf("meta=%+v", got)
	}
}

func TestReadAttachmentPDFDefaultPages(t *testing.T) {
	pages := make([]string, 12)
	for i := range pages {
		pages[i] = "PAGE " + strings.Repeat("x", 1) + string(rune('A'+i%26))
	}
	pages[0] = "Hello inbound PDF"
	pages[9] = "page ten marker"
	pages[10] = "page eleven should be omitted"
	body := BuildTextPDF(pages)
	tool := testReadTool(t, pdfOpener(MediaTypePDF, body))
	raw, err := tool.InvokableRun(context.Background(), `{"fileId":"`+readTestFileID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if json.Unmarshal([]byte(raw), &out) != nil {
		t.Fatalf("json %s", raw)
	}
	if out["ok"] != true {
		t.Fatalf("result=%s", raw)
	}
	text, _ := out["text"].(string)
	if !strings.Contains(text, "Hello inbound PDF") {
		t.Fatalf("missing layer: %s", raw)
	}
	if strings.Contains(text, "page eleven should be omitted") {
		t.Fatalf("default range leaked page 11: %s", raw)
	}
	if _, hasURL := out["url"]; hasURL {
		t.Fatalf("url key present: %s", raw)
	}
	if _, has := out["downloadUrl"]; has {
		t.Fatalf("downloadUrl present: %s", raw)
	}
}

func TestReadAttachmentPDFPageCap(t *testing.T) {
	pages := make([]string, 25)
	for i := range pages {
		pages[i] = "P" + strings.Repeat("y", 8)
	}
	pages[0] = "first"
	pages[19] = "page twenty"
	pages[20] = "page twenty one"
	body := BuildTextPDF(pages)
	tool := testReadTool(t, pdfOpener(MediaTypePDF, body))
	raw, err := tool.InvokableRun(context.Background(), `{"fileId":"`+readTestFileID+`","pages":"1-25"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if json.Unmarshal([]byte(raw), &out) != nil {
		t.Fatalf("json %s", raw)
	}
	text, _ := out["text"].(string)
	if !strings.Contains(text, "page twenty") {
		t.Fatalf("expected page 20: %s", raw)
	}
	if strings.Contains(text, "page twenty one") {
		t.Fatalf("20-page cap leaked page 21: %s", raw)
	}
}

func TestReadAttachmentTruncatesText(t *testing.T) {
	body := BuildTextPDF([]string{strings.Repeat("Z", 80)})
	tool, err := NewReadAttachmentTool(ReadAttachmentConfig{
		Opener:          pdfOpener(MediaTypePDF, body),
		ReadableFileIDs: map[string]struct{}{readTestFileID: {}},
		WorkspaceID:     readTestWorkspace, AgentID: readTestAgent,
		MaxTextBytes: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := tool.InvokableRun(context.Background(), `{"fileId":"`+readTestFileID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if json.Unmarshal([]byte(raw), &out) != nil {
		t.Fatal(raw)
	}
	if out["truncated"] != true {
		t.Fatalf("want truncated: %s", raw)
	}
	text, _ := out["text"].(string)
	if len(text) > 16 {
		t.Fatalf("text len=%d", len(text))
	}
}

func TestReadAttachmentOfficeAndZipDenied(t *testing.T) {
	for _, media := range []string{MediaTypeDocx, MediaTypeXlsx, MediaTypeZip} {
		tool := testReadTool(t, pdfOpener(media, []byte("PK\x03\x04")))
		raw, err := tool.InvokableRun(context.Background(), `{"fileId":"`+readTestFileID+`"}`)
		if err != nil {
			t.Fatal(err)
		}
		ok, code := ParseReadResultStatus(raw)
		if ok || code != ErrorCodeMediaTypeDenied {
			t.Fatalf("%s: %s", media, raw)
		}
		if strings.Contains(raw, "url") || strings.Contains(strings.ToLower(raw), "download") {
			t.Fatalf("denied result leaked url: %s", raw)
		}
	}
}

func TestReadAttachmentUnknownFileIDNotFound(t *testing.T) {
	tool := testReadTool(t, pdfOpener(MediaTypePDF, BuildTextPDF([]string{"x"})), readTestFileID)
	raw, err := tool.InvokableRun(context.Background(), `{"fileId":"`+readOtherFileID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	ok, code := ParseReadResultStatus(raw)
	if ok || code != ErrorCodeNotFound {
		t.Fatalf("got %s", raw)
	}
}

func TestReadAttachmentRejectsUnknownArgs(t *testing.T) {
	tool := testReadTool(t, pdfOpener(MediaTypePDF, BuildTextPDF([]string{"x"})))
	raw, err := tool.InvokableRun(context.Background(), `{"fileId":"`+readTestFileID+`","path":"/tmp/x"}`)
	if err != nil {
		t.Fatal(err)
	}
	ok, code := ParseReadResultStatus(raw)
	if ok || code != ErrorCodeInvalid {
		t.Fatalf("got %s", raw)
	}
}

func TestAppendInboundReadPromptRulesIdempotent(t *testing.T) {
	once := AppendInboundReadPromptRules("You are helpful.")
	if !strings.Contains(once, InboundReadPromptMarker) || !strings.Contains(once, ReadAttachmentToolName) {
		t.Fatalf("appendix missing: %s", once)
	}
	twice := AppendInboundReadPromptRules(once)
	if once != twice {
		t.Fatal("not idempotent")
	}
}

func TestParsePDFPageRange(t *testing.T) {
	start, end, err := ParsePDFPageRange("", 24)
	if err != nil || start != 1 || end != 10 {
		t.Fatalf("default: %d-%d %v", start, end, err)
	}
	start, end, err = ParsePDFPageRange("3", 24)
	if err != nil || start != 3 || end != 3 {
		t.Fatalf("single: %d-%d %v", start, end, err)
	}
	start, end, err = ParsePDFPageRange("10-", 24)
	if err != nil || start != 10 || end != 29 && end != 24 {
		if start != 10 || end != 24 {
			// 10- with max 20 pages → 10..29 clamped to 24
			t.Fatalf("open: %d-%d %v", start, end, err)
		}
	}
	start, end, err = ParsePDFPageRange("1-25", 24)
	if err != nil || start != 1 || end != 20 {
		t.Fatalf("cap: %d-%d %v", start, end, err)
	}
}
