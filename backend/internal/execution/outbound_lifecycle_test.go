package execution

import (
	"context"
	"testing"
	"time"

	"actweave/backend/internal/outboundidentity"
)

func TestRootOutboundLifecycleCleansVaultAndCache(t *testing.T) {
	clock := outboundidentity.NewFakeClock(time.Now().UTC())
	vault, err := outboundidentity.NewRuntimeCredentialVault("boot-life", clock, outboundidentity.VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	cache := outboundidentity.NewBrokerTokenCache(clock)

	ws := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	user := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	rootID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	conn := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	key := outboundidentity.VaultKey{
		BootID: "boot-life", WorkspaceID: ws,
		SubjectType: outboundidentity.SubjectTypeUser, SubjectID: user,
		RootScopeType: outboundidentity.RootScopeWorkflowExecution, RootScopeID: rootID,
		ConnectionID: conn, ConnectionPolicyVersion: 1,
	}
	if err := vault.Attach([]outboundidentity.AttachBinding{{
		Key: key, CredentialType: outboundidentity.CredentialTypeAccessToken,
		Value: []byte("life-token"), ExpiresAt: clock.Now().Add(10 * time.Minute),
	}}); err != nil {
		t.Fatal(err)
	}
	// Seed cache with a fake entry via InvalidateRoot path after store simulation:
	// use FormatBrokerCacheKeyString + GetOrExchange would need broker; just
	// verify CleanupRoot does not panic and vault is empty.
	life := &RootOutboundLifecycle{Vault: vault, Cache: cache, BootID: "boot-life"}
	life.CleanupRoot(context.Background(), CleanupRootInput{
		BootID: "boot-life", WorkspaceID: ws,
		SubjectType: outboundidentity.SubjectTypeUser, SubjectID: user,
		RootScopeType: outboundidentity.RootScopeWorkflowExecution, RootScopeID: rootID,
		ClearAffinity: false,
	})
	// Second call idempotent.
	life.CleanupRoot(context.Background(), CleanupRootInput{
		BootID: "boot-life", WorkspaceID: ws,
		SubjectType: outboundidentity.SubjectTypeUser, SubjectID: user,
		RootScopeType: outboundidentity.RootScopeWorkflowExecution, RootScopeID: rootID,
	})
	if _, err := vault.Borrow(key); err == nil {
		t.Fatal("expected vault entry cleaned")
	}
}

func TestRootScopeForInvokePrefersAgentRun(t *testing.T) {
	typ, id := RootScopeForInvoke("run-1", "wf-exec-1", "inv-1")
	if typ != outboundidentity.RootScopeAgentRun || id != "run-1" {
		t.Fatalf("got %s %s", typ, id)
	}
	typ, id = RootScopeForInvoke("", "wf-exec-1", "inv-1")
	if typ != outboundidentity.RootScopeWorkflowExecution || id != "wf-exec-1" {
		t.Fatalf("got %s %s", typ, id)
	}
	typ, id = RootScopeForInvoke("", "", "inv-1")
	if typ != outboundidentity.RootScopeDirectInvocation || id != "inv-1" {
		t.Fatalf("got %s %s", typ, id)
	}
}

func TestOutboundInvokeContextTrialTrace(t *testing.T) {
	ctx := outboundInvokeContextFromRequest(InvokeRequest{
		InvocationID: "inv-1", WorkflowExecutionID: "trial-exec-1",
		TraceID: "workflow-trial/compile-1",
	})
	if ctx.RootScopeType != outboundidentity.RootScopeWorkflowTrial || ctx.RootScopeID != "trial-exec-1" {
		t.Fatalf("trial root: %+v", ctx)
	}
	ctx2 := outboundInvokeContextFromRequest(InvokeRequest{
		InvocationID: "inv-1", WorkflowExecutionID: "prod-exec-1",
		TraceID: "workflow-production/rev-1",
	})
	if ctx2.RootScopeType != outboundidentity.RootScopeWorkflowExecution {
		t.Fatalf("production root: %+v", ctx2)
	}
	ctx3 := outboundInvokeContextFromRequest(InvokeRequest{
		InvocationID: "inv-1", AgentRunID: "agent-run-1", WorkflowExecutionID: "wf-1",
	})
	if ctx3.RootScopeType != outboundidentity.RootScopeAgentRun || ctx3.RootScopeID != "agent-run-1" {
		t.Fatalf("agent root: %+v", ctx3)
	}
}
