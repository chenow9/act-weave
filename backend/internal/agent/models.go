package agent

import (
	"encoding/json"
	"time"
)

type Status string

const (
	StatusActive   Status = "ACTIVE"
	StatusDisabled Status = "DISABLED"
	StatusError    Status = "ERROR"
)

type Agent struct {
	ID                      string
	WorkspaceID             string
	Name                    string
	RoleDescription         string
	CurrentPromptRevisionID *string
	ModelConfigID           string
	IsDefault               bool
	Status                  Status
	CreatedBy               string
	UpdatedBy               string
	CreatedAt               time.Time
	UpdatedAt               time.Time
	LockVersion             int64
	DeletedAt               *time.Time
}

type Summary struct {
	Agent
	ToolsCount     int
	WorkflowsCount int
}

type PromptRevision struct {
	ID            string
	WorkspaceID   string
	AgentID       string
	RevisionNo    int
	SystemPrompt  string
	Source        string
	ContentSHA256 string
	CreatedBy     string
	CreatedAt     time.Time
}

const (
	PromptSourceManual     = "MANUAL"
	PromptSourceEnhanced   = "ENHANCED"
	PromptSourceGenerated  = "GENERATED"
	PromptSourceImported   = "IMPORTED"
	PromptSourceAIAssisted = "AI_ASSISTED"

	PromptOperationEnhance       = "ENHANCE"
	PromptOperationGenerate      = "GENERATE"
	PromptOperationPreview       = "PREVIEW"
	PromptOperationCreatePreview = "CREATE_PREVIEW"
)

type PromptRun struct {
	ID                 string
	WorkspaceID        string
	AgentID            *string
	OperationType      string
	ModelConfigID      string
	ModelSnapshot      json.RawMessage
	InputObjectID      string
	InputSHA256        string
	InputLength        int64
	OutputObjectID     *string
	OutputSHA256       *string
	OutputLength       *int64
	Status             string
	AcceptedRevisionID *string
	TraceID            string
	CreatedBy          string
	CreatedAt          time.Time
	FinishedAt         *time.Time
	ErrorCode          *string
	ExpiresAt          *time.Time
	PromotedAt         *time.Time
	ContentPurgedAt    *time.Time
}

type NewAgent struct {
	ID                string
	WorkspaceID       string
	Name              string
	RoleDescription   string
	ModelConfigID     string
	IsDefault         bool
	InitialRevisionID string
	InitialPrompt     string
	PromptSource      string
	CreatedBy         string
}

type UpdateAgent struct {
	Name                string
	RoleDescription     string
	ModelConfigID       string
	Status              Status
	UpdatedBy           string
	ExpectedLockVersion int64
}

type NewPromptRun struct {
	ID            string
	WorkspaceID   string
	AgentID       *string
	OperationType string
	ModelConfigID string
	ModelSnapshot json.RawMessage
	InputObjectID string
	InputSHA256   string
	InputLength   int64
	TraceID       string
	CreatedBy     string
	// FixedCreatedAt is optional and only for CREATE_PREVIEW. When set, the Run
	// uses this timestamp as created_at and expires_at = created_at + 30 days so
	// input/output StoredObject retention_until can share the same clock source.
	FixedCreatedAt *time.Time
}
