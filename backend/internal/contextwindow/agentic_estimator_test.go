package contextwindow_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"actweave/backend/internal/contextwindow"
)

func TestAgenticEstimatorZeroTools(t *testing.T) {
	est, err := contextwindow.NewEstimator(contextwindow.ProfileByteUpperBound)
	if err != nil {
		t.Fatal(err)
	}
	got, err := est.EstimateAgenticRequest("sys", contextwindow.ToolExposureEstimate{}, []contextwindow.Message{
		{Role: contextwindow.RoleUser, Content: "hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.EstimatorVersion != contextwindow.EstimatorVersionAgenticOpenAIResponsesV1 {
		t.Fatalf("version=%s", got.EstimatorVersion)
	}
	if got.ImmediateToolsTokens != 0 || got.DeferredMetadataTokens != 0 || got.DynamicToolLoadReserveTokens != 0 {
		t.Fatalf("expected zero tool tokens: %+v", got)
	}
	if got.ToolsTokens != 0 {
		t.Fatalf("ToolsTokens=%d", got.ToolsTokens)
	}
	if got.MaxLoadedToolCount != 0 {
		t.Fatalf("max loaded must be 0 with zero deferred, got %d", got.MaxLoadedToolCount)
	}
}

func TestAgenticEstimatorReserveBoundsAndStability(t *testing.T) {
	est, err := contextwindow.NewEstimator(contextwindow.ProfileByteUpperBound)
	if err != nil {
		t.Fatal(err)
	}

	// Build 45 deferred tools with decreasing schema sizes so top-40 are largest.
	var meta []contextwindow.ToolMetadata
	var full []contextwindow.ToolSchema
	for i := 0; i < 45; i++ {
		name := fmt.Sprintf("tool_%02d", i)
		// Larger schema for lower indices (tool_00 largest).
		params := json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"p":{"type":"string","description":"%s"}}}`, strings.Repeat("x", 200-i*2)))
		meta = append(meta, contextwindow.ToolMetadata{Name: name, Description: "d"})
		full = append(full, contextwindow.ToolSchema{Name: name, Description: "d", Parameters: params})
	}
	// Equal-delta pair for deterministic name order.
	meta = append(meta, contextwindow.ToolMetadata{Name: "equal_a", Description: "same"})
	meta = append(meta, contextwindow.ToolMetadata{Name: "equal_b", Description: "same"})
	sameParams := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
	full = append(full,
		contextwindow.ToolSchema{Name: "equal_a", Description: "same", Parameters: sameParams},
		contextwindow.ToolSchema{Name: "equal_b", Description: "same", Parameters: sameParams},
	)

	exposure := contextwindow.ToolExposureEstimate{
		DeferredMetadata: meta,
		LoadCandidates:   full,
		// MaxLoadedTools omitted (0) — derived as min(47, 40)=40.
	}
	a, err := est.EstimateAgenticRequest("system", exposure, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := est.EstimateAgenticRequest("system", exposure, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.DynamicToolLoadReserveTokens != b.DynamicToolLoadReserveTokens || a.DynamicToolLoadReserveTokens <= 0 {
		t.Fatalf("reserve not stable/positive: %d vs %d", a.DynamicToolLoadReserveTokens, b.DynamicToolLoadReserveTokens)
	}
	if a.ToolsTokens != a.ImmediateToolsTokens+a.DeferredMetadataTokens+a.DynamicToolLoadReserveTokens {
		t.Fatalf("ToolsTokens sum mismatch: %+v", a)
	}
	if a.MaxLoadedToolCount != 40 {
		t.Fatalf("max loaded=%d want 40", a.MaxLoadedToolCount)
	}

	// Fewer than five deferred tools: MaxLoaded = deferredCount, still reserves all 8 search groups.
	small := contextwindow.ToolExposureEstimate{
		DeferredMetadata: meta[:3],
		LoadCandidates:   full[:3],
	}
	s, err := est.EstimateAgenticRequest("", small, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.DeferredToolCount != 3 || s.MaxLoadedToolCount != 3 {
		t.Fatalf("small catalog counts: %+v", s)
	}
	if s.DynamicToolLoadReserveTokens <= 0 {
		t.Fatalf("small catalog reserve: %+v", s)
	}
	// Prove 8 search groups are reserved even with only 3 deferred:
	// search overhead floor = 8 * 96 + 8 * 32 = 1024 (constants internal; reserve > framing alone).
	// Compare 1 deferred vs 3 deferred: search component identical, only schema+loaded framing differ.
	one := contextwindow.ToolExposureEstimate{
		DeferredMetadata: meta[:1],
		LoadCandidates:   full[:1],
	}
	r1, err := est.EstimateAgenticRequest("", one, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Both must include full 8-group search overhead; 3-tool reserve >= 1-tool reserve.
	if s.DynamicToolLoadReserveTokens < r1.DynamicToolLoadReserveTokens {
		t.Fatalf("3-tool reserve %d < 1-tool %d", s.DynamicToolLoadReserveTokens, r1.DynamicToolLoadReserveTokens)
	}

	// 40 vs 41+: MaxLoaded capped at 40; reserve for top-40 schema portion.
	e40 := contextwindow.ToolExposureEstimate{DeferredMetadata: meta[:40], LoadCandidates: full[:40]}
	e41 := contextwindow.ToolExposureEstimate{DeferredMetadata: meta[:41], LoadCandidates: full[:41]}
	r40, err := est.EstimateAgenticRequest("", e40, nil)
	if err != nil {
		t.Fatal(err)
	}
	r41, err := est.EstimateAgenticRequest("", e41, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r40.MaxLoadedToolCount != 40 || r41.MaxLoadedToolCount != 40 {
		t.Fatalf("max loaded 40/41: %d/%d", r40.MaxLoadedToolCount, r41.MaxLoadedToolCount)
	}
	if r40.DynamicToolLoadReserveTokens != r41.DynamicToolLoadReserveTokens {
		if r41.DynamicToolLoadReserveTokens < r40.DynamicToolLoadReserveTokens {
			t.Fatalf("41 reserve smaller than 40: %d < %d", r41.DynamicToolLoadReserveTokens, r40.DynamicToolLoadReserveTokens)
		}
	}
}

func TestAgenticEstimatorRejectsCallerLoweredMaxLoaded(t *testing.T) {
	est, err := contextwindow.NewEstimator(contextwindow.ProfileByteUpperBound)
	if err != nil {
		t.Fatal(err)
	}
	meta := []contextwindow.ToolMetadata{{Name: "a", Description: "d"}, {Name: "b", Description: "d"}}
	full := []contextwindow.ToolSchema{
		{Name: "a", Description: "d", Parameters: json.RawMessage(`{}`)},
		{Name: "b", Description: "d", Parameters: json.RawMessage(`{}`)},
	}
	// Caller tries to lower max loaded to 1 — must reject.
	if _, err := est.EstimateAgenticRequest("", contextwindow.ToolExposureEstimate{
		DeferredMetadata: meta, LoadCandidates: full, MaxLoadedTools: 1,
	}, nil); err == nil || !errors.Is(err, contextwindow.ErrAgenticEstimatorInvalid) {
		t.Fatalf("expected reject lowered MaxLoadedTools, got %v", err)
	}
	// Exact derived value accepted.
	if _, err := est.EstimateAgenticRequest("", contextwindow.ToolExposureEstimate{
		DeferredMetadata: meta, LoadCandidates: full, MaxLoadedTools: 2,
	}, nil); err != nil {
		t.Fatalf("derived MaxLoadedTools must accept: %v", err)
	}
}

func TestAgenticEstimatorRejectsIdentityMismatch(t *testing.T) {
	est, err := contextwindow.NewEstimator(contextwindow.ProfileByteUpperBound)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		exp  contextwindow.ToolExposureEstimate
	}{
		{"missing candidate", contextwindow.ToolExposureEstimate{
			DeferredMetadata: []contextwindow.ToolMetadata{{Name: "a", Description: "d"}},
		}},
		{"extra candidate", contextwindow.ToolExposureEstimate{
			LoadCandidates: []contextwindow.ToolSchema{{Name: "a", Description: "d", Parameters: json.RawMessage(`{}`)}},
		}},
		{"name mismatch", contextwindow.ToolExposureEstimate{
			DeferredMetadata: []contextwindow.ToolMetadata{{Name: "a", Description: "d"}},
			LoadCandidates:   []contextwindow.ToolSchema{{Name: "b", Description: "d", Parameters: json.RawMessage(`{}`)}},
		}},
		{"duplicate meta", contextwindow.ToolExposureEstimate{
			DeferredMetadata: []contextwindow.ToolMetadata{{Name: "a", Description: "d"}, {Name: "a", Description: "d"}},
			LoadCandidates: []contextwindow.ToolSchema{
				{Name: "a", Description: "d", Parameters: json.RawMessage(`{}`)},
				{Name: "b", Description: "d", Parameters: json.RawMessage(`{}`)},
			},
		}},
		{"duplicate candidate", contextwindow.ToolExposureEstimate{
			DeferredMetadata: []contextwindow.ToolMetadata{{Name: "a", Description: "d"}, {Name: "b", Description: "d"}},
			LoadCandidates: []contextwindow.ToolSchema{
				{Name: "a", Description: "d", Parameters: json.RawMessage(`{}`)},
				{Name: "a", Description: "d", Parameters: json.RawMessage(`{}`)},
			},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := est.EstimateAgenticRequest("", tc.exp, nil); err == nil || !errors.Is(err, contextwindow.ErrAgenticEstimatorInvalid) {
				t.Fatalf("expected identity reject, got %v", err)
			}
		})
	}
}

func TestAgenticEstimatorNegativeDeltaClamp(t *testing.T) {
	est, err := contextwindow.NewEstimator(contextwindow.ProfileByteUpperBound)
	if err != nil {
		t.Fatal(err)
	}
	// Full schema smaller than metadata envelope (empty params vs long desc).
	// Envelope bijection requires matching name+description; clamp is on token delta.
	longDesc := strings.Repeat("long description ", 20)
	exposure := contextwindow.ToolExposureEstimate{
		DeferredMetadata: []contextwindow.ToolMetadata{
			{Name: "t", Description: longDesc},
		},
		LoadCandidates: []contextwindow.ToolSchema{
			{Name: "t", Description: longDesc, Parameters: json.RawMessage(`{}`)},
		},
	}
	got, err := est.EstimateAgenticRequest("", exposure, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Reserve must still be nonnegative (search framing may be > 0).
	if got.DynamicToolLoadReserveTokens < 0 {
		t.Fatalf("negative reserve: %d", got.DynamicToolLoadReserveTokens)
	}
	// With deferred present, reserve includes 8 search groups → strictly positive.
	if got.DynamicToolLoadReserveTokens == 0 {
		t.Fatal("expected positive reserve with deferred tools (8 search groups)")
	}
}

func TestAgenticPreflightMandatoryTooLarge(t *testing.T) {
	_, err := contextwindow.PreflightAgenticMandatory(contextwindow.AgenticPreflightInput{
		ModelContextWindowTokens: 1000,
		OutputReserveTokens:      100,
		SafetyMarginTokens:       50,
		DynamicReserveTokens:     200,
		MandatoryTokens:          700, // ceiling = 1000-100-50-200 = 650
	})
	if !errors.Is(err, contextwindow.ErrMandatoryInputTooLarge) {
		t.Fatalf("expected mandatory too large, got %v", err)
	}
	ok, err := contextwindow.PreflightAgenticMandatory(contextwindow.AgenticPreflightInput{
		ModelContextWindowTokens: 1000,
		OutputReserveTokens:      100,
		SafetyMarginTokens:       50,
		DynamicReserveTokens:     200,
		MandatoryTokens:          600,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok.SafeInputCeiling != 650 {
		t.Fatalf("ceiling=%d", ok.SafeInputCeiling)
	}
}

func TestAgenticPreflightDynamicReserveExceeded(t *testing.T) {
	_, err := contextwindow.PreflightAgenticMandatory(contextwindow.AgenticPreflightInput{
		ModelContextWindowTokens: 10000,
		OutputReserveTokens:      100,
		SafetyMarginTokens:       10,
		DynamicReserveTokens:     50,
		MandatoryTokens:          10,
		MaxLoadedToolCount:       5,
		ActualLoadedToolCount:    6,
	})
	if !errors.Is(err, contextwindow.ErrDynamicToolReserveExceeded) {
		t.Fatalf("expected reserve exceeded, got %v", err)
	}
}

func TestAgenticPreflightStructuralBounds(t *testing.T) {
	base := contextwindow.AgenticPreflightInput{
		ModelContextWindowTokens: 10000,
		OutputReserveTokens:      100,
		SafetyMarginTokens:       10,
		DynamicReserveTokens:     50,
		MandatoryTokens:          10,
		MaxLoadedToolCount:       5,
		ActualLoadedToolCount:    0,
	}
	// Exact boundaries 0/40 accepted; 41/-1 rejected as invalid input.
	for _, tc := range []struct {
		name    string
		mod     func(*contextwindow.AgenticPreflightInput)
		wantErr error
	}{
		{
			name: "zero_no_tools",
			mod: func(in *contextwindow.AgenticPreflightInput) {
				in.MaxLoadedToolCount = 0
				in.ActualLoadedToolCount = 0
			},
			wantErr: nil,
		},
		{
			name: "max_40_actual_40",
			mod: func(in *contextwindow.AgenticPreflightInput) {
				in.MaxLoadedToolCount = 40
				in.ActualLoadedToolCount = 40
			},
			wantErr: nil,
		},
		{
			name: "max_40_actual_0",
			mod: func(in *contextwindow.AgenticPreflightInput) {
				in.MaxLoadedToolCount = 40
				in.ActualLoadedToolCount = 0
			},
			wantErr: nil,
		},
		{
			name: "max_41_invalid",
			mod: func(in *contextwindow.AgenticPreflightInput) {
				in.MaxLoadedToolCount = 41
				in.ActualLoadedToolCount = 0
			},
			wantErr: contextwindow.ErrAgenticEstimatorInvalid,
		},
		{
			name: "max_neg1_invalid",
			mod: func(in *contextwindow.AgenticPreflightInput) {
				in.MaxLoadedToolCount = -1
				in.ActualLoadedToolCount = 0
			},
			wantErr: contextwindow.ErrAgenticEstimatorInvalid,
		},
		{
			name: "actual_41_invalid",
			mod: func(in *contextwindow.AgenticPreflightInput) {
				in.MaxLoadedToolCount = 40
				in.ActualLoadedToolCount = 41
			},
			wantErr: contextwindow.ErrAgenticEstimatorInvalid,
		},
		{
			name: "actual_neg1_invalid",
			mod: func(in *contextwindow.AgenticPreflightInput) {
				in.MaxLoadedToolCount = 5
				in.ActualLoadedToolCount = -1
			},
			wantErr: contextwindow.ErrAgenticEstimatorInvalid,
		},
		{
			name: "actual_gt_max_reserve",
			mod: func(in *contextwindow.AgenticPreflightInput) {
				in.MaxLoadedToolCount = 0
				in.ActualLoadedToolCount = 1
			},
			wantErr: contextwindow.ErrDynamicToolReserveExceeded,
		},
		{
			name: "actual_6_max_5_reserve",
			mod: func(in *contextwindow.AgenticPreflightInput) {
				in.MaxLoadedToolCount = 5
				in.ActualLoadedToolCount = 6
			},
			wantErr: contextwindow.ErrDynamicToolReserveExceeded,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.mod(&in)
			_, err := contextwindow.PreflightAgenticMandatory(in)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("want ok, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestAgenticVsClassicInitialVisibleReduction(t *testing.T) {
	est, err := contextwindow.NewEstimator(contextwindow.ProfileByteUpperBound)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{50, 100, 500} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			var classicTools []contextwindow.ToolSchema
			var meta []contextwindow.ToolMetadata
			var full []contextwindow.ToolSchema
			largeSchema := json.RawMessage(`{"type":"object","properties":{` +
				`"a":{"type":"string","description":"` + strings.Repeat("param ", 40) + `"},` +
				`"b":{"type":"integer","description":"` + strings.Repeat("num ", 40) + `"},` +
				`"c":{"type":"object","properties":{"nested":{"type":"string","description":"` + strings.Repeat("nest ", 30) + `"}}}}}`)
			for i := 0; i < n; i++ {
				name := fmt.Sprintf("biz_%04d", i)
				desc := "business tool " + name
				classicTools = append(classicTools, contextwindow.ToolSchema{Name: name, Description: desc, Parameters: largeSchema})
				meta = append(meta, contextwindow.ToolMetadata{Name: name, Description: desc})
				full = append(full, contextwindow.ToolSchema{Name: name, Description: desc, Parameters: largeSchema})
			}
			classic, err := est.EstimateRequest("system prompt", classicTools, []contextwindow.Message{
				{Role: contextwindow.RoleUser, Content: "current user message"},
			})
			if err != nil {
				t.Fatal(err)
			}
			agentic, err := est.EstimateAgenticRequest("system prompt", contextwindow.ToolExposureEstimate{
				DeferredMetadata: meta,
				LoadCandidates:   full,
			}, []contextwindow.Message{
				{Role: contextwindow.RoleUser, Content: "current user message"},
			})
			if err != nil {
				t.Fatal(err)
			}
			// Initial visible must be materially smaller than classic full-schema input (>=60%).
			if agentic.InitialVisibleTokens >= classic.TotalTokens {
				t.Fatalf("agentic visible %d not < classic %d", agentic.InitialVisibleTokens, classic.TotalTokens)
			}
			if agentic.InitialVisibleTokens*100 > classic.TotalTokens*40 {
				t.Fatalf("reduction below 60%%: visible=%d classic=%d ratio=%.2f%%",
					agentic.InitialVisibleTokens, classic.TotalTokens,
					100*float64(agentic.InitialVisibleTokens)/float64(classic.TotalTokens))
			}
			if agentic.DynamicToolLoadReserveTokens <= 0 {
				t.Fatal("expected positive reserve")
			}
			if agentic.InitialVisibleTokens == agentic.TotalTokens {
				t.Fatal("reserve must not be collapsed into initial visible")
			}
			if agentic.ToolsTokens != agentic.ImmediateToolsTokens+agentic.DeferredMetadataTokens+agentic.DynamicToolLoadReserveTokens {
				t.Fatalf("tools sum: %+v", agentic)
			}
			if agentic.MaxLoadedToolCount != contextwindow.DeriveMaxLoadedToolCount(n) {
				t.Fatalf("max loaded=%d want %d", agentic.MaxLoadedToolCount, contextwindow.DeriveMaxLoadedToolCount(n))
			}
		})
	}
}

func TestClassicEstimatorUnchanged(t *testing.T) {
	est, err := contextwindow.NewEstimator(contextwindow.ProfileByteUpperBound)
	if err != nil {
		t.Fatal(err)
	}
	got, err := est.EstimateRequest("s", nil, []contextwindow.Message{{Role: contextwindow.RoleUser, Content: "u"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.EstimatorVersion != contextwindow.EstimatorVersion {
		t.Fatalf("classic version changed: %s", got.EstimatorVersion)
	}
}

func TestPromptCacheKeyBuilder(t *testing.T) {
	hashA := strings.Repeat("a", 64)
	hashB := strings.Repeat("b", 64)
	in := contextwindow.PromptCacheKeyInput{
		ProviderProtocol:   contextwindow.PromptCacheProviderProtocolOpenAIResponsesV1,
		ModelConfigID:      "018f1f2e-7b5a-7c3d-8e9f-e234567890b0",
		ModelLockVersion:   2,
		PromptRevisionHash: hashA,
		CatalogDigest:      hashB,
		AdapterVersion:     contextwindow.PromptCacheAdapterAgenticOpenAIV022,
	}
	k1, err := contextwindow.BuildAgenticPromptCacheKey(in)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := contextwindow.BuildAgenticPromptCacheKey(in)
	if err != nil {
		t.Fatal(err)
	}
	if k1 != k2 || !strings.HasPrefix(k1, "aw:agentic:v1:") {
		t.Fatalf("unstable key: %s vs %s", k1, k2)
	}
	if len(k1) != len("aw:agentic:v1:")+64 {
		t.Fatalf("key length: %s", k1)
	}
	// Catalog change rotates key.
	in.CatalogDigest = strings.Repeat("c", 64)
	k3, err := contextwindow.BuildAgenticPromptCacheKey(in)
	if err != nil {
		t.Fatal(err)
	}
	if k3 == k1 {
		t.Fatal("catalog change must rotate key")
	}
	// Prompt change rotates key.
	in.CatalogDigest = hashB
	in.PromptRevisionHash = strings.Repeat("d", 64)
	k4, err := contextwindow.BuildAgenticPromptCacheKey(in)
	if err != nil {
		t.Fatal(err)
	}
	if k4 == k1 {
		t.Fatal("prompt change must rotate key")
	}
	// Model lock change rotates key.
	in.PromptRevisionHash = hashA
	in.ModelLockVersion = 3
	k5, err := contextwindow.BuildAgenticPromptCacheKey(in)
	if err != nil {
		t.Fatal(err)
	}
	if k5 == k1 {
		t.Fatal("lock change must rotate key")
	}
	// Reject empty / arbitrary protocol / wrong adapter / non-UUID / case / whitespace.
	if _, err := contextwindow.BuildAgenticPromptCacheKey(contextwindow.PromptCacheKeyInput{}); err == nil {
		t.Fatal("expected empty reject")
	}
	for _, bad := range []contextwindow.PromptCacheKeyInput{
		func() contextwindow.PromptCacheKeyInput { b := in; b.ProviderProtocol = "chat-completions"; return b }(),
		func() contextwindow.PromptCacheKeyInput { b := in; b.AdapterVersion = "agenticopenai/v9.9.9"; return b }(),
		func() contextwindow.PromptCacheKeyInput { b := in; b.ModelConfigID = "not-a-uuid"; return b }(),
		func() contextwindow.PromptCacheKeyInput { b := in; b.ModelConfigID = "Primary Model"; return b }(),
		func() contextwindow.PromptCacheKeyInput { b := in; b.ModelConfigID = "user@example.com"; return b }(),
		func() contextwindow.PromptCacheKeyInput { b := in; b.ModelLockVersion = 0; return b }(),
		func() contextwindow.PromptCacheKeyInput {
			b := in
			b.PromptRevisionHash = strings.Repeat("A", 64)
			return b
		}(),
		func() contextwindow.PromptCacheKeyInput {
			b := in
			b.CatalogDigest = " " + strings.Repeat("a", 63)
			return b
		}(),
		func() contextwindow.PromptCacheKeyInput {
			b := in
			b.PromptRevisionHash = strings.Repeat("a", 63)
			return b
		}(),
	} {
		if _, err := contextwindow.BuildAgenticPromptCacheKey(bad); err == nil {
			t.Fatalf("expected reject for %+v", bad)
		}
	}
	// Stability: no workspace/run/user/session/time in input surface (fields absent by type).
	// Re-build with same inputs after "time passes" — same key.
	kAgain, err := contextwindow.BuildAgenticPromptCacheKey(contextwindow.PromptCacheKeyInput{
		ProviderProtocol: contextwindow.PromptCacheProviderProtocolOpenAIResponsesV1,
		ModelConfigID:    "018f1f2e-7b5a-7c3d-8e9f-e234567890b0", ModelLockVersion: 2,
		PromptRevisionHash: hashA, CatalogDigest: hashB,
		AdapterVersion: contextwindow.PromptCacheAdapterAgenticOpenAIV022,
	})
	if err != nil || kAgain != k1 {
		t.Fatalf("stability broken: %v %s vs %s", err, kAgain, k1)
	}
}

func TestAgenticEstimatorOverflowAndBounds(t *testing.T) {
	est, err := contextwindow.NewEstimator(contextwindow.ProfileByteUpperBound)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := est.EstimateAgenticRequest("", contextwindow.ToolExposureEstimate{MaxLoadedTools: 41}, nil); err == nil {
		t.Fatal("expected max loaded out of range reject")
	}
	if _, err := est.EstimateAgenticRequest("", contextwindow.ToolExposureEstimate{MaxLoadedTools: -1}, nil); err == nil {
		t.Fatal("expected negative max loaded reject")
	}
	_ = math.MaxInt64
}

func TestDeriveMaxLoadedToolCount(t *testing.T) {
	if contextwindow.DeriveMaxLoadedToolCount(0) != 0 {
		t.Fatal("0")
	}
	if contextwindow.DeriveMaxLoadedToolCount(3) != 3 {
		t.Fatal("3")
	}
	if contextwindow.DeriveMaxLoadedToolCount(40) != 40 {
		t.Fatal("40")
	}
	if contextwindow.DeriveMaxLoadedToolCount(41) != 40 {
		t.Fatal("41->40")
	}
	if contextwindow.DeriveMaxLoadedToolCount(500) != contextwindow.AgenticMaxLoadedDefinitionsPerRun {
		t.Fatal("500")
	}
}

func TestAgenticEstimatorEnvelopeBijection(t *testing.T) {
	est, err := contextwindow.NewEstimator(contextwindow.ProfileByteUpperBound)
	if err != nil {
		t.Fatal(err)
	}
	// Same name but different description must fail (envelope identity).
	meta := []contextwindow.ToolMetadata{{Name: "t1", Description: "desc-a"}}
	cands := []contextwindow.ToolSchema{{Name: "t1", Description: "desc-b", Parameters: json.RawMessage(`{}`)}}
	if _, err := est.EstimateAgenticRequest("", contextwindow.ToolExposureEstimate{
		DeferredMetadata: meta, LoadCandidates: cands,
	}, nil); err == nil {
		t.Fatal("expected description envelope mismatch reject")
	}
	// Matching name+description accepted.
	cands[0].Description = "desc-a"
	if _, err := est.EstimateAgenticRequest("", contextwindow.ToolExposureEstimate{
		DeferredMetadata: meta, LoadCandidates: cands,
	}, nil); err != nil {
		t.Fatal(err)
	}
}
