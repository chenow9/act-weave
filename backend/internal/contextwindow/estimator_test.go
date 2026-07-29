package contextwindow_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/contextwindow"
)

func TestLookupUnknownProfileFailsClosed(t *testing.T) {
	_, err := contextwindow.LookupTokenizer("guess-from-model-name")
	if !errors.Is(err, contextwindow.ErrUnknownProfile) {
		t.Fatalf("expected ErrUnknownProfile, got %v", err)
	}
	_, err = contextwindow.NewEstimator("not-registered")
	if !errors.Is(err, contextwindow.ErrUnknownProfile) {
		t.Fatalf("expected ErrUnknownProfile from NewEstimator, got %v", err)
	}
}

func TestO200kGoldenCorpus(t *testing.T) {
	est, err := contextwindow.NewEstimator(contextwindow.ProfileO200kBase)
	if err != nil {
		t.Fatalf("estimator: %v", err)
	}
	cases := []struct {
		name string
		text string
		min  int64
	}{
		{"empty", "", 0},
		{"ascii", "hello world", 1},
		{"cjk", "你好世界上下文窗口管理", 1},
		{"emoji", "😀🚀✨🎉", 1},
		{"long ascii", strings.Repeat("abcdefghijklmnopqrstuvwxyz ", 40), 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, err := est.EstimateText(tc.text)
			if err != nil {
				t.Fatal(err)
			}
			if n < tc.min {
				t.Fatalf("tokens=%d < min %d", n, tc.min)
			}
			// Determinism
			n2, err := est.EstimateText(tc.text)
			if err != nil || n2 != n {
				t.Fatalf("non-deterministic: %d vs %d err=%v", n, n2, err)
			}
		})
	}
}

func TestByteUpperBoundDoesNotUnderestimateVsO200kOnCJK(t *testing.T) {
	exact, err := contextwindow.NewEstimator(contextwindow.ProfileO200kBase)
	if err != nil {
		t.Fatal(err)
	}
	upper, err := contextwindow.NewEstimator(contextwindow.ProfileByteUpperBound)
	if err != nil {
		t.Fatal(err)
	}
	samples := []string{
		"你好，这是一段较长的中文测试文本，用于验证字节上界不会低估。",
		"emoji 混排 😀🚀 and ASCII 12345",
		strings.Repeat("漢字", 100),
	}
	for _, sample := range samples {
		a, err := exact.EstimateText(sample)
		if err != nil {
			t.Fatal(err)
		}
		b, err := upper.EstimateText(sample)
		if err != nil {
			t.Fatal(err)
		}
		if b < a {
			t.Fatalf("byte_upper_bound underestimates: upper=%d exact=%d sample_len=%d", b, a, len(sample))
		}
	}
}

func TestEstimateRequestDeterministicWithTools(t *testing.T) {
	est, err := contextwindow.NewEstimator(contextwindow.ProfileCL100kBase)
	if err != nil {
		t.Fatal(err)
	}
	tools := []contextwindow.ToolSchema{{
		Name: "search", Description: "Search knowledge",
		Parameters: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
	}}
	messages := []contextwindow.Message{
		{Role: contextwindow.RoleUser, Content: "hello"},
		{Role: contextwindow.RoleAssistant, Content: "hi there"},
	}
	a, err := est.EstimateRequest("You are helpful.", tools, messages)
	if err != nil {
		t.Fatal(err)
	}
	b, err := est.EstimateRequest("You are helpful.", tools, messages)
	if err != nil {
		t.Fatal(err)
	}
	if a.TotalTokens != b.TotalTokens || a.TotalTokens <= 0 {
		t.Fatalf("unexpected totals a=%+v b=%+v", a, b)
	}
	if a.EstimatorVersion != contextwindow.EstimatorVersion {
		t.Fatalf("version: %s", a.EstimatorVersion)
	}
	if a.ToolCount != 1 || a.MessageCount != 2 {
		t.Fatalf("counts: %+v", a)
	}
	if a.SystemTokens <= 0 || a.ToolsTokens <= 0 || a.MessagesTokens <= 0 {
		t.Fatalf("component zeros: %+v", a)
	}
}

func TestEstimateNoNegativeOrOverflow(t *testing.T) {
	est, err := contextwindow.NewEstimator(contextwindow.ProfileByteUpperBound)
	if err != nil {
		t.Fatal(err)
	}
	// Property-like: empty pieces stay non-negative.
	result, err := est.EstimateRequest("", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalTokens < 0 || result.SystemTokens < 0 || result.MessagesTokens < 0 {
		t.Fatalf("negative: %+v", result)
	}
	// Large tool schema
	big := json.RawMessage(`{"type":"object","properties":{` + strings.Repeat(`"k":{"type":"string"},`, 200) + `"z":{"type":"string"}}}`)
	result, err = est.EstimateRequest("sys", []contextwindow.ToolSchema{{Name: "t", Description: "d", Parameters: big}},
		[]contextwindow.Message{{Role: contextwindow.RoleUser, Content: strings.Repeat("x", 10000)}})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalTokens <= 0 {
		t.Fatalf("expected positive total, got %+v", result)
	}
}
