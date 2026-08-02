package a2agateway_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

func TestCreateRemote_AuthSecretRef_CrossWorkspaceRejected(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedA2AAuditFixture(t, db)
	repo, err := a2agateway.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	otherWS := uuid.Must(uuid.NewV7()).String()
	secretID := uuid.Must(uuid.NewV7()).String()
	_, err = repo.CreateRemote(context.Background(), a2agateway.CreateRemoteInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.workspaceID,
		CallerAgentID: fx.agentA, CallableName: "remote-x",
		EndpointURL: "https://1.1.1.1/a2a", AllowedHosts: []string{"1.1.1.1"},
		AuthSecretRef: "secret:" + otherWS + ":" + secretID,
		TimeoutMs:     5000, Enabled: true, ActorID: fx.ownerID,
	})
	if err == nil {
		t.Fatal("cross-workspace authSecretRef must be rejected at CreateRemote")
	}
	if !errors.Is(err, a2agateway.ErrInvalid) && !strings.Contains(strings.ToLower(err.Error()), "workspace") {
		t.Fatalf("want workspace mismatch / invalid, got %v", err)
	}
	if strings.Contains(err.Error(), "Bearer") || strings.Contains(err.Error(), "password") {
		t.Fatalf("secret leakage in error: %v", err)
	}
}

func TestCreateRemote_AuthSecretRef_SameWorkspaceAccepted(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedA2AAuditFixture(t, db)
	repo, err := a2agateway.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	secretID := uuid.Must(uuid.NewV7()).String()
	ref := "secret:" + fx.workspaceID + ":" + secretID
	remote, err := repo.CreateRemote(context.Background(), a2agateway.CreateRemoteInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.workspaceID,
		CallerAgentID: fx.agentA, CallableName: "remote-ok",
		EndpointURL: "https://1.1.1.1/a2a", AllowedHosts: []string{"1.1.1.1"},
		AuthSecretRef: ref, TimeoutMs: 5000, Enabled: true, ActorID: fx.ownerID,
	})
	if err != nil {
		t.Fatalf("same-workspace ref: %v", err)
	}
	if remote.AuthSecretRef != ref {
		t.Fatalf("authSecretRef=%q", remote.AuthSecretRef)
	}
}

func TestUpdateRemote_AuthSecretRef_CrossWorkspaceRejected(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedA2AAuditFixture(t, db)
	repo, _ := a2agateway.NewRepository(db)
	remote, err := repo.CreateRemote(context.Background(), a2agateway.CreateRemoteInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: fx.workspaceID,
		CallerAgentID: fx.agentA, CallableName: "remote-upd",
		EndpointURL: "https://1.1.1.1/a2a", AllowedHosts: []string{"1.1.1.1"},
		TimeoutMs: 5000, Enabled: true, ActorID: fx.ownerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	otherWS := uuid.Must(uuid.NewV7()).String()
	bad := "secret:" + otherWS + ":" + uuid.Must(uuid.NewV7()).String()
	_, err = repo.UpdateRemote(context.Background(), a2agateway.UpdateRemoteInput{
		WorkspaceID: fx.workspaceID, BindingID: remote.ID,
		ExpectedVersion: remote.Version, AuthSecretRef: &bad, ActorID: fx.ownerID,
	})
	if err == nil {
		t.Fatal("UpdateRemote cross-workspace authSecretRef must fail")
	}
}

// TestRuntimeAuthSecretResolver_WorkspaceBinding mirrors production fail-closed rule:
// ref workspace must equal RunContext.WorkspaceID (historical bad rows cannot resolve).
func TestRuntimeAuthSecretResolver_WorkspaceBinding(t *testing.T) {
	resolver := func(ctx context.Context, secretRef string) (string, error) {
		parts := strings.Split(strings.TrimSpace(secretRef), ":")
		if len(parts) != 3 || parts[0] != "secret" {
			return "", a2agateway.ErrInvalid
		}
		refWS := strings.TrimSpace(parts[1])
		rc, ok := agentdelegation.RunContextFrom(ctx)
		if !ok || rc == nil || strings.TrimSpace(rc.WorkspaceID) == "" {
			return "", a2agateway.ErrInvalid
		}
		if !strings.EqualFold(refWS, strings.TrimSpace(rc.WorkspaceID)) {
			return "", a2agateway.ErrInvalid
		}
		return "Bearer ok", nil
	}
	ws := uuid.Must(uuid.NewV7()).String()
	other := uuid.Must(uuid.NewV7()).String()
	secretID := uuid.Must(uuid.NewV7()).String()
	ctx := agentdelegation.WithRunContext(context.Background(), &agentdelegation.RunContext{
		WorkspaceID: ws, ParentRunID: uuid.Must(uuid.NewV7()).String(), RunID: uuid.Must(uuid.NewV7()).String(),
	})
	if _, err := resolver(ctx, "secret:"+ws+":"+secretID); err != nil {
		t.Fatalf("same workspace: %v", err)
	}
	if _, err := resolver(ctx, "secret:"+other+":"+secretID); err == nil {
		t.Fatal("cross workspace must fail")
	}
	if _, err := resolver(context.Background(), "secret:"+ws+":"+secretID); err == nil {
		t.Fatal("missing run context must fail")
	}
}
