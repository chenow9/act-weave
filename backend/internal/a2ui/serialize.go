package a2ui

import (
	"encoding/json"
	"fmt"
)

// MessageContentSchemaVersion is the durable AAP multi-part assistant body schema.
// Kept in sync with chat.MessageContentSchemaVersion / chatruntime constant.
const MessageContentSchemaVersion = "aap.message-content.v1"

// SerializeAssistantDurable builds the durable chat_messages content body.
//
//   - payload == nil → plain text string (KD-6 text-only path; zero wire change)
//   - payload != nil → aap.message-content.v1 with parts [text, a2ui]
//
// Empty text is allowed when payload is non-nil (KD-16); the returned envelope
// JSON is still a non-empty string suitable for RecordAssistantResult.
func SerializeAssistantDurable(text string, payload *Payload) (string, error) {
	if payload == nil {
		return text, nil
	}
	if len(payload.Surface) == 0 {
		return "", fmt.Errorf("a2ui: surface required")
	}
	version := payload.Version
	if version == "" {
		version = EnvelopeVersionV0
	}
	a2uiPart := map[string]any{
		"type":    "a2ui",
		"version": version,
		"surface": json.RawMessage(payload.Surface),
	}
	if payload.CatalogID != "" {
		a2uiPart["catalogId"] = payload.CatalogID
	}
	envelope := map[string]any{
		"schemaVersion": MessageContentSchemaVersion,
		"parts": []any{
			map[string]any{"type": "text", "text": text},
			a2uiPart,
		},
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
