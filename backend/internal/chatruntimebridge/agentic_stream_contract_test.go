package chatruntimebridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/eino/schema/openai"
)

// A contract fake for a provider that actually streams (Task 4B-5).
//
// scriptedAgenticModel returns one complete message per turn, so a stream of it
// carries exactly one chunk. Every projection assertion built on it is therefore
// satisfied by a runtime that emits the whole answer as a single delta at the
// end — which is the defect 4B-3 fixed. These tests use chunk sequences shaped
// like a real OpenAI Responses stream: reasoning fragments first, then text
// fragments, with token usage arriving only on the last chunk.

// streamingAgenticModel streams a scripted chunk sequence per turn.
type streamingAgenticModel struct {
	mu    sync.Mutex
	turns [][]*schema.AgenticMessage
	calls atomic.Int64
}

func (m *streamingAgenticModel) next() ([]*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls.Add(1)
	if len(m.turns) == 0 {
		return nil, errors.New("streamingAgenticModel: no more turns")
	}
	turn := m.turns[0]
	m.turns = m.turns[1:]
	return turn, nil
}

func (m *streamingAgenticModel) Generate(
	context.Context, []*schema.AgenticMessage, ...model.Option,
) (*schema.AgenticMessage, error) {
	return nil, errors.New("streamingAgenticModel: this contract fake only streams")
}

func (m *streamingAgenticModel) Stream(
	context.Context, []*schema.AgenticMessage, ...model.Option,
) (*schema.StreamReader[*schema.AgenticMessage], error) {
	chunks, err := m.next()
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray(chunks), nil
}

var _ model.AgenticModel = (*streamingAgenticModel)(nil)

// reasoningThenTextTurn builds one streamed assistant turn the way the provider
// sends it: private reasoning fragments on one content block, public text
// fragments on another, and usage only at the end.
//
// The OpenAI response id on the second chunk is what makes the concatenated
// reasoning self-generated, which complete-message validation requires.
func reasoningThenTextTurn(reasoning string, pieces []string, cachedTokens int) []*schema.AgenticMessage {
	rawIndex := 0
	chunks := []*schema.AgenticMessage{
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlockChunk(&schema.Reasoning{
					Text: reasoning,
					OpenAIExtension: &openai.ReasoningExtension{
						Content: []*openai.ReasoningContent{{Text: reasoning, Index: &rawIndex}},
					},
				}, &schema.StreamingMeta{Index: 0}),
			},
			ResponseMeta: &schema.AgenticResponseMeta{
				OpenAIExtension: &openai.ResponseMetaExtension{ID: "resp_contract_fake"},
			},
		},
	}
	for i, piece := range pieces {
		chunk := &schema.AgenticMessage{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlockChunk(
					&schema.AssistantGenText{Text: piece}, &schema.StreamingMeta{Index: 1}),
			},
		}
		if i == len(pieces)-1 {
			chunk.ResponseMeta = &schema.AgenticResponseMeta{
				TokenUsage: &schema.TokenUsage{
					PromptTokens: 2048, CompletionTokens: 12, TotalTokens: 2060,
					PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: cachedTokens},
				},
			}
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

// TestAgenticStreamContract_DeliversTextProgressively is the assertion a
// single-chunk fake cannot make. A runtime that buffers the whole turn and emits
// it as one delta at the end satisfies "the deltas concatenate to the answer";
// it does not satisfy this.
func TestAgenticStreamContract_DeliversTextProgressively(t *testing.T) {
	pieces := []string{"The ", "order ", "shipped ", "yesterday."}
	streamer := &streamingAgenticModel{
		turns: [][]*schema.AgenticMessage{
			reasoningThenTextTurn("check the order table", pieces, 1792),
		},
	}
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.steps = &agenticSteps{}
		f.modelTurns = &agenticModelTurns{}
		f.agentic.model = streamer
	})
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sink, _, ok := f.sinks.last()
	if !ok {
		t.Fatal("no text sink was opened")
	}
	if len(sink.Emissions) != len(pieces) {
		t.Fatalf("the client received %d deltas for a %d-chunk turn: %q",
			len(sink.Emissions), len(pieces), joinedDeltas(sink))
	}
	for i, piece := range pieces {
		if got := sink.Emissions[i].Text; got != piece {
			t.Errorf("delta %d = %q, want %q; chunk order or boundaries were not preserved",
				i, got, piece)
		}
	}
	want := strings.Join(pieces, "")
	if got := joinedDeltas(sink); got != want {
		t.Fatalf("streamed %q, want %q", got, want)
	}
	if got := f.results.lastContent(); got != want {
		t.Fatalf("recorded %q, want %q", got, want)
	}
}

// TestAgenticStreamContract_NeverStreamsReasoning is the end-to-end form of the
// privacy rule. The provider sends reasoning on the same stream as the answer,
// interleaved on a separate content block; a runtime that forwards chunks
// wholesale would publish the model's private reasoning to the end user.
func TestAgenticStreamContract_NeverStreamsReasoning(t *testing.T) {
	const secret = "check the order table"
	streamer := &streamingAgenticModel{
		turns: [][]*schema.AgenticMessage{
			reasoningThenTextTurn(secret, []string{"Shipped ", "yesterday."}, 0),
		},
	}
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.agentic.model = streamer
	})
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sink, _, ok := f.sinks.last()
	if !ok {
		t.Fatal("no text sink was opened")
	}
	if strings.Contains(joinedDeltas(sink), secret) {
		t.Fatalf("the model's reasoning was streamed to the user: %q", joinedDeltas(sink))
	}
	if sink.Completion != nil && strings.Contains(sink.Completion.Text, secret) {
		t.Fatalf("the model's reasoning landed in the completed item: %q", sink.Completion.Text)
	}
	if got := f.results.lastContent(); strings.Contains(got, secret) {
		t.Fatalf("the model's reasoning was stored as the assistant answer: %q", got)
	}
}

// TestAgenticStreamContract_MergesUsageArrivingOnTheLastChunk covers how usage
// really arrives. A provider reports the cached prompt prefix once, on the final
// chunk of the stream, so the audit evidence has to come from the concatenated
// message rather than from any single chunk.
func TestAgenticStreamContract_MergesUsageArrivingOnTheLastChunk(t *testing.T) {
	streamer := &streamingAgenticModel{
		turns: [][]*schema.AgenticMessage{
			reasoningThenTextTurn("plan", []string{"a", "b", "c"}, 1792),
		},
	}
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.steps = &agenticSteps{}
		f.modelTurns = &agenticModelTurns{}
		f.agentic.model = streamer
	})
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	payload, ok := f.modelTurns.last()
	if !ok {
		t.Fatal("no MODEL turn evidence was recorded for a streamed turn")
	}
	usage, ok := payload["usage"].(map[string]any)
	if !ok {
		t.Fatalf("streamed turn recorded no usage: %v", payload)
	}
	if got := usage["promptTokens"]; got != float64(2048) {
		t.Fatalf("promptTokens = %v, want 2048", got)
	}
	if got := usage["cachedPromptTokens"]; got != float64(1792) {
		t.Fatalf("cachedPromptTokens = %v, want 1792; usage on the last chunk was lost", got)
	}
}

// TestAgenticStreamContract_ResumedTurnStreamsProgressively closes the loop the
// user actually walks: a tool needs approval, the run pauses, the user approves,
// and the answer that follows must stream like any other turn. The resume has no
// assembly phase and was built separately from the initial path, so its
// progressive delivery cannot be inferred from the initial turn's.
func TestAgenticStreamContract_ResumedTurnStreamsProgressively(t *testing.T) {
	pieces := []string{"Transferred ", "42 ", "dollars."}
	streamer := &streamingAgenticModel{
		turns: [][]*schema.AgenticMessage{
			{agenticToolCall("call_1", "wire_money", `{"q":"x"}`)},
			reasoningThenTextTurn("confirm the transfer result", pieces, 0),
		},
	}
	h := newAgenticHITLFixture(t, nil)
	h.f.agentic.model = streamer

	snapshot := h.pause(t)
	if err := h.bridge.ContinueAfterConfirmation(context.Background(), h.job(),
		snapshot, json.RawMessage(`{"ok":true,"transferred":42}`)); err != nil {
		t.Fatalf("ContinueAfterConfirmation: %v", err)
	}

	sink, args, ok := h.f.sinks.last()
	if !ok {
		t.Fatal("the resumed turn opened no text sink")
	}
	if len(sink.Emissions) != len(pieces) {
		t.Fatalf("the resumed turn delivered %d deltas for a %d-chunk answer: %q",
			len(sink.Emissions), len(pieces), joinedDeltas(sink))
	}
	if got, want := joinedDeltas(sink), strings.Join(pieces, ""); got != want {
		t.Fatalf("resumed stream carried %q, want %q", got, want)
	}
	if got := h.f.results.lastMessageID(); got != args.MessageID {
		t.Fatalf("resumed deltas streamed under item %q but the run recorded message %q",
			args.MessageID, got)
	}
}
