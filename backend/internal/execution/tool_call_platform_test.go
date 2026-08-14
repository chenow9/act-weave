package execution_test

import (
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

func TestMapStartedRejectsEmptyCapabilityReleaseID(t *testing.T) {
	mapper := execution.NewToolCallProtocolMapper()
	inv := execution.ToolInvocation{
		ID:                  uuid.NewString(),
		WorkspaceID:         uuid.NewString(),
		CapabilityReleaseID: "",
		AgentRunID:          uuid.NewString(),
		TraceID:             "trace-platform",
		Status:              "RUNNING",
		StartedAt:           time.Now().UTC(),
	}
	if _, err := mapper.MapStarted(inv, "actweave.publish_attachment"); !errors.Is(err, execution.ErrToolInvocationInvalid) {
		t.Fatalf("empty CapabilityReleaseID err=%v", err)
	}
}

func TestMapStartedAcceptsNilReleaseRUNNINGClone(t *testing.T) {
	mapper := execution.NewToolCallProtocolMapper()
	inv := execution.ToolInvocation{
		ID:                  uuid.NewString(),
		WorkspaceID:         uuid.NewString(),
		CapabilityReleaseID: chatruntime.PlatformPublishReleaseID,
		AgentRunID:          uuid.NewString(),
		TraceID:             "trace-platform",
		Status:              "RUNNING",
		StartedAt:           time.Now().UTC(),
	}
	item, err := mapper.MapStarted(inv, "actweave.publish_attachment")
	if err != nil {
		t.Fatal(err)
	}
	if item.Name != "actweave.publish_attachment" || item.Status != "in_progress" {
		t.Fatalf("item=%+v", item)
	}
}

func TestMapStartedRejectsSucceededWithoutRUNNINGClone(t *testing.T) {
	mapper := execution.NewToolCallProtocolMapper()
	now := time.Now().UTC()
	inv := execution.ToolInvocation{
		ID:                  uuid.NewString(),
		WorkspaceID:         uuid.NewString(),
		CapabilityReleaseID: chatruntime.PlatformPublishReleaseID,
		AgentRunID:          uuid.NewString(),
		TraceID:             "trace-platform",
		Status:              "SUCCEEDED",
		StartedAt:           now,
		FinishedAt:          &now,
	}
	if _, err := mapper.MapStarted(inv, "actweave.publish_attachment"); !errors.Is(err, execution.ErrToolInvocationInvalid) {
		t.Fatalf("SUCCEEDED MapStarted err=%v", err)
	}
}
