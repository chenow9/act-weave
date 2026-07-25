package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/execution"
)

func TestHTTPExecutorInvokesImmutableSnapshotAndNormalizesResponse(t *testing.T) {
	requestArrived := make(chan struct{})
	releaseResponse := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.EscapedPath() != "/api/orders/A%2FB" {
			t.Errorf("unexpected request target: %s %s", request.Method, request.URL.EscapedPath())
		}
		if request.URL.Query().Get("verbose") != "true" {
			t.Errorf("expected query mapping, got %s", request.URL.RawQuery)
		}
		if request.Header.Get("X-Tenant") != "tenant-one" {
			t.Errorf("expected connection header snapshot, got %q", request.Header.Get("X-Tenant"))
		}
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		if string(payload) != `{"quantity":2}` {
			t.Errorf("unexpected request body: %s", payload)
		}
		close(requestArrived)
		<-releaseResponse
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(" {\n  \"status\": \"accepted\"\n} "))
	}))
	defer server.Close()

	executor := NewHTTPExecutor(server.Client())
	request := validExecutorRequest(server.URL + "/api")
	events := &eventRecorder{}
	resultChannel := make(chan execution.InvocationResult, 1)
	errorChannel := make(chan error, 1)
	go func() {
		result, err := executor.Invoke(context.Background(), request, events)
		resultChannel <- result
		errorChannel <- err
	}()
	<-requestArrived
	request.Snapshot.ActionConfig = json.RawMessage(`{"method":"DELETE","path":"/changed"}`)
	request.Connection.Headers["X-Tenant"] = "mutated"
	close(releaseResponse)
	result := <-resultChannel
	if err := <-errorChannel; err != nil {
		t.Fatalf("invoke HTTP snapshot: %v", err)
	}
	if result.HTTPStatus != http.StatusCreated || result.ContentType != "application/json" ||
		string(result.Output) != `{"status":"accepted"}` || result.Latency <= 0 || result.FinishedAt.Before(result.StartedAt) {
		t.Fatalf("unexpected normalized result: %+v", result)
	}
	if types := events.types(); !reflect.DeepEqual(types, []string{execution.EventStarted, execution.EventCompleted}) {
		t.Fatalf("unexpected invocation events: %v", types)
	}
	features := executor.Capabilities()
	if !features.Cancel || features.Streaming || features.Session || features.Sandbox {
		t.Fatalf("unexpected HTTP executor features: %+v", features)
	}
}

func TestHTTPExecutorTimeoutCancellationAndResponseLimit(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			time.Sleep(100 * time.Millisecond)
		}))
		defer server.Close()
		request := validExecutorRequest(server.URL)
		request.Snapshot.RuntimePolicy = json.RawMessage(`{"timeoutMs":15,"maxResponseBytes":1024}`)
		_, err := NewHTTPExecutor(server.Client()).Invoke(context.Background(), request, nil)
		if execution.ErrorCode(err) != execution.ErrorCodeTimeout || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected stable timeout error, got %v", err)
		}
	})

	t.Run("explicit cancellation", func(t *testing.T) {
		requestArrived := make(chan struct{})
		releaseHandler := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			close(requestArrived)
			<-releaseHandler
		}))
		defer server.Close()
		defer close(releaseHandler)
		executor := NewHTTPExecutor(server.Client())
		request := validExecutorRequest(server.URL)
		errorChannel := make(chan error, 1)
		go func() {
			_, err := executor.Invoke(context.Background(), request, nil)
			errorChannel <- err
		}()
		<-requestArrived
		if err := executor.Cancel(context.Background(), execution.InvocationRef{InvocationID: request.InvocationID}); err != nil {
			t.Fatalf("cancel active invocation: %v", err)
		}
		if err := <-errorChannel; execution.ErrorCode(err) != execution.ErrorCodeCanceled || !errors.Is(err, context.Canceled) {
			t.Fatalf("expected stable cancellation error, got %v", err)
		}
		if err := executor.Cancel(context.Background(), execution.InvocationRef{InvocationID: request.InvocationID}); !errors.Is(err, execution.ErrInvocationNotActive) {
			t.Fatalf("expected completed invocation to be inactive, got %v", err)
		}
	})

	t.Run("response limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(strings.Repeat("x", 65)))
		}))
		defer server.Close()
		request := validExecutorRequest(server.URL)
		request.Snapshot.RuntimePolicy = json.RawMessage(`{"timeoutMs":1000,"maxResponseBytes":64}`)
		result, err := NewHTTPExecutor(server.Client()).Invoke(context.Background(), request, nil)
		if execution.ErrorCode(err) != execution.ErrorCodeResponseTooLarge || len(result.Output) != 0 {
			t.Fatalf("expected response-size rejection without partial output, result=%+v err=%v", result, err)
		}
	})
}

func TestHTTPExecutorNormalizesUpstreamErrorsWithoutBodyLeak(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = writer.Write([]byte(`{"internal":"upstream-secret"}`))
	}))
	defer server.Close()
	result, err := NewHTTPExecutor(server.Client()).Invoke(context.Background(), validExecutorRequest(server.URL), nil)
	if execution.ErrorCode(err) != execution.ErrorCodeUpstreamHTTP || result.HTTPStatus != http.StatusBadGateway {
		t.Fatalf("unexpected upstream HTTP error: result=%+v err=%v", result, err)
	}
	if strings.Contains(err.Error(), "upstream-secret") || string(result.Output) != `{"internal":"upstream-secret"}` {
		t.Fatalf("error should be stable while controlled result retains response: result=%s err=%v", result.Output, err)
	}
}

func TestHTTPExecutorSSRFRejectsPrivateEndpointWithoutExplicitPolicy(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls++
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	request := validExecutorRequest(server.URL)
	request.Connection.EgressPolicy = execution.EgressPolicy{}
	_, err := NewHTTPExecutor(server.Client()).Invoke(context.Background(), request, nil)
	if execution.ErrorCode(err) != execution.ErrorCodeEgressDenied || calls != 0 {
		t.Fatalf("expected private endpoint to be denied before dialing, calls=%d err=%v", calls, err)
	}
}

func TestExecutorRegistryOnlyRegistersHTTP(t *testing.T) {
	registry, err := NewExecutorRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(registry.Kinds(), []string{execution.ExecutorTypeHTTP}) {
		t.Fatalf("phase-one registry contains unavailable executors: %v", registry.Kinds())
	}
	if _, err := registry.Resolve("HTTP"); err != nil {
		t.Fatalf("resolve registered HTTP executor: %v", err)
	}
	for _, unavailable := range []string{"INTERNAL", "MCP", "CONNECTOR_ACTION", "SHELL"} {
		if _, err := registry.Resolve(unavailable); !errors.Is(err, execution.ErrExecutorNotFound) {
			t.Fatalf("expected %s to remain unavailable, got %v", unavailable, err)
		}
	}
}

func validExecutorRequest(baseURL string) execution.InvocationRequest {
	return execution.InvocationRequest{
		InvocationID: "invocation-one", TraceID: "trace-one",
		Snapshot: execution.ReleaseSnapshot{
			ReleaseID: "release-one", WorkspaceID: "workspace-one", CapabilityID: "tool-one",
			ToolVersionID: "version-one", ExecutorType: execution.ExecutorTypeHTTP,
			ProviderID: "provider-one", ActionSchemaVersion: "http.v1",
			ActionConfig: json.RawMessage(`{
				"method":"POST","path":"/orders/{orderId}",
				"parameters":[
					{"name":"orderId","in":"path","required":true},
					{"name":"verbose","in":"query"}
				],
				"requestBody":{"input":"payload"}
			}`),
			InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
			ErrorMappings: json.RawMessage(`{}`), RuntimePolicy: json.RawMessage(`{"timeoutMs":1000,"maxResponseBytes":1024}`),
			Checksum: "snapshot-checksum",
		},
		Connection: execution.ConnectionSnapshot{
			ID: "connection-one", WorkspaceID: "workspace-one", ProviderID: "provider-one",
			BaseURL: baseURL, Headers: map[string]string{"X-Tenant": "tenant-one"},
			EgressPolicy: execution.EgressPolicy{AllowedCIDRs: []string{"127.0.0.0/8"}},
		},
		Input: json.RawMessage(`{"orderId":"A/B","verbose":true,"payload":{"quantity":2}}`),
	}
}

type eventRecorder struct {
	mutex  sync.Mutex
	events []execution.InvocationEvent
}

func (recorder *eventRecorder) Emit(_ context.Context, event execution.InvocationEvent) error {
	recorder.mutex.Lock()
	recorder.events = append(recorder.events, event)
	recorder.mutex.Unlock()
	return nil
}

func (recorder *eventRecorder) types() []string {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	values := make([]string, len(recorder.events))
	for index, event := range recorder.events {
		values[index] = event.Type
	}
	return values
}
