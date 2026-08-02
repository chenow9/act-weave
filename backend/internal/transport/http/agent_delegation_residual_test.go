package httptransport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/database/dbtest"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Residual #2/#3: capabilities.allowAuthNone + full PATCH fields for remote/exposure.

type stubWSAuthorizer struct{}

func (stubWSAuthorizer) AuthorizeWorkspace(_ context.Context, _, _ string, _ authz.Action) (authz.WorkspaceContext, error) {
	return authz.WorkspaceContext{}, nil
}

func TestAgentDelegation_Capabilities_AllowAuthNone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedDelegationHTTPFixture(t, db)

	delRepo, _ := agentdelegation.NewRepository(db)
	delSvc, _ := agentdelegation.NewService(delRepo)
	a2aRepo, _ := a2agateway.NewRepository(db)

	// allowAuthNone=false
	routesOff, err := NewAgentDelegationRoutes(stubWSAuthorizer{}, delSvc, a2aRepo, "https://app.example",
		AgentDelegationRouteOptions{AllowAuthNone: false})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	v1.Use(func(c *gin.Context) {
		p := Principal{UserID: fx.owner, PlatformRole: "PLATFORM_ADMIN"}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), principalContextKey{}, p))
		c.Next()
	})
	routesOff.RegisterV1(V1Routes{Protected: v1})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+fx.ws+"/a2a/capabilities", nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var caps map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	if caps["allowAuthNone"] != false {
		t.Fatalf("allowAuthNone=%v want false", caps["allowAuthNone"])
	}
	modes, _ := caps["authModes"].([]any)
	for _, m := range modes {
		if m == "NONE" {
			t.Fatal("NONE must not be listed when allowAuthNone=false")
		}
	}

	// allowAuthNone=true
	routesOn, _ := NewAgentDelegationRoutes(stubWSAuthorizer{}, delSvc, a2aRepo, "https://app.example",
		AgentDelegationRouteOptions{AllowAuthNone: true})
	engine2 := gin.New()
	v12 := engine2.Group("/api/v1")
	v12.Use(func(c *gin.Context) {
		p := Principal{UserID: fx.owner, PlatformRole: "PLATFORM_ADMIN"}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), principalContextKey{}, p))
		c.Next()
	})
	routesOn.RegisterV1(V1Routes{Protected: v12})
	req = httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+fx.ws+"/a2a/capabilities", nil)
	rec = httptest.NewRecorder()
	engine2.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	caps = map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &caps)
	if caps["allowAuthNone"] != true {
		t.Fatalf("allowAuthNone=%v want true", caps["allowAuthNone"])
	}
	modes, _ = caps["authModes"].([]any)
	foundNone := false
	for _, m := range modes {
		if m == "NONE" {
			foundNone = true
		}
	}
	if !foundNone {
		t.Fatalf("authModes missing NONE: %v", modes)
	}
}

func TestAgentDelegation_FullPatchRemoteAndExposure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedDelegationHTTPFixture(t, db)

	delRepo, _ := agentdelegation.NewRepository(db)
	delSvc, _ := agentdelegation.NewService(delRepo)
	a2aRepo, _ := a2agateway.NewRepository(db)
	routes, err := NewAgentDelegationRoutes(stubWSAuthorizer{}, delSvc, a2aRepo, "https://app.example",
		AgentDelegationRouteOptions{AllowAuthNone: true})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	v1.Use(func(c *gin.Context) {
		p := Principal{UserID: fx.owner, PlatformRole: "PLATFORM_ADMIN"}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), principalContextKey{}, p))
		c.Next()
	})
	routes.RegisterV1(V1Routes{Protected: v1})

	// Create exposure
	expBody := map[string]any{
		"agentId": fx.agentA, "publicName": "Pub", "publicDescription": "d0",
		"authMode": "AGENT_ACCESS", "enabled": true,
	}
	rec := doJSON(t, engine, http.MethodPost, "/api/v1/workspaces/"+fx.ws+"/a2a/exposures", expBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create exp status=%d body=%s", rec.Code, rec.Body.String())
	}
	var exp a2agateway.Exposure
	_ = json.Unmarshal(rec.Body.Bytes(), &exp)

	// Full PATCH exposure fields
	name2, desc2, auth2 := "Pub2", "d1", "NONE"
	en := false
	patchExp := map[string]any{
		"expectedVersion": exp.Version, "publicName": name2, "publicDescription": desc2,
		"authMode": auth2, "enabled": en,
	}
	rec = doJSON(t, engine, http.MethodPatch, "/api/v1/workspaces/"+fx.ws+"/a2a/exposures/"+exp.ID, patchExp)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch exp status=%d body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &exp)
	if exp.PublicName != name2 || exp.PublicDescription != desc2 || exp.AuthMode != auth2 || exp.Enabled {
		t.Fatalf("exposure patch not applied: %+v", exp)
	}
	// Soft re-enable
	en = true
	rec = doJSON(t, engine, http.MethodPatch, "/api/v1/workspaces/"+fx.ws+"/a2a/exposures/"+exp.ID, map[string]any{
		"expectedVersion": exp.Version, "enabled": en,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("re-enable exp status=%d body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &exp)
	if !exp.Enabled {
		t.Fatal("re-enable failed")
	}

	// Create remote (literal public IPs — avoid example.com DNS sinkhole to 198.18/15).
	// authSecretRef must be secret:<bindingWorkspaceUUID>:<secretUUID>.
	secret1 := "secret:" + fx.ws + ":" + uuid.Must(uuid.NewV7()).String()
	remoteBody := map[string]any{
		"callableName": "remote_x", "description": "rd0",
		"endpointUrl": "https://1.1.1.1/a2a", "agentCardUrl": "https://1.1.1.1/.well-known/agent.json",
		"allowedHosts": []string{"1.1.1.1"}, "authSecretRef": secret1,
		"timeoutMs": 45000, "enabled": true,
	}
	rec = doJSON(t, engine, http.MethodPost, "/api/v1/workspaces/"+fx.ws+"/agents/"+fx.agentA+"/a2a-remotes", remoteBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create remote status=%d body=%s", rec.Code, rec.Body.String())
	}
	var remote a2agateway.RemoteBinding
	_ = json.Unmarshal(rec.Body.Bytes(), &remote)

	// Full PATCH remote model fields
	cn, desc, ep, card := "remote_y", "rd1", "https://8.8.8.8/rpc", "https://8.8.8.8/card"
	hosts := []string{"8.8.8.8"}
	secret := "secret:" + fx.ws + ":" + uuid.Must(uuid.NewV7()).String()
	to := 90000
	en = false
	rec = doJSON(t, engine, http.MethodPatch, "/api/v1/workspaces/"+fx.ws+"/a2a-remotes/"+remote.ID, map[string]any{
		"expectedVersion": remote.Version,
		"callableName":    cn, "description": desc, "endpointUrl": ep, "agentCardUrl": card,
		"allowedHosts": hosts, "authSecretRef": secret, "timeoutMs": to, "enabled": en,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch remote status=%d body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &remote)
	if remote.CallableName != cn || remote.Description != desc || remote.EndpointURL != ep ||
		remote.AgentCardURL != card || remote.AuthSecretRef != secret || remote.TimeoutMs != to || remote.Enabled {
		t.Fatalf("remote full patch not applied: %+v", remote)
	}
	if len(remote.AllowedHosts) != 1 || remote.AllowedHosts[0] != "8.8.8.8" {
		t.Fatalf("hosts=%v", remote.AllowedHosts)
	}

	// Binding create + full PATCH including targetAgentId/callableName/mode/enabled
	rec = doJSON(t, engine, http.MethodPost, "/api/v1/workspaces/"+fx.ws+"/agents/"+fx.agentA+"/delegation-bindings", map[string]any{
		"targetAgentId": fx.agentB, "callableName": "call_b", "description": "bd0",
		"mode": "INLINE", "contextPolicy": "TASK_ONLY", "enabled": true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create binding status=%d body=%s", rec.Code, rec.Body.String())
	}
	var b agentdelegation.Binding
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	rec = doJSON(t, engine, http.MethodPatch, "/api/v1/workspaces/"+fx.ws+"/delegation-bindings/"+b.ID, map[string]any{
		"expectedVersion": b.Version,
		"targetAgentId":   fx.agentC, "callableName": "call_c2", "description": "bd1",
		"mode": "TASK", "contextPolicy": "TASK_ONLY", "enabled": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch binding status=%d body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if b.TargetAgentID != fx.agentC || b.CallableName != "call_c2" || b.Mode != "TASK" || b.Description != "bd1" {
		t.Fatalf("binding patch: %+v", b)
	}
}

func TestAgentDelegation_RejectsAuthNoneWhenDisallowed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedDelegationHTTPFixture(t, db)
	delRepo, _ := agentdelegation.NewRepository(db)
	delSvc, _ := agentdelegation.NewService(delRepo)
	a2aRepo, _ := a2agateway.NewRepository(db)
	routes, _ := NewAgentDelegationRoutes(stubWSAuthorizer{}, delSvc, a2aRepo, "https://app.example",
		AgentDelegationRouteOptions{AllowAuthNone: false})
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	v1.Use(func(c *gin.Context) {
		p := Principal{UserID: fx.owner, PlatformRole: "PLATFORM_ADMIN"}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), principalContextKey{}, p))
		c.Next()
	})
	routes.RegisterV1(V1Routes{Protected: v1})
	rec := doJSON(t, engine, http.MethodPost, "/api/v1/workspaces/"+fx.ws+"/a2a/exposures", map[string]any{
		"agentId": fx.agentA, "publicName": "N", "authMode": "NONE", "enabled": true,
	})
	if rec.Code == http.StatusCreated {
		t.Fatalf("NONE must be rejected when allowAuthNone=false: %s", rec.Body.String())
	}
}

func TestAgentDelegation_NamespaceConflict_InternalVsRemote(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedDelegationHTTPFixture(t, db)
	delRepo, _ := agentdelegation.NewRepository(db)
	delSvc, _ := agentdelegation.NewService(delRepo)
	a2aRepo, _ := a2agateway.NewRepository(db)
	routes, _ := NewAgentDelegationRoutes(stubWSAuthorizer{}, delSvc, a2aRepo, "https://app.example")
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	v1.Use(func(c *gin.Context) {
		p := Principal{UserID: fx.owner, PlatformRole: "PLATFORM_ADMIN"}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), principalContextKey{}, p))
		c.Next()
	})
	routes.RegisterV1(V1Routes{Protected: v1})

	rec := doJSON(t, engine, http.MethodPost, "/api/v1/workspaces/"+fx.ws+"/agents/"+fx.agentA+"/delegation-bindings", map[string]any{
		"targetAgentId": fx.agentB, "callableName": "shared_name", "mode": "INLINE",
		"contextPolicy": "TASK_ONLY", "enabled": true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("binding: %d %s", rec.Code, rec.Body.String())
	}
	// Same callable_name as remote must conflict.
	rec = doJSON(t, engine, http.MethodPost, "/api/v1/workspaces/"+fx.ws+"/agents/"+fx.agentA+"/a2a-remotes", map[string]any{
		"callableName": "shared_name", "endpointUrl": "https://1.1.1.1/a2a",
		"allowedHosts": []string{"1.1.1.1"}, "timeoutMs": 5000, "enabled": true,
	})
	if rec.Code == http.StatusCreated {
		t.Fatalf("expected namespace conflict, got created: %s", rec.Body.String())
	}
	if rec.Code != http.StatusConflict && rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
		// Accept 4xx family for conflict/invalid mapping.
		if rec.Code < 400 || rec.Code >= 500 {
			t.Fatalf("namespace conflict status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

func doJSON(t *testing.T, engine *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

type delHTTPFx struct {
	owner, ws, model, agentA, agentB, agentC string
}

func seedDelegationHTTPFixture(t *testing.T, db *sql.DB) delHTTPFx {
	t.Helper()
	fx := delHTTPFx{
		owner: uuid.Must(uuid.NewV7()).String(), ws: uuid.Must(uuid.NewV7()).String(),
		model: uuid.Must(uuid.NewV7()).String(), agentA: uuid.Must(uuid.NewV7()).String(),
		agentB: uuid.Must(uuid.NewV7()).String(), agentC: uuid.Must(uuid.NewV7()).String(),
	}
	exec := func(q string, a ...any) {
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	exec(`INSERT INTO users(id,username,display_name) VALUES($1,'del.http','D')`, fx.owner)
	exec(`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,$2,'D','SANDBOX',$3,$3,$3)`, fx.ws, "del-"+fx.ws[:8], fx.owner)
	exec(`INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		VALUES($1,$2,'m','openai','https://x','m',$3,$3)`, fx.model, fx.ws, fx.owner)
	for i, a := range []string{fx.agentA, fx.agentB, fx.agentC} {
		// UUID v7 shares a time prefix — use full id for unique active names.
		exec(`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
			VALUES($1,$2,$3,$4,$5,$5)`, a, fx.ws, "agent-"+a, fx.model, fx.owner)
		_ = i
	}
	return fx
}

// silence unused in case of build tags
var _ = strings.Contains
