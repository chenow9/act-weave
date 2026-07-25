package execution

import (
	"context"
	"strings"

	"actweave/backend/internal/outboundidentity"
)

// RootOutboundLifecycle cleans process-local outbound state when a root execution
// reaches a terminal / cancelled / failed recovery outcome (checklist #10).
//
// It never restores Tokens, never migrates Vault entries across boots, and never
// writes Secret material to durable stores.
type RootOutboundLifecycle struct {
	Vault   outboundidentity.RootLifecycleCleaner
	Cache   *outboundidentity.BrokerTokenCache
	Runtime *outboundidentity.RuntimeRepository
	// BootID is the process boot used when constructing cleanup keys.
	BootID string
}

// CleanupRootInput identifies a top-level root without Token material.
type CleanupRootInput struct {
	BootID        string
	WorkspaceID   string
	SubjectType   outboundidentity.SubjectType
	SubjectID     string
	RootScopeType outboundidentity.RootScopeType
	RootScopeID   string
	// ClearAffinity deletes outbound_runtime_affinities for this root (passthrough).
	ClearAffinity bool
}

// CleanupRoot is idempotent. Safe to call on every terminal transition.
func (l *RootOutboundLifecycle) CleanupRoot(ctx context.Context, input CleanupRootInput) {
	if l == nil {
		return
	}
	bootID := strings.TrimSpace(input.BootID)
	if bootID == "" {
		bootID = strings.TrimSpace(l.BootID)
	}
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	rootID := strings.TrimSpace(input.RootScopeID)
	if bootID == "" || workspaceID == "" || rootID == "" {
		return
	}
	if l.Vault != nil && input.SubjectType.Valid() && strings.TrimSpace(input.SubjectID) != "" &&
		input.RootScopeType.Valid() {
		l.Vault.CleanupRoot(outboundidentity.RootScope{
			BootID: bootID, WorkspaceID: workspaceID,
			SubjectType: input.SubjectType, SubjectID: input.SubjectID,
			RootScopeType: input.RootScopeType, RootScopeID: rootID,
		})
	}
	if l.Cache != nil {
		l.Cache.InvalidateRoot(bootID, workspaceID, rootID)
	}
	if input.ClearAffinity && l.Runtime != nil && ctx != nil && input.RootScopeType.Valid() {
		_ = l.Runtime.DeleteAffinity(ctx, workspaceID, input.RootScopeType, rootID)
	}
}

// SubjectFromPrincipal extracts USER / EXTERNAL_SUBJECT for cleanup keys.
// SYSTEM / nil → empty (cleanup still clears cache by root id when subject unknown).
func SubjectFromPrincipal(snapshot *principalRef) (outboundidentity.SubjectType, string) {
	if snapshot == nil {
		return "", ""
	}
	return snapshot.Type, snapshot.ID
}

// principalRef is a minimal subject view to avoid circular imports in tests.
// Production callers pass typed values via CleanupRootInput directly.
type principalRef struct {
	Type outboundidentity.SubjectType
	ID   string
}

// RootScopeForInvoke picks the top-level root for Vault/Broker (AgentRun wins).
// Nested Workflow → Tool keeps the same AgentRun root when present.
func RootScopeForInvoke(agentRunID, workflowExecutionID, invocationID string) (outboundidentity.RootScopeType, string) {
	if id := strings.TrimSpace(agentRunID); id != "" {
		return outboundidentity.RootScopeAgentRun, id
	}
	if id := strings.TrimSpace(workflowExecutionID); id != "" {
		return outboundidentity.RootScopeWorkflowExecution, id
	}
	if id := strings.TrimSpace(invocationID); id != "" {
		return outboundidentity.RootScopeDirectInvocation, id
	}
	return "", ""
}
