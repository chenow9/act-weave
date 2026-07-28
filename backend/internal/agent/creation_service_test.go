package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/audit"
	"actweave/backend/internal/storedobject"
	"github.com/google/uuid"
)

func TestCreateWithEligiblePreviewPromotesAtomically(t *testing.T) {
	repository, db := newAgentServiceTest(t)
	objects := newMemoryPromptObjects(db)
	service, err := NewPromptService(repository, objects,
		staticModelSnapshot{value: json.RawMessage(`{"model":"m","status":"VERIFIED"}`)},
		PromptGeneratorFunc(func(context.Context, PromptGenerationRequest) (string, error) {
			return "Promoted system prompt", nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	run, output, err := service.RunCreatePreview(context.Background(), serviceWorkspaceID, serviceModelID,
		"Draft", "trace-create-eligible", serviceOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	storeRepo, err := storedobject.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	creation, err := NewCreationService(repository, storeRepo)
	if err != nil {
		t.Fatal(err)
	}
	agentID := uuid.Must(uuid.NewV7()).String()
	revisionID := uuid.Must(uuid.NewV7()).String()
	result, err := creation.Create(context.Background(), NewAgent{
		ID: agentID, WorkspaceID: serviceWorkspaceID, Name: "Linked Agent",
		ModelConfigID: serviceModelID, InitialRevisionID: revisionID,
		InitialPrompt: output, PromptSource: PromptSourceManual, CreatedBy: serviceOwnerID,
	}, run.ID)
	if err != nil {
		t.Fatalf("create with preview: %v", err)
	}
	if !result.SourceLinked || result.Revision.Source != PromptSourceAIAssisted ||
		result.SourceReason != SourceLinkReasonLinked {
		t.Fatalf("unexpected link result: %+v revision=%+v", result, result.Revision)
	}
	var retention string
	if err := db.QueryRow(`SELECT retention_mode FROM stored_objects WHERE id=$1`, *run.OutputObjectID).
		Scan(&retention); err != nil || retention != "PERMANENT" {
		t.Fatalf("output retention=%s err=%v", retention, err)
	}
	var promotedAgent *string
	var promotedAt *time.Time
	if err := db.QueryRow(`
		SELECT agent_id::text, promoted_at FROM prompt_runs WHERE id=$1
	`, run.ID).Scan(&promotedAgent, &promotedAt); err != nil || promotedAgent == nil || promotedAt == nil {
		t.Fatalf("run promotion: agent=%v at=%v err=%v", promotedAgent, promotedAt, err)
	}
}

func TestCreateWithIneligibleSourceFallsBackToManual(t *testing.T) {
	repository, db := newAgentServiceTest(t)
	storeRepo, err := storedobject.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	creation, err := NewCreationService(repository, storeRepo)
	if err != nil {
		t.Fatal(err)
	}
	missingRun := uuid.Must(uuid.NewV7()).String()
	result, err := creation.Create(context.Background(), NewAgent{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: serviceWorkspaceID, Name: "Manual Fallback",
		ModelConfigID: serviceModelID, InitialRevisionID: uuid.Must(uuid.NewV7()).String(),
		InitialPrompt: "Manual prompt body", CreatedBy: serviceOwnerID,
	}, missingRun)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if result.SourceLinked || result.Revision.Source != PromptSourceManual ||
		result.SourceReason != SourceLinkReasonNotEligible {
		t.Fatalf("expected manual not-eligible: %+v", result)
	}
}

func TestCreateSecondConsumeIsManualNotLinked(t *testing.T) {
	repository, db := newAgentServiceTest(t)
	objects := newMemoryPromptObjects(db)
	service, err := NewPromptService(repository, objects,
		staticModelSnapshot{value: json.RawMessage(`{"model":"m"}`)},
		PromptGeneratorFunc(func(context.Context, PromptGenerationRequest) (string, error) {
			return "Shared preview output", nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	run, output, err := service.RunCreatePreview(context.Background(), serviceWorkspaceID, serviceModelID,
		"Draft", "trace-second", serviceOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	storeRepo, _ := storedobject.NewRepository(db)
	auditor := &txRecordingAuditor{}
	creation, _ := NewCreationService(repository, storeRepo)
	creation = creation.WithAuditor(auditor)
	first, err := creation.Create(context.Background(), NewAgent{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: serviceWorkspaceID, Name: "First Link",
		ModelConfigID: serviceModelID, InitialRevisionID: uuid.Must(uuid.NewV7()).String(),
		InitialPrompt: output, CreatedBy: serviceOwnerID,
	}, run.ID)
	if err != nil || !first.SourceLinked {
		t.Fatalf("first create: %+v err=%v", first, err)
	}
	if len(auditor.events) != 1 || auditor.events[0].Action != audit.ActionAgentPromptPreviewPromoted {
		t.Fatalf("expected promotion audit, got %+v", auditor.events)
	}
	if auditor.events[0].ActorDisplay != "Workspace member" {
		t.Fatalf("ActorDisplay=%q", auditor.events[0].ActorDisplay)
	}
	second, err := creation.Create(context.Background(), NewAgent{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: serviceWorkspaceID, Name: "Second Link",
		ModelConfigID: serviceModelID, InitialRevisionID: uuid.Must(uuid.NewV7()).String(),
		InitialPrompt: output, CreatedBy: serviceOwnerID,
	}, run.ID)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if second.SourceLinked || second.Revision.Source != PromptSourceManual ||
		second.SourceReason != SourceLinkReasonNotEligible {
		t.Fatalf("second should be manual not-eligible: %+v", second)
	}
}

type txRecordingAuditor struct {
	events []audit.ManagementEventInput
}

func (a *txRecordingAuditor) RecordInTransaction(
	_ context.Context, _ *sql.Tx, input audit.ManagementEventInput,
) (audit.Event, error) {
	a.events = append(a.events, input)
	return audit.Event{ID: input.EventID, Action: input.Action}, nil
}

// builderTxAuditor exercises real audit.Builder (fails closed if ActorDisplay missing).
type builderTxAuditor struct {
	builder *audit.Builder
	events  []audit.ManagementEventInput
}

func (a *builderTxAuditor) RecordInTransaction(
	_ context.Context, _ *sql.Tx, input audit.ManagementEventInput,
) (audit.Event, error) {
	a.events = append(a.events, input)
	return a.builder.Build(audit.BuildInput{
		ID: input.EventID, OccurredAt: input.OccurredAt, WorkspaceID: input.WorkspaceID,
		ActorType: input.ActorType, ActorID: input.ActorID, ActorDisplay: input.ActorDisplay,
		Action: input.Action, ResourceType: input.ResourceType, ResourceID: input.ResourceID,
		Result: input.Result, Metadata: input.Metadata,
	})
}

func TestCreateEligiblePreviewPromotionPassesRealBuilder(t *testing.T) {
	repository, db := newAgentServiceTest(t)
	objects := newMemoryPromptObjects(db)
	service, err := NewPromptService(repository, objects,
		staticModelSnapshot{value: json.RawMessage(`{"model":"m"}`)},
		PromptGeneratorFunc(func(context.Context, PromptGenerationRequest) (string, error) {
			return "Builder-audited promote output", nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	run, output, err := service.RunCreatePreview(context.Background(), serviceWorkspaceID, serviceModelID,
		"Draft", "trace-builder-promote", serviceOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	storeRepo, err := storedobject.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	builder, err := audit.NewBuilder(2048)
	if err != nil {
		t.Fatal(err)
	}
	auditor := &builderTxAuditor{builder: builder}
	creation, err := NewCreationService(repository, storeRepo)
	if err != nil {
		t.Fatal(err)
	}
	creation = creation.WithAuditor(auditor)
	result, err := creation.Create(context.Background(), NewAgent{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: serviceWorkspaceID, Name: "Builder Promote",
		ModelConfigID: serviceModelID, InitialRevisionID: uuid.Must(uuid.NewV7()).String(),
		InitialPrompt: output, CreatedBy: serviceOwnerID,
	}, run.ID)
	if err != nil {
		t.Fatalf("create with real builder audit: %v", err)
	}
	if !result.SourceLinked {
		t.Fatalf("expected linked create: %+v", result)
	}
	if len(auditor.events) != 1 || auditor.events[0].ActorDisplay != "Workspace member" {
		t.Fatalf("promotion audit events=%+v", auditor.events)
	}
}

func TestCreateHashMismatchDoesNotCreate(t *testing.T) {
	repository, db := newAgentServiceTest(t)
	objects := newMemoryPromptObjects(db)
	service, err := NewPromptService(repository, objects,
		staticModelSnapshot{value: json.RawMessage(`{"model":"m"}`)},
		PromptGeneratorFunc(func(context.Context, PromptGenerationRequest) (string, error) {
			return "Exact model output", nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := service.RunCreatePreview(context.Background(), serviceWorkspaceID, serviceModelID,
		"Draft", "trace-mismatch", serviceOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	storeRepo, _ := storedobject.NewRepository(db)
	creation, _ := NewCreationService(repository, storeRepo)
	_, err = creation.Create(context.Background(), NewAgent{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: serviceWorkspaceID, Name: "Mismatch Agent",
		ModelConfigID: serviceModelID, InitialRevisionID: uuid.Must(uuid.NewV7()).String(),
		InitialPrompt: "Different from model output", CreatedBy: serviceOwnerID,
	}, run.ID)
	if !errors.Is(err, ErrPromptOutputMismatch) {
		t.Fatalf("error=%v want ErrPromptOutputMismatch", err)
	}
	var agents int
	if err := db.QueryRow(`SELECT count(*) FROM agents WHERE workspace_id=$1`, serviceWorkspaceID).Scan(&agents); err != nil || agents != 0 {
		t.Fatalf("agent count after mismatch=%d err=%v", agents, err)
	}
}
