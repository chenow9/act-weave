package agentaccessauth_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
)

type staticCORSOriginLister struct {
	bindings []agentaccessauth.CORSOriginBinding
	err      error
	calls    atomic.Int32
}

func (lister *staticCORSOriginLister) ListExactCORSOriginBindings(
	context.Context,
) ([]agentaccessauth.CORSOriginBinding, error) {
	lister.calls.Add(1)
	if lister.err != nil {
		return nil, lister.err
	}
	return append([]agentaccessauth.CORSOriginBinding(nil), lister.bindings...), nil
}

func TestCachedExactOriginMatcherUsesClientOrigins(t *testing.T) {
	t.Parallel()
	lister := &staticCORSOriginLister{bindings: []agentaccessauth.CORSOriginBinding{{
		Origin: "https://app.example.test", WorkspaceID: "ws-1",
		PublicClientID: "awcl_client_a", InternalClientID: "id-a",
	}}}
	matcher, err := agentaccessauth.NewCachedExactOriginMatcher(lister, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	policy := agentaccessauth.CORSPolicy{
		Mode: agentaccessauth.CORSModeExact, Matcher: matcher, ClientMatcher: matcher,
	}
	if !policy.Allows("https://app.example.test") {
		t.Fatal("expected client origin allow")
	}
	if policy.Allows("https://evil.example.test") {
		t.Fatal("unexpected evil origin")
	}
	if lister.calls.Load() < 1 {
		t.Fatal("expected origin list load")
	}
}

func TestCachedExactOriginMatcherIsolatesClients(t *testing.T) {
	t.Parallel()
	lister := &staticCORSOriginLister{bindings: []agentaccessauth.CORSOriginBinding{
		{
			Origin: "https://a.example.test", WorkspaceID: "ws-1",
			PublicClientID: "awcl_client_a", InternalClientID: "id-a",
		},
		{
			Origin: "https://b.example.test", WorkspaceID: "ws-1",
			PublicClientID: "awcl_client_b", InternalClientID: "id-b",
		},
		{
			Origin: "https://other-ws.example.test", WorkspaceID: "ws-2",
			PublicClientID: "awcl_client_c", InternalClientID: "id-c",
		},
	}}
	matcher, err := agentaccessauth.NewCachedExactOriginMatcher(lister, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	policy := agentaccessauth.CORSPolicy{
		Mode: agentaccessauth.CORSModeExact, Matcher: matcher, ClientMatcher: matcher,
	}
	// Client A must not receive CORS for Client B's origin (and vice versa).
	if got := policy.ReflectOriginForClient("https://b.example.test", "awcl_client_a"); got != "" {
		t.Fatalf("client A must not reflect B origin: %q", got)
	}
	if got := policy.ReflectOriginForClient("https://a.example.test", "awcl_client_a"); got != "https://a.example.test" {
		t.Fatalf("client A own origin: %q", got)
	}
	if got := policy.ReflectOriginForClient("https://b.example.test", "awcl_client_b"); got != "https://b.example.test" {
		t.Fatalf("client B own origin: %q", got)
	}
	// Workspace isolation for preflight.
	if got := policy.ReflectOriginForWorkspace("https://other-ws.example.test", "ws-1"); got != "" {
		t.Fatalf("ws-1 must not reflect other workspace origin: %q", got)
	}
	if got := policy.ReflectOriginForWorkspace("https://a.example.test", "ws-1"); got != "https://a.example.test" {
		t.Fatalf("ws-1 own origin: %q", got)
	}
}

func TestCachedExactOriginMatcherKeepsLastGoodOnError(t *testing.T) {
	t.Parallel()
	lister := &staticCORSOriginLister{bindings: []agentaccessauth.CORSOriginBinding{{
		Origin: "https://app.example.test", WorkspaceID: "ws-1",
		PublicClientID: "awcl_client_a",
	}}}
	matcher, err := agentaccessauth.NewCachedExactOriginMatcher(lister, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !matcher.AllowsExactOrigin("https://app.example.test") {
		t.Fatal("warm cache")
	}
	lister.err = errors.New("db down")
	lister.bindings = nil
	time.Sleep(3 * time.Millisecond)
	if !matcher.AllowsExactOrigin("https://app.example.test") {
		t.Fatal("expected last-good origin after list failure")
	}
}
