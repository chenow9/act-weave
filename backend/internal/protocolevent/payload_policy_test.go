package protocolevent_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/protocolevent"
)

func TestSchemaValidation(t *testing.T) {
	validator := protocolevent.MustDefaultPayloadValidator()
	valid := map[string]json.RawMessage{
		"run.started": json.RawMessage(`{
			"run":{"id":"608f1f2e-7b5a-7c3d-8e9f-123456789008",
			"conversationId":"608f1f2e-7b5a-7c3d-8e9f-123456789006",
			"agentId":"608f1f2e-7b5a-7c3d-8e9f-123456789004","status":"running",
			"trigger":"api","startedAt":"2026-07-20T12:00:00Z"}}
		`),
		"item.delta": json.RawMessage(`{
			"itemId":"608f1f2e-7b5a-7c3d-8e9f-12345678900f",
			"delta":{"type":"text_delta","index":0,"text":"hello"}}
		`),
		"interaction.requested": json.RawMessage(`{
			"interaction":{"id":"608f1f2e-7b5a-7c3d-8e9f-123456789021",
			"kind":"approval","status":"pending",
			"targetItemId":"608f1f2e-7b5a-7c3d-8e9f-12345678900f",
			"title":"Approve","reason":"External change",
			"risk":{"level":"high","reasons":["external_side_effect"]},
			"allowedDecisions":["approve","decline"],"version":1,
			"expiresAt":"2026-07-20T12:10:00Z"}}
		`),
		"usage.updated": json.RawMessage(`{"usage":{"inputTokens":1,"outputTokens":2,"totalTokens":3}}`),
		"future.event":  json.RawMessage(`{"future":{"enabled":true}}`),
	}
	for eventType, data := range valid {
		if err := validator.ValidateEventData(eventType, data); err != nil {
			t.Fatalf("valid %s rejected: %v", eventType, err)
		}
	}

	invalid := map[string]json.RawMessage{
		"run.started":           json.RawMessage(`{"run":{}}`),
		"item.started":          json.RawMessage(`{"item":{"id":"not-a-uuid","type":"notice","status":"completed","code":"X","message":"x"}}`),
		"item.delta":            json.RawMessage(`{"itemId":"608f1f2e-7b5a-7c3d-8e9f-12345678900f","delta":{"type":"text_delta","text":"missing index"}}`),
		"interaction.requested": json.RawMessage(`{"interaction":{"kind":"approval"}}`),
		"usage.updated":         json.RawMessage(`{"usage":{"inputTokens":-1,"outputTokens":0,"totalTokens":0}}`),
		"future.event":          json.RawMessage(`[]`),
	}
	for eventType, data := range invalid {
		if err := validator.ValidateEventData(eventType, data); !errors.Is(err, protocolevent.ErrSchemaValidationFailed) {
			t.Fatalf("invalid %s error=%v", eventType, err)
		}
	}
	validateGoldenTraceSchemas(t, validator)
}

func validateGoldenTraceSchemas(t *testing.T, validator *protocolevent.PayloadValidator) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "protocolschema", "testdata", "aap", "v1", "*.jsonl"))
	if err != nil || len(paths) != 4 {
		t.Fatalf("locate golden traces paths=%v err=%v", paths, err)
	}
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(file)
		line := 0
		for scanner.Scan() {
			line++
			var envelope struct {
				Type string          `json:"type"`
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
				_ = file.Close()
				t.Fatalf("decode %s:%d: %v", path, line, err)
			}
			if err := validator.ValidateEventData(envelope.Type, envelope.Data); err != nil {
				_ = file.Close()
				t.Fatalf("validate %s:%d type=%s: %v", path, line, envelope.Type, err)
			}
		}
		if err := scanner.Err(); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSensitivePayloadPolicy(t *testing.T) {
	validator := protocolevent.MustDefaultPayloadValidator()
	forbidden := []json.RawMessage{
		json.RawMessage(`{"authorization":"Bearer abcdefghijklmnop"}`),
		json.RawMessage(`{"nested":{"accessToken":"opaque-value"}}`),
		json.RawMessage(`{"resume_token":"opaque-value"}`),
		json.RawMessage(`{"clientSecret":"opaque-value"}`),
		json.RawMessage(`{"cookie":"session=opaque"}`),
		json.RawMessage(`{"signedUrl":"https://objects.example.test/a"}`),
		json.RawMessage(`{"chainOfThought":"private reasoning"}`),
		json.RawMessage(`{"message":"Bearer abcdefghijklmnop"}`),
		json.RawMessage(`{"message":"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTYifQ.abcdefghijklmnop"}`),
		json.RawMessage(`{"message":"awsk_abcdefghijklmnop"}`),
		json.RawMessage(`{"url":"https://objects.example.test/a?X-Amz-Signature=abcdef"}`),
	}
	for index, data := range forbidden {
		err := validator.ValidateEventData("future.event", data)
		if !errors.Is(err, protocolevent.ErrSensitivePayload) {
			t.Fatalf("forbidden case %d error=%v", index, err)
		}
		if strings.Contains(strings.ToLower(err.Error()), "opaque") ||
			strings.Contains(strings.ToLower(err.Error()), "bearer") {
			t.Fatalf("error echoed sensitive value: %v", err)
		}
	}

	safe := []json.RawMessage{
		json.RawMessage(`{"inputTokens":1,"outputTokens":2,"totalTokens":3}`),
		json.RawMessage(`{"tokenCount":3,"maxOutputTokens":100,"accessPolicy":"restricted"}`),
		json.RawMessage(`{"url":"https://docs.example.test/tokenization","message":"public summary"}`),
	}
	for index, data := range safe {
		if err := validator.ValidateEventData("future.event", data); err != nil {
			t.Fatalf("safe case %d rejected: %v", index, err)
		}
	}

	policy := protocolevent.DefaultPayloadPolicy()
	policy.AllowedPropertyNames = append(policy.AllowedPropertyNames, "accessToken")
	if _, err := protocolevent.NewPayloadValidator(policy); !errors.Is(err, protocolevent.ErrPayloadPolicyInvalid) {
		t.Fatalf("unsafe allowlist error=%v", err)
	}
	smallPolicy := protocolevent.PayloadPolicy{
		MaxDataBytes: 64, MaxEnvelopeBytes: 128,
		AllowedPropertyNames: []string{"tokenCount"},
	}
	small, err := protocolevent.NewPayloadValidator(smallPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if err := small.ValidateEventData("future.event", json.RawMessage(`{"message":"`+strings.Repeat("a", 80)+`"}`)); !errors.Is(err, protocolevent.ErrPayloadTooLarge) {
		t.Fatalf("oversize data error=%v", err)
	}

	assertAppenderRejectsUnsafePayloadBeforePersistence(t)
}

func assertAppenderRejectsUnsafePayloadBeforePersistence(t *testing.T) {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateTo(t, 40)
	db := testDatabase.Open(t)
	insertProtocolEventFixtures(t, db)
	insertProtocolStream(t, db)
	appender := protocolevent.NewEventAppender()

	tests := []struct {
		name      string
		eventType string
		data      json.RawMessage
		want      error
	}{
		{name: "schema", eventType: "item.delta", data: json.RawMessage(`{"itemId":"bad"}`), want: protocolevent.ErrSchemaValidationFailed},
		{name: "sensitive", eventType: "future.event", data: json.RawMessage(`{"accessToken":"opaque-secret-value"}`), want: protocolevent.ErrSensitivePayload},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			event := unsafePolicyEvent(test.eventType, test.data)
			_, err = appender.AppendInTx(context.Background(), tx, []protocolevent.NewProtocolEvent{event})
			if !errors.Is(err, test.want) {
				_ = tx.Rollback()
				t.Fatalf("append error=%v, want %v", err, test.want)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
	var count int
	var nextSequence int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM protocol_events WHERE stream_id=$1`, protocolStreamID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT next_sequence FROM protocol_event_streams WHERE id=$1`, protocolStreamID).Scan(&nextSequence); err != nil {
		t.Fatal(err)
	}
	if count != 0 || nextSequence != 1 {
		t.Fatalf("rejected payload changed facts count=%d nextSequence=%d", count, nextSequence)
	}
}

func unsafePolicyEvent(eventType string, data json.RawMessage) protocolevent.NewProtocolEvent {
	return protocolevent.NewProtocolEvent{
		ID:            "e08f1f2e-7b5a-7c3d-8e9f-123456789001",
		EventStreamID: protocolStreamID, WorkspaceID: protocolWorkspaceID,
		AgentID: protocolAgentID, ConversationID: protocolSessionID, RunID: protocolRunID,
		Type: eventType, SpecVersion: "1.0", TraceID: "trace-policy",
		OccurredAt: time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC), Data: data,
	}
}
