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
	Filename          string
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

// AttachmentsMarker is the stable wrapper used for assembly-time document listings.
const AttachmentsMarker = "actweave_attachments"

// MultimodalAssembler builds schema.Message values for the model from durable
// chat content. READY image input_file parts are assembled as UserInputMultiContent
// (base64, no URLs) when RuntimeMultimodal is true. Inbound-allowlisted non-image
// files become an assembly-time <actweave_attachments> listing (never fail-closed,
// never inlined as bytes/URLs). Unknown media types fail with ErrModelContentUnsupported.
type MultimodalAssembler struct {
	// RuntimeMultimodal gates image assembly (config.AgentAccessFiles.RuntimeMultimodal).
	// Document listings do not require this flag; images still fail closed when false.
	RuntimeMultimodal bool
	// Files loads file metadata and (for images) permanent bodies.
	// Required when messages contain input_file.
	Files MultimodalFileSource
	// MaxBytes caps image body reads (0 → 25 MiB).
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

// inboundDocumentMediaTypes are allowlisted non-image types that assemble as a
// listing stub instead of provider file_url (KD-IR-4).
var inboundDocumentMediaTypes = map[string]struct{}{
	"application/pdf":    {},
	"application/msword": {},
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": {},
	"application/vnd.ms-excel": {},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": {},
	"application/zip":              {},
	"application/x-zip-compressed": {},
}

// IsVisionMediaType reports whether media is assemblable as image_url.
func IsVisionMediaType(mediaType string) bool {
	_, ok := visionMediaTypes[normalizeAssemblyMediaType(mediaType)]
	return ok
}

// IsInboundDocumentMediaType reports whether media is listed (not vision, not unknown).
func IsInboundDocumentMediaType(mediaType string) bool {
	_, ok := inboundDocumentMediaTypes[normalizeAssemblyMediaType(mediaType)]
	return ok
}

func normalizeAssemblyMediaType(mediaType string) string {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if idx := strings.IndexByte(mediaType, ';'); idx >= 0 {
		mediaType = strings.TrimSpace(mediaType[:idx])
	}
	if mediaType == "image/jpg" {
		return "image/jpeg"
	}
	return mediaType
}

// AssembleUserMessage maps a durable user message body to a model schema message.
//
//   - Legacy plain text / non-v1 JSON → Content string (Console/history compat).
//   - aap.message-content.v1 text-only → Content = joined text (not raw JSON).
//   - v1 with image input_file → UserInputMultiContent when RuntimeMultimodal.
//   - v1 with allowlisted document input_file → listing appended to user text.
func (a *MultimodalAssembler) AssembleUserMessage(
	ctx context.Context,
	workspaceID, agentID, content string,
) (*schema.Message, error) {
	assembled, err := a.assembleUser(ctx, workspaceID, agentID, content)
	if err != nil {
		return nil, err
	}
	if assembled.legacy {
		return schema.UserMessage(assembled.text), nil
	}
	if len(assembled.images) == 0 {
		if assembled.text == "" {
			return nil, ErrModelContentUnsupported
		}
		return schema.UserMessage(assembled.text), nil
	}
	multi := make([]schema.MessageInputPart, 0, 1+len(assembled.images))
	if assembled.text != "" {
		multi = append(multi, schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeText,
			Text: assembled.text,
		})
	}
	for _, img := range assembled.images {
		encoded := img.base64
		multi = append(multi, schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeImageURL,
			Image: &schema.MessageInputImage{
				MessagePartCommon: schema.MessagePartCommon{
					Base64Data: &encoded,
					MIMEType:   img.mime,
				},
				Detail: schema.ImageURLDetailAuto,
			},
		})
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
	Filename  string `json:"filename,omitempty"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

type assembledUser struct {
	legacy bool
	text   string
	images []assembledImage
}

type assembledImage struct {
	base64 string
	mime   string
}

type listingEntry struct {
	fileID    string
	filename  string
	mediaType string
	sizeBytes int64
}

func (a *MultimodalAssembler) assembleUser(
	ctx context.Context,
	workspaceID, agentID, content string,
) (assembledUser, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return assembledUser{}, ErrModelContentUnsupported
	}
	parts, ok := parseMessageContentV1(content)
	if !ok {
		return assembledUser{legacy: true, text: content}, nil
	}
	if len(parts) == 0 {
		return assembledUser{}, ErrModelContentUnsupported
	}

	hasFile := false
	for _, p := range parts {
		switch p.Type {
		case "input_file":
			hasFile = true
		case "a2ui":
			return assembledUser{}, fmt.Errorf("%w: a2ui content parts are not accepted on user messages", ErrModelContentUnsupported)
		default:
			if p.Type != "text" {
				return assembledUser{}, fmt.Errorf("%w: unsupported content part type %q", ErrModelContentUnsupported, p.Type)
			}
		}
	}

	text := joinTextParts(parts)
	if !hasFile {
		if text == "" {
			return assembledUser{}, ErrModelContentUnsupported
		}
		return assembledUser{text: text}, nil
	}
	if a == nil || a.Files == nil {
		return assembledUser{}, fmt.Errorf("%w: file source unavailable for input_file", ErrModelContentUnsupported)
	}

	maxBytes := a.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMultimodalMaxBytes
	}

	var (
		images    []assembledImage
		listing   []listingEntry
		hasVision bool
	)
	for _, p := range parts {
		if p.Type != "input_file" {
			continue
		}
		kind, meta, err := a.classifyInputFile(ctx, workspaceID, agentID, p)
		if err != nil {
			return assembledUser{}, err
		}
		switch kind {
		case fileKindVision:
			hasVision = true
			img, err := a.openVisionImage(ctx, workspaceID, meta, maxBytes)
			if err != nil {
				return assembledUser{}, err
			}
			images = append(images, img)
		case fileKindDocument:
			listing = append(listing, listingEntry{
				fileID:    meta.ID,
				filename:  meta.Filename,
				mediaType: normalizeAssemblyMediaType(firstNonEmptyMedia(p.MediaType, meta.DetectedMediaType, meta.DeclaredMediaType)),
				sizeBytes: meta.SizeBytes,
			})
		default:
			media := normalizeAssemblyMediaType(firstNonEmptyMedia(p.MediaType, meta.DetectedMediaType, meta.DeclaredMediaType))
			return assembledUser{}, fmt.Errorf(
				"%w: media type %q cannot be assembled for the model provider",
				ErrModelContentUnsupported, media,
			)
		}
	}
	if hasVision && (a == nil || !a.RuntimeMultimodal) {
		return assembledUser{}, fmt.Errorf("%w: multimodal runtime unavailable for input_file", ErrModelContentUnsupported)
	}
	text = appendAttachmentListing(text, formatAttachmentListing(listing))
	if text == "" && len(images) == 0 {
		return assembledUser{}, ErrModelContentUnsupported
	}
	return assembledUser{text: text, images: images}, nil
}

const (
	fileKindVision   = "vision"
	fileKindDocument = "document"
	fileKindUnknown  = "unknown"
)

func (a *MultimodalAssembler) classifyInputFile(
	ctx context.Context,
	workspaceID, agentID string,
	part contentPartWire,
) (kind string, meta MultimodalFileMeta, err error) {
	fileID := strings.TrimSpace(part.FileID)
	if fileID == "" {
		return "", MultimodalFileMeta{}, fmt.Errorf("%w: input_file missing fileId", ErrModelContentUnsupported)
	}
	meta, err = a.Files.GetFile(ctx, workspaceID, fileID)
	if err != nil {
		return "", MultimodalFileMeta{}, fmt.Errorf("%w: load file: %v", ErrModelContentUnsupported, err)
	}
	if meta.ID == "" {
		meta.ID = fileID
	}
	if strings.TrimSpace(meta.WorkspaceID) != "" &&
		!strings.EqualFold(meta.WorkspaceID, workspaceID) {
		return "", MultimodalFileMeta{}, fmt.Errorf("%w: file workspace mismatch", ErrModelContentUnsupported)
	}
	if agentID != "" && strings.TrimSpace(meta.AgentID) != "" &&
		!strings.EqualFold(meta.AgentID, agentID) {
		return "", MultimodalFileMeta{}, fmt.Errorf("%w: file agent mismatch", ErrModelContentUnsupported)
	}
	if !strings.EqualFold(strings.TrimSpace(meta.Status), "READY") {
		return "", MultimodalFileMeta{}, fmt.Errorf("%w: file not READY", ErrModelContentUnsupported)
	}
	mediaType := normalizeAssemblyMediaType(firstNonEmptyMedia(part.MediaType, meta.DetectedMediaType, meta.DeclaredMediaType))
	if IsVisionMediaType(mediaType) {
		return fileKindVision, meta, nil
	}
	if IsInboundDocumentMediaType(mediaType) {
		return fileKindDocument, meta, nil
	}
	return fileKindUnknown, meta, nil
}

func (a *MultimodalAssembler) openVisionImage(
	ctx context.Context,
	workspaceID string,
	meta MultimodalFileMeta,
	maxBytes int64,
) (assembledImage, error) {
	objectID := strings.TrimSpace(meta.StoredObjectID)
	if objectID == "" {
		return assembledImage{}, fmt.Errorf("%w: file has no permanent object", ErrModelContentUnsupported)
	}
	if meta.SizeBytes > maxBytes {
		return assembledImage{}, fmt.Errorf("%w: file exceeds assembly size limit", ErrModelContentUnsupported)
	}
	body, err := a.Files.OpenFileBytes(ctx, workspaceID, objectID)
	if err != nil {
		return assembledImage{}, fmt.Errorf("%w: open file body: %v", ErrModelContentUnsupported, err)
	}
	if int64(len(body)) > maxBytes {
		return assembledImage{}, fmt.Errorf("%w: file body exceeds assembly size limit", ErrModelContentUnsupported)
	}
	if len(body) == 0 {
		return assembledImage{}, fmt.Errorf("%w: empty file body", ErrModelContentUnsupported)
	}
	mediaType := normalizeAssemblyMediaType(firstNonEmptyMedia(meta.DetectedMediaType, meta.DeclaredMediaType))
	if !IsVisionMediaType(mediaType) {
		mediaType = "image/png"
	}
	return assembledImage{
		base64: base64.StdEncoding.EncodeToString(body),
		mime:   mediaType,
	}, nil
}

func firstNonEmptyMedia(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func formatAttachmentListing(entries []listingEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<" + AttachmentsMarker + ">\n")
	for _, e := range entries {
		b.WriteString("<file id=\"")
		b.WriteString(xmlAttr(e.fileID))
		b.WriteString("\" name=\"")
		b.WriteString(xmlAttr(e.filename))
		b.WriteString("\" mediaType=\"")
		b.WriteString(xmlAttr(e.mediaType))
		b.WriteString("\" sizeBytes=\"")
		b.WriteString(fmt.Sprintf("%d", e.sizeBytes))
		b.WriteString("\"/>\n")
	}
	b.WriteString("</" + AttachmentsMarker + ">")
	return b.String()
}

// FormatAttachmentListingFromParts builds a listing from durable wire parts
// (used by token estimates; assembly listing uses GetFile metadata).
func FormatAttachmentListingFromParts(content string) string {
	parts, ok := parseMessageContentV1(content)
	if !ok {
		return ""
	}
	var entries []listingEntry
	for _, p := range parts {
		if p.Type != "input_file" {
			continue
		}
		media := normalizeAssemblyMediaType(p.MediaType)
		if IsVisionMediaType(media) {
			continue
		}
		if media != "" && !IsInboundDocumentMediaType(media) {
			continue
		}
		if media == "" {
			// Envelope omitted type: still mention the fileId so the estimate
			// is not zero. Assembly will load the real type.
			media = "application/octet-stream"
		}
		entries = append(entries, listingEntry{
			fileID:    strings.TrimSpace(p.FileID),
			filename:  strings.TrimSpace(p.Filename),
			mediaType: media,
			sizeBytes: p.SizeBytes,
		})
	}
	return formatAttachmentListing(entries)
}

func appendAttachmentListing(text, listing string) string {
	listing = strings.TrimSpace(listing)
	if listing == "" {
		return text
	}
	if strings.Contains(text, "<"+AttachmentsMarker+">") {
		return text
	}
	text = strings.TrimRight(text, " \t\r\n")
	if text == "" {
		return listing
	}
	return text + "\n\n" + listing
}

func xmlAttr(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, `"`, "&quot;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	return value
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
	kind, meta, err := a.classifyInputFile(ctx, workspaceID, agentID, part)
	if err != nil {
		return schema.MessageInputPart{}, err
	}
	if kind != fileKindVision {
		return schema.MessageInputPart{}, fmt.Errorf(
			"%w: media type cannot be assembled as image_url",
			ErrModelContentUnsupported,
		)
	}
	img, err := a.openVisionImage(ctx, workspaceID, meta, maxBytes)
	if err != nil {
		return schema.MessageInputPart{}, err
	}
	encoded := img.base64
	return schema.MessageInputPart{
		Type: schema.ChatMessagePartTypeImageURL,
		Image: &schema.MessageInputImage{
			MessagePartCommon: schema.MessagePartCommon{
				Base64Data: &encoded,
				MIMEType:   img.mime,
			},
			Detail: schema.ImageURLDetailAuto,
		},
	}, nil
}

// LimitReader is exported for tests/adapters that stream with a size cap.
func LimitReader(r io.Reader, n int64) io.Reader {
	return io.LimitReader(r, n)
}

// TextForTokenEstimate returns plain text suitable for context estimators.
// For aap.message-content.v1 it joins text parts and the document listing
// (never tool/reasoning JSON, never image bytes).
// ok=false means content is not v1; callers should use the raw string.
func TextForTokenEstimate(content string) (string, bool) {
	parts, ok := parseMessageContentV1(content)
	if !ok {
		return "", false
	}
	return appendAttachmentListing(joinTextParts(parts), FormatAttachmentListingFromParts(content)), true
}

// AssembleUserAgenticMessage maps durable user content to a validated Agentic
// user message (Task 4A). Text → UserInputText; READY vision input_file →
// UserInputImage (base64); documents → listing on the text block.
// Never projects tool/reasoning/search blocks into public text.
func (a *MultimodalAssembler) AssembleUserAgenticMessage(
	ctx context.Context,
	workspaceID, agentID, content string,
) (*schema.AgenticMessage, error) {
	assembled, err := a.assembleUser(ctx, workspaceID, agentID, content)
	if err != nil {
		return nil, err
	}
	if assembled.legacy || len(assembled.images) == 0 {
		if assembled.text == "" {
			return nil, ErrModelContentUnsupported
		}
		return schema.UserAgenticMessage(assembled.text), nil
	}
	blocks := make([]*schema.ContentBlock, 0, 1+len(assembled.images))
	if assembled.text != "" {
		blocks = append(blocks, schema.NewContentBlock(&schema.UserInputText{Text: assembled.text}))
	}
	for _, img := range assembled.images {
		blocks = append(blocks, schema.NewContentBlock(&schema.UserInputImage{
			Base64Data: img.base64,
			MIMEType:   img.mime,
			Detail:     schema.ImageURLDetailAuto,
		}))
	}
	if len(blocks) == 0 {
		return nil, ErrModelContentUnsupported
	}
	return &schema.AgenticMessage{
		Role:          schema.AgenticRoleTypeUser,
		ContentBlocks: blocks,
	}, nil
}
