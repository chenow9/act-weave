package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"actweave/backend/internal/execution"
)

// Header names for partner-facing file download injection (design §5.9.2 / KD-22).
const (
	HeaderFileDownload  = "X-ActWeave-File-Download"
	HeaderFileDownloads = "X-ActWeave-File-Downloads"
)

// AnnotationActweaveFile is the only discovery signal for tool file handles.
// Field names such as "fileId" must never be guessed without this flag.
const AnnotationActweaveFile = "x-actweave-file"

// ToolFileTokenMinter mints opaque tool_invoke download tokens for READY files.
// Implementations must not return live MinIO/presign URLs — only opaque token IDs.
type ToolFileTokenMinter interface {
	// MintToolInvokeToken returns an opaque download token id for a READY file
	// visible in workspaceID. createdBy is a non-secret actor label.
	MintToolInvokeToken(ctx context.Context, workspaceID, fileID, createdBy string) (tokenID string, err error)
}

// FileDownloadEnricher produces a wire-only input copy with downloadUrl injection
// and partner headers. Persistent / protocol args must use scrubbed (original) input.
type FileDownloadEnricher interface {
	EnrichFileDownloads(ctx context.Context, req FileDownloadEnrichRequest) (FileDownloadEnrichResult, error)
}

// FileDownloadEnrichRequest is the executor-side enrichment input.
type FileDownloadEnrichRequest struct {
	WorkspaceID string
	// CreatedBy is a non-secret actor label stored on the download token row.
	CreatedBy  string
	InputSchema json.RawMessage
	// Input is the model/user tool args (no URLs). Must not be mutated.
	Input map[string]any
}

// FileDownloadEnrichResult is the wire-only view of tool args for outbound HTTP.
type FileDownloadEnrichResult struct {
	// WireInput is a deep copy of Input with downloadUrl injected where schema allows.
	WireInput map[string]any
	// Headers are X-ActWeave-File-Download(s) only (merge into connection headers).
	Headers map[string]string
	// FileIDs is the ordered set of enriched file ids (for tests/observability).
	FileIDs []string
}

// SchemaFileDownloadEnricher walks InputSchema for x-actweave-file nodes and
// mints tool_invoke tokens (design §5.9.2).
type SchemaFileDownloadEnricher struct {
	Minter        ToolFileTokenMinter
	PublicBaseURL string
}

// NewSchemaFileDownloadEnricher builds an enricher. publicBaseURL is the absolute
// AAP public origin (e.g. https://aap.example.com). Empty falls back to https://localhost.
func NewSchemaFileDownloadEnricher(minter ToolFileTokenMinter, publicBaseURL string) *SchemaFileDownloadEnricher {
	return &SchemaFileDownloadEnricher{Minter: minter, PublicBaseURL: publicBaseURL}
}

// EnrichFileDownloads deep-copies input, injects downloadUrl only when the
// annotated object schema declares that property, and builds partner headers.
func (e *SchemaFileDownloadEnricher) EnrichFileDownloads(
	ctx context.Context,
	req FileDownloadEnrichRequest,
) (FileDownloadEnrichResult, error) {
	if e == nil || e.Minter == nil {
		return FileDownloadEnrichResult{WireInput: cloneAnyMap(req.Input), Headers: map[string]string{}}, nil
	}
	workspaceID := strings.TrimSpace(req.WorkspaceID)
	if workspaceID == "" {
		return FileDownloadEnrichResult{}, execution.NewError(
			execution.ErrorCodeInvalidRequest, "VALIDATION", false, 0, nil,
		)
	}
	createdBy := strings.TrimSpace(req.CreatedBy)
	if createdBy == "" {
		createdBy = "system:tool-invoke"
	}
	wire := cloneAnyMap(req.Input)
	if wire == nil {
		wire = map[string]any{}
	}
	schema, err := parseSchemaObject(req.InputSchema)
	if err != nil {
		return FileDownloadEnrichResult{}, execution.NewError(
			execution.ErrorCodeInvalidSnapshot, "VALIDATION", false, 0, err,
		)
	}
	downloads := make(map[string]string) // fileId → absolute URL
	var fileIDs []string
	if err := e.walkSchema(ctx, schema, wire, workspaceID, createdBy, downloads, &fileIDs); err != nil {
		return FileDownloadEnrichResult{}, err
	}
	headers := buildFileDownloadHeaders(downloads)
	return FileDownloadEnrichResult{WireInput: wire, Headers: headers, FileIDs: fileIDs}, nil
}

func (e *SchemaFileDownloadEnricher) walkSchema(
	ctx context.Context,
	schema map[string]any,
	value any,
	workspaceID, createdBy string,
	downloads map[string]string,
	fileIDs *[]string,
) error {
	if schema == nil {
		return nil
	}
	if isActweaveFileSchema(schema) {
		return e.enrichFileNode(ctx, schema, value, workspaceID, createdBy, downloads, fileIDs)
	}
	// object properties
	if props, ok := schema["properties"].(map[string]any); ok {
		obj, _ := value.(map[string]any)
		if obj == nil {
			return nil
		}
		for name, propRaw := range props {
			propSchema, ok := propRaw.(map[string]any)
			if !ok {
				continue
			}
			child, exists := obj[name]
			if !exists || child == nil {
				continue
			}
			if err := e.walkSchema(ctx, propSchema, child, workspaceID, createdBy, downloads, fileIDs); err != nil {
				return err
			}
		}
	}
	// array items
	if items, ok := schema["items"].(map[string]any); ok {
		arr, _ := value.([]any)
		for _, item := range arr {
			if item == nil {
				continue
			}
			if err := e.walkSchema(ctx, items, item, workspaceID, createdBy, downloads, fileIDs); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *SchemaFileDownloadEnricher) enrichFileNode(
	ctx context.Context,
	schema map[string]any,
	value any,
	workspaceID, createdBy string,
	downloads map[string]string,
	fileIDs *[]string,
) error {
	obj, ok := value.(map[string]any)
	if !ok || obj == nil {
		// Annotated node present in schema but value is not an object — skip.
		// (Schema validation should already have rejected malformed required nodes.)
		return nil
	}
	fileID := extractFileID(obj)
	if fileID == "" {
		return execution.NewError(
			"INVOCATION_FILE_INVALID", "VALIDATION", false, 0,
			fmt.Errorf("x-actweave-file node missing fileId"),
		)
	}
	if _, exists := downloads[fileID]; exists {
		// Same file referenced twice: reuse URL already minted this invoke.
		if schemaDeclaresDownloadURL(schema) {
			obj["downloadUrl"] = downloads[fileID]
		}
		return nil
	}
	tokenID, err := e.Minter.MintToolInvokeToken(ctx, workspaceID, fileID, createdBy)
	if err != nil {
		return mapFileMintError(err)
	}
	url := absoluteDownloadURL(e.PublicBaseURL, tokenID)
	downloads[fileID] = url
	*fileIDs = append(*fileIDs, fileID)
	// Inject body field only when the object schema declares downloadUrl.
	// With additionalProperties:false and no downloadUrl property, header-only.
	if schemaDeclaresDownloadURL(schema) {
		obj["downloadUrl"] = url
	}
	return nil
}

func mapFileMintError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not ready") || strings.Contains(msg, "aap file is not ready"):
		return execution.NewError("INVOCATION_FILE_NOT_READY", "VALIDATION", true, 0, err)
	case strings.Contains(msg, "not found") || strings.Contains(msg, "aap file not found"):
		return execution.NewError("INVOCATION_FILE_NOT_FOUND", "VALIDATION", false, 0, err)
	default:
		return execution.NewError("INVOCATION_FILE_RESOLVE_FAILED", "VALIDATION", false, 0, err)
	}
}

func isActweaveFileSchema(schema map[string]any) bool {
	if schema == nil {
		return false
	}
	raw, ok := schema[AnnotationActweaveFile]
	if !ok {
		return false
	}
	switch typed := raw.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func schemaDeclaresDownloadURL(schema map[string]any) bool {
	props, ok := schema["properties"].(map[string]any)
	if !ok || props == nil {
		return false
	}
	_, exists := props["downloadUrl"]
	return exists
}

func extractFileID(obj map[string]any) string {
	if obj == nil {
		return ""
	}
	for _, key := range []string{"fileId", "file_id"} {
		if raw, ok := obj[key]; ok {
			if s, ok := raw.(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func absoluteDownloadURL(publicBase, tokenID string) string {
	base := strings.TrimRight(strings.TrimSpace(publicBase), "/")
	if base == "" {
		base = "https://localhost"
	}
	tokenID = strings.TrimSpace(tokenID)
	return base + "/api/agent-access/v1/files/downloads/" + tokenID
}

func buildFileDownloadHeaders(downloads map[string]string) map[string]string {
	headers := make(map[string]string)
	switch len(downloads) {
	case 0:
		return headers
	case 1:
		for _, url := range downloads {
			headers[HeaderFileDownload] = url
		}
	default:
		// Stable JSON object: fileId → url (map marshal order is random; re-encode sorted).
		encoded, err := json.Marshal(downloads)
		if err != nil {
			// Extremely unlikely; fall back to first URL as single header.
			for _, url := range downloads {
				headers[HeaderFileDownload] = url
				break
			}
			return headers
		}
		headers[HeaderFileDownloads] = string(encoded)
	}
	return headers
}

// ScrubFileDownloadArgs returns a deep copy of input with downloadUrl fields
// removed and any /files/downloads/ string values stripped. Used so persist /
// protocol paths never retain wire-only URLs (KD-9 / KD-22).
func ScrubFileDownloadArgs(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	cloned := cloneAnyMap(input)
	scrubValue(cloned)
	return cloned
}

// ScrubFileDownloadJSON scrubs a JSON object payload the same way as ScrubFileDownloadArgs.
func ScrubFileDownloadJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytesTrimSpace(raw)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	scrubValue(value)
	return json.Marshal(value)
}

func scrubValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if isDownloadURLKey(key) {
				delete(typed, key)
				continue
			}
			if s, ok := child.(string); ok && containsFileDownloadPath(s) {
				delete(typed, key)
				continue
			}
			scrubValue(child)
		}
	case []any:
		for _, child := range typed {
			scrubValue(child)
		}
	}
}

func isDownloadURLKey(key string) bool {
	normalized := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(strings.TrimSpace(key)))
	return normalized == "downloadurl"
}

func containsFileDownloadPath(value string) bool {
	return strings.Contains(strings.ToLower(value), "/files/downloads/")
}

func parseSchemaObject(raw json.RawMessage) (map[string]any, error) {
	raw = bytesTrimSpace(raw)
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	if schema == nil {
		return map[string]any{}, nil
	}
	return schema, nil
}

func cloneAnyMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	// JSON round-trip keeps types consistent with Unmarshal (float64, etc.).
	payload, err := json.Marshal(input)
	if err != nil {
		out := make(map[string]any, len(input))
		for k, v := range input {
			out[k] = v
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil || out == nil {
		out = make(map[string]any, len(input))
		for k, v := range input {
			out[k] = v
		}
	}
	return out
}

func bytesTrimSpace(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}
