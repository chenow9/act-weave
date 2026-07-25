package execution

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestInvokePipelineRunsOrderedGuardsAndIdempotentRetry(t *testing.T) {
	log := &pipelineLog{}
	executor := &pipelineExecutor{log: log, failures: 1, retryable: true}
	registry, _ := NewRegistry(executor)
	idempotency := &pipelineIdempotency{log: log, decision: IdempotencyDecision{State: IdempotencyNew}}
	recorder := &pipelineRecorder{log: log}
	pipeline := newPipelineTest(t, log, registry, idempotency, recorder, ResolvedInvocation{
		Snapshot: validPipelineSnapshot(), Connection: validPipelineConnection(),
		Credential: CredentialReference{WorkspaceID: "workspace-one", AuthMode: "NONE"},
		RiskLevel:  "MEDIUM", SideEffectLevel: "READ", RequiresConfirmation: true,
		Idempotent: true, RetryCount: 1,
	})
	result, err := pipeline.Invoke(context.Background(), validPipelineRequest())
	if err != nil {
		t.Fatalf("invoke ordered pipeline: %v", err)
	}
	if result.Attempts != 2 || result.Cached || string(result.Output) != `{"status":"ok"}` {
		t.Fatalf("unexpected invocation result: %+v", result)
	}
	expected := []string{
		"authorize", "resolve", "confirm", "idempotency.begin", "limit", "record.started",
		"secret", "executor", "retry.wait", "secret", "executor", "record.finished", "idempotency.complete",
	}
	if actual := log.snapshot(); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("pipeline order mismatch:\nactual:   %v\nexpected: %v", actual, expected)
	}
	if recorder.finished.Status != "SUCCEEDED" || recorder.finished.Attempts != 2 ||
		recorder.finished.RetentionMode != InvocationRetentionMode {
		t.Fatalf("unexpected finished invocation record: %+v", recorder.finished)
	}
	encoded, _ := json.Marshal(recorder.finished)
	if strings.Contains(string(encoded), "customer-sensitive") || strings.Contains(string(encoded), "status") {
		t.Fatalf("serialized invocation record leaked raw payload: %s", encoded)
	}
}

func TestInvokeRetriesOnlyExplicitlyIdempotentOperations(t *testing.T) {
	tests := []struct {
		name         string
		resolved     ResolvedInvocation
		request      InvokeRequest
		wantAttempts int
	}{
		{name: "non idempotent", resolved: ResolvedInvocation{
			Snapshot: validPipelineSnapshot(), Connection: validPipelineConnection(),
			Credential: CredentialReference{WorkspaceID: "workspace-one", AuthMode: "NONE"},
			RiskLevel:  "MEDIUM", RetryCount: 3,
		}, request: validPipelineRequest(), wantAttempts: 1},
		{name: "high risk without key", resolved: ResolvedInvocation{
			Snapshot: validPipelineSnapshot(), Connection: validPipelineConnection(),
			Credential: CredentialReference{WorkspaceID: "workspace-one", AuthMode: "NONE"},
			RiskLevel:  "HIGH", Idempotent: true, RetryCount: 3,
		}, request: requestWithoutIdempotency(), wantAttempts: 1},
		{name: "key unsupported", resolved: ResolvedInvocation{
			Snapshot: validPipelineSnapshot(), Connection: validPipelineConnection(),
			Credential: CredentialReference{WorkspaceID: "workspace-one", AuthMode: "NONE"},
			RiskLevel:  "MEDIUM", RetryCount: 3,
		}, request: validPipelineRequest(), wantAttempts: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			log := &pipelineLog{}
			executor := &pipelineExecutor{log: log, failures: 10, retryable: true}
			registry, _ := NewRegistry(executor)
			pipeline := newPipelineTest(t, log, registry,
				&pipelineIdempotency{log: log, decision: IdempotencyDecision{State: IdempotencyNew}},
				&pipelineRecorder{log: log}, test.resolved)
			result, err := pipeline.Invoke(context.Background(), test.request)
			if ErrorCode(err) != ErrorCodeUpstream || result.Attempts != test.wantAttempts {
				t.Fatalf("unexpected retry decision: attempts=%d err=%v", result.Attempts, err)
			}
		})
	}
}

func TestInvokeUsesCachedIdempotentResultWithoutExecution(t *testing.T) {
	log := &pipelineLog{}
	cached := InvocationResult{InvocationID: "cached", Output: json.RawMessage(`{"cached":true}`)}
	executor := &pipelineExecutor{log: log}
	registry, _ := NewRegistry(executor)
	pipeline := newPipelineTest(t, log, registry,
		&pipelineIdempotency{log: log, decision: IdempotencyDecision{State: IdempotencyCached, Result: cached}},
		&pipelineRecorder{log: log}, ResolvedInvocation{
			Snapshot: validPipelineSnapshot(), Connection: validPipelineConnection(),
			Credential: CredentialReference{WorkspaceID: "workspace-one", AuthMode: "NONE"},
			RiskLevel:  "LOW",
		})
	request := validPipelineRequest()
	request.ConfirmationID = ""
	result, err := pipeline.Invoke(context.Background(), request)
	if err != nil || !result.Cached || result.InvocationID != "cached" {
		t.Fatalf("return cached invocation: %+v err=%v", result, err)
	}
	if actual := log.snapshot(); !reflect.DeepEqual(actual, []string{"authorize", "resolve", "idempotency.begin"}) {
		t.Fatalf("cached invocation executed side effects: %v", actual)
	}
}

func TestInvokeMapsHTTPErrorAndDoesNotRetryMappedNonRetryableCode(t *testing.T) {
	log := &pipelineLog{}
	snapshot := validPipelineSnapshot()
	snapshot.ErrorMappings = json.RawMessage(`{"503":{"errorCode":"ORDERS_UNAVAILABLE","retryable":false}}`)
	executor := &pipelineExecutor{log: log, failures: 10, retryable: true, httpStatus: 503}
	registry, _ := NewRegistry(executor)
	pipeline := newPipelineTest(t, log, registry,
		&pipelineIdempotency{log: log, decision: IdempotencyDecision{State: IdempotencyNew}},
		&pipelineRecorder{log: log}, ResolvedInvocation{
			Snapshot: snapshot, Connection: validPipelineConnection(),
			Credential: CredentialReference{WorkspaceID: "workspace-one", AuthMode: "NONE"},
			RiskLevel:  "LOW", Idempotent: true, RetryCount: 3,
		})
	result, err := pipeline.Invoke(context.Background(), validPipelineRequest())
	if ErrorCode(err) != "ORDERS_UNAVAILABLE" || result.Attempts != 1 {
		t.Fatalf("configured error mapping/retry failed: attempts=%d err=%v", result.Attempts, err)
	}
}

func newPipelineTest(
	t *testing.T,
	log *pipelineLog,
	registry *Registry,
	idempotency IdempotencyStore,
	recorder *pipelineRecorder,
	resolved ResolvedInvocation,
) *InvocationPipeline {
	t.Helper()
	pipeline, err := NewInvocationPipeline(
		pipelineAuthorizer{log: log}, pipelineResolver{log: log, resolved: resolved},
		pipelineConfirmation{log: log}, idempotency, pipelineLimiter{log: log},
		pipelineInjector{log: log}, registry, recorder,
		RetryWaiterFunc(func(context.Context, int) error { log.add("retry.wait"); return nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	return pipeline
}

func validPipelineRequest() InvokeRequest {
	return InvokeRequest{
		InvocationID: "invocation-one", WorkspaceID: "workspace-one", CapabilityID: "tool-one",
		ReleaseID: "release-one", ActorType: "USER", ActorID: "user-one", TraceID: "trace-one",
		Input:          json.RawMessage(`{"orderId":"customer-sensitive"}`),
		ConfirmationID: "confirmation-one", IdempotencyKey: "idempotency-one",
	}
}

func requestWithoutIdempotency() InvokeRequest {
	request := validPipelineRequest()
	request.IdempotencyKey = ""
	return request
}

func validPipelineSnapshot() ReleaseSnapshot {
	return ReleaseSnapshot{
		ReleaseID: "release-one", WorkspaceID: "workspace-one", CapabilityID: "tool-one",
		ToolVersionID: "version-one", ExecutorType: ExecutorTypeHTTP, ProviderID: "provider-one",
		InputSchema:   json.RawMessage(`{"type":"object","required":["orderId"]}`),
		OutputSchema:  json.RawMessage(`{"type":"object","required":["status"]}`),
		ErrorMappings: json.RawMessage(`{}`), RuntimePolicy: json.RawMessage(`{}`),
	}
}

func validPipelineConnection() ConnectionSnapshot {
	return ConnectionSnapshot{ID: "connection-one", WorkspaceID: "workspace-one", ProviderID: "provider-one"}
}

type pipelineLog struct {
	mutex  sync.Mutex
	values []string
}

func (log *pipelineLog) add(value string) {
	log.mutex.Lock()
	log.values = append(log.values, value)
	log.mutex.Unlock()
}
func (log *pipelineLog) snapshot() []string {
	log.mutex.Lock()
	defer log.mutex.Unlock()
	return append([]string(nil), log.values...)
}

type pipelineAuthorizer struct{ log *pipelineLog }

func (value pipelineAuthorizer) AuthorizeInvocation(context.Context, string, string) error {
	value.log.add("authorize")
	return nil
}

type pipelineResolver struct {
	log      *pipelineLog
	resolved ResolvedInvocation
}

func (value pipelineResolver) ResolveInvocation(context.Context, ResolveRequest) (ResolvedInvocation, error) {
	value.log.add("resolve")
	return value.resolved, nil
}

type pipelineConfirmation struct{ log *pipelineLog }

func (value pipelineConfirmation) VerifyInvocationConfirmation(context.Context, ConfirmationCheck) error {
	value.log.add("confirm")
	return nil
}

type pipelineIdempotency struct {
	log      *pipelineLog
	decision IdempotencyDecision
}

func (value *pipelineIdempotency) BeginInvocation(context.Context, IdempotencyRequest) (IdempotencyDecision, error) {
	value.log.add("idempotency.begin")
	return value.decision, nil
}
func (value *pipelineIdempotency) CompleteInvocation(context.Context, IdempotencyRequest, InvocationResult) error {
	value.log.add("idempotency.complete")
	return nil
}
func (value *pipelineIdempotency) FailInvocation(context.Context, IdempotencyRequest, string) error {
	value.log.add("idempotency.fail")
	return nil
}

type pipelineLimiter struct{ log *pipelineLog }

func (value pipelineLimiter) AllowInvocation(context.Context, LimitRequest) error {
	value.log.add("limit")
	return nil
}

type pipelineInjector struct{ log *pipelineLog }

func (value pipelineInjector) WithInjectedConnection(_ context.Context, connection ConnectionSnapshot, _ CredentialReference, invoke func(ConnectionSnapshot) error) error {
	value.log.add("secret")
	return invoke(connection)
}

type pipelineExecutor struct {
	log        *pipelineLog
	failures   int
	retryable  bool
	httpStatus int
}

func (*pipelineExecutor) Kind() string                   { return ExecutorTypeHTTP }
func (*pipelineExecutor) Capabilities() ExecutorFeatures { return ExecutorFeatures{} }
func (value *pipelineExecutor) Invoke(_ context.Context, request InvocationRequest, _ InvocationEventSink) (InvocationResult, error) {
	value.log.add("executor")
	if value.failures > 0 {
		value.failures--
		return InvocationResult{InvocationID: request.InvocationID, HTTPStatus: value.httpStatus},
			NewError(ErrorCodeUpstream, "UPSTREAM", value.retryable, value.httpStatus, nil)
	}
	return InvocationResult{InvocationID: request.InvocationID, Output: json.RawMessage(`{"status":"ok"}`)}, nil
}
func (*pipelineExecutor) Cancel(context.Context, InvocationRef) error { return nil }

type pipelineRecorder struct {
	log      *pipelineLog
	started  InvocationRecord
	finished InvocationRecord
}

func (value *pipelineRecorder) InvocationStarted(_ context.Context, record InvocationRecord) error {
	value.log.add("record.started")
	value.started = record
	return nil
}
func (value *pipelineRecorder) InvocationFinished(_ context.Context, record InvocationRecord) error {
	value.log.add("record.finished")
	value.finished = record
	return nil
}
