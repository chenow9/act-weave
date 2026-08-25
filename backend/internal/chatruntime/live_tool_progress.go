package chatruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

// LiveToolProgressInput is one item.delta progress tick for a running tool_call.
type LiveToolProgressInput struct {
	Run          execution.AgentRun
	Job          Job
	Name         string
	ReleaseID    string
	InvocationID string
	Args         json.RawMessage
	Current      float64
	Total        *float64
	Unit         string
	Message      string
	OccurredAt   time.Time
}

// LiveToolCompleteInput finishes a tool_call that was opened for live progress.
type LiveToolCompleteInput struct {
	Run          execution.AgentRun
	Job          Job
	Name         string
	ReleaseID    string
	InvocationID string
	Args         json.RawMessage
	Result       json.RawMessage
	OK           bool
	ErrorCode    string
	StartedAt    time.Time
	FinishedAt   time.Time
}

// LiveToolCallProjector opens/ticks/closes protocol tool_call items during invoke.
type LiveToolCallProjector interface {
	ProjectLiveToolProgress(context.Context, LiveToolProgressInput) error
	ProjectLiveToolCompleted(context.Context, LiveToolCompleteInput) error
}

var _ LiveToolCallProjector = (*NativeProtocolRecorder)(nil)

func (recorder *NativeProtocolRecorder) ProjectLiveToolProgress(
	ctx context.Context,
	in LiveToolProgressInput,
) error {
	if recorder == nil || recorder.toolProjector == nil || ctx == nil {
		return errors.New("native runtime live tool progress is invalid")
	}
	invocationID, name, started, protocolContext, err := recorder.liveToolBase(in.Run, in.Job, in.Name, in.ReleaseID, in.InvocationID, in.Args, in.OccurredAt)
	if err != nil {
		return err
	}
	_, itemErr := recorder.items.Get(ctx, in.Run.WorkspaceID, in.Run.AgentID, in.Run.ID, invocationID)
	if errors.Is(itemErr, protocolevent.ErrRunItemNotFound) {
		ordinal, ordinalErr := recorder.nextOrdinal(ctx, in.Run)
		if ordinalErr != nil {
			return ordinalErr
		}
		if _, err := recorder.toolProjector.ProjectStarted(ctx, execution.ProjectToolCallStartedInput{
			Context: protocolContext, Invocation: started, Name: name, Ordinal: ordinal,
			SourceType: protocolevent.SourceRuntime,
		}); err != nil {
			return err
		}
		if _, err := recorder.toolProjector.ProjectArguments(ctx, execution.ProjectToolCallDeltaInput{
			Context: protocolContext, Invocation: started, OccurredAt: started.StartedAt,
		}); err != nil {
			return err
		}
	} else if itemErr != nil {
		return itemErr
	}
	occurredAt := in.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	unit := strings.TrimSpace(in.Unit)
	if unit == "" {
		unit = "percent"
	}
	_, err = recorder.toolProjector.ProjectProgress(ctx, execution.ProjectToolCallProgressInput{
		Context: protocolContext, Invocation: started, Current: in.Current, Total: in.Total,
		Unit: unit, Message: in.Message, OccurredAt: occurredAt,
	})
	return err
}

func (recorder *NativeProtocolRecorder) ProjectLiveToolCompleted(
	ctx context.Context,
	in LiveToolCompleteInput,
) error {
	if recorder == nil || recorder.toolProjector == nil || ctx == nil {
		return errors.New("native runtime live tool complete is invalid")
	}
	invocationID := strings.TrimSpace(in.InvocationID)
	if invocationID == "" {
		return nil
	}
	_, itemErr := recorder.items.Get(ctx, in.Run.WorkspaceID, in.Run.AgentID, in.Run.ID, invocationID)
	if errors.Is(itemErr, protocolevent.ErrRunItemNotFound) {
		return nil
	}
	if itemErr != nil {
		return itemErr
	}
	finishedAt := in.FinishedAt.UTC()
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	result := append(json.RawMessage(nil), in.Result...)
	if len(bytes.TrimSpace(result)) == 0 {
		result = json.RawMessage(`{}`)
	}
	args := append(json.RawMessage(nil), in.Args...)
	if len(bytes.TrimSpace(args)) == 0 {
		args = json.RawMessage(`{}`)
	}
	releaseID := strings.TrimSpace(in.ReleaseID)
	if releaseID == "" {
		releaseID = PlatformPublishReleaseID
	}
	startedAt := in.StartedAt.UTC()
	if startedAt.IsZero() {
		startedAt = finishedAt
	}
	completed := execution.ToolInvocation{
		ID: invocationID, WorkspaceID: in.Run.WorkspaceID, AgentRunID: in.Run.ID,
		CapabilityReleaseID: releaseID, StartedAt: startedAt,
		ActorType: in.Run.TriggeredByType,
		ActorID:   firstNonEmpty(in.Job.ActorID, in.Run.TriggeredByID),
		TraceID:   in.Run.TraceID, InputSummary: args, OutputSummary: result,
		FinishedAt: &finishedAt,
	}
	if in.OK {
		completed.Status = "SUCCEEDED"
	} else {
		completed.Status = "FAILED"
		completed.ErrorCode = strings.TrimSpace(in.ErrorCode)
		if completed.ErrorCode == "" {
			completed.ErrorCode = "TOOL_INVOKE_FAILED"
		}
	}
	protocolContext := execution.ProtocolToolCallContext{
		Scope: runtimeProtocolScope(in.Run), EventStreamID: in.Run.ID, TraceID: in.Run.TraceID,
	}
	_, err := recorder.toolProjector.Complete(ctx, execution.CompleteProtocolToolCallInput{
		Context: protocolContext, Invocation: completed, Name: strings.TrimSpace(in.Name),
		CompletedAt: finishedAt,
	})
	return err
}

func (recorder *NativeProtocolRecorder) liveToolBase(
	run execution.AgentRun,
	job Job,
	name, releaseID, invocationID string,
	args json.RawMessage,
	occurredAt time.Time,
) (string, string, execution.ToolInvocation, execution.ProtocolToolCallContext, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.TrimSpace(run.ID) == "" || strings.TrimSpace(run.TraceID) == "" {
		return "", "", execution.ToolInvocation{}, execution.ProtocolToolCallContext{},
			errors.New("native runtime live tool progress is invalid")
	}
	invocationID = strings.TrimSpace(invocationID)
	if invocationID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return "", "", execution.ToolInvocation{}, execution.ProtocolToolCallContext{}, err
		}
		invocationID = id.String()
	}
	startedAt := occurredAt.UTC()
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	clonedArgs := append(json.RawMessage(nil), args...)
	if len(bytes.TrimSpace(clonedArgs)) == 0 {
		clonedArgs = json.RawMessage(`{}`)
	}
	releaseID = strings.TrimSpace(releaseID)
	if releaseID == "" {
		releaseID = PlatformPublishReleaseID
	}
	started := execution.ToolInvocation{
		ID: invocationID, WorkspaceID: run.WorkspaceID, AgentRunID: run.ID,
		CapabilityReleaseID: releaseID,
		ActorType:           run.TriggeredByType,
		ActorID:             firstNonEmpty(job.ActorID, run.TriggeredByID),
		TraceID:             run.TraceID, InputSummary: clonedArgs,
		Status: "RUNNING", StartedAt: startedAt,
	}
	protocolContext := execution.ProtocolToolCallContext{
		Scope: runtimeProtocolScope(run), EventStreamID: run.ID, TraceID: run.TraceID,
	}
	return invocationID, name, started, protocolContext, nil
}
