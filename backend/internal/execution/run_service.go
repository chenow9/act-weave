package execution

import (
	"context"
	"encoding/json"
	"errors"

	"actweave/backend/internal/principal"
)

type AgentRunSnapshotSource interface {
	SnapshotAgentRun(context.Context, string, string) (AgentRunSnapshots, error)
}

type RunAuthorizationSource interface {
	AuthorizeRun(
		context.Context,
		string,
		string,
		string,
		string,
		string,
	) (json.RawMessage, error)
}

type RunService struct {
	repository *RunRepository
	snapshots  AgentRunSnapshotSource
	authorizer RunAuthorizationSource
}

func NewRunService(
	repository *RunRepository,
	snapshots AgentRunSnapshotSource,
	authorizer RunAuthorizationSource,
) (*RunService, error) {
	if repository == nil || snapshots == nil || authorizer == nil {
		return nil, errors.New("run service repository, snapshot source and authorizer are required")
	}
	return &RunService{repository: repository, snapshots: snapshots, authorizer: authorizer}, nil
}

type StartAgentRunRequest struct {
	ID                    string
	WorkspaceID           string
	SessionID             string
	AgentID               string
	TriggerType           string
	TriggeredByType       string
	TriggeredByID         string
	TraceID               string
	InputSummary          json.RawMessage
	AuthorizationSnapshot json.RawMessage
	PrincipalSnapshot     *principal.ExecutionSnapshot
}

func (s *RunService) StartAgentRun(
	ctx context.Context,
	request StartAgentRunRequest,
) (AgentRun, error) {
	input, err := s.PrepareAgentRun(ctx, request)
	if err != nil {
		return AgentRun{}, err
	}
	return s.repository.StartAgentRun(ctx, input)
}

// PrepareAgentRun resolves authorization and immutable snapshots outside the
// caller's database transaction. Chat orchestration can then atomically persist
// the prepared Run and its messages without holding locks across external work.
func (s *RunService) PrepareAgentRun(
	ctx context.Context,
	request StartAgentRunRequest,
) (StartAgentRunInput, error) {
	var authorization json.RawMessage
	var err error
	if len(request.AuthorizationSnapshot) > 0 {
		if request.PrincipalSnapshot == nil {
			return StartAgentRunInput{}, ErrRunInvalid
		}
		authorization, err = canonicalRunObject(request.AuthorizationSnapshot)
	} else {
		authorization, err = s.authorizer.AuthorizeRun(
			ctx, request.TriggeredByType, request.TriggeredByID, request.WorkspaceID,
			"agent.run", request.AgentID,
		)
	}
	if err != nil {
		return StartAgentRunInput{}, err
	}
	snapshots, err := s.snapshots.SnapshotAgentRun(ctx, request.WorkspaceID, request.AgentID)
	if err != nil {
		return StartAgentRunInput{}, err
	}
	return StartAgentRunInput{
		ID: request.ID, WorkspaceID: request.WorkspaceID, SessionID: request.SessionID,
		AgentID: request.AgentID, TriggerType: request.TriggerType,
		TriggeredByType: request.TriggeredByType, TriggeredByID: request.TriggeredByID,
		TraceID: request.TraceID, Snapshots: snapshots,
		AuthorizationSnapshot: authorization, InputSummary: request.InputSummary,
		PrincipalSnapshot: request.PrincipalSnapshot,
		// Root chat runs carry their own explicit empty freeze; startAgentRun
		// still defaults to {} when the snapshot source produced none (legacy).
		AgentGraphSnapshot: snapshots.Graph,
	}, nil
}

type StartWorkflowExecutionRequest struct {
	ID                string
	WorkspaceID       string
	WorkflowID        string
	RevisionID        string
	AgentRunID        string
	TriggerType       string
	TriggeredByType   string
	TriggeredByID     string
	TraceID           string
	InputSummary      json.RawMessage
	PrincipalSnapshot *principal.ExecutionSnapshot
}

func (s *RunService) StartWorkflowExecution(
	ctx context.Context,
	request StartWorkflowExecutionRequest,
) (WorkflowExecution, error) {
	input, err := s.PrepareWorkflowExecution(ctx, request)
	if err != nil {
		return WorkflowExecution{}, err
	}
	return s.repository.StartWorkflowExecution(ctx, input)
}

// PrepareWorkflowExecution performs authorization and snapshot construction
// without writing. It is used when the caller must create the execution inside
// a larger transaction, such as production idempotency claim + start.
func (s *RunService) PrepareWorkflowExecution(
	ctx context.Context,
	request StartWorkflowExecutionRequest,
) (StartWorkflowExecutionInput, error) {
	authorization, err := s.authorizer.AuthorizeRun(
		ctx, request.TriggeredByType, request.TriggeredByID, request.WorkspaceID,
		"workflow.execute", request.WorkflowID,
	)
	if err != nil {
		return StartWorkflowExecutionInput{}, err
	}
	return StartWorkflowExecutionInput{
		ID: request.ID, WorkspaceID: request.WorkspaceID, WorkflowID: request.WorkflowID,
		RevisionID: request.RevisionID, AgentRunID: request.AgentRunID,
		TriggerType: request.TriggerType, TriggeredByType: request.TriggeredByType,
		TriggeredByID: request.TriggeredByID, TraceID: request.TraceID,
		SnapshotSchemaVersion: "run.v1", AuthorizationSnapshot: authorization,
		InputSummary:      request.InputSummary,
		PrincipalSnapshot: request.PrincipalSnapshot,
	}, nil
}
