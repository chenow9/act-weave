package a2agateway_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

func TestA2AExposureAndRemoteCRUD(t *testing.T) {
	h := dbtest.New(t)
	v := h.MigrateToLatest(t)
	if !v.Applied || v.Number < 15 {
		t.Fatalf("version=%+v", v)
	}
	db := h.Open(t)
	fx := seedA2A(t, db)
	repo, err := a2agateway.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	exp, err := repo.CreateExposure(ctx, a2agateway.CreateExposureInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.ws, AgentID: fx.agent,
		PublicName: "Public Helper", PublicDescription: "demo",
		AuthMode: a2agateway.AuthModeAgentAccess, Enabled: true, ActorID: fx.owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetExposureByAgent(ctx, fx.ws, fx.agent)
	if err != nil || got.ID != exp.ID {
		t.Fatalf("get by agent: %+v err=%v", got, err)
	}

	// Empty allowlist rejected (required non-empty).
	_, err = repo.CreateRemote(ctx, a2agateway.CreateRemoteInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.ws, CallerAgentID: fx.agent,
		CallableName: "remote_x", EndpointURL: "https://1.1.1.1/a2a",
		AllowedHosts: nil, TimeoutMs: 5000, Enabled: true, ActorID: fx.owner,
	})
	if !errors.Is(err, a2agateway.ErrInvalid) {
		t.Fatalf("expected ErrInvalid for empty allowlist, got %v", err)
	}
	// SSRF reject private even when listed
	_, err = repo.CreateRemote(ctx, a2agateway.CreateRemoteInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.ws, CallerAgentID: fx.agent,
		CallableName: "remote_y", EndpointURL: "http://127.0.0.1/evil",
		AllowedHosts: []string{"127.0.0.1"}, TimeoutMs: 5000, Enabled: true, ActorID: fx.owner,
	})
	if !errors.Is(err, a2agateway.ErrSSRFDenied) && err == nil {
		t.Fatalf("expected ssrf, got %v", err)
	}
	// agentCardURL must also be covered by allowlist
	_, err = repo.CreateRemote(ctx, a2agateway.CreateRemoteInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.ws, CallerAgentID: fx.agent,
		CallableName: "remote_card", EndpointURL: "https://1.1.1.1/a2a",
		AgentCardURL: "https://8.8.8.8/card", AllowedHosts: []string{"1.1.1.1"},
		TimeoutMs: 5000, Enabled: true, ActorID: fx.owner,
	})
	if err == nil {
		t.Fatal("expected agentCardURL allowlist reject")
	}

	remote, err := repo.CreateRemote(ctx, a2agateway.CreateRemoteInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.ws, CallerAgentID: fx.agent,
		CallableName: "remote_helper", EndpointURL: "https://1.1.1.1/a2a",
		AllowedHosts: []string{"1.1.1.1"}, TimeoutMs: 30000,
		Enabled: true, ActorID: fx.owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListEnabledRemotesForCaller(ctx, fx.ws, fx.agent)
	if err != nil || len(list) != 1 || list[0].ID != remote.ID {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	// Soft disable
	if err := repo.SoftDisableRemote(ctx, fx.ws, remote.ID, remote.Version, fx.owner); err != nil {
		t.Fatal(err)
	}
	list, _ = repo.ListEnabledRemotesForCaller(ctx, fx.ws, fx.agent)
	if len(list) != 0 {
		t.Fatalf("still enabled: %+v", list)
	}
}

func TestInboundCardAllowlist(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedA2A(t, db)
	repo, _ := a2agateway.NewRepository(db)
	ctx := context.Background()
	exp, err := repo.CreateExposure(ctx, a2agateway.CreateExposureInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.ws, AgentID: fx.agent,
		PublicName: "Card Agent", AuthMode: a2agateway.AuthModeNone, Enabled: true, ActorID: fx.owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	card := a2agateway.BuildAgentCardForExposure("http://127.0.0.1:8080", exp)
	if card.Name != "Card Agent" || card.URL == "" {
		t.Fatalf("%+v", card)
	}
	// Disabled exposure not listable as enabled
	_, _ = repo.UpdateExposure(ctx, a2agateway.UpdateExposureInput{
		WorkspaceID: fx.ws, ExposureID: exp.ID, ExpectedVersion: exp.Version,
		Enabled: boolPtr(false), ActorID: fx.owner,
	})
	enabled, err := repo.ListEnabledExposures(ctx, fx.ws)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range enabled {
		if e.ID == exp.ID {
			t.Fatal("disabled still enabled list")
		}
	}
}

type a2aFx struct{ owner, ws, model, agent string }

func seedA2A(t *testing.T, db *sql.DB) a2aFx {
	t.Helper()
	fx := a2aFx{
		owner: uuid.Must(uuid.NewV7()).String(),
		ws:    uuid.Must(uuid.NewV7()).String(),
		model: uuid.Must(uuid.NewV7()).String(),
		agent: uuid.Must(uuid.NewV7()).String(),
	}
	exec := func(q string, a ...any) {
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	exec(`INSERT INTO users(id,username,display_name) VALUES($1,'a2a.owner','A2A')`, fx.owner)
	exec(`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,$2,'A2A','SANDBOX',$3,$3,$3)`, fx.ws, "a2a-"+fx.ws[:8], fx.owner)
	exec(`INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		VALUES($1,$2,'m','openai','https://x','m',$3,$3)`, fx.model, fx.ws, fx.owner)
	exec(`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'A',$3,$4,$4)`, fx.agent, fx.ws, fx.model, fx.owner)
	return fx
}

func boolPtr(v bool) *bool { return &v }
