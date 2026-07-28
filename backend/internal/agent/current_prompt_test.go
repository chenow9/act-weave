package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"actweave/backend/internal/audit"
)

func TestCurrentPromptQuerySuccessRequiresAudit(t *testing.T) {
	repository, _ := newAgentServiceTest(t)
	created, initial, err := repository.Create(context.Background(), NewAgent{
		ID: serviceAgentID, WorkspaceID: serviceWorkspaceID, Name: "Current Prompt Agent",
		ModelConfigID: serviceModelID, InitialRevisionID: serviceRevisionID,
		InitialPrompt: "canary-CURRENT-PROMPT-BODY", CreatedBy: serviceOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	auditor := &recordingPromptAuditor{}
	query, err := NewCurrentPromptQuery(repository, auditor)
	if err != nil {
		t.Fatal(err)
	}
	current, err := query.GetCurrent(context.Background(), serviceWorkspaceID, created.ID, serviceOwnerID, "prompt.reader")
	if err != nil {
		t.Fatalf("GetCurrent: %v", err)
	}
	if current.SystemPrompt != "canary-CURRENT-PROMPT-BODY" || current.RevisionID != initial.ID ||
		current.RevisionNo != 1 || current.AgentID != created.ID {
		t.Fatalf("unexpected current prompt: %+v", current)
	}
	if len(auditor.events) != 1 || auditor.events[0].Result != "SUCCESS" {
		t.Fatalf("audit events=%+v", auditor.events)
	}
	if auditor.events[0].ActorDisplay != "prompt.reader" {
		t.Fatalf("ActorDisplay=%q", auditor.events[0].ActorDisplay)
	}
	metaJSON, _ := json.Marshal(auditor.events[0].Metadata)
	if strings.Contains(string(metaJSON), "canary-CURRENT-PROMPT-BODY") {
		t.Fatalf("prompt body leaked into audit metadata: %s", metaJSON)
	}
}

func TestCurrentPromptQueryRealBuilderRequiresActorDisplay(t *testing.T) {
	repository, _ := newAgentServiceTest(t)
	created, _, err := repository.Create(context.Background(), NewAgent{
		ID: serviceAgentID, WorkspaceID: serviceWorkspaceID, Name: "Builder Audit Agent",
		ModelConfigID: serviceModelID, InitialRevisionID: serviceRevisionID,
		InitialPrompt: "builder-path-body", CreatedBy: serviceOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	builder, err := audit.NewBuilder(2048)
	if err != nil {
		t.Fatal(err)
	}
	auditor := &builderPromptAuditor{builder: builder}
	query, err := NewCurrentPromptQuery(repository, auditor)
	if err != nil {
		t.Fatal(err)
	}
	// Empty display falls back to "Workspace member" and must pass real Builder.
	current, err := query.GetCurrent(context.Background(), serviceWorkspaceID, created.ID, serviceOwnerID, "")
	if err != nil {
		t.Fatalf("GetCurrent with real builder: %v", err)
	}
	if current.SystemPrompt != "builder-path-body" {
		t.Fatalf("unexpected body: %+v", current)
	}
	if len(auditor.events) != 1 || auditor.events[0].ActorDisplay != "Workspace member" {
		t.Fatalf("audit events=%+v", auditor.events)
	}
}

func TestCurrentPromptQueryAuditFailureDoesNotReturnBody(t *testing.T) {
	repository, _ := newAgentServiceTest(t)
	created, _, err := repository.Create(context.Background(), NewAgent{
		ID: serviceAgentID, WorkspaceID: serviceWorkspaceID, Name: "Audit Fail Agent",
		ModelConfigID: serviceModelID, InitialRevisionID: serviceRevisionID,
		InitialPrompt: "secret-body-must-not-escape", CreatedBy: serviceOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	auditor := &recordingPromptAuditor{err: errors.New("audit store down")}
	query, err := NewCurrentPromptQuery(repository, auditor)
	if err != nil {
		t.Fatal(err)
	}
	current, err := query.GetCurrent(context.Background(), serviceWorkspaceID, created.ID, serviceOwnerID, "prompt.reader")
	if !errors.Is(err, ErrPromptAuditUnavailable) {
		t.Fatalf("error=%v want ErrPromptAuditUnavailable", err)
	}
	if current.SystemPrompt != "" {
		t.Fatalf("body escaped on audit failure: %+v", current)
	}
}

func TestCurrentPromptQueryMissingAgentDeniesWithoutBody(t *testing.T) {
	repository, _ := newAgentServiceTest(t)
	auditor := &recordingPromptAuditor{}
	query, err := NewCurrentPromptQuery(repository, auditor)
	if err != nil {
		t.Fatal(err)
	}
	_, err = query.GetCurrent(context.Background(), serviceWorkspaceID, serviceAgentID, serviceOwnerID, "prompt.reader")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error=%v", err)
	}
	if len(auditor.events) != 1 || auditor.events[0].Result != "DENIED" {
		t.Fatalf("expected denied audit, got %+v", auditor.events)
	}
}

type recordingPromptAuditor struct {
	mu     sync.Mutex
	events []audit.ManagementEventInput
	err    error
}

func (a *recordingPromptAuditor) Record(_ context.Context, input audit.ManagementEventInput) (audit.Event, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, input)
	if a.err != nil {
		return audit.Event{}, a.err
	}
	return audit.Event{ID: input.EventID, Action: input.Action, Result: input.Result}, nil
}

// builderPromptAuditor exercises the real audit.Builder path (catches missing ActorDisplay).
type builderPromptAuditor struct {
	builder *audit.Builder
	events  []audit.ManagementEventInput
}

func (a *builderPromptAuditor) Record(_ context.Context, input audit.ManagementEventInput) (audit.Event, error) {
	a.events = append(a.events, input)
	return a.builder.Build(audit.BuildInput{
		ID: input.EventID, OccurredAt: input.OccurredAt, WorkspaceID: input.WorkspaceID,
		ActorType: input.ActorType, ActorID: input.ActorID, ActorDisplay: input.ActorDisplay,
		Action: input.Action, ResourceType: input.ResourceType, ResourceID: input.ResourceID,
		Result: input.Result, Metadata: input.Metadata,
	})
}
