package httptransport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/storedobject"
	"actweave/backend/internal/workspace"

	"github.com/google/uuid"
)

type chatStoreOverlay struct {
	ChatStore
	extra map[string]chat.Message
}

func (store *chatStoreOverlay) GetMessage(ctx context.Context, workspaceID, messageID string) (chat.Message, error) {
	if store != nil {
		if message, ok := store.extra[messageID]; ok && message.WorkspaceID == workspaceID {
			return message, nil
		}
	}
	return store.ChatStore.GetMessage(ctx, workspaceID, messageID)
}

func (store *chatStoreOverlay) ListMessages(ctx context.Context, workspaceID, sessionID string) ([]chat.Message, error) {
	values, err := store.ChatStore.ListMessages(ctx, workspaceID, sessionID)
	if err != nil {
		return nil, err
	}
	for _, message := range store.extra {
		if message.WorkspaceID == workspaceID && message.SessionID == sessionID {
			values = append(values, message)
		}
	}
	return values, nil
}

func (store *chatStoreOverlay) put(message chat.Message) {
	if store.extra == nil {
		store.extra = map[string]chat.Message{}
	}
	store.extra[message.ID] = message
}

type stubSessionFileLookup struct {
	files map[string]aapfile.File
	calls int
}

func (store *stubSessionFileLookup) GetFile(_ context.Context, workspaceID, fileID string) (aapfile.File, error) {
	store.calls++
	file, ok := store.files[workspaceID+"/"+fileID]
	if !ok {
		return aapfile.File{}, aapfile.ErrNotFound
	}
	return file, nil
}

func (store *stubSessionFileLookup) put(file aapfile.File) {
	if store.files == nil {
		store.files = map[string]aapfile.File{}
	}
	store.files[file.WorkspaceID+"/"+file.ID] = file
}

type stubSessionFileObjects struct {
	bodies map[string][]byte
	opens  []storedobject.ReadRequest
}

func (store *stubSessionFileObjects) Open(_ context.Context, request storedobject.ReadRequest) (storedobject.OpenedObject, error) {
	store.opens = append(store.opens, request)
	body, ok := store.bodies[request.ObjectID]
	if !ok {
		return storedobject.OpenedObject{}, storedobject.ErrNotFound
	}
	return storedobject.OpenedObject{
		Metadata: storedobject.StoredObject{
			ID: request.ObjectID, WorkspaceID: request.WorkspaceID, Kind: storedobject.KindAAPFile,
		},
		Body: io.NopCloser(bytes.NewReader(body)),
	}, nil
}

func (store *stubSessionFileObjects) put(objectID string, body []byte) {
	if store.bodies == nil {
		store.bodies = map[string][]byte{}
	}
	store.bodies[objectID] = body
}

func TestV1SessionMessageFileContentServesReferencedOutputFile(t *testing.T) {
	fixture := newChatExecutionAPIFixture(t)
	session := fixture.createOwnedSession(t, "Attachments")
	fileID := uuid.NewString()
	objectID := uuid.NewString()
	message := fixture.putAssistantFileMessage(session, fileID, "invoice-2026-08.csv", "text/csv")
	filename := "invoice-2026-08.csv"
	media := "text/csv"
	size := int64(len("date,amount\n"))
	fixture.files.put(aapfile.File{
		ID: fileID, WorkspaceID: fixture.workspaceID, AgentID: fixture.agentID,
		Status: aapfile.StatusReady, Filename: &filename, DeclaredMediaType: media,
		SizeBytes: size, StoredObjectID: &objectID,
	})
	fixture.objects.put(objectID, []byte("date,amount\n"))

	got := fixture.request(http.MethodGet, fixture.sessionFileContentPath(session.ID, message.ID, fileID),
		nil, fixture.adminToken, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("content status=%d body=%s", got.Code, got.Body.String())
	}
	if got.Body.String() != "date,amount\n" {
		t.Fatalf("body=%q", got.Body.String())
	}
	if got.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing nosniff: %v", got.Header())
	}
	if !strings.Contains(got.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("content-type=%q", got.Header().Get("Content-Type"))
	}
	if !strings.Contains(got.Header().Get("Content-Disposition"), "invoice-2026-08.csv") {
		t.Fatalf("disposition=%q", got.Header().Get("Content-Disposition"))
	}
	if len(fixture.objects.opens) != 1 {
		t.Fatalf("opens=%d", len(fixture.objects.opens))
	}
	open := fixture.objects.opens[0]
	if open.ActorType != storedobject.CreatorSystem || open.ActorID != aapFileSystemDownloadActorID ||
		open.ObjectID != objectID {
		t.Fatalf("open request=%+v", open)
	}

	detail := fixture.request(http.MethodGet, fixture.base+"/chat/sessions/"+session.ID, nil, fixture.adminToken, nil)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"fileId":"`+fileID+`"`) ||
		!strings.Contains(detail.Body.String(), `"attachments"`) ||
		!strings.Contains(detail.Body.String(), `"对账单已生成。"`) {
		t.Fatalf("session history missing attachment metadata: %s", detail.Body.String())
	}
}

func TestV1SessionMessageFileContentIDOR(t *testing.T) {
	fixture := newChatExecutionAPIFixture(t)
	session := fixture.createOwnedSession(t, "Owner")
	otherSession := fixture.createOwnedSession(t, "Other session")
	fileID := uuid.NewString()
	objectID := uuid.NewString()
	message := fixture.putAssistantFileMessage(session, fileID, "secret.csv", "text/csv")
	filename := "secret.csv"
	fixture.files.put(aapfile.File{
		ID: fileID, WorkspaceID: fixture.workspaceID, AgentID: fixture.agentID,
		Status: aapfile.StatusReady, Filename: &filename, DeclaredMediaType: "text/csv",
		SizeBytes: 12, StoredObjectID: &objectID,
	})
	fixture.objects.put(objectID, []byte("secret-bytes"))

	wrongSession := fixture.request(http.MethodGet,
		fixture.sessionFileContentPath(otherSession.ID, message.ID, fileID),
		nil, fixture.adminToken, nil)
	assertErrorResponse(t, wrongSession, http.StatusNotFound, "NOT_FOUND")

	unknownFile := fixture.request(http.MethodGet,
		fixture.sessionFileContentPath(session.ID, message.ID, uuid.NewString()),
		nil, fixture.adminToken, nil)
	assertErrorResponse(t, unknownFile, http.StatusNotFound, "NOT_FOUND")

	otherUser := fixture.request(http.MethodGet,
		fixture.sessionFileContentPath(session.ID, message.ID, fileID),
		nil, fixture.otherToken, nil)
	assertErrorResponse(t, otherUser, http.StatusNotFound, "NOT_FOUND")

	foreignWorkspaceID := uuid.NewString()
	if _, err := fixture.workspaces.Create(context.Background(), workspace.NewWorkspace{
		ID: foreignWorkspaceID, Slug: "chat-idor-" + foreignWorkspaceID[:8],
		DisplayName: "Foreign", Mode: workspace.ModeProduction,
		OwnerUserID: v1AdminUserID, CreatedBy: v1AdminUserID,
	}); err != nil {
		t.Fatal(err)
	}
	// Caller can view workspace B, but the session/message live in workspace A.
	otherWorkspace := fixture.request(http.MethodGet,
		"/api/v1/workspaces/"+foreignWorkspaceID+"/sessions/"+session.ID+"/messages/"+message.ID+"/files/"+fileID+"/content",
		nil, fixture.adminToken, nil)
	assertErrorResponse(t, otherWorkspace, http.StatusNotFound, "NOT_FOUND")

	unrelatedWorkspace := fixture.request(http.MethodGet,
		"/api/v1/workspaces/"+uuid.NewString()+"/sessions/"+session.ID+"/messages/"+message.ID+"/files/"+fileID+"/content",
		nil, fixture.adminToken, nil)
	assertErrorResponse(t, unrelatedWorkspace, http.StatusNotFound, "NOT_FOUND")

	if len(fixture.objects.opens) != 0 {
		t.Fatalf("IDOR paths opened stored object: %+v", fixture.objects.opens)
	}
}

func (fixture *chatExecutionAPIFixture) createOwnedSession(t *testing.T, title string) chatSessionDTO {
	t.Helper()
	created := fixture.request(http.MethodPost, fixture.base+"/chat/sessions", map[string]any{
		"agentId": fixture.agentID, "title": title,
	}, fixture.adminToken, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create session status=%d body=%s", created.Code, created.Body.String())
	}
	var session chatSessionDTO
	decodeResponse(t, created.Body.Bytes(), &session)
	return session
}

func (fixture *chatExecutionAPIFixture) putAssistantFileMessage(
	session chatSessionDTO,
	fileID, filename, mediaType string,
) chat.Message {
	durable := `{"schemaVersion":"aap.message-content.v1","parts":[` +
		`{"type":"text","text":"对账单已生成。"},` +
		`{"type":"output_file","fileId":"` + fileID + `","mediaType":"` + mediaType +
		`","filename":"` + filename + `","sizeBytes":12}` +
		`]}`
	digest := sha256.Sum256([]byte(durable))
	message := chat.Message{
		ID: uuid.NewString(), WorkspaceID: fixture.workspaceID, SessionID: session.ID,
		Role: "ASSISTANT", Content: durable, ContentSHA256: hex.EncodeToString(digest[:]),
		ContentLength: int64(len([]byte(durable))), Status: "EXECUTED",
		CreatedAt: time.Now().UTC(),
	}
	fixture.chats.put(message)
	return message
}

func (fixture *chatExecutionAPIFixture) sessionFileContentPath(sessionID, messageID, fileID string) string {
	return fixture.base + "/sessions/" + sessionID + "/messages/" + messageID + "/files/" + fileID + "/content"
}
