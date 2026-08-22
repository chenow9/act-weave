package aapfile

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"actweave/backend/internal/toolruntime"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/google/uuid"
)

const readAttachmentParamsJSON = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["fileId"],
  "properties": {
    "fileId": {
      "type": "string",
      "description": "UUID of an attached user file from this conversation."
    },
    "pages": {
      "type": "string",
      "description": "PDF page range: '1-5', '3', '10-'. Ignored for non-PDF. Max 20 pages per call. Default first 10 pages."
    }
  }
}`

// ReadAttachmentConfig binds opener + readable set for one InvokableTool.
type ReadAttachmentConfig struct {
	Opener          toolruntime.PlatformFileOpener
	ReadableFileIDs map[string]struct{}
	WorkspaceID     string
	AgentID         string
	MaxBytes        int64
	MaxTextBytes    int
	ExtractTimeout  time.Duration
}

// ReadAttachmentTool is the Eino InvokableTool for actweave.read_attachment.
type ReadAttachmentTool struct {
	cfg  ReadAttachmentConfig
	info *schema.ToolInfo
}

var _ tool.InvokableTool = (*ReadAttachmentTool)(nil)

// NewReadAttachmentTool constructs the inbound PDF read tool.
func NewReadAttachmentTool(cfg ReadAttachmentConfig) (*ReadAttachmentTool, error) {
	if cfg.Opener == nil {
		return nil, ErrInvalid
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = DefaultMaxBytes
	}
	if cfg.MaxTextBytes <= 0 {
		cfg.MaxTextBytes = MaxReadTextBytes
	}
	if cfg.ExtractTimeout <= 0 {
		cfg.ExtractTimeout = PDFExtractTimeout
	}
	js := &jsonschema.Schema{}
	if err := json.Unmarshal([]byte(readAttachmentParamsJSON), js); err != nil {
		return nil, fmt.Errorf("read attachment schema: %w", err)
	}
	return &ReadAttachmentTool{
		cfg: cfg,
		info: &schema.ToolInfo{
			Name:        ReadAttachmentToolName,
			Desc:        "Read text from a user-attached PDF. Use fileId from the <actweave_attachments> listing. Optional pages (default 1-10, max 20). Office and zip are listed but cannot be read.",
			ParamsOneOf: schema.NewParamsOneOfByJSONSchema(js),
		},
	}, nil
}

func (t *ReadAttachmentTool) Info(context.Context) (*schema.ToolInfo, error) {
	if t == nil || t.info == nil {
		return nil, ErrInvalid
	}
	return t.info, nil
}

func (t *ReadAttachmentTool) InvokableRun(
	ctx context.Context,
	argumentsInJSON string,
	_ ...tool.Option,
) (string, error) {
	if t == nil || t.cfg.Opener == nil {
		return readErrorJSON(ErrorCodeInvalid, "Read attachment is not configured."), nil
	}
	args, err := parseReadArgs(argumentsInJSON)
	if err != nil {
		return readErrorJSON(ErrorCodeInvalid, "Read attachment arguments are invalid."), nil
	}
	if _, ok := t.cfg.ReadableFileIDs[args.FileID]; !ok {
		return readErrorJSON(ErrorCodeNotFound, "The file was not found."), nil
	}
	opened, err := t.cfg.Opener.OpenReadyFile(ctx, t.cfg.WorkspaceID, args.FileID)
	if err != nil {
		code, msg := mapReadOpenError(err)
		return readErrorJSON(code, msg), nil
	}
	defer opened.Body.Close()
	if t.cfg.AgentID != "" && strings.TrimSpace(opened.Meta.AgentID) != "" &&
		!strings.EqualFold(opened.Meta.AgentID, t.cfg.AgentID) {
		return readErrorJSON(ErrorCodeNotFound, "The file was not found."), nil
	}
	media := opened.Meta.DeclaredMedia
	if opened.Meta.DetectedMedia != "" {
		media = opened.Meta.DetectedMedia
	}
	media, _ = NormalizeMediaType(media)
	if media != MediaTypePDF {
		return readErrorJSON(ErrorCodeMediaTypeDenied, "This media type cannot be read."), nil
	}
	limited := io.LimitReader(opened.Body, t.cfg.MaxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return readErrorJSON(ErrorCodeProcessingFailed, "The file could not be read."), nil
	}
	if int64(len(body)) > t.cfg.MaxBytes {
		return readErrorJSON(ErrorCodeSizeExceeded, "The file size exceeds the configured limit."), nil
	}
	extractCtx := ctx
	if t.cfg.ExtractTimeout > 0 {
		var cancel context.CancelFunc
		extractCtx, cancel = context.WithTimeout(ctx, t.cfg.ExtractTimeout)
		defer cancel()
	}
	extracted, err := ExtractPDFText(extractCtx, body, args.Pages, t.cfg.MaxTextBytes)
	if err != nil {
		code, msg := mapReadExtractError(err)
		return readErrorJSON(code, msg), nil
	}
	pages := fmt.Sprintf("%d-%d", extracted.StartPage, extracted.EndPage)
	if extracted.StartPage == extracted.EndPage {
		pages = fmt.Sprintf("%d", extracted.StartPage)
	}
	out := map[string]any{
		"ok":        true,
		"fileId":    args.FileID,
		"filename":  opened.Meta.Filename,
		"mediaType": MediaTypePDF,
		"pages":     pages,
		"pageCount": extracted.PageCount,
		"truncated": extracted.Truncated,
		"text":      extracted.Text,
	}
	if extracted.NoTextLayer {
		out["warning"] = "NO_TEXT_LAYER"
	}
	encoded, err := json.Marshal(AllowlistedReadResult(out))
	if err != nil {
		return readErrorJSON(ErrorCodeInvalid, "Read attachment failed."), nil
	}
	return string(encoded), nil
}

type readArgs struct {
	FileID string `json:"fileId"`
	Pages  string `json:"pages"`
}

func parseReadArgs(raw string) (readArgs, error) {
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
	dec.DisallowUnknownFields()
	var args readArgs
	if err := dec.Decode(&args); err != nil {
		return readArgs{}, ErrInvalid
	}
	args.FileID = strings.ToLower(strings.TrimSpace(args.FileID))
	args.Pages = strings.TrimSpace(args.Pages)
	if _, err := uuid.Parse(args.FileID); err != nil {
		return readArgs{}, ErrInvalid
	}
	return args, nil
}

// AllowlistedReadArgs rebuilds tool arguments with only fileId/pages.
func AllowlistedReadArgs(raw string) json.RawMessage {
	args, err := parseReadArgs(raw)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	body := map[string]any{"fileId": args.FileID}
	if args.Pages != "" {
		body["pages"] = args.Pages
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

// AllowlistedReadResult keeps only the documented result keys (no URL).
func AllowlistedReadResult(body map[string]any) map[string]any {
	if body == nil {
		return map[string]any{"ok": false}
	}
	out := map[string]any{}
	if ok, exists := body["ok"].(bool); exists && ok {
		out["ok"] = true
		for _, key := range []string{"fileId", "filename", "mediaType", "pages", "text", "warning"} {
			if v, has := body[key]; has {
				out[key] = v
			}
		}
		if v, has := body["pageCount"]; has {
			out["pageCount"] = v
		}
		if v, has := body["truncated"]; has {
			out["truncated"] = v
		}
	} else {
		out["ok"] = false
		if v, has := body["errorCode"]; has {
			out["errorCode"] = v
		}
		if v, has := body["message"]; has {
			out["message"] = v
		}
	}
	return out
}

func readErrorJSON(code, message string) string {
	encoded, err := json.Marshal(AllowlistedReadResult(map[string]any{
		"ok": false, "errorCode": code, "message": message,
	}))
	if err != nil {
		return `{"ok":false,"errorCode":"FILE_INVALID"}`
	}
	return string(encoded)
}

func mapReadOpenError(err error) (string, string) {
	if err == nil {
		return ErrorCodeInvalid, "Read attachment failed."
	}
	if err == toolruntime.ErrPlatformFileNotFound {
		return ErrorCodeNotFound, "The file was not found."
	}
	if err == toolruntime.ErrPlatformFileNotReady {
		return ErrorCodeNotReady, "The file is not ready."
	}
	return ErrorCodeNotFound, "The file was not found."
}

func mapReadExtractError(err error) (string, string) {
	if err == nil {
		return ErrorCodeInvalid, "Read attachment failed."
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, ErrorCodeInvalid):
		return ErrorCodeInvalid, "Read attachment arguments are invalid."
	case strings.Contains(msg, ErrorCodeProcessingFailed):
		return ErrorCodeProcessingFailed, "The file could not be processed."
	default:
		return ErrorCodeProcessingFailed, "The file could not be processed."
	}
}

// ParseReadResultStatus extracts ok + errorCode from allowlisted result JSON.
func ParseReadResultStatus(raw string) (ok bool, errorCode string) {
	var body struct {
		OK        bool   `json:"ok"`
		ErrorCode string `json:"errorCode"`
	}
	if json.Unmarshal([]byte(raw), &body) != nil {
		return false, ErrorCodeInvalid
	}
	return body.OK, strings.TrimSpace(body.ErrorCode)
}
