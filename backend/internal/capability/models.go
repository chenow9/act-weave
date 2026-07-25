package capability

import (
	"encoding/json"
	"time"
)

type Capability struct {
	ID              string
	WorkspaceID     string
	Kind            string
	Name            string
	Slug            string
	Description     string
	Status          string
	ActiveReleaseID *string
	CreatedBy       string
	UpdatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LockVersion     int64
	DeletedAt       *time.Time
}

type CatalogItem struct {
	Capability
	BoundAgentCount int
	ActiveRelease   *Descriptor
}

type Release struct {
	ID                   string
	WorkspaceID          string
	CapabilityID         string
	ReleaseNo            int
	SourceType           string
	SourceID             string
	CallableName         string
	CallableDescription  string
	InputSchema          json.RawMessage
	OutputSchema         json.RawMessage
	RiskLevel            string
	SideEffectLevel      string
	RequiresConfirmation bool
	Checksum             string
	PublishedBy          string
	PublishedAt          time.Time
	RetiredAt            *time.Time
}

type Descriptor struct {
	CapabilityID         string          `json:"capabilityId"`
	ReleaseID            string          `json:"releaseId"`
	Kind                 string          `json:"kind"`
	CallableName         string          `json:"callableName"`
	CallableDescription  string          `json:"callableDescription"`
	InputSchema          json.RawMessage `json:"inputSchema"`
	OutputSchema         json.RawMessage `json:"outputSchema"`
	RiskLevel            string          `json:"riskLevel"`
	SideEffectLevel      string          `json:"sideEffectLevel"`
	RequiresConfirmation bool            `json:"requiresConfirmation"`
	// ConnectionID is filled from agent binding when listing for a run snapshot.
	// It is not part of the published release itself.
	ConnectionID string `json:"connectionId,omitempty"`
	// OutboundRequirements is outbound-requirements.v1 when a dual-mode
	// Connection is bound. Descriptor only — never Secret / Token / locator.
	OutboundRequirements json.RawMessage `json:"outboundRequirements,omitempty"`
}

type ResolvedCapability struct {
	Descriptor
	WorkspaceID string
	SourceType  string
	SourceID    string
	Checksum    string
	ReleaseNo   int
	PublishedAt time.Time
}

type NewCapability struct {
	ID          string
	WorkspaceID string
	Kind        string
	Name        string
	Slug        string
	Description string
	CreatedBy   string
}

type PublishRelease struct {
	ID                   string
	WorkspaceID          string
	CapabilityID         string
	SourceType           string
	SourceID             string
	CallableName         string
	CallableDescription  string
	InputSchema          json.RawMessage
	OutputSchema         json.RawMessage
	RiskLevel            string
	SideEffectLevel      string
	RequiresConfirmation bool
	Checksum             string
	PublishedBy          string
}

type BindingSelection struct {
	CapabilityID    string
	VersionPolicy   string
	PinnedReleaseID *string
	ConnectionID    *string
}

type Binding struct {
	WorkspaceID       string
	AgentID           string
	CapabilityID      string
	VersionPolicy     string
	PinnedReleaseID   *string
	ConnectionID      *string
	ExecutionPolicyID *string
	Enabled           bool
	ConfigOverrides   json.RawMessage
	BoundBy           string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	LockVersion       int64
}

type BindInput struct {
	WorkspaceID         string
	AgentID             string
	CapabilityID        string
	VersionPolicy       string
	PinnedReleaseID     *string
	ConnectionID        *string
	ExecutionPolicyID   *string
	Enabled             bool
	ConfigOverrides     json.RawMessage
	BoundBy             string
	ExpectedLockVersion int64
}
