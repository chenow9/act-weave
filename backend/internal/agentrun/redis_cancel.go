package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"

	"actweave/backend/internal/redisx"

	"github.com/redis/go-redis/v9"
)

var errCancelBusInvalid = errors.New("redis cancel bus is invalid")

type redisCancelPayload struct {
	WorkspaceID string `json:"workspaceId"`
	RunID       string `json:"runId"`
}

// RedisCancelBus broadcasts CancelRun to every replica. The replica that
// accepted the HTTP cancel also interrupts locally; others no-op if idle.
type RedisCancelBus struct {
	client *redisx.Client
	pubsub *redis.PubSub
	cancel context.CancelFunc
	closed atomic.Bool
}

func NewRedisCancelBus(parent context.Context, client *redisx.Client) (*RedisCancelBus, error) {
	if client == nil || client.RDB == nil {
		return nil, errCancelBusInvalid
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	bus := &RedisCancelBus{client: client, cancel: cancel}
	bus.pubsub = client.RDB.Subscribe(ctx, client.Channel("cancel"))
	return bus, nil
}

func (bus *RedisCancelBus) Publish(ctx context.Context, workspaceID, runID string) error {
	if bus == nil || bus.closed.Load() {
		return errCancelBusInvalid
	}
	workspaceID = strings.TrimSpace(workspaceID)
	runID = strings.TrimSpace(runID)
	if workspaceID == "" || runID == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	payload, err := json.Marshal(redisCancelPayload{WorkspaceID: workspaceID, RunID: runID})
	if err != nil {
		return err
	}
	return bus.client.RDB.Publish(ctx, bus.client.Channel("cancel"), payload).Err()
}

func (bus *RedisCancelBus) Listen(local Runtime) {
	if bus == nil || local == nil || bus.pubsub == nil {
		return
	}
	ch := bus.pubsub.Channel()
	for msg := range ch {
		var payload redisCancelPayload
		if json.Unmarshal([]byte(msg.Payload), &payload) != nil {
			continue
		}
		_ = local.CancelRun(payload.WorkspaceID, payload.RunID)
	}
}

func (bus *RedisCancelBus) Close() error {
	if bus == nil {
		return nil
	}
	if !bus.closed.CompareAndSwap(false, true) {
		return nil
	}
	if bus.cancel != nil {
		bus.cancel()
	}
	if bus.pubsub != nil {
		return bus.pubsub.Close()
	}
	return nil
}

// BroadcastingRuntime cancels locally then publishes so other replicas interrupt.
type BroadcastingRuntime struct {
	Local Runtime
	Bus   *RedisCancelBus
}

func (runtime BroadcastingRuntime) Enqueue(job Job) {
	if runtime.Local != nil {
		runtime.Local.Enqueue(job)
	}
}

func (runtime BroadcastingRuntime) EnqueueContinueWithLifecycle(
	job Job, requestSnapshot, toolResult json.RawMessage, life ContinueLifecycle,
) {
	if runtime.Local != nil {
		runtime.Local.EnqueueContinueWithLifecycle(job, requestSnapshot, toolResult, life)
	}
}

func (runtime BroadcastingRuntime) CancelRun(workspaceID, runID string) error {
	var localErr error
	if runtime.Local != nil {
		localErr = runtime.Local.CancelRun(workspaceID, runID)
	}
	if runtime.Bus != nil {
		if pubErr := runtime.Bus.Publish(context.Background(), workspaceID, runID); pubErr != nil && localErr == nil {
			return pubErr
		}
	}
	return localErr
}
