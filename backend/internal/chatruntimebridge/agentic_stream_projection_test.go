package chatruntimebridge_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/chatruntimebridge"
	"actweave/backend/internal/execution"
)

// lastMessageID is the assistant message identity the run recorded on
// completion — the identity a client uses to attribute the finished item.
func (r *agenticResults) lastMessageID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.messageID
}

// joinedDeltas is what the client actually received, in arrival order.
func joinedDeltas(sink *chatruntimebridge.RecordingTextDeltaSink) string {
	var b strings.Builder
	for _, emission := range sink.Emissions {
		b.WriteString(emission.Text)
	}
	return b.String()
}

// agenticSteps is a permissive StepStore that records which step types the run
// appended, so a test can assert the MODEL evidence exists.
type agenticSteps struct {
	mu    sync.Mutex
	types []string
}

func (s *agenticSteps) AppendAgentRunStep(
	_ context.Context, in execution.AppendAgentRunStepInput,
) (execution.AgentRunStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.types = append(s.types, in.StepType)
	return execution.AgentRunStep{ID: in.ID, RunID: in.RunID, StepType: in.StepType}, nil
}

func (s *agenticSteps) TransitionAgentRunStep(
	_ context.Context, _, stepID string, _ execution.StepTransition,
) (execution.AgentRunStep, error) {
	return execution.AgentRunStep{ID: stepID}, nil
}

func (s *agenticSteps) TransitionAgentRun(
	_ context.Context, _, runID string, _ execution.RunTransition,
) (execution.AgentRun, error) {
	return execution.AgentRun{ID: runID}, nil
}

func (s *agenticSteps) StartWorkflowExecution(
	_ context.Context, _ execution.StartWorkflowExecutionInput,
) (execution.WorkflowExecution, error) {
	return execution.WorkflowExecution{}, nil
}

func (s *agenticSteps) TransitionWorkflowExecution(
	_ context.Context, _, id string, _ execution.RunTransition,
) (execution.WorkflowExecution, error) {
	return execution.WorkflowExecution{ID: id}, nil
}

func (s *agenticSteps) GetAgentRun(
	_ context.Context, _, runID string,
) (execution.AgentRun, error) {
	return execution.AgentRun{ID: runID}, nil
}

func (s *agenticSteps) has(stepType string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.types {
		if t == stepType {
			return true
		}
	}
	return false
}

// agenticModelTurns retains the permanent MODEL audit payloads.
type agenticModelTurns struct {
	mu       sync.Mutex
	payloads []json.RawMessage
}

func (m *agenticModelTurns) Record(
	_ context.Context, in chatruntime.ModelTurnRecordInput,
) (execution.AgentRunStep, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.payloads = append(m.payloads, in.Content)
	return execution.AgentRunStep{ID: in.StepID}, nil
}

func (m *agenticModelTurns) last() (map[string]any, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.payloads) == 0 {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(m.payloads[len(m.payloads)-1], &out); err != nil {
		return nil, false
	}
	return out, true
}

// TestAgenticInitial_StreamsDeltasUnderTheAssistantIdentity is the functional
// hole 4B-3 closes. Task 4A built a StreamDeltaRecorder for the Agentic path and
// then never handed it to the engine: the run completed with the right final
// text, so every 4A test passed, while the client received no item.delta at all
// and the whole answer appeared in one jump at the end. Nothing about a correct
// final answer can detect that, which is why this asserts on the sink rather
// than on the recorded content.
func TestAgenticInitial_StreamsDeltasUnderTheAssistantIdentity(t *testing.T) {
	f := newAgenticFixture(t, nil)
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sink, args, ok := f.sinks.last()
	if !ok {
		t.Fatal("no text sink was opened, so the run streamed nothing")
	}
	if len(sink.Emissions) == 0 {
		t.Fatal("the client received no item.delta: progressive output is missing")
	}
	if got, want := joinedDeltas(sink), f.results.lastContent(); got != want {
		t.Fatalf("streamed %q but recorded %q; the stream and the stored message disagree", got, want)
	}
	if sink.Completion == nil {
		t.Fatal("the stream was never completed")
	}
	// AAP A.1: the deltas, the completion, and the stored assistant message are
	// one item. A client that opened an item under the sink's identity must find
	// that same identity on the message it is told is finished.
	if args.MessageID == "" {
		t.Fatal("the sink was opened without an assistant message identity")
	}
	if got := f.results.lastMessageID(); got != args.MessageID {
		t.Fatalf("deltas streamed under item %q but the run recorded message %q",
			args.MessageID, got)
	}
}

// TestAgenticInitial_RecordsModelTurnEvidence covers the audit half of
// projection. Task 4A wired a ModelTurnHook that could never fire, so MODEL
// steps were absent from the platform-admin timeline for every Agentic run.
//
// Cached prompt tokens are asserted here because the provider reports the cached
// prefix once, on the turn that used it: if the bridge drops the number, no later
// query can recover whether the frozen cache-stable prompt was being rewarded.
func TestAgenticInitial_RecordsModelTurnEvidence(t *testing.T) {
	answer := agenticmsg.AssistantText("agentic-ok")
	answer.ResponseMeta = &schema.AgenticResponseMeta{
		TokenUsage: &schema.TokenUsage{
			PromptTokens: 1000, CompletionTokens: 5, TotalTokens: 1005,
			PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: 896},
		},
	}
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.steps = &agenticSteps{}
		f.modelTurns = &agenticModelTurns{}
		f.mdl.responses = []*schema.AgenticMessage{answer}
	})
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !f.steps.has("MODEL") {
		t.Fatal("the run left no MODEL step: the model turn was never observed")
	}
	payload, ok := f.modelTurns.last()
	if !ok {
		t.Fatal("no MODEL turn evidence was recorded")
	}
	usage, ok := payload["usage"].(map[string]any)
	if !ok {
		t.Fatalf("MODEL evidence carries no usage: %v", payload)
	}
	if got := usage["promptTokens"]; got != float64(1000) {
		t.Fatalf("promptTokens = %v, want 1000", got)
	}
	if got := usage["cachedPromptTokens"]; got != float64(896) {
		t.Fatalf("cachedPromptTokens = %v, want 896; KV-cache evidence was dropped", got)
	}
}

// TestAgenticEngineCallSitesPassAProjector is a structural guard against the
// exact defect Task 4A shipped: a projector was constructed and then simply not
// handed to the engine. The compiler cannot catch it, because Projector is an
// optional field and a nil one is a legal no-projection run — which is what a
// delegation or SmartDAG path added later would silently become.
func TestAgenticEngineCallSitesPassAProjector(t *testing.T) {
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, name := range entries {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(source)
		for _, literal := range []string{
			"einoruntime.AgenticRunInput{", "einoruntime.AgenticResumeInput{",
		} {
			for offset := 0; ; {
				at := strings.Index(text[offset:], literal)
				if at < 0 {
					break
				}
				start := offset + at
				end := start + len(literal)
				depth := 1
				for end < len(text) && depth > 0 {
					switch text[end] {
					case '{':
						depth++
					case '}':
						depth--
					}
					end++
				}
				found++
				if !strings.Contains(text[start:end], "Projector:") {
					t.Errorf("%s: %s built without a Projector, so this turn streams nothing:\n%s",
						name, literal, text[start:end])
				}
				offset = end
			}
		}
	}
	if found == 0 {
		t.Fatal("no agentic engine call sites found; this guard has stopped guarding anything")
	}
}

// TestAgenticResume_StreamsDeltasOnTheResumedTurn covers the path with no
// assembly phase. A resumed turn produces a fresh answer and owes the client the
// same stream as an initial one; because the resume was built separately from
// the initial path, projection could easily be wired on one and not the other.
func TestAgenticResume_StreamsDeltasOnTheResumedTurn(t *testing.T) {
	h := newAgenticHITLFixture(t, []*schema.AgenticMessage{
		agenticToolCall("call_1", "wire_money", `{"q":"x"}`),
		agenticmsg.AssistantText("transfer done"),
	})
	snapshot := h.pause(t)
	if err := h.bridge.ContinueAfterConfirmation(context.Background(), h.job(),
		snapshot, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatalf("ContinueAfterConfirmation: %v", err)
	}

	sink, args, ok := h.f.sinks.last()
	if !ok {
		t.Fatal("the resumed turn opened no text sink")
	}
	if len(sink.Emissions) == 0 {
		t.Fatal("the resumed turn streamed no item.delta")
	}
	if got := joinedDeltas(sink); !strings.Contains(got, "transfer done") {
		t.Fatalf("resumed stream carried %q, want the post-confirmation answer", got)
	}
	if got := h.f.results.lastMessageID(); got != args.MessageID {
		t.Fatalf("resumed deltas streamed under item %q but the run recorded message %q",
			args.MessageID, got)
	}
}
