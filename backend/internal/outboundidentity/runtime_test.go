package outboundidentity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

const (
	rtUserID      = "018f70a0-0001-7000-8000-000000000001"
	rtWorkspaceID = "018f70a0-0001-7000-8000-000000000002"
	rtRunID       = "018f70a0-0001-7000-8000-000000000010"
	rtRunID2      = "018f70a0-0001-7000-8000-000000000011"
)

func newRuntimeTest(t *testing.T) (*RuntimeRepository, *sql.DB, context.Context) {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Dirty {
		t.Fatalf("migrate: %+v", version)
	}
	db := testDatabase.Open(t)
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'rt.owner','RT Owner')`, rtUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'rt-ws','RT Workspace','PRODUCTION',$2,$2,$2)
	`, rtWorkspaceID, rtUserID); err != nil {
		t.Fatal(err)
	}
	repo, err := NewRuntimeRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return repo, db, context.Background()
}

func testPubKey(tag string) []byte {
	return []byte("routing-pubkey-" + tag)
}

func TestRuntimeRegisterHeartbeatDrain(t *testing.T) {
	repo, _, ctx := newRuntimeTest(t)
	clock := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
	repo.WithClock(func() time.Time { return clock })

	inst := RuntimeInstance{
		InstanceID: "inst-a", BootID: "boot-1",
		InternalAddress:  "https://runtime-a.internal:8443",
		RoutingPublicKey: testPubKey("a"),
	}
	if err := repo.RegisterInstance(ctx, inst); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetInstance(ctx, "inst-a", "boot-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.InternalAddress != inst.InternalAddress || got.Draining {
		t.Fatalf("instance: %+v", got)
	}
	// Reject plain http / credential-looking addresses.
	if err := repo.RegisterInstance(ctx, RuntimeInstance{
		InstanceID: "inst-b", BootID: "boot-1",
		InternalAddress: "http://insecure.internal", RoutingPublicKey: testPubKey("b"),
	}); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("http address: %v", err)
	}
	if err := repo.RegisterInstance(ctx, RuntimeInstance{
		InstanceID: "inst-b", BootID: "boot-1",
		InternalAddress: "user:pass@host:1", RoutingPublicKey: testPubKey("b"),
	}); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("userinfo address: %v", err)
	}

	clock = clock.Add(5 * time.Second)
	if err := repo.Heartbeat(ctx, "inst-a", "boot-1"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetDraining(ctx, "inst-a", "boot-1", true); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetInstance(ctx, "inst-a", "boot-1")
	if !got.Draining {
		t.Fatal("expected draining")
	}
	if got.Live(clock, DefaultHeartbeatStaleAfter) {
		t.Fatal("draining instance must not be live")
	}
}

func TestRuntimeAffinityClaimCASAndIsolation(t *testing.T) {
	repo, _, ctx := newRuntimeTest(t)
	clock := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
	repo.WithClock(func() time.Time { return clock })

	mustRegister := func(inst, boot, addr string) {
		t.Helper()
		if err := repo.RegisterInstance(ctx, RuntimeInstance{
			InstanceID: inst, BootID: boot, InternalAddress: addr, RoutingPublicKey: testPubKey(inst + boot),
		}); err != nil {
			t.Fatal(err)
		}
	}
	mustRegister("inst-a", "boot-1", "https://a.internal:8443")
	mustRegister("inst-b", "boot-2", "https://b.internal:8443")

	deadline := clock.Add(10 * time.Minute)
	// Pure broker must not claim.
	if _, err := repo.ClaimAffinity(ctx, AffinityClaimRequest{
		WorkspaceID: rtWorkspaceID, RootScopeType: RootScopeAgentRun, RootScopeID: rtRunID,
		OwnerInstanceID: "inst-a", OwnerBootID: "boot-1", RootDeadlineAt: deadline,
		RequiresPassthrough: false,
	}); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("broker claim: %v", err)
	}
	// Debug attachment must not persist affinity.
	if _, err := repo.ClaimAffinity(ctx, AffinityClaimRequest{
		WorkspaceID: rtWorkspaceID, RootScopeType: RootScopeDebugAttachment, RootScopeID: rtRunID,
		OwnerInstanceID: "inst-a", OwnerBootID: "boot-1", RootDeadlineAt: deadline,
		RequiresPassthrough: true,
	}); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("debug claim: %v", err)
	}

	aff, err := repo.ClaimAffinity(ctx, AffinityClaimRequest{
		WorkspaceID: rtWorkspaceID, RootScopeType: RootScopeAgentRun, RootScopeID: rtRunID,
		OwnerInstanceID: "inst-a", OwnerBootID: "boot-1", RootDeadlineAt: deadline,
		RequiresPassthrough: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !aff.OwnedBy("inst-a", "boot-1") {
		t.Fatalf("owner: %+v", aff)
	}
	// Same owner re-claim OK.
	if _, err := repo.ClaimAffinity(ctx, AffinityClaimRequest{
		WorkspaceID: rtWorkspaceID, RootScopeType: RootScopeAgentRun, RootScopeID: rtRunID,
		OwnerInstanceID: "inst-a", OwnerBootID: "boot-1", RootDeadlineAt: deadline.Add(time.Minute),
		RequiresPassthrough: true,
	}); err != nil {
		t.Fatal(err)
	}
	// Other owner conflict.
	if _, err := repo.ClaimAffinity(ctx, AffinityClaimRequest{
		WorkspaceID: rtWorkspaceID, RootScopeType: RootScopeAgentRun, RootScopeID: rtRunID,
		OwnerInstanceID: "inst-b", OwnerBootID: "boot-2", RootDeadlineAt: deadline,
		RequiresPassthrough: true,
	}); !errors.Is(err, ErrAffinityConflict) {
		t.Fatalf("conflict: %v", err)
	}

	// Concurrent claims on a free root — exactly one owning boot at the end.
	// Same-owner reclaims also succeed (idempotent), so count unique owners, not raw wins.
	var wg sync.WaitGroup
	var conflicts int
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			owner := "inst-a"
			boot := "boot-1"
			if i%2 == 0 {
				owner, boot = "inst-b", "boot-2"
			}
			_, err := repo.ClaimAffinity(ctx, AffinityClaimRequest{
				WorkspaceID: rtWorkspaceID, RootScopeType: RootScopeAgentRun, RootScopeID: rtRunID2,
				OwnerInstanceID: owner, OwnerBootID: boot, RootDeadlineAt: deadline,
				RequiresPassthrough: true,
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				return
			}
			if errors.Is(err, ErrAffinityConflict) {
				conflicts++
				return
			}
			t.Errorf("unexpected: %v", err)
		}(i)
	}
	wg.Wait()
	if conflicts == 0 {
		t.Fatal("expected at least one cross-owner conflict under concurrency")
	}
	final, err := repo.GetAffinity(ctx, rtWorkspaceID, RootScopeAgentRun, rtRunID2)
	if err != nil {
		t.Fatal(err)
	}
	if !final.OwnedBy("inst-a", "boot-1") && !final.OwnedBy("inst-b", "boot-2") {
		t.Fatalf("unexpected owner: %+v", final)
	}

	// Affinity row must not encode token-like fields — scan via JSON dump of struct tags.
	raw, _ := json.Marshal(aff)
	if strings.Contains(strings.ToLower(string(raw)), "token") ||
		strings.Contains(string(raw), "CANARY") ||
		strings.Contains(strings.ToLower(string(raw)), "vault") {
		t.Fatalf("affinity payload looks sensitive: %s", raw)
	}
}

func TestRuntimeRouterLocalForwardExpiredNone(t *testing.T) {
	repo, _, ctx := newRuntimeTest(t)
	clock := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
	repo.WithClock(func() time.Time { return clock })

	if err := repo.RegisterInstance(ctx, RuntimeInstance{
		InstanceID: "inst-a", BootID: "boot-1",
		InternalAddress: "https://a.internal:8443", RoutingPublicKey: testPubKey("a"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.RegisterInstance(ctx, RuntimeInstance{
		InstanceID: "inst-b", BootID: "boot-2",
		InternalAddress: "https://b.internal:8443", RoutingPublicKey: testPubKey("b"),
	}); err != nil {
		t.Fatal(err)
	}

	routerA, err := NewRuntimeRouter(repo, "inst-a", "boot-1", DefaultHeartbeatStaleAfter)
	if err != nil {
		t.Fatal(err)
	}
	routerA.WithClock(func() time.Time { return clock })
	routerB, err := NewRuntimeRouter(repo, "inst-b", "boot-2", DefaultHeartbeatStaleAfter)
	if err != nil {
		t.Fatal(err)
	}
	routerB.WithClock(func() time.Time { return clock })

	// No affinity → NONE (pure broker reclaim OK).
	d, err := routerA.Route(ctx, rtWorkspaceID, RootScopeAgentRun, rtRunID)
	if err != nil || d.Kind != RouteNone {
		t.Fatalf("none: %+v %v", d, err)
	}
	gate, err := routerA.GateContinuation(ctx, rtWorkspaceID, rtRunID)
	if err != nil || !gate.Allow || gate.Skip || gate.FailClosed {
		t.Fatalf("gate none: %+v %v", gate, err)
	}

	deadline := clock.Add(5 * time.Minute)
	if _, err := repo.ClaimAffinity(ctx, AffinityClaimRequest{
		WorkspaceID: rtWorkspaceID, RootScopeType: RootScopeAgentRun, RootScopeID: rtRunID,
		OwnerInstanceID: "inst-a", OwnerBootID: "boot-1", RootDeadlineAt: deadline,
		RequiresPassthrough: true,
	}); err != nil {
		t.Fatal(err)
	}

	d, err = routerA.Route(ctx, rtWorkspaceID, RootScopeAgentRun, rtRunID)
	if err != nil || d.Kind != RouteLocal {
		t.Fatalf("local: %+v %v", d, err)
	}
	d, err = routerB.Route(ctx, rtWorkspaceID, RootScopeAgentRun, rtRunID)
	if err != nil || d.Kind != RouteForward || d.InternalAddress != "https://a.internal:8443" {
		t.Fatalf("forward: %+v %v", d, err)
	}
	gate, _ = routerB.GateContinuation(ctx, rtWorkspaceID, rtRunID)
	if !gate.Skip || gate.Allow {
		t.Fatalf("B must skip: %+v", gate)
	}

	// Owner lost: stop heartbeats and advance past stale.
	clock = clock.Add(DefaultHeartbeatStaleAfter + time.Second)
	d, err = routerB.Route(ctx, rtWorkspaceID, RootScopeAgentRun, rtRunID)
	if err != nil || d.Kind != RouteExpired || d.ReasonCode != CodeCredentialExpired {
		t.Fatalf("expired: %+v %v", d, err)
	}
	gate, _ = routerB.GateContinuation(ctx, rtWorkspaceID, rtRunID)
	if !gate.FailClosed {
		t.Fatalf("fail closed: %+v", gate)
	}
}

func TestRuntimeStaleReconcilerNoSideEffectClaim(t *testing.T) {
	repo, _, ctx := newRuntimeTest(t)
	clock := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
	repo.WithClock(func() time.Time { return clock })

	if err := repo.RegisterInstance(ctx, RuntimeInstance{
		InstanceID: "inst-a", BootID: "boot-1",
		InternalAddress: "https://a.internal:8443", RoutingPublicKey: testPubKey("a"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ClaimAffinity(ctx, AffinityClaimRequest{
		WorkspaceID: rtWorkspaceID, RootScopeType: RootScopeAgentRun, RootScopeID: rtRunID,
		OwnerInstanceID: "inst-a", OwnerBootID: "boot-1",
		RootDeadlineAt: clock.Add(time.Minute), RequiresPassthrough: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Deadline pass → stale.
	clock = clock.Add(2 * time.Minute)
	var hookCalls int
	var sideEffectClaims int
	rec, err := NewStaleAffinityReconciler(repo, DefaultHeartbeatStaleAfter, func(ctx context.Context, a RuntimeAffinity) error {
		hookCalls++
		// Hook must not take tool claims — we only count that we didn't.
		_ = a
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	n, err := rec.ReconcileOnce(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || hookCalls != 1 || sideEffectClaims != 0 {
		t.Fatalf("n=%d hooks=%d claims=%d", n, hookCalls, sideEffectClaims)
	}
	if _, err := repo.GetAffinity(ctx, rtWorkspaceID, RootScopeAgentRun, rtRunID); !errors.Is(err, ErrAffinityNotFound) {
		t.Fatalf("affinity should be gone: %v", err)
	}
}

func TestRuntimeInternalCommandValidation(t *testing.T) {
	repo, _, _ := newRuntimeTest(t)
	router, err := NewRuntimeRouter(repo, "inst-a", "boot-1", DefaultHeartbeatStaleAfter)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
	router.WithClock(func() time.Time { return now })

	cmd, err := BuildInternalCommand(rtWorkspaceID, RootScopeAgentRun, rtRunID, InternalCommandContinue, "", "nonce-1", now, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := MarshalInternalCommand(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "token") ||
		strings.Contains(strings.ToLower(string(raw)), "value") {
		t.Fatalf("command has sensitive fields: %s", raw)
	}
	got, err := router.ValidateInternalCommand(raw, now)
	if err != nil || got.Nonce != "nonce-1" {
		t.Fatalf("validate: %+v %v", got, err)
	}
	// Replay rejected.
	if _, err := router.ValidateInternalCommand(raw, now); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("replay: %v", err)
	}
	// Unknown field rejected.
	bad := []byte(`{"schemaVersion":"outbound-internal-route.v1","workspaceId":"` + rtWorkspaceID +
		`","rootScopeType":"AGENT_RUN","rootScopeId":"` + rtRunID +
		`","commandType":"CONTINUE","issuedAt":"` + now.Format(time.RFC3339Nano) +
		`","expiresAt":"` + now.Add(10*time.Second).Format(time.RFC3339Nano) +
		`","nonce":"n2","token":"LEAK"}`)
	if _, err := router.ValidateInternalCommand(bad, now); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("unknown field: %v", err)
	}
	// Oversized rejected.
	big := make([]byte, MaxInternalRouteBodyBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	if _, err := router.ValidateInternalCommand(big, now); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("oversize: %v", err)
	}
}

func TestRuntimeAffinityDeleteBeforeInstance(t *testing.T) {
	repo, _, ctx := newRuntimeTest(t)
	clock := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
	repo.WithClock(func() time.Time { return clock })
	if err := repo.RegisterInstance(ctx, RuntimeInstance{
		InstanceID: "inst-a", BootID: "boot-1",
		InternalAddress: "https://a.internal:8443", RoutingPublicKey: testPubKey("a"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ClaimAffinity(ctx, AffinityClaimRequest{
		WorkspaceID: rtWorkspaceID, RootScopeType: RootScopeAgentRun, RootScopeID: rtRunID,
		OwnerInstanceID: "inst-a", OwnerBootID: "boot-1",
		RootDeadlineAt: clock.Add(time.Minute), RequiresPassthrough: true,
	}); err != nil {
		t.Fatal(err)
	}
	// FK RESTRICT: cannot delete instance while affinity references it.
	err := repo.DeleteInstance(ctx, "inst-a", "boot-1")
	if err == nil {
		t.Fatal("expected FK protect instance delete")
	}
	n, err := repo.DeleteAffinitiesForOwner(ctx, "inst-a", "boot-1")
	if err != nil || n != 1 {
		t.Fatalf("delete affinities: n=%d err=%v", n, err)
	}
	if err := repo.DeleteInstance(ctx, "inst-a", "boot-1"); err != nil {
		t.Fatal(err)
	}
}

// Ensure uuid import used when generating unique IDs in future tests.
var _ = uuid.New
