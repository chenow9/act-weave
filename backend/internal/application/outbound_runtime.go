package application

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"log/slog"
	"strings"
	"sync"
	"time"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/outboundidentity"

	"github.com/google/uuid"
)

// outboundRuntimeLifecycle owns instance registration, heartbeat, drain and
// affinity gate wiring for T2=A. Private keys never leave process memory.
type outboundRuntimeLifecycle struct {
	repo       *outboundidentity.RuntimeRepository
	router     *outboundidentity.RuntimeRouter
	reconciler *outboundidentity.StaleAffinityReconciler
	instanceID string
	bootID     string
	// routingPrivateKey is process-local only; DB stores public key only.
	routingPrivateKey ed25519.PrivateKey

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func startOutboundRuntimeLifecycle(
	ctx context.Context,
	db *sql.DB,
	instanceID, internalAddress string,
	continuationRecovery *execution.ContinuationRecoveryService,
	recoveryWorker *execution.RecoveryWorker,
	logger *slog.Logger,
) (*outboundRuntimeLifecycle, error) {
	if db == nil || instanceID == "" || internalAddress == "" {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	repo, err := outboundidentity.NewRuntimeRepository(db)
	if err != nil {
		return nil, err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	bootID := uuid.Must(uuid.NewV7()).String()
	if err := repo.RegisterInstance(ctx, outboundidentity.RuntimeInstance{
		InstanceID: instanceID, BootID: bootID,
		InternalAddress: internalAddress, RoutingPublicKey: pub,
	}); err != nil {
		return nil, err
	}
	router, err := outboundidentity.NewRuntimeRouter(
		repo, instanceID, bootID, outboundidentity.DefaultHeartbeatStaleAfter,
	)
	if err != nil {
		return nil, err
	}
	if continuationRecovery != nil {
		continuationRecovery.WithOutboundGate(execution.RuntimeRouterContinuationGate{Router: router})
	}
	reconciler, err := outboundidentity.NewStaleAffinityReconciler(
		repo, outboundidentity.DefaultHeartbeatStaleAfter, nil, logger,
	)
	if err != nil {
		return nil, err
	}
	if recoveryWorker != nil {
		recoveryWorker.WithAffinityReconciler(reconciler)
	}
	life := &outboundRuntimeLifecycle{
		repo: repo, router: router, reconciler: reconciler,
		instanceID: instanceID, bootID: bootID, routingPrivateKey: priv,
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	life.cancel = cancel
	life.done = make(chan struct{})
	go life.heartbeatLoop(runCtx)
	return life, nil
}

func (l *outboundRuntimeLifecycle) heartbeatLoop(ctx context.Context) {
	defer close(l.done)
	ticker := time.NewTicker(outboundidentity.DefaultHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := l.repo.Heartbeat(ctx, l.instanceID, l.bootID); err != nil {
				// Safe log — no tokens.
				slog.Default().Warn("outbound runtime heartbeat failed",
					"event", "outbound.runtime.heartbeat_failed",
					"error_code", outboundidentity.CodeCredentialExpired,
				)
			}
		}
	}
}

// Stop drains, deletes owner affinities, unregisters the instance. Idempotent.
func (l *outboundRuntimeLifecycle) Stop() {
	if l == nil {
		return
	}
	l.mu.Lock()
	cancel := l.cancel
	done := l.done
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	ctx, cancelTimeout := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelTimeout()
	_ = l.repo.SetDraining(ctx, l.instanceID, l.bootID, true)
	_, _ = l.repo.DeleteAffinitiesForOwner(ctx, l.instanceID, l.bootID)
	_ = l.repo.DeleteInstance(ctx, l.instanceID, l.bootID)
	// Zero private key material best-effort.
	for i := range l.routingPrivateKey {
		l.routingPrivateKey[i] = 0
	}
	l.mu.Lock()
	l.cancel = nil
	l.done = nil
	l.mu.Unlock()
}

// Router exposes the process-local router for tests / later envelope wiring.
func (l *outboundRuntimeLifecycle) Router() *outboundidentity.RuntimeRouter {
	if l == nil {
		return nil
	}
	return l.router
}

// machineCredentialResolver loads machine Secret id + active version for Broker private_key_jwt.
type machineCredentialResolver struct {
	db *sql.DB
}

func (r machineCredentialResolver) ResolveMachineCredential(
	ctx context.Context, workspaceID, connectionID string,
) (execution.MachineCredentialRef, error) {
	if r.db == nil {
		return execution.MachineCredentialRef{}, outboundidentity.ErrIdentityConnectionNotReady
	}
	var secretID sql.NullString
	var version sql.NullInt64
	err := r.db.QueryRowContext(ctx, `
		SELECT c.machine_credential_secret_id, mv.version
		FROM service_connections c
		LEFT JOIN secrets s
		  ON s.workspace_id=c.workspace_id AND s.id=c.machine_credential_secret_id
		LEFT JOIN secret_versions mv
		  ON mv.workspace_id=s.workspace_id AND mv.secret_id=s.id
		 AND mv.id=s.active_version_id AND mv.revoked_at IS NULL
		WHERE c.workspace_id=$1 AND c.id=$2 AND c.deleted_at IS NULL
	`, workspaceID, connectionID).Scan(&secretID, &version)
	if err != nil || !secretID.Valid || strings.TrimSpace(secretID.String) == "" || !version.Valid || version.Int64 <= 0 {
		return execution.MachineCredentialRef{}, outboundidentity.ErrIdentityConnectionNotReady
	}
	return execution.MachineCredentialRef{SecretID: secretID.String, Version: version.Int64}, nil
}
