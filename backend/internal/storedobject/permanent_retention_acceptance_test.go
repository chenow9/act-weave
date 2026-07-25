package storedobject

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/workspace"
)

const permanentAcceptanceViewerID = "e18f1f2e-7b5a-7c3d-8e9f-123456789099"

func TestPermanentRetentionAccessAcceptance(t *testing.T) {
	repository, db := newMinIOStoreRepository(t)
	backend := newFakeBlobBackend()
	policy := &acceptanceWorkspacePolicy{ownerID: minioStoreOwnerID, viewerID: permanentAcceptanceViewerID}
	authorizer, _ := NewWorkspaceReadAuthorizer(policy)
	objects, _ := newObjectStore(backend, repository, authorizer, 1<<20)
	cipher, _ := NewLocalChunkCipher("permanent-acceptance-key-v1", objectEncryptionTestKey)
	secure, _ := NewSecureStore(objects, cipher)

	types := []struct {
		id             string
		kind           string
		classification string
		content        []byte
	}{
		{"e18f1f2e-7b5a-7c3d-8e9f-123456789010", KindPromptRunInput, ClassificationSensitive, []byte("private prompt input")},
		{"e18f1f2e-7b5a-7c3d-8e9f-123456789011", KindChatMessage, ClassificationSensitive, []byte("private chat message")},
		{"e18f1f2e-7b5a-7c3d-8e9f-123456789012", KindModelTurn, ClassificationSensitive, []byte(`{"role":"assistant","content":"private turn"}`)},
		{"e18f1f2e-7b5a-7c3d-8e9f-123456789013", KindToolInvocationPayload, ClassificationRestricted, []byte(`{"request":{"orderId":"A-10293"}}`)},
	}
	created := make(map[string]StoredObject, len(types))
	for _, item := range types {
		input := securePutInput(item.id, item.kind, item.content)
		input.Classification = item.classification
		metadata, err := secure.Put(context.Background(), input)
		if err != nil {
			t.Fatalf("put %s permanent content: %v", item.kind, err)
		}
		created[item.kind] = metadata
		opened, err := secure.Open(context.Background(), ReadRequest{
			WorkspaceID: minioStoreWorkspaceID, ObjectID: item.id,
			ActorType: CreatorUser, ActorID: minioStoreOwnerID,
		})
		if err != nil {
			t.Fatalf("owner open %s: %v", item.kind, err)
		}
		read, readErr := io.ReadAll(opened.Body)
		_ = opened.Body.Close()
		if readErr != nil || !bytes.Equal(read, item.content) {
			t.Fatalf("read %s content=%q err=%v", item.kind, read, readErr)
		}
	}

	if _, err := secure.Open(context.Background(), ReadRequest{
		WorkspaceID: minioStoreWorkspaceID, ObjectID: types[3].id,
		ActorType: CreatorUser, ActorID: permanentAcceptanceViewerID,
	}); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("viewer restricted read error = %v", err)
	}
	if _, err := secure.Open(context.Background(), ReadRequest{
		WorkspaceID: "e18f1f2e-7b5a-7c3d-8e9f-123456789098", ObjectID: types[0].id,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace read error = %v", err)
	}
	if _, err := secure.PresignDownload(context.Background(), ReadRequest{
		WorkspaceID: minioStoreWorkspaceID, ObjectID: types[0].id,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	}, time.Minute); !errors.Is(err, ErrInvalid) {
		t.Fatalf("encrypted permanent URL error = %v", err)
	}

	chat := created[KindChatMessage]
	backend.tamperObject(chat.Bucket, chat.ObjectKey)
	opened, err := secure.Open(context.Background(), ReadRequest{
		WorkspaceID: minioStoreWorkspaceID, ObjectID: chat.ID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	})
	if err == nil {
		_, err = io.ReadAll(opened.Body)
		_ = opened.Body.Close()
	}
	if !errors.Is(err, ErrIntegrity) && !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered permanent object error = %v", err)
	}

	openAPIID := "e18f1f2e-7b5a-7c3d-8e9f-123456789014"
	openAPI := minIOPutInput(openAPIID, []byte(`{"openapi":"3.1.0"}`))
	if _, err := objects.Put(context.Background(), openAPI); err != nil {
		t.Fatal(err)
	}
	signed, err := objects.PresignDownload(context.Background(), ReadRequest{
		WorkspaceID: minioStoreWorkspaceID, ObjectID: openAPIID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	}, 5*time.Minute)
	if err != nil || signed.Query().Get("expires") != "300" {
		t.Fatalf("bounded signed URL: %v err=%v", signed, err)
	}
	if _, err := objects.PresignDownload(context.Background(), ReadRequest{
		WorkspaceID: minioStoreWorkspaceID, ObjectID: openAPIID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	}, 16*time.Minute); !errors.Is(err, ErrInvalid) {
		t.Fatalf("overlong signed URL error = %v", err)
	}

	if _, err := db.Exec(`DELETE FROM stored_objects WHERE id=$1`, types[0].id); err == nil {
		t.Fatal("permanent object metadata deletion was allowed")
	}
	if _, ok := reflect.TypeOf(secure).MethodByName("Delete"); ok {
		t.Fatal("secure object store exposes a user delete method")
	}
	for _, constraint := range []string{
		"prompt_runs_input_object_fk", "chat_messages_content_object_fk",
		"agent_run_steps_raw_object_fk", "tool_tests_raw_object_fk",
		"tool_invocations_raw_object_fk",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname=$1)`, constraint).Scan(&exists); err != nil || !exists {
			t.Fatalf("business object reference %s: exists=%v err=%v", constraint, exists, err)
		}
	}
	if policy.manageCalls == 0 || policy.viewCalls < len(types)-1 ||
		strings.TrimSpace(policy.lastWorkspaceID) != minioStoreWorkspaceID {
		t.Fatalf("classification access policy not applied: %+v", policy)
	}
}

type acceptanceWorkspacePolicy struct {
	ownerID         string
	viewerID        string
	viewCalls       int
	manageCalls     int
	lastWorkspaceID string
}

func (policy *acceptanceWorkspacePolicy) AuthorizeWorkspace(
	_ context.Context,
	userID, workspaceID string,
	action authz.Action,
) (authz.WorkspaceContext, error) {
	policy.lastWorkspaceID = workspaceID
	if action == authz.ActionManage {
		policy.manageCalls++
		if userID != policy.ownerID {
			return authz.WorkspaceContext{}, authz.ErrDenied
		}
		return authz.WorkspaceContext{WorkspaceID: workspaceID, UserID: userID,
			Role: workspace.RoleOwner, Action: action}, nil
	}
	policy.viewCalls++
	if userID != policy.ownerID && userID != policy.viewerID {
		return authz.WorkspaceContext{}, authz.ErrDenied
	}
	return authz.WorkspaceContext{WorkspaceID: workspaceID, UserID: userID,
		Role: workspace.RoleViewer, Action: action}, nil
}
