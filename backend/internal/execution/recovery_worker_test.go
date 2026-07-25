package execution_test

import (
	"context"
	"testing"
	"time"

	"actweave/backend/internal/execution"
)

func TestRecoveryWorkerConfigValidation(t *testing.T) {
	t.Parallel()
	_, err := execution.NewRecoveryWorker(nil, nil, nil, execution.DefaultRecoveryWorkerConfig(), nil)
	if err == nil {
		t.Fatal("expected dependency error")
	}
	// Zero interval is invalid even with non-nil deps once constructed carefully.
	cfg := execution.DefaultRecoveryWorkerConfig()
	if cfg.Interval < time.Second || cfg.BatchLimit < 1 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestRecoveryWorkerStartStopWithoutPanic(t *testing.T) {
	// Construction without DB-backed services is rejected; Start/Stop nil-safe.
	var worker *execution.RecoveryWorker
	worker.Start(context.Background())
	worker.Stop()
	worker.RunOnce(context.Background())
}
