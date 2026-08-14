package agentaccessauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/redisx"

	"github.com/alicebob/miniredis/v2"
)

func TestRedisTokenEndpointLimiterSharedAcrossClients(t *testing.T) {
	mini := miniredis.RunT(t)
	rdb, err := redisx.Open(context.Background(), redisx.Config{Addr: mini.Addr(), KeyPrefix: "t"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	cfg := TokenEndpointLimiterConfig{MaxIssues: 2, Window: time.Minute, MaxEntries: 100}
	a, err := NewRedisTokenEndpointLimiter(rdb, cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewRedisTokenEndpointLimiter(rdb, cfg)
	if err != nil {
		t.Fatal(err)
	}
	attempt := TokenIssueAttempt{PublicClientID: "awcl_shared", RemoteIP: "1.1.1.1", GrantType: "client_credentials"}
	if _, err := a.AllowTokenIssue(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AllowTokenIssue(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AllowTokenIssue(context.Background(), attempt); !errors.Is(err, ErrTokenIssueLimited) {
		t.Fatalf("third issue must be limited, got %v", err)
	}
}
