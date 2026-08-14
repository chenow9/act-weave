package protocolevent_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

const outputFileID = "019f0000-0000-7000-8000-00000000f001"

func TestOutputFileContentPartDecodeAndValidate(t *testing.T) {
	t.Parallel()

	t.Run("roundTripAllowlisted", func(t *testing.T) {
		raw := json.RawMessage(`{
			"type":"output_file",
			"fileId":"` + outputFileID + `",
			"mediaType":"text/csv",
			"filename":"invoice-2026-08.csv",
			"sizeBytes":4096
		}`)
		part, err := protocolevent.DecodeContentPart(raw)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		file, ok := part.(protocolevent.OutputFileContentPart)
		if !ok || file.ContentKind() != protocolevent.ContentPartTypeOutputFile {
			t.Fatalf("decoded type=%T kind=%v", part, part.ContentKind())
		}
		if file.FileID != outputFileID || file.MediaType != "text/csv" ||
			file.Filename != "invoice-2026-08.csv" || file.SizeBytes != 4096 {
			t.Fatalf("decoded=%+v", file)
		}
		if err := protocolevent.ValidateItem(outputFileMessageItem(file)); err != nil {
			t.Fatalf("ValidateItem: %v", err)
		}
	})

	t.Run("stripsURLKeysBeforeProjection", func(t *testing.T) {
		raw := json.RawMessage(`{
			"type":"output_file",
			"fileId":"` + outputFileID + `",
			"mediaType":"text/plain",
			"filename":"note.txt",
			"sizeBytes":12,
			"url":"https://example.com/x",
			"downloadUrl":"https://example.com/d"
		}`)
		part, err := protocolevent.DecodeContentPart(raw)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		encoded, err := json.Marshal(part)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "url") || strings.Contains(string(encoded), "downloadUrl") ||
			strings.Contains(string(encoded), "example.com") {
			t.Fatalf("allowlist rebuild leaked URL keys: %s", encoded)
		}
		if err := protocolevent.ValidateProjectionItem(
			outputFileMessageItem(part),
			protocolevent.EventItemCompleted,
			protocolevent.MustDefaultPayloadValidator(),
		); err != nil {
			t.Fatalf("ValidateProjectionItem after strip: %v", err)
		}
	})

	t.Run("scannerDoesNotRejectURLFieldNames", func(t *testing.T) {
		// Proves strip-on-write is load-bearing: field names url/downloadUrl are not sensitive.
		itemID := uuid.NewString()
		data := json.RawMessage(`{
			"item":{
				"id":"` + itemID + `",
				"type":"message","status":"completed","role":"assistant",
				"content":[{
					"type":"output_file",
					"fileId":"` + outputFileID + `",
					"url":"https://example.com/x",
					"downloadUrl":"https://example.com/y"
				}]
			}
		}`)
		if err := protocolevent.MustDefaultPayloadValidator().ValidateEventData(
			protocolevent.EventItemCompleted, data,
		); err != nil {
			t.Fatalf("unstripped url/downloadUrl must not fail the scanner: %v", err)
		}
	})

	t.Run("rejectsInvalidFileIDFilenameSize", func(t *testing.T) {
		cases := []string{
			`{"type":"output_file"}`,
			`{"type":"output_file","fileId":"not-a-uuid"}`,
			`{"type":"output_file","fileId":"` + outputFileID + `","sizeBytes":0}`,
			`{"type":"output_file","fileId":"` + outputFileID + `","sizeBytes":-1}`,
			`{"type":"output_file","fileId":"` + outputFileID + `","filename":"../secret.csv"}`,
			`{"type":"output_file","fileId":"` + outputFileID + `","filename":"a/b.csv"}`,
			`{"type":"output_file","fileId":"` + outputFileID + `","filename":"a\\b.csv"}`,
			`{"type":"output_file","fileId":"` + outputFileID + `","filename":"bad\u0000name.csv"}`,
			`{"type":"output_file","fileId":"` + outputFileID + `","filename":"foo..bar.csv"}`,
		}
		for _, payload := range cases {
			_, err := protocolevent.DecodeContentPart(json.RawMessage(payload))
			if !errors.Is(err, protocolevent.ErrModelInvalid) {
				t.Fatalf("payload %s error=%v, want ErrModelInvalid", payload, err)
			}
		}
		bad := protocolevent.OutputFileContentPart{
			Type: protocolevent.ContentPartTypeOutputFile, FileID: outputFileID,
			Filename: "../escape.csv",
		}
		if err := protocolevent.ValidateItem(outputFileMessageItem(bad)); !errors.Is(err, protocolevent.ErrModelInvalid) {
			t.Fatalf("ValidateItem bad filename error=%v", err)
		}
	})
}

func TestParseContentPartTypeOutputFile(t *testing.T) {
	t.Parallel()
	if got := protocolevent.ParseContentPartType("output_file"); got != protocolevent.ContentPartTypeOutputFile {
		t.Fatalf("ParseContentPartType(output_file)=%v", got)
	}
}

func outputFileMessageItem(part protocolevent.ContentPart) protocolevent.MessageItem {
	return protocolevent.MessageItem{
		ID: uuid.NewString(), Type: protocolevent.ItemTypeMessage,
		Status: protocolevent.ItemStatusCompleted, Role: protocolevent.MessageRoleAssistant,
		Content: []protocolevent.ContentPart{
			protocolevent.TextContentPart{Type: protocolevent.ContentPartTypeText, Text: ""},
			part,
		},
	}
}
