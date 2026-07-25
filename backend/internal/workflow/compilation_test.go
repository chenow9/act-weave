package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/workflowcompiler"
)

func TestCompilationPersistsCurrentImmutableCompilerOutput(t *testing.T) {
	repository, db := newDraftRepositoryTest(t)
	input := validWorkflowCreateInput()
	input.Graph = validCompilationGraph(false)
	_, draft, err := repository.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewCompilationService(repository, workflowcompiler.New())
	if err != nil {
		t.Fatal(err)
	}

	compiled, err := service.Compile(context.Background(), draftWorkspaceID, draftCapabilityID, draftOwnerID)
	if err != nil {
		t.Fatalf("compile current draft: %v", err)
	}
	if compiled.Status != "VALID" || compiled.DraftID != draft.ID ||
		compiled.DraftVersion != draft.DraftVersion || compiled.GraphHash != draft.GraphHash ||
		compiled.CompilerVersion != workflowcompiler.Version || len(compiled.PlanHash) != 64 ||
		string(compiled.Issues) != `[]` {
		t.Fatalf("unexpected persisted compilation: %+v", compiled)
	}
	var spec domain.ExecutableWorkflowSpec
	var plan domain.CompiledExecutionPlan
	if err := json.Unmarshal(compiled.Spec, &spec); err != nil || len(spec.Nodes) != 2 {
		t.Fatalf("unexpected persisted spec: %s err=%v", compiled.Spec, err)
	}
	if err := json.Unmarshal(compiled.Plan, &plan); err != nil || len(plan.Nodes) != 2 {
		t.Fatalf("unexpected persisted plan: %s err=%v", compiled.Plan, err)
	}
	_, reproduciblePlanHash, err := canonicalJSON(compiled.Plan, "object")
	if err != nil || reproduciblePlanHash != compiled.PlanHash {
		t.Fatalf("plan hash is not reproducible: got=%s want=%s err=%v", reproduciblePlanHash, compiled.PlanHash, err)
	}
	current, err := repository.GetCurrentValidCompilation(
		context.Background(), draftWorkspaceID, draftCapabilityID, compiled.ID,
	)
	if err != nil || current.ID != compiled.ID {
		t.Fatalf("resolve current valid compilation: %+v err=%v", current, err)
	}
	if _, err := repository.GetCompilation(
		context.Background(), draftOtherWorkspaceID, draftCapabilityID, compiled.ID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-workspace compilation miss, got %v", err)
	}
	if _, err := db.Exec(`UPDATE workflow_compilations SET plan='{}' WHERE id=$1`, compiled.ID); err == nil {
		t.Fatal("expected persisted compilation to be immutable")
	}
	var latestID string
	if err := db.QueryRow(`SELECT latest_compilation_id FROM workflows WHERE capability_id=$1`, draftCapabilityID).Scan(&latestID); err != nil || latestID != compiled.ID {
		t.Fatalf("latest compilation pointer mismatch: id=%s err=%v", latestID, err)
	}

	updatedDraft, err := repository.UpdateDraft(context.Background(), draftWorkspaceID, draftCapabilityID, DraftUpdate{
		SchemaVersion: "workflow.graph.v1", Graph: validCompilationGraph(true), UpdatedBy: draftOwnerID,
		ExpectedDraftVersion: draft.DraftVersion, ExpectedLockVersion: draft.LockVersion,
	})
	if err != nil || updatedDraft.DraftVersion != draft.DraftVersion+1 {
		t.Fatalf("update draft after compilation: %+v err=%v", updatedDraft, err)
	}
	if _, err := repository.GetCurrentValidCompilation(
		context.Background(), draftWorkspaceID, draftCapabilityID, compiled.ID,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected old compilation to become stale, got %v", err)
	}
	if stored, err := repository.GetCompilation(
		context.Background(), draftWorkspaceID, draftCapabilityID, compiled.ID,
	); err != nil || stored.ID != compiled.ID {
		t.Fatalf("immutable historical compilation was lost: %+v err=%v", stored, err)
	}
}

func TestCompilationPersistsInvalidIssuesWithoutValidatingForLaterUse(t *testing.T) {
	repository, _ := newDraftRepositoryTest(t)
	_, _, err := repository.Create(context.Background(), validWorkflowCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewCompilationService(repository, workflowcompiler.New())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := service.Compile(context.Background(), draftWorkspaceID, draftCapabilityID, draftOwnerID)
	if err != nil {
		t.Fatalf("persist invalid compilation: %v", err)
	}
	if compiled.Status != "INVALID" || string(compiled.Spec) != `{}` ||
		string(compiled.Plan) != `{}` || string(compiled.Issues) == `[]` {
		t.Fatalf("unexpected invalid compilation: %+v", compiled)
	}
	if _, err := repository.GetCurrentValidCompilation(
		context.Background(), draftWorkspaceID, draftCapabilityID, compiled.ID,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid compilation must not enter later flow, got %v", err)
	}
}

func TestCompilationRunsCompilerOutsideTransactionAndRejectsStaleResult(t *testing.T) {
	repository, db := newDraftRepositoryTest(t)
	input := validWorkflowCreateInput()
	input.Graph = validCompilationGraph(false)
	_, draft, err := repository.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	compiler := &blockingCompilationCompiler{
		delegate: workflowcompiler.New(),
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	service, err := NewCompilationService(repository, compiler)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := service.Compile(context.Background(), draftWorkspaceID, draftCapabilityID, draftOwnerID)
		result <- err
	}()
	select {
	case <-compiler.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("compiler did not start")
	}
	released := false
	defer func() {
		if !released {
			close(compiler.release)
		}
	}()

	updateContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := repository.UpdateDraft(updateContext, draftWorkspaceID, draftCapabilityID, DraftUpdate{
		SchemaVersion: "workflow.graph.v1", Graph: validCompilationGraph(true), UpdatedBy: draftOwnerID,
		ExpectedDraftVersion: draft.DraftVersion, ExpectedLockVersion: draft.LockVersion,
	}); err != nil {
		t.Fatalf("draft update blocked while compiler ran outside transaction: %v", err)
	}
	close(compiler.release)
	released = true
	select {
	case err := <-result:
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("expected stale compilation conflict, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stale compilation did not finish")
	}
	var compilationCount int
	if err := db.QueryRow(`SELECT count(*) FROM workflow_compilations WHERE capability_id=$1`, draftCapabilityID).Scan(&compilationCount); err != nil {
		t.Fatal(err)
	}
	if compilationCount != 0 {
		t.Fatalf("stale compiler output was persisted: %d", compilationCount)
	}
	var hasLatest bool
	if err := db.QueryRow(`SELECT latest_compilation_id IS NOT NULL FROM workflows WHERE capability_id=$1`, draftCapabilityID).Scan(&hasLatest); err != nil {
		t.Fatal(err)
	}
	if hasLatest {
		t.Fatal("stale compiler output changed latest compilation pointer")
	}
}

type blockingCompilationCompiler struct {
	delegate workflowcompiler.Compiler
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (*blockingCompilationCompiler) Version() string { return workflowcompiler.Version }

func (c *blockingCompilationCompiler) Compile(
	workflowID string,
	draftVersion string,
	draft domain.WorkflowGraphDraft,
) domain.WorkflowCompilation {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return c.delegate.Compile(workflowID, draftVersion, draft)
}

func validCompilationGraph(edited bool) json.RawMessage {
	if edited {
		return json.RawMessage(`{
			"schemaVersion":"workflow.graph.v1",
			"nodes":[{"id":"start","type":"Start"},{"id":"end","type":"End"}],
			"edges":[{"id":"edge-1","sourceNodeId":"start","targetNodeId":"end"}],
			"ui":{"edited":true}
		}`)
	}
	return json.RawMessage(`{
		"schemaVersion":"workflow.graph.v1",
		"nodes":[{"id":"start","type":"Start"},{"id":"end","type":"End"}],
		"edges":[{"id":"edge-1","sourceNodeId":"start","targetNodeId":"end"}]
	}`)
}
