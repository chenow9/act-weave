package tool

import (
	"encoding/json"
	"time"
)

type Tool struct {
	CapabilityID        string
	WorkspaceID         string
	ProviderID          string
	SourceAssetID       *string
	DefaultConnectionID *string
	SourceEndpointID    *string
	Name                string
	Slug                string
	Description         string
	Status              string
	ActiveReleaseID     *string
	CreatedBy           string
	UpdatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	LockVersion         int64
	DeletedAt           *time.Time
}

type Version struct {
	ID                   string
	WorkspaceID          string
	CapabilityID         string
	VersionNo            int
	LifecycleStatus      string
	ExecutorType         string
	ProviderID           string
	ProviderAssetID      *string
	DefaultConnectionID  *string
	HandlerKey           *string
	ExecutionProfileID   *string
	ActionSchemaVersion  string
	ActionConfig         json.RawMessage
	InputSchema          json.RawMessage
	OutputSchema         json.RawMessage
	ErrorMappings        json.RawMessage
	RuntimePolicy        json.RawMessage
	RiskLevel            string
	SideEffectLevel      string
	RequiresConfirmation bool
	Checksum             string
	CreatedBy            string
	UpdatedBy            string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	PublishedAt          *time.Time
	LockVersion          int64
}

type DraftSpec struct {
	ProviderAssetID      *string
	DefaultConnectionID  *string
	ActionSchemaVersion  string
	ActionConfig         json.RawMessage
	InputSchema          json.RawMessage
	OutputSchema         json.RawMessage
	ErrorMappings        json.RawMessage
	RuntimePolicy        json.RawMessage
	RiskLevel            string
	SideEffectLevel      string
	RequiresConfirmation bool
}

type CreateInput struct {
	CapabilityID        string
	InitialVersionID    string
	WorkspaceID         string
	ProviderID          string
	SourceAssetID       *string
	DefaultConnectionID *string
	SourceEndpointID    *string
	Name                string
	Slug                string
	Description         string
	Draft               DraftSpec
	CreatedBy           string
}

type MetadataUpdate struct {
	Name                string
	Slug                string
	Description         string
	Status              string
	SourceAssetID       *string
	DefaultConnectionID *string
	UpdatedBy           string
	ExpectedLockVersion int64
}

type DraftUpdate struct {
	Spec                DraftSpec
	LifecycleStatus     string
	UpdatedBy           string
	ExpectedLockVersion int64
}

type TestRecord struct {
	ID                   string
	WorkspaceID          string
	ToolVersionID        string
	Status               string
	ConnectivityPassed   bool
	ResponseSchemaPassed bool
	ErrorMappingPassed   bool
	RuntimePolicyPassed  bool
	RequestSummary       json.RawMessage
	ResponseSummary      json.RawMessage
	LatencyMS            *int
	ErrorCode            *string
	RawObjectID          *string
	TestedBy             string
	TestedAt             time.Time
}

type RecordTestInput struct {
	ID                   string
	WorkspaceID          string
	ToolVersionID        string
	VersionChecksum      string
	ExpectedVersionLock  int64
	Status               string
	ConnectivityPassed   bool
	ResponseSchemaPassed bool
	ErrorMappingPassed   bool
	RuntimePolicyPassed  bool
	RequestSummary       json.RawMessage
	ResponseSummary      json.RawMessage
	LatencyMS            *int
	ErrorCode            *string
	RawObjectID          *string
	TestedBy             string
}
