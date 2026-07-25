package httptransport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/audit"
	"actweave/backend/internal/authn"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/identity"
	"actweave/backend/internal/storedobject"
	"actweave/backend/internal/workspace"

	"github.com/google/uuid"
)

func TestV1AuditRoleCroppingAndExport(t *testing.T) {
	fixture := newAuditAPIFixture(t)
	ownerList := fixture.request(http.MethodGet, fixture.base+"/audit-events?traceId=trace-audit-detail&result=SUCCESS", nil, fixture.ownerToken)
	if ownerList.Code != http.StatusOK || !strings.Contains(ownerList.Body.String(), fixture.eventID) ||
		!strings.Contains(ownerList.Body.String(), `"sourceIp":"203.0.113.9"`) ||
		!strings.Contains(ownerList.Body.String(), `"userAgent":"Audit fixture"`) {
		t.Fatalf("owner list status=%d body=%s", ownerList.Code, ownerList.Body.String())
	}
	viewerList := fixture.request(http.MethodGet, fixture.base+"/audit-events?traceId=trace-audit-detail", nil, fixture.viewerToken)
	if viewerList.Code != http.StatusOK || !strings.Contains(viewerList.Body.String(), fixture.eventID) ||
		strings.Contains(viewerList.Body.String(), "sourceIp") || strings.Contains(viewerList.Body.String(), "userAgent") {
		t.Fatalf("viewer list status=%d body=%s", viewerList.Code, viewerList.Body.String())
	}
	ownerDetail := fixture.request(http.MethodGet, fixture.base+"/audit-events/"+fixture.eventID, nil, fixture.ownerToken)
	if ownerDetail.Code != http.StatusOK || !strings.Contains(ownerDetail.Body.String(), `"payload":{"detail":"owner only"}`) ||
		!strings.Contains(ownerDetail.Body.String(), `"sourceIp":"203.0.113.9"`) {
		t.Fatalf("owner detail status=%d body=%s", ownerDetail.Code, ownerDetail.Body.String())
	}
	viewerDetail := fixture.request(http.MethodGet, fixture.base+"/audit-events/"+fixture.eventID, nil, fixture.viewerToken)
	if viewerDetail.Code != http.StatusOK || strings.Contains(viewerDetail.Body.String(), "owner only") ||
		strings.Contains(viewerDetail.Body.String(), "sourceIp") || strings.Contains(viewerDetail.Body.String(), "userAgent") {
		t.Fatalf("viewer detail status=%d body=%s", viewerDetail.Code, viewerDetail.Body.String())
	}
	invalidFilter := fixture.request(http.MethodGet, fixture.base+"/audit-events?result=UNKNOWN", nil, fixture.ownerToken)
	assertErrorResponse(t, invalidFilter, http.StatusUnprocessableEntity, "VALIDATION_ERROR")

	deniedExport := fixture.request(http.MethodPost, fixture.base+"/audit-exports", map[string]any{
		"traceId": "trace-audit-detail", "expiresInSeconds": 3600,
	}, fixture.viewerToken)
	assertErrorResponse(t, deniedExport, http.StatusForbidden, "FORBIDDEN")
	created := fixture.request(http.MethodPost, fixture.base+"/audit-exports", map[string]any{
		"traceId": "trace-audit-detail", "results": []string{"SUCCESS"}, "expiresInSeconds": 3600,
	}, fixture.ownerToken)
	if created.Code != http.StatusAccepted || !strings.Contains(created.Body.String(), `"status":"PENDING"`) {
		t.Fatalf("create export status=%d body=%s", created.Code, created.Body.String())
	}
	var export auditExportDTO
	decodeResponse(t, created.Body.Bytes(), &export)
	processed, err := fixture.exports.ProcessNext(context.Background())
	if err != nil || !processed {
		t.Fatalf("process export processed=%t err=%v", processed, err)
	}
	status := fixture.request(http.MethodGet, fixture.base+"/audit-exports/"+export.ID, nil, fixture.ownerToken)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"status":"SUCCEEDED"`) ||
		!strings.Contains(status.Body.String(), `"downloadUrl":"https://downloads.example.test/`) {
		t.Fatalf("export status=%d body=%s", status.Code, status.Body.String())
	}
	if ttl := fixture.objects.downloadTTL(); ttl <= 0 || ttl > 5*time.Minute {
		t.Fatalf("download URL ttl=%s", ttl)
	}
	viewerStatus := fixture.request(http.MethodGet, fixture.base+"/audit-exports/"+export.ID, nil, fixture.viewerToken)
	assertErrorResponse(t, viewerStatus, http.StatusForbidden, "FORBIDDEN")
}

type auditAPIObjects struct {
	mu         sync.Mutex
	repository *storedobject.Repository
	bodies     map[string][]byte
	lastTTL    time.Duration
}

func (store *auditAPIObjects) Put(ctx context.Context, input storedobject.PutInput) (storedobject.StoredObject, error) {
	body, err := io.ReadAll(input.Reader)
	if err != nil {
		return storedobject.StoredObject{}, err
	}
	created, err := store.repository.Create(ctx, storedobject.CreateInput{ID: input.ID,
		WorkspaceID: input.WorkspaceID, Bucket: "audit-api", ObjectKey: "objects/" + input.ID,
		Kind: input.Kind, ContentType: input.ContentType, SizeBytes: input.SizeBytes,
		SHA256: input.SHA256, EncryptionKeyID: input.EncryptionKeyID,
		Classification: input.Classification, RetentionMode: input.RetentionMode,
		RetentionUntil: input.RetentionUntil, CreatedByType: input.CreatedByType, CreatedByID: input.CreatedByID})
	if err != nil {
		return storedobject.StoredObject{}, err
	}
	store.mu.Lock()
	store.bodies[input.ID] = append([]byte(nil), body...)
	store.mu.Unlock()
	return created, nil
}

func (store *auditAPIObjects) Open(ctx context.Context, request storedobject.ReadRequest) (storedobject.OpenedObject, error) {
	metadata, err := store.repository.Get(ctx, request.WorkspaceID, request.ObjectID)
	if err != nil {
		return storedobject.OpenedObject{}, err
	}
	store.mu.Lock()
	body, exists := store.bodies[request.ObjectID]
	store.mu.Unlock()
	if !exists {
		return storedobject.OpenedObject{}, storedobject.ErrNotFound
	}
	return storedobject.OpenedObject{Metadata: metadata, Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func (store *auditAPIObjects) PresignDownload(_ context.Context, request storedobject.ReadRequest, ttl time.Duration) (*url.URL, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.bodies[request.ObjectID]; !exists {
		return nil, storedobject.ErrNotFound
	}
	store.lastTTL = ttl
	return url.Parse("https://downloads.example.test/" + request.ObjectID)
}

func (store *auditAPIObjects) downloadTTL() time.Duration {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.lastTTL
}

type auditAPIFixture struct {
	router      http.Handler
	base        string
	ownerToken  string
	viewerToken string
	eventID     string
	exports     *audit.ExportService
	objects     *auditAPIObjects
}

func newAuditAPIFixture(t *testing.T) *auditAPIFixture {
	t.Helper()
	authFixture := newV1AuthFixture(t)
	ownerLogin := authFixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": v1AdminName, "password": v1AdminPass,
	}, "", nil)
	ownerTokens := decodeTokenResponse(t, ownerLogin)
	viewerID := uuid.NewString()
	if _, err := authFixture.service.CreateUser(context.Background(), authn.CreateUserRequest{
		ID: viewerID, Username: "v1.audit.viewer", DisplayName: "Audit Viewer",
		Password: "Audit-viewer-password-1", Status: identity.StatusActive,
		PlatformRole: identity.PlatformRoleUser, Locale: "zh-CN", Timezone: "Asia/Singapore",
	}); err != nil {
		t.Fatal(err)
	}
	viewerLogin := authFixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "v1.audit.viewer", "password": "Audit-viewer-password-1",
	}, "", nil)
	viewerTokens := decodeTokenResponse(t, viewerLogin)
	ctx := context.Background()
	workspaceID := uuid.NewString()
	workspaces, err := workspace.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = workspaces.Create(ctx, workspace.NewWorkspace{ID: workspaceID,
		Slug: "audit-api-" + workspaceID[:8], DisplayName: "Audit API", Mode: workspace.ModeProduction,
		OwnerUserID: v1AdminUserID, CreatedBy: v1AdminUserID}); err != nil {
		t.Fatal(err)
	}
	if _, err = workspaces.AddMember(ctx, workspace.NewMember{WorkspaceID: workspaceID,
		UserID: viewerID, Role: workspace.RoleViewer, InvitedBy: v1AdminUserID}); err != nil {
		t.Fatal(err)
	}
	authorizer, err := authz.NewService(workspaces)
	if err != nil {
		t.Fatal(err)
	}
	objectRepository, err := storedobject.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	objects := &auditAPIObjects{repository: objectRepository, bodies: map[string][]byte{}}
	payload := []byte(`{"detail":"owner only"}`)
	digest := sha256.Sum256(payload)
	payloadID := uuid.NewString()
	if _, err = objects.Put(ctx, storedobject.PutInput{ID: payloadID, WorkspaceID: workspaceID,
		Kind: storedobject.KindAuditEventPayload, ContentType: "application/json",
		SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(digest[:]),
		EncryptionKeyID: "audit-fixture-key", Classification: storedobject.ClassificationSensitive,
		RetentionMode: storedobject.RetentionPermanent, CreatedByType: storedobject.CreatorUser,
		CreatedByID: v1AdminUserID, Reader: bytes.NewReader(payload)}); err != nil {
		t.Fatal(err)
	}
	events, err := audit.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	eventID := uuid.NewString()
	if _, err = events.Insert(ctx, audit.Event{ID: eventID, OccurredAt: time.Now().UTC(),
		WorkspaceID: workspaceID, ActorType: "USER", ActorID: v1AdminUserID,
		ActorDisplay: "Audit Owner", Action: "agent.changed", ResourceType: "AGENT",
		ResourceID: uuid.NewString(), Result: "SUCCESS", RequestID: "request-audit-detail",
		TraceID: "trace-audit-detail", SourceIP: netip.MustParseAddr("203.0.113.9"),
		UserAgent: "Audit fixture", Changes: json.RawMessage(`{"name":{"from":"old","to":"new"}}`),
		Metadata: json.RawMessage(`{"summary":"safe"}`), PayloadObjectID: payloadID,
		SchemaVersion: audit.SchemaVersionV1}); err != nil {
		t.Fatal(err)
	}
	outbox, err := audit.NewOutboxRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	builder, err := audit.NewBuilder(audit.DefaultInlineDetailBytes)
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := audit.NewRecorder(events, outbox, builder)
	if err != nil {
		t.Fatal(err)
	}
	queries, err := audit.NewQueryService(authFixture.db, objects)
	if err != nil {
		t.Fatal(err)
	}
	exports, err := audit.NewExportService(authFixture.db, queries, recorder, objects)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := NewAuditRoutes(authorizer, queries, exports)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{Authenticator: authFixture.auth,
		Registrars: []V1RouteRegistrar{authFixture.authRoutes, routes}})
	if err != nil {
		t.Fatal(err)
	}
	return &auditAPIFixture{router: router, base: "/api/v1/workspaces/" + workspaceID,
		ownerToken: ownerTokens.AccessToken, viewerToken: viewerTokens.AccessToken,
		eventID: eventID, exports: exports, objects: objects}
}

func (fixture *auditAPIFixture) request(method, path string, body any, token string) *httptest.ResponseRecorder {
	payload, _ := json.Marshal(body)
	if body == nil {
		payload = nil
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Request-ID", "request-v1-audit-test")
	request.Header.Set("X-Trace-ID", "trace-v1-audit-test")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	fixture.router.ServeHTTP(response, request)
	return response
}
