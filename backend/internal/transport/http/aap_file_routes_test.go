package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/aap"
	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/agentaccess"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/config"
	"actweave/backend/internal/storedobject"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	aapFileWorkspaceID = "f38f1f2e-7b5a-7c3d-8e9f-123456789001"
	aapFileAgentID     = "f38f1f2e-7b5a-7c3d-8e9f-123456789002"
	aapFileServiceID   = "f38f1f2e-7b5a-7c3d-8e9f-123456789003"
	aapFileClientID    = "f38f1f2e-7b5a-7c3d-8e9f-123456789006"
	aapFileGrantID     = "f38f1f2e-7b5a-7c3d-8e9f-123456789007"
	aapFileTokenID     = "f38f1f2e-7b5a-7c3d-8e9f-123456789008"
	aapFileKeyOne      = "f38f1f2e-7b5a-7c3d-8e9f-123456789009"
	aapFileKeyTwo      = "f38f1f2e-7b5a-7c3d-8e9f-12345678900a"
)

var png1x1 = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, 0x00, 0x00, 0x00,
	0x0c, 0x49, 0x44, 0x41, 0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
	0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0x05, 0xfe, 0xd4, 0xef, 0x00, 0x00,
	0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func TestAAPFileRoutes(t *testing.T) {
	filesOn := config.AgentAccessFilesConfig{
		Enabled: true, AllowAllWorkspaces: true, AllowAllClients: true,
	}
	store := newMemoryFileStore()
	staging := newMemoryStaging()
	secure := &memorySecurePutter{}
	domain, err := aapfile.NewService(store, staging, secure)
	if err != nil {
		t.Fatal(err)
	}
	app, err := aap.NewFileService(domain)
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &aapFileAuthorizer{}
	content := &memoryContentOpener{bodies: map[string][]byte{}}
	routes, err := NewAAPFileRoutes(authorizer, app, content, &filesOn)
	if err != nil {
		t.Fatal(err)
	}
	profile := aapFileProfileRegistrar{}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: aapFileTokenAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{routes, profile},
	})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/agent-access/v1/workspaces/" + aapFileWorkspaceID +
		"/agents/" + aapFileAgentID + "/files"

	var fileID string
	t.Run("create returns upload headers with Content-Length and Type", func(t *testing.T) {
		response := requestAAPFile(t, router, http.MethodPost, base, map[string]any{
			"filename": "pixel.png", "mediaType": "image/png",
			"sizeBytes": len(png1x1), "purpose": "GENERAL",
		}, "service", aapFileKeyOne)
		if response.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var body aapCreateFileResponse
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.File.ID == "" || body.File.Status != "pending_upload" || body.Upload == nil {
			t.Fatalf("response=%+v", body)
		}
		fileID = body.File.ID
		if body.Upload.Method != "PUT" || body.Upload.URL == "" {
			t.Fatalf("upload=%+v", body.Upload)
		}
		if body.Upload.Headers["Content-Type"] != "image/png" {
			t.Fatalf("Content-Type header=%v", body.Upload.Headers)
		}
		if body.Upload.Headers["Content-Length"] == "" {
			t.Fatal("Content-Length must be bound on create upload headers")
		}
	})

	t.Run("complete after staging put", func(t *testing.T) {
		if fileID == "" {
			t.Fatal("no file created")
		}
		f := store.files[fileID]
		staging.put(f.StagingBucket, *f.StagingObjectKey, png1x1)

		response := requestAAPFile(t, router, http.MethodPost, base+"/"+fileID+":complete",
			map[string]any{}, "service", aapFileKeyTwo)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var body aapCompleteFileResponse
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.File.Status != "uploaded" {
			t.Fatalf("status=%s want uploaded (async promote)", body.File.Status)
		}
		if strings.Contains(strings.ToLower(response.Body.String()), `"upload"`) {
			t.Fatalf("complete leaked upload: %s", response.Body.String())
		}
	})

	t.Run("GET file never has upload or downloadUrl", func(t *testing.T) {
		response := requestAAPFile(t, router, http.MethodGet, base+"/"+fileID, nil, "service", "")
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		raw := strings.ToLower(response.Body.String())
		for _, forbidden := range []string{
			`"upload"`, "presign", "downloadurl", "x-amz-signature", "aws4-hmac",
		} {
			if strings.Contains(raw, forbidden) {
				t.Fatalf("GET file leaked %q: %s", forbidden, response.Body.String())
			}
		}
		var body aapGetFileResponse
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.File.Links.Content == "" || strings.Contains(body.File.Links.Content, "http") {
			t.Fatalf("content link must be relative path: %q", body.File.Links.Content)
		}
	})

	t.Run("mint download token for READY file", func(t *testing.T) {
		f := store.files[fileID]
		objID := uuid.NewString()
		f.Status = aapfile.StatusReady
		f.StoredObjectID = &objID
		store.files[fileID] = f
		content.bodies[objID] = png1x1

		response := requestAAPFile(t, router, http.MethodPost, base+"/"+fileID+":download",
			nil, "service", "")
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var body aapMintDownloadResponse
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Token == "" || !strings.HasPrefix(body.URL, "/api/agent-access/v1/files/downloads/") {
			t.Fatalf("mint=%+v", body)
		}
		get := httptest.NewRequest(http.MethodGet, body.URL, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, get)
		if rec.Code != http.StatusOK {
			t.Fatalf("token download status=%d body=%s", rec.Code, rec.Body.String())
		}
		if !bytes.Equal(rec.Body.Bytes(), png1x1) {
			t.Fatalf("body mismatch len=%d", rec.Body.Len())
		}
	})

	t.Run("files gate off conceals file routes; other AAP ok", func(t *testing.T) {
		filesOff := config.AgentAccessFilesConfig{Enabled: false}
		offRoutes, err := NewAAPFileRoutes(authorizer, app, content, &filesOff)
		if err != nil {
			t.Fatal(err)
		}
		offRouter, err := NewRouter(Config{
			AgentAccessAuthenticator: aapFileTokenAuthenticator{},
			AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{offRoutes, profile},
		})
		if err != nil {
			t.Fatal(err)
		}
		fileResp := requestAAPFile(t, offRouter, http.MethodPost, base, map[string]any{
			"mediaType": "image/png", "sizeBytes": 10,
		}, "service", uuid.NewString())
		if fileResp.Code != http.StatusNotFound {
			t.Fatalf("gate off file status=%d body=%s", fileResp.Code, fileResp.Body.String())
		}
		var errBody ErrorResponse
		_ = json.Unmarshal(fileResp.Body.Bytes(), &errBody)
		if errBody.Error.Code != "FILE_FEATURE_DISABLED" {
			t.Fatalf("gate off code=%s want FILE_FEATURE_DISABLED", errBody.Error.Code)
		}
		profilePath := "/api/agent-access/v1/workspaces/" + aapFileWorkspaceID +
			"/agents/" + aapFileAgentID + "/profile"
		profReq := httptest.NewRequest(http.MethodGet, profilePath, nil)
		profReq.Header.Set("Authorization", "Bearer service")
		profRec := httptest.NewRecorder()
		offRouter.ServeHTTP(profRec, profReq)
		if profRec.Code != http.StatusOK {
			t.Fatalf("profile status=%d body=%s", profRec.Code, profRec.Body.String())
		}
	})

	t.Run("without file:write cannot create", func(t *testing.T) {
		authorizer.denyWrite = true
		defer func() { authorizer.denyWrite = false }()
		response := requestAAPFile(t, router, http.MethodPost, base, map[string]any{
			"mediaType": "image/png", "sizeBytes": len(png1x1),
		}, "service", uuid.NewString())
		if response.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
}

func TestAAPFileDTOSensitiveAllowlist(t *testing.T) {
	fileTags := contractJSONTags(aapFileDTO{})
	for tag := range fileTags {
		normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(tag))
		for _, forbidden := range []string{"upload", "presign", "downloadurl", "signedurl", "presigned"} {
			if strings.Contains(normalized, forbidden) {
				t.Fatalf("aapFileDTO exposes sensitive tag %q", tag)
			}
		}
	}
	if _, ok := fileTags["upload"]; ok {
		t.Fatal("aapFileDTO must not have upload field")
	}
	// Create response intentionally includes upload (create-only surface).
	createTags := contractJSONTags(aapCreateFileResponse{})
	if _, ok := createTags["upload"]; !ok {
		t.Fatal("create response should allow upload field")
	}
}

func TestSetAAPFileStreamHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("image stays inline with nosniff", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		setAAPFileStreamHeaders(c, "image/png", "pixel.png")
		if rec.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("Content-Type=%q", rec.Header().Get("Content-Type"))
		}
		if rec.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("Cache-Control=%q", rec.Header().Get("Cache-Control"))
		}
		if rec.Header().Get("X-Accel-Buffering") != "no" {
			t.Fatalf("X-Accel-Buffering=%q", rec.Header().Get("X-Accel-Buffering"))
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("nosniff=%q", rec.Header().Get("X-Content-Type-Options"))
		}
		if rec.Header().Get("Content-Disposition") != "" {
			t.Fatalf("image must stay inline, got %q", rec.Header().Get("Content-Disposition"))
		}
	})

	t.Run("non-image is attachment", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		setAAPFileStreamHeaders(c, "text/csv", "invoice-2026-08.csv")
		got := rec.Header().Get("Content-Disposition")
		if got != `attachment; filename="invoice-2026-08.csv"` {
			t.Fatalf("Content-Disposition=%q", got)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("nosniff=%q", rec.Header().Get("X-Content-Type-Options"))
		}
	})

	t.Run("non-ascii filename uses RFC 5987", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		setAAPFileStreamHeaders(c, "application/json", "对账单.json")
		got := rec.Header().Get("Content-Disposition")
		if !strings.Contains(got, `attachment; filename="`) || !strings.Contains(got, "filename*=UTF-8''") {
			t.Fatalf("Content-Disposition=%q", got)
		}
	})
}

// IC-07: download token hardening on the public content proxy path.
func TestAAPFileDownloadTokenHardening(t *testing.T) {
	filesOn := config.AgentAccessFilesConfig{
		Enabled: true, AllowAllWorkspaces: true, AllowAllClients: true,
	}
	store := newMemoryFileStore()
	staging := newMemoryStaging()
	secure := &memorySecurePutter{}
	// Clock is controlled so expired-token cases are deterministic.
	clock := time.Now().UTC()
	domain, err := aapfile.NewService(store, staging, secure,
		aapfile.WithClock(func() time.Time { return clock }),
	)
	if err != nil {
		t.Fatal(err)
	}
	app, err := aap.NewFileService(domain)
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &aapFileAuthorizer{}
	content := &memoryContentOpener{bodies: map[string][]byte{}}
	routes, err := NewAAPFileRoutes(authorizer, app, content, &filesOn)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: aapFileTokenAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{routes},
	})
	if err != nil {
		t.Fatal(err)
	}

	objID := uuid.NewString()
	fileID := uuid.NewString()
	ready := aapfile.File{
		ID: fileID, WorkspaceID: aapFileWorkspaceID, AgentID: aapFileAgentID,
		Status: aapfile.StatusReady, DeclaredMediaType: "image/png",
		SizeBytes: int64(len(png1x1)), StoredObjectID: &objID,
		ActorType: aapfile.ActorServicePrincipal, ActorID: aapFileServiceID,
		ClientID: aapFileClientID, OwnershipMode: aapfile.OwnershipSubjectOwned,
		OwnershipPolicyVersion: 1, Purpose: aapfile.PurposeGeneral,
		StagingBucket: "staging", StagingExpiresAt: clock.Add(time.Hour),
		CreatedAt: clock, UpdatedAt: clock,
	}
	store.files[fileID] = ready
	content.bodies[objID] = png1x1

	t.Run("single_use_second_read_not_found", func(t *testing.T) {
		minted, err := domain.MintDownloadToken(context.Background(), aapfile.MintDownloadTokenInput{
			Scope:  aapfile.Scope{WorkspaceID: aapFileWorkspaceID, AgentID: aapFileAgentID},
			FileID: fileID, Purpose: aapfile.DownloadPurposeToolInvoke,
			CreatedBy: aapFileServiceID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !minted.Token.SingleUse {
			t.Fatal("tool_invoke must be single_use")
		}
		path := "/api/agent-access/v1/files/downloads/" + minted.Token.ID
		first := httptest.NewRecorder()
		router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, path, nil))
		if first.Code != http.StatusOK {
			t.Fatalf("first download status=%d body=%s", first.Code, first.Body.String())
		}
		if first.Header().Get("X-Accel-Buffering") != "no" {
			t.Fatalf("missing X-Accel-Buffering: %q", first.Header().Get("X-Accel-Buffering"))
		}
		if first.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("missing nosniff: %q", first.Header().Get("X-Content-Type-Options"))
		}
		if first.Header().Get("Content-Disposition") != "" {
			t.Fatalf("image download must stay inline, got %q", first.Header().Get("Content-Disposition"))
		}
		if !bytes.Equal(first.Body.Bytes(), png1x1) {
			t.Fatal("body mismatch on first download")
		}
		// Second read must fail closed (404 conceal).
		second := httptest.NewRecorder()
		router.ServeHTTP(second, httptest.NewRequest(http.MethodGet, path, nil))
		if second.Code != http.StatusNotFound {
			t.Fatalf("second download status=%d body=%s want 404", second.Code, second.Body.String())
		}
		raw := strings.ToLower(second.Body.String())
		for _, forbidden := range []string{"minio", "x-amz", "presign", "secret"} {
			if strings.Contains(raw, forbidden) {
				t.Fatalf("error body leaked %q: %s", forbidden, second.Body.String())
			}
		}
	})

	t.Run("expired_token_rejected", func(t *testing.T) {
		minted, err := domain.MintDownloadToken(context.Background(), aapfile.MintDownloadTokenInput{
			Scope:  aapfile.Scope{WorkspaceID: aapFileWorkspaceID, AgentID: aapFileAgentID},
			FileID: fileID, Purpose: aapfile.DownloadPurposeClientContent,
			CreatedBy: aapFileServiceID, TTL: time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		clock = clock.Add(2 * time.Minute)
		path := "/api/agent-access/v1/files/downloads/" + minted.Token.ID
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expired status=%d body=%s want 404", rec.Code, rec.Body.String())
		}
		// Restore clock for subsequent cases.
		clock = time.Now().UTC()
	})

	t.Run("purpose_mismatch_rejected", func(t *testing.T) {
		// Mint client_content; resolve with tool_invoke expectation must fail.
		minted, err := domain.MintDownloadToken(context.Background(), aapfile.MintDownloadTokenInput{
			Scope:  aapfile.Scope{WorkspaceID: aapFileWorkspaceID, AgentID: aapFileAgentID},
			FileID: fileID, Purpose: aapfile.DownloadPurposeClientContent,
			CreatedBy: aapFileServiceID,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = domain.ResolveDownloadTokenForPurpose(
			context.Background(), minted.Token.ID, aapfile.DownloadPurposeToolInvoke,
		)
		if err != aapfile.ErrNotFound {
			t.Fatalf("purpose mismatch err=%v want ErrNotFound", err)
		}
		// Inject a token with an invalid purpose string (bypass mint validation).
		badID := uuid.NewString()
		store.tokens[badID] = aapfile.DownloadToken{
			ID: badID, WorkspaceID: aapFileWorkspaceID, FileID: fileID,
			Purpose: "not_a_real_purpose", JTI: uuid.NewString(),
			ExpiresAt: clock.Add(5 * time.Minute), CreatedBy: aapFileServiceID,
		}
		path := "/api/agent-access/v1/files/downloads/" + badID
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("bad purpose status=%d body=%s want 404", rec.Code, rec.Body.String())
		}
	})

	t.Run("mint_download_uses_quota_when_configured", func(t *testing.T) {
		// Reuse existing DataPlaneQuota pattern (file.download / file.content).
		// A 1-request limit proves mint is wired; content already covered by IC-04.
		quota, err := agentaccess.NewInMemoryDataPlaneQuota(agentaccess.DataPlaneQuotaConfig{
			Window: time.Minute, MaxEntries: 1000,
			Limits: map[agentaccess.DataPlaneQuotaOperation]int{
				agentaccess.QuotaFileDownload: 1,
				agentaccess.QuotaFileContent:  120,
				agentaccess.QuotaFileCreate:   60,
				agentaccess.QuotaFileComplete: 60,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		limited, err := NewAAPFileRoutes(authorizer, app, content, &filesOn)
		if err != nil {
			t.Fatal(err)
		}
		if err := limited.ConfigureCommandQuota(quota); err != nil {
			t.Fatal(err)
		}
		limRouter, err := NewRouter(Config{
			AgentAccessAuthenticator: aapFileTokenAuthenticator{},
			AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{limited},
		})
		if err != nil {
			t.Fatal(err)
		}
		base := "/api/agent-access/v1/workspaces/" + aapFileWorkspaceID +
			"/agents/" + aapFileAgentID + "/files/" + fileID + ":download"
		first := requestAAPFile(t, limRouter, http.MethodPost, base, nil, "service", "")
		if first.Code != http.StatusOK {
			t.Fatalf("first mint status=%d body=%s", first.Code, first.Body.String())
		}
		second := requestAAPFile(t, limRouter, http.MethodPost, base, nil, "service", "")
		if second.Code != http.StatusTooManyRequests {
			t.Fatalf("second mint status=%d body=%s want 429", second.Code, second.Body.String())
		}
	})
}

// ---- helpers & fakes ----

func requestAAPFile(
	t *testing.T,
	router http.Handler,
	method, path string,
	body map[string]any,
	token, idempotencyKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

type aapFileProfileRegistrar struct{}

func (aapFileProfileRegistrar) RegisterAgentAccessV1(v1 AgentAccessV1Routes) {
	v1.Protected.GET("/workspaces/:wid/agents/:aid/profile", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"id": c.Param("aid")})
	})
}

type aapFileAuthorizer struct {
	denyWrite bool
}

func (authorizer *aapFileAuthorizer) Authorize(
	_ context.Context,
	request agentaccessauth.AAPAuthorizationRequest,
) (agentaccessauth.AAPAuthorizationDecision, error) {
	if authorizer.denyWrite &&
		(request.Action == agentaccessauth.ActionFileCreate ||
			request.Action == agentaccessauth.ActionFileComplete) {
		return agentaccessauth.AAPAuthorizationDecision{}, agentaccessauth.ErrAAPAuthorizationDenied
	}
	required := "file:write"
	resourceType := agentaccessauth.ResourceNone
	resourceID := ""
	ownershipMode, ownershipVersion := "", int64(0)
	switch request.Action {
	case agentaccessauth.ActionFileRead, agentaccessauth.ActionFileContent:
		required = "file:read"
		resourceType = agentaccessauth.ResourceFile
		resourceID = request.Resource.ID
		ownershipMode, ownershipVersion = "SUBJECT_OWNED", 11
	case agentaccessauth.ActionFileComplete:
		resourceType = agentaccessauth.ResourceFile
		resourceID = request.Resource.ID
		ownershipMode, ownershipVersion = "SUBJECT_OWNED", 11
	}
	snapshot := agentaccessauth.AAPAuthorizationSnapshot{
		SpecVersion: "aap.authorization.v1", WorkspaceID: request.Principal.WorkspaceID,
		AgentID: request.Principal.AgentID, ClientID: aapFileClientID,
		AuthorizedParty:    request.Principal.AuthorizedParty,
		ServicePrincipalID: request.Principal.ServicePrincipalID,
		SubjectID:          request.Principal.PrincipalID, GrantID: aapFileGrantID,
		Action: request.Action, RequiredScope: required,
		TokenScopes: []string{required}, GrantScopes: []string{required},
		AgentPolicyScopes: []string{required}, EffectiveScopes: []string{required},
		TokenSecurityVersion: 1, ResolvedSecurityVersion: 1,
		WorkspaceVersion: 1, ClientVersion: 1, GrantVersion: 7, AgentPolicyVersion: 11,
		TokenID: request.Principal.TokenID, ResourceType: resourceType,
		ResourceID: resourceID, OwnershipMode: ownershipMode,
		OwnershipPolicyVersion: ownershipVersion, AuthorizedAt: time.Now().UTC(),
	}
	return agentaccessauth.AAPAuthorizationDecision{
		EffectiveScopes: []string{required}, Snapshot: snapshot,
	}, nil
}

type aapFileTokenAuthenticator struct{}

func (aapFileTokenAuthenticator) VerifyAccessToken(
	_ context.Context, value string,
) (agentaccessauth.AAPAccessTokenPrincipal, error) {
	if value != "service" {
		return agentaccessauth.AAPAccessTokenPrincipal{}, errors.New("invalid file token")
	}
	now := time.Now().UTC()
	return agentaccessauth.AAPAccessTokenPrincipal{
		PrincipalID: aapFileServiceID, ServicePrincipalID: aapFileServiceID,
		AuthorizedParty: "awcl_aap_file_client",
		WorkspaceID:     aapFileWorkspaceID, AgentID: aapFileAgentID,
		Scopes: []string{"file:write", "file:read", "agent:read"}, SecurityVersion: 1,
		TokenID: aapFileTokenID, IssuedAt: now.Add(-time.Minute),
		NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(10 * time.Minute),
	}, nil
}

type memoryFileStore struct {
	mu     sync.Mutex
	files  map[string]aapfile.File
	jobs   map[string]aapfile.ProcessingJob
	tokens map[string]aapfile.DownloadToken
}

func newMemoryFileStore() *memoryFileStore {
	return &memoryFileStore{
		files:  make(map[string]aapfile.File),
		jobs:   make(map[string]aapfile.ProcessingJob),
		tokens: make(map[string]aapfile.DownloadToken),
	}
}

func (s *memoryFileStore) InsertFile(_ context.Context, file aapfile.File) (aapfile.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.files[file.ID]; ok {
		return aapfile.File{}, aapfile.ErrConflict
	}
	now := time.Now().UTC()
	file.CreatedAt = now
	file.UpdatedAt = now
	s.files[file.ID] = file
	return file, nil
}

func (s *memoryFileStore) GetFile(_ context.Context, workspaceID, fileID string) (aapfile.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, ok := s.files[fileID]
	if !ok || file.WorkspaceID != workspaceID {
		return aapfile.File{}, aapfile.ErrNotFound
	}
	return file, nil
}

func (s *memoryFileStore) CompleteUploadCAS(
	_ context.Context, workspaceID, fileID string, expectedVersion int64, detected *string,
) (aapfile.File, aapfile.ProcessingJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, ok := s.files[fileID]
	if !ok || file.WorkspaceID != workspaceID {
		return aapfile.File{}, aapfile.ProcessingJob{}, aapfile.ErrNotFound
	}
	if file.Status != aapfile.StatusPendingUpload {
		job, jobOK := s.jobs[fileID+":promote"]
		if !jobOK {
			return aapfile.File{}, aapfile.ProcessingJob{}, aapfile.ErrNotFound
		}
		return file, job, nil
	}
	if file.ProcessingVersion != expectedVersion {
		return aapfile.File{}, aapfile.ProcessingJob{}, aapfile.ErrConflict
	}
	file.Status = aapfile.StatusUploaded
	file.ProcessingVersion++
	file.UpdatedAt = time.Now().UTC()
	if detected != nil {
		file.DetectedMediaType = detected
	}
	s.files[fileID] = file
	job := aapfile.ProcessingJob{
		ID: uuid.NewString(), WorkspaceID: workspaceID, FileID: fileID,
		Stage: aapfile.StagePromote, Status: aapfile.JobPending,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	s.jobs[fileID+":promote"] = job
	return file, job, nil
}

func (s *memoryFileStore) MarkFileFailed(
	_ context.Context, workspaceID, fileID, code, message string, _ int64,
) (aapfile.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, ok := s.files[fileID]
	if !ok || file.WorkspaceID != workspaceID {
		return aapfile.File{}, aapfile.ErrNotFound
	}
	file.Status = aapfile.StatusFailed
	file.ErrorCode = &code
	file.ErrorMessage = &message
	file.ProcessingVersion++
	s.files[fileID] = file
	return file, nil
}

func (s *memoryFileStore) ApplyPromoteSuccess(
	context.Context, string, string, string, string, string, int64, bool, *time.Time, bool,
) (aapfile.File, error) {
	return aapfile.File{}, aapfile.ErrInvalid
}

func (s *memoryFileStore) MarkPromoteFailed(
	context.Context, string, string, string, string, int64,
) (aapfile.File, error) {
	return aapfile.File{}, aapfile.ErrInvalid
}

func (s *memoryFileStore) GetJob(
	_ context.Context, workspaceID, fileID, stage string,
) (aapfile.ProcessingJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[fileID+":"+stage]
	if !ok || job.WorkspaceID != workspaceID {
		return aapfile.ProcessingJob{}, aapfile.ErrNotFound
	}
	return job, nil
}

func (s *memoryFileStore) ListJobs(
	_ context.Context, workspaceID, fileID string,
) ([]aapfile.ProcessingJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []aapfile.ProcessingJob
	for _, job := range s.jobs {
		if job.WorkspaceID == workspaceID && job.FileID == fileID {
			out = append(out, job)
		}
	}
	return out, nil
}

func (s *memoryFileStore) CountPendingUploads(_ context.Context, workspaceID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, f := range s.files {
		if f.WorkspaceID == workspaceID && f.Status == aapfile.StatusPendingUpload {
			n++
		}
	}
	return n, nil
}

func (s *memoryFileStore) SumReadyBytes(_ context.Context, workspaceID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	for _, f := range s.files {
		if f.WorkspaceID == workspaceID && f.Status == aapfile.StatusReady {
			total += f.SizeBytes
		}
	}
	return total, nil
}

func (s *memoryFileStore) InsertDownloadToken(
	_ context.Context, token aapfile.DownloadToken,
) (aapfile.DownloadToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token.CreatedAt = time.Now().UTC()
	s.tokens[token.ID] = token
	return token, nil
}

func (s *memoryFileStore) GetDownloadToken(_ context.Context, tokenID string) (aapfile.DownloadToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.tokens[tokenID]
	if !ok {
		return aapfile.DownloadToken{}, aapfile.ErrNotFound
	}
	return token, nil
}

func (s *memoryFileStore) ConsumeDownloadToken(
	_ context.Context, tokenID string,
) (aapfile.DownloadToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.tokens[tokenID]
	if !ok || token.ConsumedAt != nil || !token.SingleUse {
		return aapfile.DownloadToken{}, aapfile.ErrNotFound
	}
	now := time.Now().UTC()
	if !token.ExpiresAt.After(now) {
		return aapfile.DownloadToken{}, aapfile.ErrNotFound
	}
	token.ConsumedAt = &now
	s.tokens[tokenID] = token
	return token, nil
}

func (s *memoryFileStore) ListGeneratedForRun(
	_ context.Context, workspaceID, agentID, runID string,
) ([]aapfile.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]aapfile.File, 0)
	for _, file := range s.files {
		if file.WorkspaceID == workspaceID && file.AgentID == agentID &&
			file.SourceRunID == runID && file.Purpose == aapfile.PurposeAgentOutput {
			out = append(out, file)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *memoryFileStore) PurgeExpiredDownloadTokens(
	_ context.Context, limit int,
) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = aapfile.DefaultDownloadTokenPurgeBatch
	}
	now := time.Now().UTC()
	n := 0
	for id, token := range s.tokens {
		if n >= limit {
			break
		}
		if !token.ExpiresAt.After(now) {
			delete(s.tokens, id)
			n++
		}
	}
	return n, nil
}

type memoryStaging struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemoryStaging() *memoryStaging {
	return &memoryStaging{data: make(map[string][]byte)}
}

func (s *memoryStaging) key(bucket, object string) string { return bucket + "/" + object }

func (s *memoryStaging) put(bucket, object string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[s.key(bucket, object)] = append([]byte(nil), body...)
}

func (s *memoryStaging) Stat(_ context.Context, bucket, key string) (aapfile.BlobInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.data[s.key(bucket, key)]
	if !ok {
		return aapfile.BlobInfo{}, errors.New("staging object not found")
	}
	return aapfile.BlobInfo{Size: int64(len(body))}, nil
}

func (s *memoryStaging) Open(_ context.Context, bucket, key string) (io.ReadCloser, aapfile.BlobInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.data[s.key(bucket, key)]
	if !ok {
		return nil, aapfile.BlobInfo{}, errors.New("staging object not found")
	}
	return io.NopCloser(bytes.NewReader(body)), aapfile.BlobInfo{Size: int64(len(body))}, nil
}

func (s *memoryStaging) Delete(_ context.Context, bucket, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, s.key(bucket, key))
	return nil
}

func (s *memoryStaging) PresignPutWithHeaders(
	_ context.Context, bucket, key string, _ time.Duration, headers http.Header,
) (*url.URL, error) {
	if headers.Get("Content-Length") == "" {
		return nil, errors.New("Content-Length required")
	}
	return url.Parse("https://staging.example.test/" + bucket + "/" + key + "?X-Amz-SignedHeaders=content-length")
}

type memorySecurePutter struct{}

func (memorySecurePutter) Put(context.Context, storedobject.PutInput) (storedobject.StoredObject, error) {
	return storedobject.StoredObject{}, errors.New("promote not used in HTTP unit tests")
}

type memoryContentOpener struct {
	bodies map[string][]byte
}

func (o *memoryContentOpener) Open(
	_ context.Context, request storedobject.ReadRequest,
) (storedobject.OpenedObject, error) {
	body, ok := o.bodies[request.ObjectID]
	if !ok {
		return storedobject.OpenedObject{}, storedobject.ErrNotFound
	}
	return storedobject.OpenedObject{
		Metadata: storedobject.StoredObject{
			ID: request.ObjectID, WorkspaceID: request.WorkspaceID,
			Kind: storedobject.KindAAPFile, SizeBytes: int64(len(body)),
		},
		Body: io.NopCloser(bytes.NewReader(body)),
	}, nil
}
