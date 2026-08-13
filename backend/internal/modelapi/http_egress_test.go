package modelapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

var testModelEgress = LoopbackEgressPolicy()

func TestProtectHTTPClientForAPIBaseRejectsLoopbackByDefault(t *testing.T) {
	_, err := ProtectHTTPClientForAPIBase(
		context.Background(), NewStreamingHTTPClient(), "http://127.0.0.1:8080/v1", EgressPolicy{},
	)
	if !errors.Is(err, ErrAgenticInvalidAPIBase) {
		t.Fatalf("expected loopback API base rejection, got %v", err)
	}
}

func TestProtectHTTPClientForAPIBaseAllowsExplicitLoopbackCIDR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	client, err := ProtectHTTPClientForAPIBase(
		context.Background(), server.Client(), server.URL+"/v1", testModelEgress,
	)
	if err != nil {
		t.Fatalf("protect client: %v", err)
	}
	response, err := client.Get(server.URL + "/v1/models")
	if err != nil {
		t.Fatalf("guarded request: %v", err)
	}
	_ = response.Body.Close()
}
