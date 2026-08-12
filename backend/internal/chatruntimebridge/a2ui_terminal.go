package chatruntimebridge

import (
	"encoding/json"
	"log/slog"
	"strings"

	"actweave/backend/internal/a2ui"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/metrics"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/sessioncontext"
)

// materializeAssistantTerminalContent turns final model text into durable
// chat_messages content for completeRun only (design §4.4).
//
// When enableA2UI and projection are on, valid fences become aap.message-content.v1
// multi-part (text + a2ui) after the same preflight as marshalProjectionItem.
// Failures degrade to non-empty plain text; the run still succeeds.
func materializeAssistantTerminalContent(
	full string,
	contextPolicySnapshot json.RawMessage,
	messageID string,
) (content string, emit a2ui.EmitResult) {
	enable := sessioncontext.EnableA2UIFromSnapshot(contextPolicySnapshot)
	prepared := a2ui.PrepareAssistantContent(full, a2ui.PrepareOptions{
		EnableA2UI:        enable,
		ProjectionEnabled: a2ui.ProjectionEnabled(),
		SurfaceID:         a2ui.SurfaceIDFor(messageID),
	})

	if prepared.AttachedA2UI {
		if err := preflightAssistantA2UIItem(messageID, prepared.Content); err != nil {
			prepared = a2ui.DegradeToTextOnly(full, prepared)
		}
	}

	// Last-resort: RecordAssistantResult rejects empty Content.
	if strings.TrimSpace(prepared.Content) == "" && strings.TrimSpace(full) != "" {
		prepared.Content = full
		prepared.AttachedA2UI = false
		if prepared.Result == a2ui.EmitOK || prepared.Result == a2ui.EmitOKEmptyText ||
			prepared.Result == a2ui.EmitTruncated {
			prepared.Result = a2ui.EmitInvalidJSON
		}
	}

	metrics.Default().ObserveA2UIEmit(string(prepared.Result))
	observeA2UICatalogOutcome(prepared)
	return prepared.Content, prepared.Result
}

// observeA2UICatalogOutcome records the signals prompt tuning depends on: which
// catalog rule a rejected surface broke, and which charts actually shipped.
func observeA2UICatalogOutcome(prepared a2ui.PrepareResult) {
	if diagnostic := prepared.Diagnostic; diagnostic != nil {
		metrics.Default().ObserveA2UICatalogInvalid(string(diagnostic.Reason), diagnostic.Keyword)
		slog.Warn("a2ui surface rejected by catalog",
			"event", "a2ui.catalog.invalid",
			"reason", string(diagnostic.Reason),
			"pointer", diagnostic.Pointer,
			"keyword", diagnostic.Keyword,
			"component", diagnostic.Component,
			"expected", diagnostic.Expected,
		)
		return
	}
	if !prepared.AttachedA2UI || prepared.Payload == nil {
		return
	}
	for _, chartType := range a2ui.ChartTypesIn(prepared.Payload.Surface) {
		metrics.Default().ObserveA2UIChartEmitted(chartType)
	}
}

// preflightAssistantA2UIItem builds the MessageItem that CompleteProjected will
// rehydrate from durable content and validates it via the shared projection path.
func preflightAssistantA2UIItem(messageID, durable string) error {
	parts, err := chat.ParseMessageContentParts(durable)
	if err != nil {
		return err
	}
	id := strings.TrimSpace(messageID)
	if id == "" {
		// Synthetic id for preflight when stream never opened a sink (tests).
		// ValidateItem requires a UUID; use a fixed valid one only for schema scan.
		id = "00000000-0000-4000-8000-0000000000a2"
	}
	item := protocolevent.MessageItem{
		ID:      id,
		Type:    protocolevent.ItemTypeMessage,
		Status:  protocolevent.ItemStatusCompleted,
		Role:    protocolevent.MessageRoleAssistant,
		Content: parts,
	}
	return protocolevent.ValidateProjectionItem(
		item, protocolevent.EventItemCompleted, protocolevent.MustDefaultPayloadValidator(),
	)
}

// assistantModelHistoryText projects durable assistant content for model reload (KD-10).
// Omits a2ui surface; plain/legacy bodies pass through.
func assistantModelHistoryText(content string) string {
	return chat.JoinTextPartsFromDurable(content)
}
