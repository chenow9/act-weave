package protocolevent_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/protocolschema"

	"github.com/google/uuid"
)

func TestModels(t *testing.T) {
	assertUnknownEnums(t)
	interaction := modelInteraction()
	items := []protocolevent.Item{
		protocolevent.MessageItem{
			ID: uuid.NewString(), Type: protocolevent.ItemTypeMessage,
			Status: protocolevent.ItemStatusCompleted, Role: protocolevent.MessageRoleAssistant,
			Content: []protocolevent.ContentPart{
				protocolevent.TextContentPart{Type: protocolevent.ContentPartTypeText, Text: "hello"},
			},
		},
		protocolevent.ToolCallItem{
			ID: uuid.NewString(), Type: protocolevent.ItemTypeToolCall,
			Status: protocolevent.ItemStatusCompleted, Name: "weather.lookup",
			Arguments: json.RawMessage(`{"city":"Singapore"}`), Output: json.RawMessage(`{"temperatureC":29}`),
		},
		protocolevent.WorkflowStepItem{
			ID: uuid.NewString(), Type: protocolevent.ItemTypeWorkflowStep,
			Status: protocolevent.ItemStatusCompleted, NodeID: "fetch", NodeType: "tool",
			Result: json.RawMessage(`{"ok":true}`),
		},
		protocolevent.InteractionItem{
			ID: uuid.NewString(), Type: protocolevent.ItemTypeInteraction,
			Status: protocolevent.ItemStatusWaiting, Interaction: interaction,
		},
		protocolevent.ArtifactItem{
			ID: uuid.NewString(), Type: protocolevent.ItemTypeArtifact,
			Status: protocolevent.ItemStatusCompleted, ArtifactID: uuid.NewString(), MediaType: "application/json",
		},
		protocolevent.ReasoningSummaryItem{
			ID: uuid.NewString(), Type: protocolevent.ItemTypeReasoningSummary,
			Status: protocolevent.ItemStatusCompleted, Text: "Compared the available options.",
		},
		protocolevent.NoticeItem{
			ID: uuid.NewString(), Type: protocolevent.ItemTypeNotice,
			Status: protocolevent.ItemStatusCompleted, Code: "DONE", Message: "Completed",
		},
	}
	for _, item := range items {
		if err := protocolevent.ValidateItem(item); err != nil {
			t.Fatalf("validate %T: %v", item, err)
		}
		raw, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := protocolevent.DecodeItem(raw)
		if err != nil || decoded.ItemKind() != item.ItemKind() || decoded.ItemID() != item.ItemID() {
			t.Fatalf("round-trip %T decoded=%T err=%v", item, decoded, err)
		}
		encoded, err := json.Marshal(decoded)
		if err != nil || !jsonSemanticallyEqual(raw, encoded) {
			t.Fatalf("item JSON changed for %T: %s -> %s", item, raw, encoded)
		}
	}

	deltas := []protocolevent.Delta{
		protocolevent.TextDelta{Type: protocolevent.DeltaTypeText, Index: 0, Text: "a"},
		protocolevent.ArgumentsJSONDelta{Type: protocolevent.DeltaTypeArgumentsJSON, PartialJSON: `{"city":`},
		protocolevent.OutputDelta{Type: protocolevent.DeltaTypeOutput, Text: "chunk"},
		protocolevent.ProgressDelta{Type: protocolevent.DeltaTypeProgress, Current: 1, Unit: "steps"},
	}
	for _, delta := range deltas {
		if err := protocolevent.ValidateDelta(delta); err != nil {
			t.Fatalf("validate %T: %v", delta, err)
		}
		raw, _ := json.Marshal(delta)
		decoded, err := protocolevent.DecodeDelta(raw)
		if err != nil || decoded.DeltaKind() != delta.DeltaKind() {
			t.Fatalf("round-trip %T decoded=%T err=%v", delta, decoded, err)
		}
	}

	unknownItemRaw := json.RawMessage(`{"id":"608f1f2e-7b5a-7c3d-8e9f-123456789020","type":"future_card","status":"streaming","futureField":{"a":1}}`)
	unknownItem, err := protocolevent.DecodeItem(unknownItemRaw)
	if err != nil || unknownItem.ItemKind() != protocolevent.ItemTypeUnknown {
		t.Fatalf("decode unknown item=%T err=%v", unknownItem, err)
	}
	assertPreservedJSON(t, unknownItemRaw, unknownItem)
	unknownDeltaRaw := json.RawMessage(`{"type":"future_delta","chunk":{"a":1}}`)
	unknownDelta, err := protocolevent.DecodeDelta(unknownDeltaRaw)
	if err != nil || unknownDelta.DeltaKind() != protocolevent.DeltaTypeUnknown {
		t.Fatalf("decode unknown delta=%T err=%v", unknownDelta, err)
	}
	assertPreservedJSON(t, unknownDeltaRaw, unknownDelta)
	unknownPartRaw := json.RawMessage(`{"type":"future_part","value":"opaque"}`)
	unknownPart, err := protocolevent.DecodeContentPart(unknownPartRaw)
	if err != nil || unknownPart.ContentKind() != protocolevent.ContentPartTypeUnknown {
		t.Fatalf("decode unknown content part=%T err=%v", unknownPart, err)
	}
	assertPreservedJSON(t, unknownPartRaw, unknownPart)

	validInput := modelEventInput(protocolevent.EventItemCompleted)
	validItem := protocolevent.ItemSnapshotData{Item: items[len(items)-1]}
	if _, err := protocolevent.BuildProtocolEvent(validInput, validItem); err != nil {
		t.Fatalf("build valid protocol event: %v", err)
	}
	invalid := validInput
	invalid.WorkspaceID = ""
	if _, err := protocolevent.BuildProtocolEvent(invalid, validItem); !errors.Is(err, protocolevent.ErrModelInvalid) {
		t.Fatalf("missing scope error=%v", err)
	}
	if _, err := protocolevent.BuildProtocolEvent(
		modelEventInput(protocolevent.EventRunStarted), validItem,
	); !errors.Is(err, protocolevent.ErrModelTypeMismatch) {
		t.Fatalf("mismatched event data error=%v", err)
	}
}

func TestSchemaRoundTrip(t *testing.T) {
	if actual, expected := protocolevent.KnownEventTypes(), protocolschema.EventTypes(); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("event model/schema drift model=%v schema=%v", actual, expected)
	}
	assertSchemaDiscriminators(t, "item.schema.json", stringValues(protocolevent.KnownItemTypes()))
	assertSchemaDiscriminators(t, "delta.schema.json", stringValues(protocolevent.KnownDeltaTypes()))

	run := modelRun()
	interaction := modelInteraction()
	notice := protocolevent.NoticeItem{
		ID: uuid.NewString(), Type: protocolevent.ItemTypeNotice,
		Status: protocolevent.ItemStatusCompleted, Code: "ROUND_TRIP", Message: "ok",
	}
	examples := map[string]protocolevent.EventData{
		protocolevent.EventRunAccepted:          protocolevent.RunSnapshotData{Run: run},
		protocolevent.EventRunStarted:           protocolevent.RunSnapshotData{Run: run},
		protocolevent.EventRunWaiting:           protocolevent.RunWaitingData{Run: run, InteractionIDs: []string{interaction.ID}},
		protocolevent.EventRunResumed:           protocolevent.RunResumedData{Run: run, InteractionID: interaction.ID},
		protocolevent.EventRunCompleted:         protocolevent.RunSnapshotData{Run: run},
		protocolevent.EventRunFailed:            protocolevent.RunSnapshotData{Run: run},
		protocolevent.EventRunCancelled:         protocolevent.RunSnapshotData{Run: run},
		protocolevent.EventItemStarted:          protocolevent.ItemSnapshotData{Item: notice},
		protocolevent.EventItemDelta:            protocolevent.ItemDeltaData{ItemID: notice.ID, Delta: protocolevent.TextDelta{Type: protocolevent.DeltaTypeText, Index: 0, Text: "x"}},
		protocolevent.EventItemCompleted:        protocolevent.ItemSnapshotData{Item: notice},
		protocolevent.EventInteractionRequested: protocolevent.InteractionData{Interaction: interaction},
		protocolevent.EventInteractionResolved:  protocolevent.InteractionData{Interaction: interaction},
		protocolevent.EventInteractionExpired:   protocolevent.InteractionData{Interaction: interaction},
		protocolevent.EventUsageUpdated:         protocolevent.UsageData{Usage: protocolevent.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}},
	}
	for _, eventType := range protocolevent.KnownEventTypes() {
		data := examples[eventType]
		built, err := protocolevent.BuildProtocolEvent(modelEventInput(eventType), data)
		if err != nil {
			t.Fatalf("build %s: %v", eventType, err)
		}
		if _, exists := protocolschema.EventDataSchema(eventType); !exists {
			t.Fatalf("schema registry lost %s", eventType)
		}
		decoded, err := protocolevent.DecodeEventData(eventType, built.Data)
		if err != nil {
			t.Fatalf("decode %s: %v", eventType, err)
		}
		roundTrip, err := json.Marshal(decoded)
		if err != nil || !jsonSemanticallyEqual(built.Data, roundTrip) {
			t.Fatalf("event data changed for %s: %s -> %s err=%v", eventType, built.Data, roundTrip, err)
		}
	}

	unknownData, err := protocolevent.NewUnknownEventData(json.RawMessage(`{"future":{"enabled":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	unknownEvent, err := protocolevent.BuildProtocolEvent(modelEventInput("future.event"), unknownData)
	if err != nil {
		t.Fatal(err)
	}
	decodedUnknown, err := protocolevent.DecodeEventData(unknownEvent.Type, unknownEvent.Data)
	if err != nil {
		t.Fatal(err)
	}
	assertPreservedJSON(t, unknownEvent.Data, decodedUnknown)
	assertGoldenTraceModelRoundTrip(t)
}

func assertGoldenTraceModelRoundTrip(t *testing.T) {
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
			decoded, err := protocolevent.DecodeEventData(envelope.Type, envelope.Data)
			if err != nil {
				_ = file.Close()
				t.Fatalf("model decode %s:%d type=%s: %v", path, line, envelope.Type, err)
			}
			roundTrip, err := json.Marshal(decoded)
			if err != nil || !jsonSemanticallyEqual(envelope.Data, roundTrip) {
				_ = file.Close()
				t.Fatalf("golden data changed %s:%d type=%s: %s -> %s err=%v",
					path, line, envelope.Type, envelope.Data, roundTrip, err)
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

func assertUnknownEnums(t *testing.T) {
	t.Helper()
	values := []struct {
		name   string
		actual any
		want   any
	}{
		{"event", protocolevent.ParseEventType("future.event"), protocolevent.EventTypeUnknown},
		{"run status", protocolevent.ParseRunStatus("paused"), protocolevent.RunStatusUnknown},
		{"run trigger", protocolevent.ParseRunTrigger("schedule"), protocolevent.RunTriggerUnknown},
		{"item type", protocolevent.ParseItemType("card"), protocolevent.ItemTypeUnknown},
		{"item status", protocolevent.ParseItemStatus("streaming"), protocolevent.ItemStatusUnknown},
		{"role", protocolevent.ParseMessageRole("developer"), protocolevent.MessageRoleUnknown},
		{"content", protocolevent.ParseContentPartType("image"), protocolevent.ContentPartTypeUnknown},
		{"content input_file", protocolevent.ParseContentPartType("input_file"), protocolevent.ContentPartTypeInputFile},
		{"delta", protocolevent.ParseDeltaType("binary_delta"), protocolevent.DeltaTypeUnknown},
		{"interaction kind", protocolevent.ParseInteractionKind("question"), protocolevent.InteractionKindUnknown},
		{"interaction status", protocolevent.ParseInteractionStatus("delegated"), protocolevent.InteractionStatusUnknown},
		{"risk", protocolevent.ParseRiskLevel("extreme"), protocolevent.RiskLevelUnknown},
		{"decision", protocolevent.ParseInteractionDecision("defer"), protocolevent.InteractionDecisionUnknown},
		{"decider", protocolevent.ParseRequiredDecider("owner"), protocolevent.RequiredDeciderUnknown},
	}
	for _, value := range values {
		if !reflect.DeepEqual(value.actual, value.want) {
			t.Fatalf("%s unknown branch=%v, want %v", value.name, value.actual, value.want)
		}
	}
}

func modelRun() protocolevent.Run {
	return protocolevent.Run{
		ID: protocolRunID, ConversationID: protocolSessionID, AgentID: protocolAgentID,
		Status: protocolevent.RunStatusRunning, Trigger: protocolevent.RunTriggerAPI,
		StartedAt: time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC),
	}
}

func modelInteraction() protocolevent.Interaction {
	return protocolevent.Interaction{
		ID: uuid.NewString(), Kind: protocolevent.InteractionKindApproval,
		Status: protocolevent.InteractionStatusPending, TargetItemID: uuid.NewString(),
		Title: "Approve", Reason: "External side effect",
		Risk:         protocolevent.InteractionRisk{Level: protocolevent.RiskLevelHigh, Reasons: []string{"external_side_effect"}},
		InputSummary: json.RawMessage(`{"operation":"write"}`),
		AllowedDecisions: []protocolevent.InteractionDecision{
			protocolevent.InteractionDecisionApprove, protocolevent.InteractionDecisionDecline,
		},
		RequiredDecider: protocolevent.RequiredDeciderSameExternalSubject,
		Version:         1, ExpiresAt: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	}
}

func modelEventInput(eventType string) protocolevent.NewProtocolEvent {
	return protocolevent.NewProtocolEvent{
		ID: uuid.NewString(), EventStreamID: protocolStreamID,
		WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID,
		ConversationID: protocolSessionID, RunID: protocolRunID,
		Type: eventType, SpecVersion: "1.0", TraceID: "trace-model",
		OccurredAt: time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC),
	}
}

func assertSchemaDiscriminators(t *testing.T, documentName string, expected []string) {
	t.Helper()
	raw, err := protocolschema.Document(documentName)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	var discriminators []string
	oneOf, _ := document["oneOf"].([]any)
	definitions, _ := document["$defs"].(map[string]any)
	for _, branchValue := range oneOf {
		branch, _ := branchValue.(map[string]any)
		if ref, _ := branch["$ref"].(string); ref != "" {
			name := ref[len("#/$defs/"):]
			branch, _ = definitions[name].(map[string]any)
		}
		properties, _ := branch["properties"].(map[string]any)
		typeSchema, _ := properties["type"].(map[string]any)
		if value, ok := typeSchema["const"].(string); ok {
			discriminators = append(discriminators, value)
		}
	}
	sort.Strings(discriminators)
	sort.Strings(expected)
	if !reflect.DeepEqual(discriminators, expected) {
		t.Fatalf("%s discriminator drift schema=%v model=%v", documentName, discriminators, expected)
	}
}

func stringValues[T ~string](values []T) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, string(value))
	}
	return result
}

func assertPreservedJSON(t *testing.T, expected json.RawMessage, value any) {
	t.Helper()
	actual, err := json.Marshal(value)
	if err != nil || !jsonSemanticallyEqual(expected, actual) {
		t.Fatalf("unknown JSON was not preserved: %s -> %s err=%v", expected, actual, err)
	}
}

func jsonSemanticallyEqual(left, right []byte) bool {
	var leftValue, rightValue any
	leftDecoder := json.NewDecoder(bytes.NewReader(left))
	leftDecoder.UseNumber()
	rightDecoder := json.NewDecoder(bytes.NewReader(right))
	rightDecoder.UseNumber()
	return leftDecoder.Decode(&leftValue) == nil && rightDecoder.Decode(&rightValue) == nil &&
		reflect.DeepEqual(leftValue, rightValue)
}
