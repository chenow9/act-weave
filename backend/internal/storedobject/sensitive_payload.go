package storedobject

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type payloadSecureStore interface {
	Put(context.Context, PutInput) (StoredObject, error)
	Open(context.Context, ReadRequest) (OpenedObject, error)
}

type JSONPayloadScrubber interface {
	ScrubJSON(context.Context, json.RawMessage) (json.RawMessage, error)
}

type SensitivePayloadInput struct {
	ObjectID      string
	WorkspaceID   string
	Kind          string
	Request       json.RawMessage
	Response      json.RawMessage
	ErrorCode     string
	CreatedByType string
	CreatedByID   string
}

type SensitivePayloadResult struct {
	ObjectID string
	SHA256   string
	Length   int64
}

type SensitivePayloadWriter struct {
	store    payloadSecureStore
	scrubber JSONPayloadScrubber
}

func NewSensitivePayloadWriter(
	store payloadSecureStore,
	scrubber JSONPayloadScrubber,
) (*SensitivePayloadWriter, error) {
	if store == nil || scrubber == nil {
		return nil, errors.New("sensitive payload store and scrubber are required")
	}
	return &SensitivePayloadWriter{store: store, scrubber: scrubber}, nil
}

func (writer *SensitivePayloadWriter) Write(
	ctx context.Context,
	input SensitivePayloadInput,
) (SensitivePayloadResult, error) {
	if input.Kind != KindToolTestPayload && input.Kind != KindToolInvocationPayload {
		return SensitivePayloadResult{}, ErrInvalid
	}
	request, err := writer.scrubber.ScrubJSON(ctx, input.Request)
	if err != nil {
		return SensitivePayloadResult{}, fmt.Errorf("scrub payload request: %w", err)
	}
	response, err := writer.scrubber.ScrubJSON(ctx, input.Response)
	if err != nil {
		return SensitivePayloadResult{}, fmt.Errorf("scrub payload response: %w", err)
	}
	payload, err := json.Marshal(struct {
		SchemaVersion string          `json:"schemaVersion"`
		Request       json.RawMessage `json:"request"`
		Response      json.RawMessage `json:"response"`
		ErrorCode     string          `json:"errorCode,omitempty"`
	}{SchemaVersion: "tool-payload.v1", Request: request, Response: response,
		ErrorCode: strings.TrimSpace(input.ErrorCode)})
	if err != nil {
		return SensitivePayloadResult{}, err
	}
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	put := PutInput{
		ID: strings.TrimSpace(input.ObjectID), WorkspaceID: strings.TrimSpace(input.WorkspaceID),
		Kind: input.Kind, ContentType: "application/json", SizeBytes: int64(len(payload)), SHA256: hash,
		Classification: ClassificationSensitive, RetentionMode: RetentionPermanent,
		CreatedByType: strings.ToUpper(strings.TrimSpace(input.CreatedByType)),
		CreatedByID:   strings.TrimSpace(input.CreatedByID), Reader: bytes.NewReader(payload),
	}
	created, err := writer.store.Put(ctx, put)
	if err == nil {
		return SensitivePayloadResult{ObjectID: created.ID, SHA256: hash, Length: int64(len(payload))}, nil
	}
	if !errors.Is(err, ErrConflict) {
		return SensitivePayloadResult{}, fmt.Errorf("put sensitive payload: %w", err)
	}
	opened, openErr := writer.store.Open(ctx, ReadRequest{
		WorkspaceID: put.WorkspaceID, ObjectID: put.ID,
		ActorType: put.CreatedByType, ActorID: put.CreatedByID,
	})
	if openErr != nil {
		return SensitivePayloadResult{}, errors.Join(err, openErr)
	}
	existing, readErr := io.ReadAll(opened.Body)
	_ = opened.Body.Close()
	if readErr != nil || opened.Metadata.Kind != input.Kind || !bytes.Equal(existing, payload) {
		return SensitivePayloadResult{}, ErrConflict
	}
	return SensitivePayloadResult{ObjectID: put.ID, SHA256: hash, Length: int64(len(payload))}, nil
}

type JSONSecretScrubber struct{ sensitiveValues map[string]struct{} }

func NewJSONSecretScrubber(sensitiveValues ...string) *JSONSecretScrubber {
	values := make(map[string]struct{}, len(sensitiveValues))
	for _, value := range sensitiveValues {
		if value = strings.TrimSpace(value); value != "" {
			values[value] = struct{}{}
		}
	}
	return &JSONSecretScrubber{sensitiveValues: values}
}

func (scrubber *JSONSecretScrubber) ScrubJSON(
	_ context.Context,
	raw json.RawMessage,
) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage(`null`), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, ErrInvalid
	}
	return json.Marshal(scrubber.scrubValue(value, ""))
}

func (scrubber *JSONSecretScrubber) scrubValue(value any, key string) any {
	if secretPayloadKey(key) {
		return "[REDACTED]"
	}
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			typed[childKey] = scrubber.scrubValue(child, childKey)
		}
		return typed
	case []any:
		for index, child := range typed {
			typed[index] = scrubber.scrubValue(child, "")
		}
		return typed
	case string:
		if _, sensitive := scrubber.sensitiveValues[typed]; sensitive {
			return "[REDACTED]"
		}
	}
	return value
}

func secretPayloadKey(key string) bool {
	normalized := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(strings.TrimSpace(key)))
	switch normalized {
	case "password", "passwd", "authorization", "proxyauthorization", "cookie", "setcookie",
		"token", "accesstoken", "refreshtoken", "idtoken", "apikey", "clientsecret",
		"secret", "secretvalue", "privatekey":
		return true
	default:
		return false
	}
}
