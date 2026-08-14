package aapfile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"actweave/backend/internal/agentaccessauth"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// publishAttachmentParamsJSON is the v1 text-only tool schema. additionalProperties
// false rejects base64. maxLength is code points; InvokableRun also checks UTF-8 bytes.
const publishAttachmentParamsJSON = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["filename", "mediaType", "text"],
  "properties": {
    "filename": {"type": "string", "minLength": 1, "maxLength": 255},
    "mediaType": {
      "type": "string",
      "enum": ["text/plain", "text/csv", "text/markdown", "application/json"]
    },
    "text": {
      "type": "string",
      "minLength": 1,
      "maxLength": 262144,
      "description": "UTF-8 body only. No base64. Hard cap MaxPublishTextBytes."
    }
  }
}`

// GeneratedIngester is the ingest surface used by the publish tool.
type GeneratedIngester interface {
	IngestGenerated(ctx context.Context, in IngestGeneratedInput) (File, error)
}

// PublishAttachmentConfig binds ingest + quota + run identity for one InvokableTool.
type PublishAttachmentConfig struct {
	Ingest    GeneratedIngester
	Collector *OutboundCollector
	Scope     Scope
	Principal agentaccessauth.AAPAccessTokenPrincipal
	ClientID  string
	// AgentPolicyVersion is copied from the frozen run principal.
	AgentPolicyVersion int64
	SourceRunID        string
}

// PublishAttachmentTool is the Eino InvokableTool for actweave.publish_attachment.
// It is not a PipelineTool and does not call InvokeResolved / HITL.
type PublishAttachmentTool struct {
	cfg  PublishAttachmentConfig
	info *schema.ToolInfo
}

var _ tool.InvokableTool = (*PublishAttachmentTool)(nil)

// NewPublishAttachmentTool constructs the text-only publish tool.
func NewPublishAttachmentTool(cfg PublishAttachmentConfig) (*PublishAttachmentTool, error) {
	if cfg.Ingest == nil || cfg.Collector == nil {
		return nil, ErrInvalid
	}
	js := &jsonschema.Schema{}
	if err := json.Unmarshal([]byte(publishAttachmentParamsJSON), js); err != nil {
		return nil, fmt.Errorf("publish attachment schema: %w", err)
	}
	return &PublishAttachmentTool{
		cfg: cfg,
		info: &schema.ToolInfo{
			Name:        PublishAttachmentToolName,
			Desc:        "Publish a small text file (plain, CSV, Markdown, or JSON) for the user. Call this instead of inventing fileIds or URLs. Refer to the file by filename in your reply.",
			ParamsOneOf: schema.NewParamsOneOfByJSONSchema(js),
		},
	}, nil
}

func (t *PublishAttachmentTool) Info(context.Context) (*schema.ToolInfo, error) {
	if t == nil || t.info == nil {
		return nil, ErrInvalid
	}
	return t.info, nil
}

func (t *PublishAttachmentTool) InvokableRun(
	ctx context.Context,
	argumentsInJSON string,
	_ ...tool.Option,
) (string, error) {
	if t == nil || t.cfg.Ingest == nil || t.cfg.Collector == nil {
		return publishErrorJSON(ErrorCodeInvalid, "Publish attachment is not configured."), nil
	}
	args, err := parsePublishArgs(argumentsInJSON)
	if err != nil {
		return publishErrorJSON(ErrorCodeInvalid, "Publish attachment arguments are invalid."), nil
	}
	body := []byte(args.Text)
	if len(body) == 0 || len(body) > MaxPublishTextBytes {
		return publishErrorJSON(ErrorCodeSizeExceeded, "The file size exceeds the configured limit."), nil
	}
	if err := t.cfg.Collector.TryReserve(1, int64(len(body))); err != nil {
		code, msg := mapPublishError(err)
		return publishErrorJSON(code, msg), nil
	}
	reserved := true
	defer func() {
		if reserved {
			t.cfg.Collector.Release(1, int64(len(body)))
		}
	}()

	sum := sha256.Sum256(body)
	file, err := t.cfg.Ingest.IngestGenerated(ctx, IngestGeneratedInput{
		Scope:              t.cfg.Scope,
		Principal:          t.cfg.Principal,
		ClientID:           t.cfg.ClientID,
		AgentPolicyVersion: t.cfg.AgentPolicyVersion,
		Filename:           args.Filename,
		MediaType:          args.MediaType,
		SizeBytes:          int64(len(body)),
		SHA256:             hex.EncodeToString(sum[:]),
		Body:               bytes.NewReader(body),
		SourceRunID:        t.cfg.SourceRunID,
	})
	if err != nil {
		code, msg := mapPublishError(err)
		return publishErrorJSON(code, msg), nil
	}
	reserved = false
	t.cfg.Collector.Remember(file)
	return string(AllowlistedPublishResult(true, file, "", "")), nil
}

type publishArgs struct {
	Filename  string `json:"filename"`
	MediaType string `json:"mediaType"`
	Text      string `json:"text"`
}

func parsePublishArgs(raw string) (publishArgs, error) {
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
	dec.DisallowUnknownFields()
	var args publishArgs
	if err := dec.Decode(&args); err != nil {
		return publishArgs{}, ErrInvalid
	}
	args.Filename = strings.TrimSpace(args.Filename)
	args.MediaType = strings.TrimSpace(args.MediaType)
	if !validOutboundFilename(args.Filename) {
		return publishArgs{}, ErrInvalid
	}
	media, err := NormalizeMediaType(args.MediaType)
	if err != nil || !AllowedPublishMediaType(media) {
		return publishArgs{}, ErrInvalid
	}
	args.MediaType = media
	if args.Text == "" {
		return publishArgs{}, ErrInvalid
	}
	return args, nil
}

// AllowedPublishMediaType reports whether mediaType is in the v1 tool enum.
func AllowedPublishMediaType(mediaType string) bool {
	normalized, err := NormalizeMediaType(mediaType)
	if err != nil {
		return false
	}
	for _, allowed := range PublishAttachmentMediaTypes {
		if normalized == allowed {
			return true
		}
	}
	return false
}

// AllowlistedPublishArgs rebuilds tool arguments with only filename/mediaType/text.
func AllowlistedPublishArgs(raw string) json.RawMessage {
	args, err := parsePublishArgs(raw)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	encoded, err := json.Marshal(map[string]any{
		"filename":  args.Filename,
		"mediaType": args.MediaType,
		"text":      args.Text,
	})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

// AllowlistedPublishResult rebuilds a model/protocol result without URL keys.
func AllowlistedPublishResult(ok bool, file File, errorCode, message string) json.RawMessage {
	if !ok {
		body := map[string]any{"ok": false}
		if errorCode != "" {
			body["errorCode"] = errorCode
		}
		if message != "" {
			body["message"] = message
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			return json.RawMessage(`{"ok":false,"errorCode":"FILE_INVALID"}`)
		}
		return encoded
	}
	filename := ""
	if file.Filename != nil {
		filename = *file.Filename
	}
	sha := ""
	if file.SHA256 != nil {
		sha = *file.SHA256
	}
	encoded, err := json.Marshal(map[string]any{
		"ok":        true,
		"fileId":    file.ID,
		"filename":  filename,
		"mediaType": file.DeclaredMediaType,
		"sizeBytes": file.SizeBytes,
		"sha256":    sha,
	})
	if err != nil {
		return json.RawMessage(`{"ok":false,"errorCode":"FILE_INVALID"}`)
	}
	return encoded
}

func publishErrorJSON(code, message string) string {
	return string(AllowlistedPublishResult(false, File{}, code, message))
}

func mapPublishError(err error) (string, string) {
	if err == nil {
		return ErrorCodeInvalid, "Publish attachment failed."
	}
	if errors.Is(err, ErrFeatureDisabled) {
		return ErrorCodeFeatureDisabled, "Outbound attachments are disabled."
	}
	if errors.Is(err, ErrInvalid) {
		return ErrorCodeInvalid, "Publish attachment input is invalid."
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, ErrorCodeOutboundTurnLimit):
		return ErrorCodeOutboundTurnLimit, "This turn already has the maximum number of attachments."
	case strings.Contains(msg, ErrorCodeSizeExceeded):
		return ErrorCodeSizeExceeded, "The file size exceeds the configured limit."
	case strings.Contains(msg, ErrorCodeIntegrityMismatch):
		return ErrorCodeIntegrityMismatch, "File integrity check failed."
	case strings.Contains(msg, ErrorCodeMediaTypeMismatch):
		return ErrorCodeMediaTypeMismatch, "Declared media type does not match the body."
	case strings.Contains(msg, ErrorCodeMediaTypeDenied):
		return ErrorCodeMediaTypeDenied, "This media type is not allowed."
	default:
		return ErrorCodeInvalid, "Publish attachment failed."
	}
}

// ParsePublishResultStatus extracts ok + errorCode from allowlisted result JSON.
func ParsePublishResultStatus(raw string) (ok bool, errorCode string) {
	var body struct {
		OK        bool   `json:"ok"`
		ErrorCode string `json:"errorCode"`
	}
	if json.Unmarshal([]byte(raw), &body) != nil {
		return false, ErrorCodeInvalid
	}
	return body.OK, strings.TrimSpace(body.ErrorCode)
}
