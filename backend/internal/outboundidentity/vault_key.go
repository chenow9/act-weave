package outboundidentity

import (
	"strings"
)

// Root scope types for top-level executions that may own Vault entries.
const (
	RootScopeAgentRun          RootScopeType = "AGENT_RUN"
	RootScopeDirectInvocation  RootScopeType = "DIRECT_INVOCATION"
	RootScopeToolTest          RootScopeType = "TOOL_TEST"
	RootScopeWorkflowTrial     RootScopeType = "WORKFLOW_TRIAL"
	RootScopeWorkflowExecution RootScopeType = "WORKFLOW_EXECUTION"
	RootScopeDebugAttachment   RootScopeType = "DEBUG_ATTACHMENT"
)

type RootScopeType string

func (r RootScopeType) Valid() bool {
	switch r {
	case RootScopeAgentRun, RootScopeDirectInvocation, RootScopeToolTest,
		RootScopeWorkflowTrial, RootScopeWorkflowExecution, RootScopeDebugAttachment:
		return true
	default:
		return false
	}
}

// VaultKey uniquely identifies a RuntimeCredentialVault entry.
// Knowing only Run ID or Connection ID is never sufficient to borrow plaintext.
//
// VaultKey is intentionally NOT serialized: no JSON tags, no String() that
// could leak into logs. Callers pass structured keys, not opaque handles.
type VaultKey struct {
	BootID                  string
	WorkspaceID             string
	SubjectType             SubjectType
	SubjectID               string
	RootScopeType           RootScopeType
	RootScopeID             string
	ConnectionID            string
	ConnectionPolicyVersion int64
}

// Valid reports whether every required component is present and typed correctly.
func (k VaultKey) Valid() bool {
	return strings.TrimSpace(k.BootID) != "" &&
		strings.TrimSpace(k.WorkspaceID) != "" &&
		k.SubjectType.Valid() &&
		strings.TrimSpace(k.SubjectID) != "" &&
		k.RootScopeType.Valid() &&
		strings.TrimSpace(k.RootScopeID) != "" &&
		strings.TrimSpace(k.ConnectionID) != "" &&
		k.ConnectionPolicyVersion > 0
}

// Equal compares two keys component-wise.
func (k VaultKey) Equal(other VaultKey) bool {
	return k.BootID == other.BootID &&
		k.WorkspaceID == other.WorkspaceID &&
		k.SubjectType == other.SubjectType &&
		k.SubjectID == other.SubjectID &&
		k.RootScopeType == other.RootScopeType &&
		k.RootScopeID == other.RootScopeID &&
		k.ConnectionID == other.ConnectionID &&
		k.ConnectionPolicyVersion == other.ConnectionPolicyVersion
}

// RootScope identifies a top-level execution for cleanup / move.
type RootScope struct {
	BootID        string
	WorkspaceID   string
	SubjectType   SubjectType
	SubjectID     string
	RootScopeType RootScopeType
	RootScopeID   string
}

func (r RootScope) Valid() bool {
	return strings.TrimSpace(r.BootID) != "" &&
		strings.TrimSpace(r.WorkspaceID) != "" &&
		r.SubjectType.Valid() &&
		strings.TrimSpace(r.SubjectID) != "" &&
		r.RootScopeType.Valid() &&
		strings.TrimSpace(r.RootScopeID) != ""
}

func (r RootScope) MatchesKey(k VaultKey) bool {
	return r.BootID == k.BootID &&
		r.WorkspaceID == k.WorkspaceID &&
		r.SubjectType == k.SubjectType &&
		r.SubjectID == k.SubjectID &&
		r.RootScopeType == k.RootScopeType &&
		r.RootScopeID == k.RootScopeID
}
