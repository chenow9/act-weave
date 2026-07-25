package einoruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/compose"
)

func TestToolBudgetMiddleware_StopsAfterCap(t *testing.T) {
	t.Parallel()
	const cap = 16
	mw, counter := NewToolBudgetMiddlewareWithCounter(cap)

	var underlyingCalls int
	endpoint := mw.Invokable(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		underlyingCalls++
		return &compose.ToolOutput{Result: `{"ok":true}`}, nil
	})

	ctx := context.Background()
	for i := 0; i < cap; i++ {
		out, err := endpoint(ctx, &compose.ToolInput{Name: "t", Arguments: `{}`, CallID: "c"})
		if err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		if out == nil || out.Result != `{"ok":true}` {
			t.Fatalf("call %d: expected underlying result, got %#v", i+1, out)
		}
	}
	if underlyingCalls != cap {
		t.Fatalf("underlyingCalls = %d, want %d", underlyingCalls, cap)
	}
	if counter.Count() != cap {
		t.Fatalf("counter = %d, want %d", counter.Count(), cap)
	}

	// 17th call: budget exceeded tool-result JSON, no underlying invoke.
	out, err := endpoint(ctx, &compose.ToolInput{Name: "t", Arguments: `{}`, CallID: "c17"})
	if err != nil {
		t.Fatalf("budget call: %v", err)
	}
	if underlyingCalls != cap {
		t.Fatalf("underlying must not be called past cap; got %d", underlyingCalls)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out.Result), &body); err != nil {
		t.Fatalf("result JSON: %v", err)
	}
	if body["ok"] != false || body["errorCode"] != ToolBudgetExceededCode {
		t.Fatalf("budget result = %v", body)
	}
	if !strings.Contains(fmtString(body["message"]), "budget") {
		t.Fatalf("message = %v", body["message"])
	}
}

func TestToolBudgetMiddleware_DefaultCap(t *testing.T) {
	t.Parallel()
	mw, counter := NewToolBudgetMiddlewareWithCounter(0) // default 16
	endpoint := mw.Invokable(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		return &compose.ToolOutput{Result: "ok"}, nil
	})
	ctx := context.Background()
	for i := 0; i < DefaultMaxToolInvocations+1; i++ {
		_, _ = endpoint(ctx, &compose.ToolInput{Name: "t", Arguments: `{}`})
	}
	if counter.Count() != DefaultMaxToolInvocations {
		t.Fatalf("counter = %d, want %d", counter.Count(), DefaultMaxToolInvocations)
	}
}

func fmtString(v any) string {
	s, _ := v.(string)
	return s
}
