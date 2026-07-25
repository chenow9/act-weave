package execution

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

var stableProtocolErrorCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)

var initialRunEventNamespace = uuid.MustParse("a34f38a2-74ec-4fa6-8c7f-34b90c6c0907")

type RunLifecycleEventInput struct {
	EventID        string
	EventStreamID  string
	EventType      string
	Run            AgentRun
	OccurredAt     time.Time
	InteractionIDs []string
	InteractionID  string
}

// RunLifecycleMapper is the single mapping from the internal AgentRun state to
// the public AAP Run snapshot and run.* Event Data.
type RunLifecycleMapper struct{}

func NewRunLifecycleMapper() *RunLifecycleMapper { return &RunLifecycleMapper{} }

func (mapper *RunLifecycleMapper) Map(
	input RunLifecycleEventInput,
) (protocolevent.NewProtocolEvent, error) {
	if mapper == nil || !runEventMatchesAgentStatus(input.EventType, input.Run.Status) {
		return protocolevent.NewProtocolEvent{}, ErrRunConflict
	}
	status := protocolRunStatus(input.EventType)
	snapshot, err := mapAgentRunSnapshot(input.Run, status)
	if err != nil {
		return protocolevent.NewProtocolEvent{}, err
	}
	base := protocolevent.NewProtocolEvent{
		ID: input.EventID, EventStreamID: input.EventStreamID,
		WorkspaceID: input.Run.WorkspaceID, AgentID: input.Run.AgentID,
		ConversationID: input.Run.SessionID, RunID: input.Run.ID,
		Type: input.EventType, SpecVersion: "1.0", TraceID: input.Run.TraceID,
		OccurredAt: input.OccurredAt,
	}
	var data protocolevent.EventData
	switch input.EventType {
	case protocolevent.EventRunWaiting:
		data = protocolevent.RunWaitingData{
			Run: snapshot, InteractionIDs: append([]string(nil), input.InteractionIDs...),
		}
	case protocolevent.EventRunResumed:
		data = protocolevent.RunResumedData{Run: snapshot, InteractionID: input.InteractionID}
	case protocolevent.EventRunAccepted, protocolevent.EventRunStarted,
		protocolevent.EventRunCompleted, protocolevent.EventRunFailed,
		protocolevent.EventRunCancelled:
		data = protocolevent.RunSnapshotData{Run: snapshot}
	default:
		return protocolevent.NewProtocolEvent{}, ErrRunInvalid
	}
	event, err := protocolevent.BuildProtocolEvent(base, data)
	if err != nil {
		return protocolevent.NewProtocolEvent{}, ErrRunInvalid
	}
	return event, nil
}

type ProtocolRunLifecycleResult struct {
	Run         AgentRun
	Events      []protocolevent.ProtocolEvent
	NotifyError error
}

type ProtocolRunTransitionInput struct {
	WorkspaceID    string
	RunID          string
	Transition     RunTransition
	InteractionIDs []string
	InteractionID  string
}

// ProtocolRunLifecycleService commits the AgentRun state and its authoritative
// run.* snapshot in the same Protocol Unit of Work.
type ProtocolRunLifecycleService struct {
	runs   *RunRepository
	unit   *protocolevent.ProtocolUnitOfWork
	mapper *RunLifecycleMapper
	now    func() time.Time
}

func NewProtocolRunLifecycleService(
	runs *RunRepository,
	unit *protocolevent.ProtocolUnitOfWork,
) (*ProtocolRunLifecycleService, error) {
	if runs == nil || unit == nil {
		return nil, ErrRunInvalid
	}
	return &ProtocolRunLifecycleService{
		runs: runs, unit: unit, mapper: NewRunLifecycleMapper(),
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

// AcceptAndStartAgentRun persists the Run ID before execution begins and emits
// accepted/running snapshots atomically with the new AgentRun row.
func (service *ProtocolRunLifecycleService) AcceptAndStartAgentRun(
	ctx context.Context,
	input StartAgentRunInput,
) (ProtocolRunLifecycleResult, error) {
	if service == nil || service.runs == nil || service.unit == nil || service.mapper == nil {
		return ProtocolRunLifecycleResult{}, ErrRunInvalid
	}
	acceptedID, startedID, err := newProtocolRunEventIDs()
	if err != nil {
		return ProtocolRunLifecycleResult{}, err
	}
	var run AgentRun
	unitResult, err := service.unit.Execute(ctx, func(
		ctx context.Context,
		transaction *protocolevent.ProtocolTransaction,
	) error {
		tx, err := transaction.SQLTx()
		if err != nil {
			return err
		}
		run, err = service.runs.StartAgentRunInTransaction(ctx, tx, input)
		if err != nil {
			return err
		}
		acceptedAt := run.StartedAt.UTC()
		startedAt := service.now().UTC()
		if startedAt.Before(acceptedAt) {
			startedAt = acceptedAt
		}
		streamID, err := transaction.EnsureRunEventStream(ctx, run.ID, protocolScope(run))
		if err != nil {
			return err
		}
		accepted, err := service.mapper.Map(RunLifecycleEventInput{
			EventID: acceptedID, EventStreamID: streamID,
			EventType: protocolevent.EventRunAccepted, Run: run, OccurredAt: acceptedAt,
		})
		if err != nil {
			return err
		}
		started, err := service.mapper.Map(RunLifecycleEventInput{
			EventID: startedID, EventStreamID: streamID,
			EventType: protocolevent.EventRunStarted, Run: run, OccurredAt: startedAt,
		})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{accepted, started})
		return err
	})
	if err != nil {
		return ProtocolRunLifecycleResult{}, err
	}
	return ProtocolRunLifecycleResult{
		Run: run, Events: unitResult.Events, NotifyError: unitResult.NotifyError,
	}, nil
}

// RecordStartedAgentRun commits the initial public snapshots for a Run that
// was atomically created with another domain aggregate (for example a Chat
// Message and Conversation CAS). Deterministic Event IDs make crash recovery
// safe: a retry cannot create a second accepted/started pair.
func (service *ProtocolRunLifecycleService) RecordStartedAgentRun(
	ctx context.Context,
	run AgentRun,
) (ProtocolRunLifecycleResult, error) {
	if service == nil || service.runs == nil || service.unit == nil || service.mapper == nil ||
		run.Status != "RUNNING" {
		return ProtocolRunLifecycleResult{}, ErrRunInvalid
	}
	acceptedID := uuid.NewSHA1(initialRunEventNamespace, []byte("accepted\x00"+run.ID)).String()
	startedID := uuid.NewSHA1(initialRunEventNamespace, []byte("started\x00"+run.ID)).String()
	acceptedAt := run.StartedAt.UTC()
	startedAt := service.now().UTC()
	if startedAt.Before(acceptedAt) {
		startedAt = acceptedAt
	}
	unitResult, err := service.unit.Execute(ctx, func(
		ctx context.Context,
		transaction *protocolevent.ProtocolTransaction,
	) error {
		streamID, err := transaction.EnsureRunEventStream(ctx, run.ID, protocolScope(run))
		if err != nil {
			return err
		}
		accepted, err := service.mapper.Map(RunLifecycleEventInput{
			EventID: acceptedID, EventStreamID: streamID,
			EventType: protocolevent.EventRunAccepted, Run: run, OccurredAt: acceptedAt,
		})
		if err != nil {
			return err
		}
		started, err := service.mapper.Map(RunLifecycleEventInput{
			EventID: startedID, EventStreamID: streamID,
			EventType: protocolevent.EventRunStarted, Run: run, OccurredAt: startedAt,
		})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{accepted, started})
		return err
	})
	if err != nil {
		return ProtocolRunLifecycleResult{}, err
	}
	return ProtocolRunLifecycleResult{
		Run: run, Events: unitResult.Events, NotifyError: unitResult.NotifyError,
	}, nil
}

func (service *ProtocolRunLifecycleService) TransitionAgentRun(
	ctx context.Context,
	input ProtocolRunTransitionInput,
) (ProtocolRunLifecycleResult, error) {
	if service == nil || service.runs == nil || service.unit == nil || service.mapper == nil {
		return ProtocolRunLifecycleResult{}, ErrRunInvalid
	}
	eventType, err := protocolTransitionEventType(
		input.Transition.ExpectedStatus, input.Transition.NewStatus,
	)
	if err != nil || !validProtocolTransitionError(input.Transition) {
		return ProtocolRunLifecycleResult{}, ErrRunInvalid
	}
	eventID, idErr := uuid.NewV7()
	if idErr != nil {
		return ProtocolRunLifecycleResult{}, idErr
	}
	occurredAt := service.now().UTC()
	var run AgentRun
	unitResult, err := service.unit.Execute(ctx, func(
		ctx context.Context,
		transaction *protocolevent.ProtocolTransaction,
	) error {
		tx, err := transaction.SQLTx()
		if err != nil {
			return err
		}
		run, err = service.runs.TransitionAgentRunInTransaction(
			ctx, tx, input.WorkspaceID, input.RunID, input.Transition,
		)
		if err != nil {
			return err
		}
		streamID, err := transaction.EnsureRunEventStream(ctx, run.ID, protocolScope(run))
		if err != nil {
			return err
		}
		event, err := service.mapper.Map(RunLifecycleEventInput{
			EventID: eventID.String(), EventStreamID: streamID,
			EventType: eventType, Run: run, OccurredAt: occurredAt,
			InteractionIDs: input.InteractionIDs, InteractionID: input.InteractionID,
		})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		return ProtocolRunLifecycleResult{}, err
	}
	return ProtocolRunLifecycleResult{
		Run: run, Events: unitResult.Events, NotifyError: unitResult.NotifyError,
	}, nil
}

func newProtocolRunEventIDs() (string, string, error) {
	accepted, err := uuid.NewV7()
	if err != nil {
		return "", "", err
	}
	started, err := uuid.NewV7()
	if err != nil {
		return "", "", err
	}
	return accepted.String(), started.String(), nil
}

func protocolScope(run AgentRun) protocolevent.RunScope {
	return protocolevent.RunScope{
		WorkspaceID: run.WorkspaceID, AgentID: run.AgentID,
		ConversationID: run.SessionID, RunID: run.ID,
	}
}

func protocolTransitionEventType(expected, next string) (string, error) {
	expected = strings.ToUpper(strings.TrimSpace(expected))
	next = strings.ToUpper(strings.TrimSpace(next))
	switch {
	case expected == "RUNNING" && next == "WAITING_CONFIRMATION":
		return protocolevent.EventRunWaiting, nil
	case expected == "WAITING_CONFIRMATION" && next == "RUNNING":
		return protocolevent.EventRunResumed, nil
	case expected == "RUNNING" && next == "SUCCEEDED":
		return protocolevent.EventRunCompleted, nil
	case expected == "RUNNING" && next == "FAILED":
		return protocolevent.EventRunFailed, nil
	case (expected == "RUNNING" || expected == "WAITING_CONFIRMATION") && next == "CANCELLED":
		return protocolevent.EventRunCancelled, nil
	default:
		return "", ErrRunInvalid
	}
}

func validProtocolTransitionError(transition RunTransition) bool {
	if strings.EqualFold(strings.TrimSpace(transition.NewStatus), "FAILED") {
		return stableProtocolErrorCode.MatchString(strings.TrimSpace(transition.ErrorCode))
	}
	return strings.TrimSpace(transition.ErrorCode) == ""
}

func runEventMatchesAgentStatus(eventType, agentStatus string) bool {
	agentStatus = strings.ToUpper(strings.TrimSpace(agentStatus))
	expected := map[string]string{
		protocolevent.EventRunAccepted:  "RUNNING",
		protocolevent.EventRunStarted:   "RUNNING",
		protocolevent.EventRunWaiting:   "WAITING_CONFIRMATION",
		protocolevent.EventRunResumed:   "RUNNING",
		protocolevent.EventRunCompleted: "SUCCEEDED",
		protocolevent.EventRunFailed:    "FAILED",
		protocolevent.EventRunCancelled: "CANCELLED",
	}[eventType]
	return expected != "" && agentStatus == expected
}

func protocolRunStatus(eventType string) protocolevent.RunStatus {
	return map[string]protocolevent.RunStatus{
		protocolevent.EventRunAccepted:  protocolevent.RunStatusAccepted,
		protocolevent.EventRunStarted:   protocolevent.RunStatusRunning,
		protocolevent.EventRunWaiting:   protocolevent.RunStatusWaitingInteraction,
		protocolevent.EventRunResumed:   protocolevent.RunStatusRunning,
		protocolevent.EventRunCompleted: protocolevent.RunStatusCompleted,
		protocolevent.EventRunFailed:    protocolevent.RunStatusFailed,
		protocolevent.EventRunCancelled: protocolevent.RunStatusCancelled,
	}[eventType]
}

func mapAgentRunSnapshot(
	run AgentRun,
	status protocolevent.RunStatus,
) (protocolevent.Run, error) {
	if status == "" || run.ID == "" || run.SessionID == "" || run.AgentID == "" ||
		run.StartedAt.IsZero() {
		return protocolevent.Run{}, ErrRunInvalid
	}
	result := protocolevent.Run{
		ID: run.ID, ConversationID: run.SessionID, AgentID: run.AgentID,
		Status: status, Trigger: protocolRunTrigger(run.TriggerType),
		StartedAt: run.StartedAt.UTC(),
	}
	if status == protocolevent.RunStatusCompleted || status == protocolevent.RunStatusFailed ||
		status == protocolevent.RunStatusCancelled {
		if run.FinishedAt == nil {
			return protocolevent.Run{}, ErrRunInvalid
		}
		finished := run.FinishedAt.UTC()
		result.CompletedAt = &finished
	}
	if status == protocolevent.RunStatusFailed {
		if !stableProtocolErrorCode.MatchString(run.ErrorCode) {
			return protocolevent.Run{}, ErrRunInvalid
		}
		result.Error = &protocolevent.ProtocolError{
			Code: run.ErrorCode, Message: "Run failed", Retryable: false,
			Details: json.RawMessage(nil),
		}
	}
	return result, nil
}

func protocolRunTrigger(value string) protocolevent.RunTrigger {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "CHAT", "MESSAGE":
		return protocolevent.RunTriggerMessage
	case "WORKFLOW":
		return protocolevent.RunTriggerWorkflow
	case "API":
		return protocolevent.RunTriggerAPI
	default:
		return protocolevent.RunTriggerSystem
	}
}
