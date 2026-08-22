package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/aap"
	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/config"
	"actweave/backend/internal/protocolevent"
)

const (
	aapRunFileIDReady      = "d41f1f2e-7b5a-7c3d-8e9f-1234567890f1"
	aapRunFileIDProcessing = "d41f1f2e-7b5a-7c3d-8e9f-1234567890f2"
	aapRunFileIDKey        = "d41f1f2e-7b5a-7c3d-8e9f-1234567890f3"
)

func TestAAPCreateRunInputFile(t *testing.T) {
	t.Run("text-only createRun still accepted", func(t *testing.T) {
		router, application, _ := newCreateRunFileRouter(t, filesGateRuntimeOn(), nil)
		base := createRunFileBase()
		response := requestAAPRun(t, router, http.MethodPost, base, map[string]any{
			"conversationId": aapRunConversationID,
			"input": []any{map[string]any{
				"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "text", "text": "hello only"}},
			}},
			"stream": false,
		}, "subject-a", aapRunFileIDKey, "application/json", "")
		if response.Code != http.StatusAccepted {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if len(application.last.Parts) != 1 || application.last.Parts[0].Type != "text" ||
			application.last.Parts[0].Text != "hello only" {
			t.Fatalf("parts=%+v", application.last.Parts)
		}
	})

	t.Run("document-only RuntimeMultimodal false is accepted", func(t *testing.T) {
		gate := filesGateRuntimeOn()
		gate.RuntimeMultimodal = false
		router, application, lookup := newCreateRunFileRouter(t, gate, &aapCreateRunFileLookup{
			files: map[string]aapfile.File{
				aapRunFileIDReady: readyCreateRunFile(aapRunFileIDReady),
			},
		})
		response := requestAAPRun(t, router, http.MethodPost, createRunFileBase(), map[string]any{
			"conversationId": aapRunConversationID,
			"input": []any{map[string]any{
				"type": "message", "role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "summarize"},
					map[string]any{"type": "input_file", "fileId": aapRunFileIDReady},
				},
			}},
			"stream": false,
		}, "subject-a", aapRunFileIDKey, "application/json", "")
		if response.Code != http.StatusAccepted {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if application.sideEffects != 1 {
			t.Fatalf("run must be created: effects=%d", application.sideEffects)
		}
		if lookup.promoteCalls != 1 {
			t.Fatalf("expected retention promote once, got %d", lookup.promoteCalls)
		}
	})

	t.Run("image RuntimeMultimodal false returns 422 FILE_RUNTIME_UNAVAILABLE without create", func(t *testing.T) {
		gate := filesGateRuntimeOn()
		gate.RuntimeMultimodal = false
		router, application, _ := newCreateRunFileRouter(t, gate, &aapCreateRunFileLookup{
			files: map[string]aapfile.File{
				aapRunFileIDReady: readyCreateRunImage(aapRunFileIDReady),
			},
		})
		response := requestAAPRun(t, router, http.MethodPost, createRunFileBase(), map[string]any{
			"conversationId": aapRunConversationID,
			"input": []any{map[string]any{
				"type": "message", "role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "what is this"},
					map[string]any{"type": "input_file", "fileId": aapRunFileIDReady, "mediaType": "image/png"},
				},
			}},
			"stream": false,
		}, "subject-a", aapRunFileIDKey, "application/json", "")
		if response.Code != http.StatusUnprocessableEntity ||
			!strings.Contains(response.Body.String(), "FILE_RUNTIME_UNAVAILABLE") {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if application.sideEffects != 0 {
			t.Fatalf("run must not be created: effects=%d", application.sideEffects)
		}
	})

	t.Run("mixed image+pdf RuntimeMultimodal false returns 422 without create", func(t *testing.T) {
		gate := filesGateRuntimeOn()
		gate.RuntimeMultimodal = false
		pdfID := aapRunFileIDReady
		imgID := aapRunFileIDProcessing
		router, application, _ := newCreateRunFileRouter(t, gate, &aapCreateRunFileLookup{
			files: map[string]aapfile.File{
				pdfID: readyCreateRunFile(pdfID),
				imgID: readyCreateRunImage(imgID),
			},
		})
		response := requestAAPRun(t, router, http.MethodPost, createRunFileBase(), map[string]any{
			"conversationId": aapRunConversationID,
			"input": []any{map[string]any{
				"type": "message", "role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "both"},
					map[string]any{"type": "input_file", "fileId": pdfID},
					map[string]any{"type": "input_file", "fileId": imgID, "mediaType": "image/png"},
				},
			}},
			"stream": false,
		}, "subject-a", aapRunFileIDKey, "application/json", "")
		if response.Code != http.StatusUnprocessableEntity ||
			!strings.Contains(response.Body.String(), "FILE_RUNTIME_UNAVAILABLE") {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if application.sideEffects != 0 {
			t.Fatalf("run must not be created: effects=%d", application.sideEffects)
		}
	})

	t.Run("READY file accepted with parts and no download URL in DTO path", func(t *testing.T) {
		lookup := &aapCreateRunFileLookup{
			files: map[string]aapfile.File{
				aapRunFileIDReady: readyCreateRunFile(aapRunFileIDReady),
			},
		}
		router, application, _ := newCreateRunFileRouter(t, filesGateRuntimeOn(), lookup)
		response := requestAAPRun(t, router, http.MethodPost, createRunFileBase(), map[string]any{
			"conversationId": aapRunConversationID,
			"input": []any{map[string]any{
				"type": "message", "role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "summarize invoice"},
					map[string]any{"type": "input_file", "fileId": aapRunFileIDReady},
				},
			}},
			"stream": false,
		}, "subject-a", aapRunFileIDKey, "application/json", "")
		if response.Code != http.StatusAccepted {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if application.sideEffects != 1 || len(application.last.Parts) != 2 {
			t.Fatalf("last=%+v effects=%d", application.last, application.sideEffects)
		}
		if application.last.Parts[1].Type != "input_file" ||
			application.last.Parts[1].FileID != aapRunFileIDReady ||
			application.last.Parts[1].MediaType != "application/pdf" {
			t.Fatalf("file part=%+v", application.last.Parts[1])
		}
		body := response.Body.String()
		for _, forbidden := range []string{"downloadUrl", "presigned", "signedUrl", "X-Amz-Signature"} {
			if strings.Contains(strings.ToLower(body), strings.ToLower(forbidden)) {
				t.Fatalf("response leaked %q: %s", forbidden, body)
			}
		}
		if lookup.promoteCalls != 1 {
			t.Fatalf("expected retention promote once, got %d", lookup.promoteCalls)
		}
	})

	t.Run("non-READY PROCESSING returns FILE_NOT_READY retryable", func(t *testing.T) {
		router, application, _ := newCreateRunFileRouter(t, filesGateRuntimeOn(), &aapCreateRunFileLookup{
			files: map[string]aapfile.File{
				aapRunFileIDProcessing: {
					ID: aapRunFileIDProcessing, WorkspaceID: aapRunWorkspaceID,
					AgentID: aapRunAgentID, Status: aapfile.StatusProcessing,
					DeclaredMediaType: "image/png",
				},
			},
		})
		response := requestAAPRun(t, router, http.MethodPost, createRunFileBase(), map[string]any{
			"conversationId": aapRunConversationID,
			"input": []any{map[string]any{
				"type": "message", "role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "wait"},
					map[string]any{"type": "input_file", "fileId": aapRunFileIDProcessing},
				},
			}},
			"stream": false,
		}, "subject-a", aapRunFileIDKey, "application/json", "")
		if response.Code != http.StatusUnprocessableEntity ||
			!strings.Contains(response.Body.String(), "FILE_NOT_READY") {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var envelope ErrorResponse
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if !envelope.Error.Retryable {
			t.Fatalf("FILE_NOT_READY should be retryable: %+v", envelope.Error)
		}
		if application.sideEffects != 0 {
			t.Fatalf("must not create run: effects=%d", application.sideEffects)
		}
	})

	t.Run("unknown content type still rejected", func(t *testing.T) {
		router, _, _ := newCreateRunFileRouter(t, filesGateRuntimeOn(), nil)
		response := requestAAPRun(t, router, http.MethodPost, createRunFileBase(), map[string]any{
			"conversationId": aapRunConversationID,
			"input": []any{map[string]any{
				"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "image", "url": "https://x"}},
			}},
			"stream": false,
		}, "subject-a", aapRunFileIDKey, "application/json", "")
		if response.Code != http.StatusUnprocessableEntity ||
			!strings.Contains(response.Body.String(), "UNSUPPORTED_CONTENT_TYPE") {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})

	// KD-7 / PR-3: profile may advertise a2ui for assistant outbound, but createRun
	// inbound a2ui remains rejected as an unknown content type (no full a2ui part support yet).
	t.Run("inbound a2ui content part is rejected", func(t *testing.T) {
		router, _, _ := newCreateRunFileRouter(t, filesGateRuntimeOn(), nil)
		response := requestAAPRun(t, router, http.MethodPost, createRunFileBase(), map[string]any{
			"conversationId": aapRunConversationID,
			"input": []any{map[string]any{
				"type": "message", "role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "with ui"},
					map[string]any{
						"type":    "a2ui",
						"surface": map[string]any{"components": []any{}},
					},
				},
			}},
			"stream": false,
		}, "subject-a", aapRunFileIDKey, "application/json", "")
		if response.Code != http.StatusUnprocessableEntity ||
			!strings.Contains(response.Body.String(), "UNSUPPORTED_CONTENT_TYPE") {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
}

// Unit-level guarantee: validateCreateRunRequest rejects type=a2ui without HTTP stack.
func TestValidateCreateRunRequestRejectsA2UI(t *testing.T) {
	err := validateCreateRunRequest(AAPCreateRunRequest{
		ConversationID: aapRunConversationID,
		Input: []AAPRunInputItem{{
			Type: "message", Role: "user",
			Content: []AAPRunContentPart{
				{Type: "text", Text: "hello"},
				{Type: "a2ui"},
			},
		}},
	})
	if !errors.Is(err, ErrAAPUnsupportedContentType) {
		t.Fatalf("err=%v want=%v", err, ErrAAPUnsupportedContentType)
	}
}

func TestValidateCreateRunRequestRejectsOutputFile(t *testing.T) {
	err := validateCreateRunRequest(AAPCreateRunRequest{
		ConversationID: aapRunConversationID,
		Input: []AAPRunInputItem{{
			Type: "message", Role: "user",
			Content: []AAPRunContentPart{
				{Type: "text", Text: "hello"},
				{Type: "output_file", FileID: aapRunFileIDReady},
			},
		}},
	})
	if !errors.Is(err, ErrAAPUnsupportedContentType) {
		t.Fatalf("err=%v want=%v", err, ErrAAPUnsupportedContentType)
	}
}

func TestAAPAgentProfileInputFileParts(t *testing.T) {
	t.Run("files off keeps text only", func(t *testing.T) {
		content := aapSupportedContentForFiles(false, nil)
		if len(content) != 1 || content[0].Type != "message" ||
			len(content[0].Parts) != 1 || content[0].Parts[0] != "text" {
			t.Fatalf("content=%+v", content)
		}
	})

	t.Run("files on adds input_file and constraints", func(t *testing.T) {
		gate := filesGateRuntimeOn()
		content := aapSupportedContentForFiles(true, gate)
		if len(content) != 2 || content[0].Type != "message" ||
			len(content[0].Parts) != 2 || content[0].Parts[1] != "input_file" ||
			content[1].Type != "input_file_constraints" || content[1].MaxBytes <= 0 ||
			len(content[1].MediaTypes) == 0 {
			t.Fatalf("content=%+v", content)
		}
	})
}

func TestMessageContentV1EncodeAndParse(t *testing.T) {
	parts := []aap.RunContentPart{
		{Type: "text", Text: "请概括这份发票"},
		{Type: "input_file", FileID: aapRunFileIDReady, MediaType: "application/pdf"},
	}
	encoded, err := aap.EncodeMessageContentV1(parts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, aap.MessageContentSchemaVersion) ||
		strings.Contains(strings.ToLower(encoded), "downloadurl") ||
		strings.Contains(strings.ToLower(encoded), "presign") {
		t.Fatalf("encoded leaked url or lost schema: %s", encoded)
	}
	// Protocol projection via chat parser.
	projected, err := parseMessageContentForTest(encoded)
	if err != nil || len(projected) != 2 {
		t.Fatalf("projected=%+v err=%v", projected, err)
	}
	filePart, ok := projected[1].(protocolevent.InputFileContentPart)
	if !ok || filePart.FileID != aapRunFileIDReady || filePart.MediaType != "application/pdf" {
		t.Fatalf("file part=%T/%+v", projected[1], projected[1])
	}
	// Legacy plain text still maps to single text part.
	legacy, err := parseMessageContentForTest("plain historical text")
	if err != nil || len(legacy) != 1 {
		t.Fatalf("legacy=%+v err=%v", legacy, err)
	}
	text, ok := legacy[0].(protocolevent.TextContentPart)
	if !ok || text.Text != "plain historical text" {
		t.Fatalf("legacy text=%+v", legacy[0])
	}
}

func TestInputFileContentPartRoundTripSensitive(t *testing.T) {
	item := protocolevent.MessageItem{
		ID: aapRunItemID, Type: protocolevent.ItemTypeMessage,
		Status: protocolevent.ItemStatusCompleted, Role: protocolevent.MessageRoleUser,
		Content: []protocolevent.ContentPart{
			protocolevent.TextContentPart{Type: protocolevent.ContentPartTypeText, Text: "hi"},
			protocolevent.InputFileContentPart{
				Type: protocolevent.ContentPartTypeInputFile, FileID: aapRunFileIDReady,
				MediaType: "image/png",
			},
		},
	}
	if err := protocolevent.ValidateItem(item); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if err := protocolevent.ScanPublicJSON(raw); err != nil {
		t.Fatalf("sensitive scan: %v body=%s", err, raw)
	}
	for _, forbidden := range []string{"downloadUrl", "presigned", "signedUrl"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("item JSON leaked %q: %s", forbidden, raw)
		}
	}
	decoded, err := protocolevent.DecodeItem(raw)
	if err != nil {
		t.Fatal(err)
	}
	message, ok := decoded.(protocolevent.MessageItem)
	if !ok || len(message.Content) != 2 {
		t.Fatalf("decoded=%T/%+v", decoded, decoded)
	}
}

func createRunFileBase() string {
	return "/api/agent-access/v1/workspaces/" + aapRunWorkspaceID +
		"/agents/" + aapRunAgentID + "/runs"
}

func filesGateRuntimeOn() *config.AgentAccessFilesConfig {
	return &config.AgentAccessFilesConfig{
		Enabled: true, AllowAllWorkspaces: true, AllowAllClients: true,
		RuntimeMultimodal: true, MaxBytes: aapfile.DefaultMaxBytes,
		AllowedMediaTypes: []string{
			"image/png", "image/jpeg", "image/webp", "image/gif", "application/pdf",
		},
	}
}

func readyCreateRunFile(id string) aapfile.File {
	objectID := "d41f1f2e-7b5a-7c3d-8e9f-1234567890f9"
	return aapfile.File{
		ID: id, WorkspaceID: aapRunWorkspaceID, AgentID: aapRunAgentID,
		Status: aapfile.StatusReady, DeclaredMediaType: "application/pdf",
		StoredObjectID: &objectID, ProcessingVersion: 2,
	}
}

func readyCreateRunImage(id string) aapfile.File {
	objectID := "d41f1f2e-7b5a-7c3d-8e9f-1234567890fa"
	return aapfile.File{
		ID: id, WorkspaceID: aapRunWorkspaceID, AgentID: aapRunAgentID,
		Status: aapfile.StatusReady, DeclaredMediaType: "image/png",
		StoredObjectID: &objectID, ProcessingVersion: 2,
	}
}

func newCreateRunFileRouter(
	t *testing.T,
	gate *config.AgentAccessFilesConfig,
	lookup *aapCreateRunFileLookup,
) (http.Handler, *aapRunRouteApplication, *aapCreateRunFileLookup) {
	t.Helper()
	reader := &aapRunRouteReader{}
	application := &aapRunRouteApplication{reader: reader}
	conversations := &aapRunRouteConversations{}
	authorizer := &aapCreateRunFileAuthorizer{}
	items := &aapRunRouteItems{}
	attacher, err := NewAAPEventCatchUp(reader)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := NewAAPRunRoutes(
		authorizer, conversations, application, reader, items, attacher,
	)
	if err != nil {
		t.Fatal(err)
	}
	if lookup == nil {
		lookup = &aapCreateRunFileLookup{files: map[string]aapfile.File{}}
	}
	if gate != nil {
		if err := routes.ConfigureFiles(gate, lookup); err != nil {
			t.Fatal(err)
		}
	}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: aapRunRouteAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{routes},
	})
	if err != nil {
		t.Fatal(err)
	}
	return router, application, lookup
}

type aapCreateRunFileLookup struct {
	files        map[string]aapfile.File
	promoteCalls int
}

func (lookup *aapCreateRunFileLookup) GetFile(
	_ context.Context,
	workspaceID, fileID string,
) (aapfile.File, error) {
	file, ok := lookup.files[fileID]
	if !ok || file.WorkspaceID != workspaceID {
		return aapfile.File{}, aapfile.ErrNotFound
	}
	return file, nil
}

func (lookup *aapCreateRunFileLookup) PromoteRetentionOnReference(
	_ context.Context,
	_, _ string,
) error {
	lookup.promoteCalls++
	return nil
}

type aapCreateRunFileAuthorizer struct{}

func (aapCreateRunFileAuthorizer) Authorize(
	_ context.Context,
	request agentaccessauth.AAPAuthorizationRequest,
) (agentaccessauth.AAPAuthorizationDecision, error) {
	now := time.Now().UTC()
	snapshot := agentaccessauth.AAPAuthorizationSnapshot{
		SpecVersion: "aap.authorization.v1", WorkspaceID: request.Principal.WorkspaceID,
		AgentID: request.Principal.AgentID, ClientID: aapRunClientID,
		AuthorizedParty:    request.Principal.AuthorizedParty,
		ServicePrincipalID: aapRunServiceID, SubjectID: request.Principal.PrincipalID,
		GrantID: aapRunGrantID, Action: request.Action, RequiredScope: "run:create",
		TokenScopes: []string{"run:create", "file:read"}, GrantScopes: []string{"run:create", "file:read"},
		AgentPolicyScopes:    []string{"run:create", "file:read"},
		EffectiveScopes:      []string{"run:create", "file:read"},
		TokenSecurityVersion: 1, ResolvedSecurityVersion: 1,
		WorkspaceVersion: 1, ClientVersion: 1, GrantVersion: 1, AgentPolicyVersion: 1,
		TokenID: aapRunTokenID, ResourceType: request.Resource.Type, ResourceID: request.Resource.ID,
		OwnershipMode: "SUBJECT_OWNED", OwnershipPolicyVersion: 1, AuthorizedAt: now,
	}
	if request.Action == agentaccessauth.ActionFileRead {
		snapshot.RequiredScope = "file:read"
	}
	return agentaccessauth.AAPAuthorizationDecision{
		EffectiveScopes: snapshot.EffectiveScopes, Snapshot: snapshot,
	}, nil
}

// parseMessageContentForTest reuses chat package projection via a thin re-export pattern
// implemented in the test file to avoid exporting test helpers from chat.
func parseMessageContentForTest(content string) ([]protocolevent.ContentPart, error) {
	// Local duplicate of chat.ParseMessageContentParts to keep transport tests free of
	// chat fixtures while still validating the durable schema contract.
	var envelope struct {
		SchemaVersion string            `json:"schemaVersion"`
		Parts         []json.RawMessage `json:"parts"`
	}
	if err := json.Unmarshal([]byte(content), &envelope); err != nil ||
		envelope.SchemaVersion != aap.MessageContentSchemaVersion || len(envelope.Parts) == 0 {
		return []protocolevent.ContentPart{
			protocolevent.TextContentPart{Type: protocolevent.ContentPartTypeText, Text: content},
		}, nil
	}
	parts := make([]protocolevent.ContentPart, 0, len(envelope.Parts))
	for _, raw := range envelope.Parts {
		part, err := protocolevent.DecodeContentPart(raw)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, nil
}
