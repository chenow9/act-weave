package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"actweave/backend/internal/storedobject"
)

type invocationPayloadWriter interface {
	Write(context.Context, storedobject.SensitivePayloadInput) (storedobject.SensitivePayloadResult, error)
}

// PermanentInvocationRecorder stores redacted summaries in PostgreSQL and the
// scrubbed full request/response in an encrypted permanent object. The start
// row is committed before the executor side effect; terminal state is recorded
// only after its payload object exists.
type PermanentInvocationRecorder struct {
	repository *ToolInvocationRepository
	payloads   invocationPayloadWriter
}

func NewPermanentInvocationRecorder(
	repository *ToolInvocationRepository,
	payloads invocationPayloadWriter,
) (*PermanentInvocationRecorder, error) {
	if repository == nil || payloads == nil {
		return nil, errors.New("tool invocation repository and payload writer are required")
	}
	return &PermanentInvocationRecorder{repository: repository, payloads: payloads}, nil
}

func (recorder *PermanentInvocationRecorder) InvocationStarted(
	ctx context.Context,
	record InvocationRecord,
) error {
	if record.Status != "RUNNING" || record.RetentionMode != InvocationRetentionMode {
		return ErrToolInvocationInvalid
	}
	started, err := recorder.repository.Start(ctx, StartToolInvocationInput{
		ID: record.InvocationID, WorkspaceID: record.WorkspaceID,
		ToolID: record.CapabilityID, ToolVersionID: record.ToolVersionID,
		CapabilityReleaseID: record.ReleaseID, ProviderID: record.ProviderID,
		ConnectionID: record.ConnectionID, ActorType: record.ActorType, ActorID: record.ActorID,
		TraceID: record.TraceID, IdempotencyKey: record.IdempotencyKey,
		InputSummary: append([]byte(nil), record.InputSummary...),
		AgentRunID:   record.AgentRunID, WorkflowExecutionID: record.WorkflowExecutionID,
		ExecutionStepID: record.ExecutionStepID, PrincipalSnapshot: record.PrincipalSnapshot,
		AuthorizationSnapshot: append([]byte(nil), record.AuthorizationSnapshot...),
	})
	if err != nil {
		return err
	}
	if !started.Created && started.Invocation.ID != record.InvocationID {
		return ErrToolInvocationIdempotencyConflict
	}
	return nil
}

func (recorder *PermanentInvocationRecorder) InvocationFinished(
	ctx context.Context,
	record InvocationRecord,
) error {
	if (record.Status != "SUCCEEDED" && record.Status != "FAILED") ||
		record.RetentionMode != InvocationRetentionMode {
		return ErrToolInvocationInvalid
	}
	payload, err := recorder.payloads.Write(ctx, storedobject.SensitivePayloadInput{
		ObjectID: strings.TrimSpace(record.InvocationID), WorkspaceID: strings.TrimSpace(record.WorkspaceID),
		Kind:    storedobject.KindToolInvocationPayload,
		Request: append([]byte(nil), record.Input...), Response: append([]byte(nil), record.Output...),
		ErrorCode: strings.TrimSpace(record.ErrorCode), CreatedByType: record.ActorType,
		CreatedByID: strings.TrimSpace(record.ActorID),
	})
	if err != nil {
		return fmt.Errorf("store permanent invocation payload: %w", err)
	}
	if record.Status == "FAILED" {
		_, err = recorder.repository.Fail(ctx, record.WorkspaceID, record.InvocationID,
			FailToolInvocationInput{
				OutputSummary: append([]byte(nil), record.OutputSummary...),
				RawObjectID:   payload.ObjectID, ErrorCode: record.ErrorCode,
			})
		return err
	}
	_, err = recorder.repository.Complete(ctx, record.WorkspaceID, record.InvocationID,
		CompleteToolInvocationInput{
			OutputSummary: append([]byte(nil), record.OutputSummary...),
			RawObjectID:   payload.ObjectID,
		})
	return err
}
