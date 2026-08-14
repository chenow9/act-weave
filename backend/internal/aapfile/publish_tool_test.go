package aapfile

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/agentaccessauth"
)

const (
	publishTestWorkspace = "a18f1f2e-7b5a-7c3d-8e9f-123456789002"
	publishTestAgent     = "a18f1f2e-7b5a-7c3d-8e9f-123456789004"
	publishTestClient    = "a18f1f2e-7b5a-7c3d-8e9f-123456789005"
	publishTestService   = "a18f1f2e-7b5a-7c3d-8e9f-123456789006"
	publishTestRun       = "a18f1f2e-7b5a-7c3d-8e9f-123456789010"
)

type fakeIngest struct {
	file File
	err  error
	in   IngestGeneratedInput
}

func (f *fakeIngest) IngestGenerated(_ context.Context, in IngestGeneratedInput) (File, error) {
	f.in = in
	if f.err != nil {
		return File{}, f.err
	}
	return f.file, nil
}

type memLister struct {
	files []File
	err   error
}

func (m *memLister) ListGeneratedForRun(context.Context, string, string, string) ([]File, error) {
	if m.err != nil {
		return nil, m.err
	}
	return append([]File(nil), m.files...), nil
}

func testPublishTool(t *testing.T, ingest *fakeIngest, collector *OutboundCollector) *PublishAttachmentTool {
	t.Helper()
	if collector == nil {
		collector = NewOutboundCollector(&memLister{}, publishTestWorkspace, publishTestAgent, publishTestRun, MaxOutboundTurnBytes)
	}
	tool, err := NewPublishAttachmentTool(PublishAttachmentConfig{
		Ingest:    ingest,
		Collector: collector,
		Scope:     Scope{WorkspaceID: publishTestWorkspace, AgentID: publishTestAgent},
		Principal: agentaccessauth.AAPAccessTokenPrincipal{
			PrincipalID: publishTestService, ServicePrincipalID: publishTestService,
			WorkspaceID: publishTestWorkspace, AgentID: publishTestAgent, AuthorizedParty: publishTestClient,
		},
		ClientID: publishTestClient, AgentPolicyVersion: 1, SourceRunID: publishTestRun,
	})
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func TestPublishAttachmentRejectsBase64Field(t *testing.T) {
	tool := testPublishTool(t, &fakeIngest{}, nil)
	out, err := tool.InvokableRun(context.Background(), `{"filename":"a.csv","mediaType":"text/csv","text":"a","base64":"QQ=="}`)
	if err != nil {
		t.Fatal(err)
	}
	ok, code := ParsePublishResultStatus(out)
	if ok || code != ErrorCodeInvalid {
		t.Fatalf("out=%s", out)
	}
}

func TestPublishAttachmentRejectsUTF8ByteCap(t *testing.T) {
	tool := testPublishTool(t, &fakeIngest{}, nil)
	// JSON maxLength is code points; a 3-byte rune can exceed 256KiB while staying under 262144 chars.
	// Use a body just over MaxPublishTextBytes.
	text := strings.Repeat("x", MaxPublishTextBytes+1)
	raw, _ := json.Marshal(map[string]any{"filename": "a.txt", "mediaType": "text/plain", "text": text})
	out, err := tool.InvokableRun(context.Background(), string(raw))
	if err != nil {
		t.Fatal(err)
	}
	ok, code := ParsePublishResultStatus(out)
	if ok || code != ErrorCodeSizeExceeded {
		t.Fatalf("out=%s", out)
	}
}

func TestPublishAttachmentTurnLimitReleases(t *testing.T) {
	collector := NewOutboundCollector(&memLister{}, publishTestWorkspace, publishTestAgent, publishTestRun, MaxOutboundTurnBytes)
	for i := 0; i < MaxOutboundFilesPerTurn; i++ {
		if err := collector.TryReserve(1, 1); err != nil {
			t.Fatal(err)
		}
	}
	ingest := &fakeIngest{file: File{ID: "019f0000-0000-7000-8000-00000000f001"}}
	tool := testPublishTool(t, ingest, collector)
	out, err := tool.InvokableRun(context.Background(), `{"filename":"a.csv","mediaType":"text/csv","text":"a,b"}`)
	if err != nil {
		t.Fatal(err)
	}
	ok, code := ParsePublishResultStatus(out)
	if ok || code != ErrorCodeOutboundTurnLimit {
		t.Fatalf("out=%s", out)
	}
	if ingest.in.SourceRunID != "" {
		t.Fatal("ingest must not run after reserve failure")
	}
}

func TestPublishAttachmentIngestFailureReleases(t *testing.T) {
	collector := NewOutboundCollector(&memLister{}, publishTestWorkspace, publishTestAgent, publishTestRun, MaxOutboundTurnBytes)
	ingest := &fakeIngest{err: ErrFeatureDisabled}
	tool := testPublishTool(t, ingest, collector)
	out, err := tool.InvokableRun(context.Background(), `{"filename":"a.csv","mediaType":"text/csv","text":"a,b"}`)
	if err != nil {
		t.Fatal(err)
	}
	ok, code := ParsePublishResultStatus(out)
	if ok || code != ErrorCodeFeatureDisabled {
		t.Fatalf("out=%s", out)
	}
	if err := collector.TryReserve(1, 1); err != nil {
		t.Fatalf("reservation leaked: %v", err)
	}
}

func TestPublishAttachmentSuccessAllowlist(t *testing.T) {
	filename := "invoice.csv"
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	id := "019f0000-0000-7000-8000-00000000f001"
	ingest := &fakeIngest{file: File{
		ID: id, DeclaredMediaType: "text/csv", Filename: &filename, SizeBytes: 3, SHA256: &sha,
	}}
	tool := testPublishTool(t, ingest, nil)
	out, err := tool.InvokableRun(context.Background(), `{"filename":"invoice.csv","mediaType":"text/csv","text":"a,b"}`)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if json.Unmarshal([]byte(out), &body) != nil {
		t.Fatal(out)
	}
	if body["ok"] != true || body["fileId"] != id || body["filename"] != filename {
		t.Fatalf("body=%v", body)
	}
	if _, has := body["url"]; has {
		t.Fatal("url leaked")
	}
	if _, has := body["downloadUrl"]; has {
		t.Fatal("downloadUrl leaked")
	}
	if _, has := body["content"]; has {
		t.Fatal("content leaked")
	}
	if ingest.in.SourceRunID != publishTestRun {
		t.Fatalf("sourceRun=%s", ingest.in.SourceRunID)
	}
}

func TestPublishAttachmentRejectsDotDotFilename(t *testing.T) {
	tool := testPublishTool(t, &fakeIngest{}, nil)
	out, err := tool.InvokableRun(context.Background(), `{"filename":"../secret.csv","mediaType":"text/csv","text":"a"}`)
	if err != nil {
		t.Fatal(err)
	}
	ok, code := ParsePublishResultStatus(out)
	if ok || code != ErrorCodeInvalid {
		t.Fatalf("out=%s", out)
	}
}

func TestMapPublishErrorCodes(t *testing.T) {
	code, _ := mapPublishError(ErrFeatureDisabled)
	if code != ErrorCodeFeatureDisabled {
		t.Fatalf("code=%s", code)
	}
	code, _ = mapPublishError(errors.New("aap file operation failed: FILE_SIZE_EXCEEDED"))
	if code != ErrorCodeSizeExceeded {
		t.Fatalf("code=%s", code)
	}
}

func TestAllowlistedPublishArgsStripsExtraKeys(t *testing.T) {
	raw := AllowlistedPublishArgs(`{"filename":"a.csv","mediaType":"text/csv","text":"x","url":"https://evil"}`)
	if strings.Contains(string(raw), "url") {
		t.Fatalf("args=%s", raw)
	}
}

func TestAppendOutboundPromptRulesIdempotent(t *testing.T) {
	once := AppendOutboundPromptRules("You are helpful.")
	if !strings.Contains(once, PublishAttachmentToolName) || !strings.Contains(once, OutboundPromptMarker) {
		t.Fatalf("appendix missing: %s", once)
	}
	twice := AppendOutboundPromptRules(once)
	if once != twice {
		t.Fatal("second append mutated instruction")
	}
}
