package redisx

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func TestOpenRequiresAddr(t *testing.T) {
	if _, err := Open(context.Background(), Config{}); err == nil {
		t.Fatal("empty addr must fail")
	}
}

func TestOpenPingsAndKeys(t *testing.T) {
	mini := miniredis.RunT(t)
	client, err := Open(context.Background(), Config{Addr: mini.Addr(), KeyPrefix: "aw"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if got := client.Key("live", "x"); got != "aw:live:x" {
		t.Fatalf("key=%s", got)
	}
	if got := client.Channel("cancel"); got != "aw:ch:cancel" {
		t.Fatalf("channel=%s", got)
	}
}

func TestOpenFailsWhenRedisDown(t *testing.T) {
	if _, err := Open(context.Background(), Config{Addr: "127.0.0.1:1"}); err == nil {
		t.Fatal("unreachable redis must fail closed")
	}
}
