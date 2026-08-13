package einoruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

func TestWithVerificationProbe(t *testing.T) {
	t.Parallel()
	if isVerificationProbe(context.Background()) || isVerificationProbe(nil) {
		t.Fatal("unmarked ctx must not be a probe")
	}
	if !isVerificationProbe(WithVerificationProbe(context.Background())) {
		t.Fatal("marked ctx must be a probe")
	}
	if !isVerificationProbe(WithVerificationProbe(nil)) {
		t.Fatal("nil ctx must still mark")
	}
}

func TestClientSearchEarlyFailClosedStillErrors(t *testing.T) {
	t.Parallel()
	cat, err := BuildToolCatalog(context.Background(), []ToolCatalogBuildEntry{
		{Tool: &stubTool{name: "echo_tool", desc: "echo"}, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}
	mw, err := NewBoundedClientToolSearchMiddleware(cat)
	if err != nil {
		t.Fatal(err)
	}
	exec, ok := mw.Executor().(tool.EnhancedInvokableTool)
	if !ok {
		t.Fatal("expected enhanced search executor")
	}
	_, runErr := exec.InvokableRun(context.Background(), nil)
	if !errors.Is(runErr, ErrToolSearchInvalidArgs) {
		t.Fatalf("nil argument: %v", runErr)
	}
	_, runErr = exec.InvokableRun(WithVerificationProbe(context.Background()), nil)
	if !errors.Is(runErr, ErrToolSearchInvalidArgs) {
		t.Fatalf("probe nil argument: %v", runErr)
	}
}
