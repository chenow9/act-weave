package chatruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// Stable error code for run.failed when provider/runtime cannot assemble
// input_file content for the model (design §5.8 / KD-23). Distinct from
// FILE_RUNTIME_UNAVAILABLE which is only returned at createRun when
// RuntimeMultimodal is off.
const ErrCodeModelContentUnsupported = "MODEL_CONTENT_UNSUPPORTED"

// ErrModelContentUnsupported is returned when message parts include input_file
// that cannot be assembled into model content (unsupported media type, runtime
// flag off at assembly time, missing file source, non-READY file, etc.).
// Callers must fail the run with this code and must never silently drop parts.
var ErrModelContentUnsupported = errors.New(ErrCodeModelContentUnsupported)

// MessageContentSchemaVersion is the durable AAP createRun message body schema.
const MessageContentSchemaVersion = "aap.message-content.v1"

// MultimodalFileMeta is the minimum file fact needed for model assembly.
type MultimodalFileMeta struct {
	ID                string
	WorkspaceID       string
	AgentID           string
	Status            string
	StoredObjectID    string
	DeclaredMediaType string
	DetectedMediaType string
	SizeBytes         int64
}

// MultimodalFileSource loads READY AAP file metadata and permanent body bytes.
// Implementations open via SecureStore with a trusted SYSTEM actor; they must
// never return download URLs.
type MultimodalFileSource interface {
	GetFile(ctx context.Context, workspaceID, fileID string) (MultimodalFileMeta, error)
	// OpenFileBytes returns plaintext body for a permanent stored_object_id.
	OpenFileBytes(ctx context.Context, workspaceID, storedObjectID string) ([]byte, error)
}

// MultimodalAssembler builds schema.Message values for the model from durable
// chat content. When RuntimeMultimodal is true and Files is set, READY image
// input_file parts are assembled as UserInputMultiContent (base64, no URLs).
// Unsupported media types fail with ErrModelContentUnsupported.
type MultimodalAssembler struct {
	// RuntimeMultimodal gates model assembly (config.AgentAccessFiles.RuntimeMultimodal).
	// Orthogonal to files.enabled; createRun already fail-closes when false.
	RuntimeMultimodal bool
	// Files loads permanent AAP file bodies. Required when RuntimeMultimodal
	// is true and messages contain input_file.
	Files MultimodalFileSource
	// MaxBytes caps body reads (0 → 25 MiB).
	MaxBytes int64
}

const defaultMultimodalMaxBytes int64 = 25 << 20

// visionMediaTypes are media types assemblable as OpenAI image_url parts via
// eino-ext / PlatformChatModel (base64 data URLs).
var visionMediaTypes = map[string]struct{}{
	"image/png":  {},
	"image/jpeg": {},
	"image/jpg":  {},
	"image/webp": {},
	"image/gif":  {},
}

// AssembleUserMessage maps a durable user message body to a model schema message.
//
// - Legacy plain text / non-v1 JSON → Content string (Console/history compat).
// - aap.message-content.v1 text-only → Content = joined text (not raw JSON).
// - v1 with input_file → UserInputMultiContent when RuntimeMultimodal + supported
//   image media; otherwise ErrModelContentUnsupported (never drop parts).
func (a *MultimodalAssembler) AssembleUserMessage(
	ctx context.Context,
	workspaceID, agentID, content string,
) (*schema.Message, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, ErrModelContentUnsupported
	}
	parts, ok := parseMessageContentV1(content)
	if !ok {
		// Legacy / Console plain text.
		return schema.UserMessage(content), nil
	}
	if len(parts) == 0 {
		return nil, ErrModelContentUnsupported
	}

	hasFile := false
	for _, p := range parts {
		switch p.Type {
		case "input_file":
			hasFile = true
		case "a2ui":
			// KD-7 / PR-5: a2ui is assistant-outbound only; never accept on user
			// multimodal / history assembly paths.
			return nil, fmt.Errorf("%w: a2ui content parts are not accepted on user messages", ErrModelContentUnsupported)
		}
	}

	// Text-only v1: extract text for the model (do not forward raw JSON envelope).
	if !hasFile {
		text := joinTextParts(parts)
		if text == "" {
			return nil, ErrModelContentUnsupported
		}
		return schema.UserMessage(text), nil
	}

	// input_file present: must assemble or fail explicitly.
	if a == nil || !a.RuntimeMultimodal || a.Files == nil {
		return nil, fmt.Errorf("%w: multimodal runtime unavailable for input_file", ErrModelContentUnsupported)
	}

	maxBytes := a.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMultimodalMaxBytes
	}

	multi := make([]schema.MessageInputPart, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text":
			if p.Text == "" {
				continue
			}
			multi = append(multi, schema.MessageInputPart{
				Type: schema.ChatMessagePartTypeText,
				Text: p.Text,
			})
		case "input_file":
			filePart, err := a.assembleInputFile(ctx, workspaceID, agentID, p, maxBytes)
			if err != nil {
				return nil, err
			}
			multi = append(multi, filePart)
		case "a2ui":
			// Defensive: a2ui already rejected above; keep fail-closed here too.
			return nil, fmt.Errorf("%w: a2ui content parts are not accepted on user messages", ErrModelContentUnsupported)
		default:
			// Unknown part type in durable body: fail closed (never drop).
			return nil, fmt.Errorf("%w: unsupported content part type %q", ErrModelContentUnsupported, p.Type)
		}
	}
	if len(multi) == 0 {
		return nil, ErrModelContentUnsupported
	}
	return &schema.Message{
		Role:                  schema.User,
		UserInputMultiContent: multi,
	}, nil
}

// HasInputFileInContent reports whether durable content references input_file.
func HasInputFileInContent(content string) bool {
	parts, ok := parseMessageContentV1(content)
	if !ok {
		return false
	}
	for _, p := range parts {
		if p.Type == "input_file" {
			return true
		}
	}
	return false
}

type contentPartWire struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	FileID    string `json:"fileId,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
}

func parseMessageContentV1(content string) ([]contentPartWire, bool) {
	var envelope struct {
		SchemaVersion string            `json:"schemaVersion"`
		Parts         []contentPartWire `json:"parts"`
	}
	if err := json.Unmarshal([]byte(content), &envelope); err != nil {
		return nil, false
	}
	if envelope.SchemaVersion != MessageContentSchemaVersion || len(envelope.Parts) == 0 {
		return nil, false
	}
	return envelope.Parts, true
}

func joinTextParts(parts []contentPartWire) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Type != "text" {
			continue
		}
		b.WriteString(p.Text)
	}
	return b.String()
}

func (a *MultimodalAssembler) assembleInputFile(
	ctx context.Context,
	workspaceID, agentID string,
	part contentPartWire,
	maxBytes int64,
) (schema.MessageInputPart, error) {
	fileID := strings.TrimSpace(part.FileID)
	if fileID == "" {
		return schema.MessageInputPart{}, fmt.Errorf("%w: input_file missing fileId", ErrModelContentUnsupported)
	}
	meta, err := a.Files.GetFile(ctx, workspaceID, fileID)
	if err != nil {
		return schema.MessageInputPart{}, fmt.Errorf("%w: load file: %v", ErrModelContentUnsupported, err)
	}
	if strings.TrimSpace(meta.WorkspaceID) != "" &&
		!strings.EqualFold(meta.WorkspaceID, workspaceID) {
		return schema.MessageInputPart{}, fmt.Errorf("%w: file workspace mismatch", ErrModelContentUnsupported)
	}
	if agentID != "" && strings.TrimSpace(meta.AgentID) != "" &&
		!strings.EqualFold(meta.AgentID, agentID) {
		return schema.MessageInputPart{}, fmt.Errorf("%w: file agent mismatch", ErrModelContentUnsupported)
	}
	if !strings.EqualFold(strings.TrimSpace(meta.Status), "READY") {
		return schema.MessageInputPart{}, fmt.Errorf("%w: file not READY", ErrModelContentUnsupported)
	}
	objectID := strings.TrimSpace(meta.StoredObjectID)
	if objectID == "" {
		return schema.MessageInputPart{}, fmt.Errorf("%w: file has no permanent object", ErrModelContentUnsupported)
	}
	if meta.SizeBytes > maxBytes {
		return schema.MessageInputPart{}, fmt.Errorf("%w: file exceeds assembly size limit", ErrModelContentUnsupported)
	}

	mediaType := strings.ToLower(strings.TrimSpace(part.MediaType))
	if mediaType == "" {
		mediaType = strings.ToLower(strings.TrimSpace(meta.DetectedMediaType))
	}
	if mediaType == "" {
		mediaType = strings.ToLower(strings.TrimSpace(meta.DeclaredMediaType))
	}
	// Strip parameters (e.g. charset).
	if idx := strings.IndexByte(mediaType, ';'); idx >= 0 {
		mediaType = strings.TrimSpace(mediaType[:idx])
	}
	if mediaType == "image/jpg" {
		mediaType = "image/jpeg"
	}
	if _, ok := visionMediaTypes[mediaType]; !ok {
		// PDF and any other type: provider path (eino-ext OpenAI) has no stable
		// file_url assembly; fail explicitly rather than drop the part.
		return schema.MessageInputPart{}, fmt.Errorf(
			"%w: media type %q cannot be assembled for the model provider",
			ErrModelContentUnsupported, mediaType,
		)
	}

	body, err := a.Files.OpenFileBytes(ctx, workspaceID, objectID)
	if err != nil {
		return schema.MessageInputPart{}, fmt.Errorf("%w: open file body: %v", ErrModelContentUnsupported, err)
	}
	if int64(len(body)) > maxBytes {
		return schema.MessageInputPart{}, fmt.Errorf("%w: file body exceeds assembly size limit", ErrModelContentUnsupported)
	}
	if len(body) == 0 {
		return schema.MessageInputPart{}, fmt.Errorf("%w: empty file body", ErrModelContentUnsupported)
	}

	encoded := base64.StdEncoding.EncodeToString(body)
	return schema.MessageInputPart{
		Type: schema.ChatMessagePartTypeImageURL,
		Image: &schema.MessageInputImage{
			MessagePartCommon: schema.MessagePartCommon{
				Base64Data: &encoded,
				MIMEType:   mediaType,
			},
			Detail: schema.ImageURLDetailAuto,
		},
	}, nil
}

// LimitReader is exported for tests/adapters that stream with a size cap.
func LimitReader(r io.Reader, n int64) io.Reader {
	return io.LimitReader(r, n)
}
