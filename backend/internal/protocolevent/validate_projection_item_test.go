package protocolevent_test

import (
	"encoding/json"
	"testing"

	"actweave/backend/internal/a2ui"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

func TestValidateProjectionItem_SharesMarshalPath(t *testing.T) {
	t.Parallel()
	surface, _ := json.Marshal(map[string]any{
		"password": "field-label", "accessToken": "path",
	})
	item := protocolevent.MessageItem{
		ID: uuid.NewString(), Type: protocolevent.ItemTypeMessage,
		Status: protocolevent.ItemStatusCompleted, Role: protocolevent.MessageRoleAssistant,
		Content: []protocolevent.ContentPart{
			protocolevent.TextContentPart{Type: protocolevent.ContentPartTypeText, Text: "form"},
			protocolevent.A2UIContentPart{
				Type: protocolevent.ContentPartTypeA2UI, Version: a2ui.EnvelopeVersionV0,
				Surface: surface, CatalogID: "standard",
			},
		},
	}
	if err := protocolevent.ValidateProjectionItem(
		item, protocolevent.EventItemCompleted, protocolevent.MustDefaultPayloadValidator(),
	); err != nil {
		t.Fatalf("ValidateProjectionItem: %v", err)
	}
	data, err := protocolevent.ValidateProjectionItemData(
		item, protocolevent.EventItemCompleted, protocolevent.MustDefaultPayloadValidator(),
	)
	if err != nil || len(data) == 0 {
		t.Fatalf("ValidateProjectionItemData: %v data=%s", err, data)
	}
}
