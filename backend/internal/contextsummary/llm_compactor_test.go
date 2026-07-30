package contextsummary

import (
	"context"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/contextwindow"
)

type fakeCompactModel struct {
	out string
	err error
	// last call params
	temp     float64
	maxTok   int
	system   string
	user     string
	calls    int
}

func (f *fakeCompactModel) Generate(_ context.Context, system, user string, temperature float64, maxTokens int) (string, error) {
	f.calls++
	f.system, f.user, f.temp, f.maxTok = system, user, temperature, maxTokens
	if f.err != nil {
		return "", f.err
	}
	return f.out, nil
}

func TestLLMCompactorStrictJSONAndRender(t *testing.T) {
	fake := &fakeCompactModel{out: `{"stableFacts":["order A-1 ready"],"decisions":["ship"],"openItems":[],"recentState":"waiting payment"}`}
	c := &LLMCompactor{Model: fake, MaxTokens: 512}
	res, err := c.Compact(context.Background(), CompactInput{
		Turns: []contextwindow.Turn{{
			User: contextwindow.HistoryMessage{ID: "u1", Content: "status?", ContentHash: "h"},
			Assistants: []contextwindow.HistoryMessage{
				{ID: "a1", Content: "ready", ContentHash: "h"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 || fake.temp != 0 || fake.maxTok != 512 {
		t.Fatalf("model call: %+v", fake)
	}
	if !strings.Contains(string(res.Body), "order A-1 ready") || !strings.Contains(string(res.Body), "稳定事实") {
		t.Fatalf("body=%s", res.Body)
	}
	if strings.Contains(string(res.Body), contextwindow.UntrustedSummaryPrefix) {
		t.Fatal("prefix must not be in stored body")
	}
	if len(res.ContentSHA256) != 64 {
		t.Fatal("digest")
	}
	// Deterministic re-render
	again, _ := c.Compact(context.Background(), CompactInput{
		Turns: []contextwindow.Turn{{
			User:       contextwindow.HistoryMessage{ID: "u1", Content: "status?", ContentHash: "h"},
			Assistants: []contextwindow.HistoryMessage{{ID: "a1", Content: "ready", ContentHash: "h"}},
		}},
	})
	if string(again.Body) != string(res.Body) || again.ContentSHA256 != res.ContentSHA256 {
		t.Fatal("non-deterministic render")
	}
}

func TestLLMCompactorRejectsInvalidOutput(t *testing.T) {
	cases := []string{
		"",
		"not json",
		`{"stableFacts":[],"decisions":[],"openItems":[],"recentState":"","extra":1}`,
		"```json\n{\"stableFacts\":[\"a\"],\"decisions\":[],\"openItems\":[],\"recentState\":\"x\"}\n```",
		`{"stableFacts":[],"decisions":[],"openItems":[],"recentState":""}`,
		`{"stableFacts":["api_key=sk-abcdefghijklmnopqrst"],"decisions":[],"openItems":[],"recentState":"x"}`,
	}
	for _, out := range cases {
		c := &LLMCompactor{Model: &fakeCompactModel{out: out}}
		if _, err := c.Compact(context.Background(), CompactInput{
			Turns: []contextwindow.Turn{{
				User: contextwindow.HistoryMessage{ID: "u", Content: "hi", ContentHash: "h"},
			}},
		}); err == nil {
			t.Fatalf("expected reject for %q", out)
		}
	}
}

func TestLLMCompactorModelError(t *testing.T) {
	c := &LLMCompactor{Model: &fakeCompactModel{err: errors.New("provider down")}}
	if _, err := c.Compact(context.Background(), CompactInput{
		Turns: []contextwindow.Turn{{User: contextwindow.HistoryMessage{ID: "u", Content: "hi", ContentHash: "h"}}},
	}); !errors.Is(err, ErrCompactorModel) {
		t.Fatalf("err=%v", err)
	}
}

func TestExtractiveNotSuccessPath(t *testing.T) {
	// buildExtractiveSummary returns empty and is unused by Generate.
	if body := buildExtractiveSummary(nil); body != "" {
		t.Fatal("extractive must not produce success body")
	}
	g := &Generator{repo: nil, Compactor: nil}
	if _, err := g.Generate(context.Background(), GenerateInput{
		Turns: []contextwindow.Turn{{User: contextwindow.HistoryMessage{ID: "u", Content: "x", ContentHash: "h"}}},
		CoverageEndMessageID: "c08f1f2e-7b5a-7c3d-8e9f-123456789001",
	}); err == nil {
		t.Fatal("expected invalid without repo/compactor")
	}
}
