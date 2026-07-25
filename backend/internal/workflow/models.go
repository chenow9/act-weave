package workflow

import (
	"encoding/json"
	"time"
)

type Workflow struct {
	CapabilityID        string
	WorkspaceID         string
	CurrentDraftID      string
	ActiveRevisionID    *string
	LatestCompilationID *string
	Name                string
	Slug                string
	Description         string
	Status              string
	CreatedBy           string
	UpdatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	LockVersion         int64
	NodeCount           int
	EdgeCount           int
	DeletedAt           *time.Time
}

type Draft struct {
	ID            string
	WorkspaceID   string
	CapabilityID  string
	DraftVersion  int64
	SchemaVersion string
	Graph         json.RawMessage
	GraphHash     string
	UpdatedBy     string
	UpdatedAt     time.Time
	LockVersion   int64
}

type CreateInput struct {
	CapabilityID  string
	DraftID       string
	WorkspaceID   string
	Name          string
	Slug          string
	Description   string
	SchemaVersion string
	Graph         json.RawMessage
	CreatedBy     string
}

type MetadataUpdate struct {
	Name                string
	Slug                string
	Description         string
	Status              string
	UpdatedBy           string
	ExpectedLockVersion int64
}

type DraftUpdate struct {
	SchemaVersion        string
	Graph                json.RawMessage
	UpdatedBy            string
	ExpectedDraftVersion int64
	ExpectedLockVersion  int64
}

type Compilation struct {
	ID              string
	WorkspaceID     string
	CapabilityID    string
	DraftID         string
	DraftVersion    int64
	GraphHash       string
	CompilerVersion string
	Status          string
	Spec            json.RawMessage
	Plan            json.RawMessage
	Issues          json.RawMessage
	PlanHash        string
	CompiledBy      string
	CompiledAt      time.Time
}

type CompilationCreate struct {
	ID              string
	CompilerVersion string
	Status          string
	Spec            json.RawMessage
	Plan            json.RawMessage
	Issues          json.RawMessage
	PlanHash        string
	CompiledBy      string
}

type TrialRun struct {
	ID            string
	WorkspaceID   string
	CapabilityID  string
	CompilationID string
	ExecutionID   string
	Status        string
	InputHash     string
	StartedBy     string
	StartedAt     time.Time
	FinishedAt    *time.Time
}

type TrialRunCreate struct {
	ID          string
	ExecutionID string
	InputHash   string
	StartedBy   string
}

type Revision struct {
	ID                  string
	WorkspaceID         string
	CapabilityID        string
	RevisionNo          int
	SourceCompilationID string
	DraftSnapshot       json.RawMessage
	SpecSnapshot        json.RawMessage
	PlanSnapshot        json.RawMessage
	PlanHash            string
	Status              string
	PublishNote         string
	CreatedBy           string
	CreatedAt           time.Time
	ActivatedAt         *time.Time
	RetiredAt           *time.Time
}

type RevisionDiff struct {
	From            Revision
	To              Revision
	DraftChanged    bool
	SpecChanged     bool
	PlanChanged     bool
	PlanHashChanged bool
}

type ReadinessBlocker struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Action      string `json:"action"`
	Severity    string `json:"severity"`
	SourceStage string `json:"sourceStage,omitempty"`
	NodeID      string `json:"nodeId,omitempty"`
	EdgeID      string `json:"edgeId,omitempty"`
	FieldPath   string `json:"fieldPath,omitempty"`
}

type Readiness struct {
	Stage              string
	CanCompile         bool
	CanTrial           bool
	CanPublish         bool
	CompilationID      *string
	CompilationCurrent bool
	CompilationValid   bool
	TrialCurrent       bool
	TrialSuccessful    bool
	Published          bool
	ActiveRevisionID   *string
	Blockers           []ReadinessBlocker
	UpdatedAt          time.Time
}
