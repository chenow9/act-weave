package execution_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/execution"
)

func TestContinuationRecoveryRedrivesDispatchWithoutDuplicateSideEffect(t *testing.T) {
	ctx := context.Background()
	db, runs, confirmations := newConfirmationResumeFixture(t)
	resolved, request := resumeToolResolution()
	requestSnapshot, resolvedSnapshot, err := execution.BuildToolConfirmationResumeSnapshots(request, resolved)
	if err != nil {
		t.Fatal(err)
	}
	var sideEffects atomic.Int32
	var resolverCalls atomic.Int32
	pipeline := newResumeInvocationPipeline(t, confirmations, &resolverCalls, &sideEffects)
	toolExecutor, err := execution.NewToolConfirmationResumeExecutor(pipeline)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := execution.NewConfirmationResumeRegistry(toolExecutor)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, _ := execution.NewConfirmationResumeRepository(db)
	resumes, err := execution.NewConfirmationResumeService(checkpoints, confirmations, runs, registry)
	if err != nil {
		t.Fatal(err)
	}
	decisions, err := execution.NewInteractionDecisionService(confirmations, resumes)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := execution.NewContinuationRecoveryService(confirmations, resumes, decisions)
	if err != nil {
		t.Fatal(err)
	}

	run, err := runs.GetAgentRun(ctx, executionWorkspaceID, executionAgentRunID)
	if err != nil {
		t.Fatal(err)
	}
	decision := resumeDecision(t, request.Input, request.ReleaseID, invocationConnectionID, "PRODUCTION", true)
	prepared, err := resumes.Prepare(ctx, execution.PrepareConfirmationResumeInput{
		Confirmation: execution.RequestExecutionConfirmationInput{
			ID: resumeConfirmationID, WorkspaceID: executionWorkspaceID,
			RunID: executionAgentRunID, NodeID: "recovery-tool",
			TargetItemID: resumeInvocationID,
			ReleaseID:    invocationReleaseID, ConnectionID: invocationConnectionID,
			PlanHash: executionPlanHash, RequestedBy: executionOwnerID, Decision: decision,
		},
		Kind:                  execution.ResumeKindTool,
		SnapshotSchemaVersion: execution.ConfirmationResumeSnapshotVersion,
		RequestSnapshot:       requestSnapshot, ResolvedSnapshot: resolvedSnapshot,
		Input: request.Input, ExpectedRunLockVersion: run.LockVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Approve via Confirm (domain) leaving checkpoint PENDING — simulates post-decision
	// commit before Dispatch/EnqueueContinue (process exit after decision).
	if _, err := confirmations.Confirm(ctx, execution.ConfirmExecutionConfirmationInput{
		WorkspaceID: executionWorkspaceID, ConfirmationID: resumeConfirmationID,
		ActorID: executionOwnerID, ResumeToken: prepared.Requested.ResumeToken,
		RunID: executionAgentRunID, TargetItemID: resumeInvocationID,
		ReleaseID: invocationReleaseID, ConnectionID: invocationConnectionID,
		PlanHash: executionPlanHash, Input: request.Input, ExpectedLockVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if sideEffects.Load() != 0 {
		t.Fatal("side effect must not run before recovery dispatch")
	}

	pending, err := recovery.ListApprovedPendingDispatch(ctx, 10)
	if err != nil || len(pending) != 1 || pending[0].ConfirmationID != resumeConfirmationID {
		t.Fatalf("pending dispatch list=%+v err=%v", pending, err)
	}

	// First recovery drives the side effect once.
	first, err := recovery.RecoverDispatch(ctx, executionWorkspaceID, resumeConfirmationID)
	if err != nil || first.DispatchError != nil || first.ResumeStatus != execution.ResumeStatusSucceeded {
		t.Fatalf("first recovery=%+v err=%v", first, err)
	}
	if sideEffects.Load() != 1 {
		t.Fatalf("side effects=%d want=1", sideEffects.Load())
	}

	// Second recovery must not re-execute (SUCCEEDED cached).
	second, err := recovery.RecoverDispatch(ctx, executionWorkspaceID, resumeConfirmationID)
	if err != nil || !second.Cached || second.ResumeStatus != execution.ResumeStatusSucceeded {
		t.Fatalf("second recovery=%+v err=%v", second, err)
	}
	if sideEffects.Load() != 1 || resolverCalls.Load() != 0 {
		t.Fatalf("duplicate side effect: effects=%d resolver=%d", sideEffects.Load(), resolverCalls.Load())
	}

	// Runtime-continue candidates: SUCCEEDED + RUNNING.
	awaiting, err := recovery.ListSucceededAwaitingRuntimeContinue(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range awaiting {
		if item.ConfirmationID == resumeConfirmationID {
			found = true
			_ = execution.DecisionResultFromRecovery(item)
		}
	}
	if !found {
		t.Fatalf("expected runtime-continue candidate after successful dispatch: %+v", awaiting)
	}

	// Multi-replica claim: only one lease wins; second fails until lease expires.
	firstClaim, err := recovery.ClaimRuntimeContinue(
		ctx, executionWorkspaceID, resumeConfirmationID, executionAgentRunID,
		execution.DefaultRuntimeContinueLease,
	)
	if err != nil || firstClaim.ClaimID == "" {
		t.Fatalf("first claim=%+v err=%v", firstClaim, err)
	}
	// Default lease must cover chatruntime ContinueTimeout (5m).
	if execution.DefaultRuntimeContinueLease < 5*time.Minute {
		t.Fatalf("DefaultRuntimeContinueLease=%s must cover 5m runtime timeout",
			execution.DefaultRuntimeContinueLease)
	}
	if firstClaim.ClaimExpiresAt.Before(time.Now().UTC().Add(5 * time.Minute).Add(-time.Second)) {
		t.Fatalf("claim expiry %s does not cover 5m runtime window", firstClaim.ClaimExpiresAt)
	}
	_, err = recovery.ClaimRuntimeContinue(
		ctx, executionWorkspaceID, resumeConfirmationID, executionAgentRunID,
		execution.DefaultRuntimeContinueLease,
	)
	if !errors.Is(err, execution.ErrRuntimeContinueNotClaimed) {
		t.Fatalf("second claim must lose: err=%v", err)
	}
	// Renew extends the owned lease; wrong claim id is rejected.
	renewed, err := recovery.RenewRuntimeContinue(
		ctx, resumeConfirmationID, firstClaim.ClaimID, execution.DefaultRuntimeContinueLease,
	)
	if err != nil || renewed.ClaimID != firstClaim.ClaimID {
		t.Fatalf("renew=%+v err=%v", renewed, err)
	}
	if !renewed.ClaimExpiresAt.After(firstClaim.ClaimExpiresAt.Add(-time.Second)) {
		t.Fatalf("renew must not shrink expiry: before=%s after=%s",
			firstClaim.ClaimExpiresAt, renewed.ClaimExpiresAt)
	}
	_, err = recovery.RenewRuntimeContinue(
		ctx, resumeConfirmationID, "00000000-0000-7000-8000-000000000099",
		execution.DefaultRuntimeContinueLease,
	)
	if !errors.Is(err, execution.ErrRuntimeContinueNotClaimed) {
		t.Fatalf("renew with foreign claim must fail: err=%v", err)
	}
	// Active lease hides candidate from the list.
	awaiting, err = recovery.ListSucceededAwaitingRuntimeContinue(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range awaiting {
		if item.ConfirmationID == resumeConfirmationID {
			t.Fatal("claimed continuation must not appear in list")
		}
	}
	// Complete releases the lease so recovery can re-drive.
	if err := recovery.CompleteRuntimeContinue(ctx, resumeConfirmationID, firstClaim.ClaimID); err != nil {
		t.Fatalf("complete: %v", err)
	}
	// Foreign complete is a no-claim error.
	if err := recovery.CompleteRuntimeContinue(
		ctx, resumeConfirmationID, firstClaim.ClaimID,
	); !errors.Is(err, execution.ErrRuntimeContinueNotClaimed) {
		t.Fatalf("second complete must fail: err=%v", err)
	}
	awaiting, err = recovery.ListSucceededAwaitingRuntimeContinue(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	foundAfterComplete := false
	for _, item := range awaiting {
		if item.ConfirmationID == resumeConfirmationID {
			foundAfterComplete = true
		}
	}
	if !foundAfterComplete {
		t.Fatal("completed lease must reappear as runtime-continue candidate")
	}
	// After complete, a new claim succeeds.
	secondClaim, err := recovery.ClaimRuntimeContinue(
		ctx, executionWorkspaceID, resumeConfirmationID, executionAgentRunID,
		execution.DefaultRuntimeContinueLease,
	)
	if err != nil || secondClaim.ClaimID == "" || secondClaim.ClaimID == firstClaim.ClaimID {
		t.Fatalf("reclaim after complete=%+v err=%v first=%s", secondClaim, err, firstClaim.ClaimID)
	}
}

func TestWaitingApprovalMetricsSQLIsValid(t *testing.T) {
	// Regression: observeWaitingApprovals used invalid `NOW() AT MIN(...)`.
	ctx := context.Background()
	db, _, _ := newConfirmationResumeFixture(t)
	var count int64
	var maxAgeSeconds float64
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(EXTRACT(EPOCH FROM (NOW() - MIN(ec.created_at))), 0)
		FROM execution_confirmations ec
		WHERE ec.status = 'PENDING'
	`).Scan(&count, &maxAgeSeconds)
	if err != nil {
		t.Fatalf("waiting approval metrics SQL must parse and run: %v", err)
	}
	if maxAgeSeconds < 0 {
		t.Fatalf("age must be non-negative: %v", maxAgeSeconds)
	}
	// Credential age query used by recovery worker observe path.
	var credAge float64
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(EXTRACT(EPOCH FROM (NOW() - MIN(last_used_at))), 0)
		FROM agent_access_credentials
		WHERE revoked_at IS NULL
		  AND last_used_at IS NOT NULL
		  AND (expires_at IS NULL OR expires_at > NOW())
	`).Scan(&credAge); err != nil {
		t.Fatalf("credential last-used age SQL: %v", err)
	}
}

func TestContinuationRecoveryExpiredClaimIsReclaimed(t *testing.T) {
	ctx := context.Background()
	db, runs, confirmations := newConfirmationResumeFixture(t)
	resolved, request := resumeToolResolution()
	requestSnapshot, resolvedSnapshot, err := execution.BuildToolConfirmationResumeSnapshots(request, resolved)
	if err != nil {
		t.Fatal(err)
	}
	var sideEffects atomic.Int32
	var resolverCalls atomic.Int32
	pipeline := newResumeInvocationPipeline(t, confirmations, &resolverCalls, &sideEffects)
	toolExecutor, _ := execution.NewToolConfirmationResumeExecutor(pipeline)
	registry, _ := execution.NewConfirmationResumeRegistry(toolExecutor)
	checkpoints, _ := execution.NewConfirmationResumeRepository(db)
	resumes, _ := execution.NewConfirmationResumeService(checkpoints, confirmations, runs, registry)
	decisions, _ := execution.NewInteractionDecisionService(confirmations, resumes)
	recovery, _ := execution.NewContinuationRecoveryService(confirmations, resumes, decisions)

	run, _ := runs.GetAgentRun(ctx, executionWorkspaceID, executionAgentRunID)
	decision := resumeDecision(t, request.Input, request.ReleaseID, invocationConnectionID, "PRODUCTION", true)
	prepared, err := resumes.Prepare(ctx, execution.PrepareConfirmationResumeInput{
		Confirmation: execution.RequestExecutionConfirmationInput{
			ID: resumeConfirmationID, WorkspaceID: executionWorkspaceID,
			RunID: executionAgentRunID, NodeID: "expired-claim",
			TargetItemID: resumeInvocationID,
			ReleaseID:    invocationReleaseID, ConnectionID: invocationConnectionID,
			PlanHash: executionPlanHash, RequestedBy: executionOwnerID, Decision: decision,
		},
		Kind: execution.ResumeKindTool, SnapshotSchemaVersion: execution.ConfirmationResumeSnapshotVersion,
		RequestSnapshot: requestSnapshot, ResolvedSnapshot: resolvedSnapshot, Input: request.Input,
		ExpectedRunLockVersion: run.LockVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := confirmations.Confirm(ctx, execution.ConfirmExecutionConfirmationInput{
		WorkspaceID: executionWorkspaceID, ConfirmationID: resumeConfirmationID,
		ActorID: executionOwnerID, ResumeToken: prepared.Requested.ResumeToken,
		RunID: executionAgentRunID, TargetItemID: resumeInvocationID,
		ReleaseID: invocationReleaseID, ConnectionID: invocationConnectionID,
		PlanHash: executionPlanHash, Input: request.Input, ExpectedLockVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate crash after CLAIMED lease without EXECUTING marker.
	if _, err := db.Exec(`
		UPDATE confirmation_resume_checkpoints
		SET status='CLAIMED', claim_id=$2,
		    claim_expires_at=$3, lock_version=lock_version+1
		WHERE confirmation_id=$1 AND status='PENDING'
	`, resumeConfirmationID, resumeCrashedClaimID, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}

	results, err := recovery.RecoverPendingDispatches(ctx, 10)
	if err != nil || len(results) == 0 {
		t.Fatalf("batch recovery=%+v err=%v", results, err)
	}
	var matched bool
	for _, result := range results {
		if result.Confirmation.ID == resumeConfirmationID {
			matched = true
			if result.DispatchError != nil || result.ResumeStatus != execution.ResumeStatusSucceeded {
				t.Fatalf("recovered result=%+v", result)
			}
		}
	}
	if !matched || sideEffects.Load() != 1 {
		t.Fatalf("matched=%v effects=%d results=%+v", matched, sideEffects.Load(), results)
	}
	// Nothing else pending.
	pending, err := recovery.ListApprovedPendingDispatch(ctx, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after recovery=%+v err=%v", pending, err)
	}
	if errors.Is(err, execution.ErrContinuationRecoveryNothing) {
		t.Fatal("unexpected nothing error")
	}
	_ = json.RawMessage(nil)
}

// TestRuntimeContinueDualReplicaContention is the P1 multi-replica gate:
// chat Confirm path and Recovery Worker share ClaimRuntimeContinue; under real
// concurrent load exactly one replica acquires the lease for a given confirmation.
func TestRuntimeContinueDualReplicaContention(t *testing.T) {
	ctx := context.Background()
	db, runs, confirmations := newConfirmationResumeFixture(t)
	resolved, request := resumeToolResolution()
	requestSnapshot, resolvedSnapshot, err := execution.BuildToolConfirmationResumeSnapshots(request, resolved)
	if err != nil {
		t.Fatal(err)
	}
	var sideEffects atomic.Int32
	var resolverCalls atomic.Int32
	pipeline := newResumeInvocationPipeline(t, confirmations, &resolverCalls, &sideEffects)
	toolExecutor, err := execution.NewToolConfirmationResumeExecutor(pipeline)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := execution.NewConfirmationResumeRegistry(toolExecutor)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, _ := execution.NewConfirmationResumeRepository(db)
	resumes, err := execution.NewConfirmationResumeService(checkpoints, confirmations, runs, registry)
	if err != nil {
		t.Fatal(err)
	}
	decisions, err := execution.NewInteractionDecisionService(confirmations, resumes)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := execution.NewContinuationRecoveryService(confirmations, resumes, decisions)
	if err != nil {
		t.Fatal(err)
	}

	run, err := runs.GetAgentRun(ctx, executionWorkspaceID, executionAgentRunID)
	if err != nil {
		t.Fatal(err)
	}
	decision := resumeDecision(t, request.Input, request.ReleaseID, invocationConnectionID, "PRODUCTION", true)
	prepared, err := resumes.Prepare(ctx, execution.PrepareConfirmationResumeInput{
		Confirmation: execution.RequestExecutionConfirmationInput{
			ID: resumeConfirmationID, WorkspaceID: executionWorkspaceID,
			RunID: executionAgentRunID, NodeID: "dual-replica-continue",
			TargetItemID: resumeInvocationID,
			ReleaseID:    invocationReleaseID, ConnectionID: invocationConnectionID,
			PlanHash: executionPlanHash, RequestedBy: executionOwnerID, Decision: decision,
		},
		Kind:                  execution.ResumeKindTool,
		SnapshotSchemaVersion: execution.ConfirmationResumeSnapshotVersion,
		RequestSnapshot:       requestSnapshot, ResolvedSnapshot: resolvedSnapshot,
		Input: request.Input, ExpectedRunLockVersion: run.LockVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := confirmations.Confirm(ctx, execution.ConfirmExecutionConfirmationInput{
		WorkspaceID: executionWorkspaceID, ConfirmationID: resumeConfirmationID,
		ActorID: executionOwnerID, ResumeToken: prepared.Requested.ResumeToken,
		RunID: executionAgentRunID, TargetItemID: resumeInvocationID,
		ReleaseID: invocationReleaseID, ConnectionID: invocationConnectionID,
		PlanHash: executionPlanHash, Input: request.Input, ExpectedLockVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	// Drive tool side-effect to SUCCEEDED so both chat Confirm continue and
	// Recovery Worker would compete for runtime continue.
	dispatched, err := recovery.RecoverDispatch(ctx, executionWorkspaceID, resumeConfirmationID)
	if err != nil || dispatched.DispatchError != nil ||
		dispatched.ResumeStatus != execution.ResumeStatusSucceeded {
		t.Fatalf("dispatch for dual-replica setup=%+v err=%v", dispatched, err)
	}

	const replicas = 8
	type claimOutcome struct {
		claim execution.RuntimeContinueClaim
		err   error
	}
	outcomes := make([]claimOutcome, replicas)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			// Replica 0 models legacy Chat Confirm; others model Recovery Worker
			// peers on other processes — all enter the same ClaimRuntimeContinue.
			claim, claimErr := recovery.ClaimRuntimeContinue(
				ctx, executionWorkspaceID, resumeConfirmationID, executionAgentRunID,
				execution.DefaultRuntimeContinueLease,
			)
			outcomes[idx] = claimOutcome{claim: claim, err: claimErr}
		}(i)
	}
	close(start)
	wg.Wait()

	var winners int
	var winnerClaimID string
	for i, out := range outcomes {
		if out.err == nil {
			winners++
			if out.claim.ClaimID == "" {
				t.Fatalf("replica %d won with empty claim id", i)
			}
			winnerClaimID = out.claim.ClaimID
			continue
		}
		if !errors.Is(out.err, execution.ErrRuntimeContinueNotClaimed) {
			t.Fatalf("replica %d unexpected err=%v", i, out.err)
		}
	}
	if winners != 1 {
		t.Fatalf("dual-replica claim must have exactly one winner, got %d outcomes=%+v",
			winners, outcomes)
	}

	// Losers (recovery peers) must not re-acquire while the chat path holds the lease.
	_, err = recovery.ClaimRuntimeContinue(
		ctx, executionWorkspaceID, resumeConfirmationID, executionAgentRunID,
		execution.DefaultRuntimeContinueLease,
	)
	if !errors.Is(err, execution.ErrRuntimeContinueNotClaimed) {
		t.Fatalf("post-contention reclaim must lose while lease live: err=%v", err)
	}

	// Winner renews then completes — same lifecycle chat Confirm and AAP use.
	if _, err := recovery.RenewRuntimeContinue(
		ctx, resumeConfirmationID, winnerClaimID, execution.DefaultRuntimeContinueLease,
	); err != nil {
		t.Fatalf("winner renew: %v", err)
	}
	if err := recovery.CompleteRuntimeContinue(ctx, resumeConfirmationID, winnerClaimID); err != nil {
		t.Fatalf("winner complete: %v", err)
	}

	// After complete, a peer (or recovery) can re-claim — single-owner invariant holds.
	reclaim, err := recovery.ClaimRuntimeContinue(
		ctx, executionWorkspaceID, resumeConfirmationID, executionAgentRunID,
		execution.DefaultRuntimeContinueLease,
	)
	if err != nil || reclaim.ClaimID == "" || reclaim.ClaimID == winnerClaimID {
		t.Fatalf("reclaim after complete=%+v err=%v first=%s", reclaim, err, winnerClaimID)
	}
	if err := recovery.CompleteRuntimeContinue(ctx, resumeConfirmationID, reclaim.ClaimID); err != nil {
		t.Fatalf("reclaim complete: %v", err)
	}
}

// countingRuntimeContinuation records ContinueApprovedInteraction calls so dual
// paths (chat Confirm lease holder vs Recovery Worker) can be asserted.
type countingRuntimeContinuation struct {
	recovery *execution.ContinuationRecoveryService
	calls    atomic.Int32
	claimed  atomic.Int32
	lost     atomic.Int32
}

func (c *countingRuntimeContinuation) ContinueApprovedInteraction(
	ctx context.Context,
	decision execution.InteractionDecisionResult,
) error {
	c.calls.Add(1)
	if c.recovery == nil {
		return execution.ErrContinuationRecoveryInvalid
	}
	claim, err := c.recovery.ClaimRuntimeContinue(
		ctx,
		decision.Confirmation.WorkspaceID,
		decision.Confirmation.ID,
		decision.Confirmation.RunID,
		execution.DefaultRuntimeContinueLease,
	)
	if err != nil {
		if errors.Is(err, execution.ErrRuntimeContinueNotClaimed) {
			c.lost.Add(1)
			return err
		}
		return err
	}
	c.claimed.Add(1)
	// Model a short-lived continue drive then release the lease.
	_ = claim
	return c.recovery.CompleteRuntimeContinue(ctx, decision.Confirmation.ID, claim.ClaimID)
}

// TestChatAndRecoveryShareContinueLease proves Recovery Worker continues through
// the same Claim path as legacy Chat Confirm: when Chat already holds the lease,
// recovery loses; when Chat completes, recovery can claim.
func TestChatAndRecoveryShareContinueLease(t *testing.T) {
	ctx := context.Background()
	db, runs, confirmations := newConfirmationResumeFixture(t)
	resolved, request := resumeToolResolution()
	requestSnapshot, resolvedSnapshot, err := execution.BuildToolConfirmationResumeSnapshots(request, resolved)
	if err != nil {
		t.Fatal(err)
	}
	var sideEffects atomic.Int32
	var resolverCalls atomic.Int32
	pipeline := newResumeInvocationPipeline(t, confirmations, &resolverCalls, &sideEffects)
	toolExecutor, _ := execution.NewToolConfirmationResumeExecutor(pipeline)
	registry, _ := execution.NewConfirmationResumeRegistry(toolExecutor)
	checkpoints, _ := execution.NewConfirmationResumeRepository(db)
	resumes, _ := execution.NewConfirmationResumeService(checkpoints, confirmations, runs, registry)
	decisions, _ := execution.NewInteractionDecisionService(confirmations, resumes)
	recovery, _ := execution.NewContinuationRecoveryService(confirmations, resumes, decisions)

	run, _ := runs.GetAgentRun(ctx, executionWorkspaceID, executionAgentRunID)
	decision := resumeDecision(t, request.Input, request.ReleaseID, invocationConnectionID, "PRODUCTION", true)
	prepared, err := resumes.Prepare(ctx, execution.PrepareConfirmationResumeInput{
		Confirmation: execution.RequestExecutionConfirmationInput{
			ID: resumeConfirmationID, WorkspaceID: executionWorkspaceID,
			RunID: executionAgentRunID, NodeID: "chat-recovery-share",
			TargetItemID: resumeInvocationID,
			ReleaseID:    invocationReleaseID, ConnectionID: invocationConnectionID,
			PlanHash: executionPlanHash, RequestedBy: executionOwnerID, Decision: decision,
		},
		Kind: execution.ResumeKindTool, SnapshotSchemaVersion: execution.ConfirmationResumeSnapshotVersion,
		RequestSnapshot: requestSnapshot, ResolvedSnapshot: resolvedSnapshot, Input: request.Input,
		ExpectedRunLockVersion: run.LockVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := confirmations.Confirm(ctx, execution.ConfirmExecutionConfirmationInput{
		WorkspaceID: executionWorkspaceID, ConfirmationID: resumeConfirmationID,
		ActorID: executionOwnerID, ResumeToken: prepared.Requested.ResumeToken,
		RunID: executionAgentRunID, TargetItemID: resumeInvocationID,
		ReleaseID: invocationReleaseID, ConnectionID: invocationConnectionID,
		PlanHash: executionPlanHash, Input: request.Input, ExpectedLockVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := recovery.RecoverDispatch(ctx, executionWorkspaceID, resumeConfirmationID); err != nil {
		t.Fatal(err)
	}

	// Replica A: legacy Chat Confirm acquires the shared continue lease first.
	chatClaim, err := recovery.ClaimRuntimeContinue(
		ctx, executionWorkspaceID, resumeConfirmationID, executionAgentRunID,
		execution.DefaultRuntimeContinueLease,
	)
	if err != nil {
		t.Fatalf("chat claim: %v", err)
	}

	// Replica B: Recovery Worker path (ContinueApprovedInteraction + claim).
	counter := &countingRuntimeContinuation{recovery: recovery}
	// Simulate what RecoveryWorker.recoverContinuations does for one candidate.
	awaiting, err := recovery.ListSucceededAwaitingRuntimeContinue(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Chat holds the lease — candidate must be hidden from the recovery list.
	for _, item := range awaiting {
		if item.ConfirmationID == resumeConfirmationID {
			t.Fatal("chat-held lease must exclude confirmation from recovery list")
		}
	}
	// Even if recovery is invoked directly (race before list filter), claim loses.
	direct := execution.DecisionResultFromRecovery(execution.RecoverableApprovedContinuation{
		WorkspaceID: executionWorkspaceID, ConfirmationID: resumeConfirmationID,
		RunID: executionAgentRunID,
		Confirmation: execution.ExecutionConfirmation{
			ID: resumeConfirmationID, WorkspaceID: executionWorkspaceID, RunID: executionAgentRunID,
		},
	})
	if contErr := counter.ContinueApprovedInteraction(ctx, direct); !errors.Is(contErr, execution.ErrRuntimeContinueNotClaimed) {
		t.Fatalf("recovery while chat holds lease: err=%v", contErr)
	}
	if counter.claimed.Load() != 0 || counter.lost.Load() != 1 {
		t.Fatalf("claimed=%d lost=%d want claimed=0 lost=1", counter.claimed.Load(), counter.lost.Load())
	}

	// Chat finishes continue drive and releases lease.
	if err := recovery.CompleteRuntimeContinue(ctx, resumeConfirmationID, chatClaim.ClaimID); err != nil {
		t.Fatalf("chat complete: %v", err)
	}
	// Recovery can now own the drive (e.g. chat crashed after complete without finishing work).
	if contErr := counter.ContinueApprovedInteraction(ctx, direct); contErr != nil {
		t.Fatalf("recovery after chat complete: %v", contErr)
	}
	if counter.claimed.Load() != 1 {
		t.Fatalf("recovery claimed=%d want 1", counter.claimed.Load())
	}
}
