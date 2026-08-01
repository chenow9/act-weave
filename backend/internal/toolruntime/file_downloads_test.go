package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"actweave/backend/internal/execution"
)

type fakeTokenMinter struct {
	mu     sync.Mutex
	tokens map[string]string // fileID → tokenID
	calls  []string
	err    error
}

func (f *fakeTokenMinter) MintToolInvokeToken(_ context.Context, workspaceID, fileID, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, workspaceID+"/"+fileID)
	if f.err != nil {
		return "", f.err
	}
	if f.tokens == nil {
		f.tokens = map[string]string{}
	}
	if token, ok := f.tokens[fileID]; ok {
		return token, nil
	}
	token := "tok-" + fileID
	f.tokens[fileID] = token
	return token, nil
}

func TestEnrichFileDownloads_InjectsBodyAndHeader(t *testing.T) {
	minter := &fakeTokenMinter{}
	enricher := NewSchemaFileDownloadEnricher(minter, "https://aap.example.test")
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"document":{
				"type":"object",
				"x-actweave-file":true,
				"required":["fileId"],
				"properties":{
					"fileId":{"type":"string"},
					"mediaType":{"type":"string"},
					"downloadUrl":{"type":"string","format":"uri"}
				},
				"additionalProperties":false
			}
		}
	}`)
	input := map[string]any{
		"document": map[string]any{
			"fileId":    "11111111-1111-7111-8111-111111111111",
			"mediaType": "image/png",
		},
	}
	// Keep a scrubbed original for persist assertion.
	original, _ := json.Marshal(input)

	result, err := enricher.EnrichFileDownloads(context.Background(), FileDownloadEnrichRequest{
		WorkspaceID: "ws-1", CreatedBy: "actor-1",
		InputSchema: schema, Input: input,
	})
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	doc, _ := result.WireInput["document"].(map[string]any)
	url, _ := doc["downloadUrl"].(string)
	if !strings.HasPrefix(url, "https://aap.example.test/api/agent-access/v1/files/downloads/tok-") {
		t.Fatalf("expected absolute downloadUrl on wire, got %q", url)
	}
	if result.Headers[HeaderFileDownload] != url {
		t.Fatalf("single-file header mismatch: %v", result.Headers)
	}
	// Original input map must not be mutated (deep copy).
	if _, has := input["document"].(map[string]any)["downloadUrl"]; has {
		t.Fatal("original input must not receive downloadUrl")
	}
	if strings.Contains(string(original), "downloadUrl") || strings.Contains(string(original), "files/downloads") {
		t.Fatalf("scrubbed original payload must not contain URL material: %s", original)
	}
	scrubbed := ScrubFileDownloadArgs(result.WireInput)
	scrubJSON, _ := json.Marshal(scrubbed)
	if strings.Contains(string(scrubJSON), "downloadUrl") || strings.Contains(string(scrubJSON), "files/downloads") {
		t.Fatalf("ScrubFileDownloadArgs leaked URL: %s", scrubJSON)
	}
	if scrubbed["document"].(map[string]any)["fileId"] != "11111111-1111-7111-8111-111111111111" {
		t.Fatalf("scrub must keep fileId: %v", scrubbed)
	}
}

func TestEnrichFileDownloads_HeaderOnlyWhenNoDownloadURLProperty(t *testing.T) {
	minter := &fakeTokenMinter{}
	enricher := NewSchemaFileDownloadEnricher(minter, "https://aap.example.test")
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"document":{
				"type":"object",
				"x-actweave-file":true,
				"properties":{"fileId":{"type":"string"}},
				"additionalProperties":false
			}
		}
	}`)
	input := map[string]any{
		"document": map[string]any{"fileId": "file-a"},
	}
	result, err := enricher.EnrichFileDownloads(context.Background(), FileDownloadEnrichRequest{
		WorkspaceID: "ws", InputSchema: schema, Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	doc := result.WireInput["document"].(map[string]any)
	if _, has := doc["downloadUrl"]; has {
		t.Fatal("must not inject downloadUrl when schema omits the property")
	}
	if !strings.Contains(result.Headers[HeaderFileDownload], "/files/downloads/") {
		t.Fatalf("expected header-only injection, got %v", result.Headers)
	}
}

func TestEnrichFileDownloads_NoAnnotationNoInject(t *testing.T) {
	minter := &fakeTokenMinter{}
	enricher := NewSchemaFileDownloadEnricher(minter, "https://aap.example.test")
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"fileId":{"type":"string"},
			"document":{
				"type":"object",
				"properties":{"fileId":{"type":"string"}}
			}
		}
	}`)
	input := map[string]any{
		"fileId":   "bare-file-id",
		"document": map[string]any{"fileId": "nested-file-id"},
	}
	result, err := enricher.EnrichFileDownloads(context.Background(), FileDownloadEnrichRequest{
		WorkspaceID: "ws", InputSchema: schema, Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(minter.calls) != 0 {
		t.Fatalf("must not mint without x-actweave-file annotation, calls=%v", minter.calls)
	}
	if len(result.Headers) != 0 {
		t.Fatalf("expected no headers, got %v", result.Headers)
	}
	raw, _ := json.Marshal(result.WireInput)
	if strings.Contains(string(raw), "downloadUrl") || strings.Contains(string(raw), "files/downloads") {
		t.Fatalf("wire input must be unchanged without annotation: %s", raw)
	}
}

func TestEnrichFileDownloads_MultipleFilesJSONHeader(t *testing.T) {
	minter := &fakeTokenMinter{}
	enricher := NewSchemaFileDownloadEnricher(minter, "https://aap.example.test")
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"attachments":{
				"type":"array",
				"items":{
					"type":"object",
					"x-actweave-file":true,
					"properties":{
						"fileId":{"type":"string"},
						"downloadUrl":{"type":"string"}
					}
				}
			}
		}
	}`)
	input := map[string]any{
		"attachments": []any{
			map[string]any{"fileId": "file-1"},
			map[string]any{"fileId": "file-2"},
		},
	}
	result, err := enricher.EnrichFileDownloads(context.Background(), FileDownloadEnrichRequest{
		WorkspaceID: "ws", InputSchema: schema, Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Headers[HeaderFileDownload] != "" {
		t.Fatalf("multi-file must use plural header, got singular %q", result.Headers[HeaderFileDownload])
	}
	payload := result.Headers[HeaderFileDownloads]
	if payload == "" || !strings.Contains(payload, "file-1") || !strings.Contains(payload, "file-2") {
		t.Fatalf("expected multi download JSON header, got %q", payload)
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(decoded["file-1"], "/files/downloads/tok-file-1") {
		t.Fatalf("file-1 url: %v", decoded)
	}
	atts := result.WireInput["attachments"].([]any)
	if atts[0].(map[string]any)["downloadUrl"] == nil || atts[1].(map[string]any)["downloadUrl"] == nil {
		t.Fatalf("expected body downloadUrl on each item: %v", atts)
	}
}

func TestHTTPExecutor_OutboundHasURL_StoredInputUnchanged(t *testing.T) {
	var gotHeader string
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(HeaderFileDownload)
		payload, _ := io.ReadAll(r.Body)
		gotBody = string(payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	minter := &fakeTokenMinter{}
	enricher := NewSchemaFileDownloadEnricher(minter, "https://aap.example.test")
	executor := NewHTTPExecutor(server.Client()).ConfigureFileDownloads(enricher)

	fileID := "22222222-2222-7222-8222-222222222222"
	request := validExecutorRequest(server.URL)
	request.Snapshot.ActionConfig = json.RawMessage(`{
		"method":"POST","path":"/ingest",
		"requestBody":{"input":"document"}
	}`)
	request.Snapshot.InputSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"document":{
				"type":"object",
				"x-actweave-file":true,
				"properties":{
					"fileId":{"type":"string"},
					"downloadUrl":{"type":"string"}
				}
			}
		}
	}`)
	// Model/protocol args: fileId only (no URL).
	scrubbedInput := json.RawMessage(`{"document":{"fileId":"` + fileID + `"}}`)
	request.Input = append(json.RawMessage(nil), scrubbedInput...)

	result, err := executor.Invoke(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if result.HTTPStatus != http.StatusOK {
		t.Fatalf("status=%d", result.HTTPStatus)
	}
	if !strings.Contains(gotHeader, "/files/downloads/") {
		t.Fatalf("outbound missing download header: %q", gotHeader)
	}
	if !strings.Contains(gotBody, "downloadUrl") || !strings.Contains(gotBody, "/files/downloads/") {
		t.Fatalf("outbound body missing downloadUrl: %s", gotBody)
	}
	// Stored / protocol payload is the original request.Input (unchanged by executor).
	if strings.Contains(string(request.Input), "downloadUrl") ||
		strings.Contains(string(request.Input), "files/downloads") {
		t.Fatalf("stored input must remain scrubbed: %s", request.Input)
	}
	// Scrub of wire body for permanent payload path.
	var wire map[string]any
	_ = json.Unmarshal([]byte(gotBody), &wire)
	// Body is the document object itself (requestBody.input=document).
	// Scrub the full invoke input representation.
	var stored map[string]any
	_ = json.Unmarshal(request.Input, &stored)
	scrubbed := ScrubFileDownloadArgs(stored)
	raw, _ := json.Marshal(scrubbed)
	if strings.Contains(string(raw), "downloadUrl") || strings.Contains(string(raw), "https://") {
		t.Fatalf("scrubbed store payload leaked URL: %s", raw)
	}
}

func TestHTTPExecutor_NoAnnotationNoInjectOnWire(t *testing.T) {
	var gotHeader string
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(HeaderFileDownload) + r.Header.Get(HeaderFileDownloads)
		payload, _ := io.ReadAll(r.Body)
		gotBody = string(payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	minter := &fakeTokenMinter{}
	executor := NewHTTPExecutor(server.Client()).ConfigureFileDownloads(
		NewSchemaFileDownloadEnricher(minter, "https://aap.example.test"),
	)
	request := validExecutorRequest(server.URL)
	request.Snapshot.ActionConfig = json.RawMessage(`{
		"method":"POST","path":"/ingest","requestBody":{"input":"payload"}
	}`)
	// Schema has fileId but NO x-actweave-file annotation.
	request.Snapshot.InputSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"payload":{
				"type":"object",
				"properties":{"fileId":{"type":"string"},"quantity":{"type":"integer"}}
			}
		}
	}`)
	request.Input = json.RawMessage(`{"payload":{"fileId":"should-not-inject","quantity":1}}`)

	if _, err := executor.Invoke(context.Background(), request, nil); err != nil {
		t.Fatal(err)
	}
	if gotHeader != "" {
		t.Fatalf("no annotation must not set download headers: %q", gotHeader)
	}
	if strings.Contains(gotBody, "downloadUrl") || strings.Contains(gotBody, "files/downloads") {
		t.Fatalf("no annotation must not inject URL into body: %s", gotBody)
	}
	if len(minter.calls) != 0 {
		t.Fatalf("minter must not be called: %v", minter.calls)
	}
}

func TestHTTPExecutor_FileNotReadyFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("upstream must not be dialed when file resolve fails")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	minter := &fakeTokenMinter{err: errors.New("aap file is not ready")}
	executor := NewHTTPExecutor(server.Client()).ConfigureFileDownloads(
		NewSchemaFileDownloadEnricher(minter, "https://aap.example.test"),
	)
	request := validExecutorRequest(server.URL)
	request.Snapshot.ActionConfig = json.RawMessage(`{"method":"POST","path":"/x","requestBody":{"input":"document"}}`)
	request.Snapshot.InputSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"document":{
				"type":"object",
				"x-actweave-file":true,
				"properties":{"fileId":{"type":"string"},"downloadUrl":{"type":"string"}}
			}
		}
	}`)
	request.Input = json.RawMessage(`{"document":{"fileId":"not-ready-file"}}`)
	_, err := executor.Invoke(context.Background(), request, nil)
	if execution.ErrorCode(err) != "INVOCATION_FILE_NOT_READY" {
		t.Fatalf("expected INVOCATION_FILE_NOT_READY, got %v", err)
	}
}

func TestPlatformFileAccess_OpenReadyNoURL(t *testing.T) {
	opened := false
	access := &PlatformFileAccess{
		GetFile: func(_ context.Context, workspaceID, fileID string) (PlatformFileMeta, error) {
			return PlatformFileMeta{
				FileID: fileID, WorkspaceID: workspaceID, Status: "READY",
				StoredObjectID: "obj-1", DeclaredMedia: "image/png", SizeBytes: 4,
			}, nil
		},
		OpenObject: func(_ context.Context, _, objectID string) (io.ReadCloser, error) {
			opened = true
			if objectID != "obj-1" {
				t.Fatalf("object id=%s", objectID)
			}
			return io.NopCloser(strings.NewReader("data")), nil
		},
	}
	file, err := access.OpenReadyFile(context.Background(), "ws", "file-1")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Body.Close()
	if !opened {
		t.Fatal("expected SecureStore open")
	}
	body, _ := io.ReadAll(file.Body)
	if string(body) != "data" {
		t.Fatalf("body=%q", body)
	}
	// Meta must not carry any URL-shaped fields (struct has none; assert content).
	raw, _ := json.Marshal(file.Meta)
	if strings.Contains(strings.ToLower(string(raw)), "url") ||
		strings.Contains(string(raw), "https://") {
		t.Fatalf("platform open meta must not contain URL: %s", raw)
	}
}

func TestPlatformFileAccess_NotReady(t *testing.T) {
	access := &PlatformFileAccess{
		GetFile: func(context.Context, string, string) (PlatformFileMeta, error) {
			return PlatformFileMeta{Status: "PROCESSING", StoredObjectID: "obj"}, nil
		},
		OpenObject: func(context.Context, string, string) (io.ReadCloser, error) {
			t.Fatal("must not open non-READY")
			return nil, nil
		},
	}
	_, err := access.OpenReadyFile(context.Background(), "ws", "f")
	if !errors.Is(err, ErrPlatformFileNotReady) {
		t.Fatalf("err=%v", err)
	}
}

func TestScrubFileDownloadJSON(t *testing.T) {
	raw := json.RawMessage(`{
		"document":{"fileId":"f1","downloadUrl":"https://aap/api/agent-access/v1/files/downloads/tok"},
		"note":"see https://aap/api/agent-access/v1/files/downloads/other"
	}`)
	out, err := ScrubFileDownloadJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "downloadUrl") || strings.Contains(string(out), "files/downloads") {
		t.Fatalf("scrub failed: %s", out)
	}
	if !strings.Contains(string(out), `"fileId":"f1"`) {
		t.Fatalf("fileId stripped: %s", out)
	}
}
