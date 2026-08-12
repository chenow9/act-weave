package agenticmsg

import (
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/eino/schema/openai"
)

func selfGeneratedAssistant(blocks ...*schema.ContentBlock) *schema.AgenticMessage {
	return &schema.AgenticMessage{
		Role:          schema.AgenticRoleTypeAssistant,
		ContentBlocks: blocks,
		ResponseMeta: &schema.AgenticResponseMeta{
			OpenAIExtension: &openai.ResponseMetaExtension{ID: "resp_test"},
		},
	}
}

func TestConstructors(t *testing.T) {
	t.Parallel()
	sys := System("s")
	if err := Validate(sys); err != nil {
		t.Fatal(err)
	}
	if sys.Role != schema.AgenticRoleTypeSystem || len(sys.ContentBlocks) != 1 ||
		sys.ContentBlocks[0].UserInputText == nil || sys.ContentBlocks[0].UserInputText.Text != "s" {
		t.Fatalf("system: %+v", sys)
	}
	user := UserText("u")
	if err := Validate(user); err != nil {
		t.Fatal(err)
	}
	if user.Role != schema.AgenticRoleTypeUser || user.ContentBlocks[0].UserInputText.Text != "u" {
		t.Fatalf("user: %+v", user)
	}
	asst := AssistantText("a")
	if err := Validate(asst); err != nil {
		t.Fatal(err)
	}
	if asst.Role != schema.AgenticRoleTypeAssistant || asst.ContentBlocks[0].AssistantGenText.Text != "a" {
		t.Fatalf("assistant: %+v", asst)
	}
}

func TestValidateStrict(t *testing.T) {
	t.Parallel()
	if err := Validate(nil); !errors.Is(err, ErrNilMessage) {
		t.Fatalf("nil message: %v", err)
	}
	if err := Validate(&schema.AgenticMessage{Role: ""}); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("empty role: %v", err)
	}
	if err := Validate(&schema.AgenticMessage{Role: schema.AgenticRoleType("tool")}); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("wrong role type: %v", err)
	}
	if err := Validate(&schema.AgenticMessage{
		Role:          schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{nil},
	}); !errors.Is(err, ErrNilBlock) {
		t.Fatalf("nil block: %v", err)
	}
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			{Type: schema.ContentBlockTypeAssistantGenText}, // missing payload
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("malformed: %v", err)
	}
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			{Type: schema.ContentBlockTypeFunctionToolCall}, // missing payload
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("malformed function call: %v", err)
	}
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			{Type: schema.ContentBlockType("not-a-real-type")},
		},
	}); !errors.Is(err, ErrUnsupportedBlock) {
		t.Fatalf("unknown: %v", err)
	}
	// Valid multi-block assistant (self-generated for reasoning).
	if err := Validate(selfGeneratedAssistant(
		schema.NewContentBlock(&schema.Reasoning{Text: "think"}),
		schema.NewContentBlock(&schema.AssistantGenText{Text: "hi"}),
		schema.NewContentBlock(&schema.FunctionToolCall{CallID: "c1", Name: "f", Arguments: `{}`}),
	)); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRoleBlockCompatibility(t *testing.T) {
	t.Parallel()

	// Assistant + user-input text → incompatible.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.UserInputText{Text: "nope"}),
		},
	}); !errors.Is(err, ErrIncompatibleBlock) {
		t.Fatalf("assistant+user_input_text: %v", err)
	}

	// Assistant + function_tool_result → incompatible (user-only).
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolResult{
				CallID: "c1", Name: "f",
				Content: []*schema.FunctionToolResultContentBlock{
					{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: "r"}},
				},
			}),
		},
	}); !errors.Is(err, ErrIncompatibleBlock) {
		t.Fatalf("assistant+tool_result: %v", err)
	}

	// Assistant + tool_search_result → incompatible (user-only).
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.ToolSearchFunctionToolResult{
				CallID: "c1", Name: "tool_search",
				Result: &schema.ToolSearchResult{},
			}),
		},
	}); !errors.Is(err, ErrIncompatibleBlock) {
		t.Fatalf("assistant+tool_search_result: %v", err)
	}

	// User + assistant_gen_text → incompatible.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{Text: "nope"}),
		},
	}); !errors.Is(err, ErrIncompatibleBlock) {
		t.Fatalf("user+assistant_gen_text: %v", err)
	}

	// User + function_tool_call → incompatible (assistant-only).
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{CallID: "c1", Name: "f", Arguments: `{}`}),
		},
	}); !errors.Is(err, ErrIncompatibleBlock) {
		t.Fatalf("user+function_tool_call: %v", err)
	}

	// System + assistant_gen_text → incompatible.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeSystem,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{Text: "nope"}),
		},
	}); !errors.Is(err, ErrIncompatibleBlock) {
		t.Fatalf("system+assistant_gen_text: %v", err)
	}

	// System + function_tool_result → incompatible.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeSystem,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolResult{
				CallID: "c1", Name: "f",
				Content: []*schema.FunctionToolResultContentBlock{
					{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: "x"}},
				},
			}),
		},
	}); !errors.Is(err, ErrIncompatibleBlock) {
		t.Fatalf("system+tool_result: %v", err)
	}

	// Valid user tool result.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolResult{
				CallID: "c1", Name: "lookup",
				Content: []*schema.FunctionToolResultContentBlock{
					{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: "ok"}},
				},
			}),
		},
	}); err != nil {
		t.Fatalf("valid user tool result: %v", err)
	}

	// Valid user tool-search result (zero tools).
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.ToolSearchFunctionToolResult{
				CallID: "c1", Name: "tool_search",
				Result: &schema.ToolSearchResult{},
			}),
		},
	}); err != nil {
		t.Fatalf("valid user tool_search result: %v", err)
	}

	// Valid assistant function call.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{CallID: "c1", Name: "f", Arguments: `{}`}),
		},
	}); err != nil {
		t.Fatalf("valid assistant function call: %v", err)
	}
}

// TestValidateNarrowProtocolSurface rejects deliberately unsupported top-level
// types and accepts the production multi-turn function/tool-search flow.
func TestValidateNarrowProtocolSurface(t *testing.T) {
	t.Parallel()

	// --- Rejected top-level types (even if Eino / full converter support them) ---
	rejected := []struct {
		name  string
		role  schema.AgenticRoleType
		block *schema.ContentBlock
	}{
		{
			name:  "user audio",
			role:  schema.AgenticRoleTypeUser,
			block: schema.NewContentBlock(&schema.UserInputAudio{URL: "http://x/a.wav"}),
		},
		{
			name:  "user video",
			role:  schema.AgenticRoleTypeUser,
			block: schema.NewContentBlock(&schema.UserInputVideo{URL: "http://x/v.mp4"}),
		},
		{
			name:  "assistant gen image",
			role:  schema.AgenticRoleTypeAssistant,
			block: schema.NewContentBlock(&schema.AssistantGenImage{URL: "http://x/i.png"}),
		},
		{
			name:  "assistant gen audio",
			role:  schema.AgenticRoleTypeAssistant,
			block: schema.NewContentBlock(&schema.AssistantGenAudio{URL: "http://x/a.wav"}),
		},
		{
			name:  "assistant gen video",
			role:  schema.AgenticRoleTypeAssistant,
			block: schema.NewContentBlock(&schema.AssistantGenVideo{URL: "http://x/v.mp4"}),
		},
		{
			name: "server tool call",
			role: schema.AgenticRoleTypeAssistant,
			block: schema.NewContentBlock(&schema.ServerToolCall{
				Name: "web_search", CallID: "s1",
			}),
		},
		{
			name: "server tool result",
			role: schema.AgenticRoleTypeAssistant,
			block: schema.NewContentBlock(&schema.ServerToolResult{
				Name: "web_search", CallID: "s1",
			}),
		},
		{
			name: "mcp tool call",
			role: schema.AgenticRoleTypeAssistant,
			block: schema.NewContentBlock(&schema.MCPToolCall{
				ServerLabel: "srv", Name: "t", CallID: "m1",
			}),
		},
		{
			name: "mcp tool result",
			role: schema.AgenticRoleTypeAssistant,
			block: schema.NewContentBlock(&schema.MCPToolResult{
				ServerLabel: "srv", Name: "t", CallID: "m1",
			}),
		},
		{
			name: "mcp list tools",
			role: schema.AgenticRoleTypeAssistant,
			block: schema.NewContentBlock(&schema.MCPListToolsResult{
				ServerLabel: "srv",
			}),
		},
		{
			name: "mcp approval request",
			role: schema.AgenticRoleTypeAssistant,
			block: schema.NewContentBlock(&schema.MCPToolApprovalRequest{
				ID: "a1", ServerLabel: "srv", Name: "t",
			}),
		},
		{
			name: "mcp approval response",
			role: schema.AgenticRoleTypeUser,
			block: schema.NewContentBlock(&schema.MCPToolApprovalResponse{
				ApprovalRequestID: "a1", Approve: true,
			}),
		},
		{
			name:  "system user audio",
			role:  schema.AgenticRoleTypeSystem,
			block: schema.NewContentBlock(&schema.UserInputAudio{URL: "http://x/a.wav"}),
		},
		{
			name:  "user assistant gen image",
			role:  schema.AgenticRoleTypeUser,
			block: schema.NewContentBlock(&schema.AssistantGenImage{URL: "http://x/i.png"}),
		},
	}
	for _, tc := range rejected {
		tc := tc
		t.Run("reject_"+tc.name, func(t *testing.T) {
			t.Parallel()
			err := Validate(&schema.AgenticMessage{
				Role:          tc.role,
				ContentBlocks: []*schema.ContentBlock{tc.block},
			})
			if !errors.Is(err, ErrUnsupportedBlock) {
				t.Fatalf("want ErrUnsupportedBlock, got %v", err)
			}
		})
	}

	// --- Accepted production flow variants ---
	accepted := []struct {
		name string
		msg  *schema.AgenticMessage
	}{
		{name: "system text", msg: System("rules")},
		{
			name: "system image",
			msg: &schema.AgenticMessage{
				Role: schema.AgenticRoleTypeSystem,
				ContentBlocks: []*schema.ContentBlock{
					schema.NewContentBlock(&schema.UserInputImage{URL: "http://x/i.png"}),
				},
			},
		},
		{name: "user text", msg: UserText("hi")},
		{
			name: "user image url",
			msg: &schema.AgenticMessage{
				Role: schema.AgenticRoleTypeUser,
				ContentBlocks: []*schema.ContentBlock{
					schema.NewContentBlock(&schema.UserInputImage{URL: "http://x/i.png"}),
				},
			},
		},
		{
			name: "user image base64",
			msg: &schema.AgenticMessage{
				Role: schema.AgenticRoleTypeUser,
				ContentBlocks: []*schema.ContentBlock{
					schema.NewContentBlock(&schema.UserInputImage{
						Base64Data: "aaaa", MIMEType: "image/png",
					}),
				},
			},
		},
		{
			name: "user file",
			msg: &schema.AgenticMessage{
				Role: schema.AgenticRoleTypeUser,
				ContentBlocks: []*schema.ContentBlock{
					schema.NewContentBlock(&schema.UserInputFile{URL: "http://x/f.pdf", Name: "f.pdf"}),
				},
			},
		},
		{
			name: "user function tool result text",
			msg: &schema.AgenticMessage{
				Role: schema.AgenticRoleTypeUser,
				ContentBlocks: []*schema.ContentBlock{
					schema.NewContentBlock(&schema.FunctionToolResult{
						CallID: "c1", Name: "lookup",
						Content: []*schema.FunctionToolResultContentBlock{
							{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: `{"ok":true}`}},
						},
					}),
				},
			},
		},
		{
			name: "user tool search zero tools",
			msg: &schema.AgenticMessage{
				Role: schema.AgenticRoleTypeUser,
				ContentBlocks: []*schema.ContentBlock{
					schema.NewContentBlock(&schema.ToolSearchFunctionToolResult{
						CallID: "ts1", Name: "tool_search",
						Result: &schema.ToolSearchResult{Tools: nil},
					}),
				},
			},
		},
		{
			name: "user tool search with tools",
			msg: &schema.AgenticMessage{
				Role: schema.AgenticRoleTypeUser,
				ContentBlocks: []*schema.ContentBlock{
					schema.NewContentBlock(&schema.ToolSearchFunctionToolResult{
						CallID: "ts1", Name: "tool_search",
						Result: &schema.ToolSearchResult{
							Tools: []*schema.ToolInfo{
								{Name: "lookup", Desc: "look up"},
								{
									Name: "search", Desc: "search",
									ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
										"q": {Type: schema.String, Desc: "query"},
									}),
								},
							},
						},
					}),
				},
			},
		},
		{name: "assistant text", msg: AssistantText("hello")},
		{
			name: "assistant self-generated reasoning",
			msg: selfGeneratedAssistant(
				schema.NewContentBlock(&schema.Reasoning{Text: "think"}),
			),
		},
		{
			name: "assistant function call",
			msg: &schema.AgenticMessage{
				Role: schema.AgenticRoleTypeAssistant,
				ContentBlocks: []*schema.ContentBlock{
					schema.NewContentBlock(&schema.FunctionToolCall{CallID: "c1", Name: "f", Arguments: `{}`}),
				},
			},
		},
		{
			name: "production multi-block",
			msg: selfGeneratedAssistant(
				schema.NewContentBlock(&schema.Reasoning{Text: "plan"}),
				schema.NewContentBlock(&schema.AssistantGenText{Text: "calling"}),
				schema.NewContentBlock(&schema.FunctionToolCall{CallID: "c1", Name: "lookup", Arguments: `{"q":"x"}`}),
			),
		},
	}
	for _, tc := range accepted {
		tc := tc
		t.Run("accept_"+tc.name, func(t *testing.T) {
			t.Parallel()
			if err := Validate(tc.msg); err != nil {
				t.Fatalf("expected valid: %v", err)
			}
		})
	}
}

func TestValidateReasoningRequiresSelfGenerated(t *testing.T) {
	t.Parallel()

	// Complete reasoning without OpenAIExtension → fail (would be silently skipped by adapter).
	err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.Reasoning{Text: "think"}),
		},
	})
	if !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("non-self-generated reasoning: %v", err)
	}

	// StreamingMeta does NOT relax complete Validate (strict complete semantics).
	streamReasoning := schema.NewContentBlockChunk(&schema.Reasoning{Text: "partial"}, &schema.StreamingMeta{Index: 0})
	if err := Validate(&schema.AgenticMessage{
		Role:          schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{streamReasoning},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("Validate must reject incomplete reasoning even with StreamingMeta: %v", err)
	}

	// Self-generated → accept under complete Validate.
	if err := Validate(selfGeneratedAssistant(
		schema.NewContentBlock(&schema.Reasoning{Text: "think"}),
	)); err != nil {
		t.Fatal(err)
	}

	// Stream fragment validation may omit OpenAIExtension only with StreamingMeta.
	if err := ValidateStreamChunk(&schema.AgenticMessage{
		Role:          schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{streamReasoning},
	}); err != nil {
		t.Fatalf("ValidateStreamChunk reasoning fragment: %v", err)
	}
}

// TestValidateStreamChunkRequiresStreamingMetaProvenance ensures completeness
// relaxation is gated on ContentBlock.StreamingMeta, not merely validation mode.
func TestValidateStreamChunkRequiresStreamingMetaProvenance(t *testing.T) {
	t.Parallel()

	// Incomplete function call without StreamingMeta must fail even via stream API.
	incompleteCallNoMeta := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{Arguments: `{"q":`}),
		},
	}
	if err := ValidateStreamChunk(incompleteCallNoMeta); !errors.Is(err, ErrMalformedBlock) && !errors.Is(err, ErrInvalidToolArguments) {
		t.Fatalf("incomplete call without StreamingMeta: want typed fail, got %v", err)
	}

	// Missing CallID/Name without marker → complete-rule failure.
	partialIDNoMeta := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{Name: "f", Arguments: `{}`}),
		},
	}
	if err := ValidateStreamChunk(partialIDNoMeta); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("missing CallID without meta: %v", err)
	}

	// Same incomplete call WITH StreamingMeta is accepted as a raw chunk.
	if err := ValidateStreamChunk(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlockChunk(&schema.FunctionToolCall{Arguments: `{"q":`}, &schema.StreamingMeta{Index: 0}),
		},
	}); err != nil {
		t.Fatalf("incomplete call with StreamingMeta: %v", err)
	}

	// Reasoning without OpenAI meta and without StreamingMeta fails stream API.
	reasoningNoMeta := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.Reasoning{Text: "think"}),
		},
	}
	if err := ValidateStreamChunk(reasoningNoMeta); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("reasoning without StreamingMeta: %v", err)
	}

	// Already-complete, strictly valid blocks do not require StreamingMeta on the stream API.
	if err := ValidateStreamChunk(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{CallID: "c1", Name: "f", Arguments: `{}`}),
		},
	}); err != nil {
		t.Fatalf("complete call without meta via stream API: %v", err)
	}
	if err := ValidateStreamChunk(selfGeneratedAssistant(
		schema.NewContentBlock(&schema.Reasoning{Text: "think"}),
	)); err != nil {
		t.Fatalf("complete self-generated reasoning without meta via stream API: %v", err)
	}

	// ConcatStream rejects incomplete no-marker chunks at pre-concat phase.
	_, err := ConcatStream([]*schema.AgenticMessage{incompleteCallNoMeta})
	if err == nil || (!errors.Is(err, ErrMalformedBlock) && !errors.Is(err, ErrInvalidToolArguments)) {
		t.Fatalf("ConcatStream incomplete no-meta: %v", err)
	}
	if !strings.Contains(err.Error(), "concat chunk") {
		t.Fatalf("expected pre-concat phase error, got %v", err)
	}
}

func TestValidateReasoningOpenAIContentNils(t *testing.T) {
	t.Parallel()

	// Nil OpenAI reasoning content entries are silently dropped by the converter;
	// complete validation must typed-fail.
	msg := selfGeneratedAssistant(
		schema.NewContentBlock(&schema.Reasoning{
			Text: "summary",
			OpenAIExtension: &openai.ReasoningExtension{
				Content: []*openai.ReasoningContent{nil, {Text: "raw"}},
			},
		}),
	)
	if err := Validate(msg); !errors.Is(err, ErrNilBlock) {
		t.Fatalf("nil reasoning content: %v", err)
	}

	// Valid non-nil content entries are accepted.
	msg = selfGeneratedAssistant(
		schema.NewContentBlock(&schema.Reasoning{
			Text: "summary",
			OpenAIExtension: &openai.ReasoningExtension{
				Content: []*openai.ReasoningContent{{Text: "raw0"}, {Text: "raw1"}},
			},
		}),
	)
	if err := Validate(msg); err != nil {
		t.Fatalf("valid reasoning content: %v", err)
	}
}

func TestValidateNestedFunctionToolResultContent(t *testing.T) {
	t.Parallel()

	userWith := func(content ...*schema.FunctionToolResultContentBlock) *schema.AgenticMessage {
		return &schema.AgenticMessage{
			Role: schema.AgenticRoleTypeUser,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.FunctionToolResult{
					CallID: "c1", Name: "tool",
					Content: content,
				}),
			},
		}
	}

	// Empty content → ErrMalformedBlock (converter rejects empty).
	if err := Validate(userWith()); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("empty content: %v", err)
	}

	// Nil nested entry → ErrNilBlock (no panic).
	if err := Validate(userWith(nil)); !errors.Is(err, ErrNilBlock) {
		t.Fatalf("nil nested: %v", err)
	}

	// Missing payload for declared type.
	if err := Validate(userWith(&schema.FunctionToolResultContentBlock{
		Type: schema.FunctionToolResultContentBlockTypeText,
	})); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("missing text payload: %v", err)
	}

	// Nested image/file rejected (text-only foundation).
	if err := Validate(userWith(&schema.FunctionToolResultContentBlock{
		Type:  schema.FunctionToolResultContentBlockTypeImage,
		Image: &schema.UserInputImage{URL: "http://x/i.png"},
	})); !errors.Is(err, ErrUnsupportedBlock) {
		t.Fatalf("nested image: %v", err)
	}
	if err := Validate(userWith(&schema.FunctionToolResultContentBlock{
		Type: schema.FunctionToolResultContentBlockTypeFile,
		File: &schema.UserInputFile{URL: "http://x/f.pdf", Name: "f.pdf"},
	})); !errors.Is(err, ErrUnsupportedBlock) {
		t.Fatalf("nested file: %v", err)
	}

	// Extra payload (text + image for type text).
	if err := Validate(userWith(&schema.FunctionToolResultContentBlock{
		Type:  schema.FunctionToolResultContentBlockTypeText,
		Text:  &schema.UserInputText{Text: "ok"},
		Image: &schema.UserInputImage{URL: "http://x/i.png"},
	})); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("extra payload: %v", err)
	}

	// Unsupported nested types (audio/video) and unknown.
	if err := Validate(userWith(&schema.FunctionToolResultContentBlock{
		Type:  schema.FunctionToolResultContentBlockTypeAudio,
		Audio: &schema.UserInputAudio{URL: "http://x/a.wav"},
	})); !errors.Is(err, ErrUnsupportedBlock) {
		t.Fatalf("nested audio: %v", err)
	}
	if err := Validate(userWith(&schema.FunctionToolResultContentBlock{
		Type:  schema.FunctionToolResultContentBlockTypeVideo,
		Video: &schema.UserInputVideo{URL: "http://x/v.mp4"},
	})); !errors.Is(err, ErrUnsupportedBlock) {
		t.Fatalf("nested video: %v", err)
	}
	if err := Validate(userWith(&schema.FunctionToolResultContentBlock{
		Type: schema.FunctionToolResultContentBlockType("weird"),
		Text: &schema.UserInputText{Text: "x"},
	})); !errors.Is(err, ErrUnsupportedBlock) {
		t.Fatalf("nested unknown: %v", err)
	}

	// Valid text-only tool result.
	if err := Validate(userWith(
		&schema.FunctionToolResultContentBlock{
			Type: schema.FunctionToolResultContentBlockTypeText,
			Text: &schema.UserInputText{Text: "result"},
		},
	)); err != nil {
		t.Fatalf("valid text tool result: %v", err)
	}

	// Empty / whitespace-only nested text rejected.
	if err := Validate(userWith(&schema.FunctionToolResultContentBlock{
		Type: schema.FunctionToolResultContentBlockTypeText,
		Text: &schema.UserInputText{Text: ""},
	})); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("empty nested text: %v", err)
	}
	if err := Validate(userWith(&schema.FunctionToolResultContentBlock{
		Type: schema.FunctionToolResultContentBlockTypeText,
		Text: &schema.UserInputText{Text: "  \t\n"},
	})); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("whitespace nested text: %v", err)
	}

	// Missing CallID.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolResult{
				Name: "tool",
				Content: []*schema.FunctionToolResultContentBlock{
					{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: "x"}},
				},
			}),
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("missing call id: %v", err)
	}
}

func TestValidateToolSearchResultNested(t *testing.T) {
	t.Parallel()

	// Nil Result → fail (converter: "tool search result should not be nil").
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.ToolSearchFunctionToolResult{
				CallID: "c1", Name: "tool_search",
			}),
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("nil Result: %v", err)
	}

	// Nil tool entry → ErrNilBlock (upstream panics).
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.ToolSearchFunctionToolResult{
				CallID: "c1", Name: "tool_search",
				Result: &schema.ToolSearchResult{
					Tools: []*schema.ToolInfo{nil},
				},
			}),
		},
	}); !errors.Is(err, ErrNilBlock) {
		t.Fatalf("nil tool: %v", err)
	}

	// Empty tool name.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.ToolSearchFunctionToolResult{
				CallID: "c1", Name: "tool_search",
				Result: &schema.ToolSearchResult{
					Tools: []*schema.ToolInfo{{Name: "", Desc: "x"}},
				},
			}),
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("empty tool name: %v", err)
	}

	// Empty tools slice is valid.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.ToolSearchFunctionToolResult{
				CallID: "c1", Name: "tool_search",
				Result: &schema.ToolSearchResult{Tools: []*schema.ToolInfo{}},
			}),
		},
	}); err != nil {
		t.Fatalf("empty tools: %v", err)
	}
}

func TestValidateMediaSource(t *testing.T) {
	t.Parallel()

	// Image missing both URL and base64.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.UserInputImage{}),
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("image empty: %v", err)
	}

	// Base64 without MIME.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.UserInputImage{Base64Data: "aaaa"}),
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("image base64 no mime: %v", err)
	}

	// data: prefix in base64 field.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.UserInputImage{
				Base64Data: "data:image/png;base64,aaaa", MIMEType: "image/png",
			}),
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("image data prefix: %v", err)
	}

	// Invalid detail.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.UserInputImage{
				URL: "http://x/i.png", Detail: schema.ImageURLDetail("ultra"),
			}),
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("invalid detail: %v", err)
	}

	// File missing source.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.UserInputFile{Name: "f.pdf"}),
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("file empty: %v", err)
	}
}

func TestValidateFunctionToolCallFields(t *testing.T) {
	t.Parallel()
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{Name: "f", Arguments: `{}`}),
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("missing CallID: %v", err)
	}
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{CallID: "c1", Arguments: `{}`}),
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("missing Name: %v", err)
	}
}

func TestValidateFunctionToolCallJSONArguments(t *testing.T) {
	t.Parallel()

	mustReject := func(name, args string) {
		t.Helper()
		err := Validate(&schema.AgenticMessage{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.FunctionToolCall{CallID: "c1", Name: "f", Arguments: args}),
			},
		})
		if !errors.Is(err, ErrInvalidToolArguments) {
			t.Fatalf("%s: want ErrInvalidToolArguments, got %v", name, err)
		}
		// Never embed raw arguments (may contain secrets) in the error text.
		if strings.Contains(err.Error(), args) && strings.TrimSpace(args) != "" {
			// Allow short structural fragments that also appear in generic messages;
			// reject when the full args payload is echoed.
			if len(strings.TrimSpace(args)) > 8 && strings.Contains(err.Error(), strings.TrimSpace(args)) {
				t.Fatalf("%s: error embeds raw args: %v", name, err)
			}
		}
	}

	mustReject("empty", "")
	mustReject("whitespace", "   ")
	mustReject("null", "null")
	mustReject("array", `[]`)
	mustReject("string", `"x"`)
	mustReject("number", `1`)
	mustReject("trailing", `{}{}`)
	mustReject("trailing after object", `{"a":1} true`)
	mustReject("incomplete", `{"a":`)
	mustReject("not json", `{a:1}`)

	// Duplicate keys at every nesting level (encoding/json keeps last; we reject).
	mustReject("top-level duplicate", `{"a":1,"a":2}`)
	mustReject("nested duplicate", `{"outer":{"a":1,"a":2}}`)
	mustReject("duplicate inside array object", `{"items":[{"a":1,"a":2}]}`)
	mustReject("escaped-equivalent keys", `{"a":1,"\u0061":2}`)
	mustReject("deep nested duplicate", `{"x":{"y":[{"z":{"k":1,"k":2}}]}}`)

	// Valid object forms (including empty object and same key names in sibling objects).
	for _, args := range []string{
		`{}`,
		`{"q":"x"}`,
		"  {\"n\":1}  ",
		`{"a":{"x":1},"b":{"x":2}}`,   // repeated key name in distinct siblings
		`{"items":[{"a":1},{"a":2}]}`, // same key in distinct array elements
		`{"a":1,"b":{"a":2}}`,         // same key at different nesting levels
		`{"unicode":"\u0061"}`,        // escaped value is fine
	} {
		if err := Validate(&schema.AgenticMessage{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.FunctionToolCall{CallID: "c1", Name: "f", Arguments: args}),
			},
		}); err != nil {
			t.Fatalf("valid args %q: %v", args, err)
		}
	}

	// Stream fragments may carry incomplete argument deltas only with StreamingMeta.
	if err := ValidateStreamChunk(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlockChunk(&schema.FunctionToolCall{Arguments: `{"q":`}, &schema.StreamingMeta{Index: 0}),
		},
	}); err != nil {
		t.Fatalf("stream partial args: %v", err)
	}
}

func TestValidateAssistantGenTextAnnotations(t *testing.T) {
	t.Parallel()

	// Nil annotation.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{
				Text: "hi",
				OpenAIExtension: &openai.AssistantGenTextExtension{
					Annotations: []*openai.TextAnnotation{nil},
				},
			}),
		},
	}); !errors.Is(err, ErrNilBlock) {
		t.Fatalf("nil annotation: %v", err)
	}

	// Missing citation payload.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{
				Text: "hi",
				OpenAIExtension: &openai.AssistantGenTextExtension{
					Annotations: []*openai.TextAnnotation{
						{Type: openai.TextAnnotationTypeURLCitation},
					},
				},
			}),
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("missing citation: %v", err)
	}

	// Valid annotation.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{
				Text: "hi",
				OpenAIExtension: &openai.AssistantGenTextExtension{
					Annotations: []*openai.TextAnnotation{
						{
							Type: openai.TextAnnotationTypeURLCitation,
							URLCitation: &openai.TextAnnotationURLCitation{
								URL: "http://example.com", Title: "ex",
							},
						},
					},
				},
			}),
		},
	}); err != nil {
		t.Fatalf("valid annotation: %v", err)
	}

	// Extra payload for declared type → exclusive union failure.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{
				Text: "hi",
				OpenAIExtension: &openai.AssistantGenTextExtension{
					Annotations: []*openai.TextAnnotation{
						{
							Type: openai.TextAnnotationTypeURLCitation,
							URLCitation: &openai.TextAnnotationURLCitation{
								URL: "http://example.com", Title: "ex",
							},
							FileCitation: &openai.TextAnnotationFileCitation{FileID: "f"},
						},
					},
				},
			}),
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("extra annotation payload: %v", err)
	}
}

func TestValidateAssistantGenTextRefusalRejected(t *testing.T) {
	t.Parallel()

	// Text + non-empty refusal must not pass Validate (would otherwise drop refusal).
	textPlusRefusal := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{
				Text: "partial answer",
				OpenAIExtension: &openai.AssistantGenTextExtension{
					Refusal: &openai.OutputRefusal{Reason: "policy"},
				},
			}),
		},
	}
	if err := Validate(textPlusRefusal); !errors.Is(err, ErrUnsupportedBlock) {
		t.Fatalf("text+refusal Validate: %v", err)
	}
	if _, err := ExtractAssistantText(textPlusRefusal); !errors.Is(err, ErrUnsupportedBlock) {
		t.Fatalf("text+refusal ExtractAssistantText: %v", err)
	}

	// Refusal-only (empty text, non-nil Refusal) also fails closed.
	refusalOnly := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{
				OpenAIExtension: &openai.AssistantGenTextExtension{
					Refusal: &openai.OutputRefusal{Reason: "refused"},
				},
			}),
		},
	}
	if err := Validate(refusalOnly); !errors.Is(err, ErrUnsupportedBlock) {
		t.Fatalf("refusal-only Validate: %v", err)
	}
	if _, err := ExtractAssistantText(refusalOnly); !errors.Is(err, ErrUnsupportedBlock) {
		t.Fatalf("refusal-only ExtractAssistantText: %v", err)
	}

	// Empty-reason / zero-value Refusal pointer is still present → reject.
	emptyReason := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{
				Text: "hi",
				OpenAIExtension: &openai.AssistantGenTextExtension{
					Refusal: &openai.OutputRefusal{},
				},
			}),
		},
	}
	if err := Validate(emptyReason); !errors.Is(err, ErrUnsupportedBlock) {
		t.Fatalf("empty-reason refusal Validate: %v", err)
	}

	// Nil Refusal with normal text annotations remains accepted.
	annotated := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{
				Text: "cited",
				OpenAIExtension: &openai.AssistantGenTextExtension{
					Annotations: []*openai.TextAnnotation{
						{
							Type: openai.TextAnnotationTypeURLCitation,
							URLCitation: &openai.TextAnnotationURLCitation{
								URL: "http://example.com", Title: "ex",
							},
						},
					},
				},
			}),
		},
	}
	if err := Validate(annotated); err != nil {
		t.Fatalf("annotations-only Validate: %v", err)
	}
	got, err := ExtractAssistantText(annotated)
	if err != nil || got != "cited" {
		t.Fatalf("annotations-only ExtractAssistantText: got=%q err=%v", got, err)
	}

	// Nil OpenAIExtension / plain text still accepted.
	if err := Validate(AssistantText("plain")); err != nil {
		t.Fatalf("plain text: %v", err)
	}

	// ConcatStream rejects completed output that carries Refusal (post-concat Validate).
	// Stream fragment with StreamingMeta + refusal is also rejected at pre-concat.
	refusalChunk := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlockChunk(&schema.AssistantGenText{
				Text: "x",
				OpenAIExtension: &openai.AssistantGenTextExtension{
					Refusal: &openai.OutputRefusal{Reason: "no"},
				},
			}, &schema.StreamingMeta{Index: 0}),
		},
	}
	if _, err := ConcatStream([]*schema.AgenticMessage{refusalChunk}); !errors.Is(err, ErrUnsupportedBlock) {
		t.Fatalf("ConcatStream refusal chunk: %v", err)
	}

	// Complete non-stream message with refusal fails ConcatStream pre-concat too.
	if _, err := ConcatStream([]*schema.AgenticMessage{textPlusRefusal}); !errors.Is(err, ErrUnsupportedBlock) {
		t.Fatalf("ConcatStream complete refusal: %v", err)
	}
}

func TestValidateUnionExclusivity(t *testing.T) {
	t.Parallel()

	// Declared assistant_gen_text plus an extra mismatched payload.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			{
				Type:             schema.ContentBlockTypeAssistantGenText,
				AssistantGenText: &schema.AssistantGenText{Text: "hi"},
				UserInputText:    &schema.UserInputText{Text: "extra"},
			},
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("extra payload: %v", err)
	}

	// Declared function_tool_call plus extra reasoning payload.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			{
				Type:             schema.ContentBlockTypeFunctionToolCall,
				FunctionToolCall: &schema.FunctionToolCall{CallID: "c1", Name: "f", Arguments: `{}`},
				Reasoning:        &schema.Reasoning{Text: "extra"},
			},
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("extra reasoning payload: %v", err)
	}

	// Missing expected payload with an unrelated payload set still fails closed
	// as missing expected (or malformed).
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			{
				Type:          schema.ContentBlockTypeAssistantGenText,
				UserInputText: &schema.UserInputText{Text: "wrong field"},
			},
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("missing expected with wrong field: %v", err)
	}
}

// markToolSearchCall sets the pinned adapter's tool-search marker on a call block.
func markToolSearchCall(block *schema.ContentBlock) *schema.ContentBlock {
	if block.Extra == nil {
		block.Extra = map[string]any{}
	}
	block.Extra["openai-tool-search-tool-call"] = true
	return block
}

func functionResult(callID, name, text string) *schema.AgenticMessage {
	return &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolResult{
				CallID: callID, Name: name,
				Content: []*schema.FunctionToolResultContentBlock{
					{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: text}},
				},
			}),
		},
	}
}

func TestValidateConversation(t *testing.T) {
	t.Parallel()

	if err := ValidateConversation([]*schema.AgenticMessage{nil}); !errors.Is(err, ErrNilMessage) {
		t.Fatalf("nil entry: %v", err)
	}

	// Unpaired function tool result.
	if err := ValidateConversation([]*schema.AgenticMessage{
		UserText("hi"),
		functionResult("missing", "f", "x"),
	}); !errors.Is(err, ErrUnpairedToolResult) {
		t.Fatalf("unpaired result: %v", err)
	}

	// Valid production flow: call then result; trailing call OK.
	call := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{CallID: "c1", Name: "lookup", Arguments: `{}`}),
		},
	}
	result := functionResult("c1", "lookup", "ok")
	pending := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{CallID: "c2", Name: "other", Arguments: `{}`}),
		},
	}
	if err := ValidateConversation([]*schema.AgenticMessage{
		System("rules"), UserText("hi"), call, result, pending,
	}); err != nil {
		t.Fatalf("valid conversation: %v", err)
	}

	// Tool search result pairing requires the adapter tool-search marker.
	tsCallBlock := markToolSearchCall(schema.NewContentBlock(&schema.FunctionToolCall{
		CallID: "ts1", Name: "tool_search", Arguments: `{"query":"x"}`,
	}))
	tsCall := &schema.AgenticMessage{
		Role:          schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{tsCallBlock},
	}
	tsResult := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.ToolSearchFunctionToolResult{
				CallID: "ts1", Name: "tool_search",
				Result: &schema.ToolSearchResult{},
			}),
		},
	}
	if err := ValidateConversation([]*schema.AgenticMessage{tsCall, tsResult}); err != nil {
		t.Fatalf("tool search pairing: %v", err)
	}
	if err := ValidateConversation([]*schema.AgenticMessage{tsResult}); !errors.Is(err, ErrUnpairedToolResult) {
		t.Fatalf("unpaired tool search: %v", err)
	}

	// Unmarked function call named tool_search is ordinary; tool-search result is wrong kind.
	unmarked := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{
				CallID: "ts2", Name: "tool_search", Arguments: `{"query":"x"}`,
			}),
		},
	}
	if err := ValidateConversation([]*schema.AgenticMessage{
		unmarked,
		&schema.AgenticMessage{
			Role: schema.AgenticRoleTypeUser,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.ToolSearchFunctionToolResult{
					CallID: "ts2", Name: "tool_search",
					Result: &schema.ToolSearchResult{},
				}),
			},
		},
	}); !errors.Is(err, ErrWrongKindToolResult) {
		t.Fatalf("unmarked tool_search + tool_search_result: %v", err)
	}

	// Tool-search call cannot be consumed by ordinary function result.
	if err := ValidateConversation([]*schema.AgenticMessage{
		tsCall,
		functionResult("ts1", "tool_search", "nope"),
	}); !errors.Is(err, ErrWrongKindToolResult) {
		t.Fatalf("tool-search call + function result: %v", err)
	}
}

func TestValidateConversationLedger(t *testing.T) {
	t.Parallel()

	// Duplicate call IDs (same kind).
	dup := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{CallID: "c1", Name: "a", Arguments: `{}`}),
			schema.NewContentBlock(&schema.FunctionToolCall{CallID: "c1", Name: "b", Arguments: `{}`}),
		},
	}
	if err := ValidateConversation([]*schema.AgenticMessage{dup}); !errors.Is(err, ErrDuplicateCallID) {
		t.Fatalf("duplicate call: %v", err)
	}

	// Repeated function result.
	call := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{CallID: "c1", Name: "a", Arguments: `{}`}),
		},
	}
	if err := ValidateConversation([]*schema.AgenticMessage{
		call, functionResult("c1", "a", "1"), functionResult("c1", "a", "2"),
	}); !errors.Is(err, ErrRepeatedToolResult) {
		t.Fatalf("repeated result: %v", err)
	}

	// Interleaved multi-call / multi-result / multi-turn.
	turn1Call := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{CallID: "f1", Name: "lookup", Arguments: `{"q":"a"}`}),
			markToolSearchCall(schema.NewContentBlock(&schema.FunctionToolCall{
				CallID: "ts1", Name: "tool_search", Arguments: `{"query":"x"}`,
			})),
		},
	}
	turn1Results := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.ToolSearchFunctionToolResult{
				CallID: "ts1", Name: "tool_search",
				Result: &schema.ToolSearchResult{
					Tools: []*schema.ToolInfo{{Name: "lookup", Desc: "d"}},
				},
			}),
			schema.NewContentBlock(&schema.FunctionToolResult{
				CallID: "f1", Name: "lookup",
				Content: []*schema.FunctionToolResultContentBlock{
					{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: `{"ok":1}`}},
				},
			}),
		},
	}
	turn2Call := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{CallID: "f2", Name: "lookup", Arguments: `{"q":"b"}`}),
		},
	}
	if err := ValidateConversation([]*schema.AgenticMessage{
		System("rules"), UserText("hi"), turn1Call, turn1Results, turn2Call,
	}); err != nil {
		t.Fatalf("interleaved multi-turn: %v", err)
	}

	// Tool-search name mismatch (alias ErrToolSearchNameMismatch / ErrToolResultNameMismatch).
	tsCall := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			markToolSearchCall(schema.NewContentBlock(&schema.FunctionToolCall{
				CallID: "ts9", Name: "my_tool_search", Arguments: `{}`,
			})),
		},
	}
	if err := ValidateConversation([]*schema.AgenticMessage{
		tsCall,
		&schema.AgenticMessage{
			Role: schema.AgenticRoleTypeUser,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.ToolSearchFunctionToolResult{
					CallID: "ts9", Name: "other_name",
					Result: &schema.ToolSearchResult{},
				}),
			},
		},
	}); !errors.Is(err, ErrToolSearchNameMismatch) || !errors.Is(err, ErrToolResultNameMismatch) {
		t.Fatalf("name mismatch: %v", err)
	}
}

// TestValidateResultNameInvariants covers blank and mismatched Names for both
// ordinary function results and tool-search results, plus valid matches and
// trim-exact (no case-fold) policy.
func TestValidateResultNameInvariants(t *testing.T) {
	t.Parallel()

	// Blank Name rejected at complete Validate for ordinary function result.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolResult{
				CallID: "c1", Name: "",
				Content: []*schema.FunctionToolResultContentBlock{
					{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: "x"}},
				},
			}),
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("blank function result Name: %v", err)
	}
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolResult{
				CallID: "c1", Name: "   ",
				Content: []*schema.FunctionToolResultContentBlock{
					{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: "x"}},
				},
			}),
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("whitespace function result Name: %v", err)
	}

	// Blank Name rejected for tool-search result.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.ToolSearchFunctionToolResult{
				CallID: "ts1", Name: "",
				Result: &schema.ToolSearchResult{},
			}),
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("blank tool-search result Name: %v", err)
	}

	// Ordinary function result name mismatch.
	call := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{CallID: "c1", Name: "lookup", Arguments: `{}`}),
		},
	}
	if err := ValidateConversation([]*schema.AgenticMessage{
		call, functionResult("c1", "other", "ok"),
	}); !errors.Is(err, ErrToolResultNameMismatch) {
		t.Fatalf("function result name mismatch: %v", err)
	}

	// Case differs → mismatch (no silent case-fold).
	if err := ValidateConversation([]*schema.AgenticMessage{
		call, functionResult("c1", "Lookup", "ok"),
	}); !errors.Is(err, ErrToolResultNameMismatch) {
		t.Fatalf("case-fold not allowed: %v", err)
	}

	// Trim-exact match is valid (surrounding whitespace on result Name).
	if err := ValidateConversation([]*schema.AgenticMessage{
		call,
		&schema.AgenticMessage{
			Role: schema.AgenticRoleTypeUser,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.FunctionToolResult{
					CallID: "c1", Name: "  lookup  ",
					Content: []*schema.FunctionToolResultContentBlock{
						{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: "ok"}},
					},
				}),
			},
		},
	}); err != nil {
		t.Fatalf("trim match: %v", err)
	}

	// Valid exact match for ordinary function.
	if err := ValidateConversation([]*schema.AgenticMessage{
		call, functionResult("c1", "lookup", "ok"),
	}); err != nil {
		t.Fatalf("valid function name match: %v", err)
	}

	// Tool-search blank name is already rejected by Validate; mismatch covered above.
	// Valid tool-search name match with surrounding whitespace on call side ledger trim.
	tsCall := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			markToolSearchCall(schema.NewContentBlock(&schema.FunctionToolCall{
				CallID: "ts1", Name: "  tool_search  ", Arguments: `{}`,
			})),
		},
	}
	if err := ValidateConversation([]*schema.AgenticMessage{
		tsCall,
		&schema.AgenticMessage{
			Role: schema.AgenticRoleTypeUser,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.ToolSearchFunctionToolResult{
					CallID: "ts1", Name: "tool_search",
					Result: &schema.ToolSearchResult{},
				}),
			},
		},
	}); err != nil {
		t.Fatalf("tool-search trim match: %v", err)
	}

	// Multiple interleaved calls: each result must match its own call name.
	multi := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{CallID: "a", Name: "alpha", Arguments: `{}`}),
			schema.NewContentBlock(&schema.FunctionToolCall{CallID: "b", Name: "beta", Arguments: `{}`}),
		},
	}
	if err := ValidateConversation([]*schema.AgenticMessage{
		multi,
		functionResult("b", "beta", "2"),
		functionResult("a", "alpha", "1"),
	}); err != nil {
		t.Fatalf("interleaved name matches: %v", err)
	}
	if err := ValidateConversation([]*schema.AgenticMessage{
		multi,
		functionResult("b", "alpha", "wrong"), // wrong name for call b
	}); !errors.Is(err, ErrToolResultNameMismatch) {
		t.Fatalf("interleaved mismatch: %v", err)
	}
}

func TestExtractAssistantText(t *testing.T) {
	t.Parallel()
	if _, err := ExtractAssistantText(nil); !errors.Is(err, ErrNilMessage) {
		t.Fatalf("nil: %v", err)
	}
	if _, err := ExtractAssistantText(UserText("x")); !errors.Is(err, ErrWrongRole) {
		t.Fatalf("wrong role: %v", err)
	}
	if _, err := ExtractAssistantText(selfGeneratedAssistant(
		schema.NewContentBlock(&schema.Reasoning{Text: "think"}),
	)); !errors.Is(err, ErrNoAssistantText) {
		t.Fatalf("no text: %v", err)
	}

	// Multi-block text concat; reasoning/function companions allowed.
	msg := selfGeneratedAssistant(
		schema.NewContentBlock(&schema.Reasoning{Text: "plan"}),
		schema.NewContentBlock(&schema.AssistantGenText{Text: "hel"}),
		schema.NewContentBlock(&schema.AssistantGenText{Text: "lo"}),
		schema.NewContentBlock(&schema.FunctionToolCall{CallID: "c1", Name: "f", Arguments: `{}`}),
	)
	got, err := ExtractAssistantText(msg)
	if err != nil || got != "hello" {
		t.Fatalf("got=%q err=%v", got, err)
	}

	// Provider-unsupported assistant gen image is rejected at Validate.
	if _, err := ExtractAssistantText(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenImage{URL: "http://x"}),
		},
	}); !errors.Is(err, ErrUnsupportedBlock) {
		t.Fatalf("image: %v", err)
	}

	// Malformed known block fails closed (no silent skip).
	if _, err := ExtractAssistantText(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			{Type: schema.ContentBlockTypeAssistantGenText},
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("malformed: %v", err)
	}

	// Nil block fails closed.
	if _, err := ExtractAssistantText(&schema.AgenticMessage{
		Role:          schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{nil},
	}); !errors.Is(err, ErrNilBlock) {
		t.Fatalf("nil block: %v", err)
	}

	// Unknown type fails closed.
	if _, err := ExtractAssistantText(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			{Type: schema.ContentBlockType("not-a-real-type")},
		},
	}); !errors.Is(err, ErrUnsupportedBlock) || !strings.Contains(err.Error(), "unknown type") {
		t.Fatalf("unknown: %v", err)
	}

	// User-only blocks on assistant fail at Validate (not silently ignored).
	if _, err := ExtractAssistantText(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{Text: "hi"}),
			schema.NewContentBlock(&schema.UserInputText{Text: "sneak"}),
		},
	}); !errors.Is(err, ErrIncompatibleBlock) {
		t.Fatalf("assistant with user_input: %v", err)
	}
	if _, err := ExtractAssistantText(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{Text: "hi"}),
			schema.NewContentBlock(&schema.FunctionToolResult{
				CallID: "c1", Name: "f",
				Content: []*schema.FunctionToolResultContentBlock{
					{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: "x"}},
				},
			}),
		},
	}); !errors.Is(err, ErrIncompatibleBlock) {
		t.Fatalf("assistant with tool_result: %v", err)
	}
}

func TestExtractReasoningAndFunctionCalls(t *testing.T) {
	t.Parallel()
	if _, err := ExtractReasoningText(nil); !errors.Is(err, ErrNilMessage) {
		t.Fatalf("reasoning nil: %v", err)
	}
	if _, err := ExtractReasoningText(UserText("x")); !errors.Is(err, ErrWrongRole) {
		t.Fatalf("reasoning wrong role: %v", err)
	}
	if _, err := FunctionCalls(nil); !errors.Is(err, ErrNilMessage) {
		t.Fatalf("calls nil: %v", err)
	}
	if _, err := FunctionCalls(UserText("x")); !errors.Is(err, ErrWrongRole) {
		t.Fatalf("calls wrong role: %v", err)
	}

	// Malformed known blocks fail closed (no silent skip).
	if _, err := FunctionCalls(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			{Type: schema.ContentBlockTypeFunctionToolCall},
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("malformed call: %v", err)
	}
	if _, err := ExtractReasoningText(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			{Type: schema.ContentBlockTypeReasoning},
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("malformed reasoning: %v", err)
	}

	msg := selfGeneratedAssistant(
		schema.NewContentBlock(&schema.Reasoning{Text: "r1"}),
		schema.NewContentBlock(&schema.Reasoning{Text: "r2"}),
		schema.NewContentBlock(&schema.FunctionToolCall{CallID: "1", Name: "a", Arguments: `{"x":1}`}),
		schema.NewContentBlock(&schema.FunctionToolCall{CallID: "2", Name: "b", Arguments: `{}`}),
		schema.NewContentBlock(&schema.AssistantGenText{Text: "done"}),
	)
	got, err := ExtractReasoningText(msg)
	if err != nil || got != "r1r2" {
		t.Fatalf("reasoning=%q err=%v", got, err)
	}
	calls, err := FunctionCalls(msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0].Name != "a" || calls[1].Name != "b" {
		t.Fatalf("calls=%+v", calls)
	}
}

func TestUsage(t *testing.T) {
	t.Parallel()

	// Nil fails closed.
	if _, err := Usage(nil); !errors.Is(err, ErrNilMessage) {
		t.Fatalf("nil: %v", err)
	}

	// Invalid role fails closed.
	if _, err := Usage(&schema.AgenticMessage{Role: schema.AgenticRoleType("tool")}); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("invalid role: %v", err)
	}

	// Malformed fails closed.
	if _, err := Usage(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			{Type: schema.ContentBlockTypeAssistantGenText},
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("malformed: %v", err)
	}

	// Role-incompatible fails closed.
	if _, err := Usage(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.UserInputText{Text: "x"}),
		},
	}); !errors.Is(err, ErrIncompatibleBlock) {
		t.Fatalf("incompatible: %v", err)
	}

	// Valid message, no usage meta → zero, nil error.
	msg := AssistantText("x")
	u, err := Usage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if u != (TokenUsage{}) {
		t.Fatalf("expected zero usage, got %+v", u)
	}

	// Full usage meta.
	msg.ResponseMeta = &schema.AgenticResponseMeta{
		TokenUsage: &schema.TokenUsage{
			PromptTokens:     11,
			CompletionTokens: 7,
			TotalTokens:      18,
			PromptTokenDetails: schema.PromptTokenDetails{
				CachedTokens: 4,
			},
			CompletionTokensDetails: schema.CompletionTokensDetails{
				ReasoningTokens: 3,
			},
		},
	}
	u, err = Usage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if u.PromptTokens != 11 || u.CompletionTokens != 7 || u.TotalTokens != 18 ||
		u.CachedTokens != 4 || u.ReasoningTokens != 3 {
		t.Fatalf("%+v", u)
	}
}

func TestConcatStream(t *testing.T) {
	t.Parallel()
	if _, err := ConcatStream(nil); !errors.Is(err, ErrEmptyConcat) {
		t.Fatalf("nil: %v", err)
	}
	if _, err := ConcatStream([]*schema.AgenticMessage{}); !errors.Is(err, ErrEmptyConcat) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := ConcatStream([]*schema.AgenticMessage{nil, nil}); !errors.Is(err, ErrNilChunk) {
		t.Fatalf("all nil: %v", err)
	}
	if _, err := ConcatStream([]*schema.AgenticMessage{
		AssistantText("a"),
		nil,
	}); !errors.Is(err, ErrNilChunk) {
		t.Fatalf("middle nil: %v", err)
	}
	// Malformed chunk fails closed.
	if _, err := ConcatStream([]*schema.AgenticMessage{
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				{Type: schema.ContentBlockTypeAssistantGenText},
			},
		},
	}); !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("malformed concat: %v", err)
	}

	// Stream-style text fragments with streaming meta indexes.
	chunks := []*schema.AgenticMessage{
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlockChunk(&schema.AssistantGenText{Text: "Hel"}, &schema.StreamingMeta{Index: 0}),
			},
		},
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlockChunk(&schema.AssistantGenText{Text: "lo"}, &schema.StreamingMeta{Index: 0}),
			},
			ResponseMeta: &schema.AgenticResponseMeta{
				TokenUsage: &schema.TokenUsage{
					PromptTokens:     5,
					CompletionTokens: 2,
					TotalTokens:      7,
					PromptTokenDetails: schema.PromptTokenDetails{
						CachedTokens: 1,
					},
					CompletionTokensDetails: schema.CompletionTokensDetails{
						ReasoningTokens: 1,
					},
				},
			},
		},
	}
	out, err := ConcatStream(chunks)
	if err != nil {
		t.Fatal(err)
	}
	text, err := ExtractAssistantText(out)
	if err != nil || text != "Hello" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	u, err := Usage(out)
	if err != nil {
		t.Fatal(err)
	}
	if u.PromptTokens != 5 || u.CachedTokens != 1 || u.ReasoningTokens != 1 {
		t.Fatalf("usage=%+v", u)
	}
}

func TestConcatStreamTypedErrors(t *testing.T) {
	t.Parallel()

	// Mixed roles → ErrConcat (upstream "cannot concat messages with different roles").
	_, err := ConcatStream([]*schema.AgenticMessage{
		AssistantText("a"),
		UserText("b"),
	})
	if !errors.Is(err, ErrConcat) {
		t.Fatalf("mixed roles: %v", err)
	}
	if errors.Is(err, ErrEmptyConcat) || errors.Is(err, ErrNilChunk) {
		t.Fatalf("mixed roles should not match pre-concat sentinels: %v", err)
	}

	// Another upstream concat failure: streaming block after non-streaming.
	_, err = ConcatStream([]*schema.AgenticMessage{
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.AssistantGenText{Text: "full"}),
			},
		},
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlockChunk(&schema.AssistantGenText{Text: "more"}, &schema.StreamingMeta{Index: 0}),
			},
		},
	})
	if !errors.Is(err, ErrConcat) {
		t.Fatalf("mixed streaming: %v", err)
	}

	// Successful concat still works (single chunk path).
	out, err := ConcatStream([]*schema.AgenticMessage{AssistantText("only")})
	if err != nil {
		t.Fatal(err)
	}
	text, err := ExtractAssistantText(out)
	if err != nil || text != "only" {
		t.Fatalf("text=%q err=%v", text, err)
	}

	// Pre-concat validation sentinel is preserved (not wrapped only as ErrConcat).
	_, err = ConcatStream([]*schema.AgenticMessage{
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				{Type: schema.ContentBlockTypeAssistantGenText}, // missing payload
			},
		},
	})
	if !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("malformed should remain ErrMalformedBlock: %v", err)
	}
}

func TestConcatStreamReasoningSelfGenerated(t *testing.T) {
	t.Parallel()

	// Positive: realistic stream fragments concatenate into a complete
	// self-generated reasoning message when OpenAI metadata arrives.
	idx := 0
	chunks := []*schema.AgenticMessage{
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlockChunk(&schema.Reasoning{
					Text: "plan",
					OpenAIExtension: &openai.ReasoningExtension{
						Content: []*openai.ReasoningContent{
							{Text: "raw", Index: &idx},
						},
					},
				}, &schema.StreamingMeta{Index: 0}),
			},
		},
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlockChunk(&schema.Reasoning{
					Text: " step",
				}, &schema.StreamingMeta{Index: 0}),
			},
			ResponseMeta: &schema.AgenticResponseMeta{
				OpenAIExtension: &openai.ResponseMetaExtension{ID: "resp_stream"},
			},
		},
	}
	out, err := ConcatStream(chunks)
	if err != nil {
		t.Fatalf("positive reasoning concat: %v", err)
	}
	if !isOpenAISelfGenerated(out) {
		t.Fatal("expected self-generated output")
	}
	got, err := ExtractReasoningText(out)
	if err != nil || got != "plan step" {
		t.Fatalf("reasoning=%q err=%v", got, err)
	}

	// Negative: OpenAI metadata never arrives → post-concat Validate fails.
	_, err = ConcatStream([]*schema.AgenticMessage{
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlockChunk(&schema.Reasoning{Text: "partial"}, &schema.StreamingMeta{Index: 0}),
			},
		},
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlockChunk(&schema.Reasoning{Text: " more"}, &schema.StreamingMeta{Index: 0}),
			},
		},
	})
	if !errors.Is(err, ErrMalformedBlock) {
		t.Fatalf("missing final OpenAI meta: %v", err)
	}
	if !strings.Contains(err.Error(), "concat output") {
		t.Fatalf("expected post-concat phase error, got %v", err)
	}
}

// TestStreamOnlyIndexesRejectCompleteAndRequireStreamingMetaProvenance covers
// TextAnnotation.Index and ReasoningContent.Index: stream-assembly fields that
// the pinned adapter drops on replay. Complete Validate / ValidateConversation
// reject them; ValidateStreamChunk permits them only with StreamingMeta.
func TestStreamOnlyIndexesRejectCompleteAndRequireStreamingMetaProvenance(t *testing.T) {
	t.Parallel()

	urlAnno := func(index int) *openai.TextAnnotation {
		return &openai.TextAnnotation{
			Index: index,
			Type:  openai.TextAnnotationTypeURLCitation,
			URLCitation: &openai.TextAnnotationURLCitation{
				URL: "http://example.com", Title: "ex",
			},
		}
	}

	// --- TextAnnotation.Index != 0 ---

	// Complete message with non-default annotation index → ErrStreamOnlyField.
	completeIndexedAnno := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{
				Text: "cited",
				OpenAIExtension: &openai.AssistantGenTextExtension{
					Annotations: []*openai.TextAnnotation{urlAnno(1)},
				},
			}),
		},
	}
	if err := Validate(completeIndexedAnno); !errors.Is(err, ErrStreamOnlyField) {
		t.Fatalf("complete TextAnnotation.Index: want ErrStreamOnlyField, got %v", err)
	}
	// ValidateConversation uses Validate.
	if err := ValidateConversation([]*schema.AgenticMessage{completeIndexedAnno}); !errors.Is(err, ErrStreamOnlyField) {
		t.Fatalf("ValidateConversation TextAnnotation.Index: %v", err)
	}
	// Extract helpers fail closed via Validate.
	if _, err := ExtractAssistantText(completeIndexedAnno); !errors.Is(err, ErrStreamOnlyField) {
		t.Fatalf("ExtractAssistantText TextAnnotation.Index: %v", err)
	}

	// Default Index 0 remains accepted on complete messages.
	if err := Validate(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{
				Text: "cited",
				OpenAIExtension: &openai.AssistantGenTextExtension{
					Annotations: []*openai.TextAnnotation{urlAnno(0)},
				},
			}),
		},
	}); err != nil {
		t.Fatalf("complete annotation Index=0: %v", err)
	}

	// Stream API without StreamingMeta still rejects non-default Index.
	if err := ValidateStreamChunk(completeIndexedAnno); !errors.Is(err, ErrStreamOnlyField) {
		t.Fatalf("stream API no meta TextAnnotation.Index: %v", err)
	}

	// Authentic stream fragment with StreamingMeta permits indexed annotation.
	streamAnno := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlockChunk(&schema.AssistantGenText{
				Text: "cited",
				OpenAIExtension: &openai.AssistantGenTextExtension{
					Annotations: []*openai.TextAnnotation{urlAnno(3)},
				},
			}, &schema.StreamingMeta{Index: 0}),
		},
	}
	if err := ValidateStreamChunk(streamAnno); err != nil {
		t.Fatalf("stream fragment indexed annotation: %v", err)
	}
	// Complete Validate still rejects even with StreamingMeta on the block.
	if err := Validate(streamAnno); !errors.Is(err, ErrStreamOnlyField) {
		t.Fatalf("Validate with StreamingMeta still rejects stream-only Index: %v", err)
	}

	// --- ReasoningContent.Index != nil ---

	idx := 0
	completeIndexedReasoning := selfGeneratedAssistant(
		schema.NewContentBlock(&schema.Reasoning{
			Text: "summary",
			OpenAIExtension: &openai.ReasoningExtension{
				Content: []*openai.ReasoningContent{
					{Text: "raw", Index: &idx},
				},
			},
		}),
	)
	if err := Validate(completeIndexedReasoning); !errors.Is(err, ErrStreamOnlyField) {
		t.Fatalf("complete ReasoningContent.Index: want ErrStreamOnlyField, got %v", err)
	}
	if err := ValidateConversation([]*schema.AgenticMessage{completeIndexedReasoning}); !errors.Is(err, ErrStreamOnlyField) {
		t.Fatalf("ValidateConversation ReasoningContent.Index: %v", err)
	}
	// Non-nil Index at zero still stream-only (not the nil default).
	if err := ValidateStreamChunk(completeIndexedReasoning); !errors.Is(err, ErrStreamOnlyField) {
		t.Fatalf("stream API no meta ReasoningContent.Index: %v", err)
	}

	// Nil Index (default) accepted on complete self-generated reasoning.
	if err := Validate(selfGeneratedAssistant(
		schema.NewContentBlock(&schema.Reasoning{
			Text: "summary",
			OpenAIExtension: &openai.ReasoningExtension{
				Content: []*openai.ReasoningContent{{Text: "raw"}},
			},
		}),
	)); err != nil {
		t.Fatalf("complete reasoning nil Index: %v", err)
	}

	// Stream fragment with StreamingMeta permits non-nil ReasoningContent.Index.
	streamReasoningIdx := 1
	streamReasoning := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlockChunk(&schema.Reasoning{
				Text: "partial",
				OpenAIExtension: &openai.ReasoningExtension{
					Content: []*openai.ReasoningContent{
						{Text: "raw", Index: &streamReasoningIdx},
					},
				},
			}, &schema.StreamingMeta{Index: 0}),
		},
	}
	if err := ValidateStreamChunk(streamReasoning); err != nil {
		t.Fatalf("stream fragment indexed reasoning: %v", err)
	}
}

// TestConcatStreamIndexedAnnotationAndReasoning proves realistic multi-chunk
// stream fragments with assembly indexes concat into a normalized complete
// message that passes strict Validate, without mutating caller-owned chunks.
func TestConcatStreamIndexedAnnotationAndReasoning(t *testing.T) {
	t.Parallel()

	// --- Indexed annotations: multi-chunk (upstream clears Index to 0) ---
	annoIdx := 2
	annoChunks := []*schema.AgenticMessage{
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlockChunk(&schema.AssistantGenText{
					Text: "Hel",
				}, &schema.StreamingMeta{Index: 0}),
			},
		},
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlockChunk(&schema.AssistantGenText{
					Text: "lo",
					OpenAIExtension: &openai.AssistantGenTextExtension{
						Annotations: []*openai.TextAnnotation{
							{
								Index: annoIdx,
								Type:  openai.TextAnnotationTypeURLCitation,
								URLCitation: &openai.TextAnnotationURLCitation{
									URL: "http://example.com", Title: "ex", StartIndex: 0, EndIndex: 5,
								},
							},
						},
					},
				}, &schema.StreamingMeta{Index: 0}),
			},
		},
	}
	// Snapshot input indexes to prove no mutation.
	inputAnnoIndexBefore := annoChunks[1].ContentBlocks[0].AssistantGenText.OpenAIExtension.Annotations[0].Index

	annoOut, err := ConcatStream(annoChunks)
	if err != nil {
		t.Fatalf("indexed annotation concat: %v", err)
	}
	if err := Validate(annoOut); err != nil {
		t.Fatalf("concat annotation output Validate: %v", err)
	}
	gotText, err := ExtractAssistantText(annoOut)
	if err != nil || gotText != "Hello" {
		t.Fatalf("text=%q err=%v", gotText, err)
	}
	outAnnos := annoOut.ContentBlocks[0].AssistantGenText.OpenAIExtension.Annotations
	if len(outAnnos) != 1 || outAnnos[0].Index != 0 {
		t.Fatalf("expected normalized annotation Index=0, got %+v", outAnnos)
	}
	if outAnnos[0].URLCitation == nil || outAnnos[0].URLCitation.URL != "http://example.com" {
		t.Fatalf("semantic citation lost: %+v", outAnnos[0])
	}
	// Caller-owned chunk unchanged.
	if annoChunks[1].ContentBlocks[0].AssistantGenText.OpenAIExtension.Annotations[0].Index != inputAnnoIndexBefore {
		t.Fatalf("caller annotation Index mutated: got %d want %d",
			annoChunks[1].ContentBlocks[0].AssistantGenText.OpenAIExtension.Annotations[0].Index,
			inputAnnoIndexBefore)
	}

	// --- Single-chunk path retains Index in upstream early-return; we normalize ---
	singleAnno := []*schema.AgenticMessage{
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlockChunk(&schema.AssistantGenText{
					Text: "solo",
					OpenAIExtension: &openai.AssistantGenTextExtension{
						Annotations: []*openai.TextAnnotation{
							{
								Index: 5,
								Type:  openai.TextAnnotationTypeURLCitation,
								URLCitation: &openai.TextAnnotationURLCitation{
									URL: "http://solo.example", Title: "s",
								},
							},
						},
					},
				}, &schema.StreamingMeta{Index: 0}),
			},
		},
	}
	singleInIndex := singleAnno[0].ContentBlocks[0].AssistantGenText.OpenAIExtension.Annotations[0].Index
	singleOut, err := ConcatStream(singleAnno)
	if err != nil {
		t.Fatalf("single-chunk indexed annotation concat: %v", err)
	}
	if err := Validate(singleOut); err != nil {
		t.Fatalf("single-chunk output Validate: %v", err)
	}
	if singleOut.ContentBlocks[0].AssistantGenText.OpenAIExtension.Annotations[0].Index != 0 {
		t.Fatalf("single-chunk should normalize Index to 0")
	}
	if singleAnno[0].ContentBlocks[0].AssistantGenText.OpenAIExtension.Annotations[0].Index != singleInIndex {
		t.Fatal("single-chunk concat mutated caller annotation Index")
	}
	// Output must not alias the input message when normalization ran.
	if singleOut == singleAnno[0] {
		t.Fatal("normalized output must not be the same pointer as input chunk")
	}

	// --- Indexed reasoning fragments (already partially covered; assert Index cleared) ---
	rIdx := 0
	rChunks := []*schema.AgenticMessage{
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlockChunk(&schema.Reasoning{
					Text: "plan",
					OpenAIExtension: &openai.ReasoningExtension{
						Content: []*openai.ReasoningContent{
							{Text: "raw-a", Index: &rIdx},
						},
					},
				}, &schema.StreamingMeta{Index: 0}),
			},
		},
		{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlockChunk(&schema.Reasoning{
					Text: " step",
					OpenAIExtension: &openai.ReasoningExtension{
						Content: []*openai.ReasoningContent{
							{Text: "raw-b", Index: &rIdx},
						},
					},
				}, &schema.StreamingMeta{Index: 0}),
			},
			ResponseMeta: &schema.AgenticResponseMeta{
				OpenAIExtension: &openai.ResponseMetaExtension{ID: "resp_idx"},
			},
		},
	}
	inputRIdx := rChunks[0].ContentBlocks[0].Reasoning.OpenAIExtension.Content[0].Index
	rOut, err := ConcatStream(rChunks)
	if err != nil {
		t.Fatalf("indexed reasoning concat: %v", err)
	}
	if err := Validate(rOut); err != nil {
		t.Fatalf("reasoning concat Validate: %v", err)
	}
	gotR, err := ExtractReasoningText(rOut)
	if err != nil || gotR != "plan step" {
		t.Fatalf("reasoning=%q err=%v", gotR, err)
	}
	for i, c := range rOut.ContentBlocks[0].Reasoning.OpenAIExtension.Content {
		if c.Index != nil {
			t.Fatalf("content[%d] retained Index=%v after concat", i, c.Index)
		}
	}
	if rChunks[0].ContentBlocks[0].Reasoning.OpenAIExtension.Content[0].Index != inputRIdx {
		t.Fatal("caller reasoning Index mutated")
	}

	// Single reasoning chunk with Index + StreamingMeta + self-generated meta
	// (upstream returns same pointer; we normalize without mutating input).
	soloIdx := 7
	soloReasoningIn := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlockChunk(&schema.Reasoning{
				Text: "think",
				OpenAIExtension: &openai.ReasoningExtension{
					Content: []*openai.ReasoningContent{
						{Text: "raw", Index: &soloIdx},
					},
				},
			}, &schema.StreamingMeta{Index: 0}),
		},
		ResponseMeta: &schema.AgenticResponseMeta{
			OpenAIExtension: &openai.ResponseMetaExtension{ID: "resp_solo"},
		},
	}
	soloOut, err := ConcatStream([]*schema.AgenticMessage{soloReasoningIn})
	if err != nil {
		t.Fatalf("solo indexed reasoning concat: %v", err)
	}
	if err := Validate(soloOut); err != nil {
		t.Fatalf("solo reasoning Validate: %v", err)
	}
	if soloOut.ContentBlocks[0].Reasoning.OpenAIExtension.Content[0].Index != nil {
		t.Fatal("solo reasoning should clear Index")
	}
	if soloReasoningIn.ContentBlocks[0].Reasoning.OpenAIExtension.Content[0].Index == nil ||
		*soloReasoningIn.ContentBlocks[0].Reasoning.OpenAIExtension.Content[0].Index != 7 {
		t.Fatal("solo concat mutated caller reasoning Index")
	}
	if soloOut == soloReasoningIn {
		t.Fatal("normalized solo output must not alias input")
	}

	// Complete (non-stream) message with stream-only Index fails ConcatStream pre-concat.
	if _, err := ConcatStream([]*schema.AgenticMessage{completeIndexedAnnoMessage()}); !errors.Is(err, ErrStreamOnlyField) {
		t.Fatalf("ConcatStream complete indexed anno: %v", err)
	}
}

func completeIndexedAnnoMessage() *schema.AgenticMessage {
	return &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{
				Text: "x",
				OpenAIExtension: &openai.AssistantGenTextExtension{
					Annotations: []*openai.TextAnnotation{
						{
							Index: 1,
							Type:  openai.TextAnnotationTypeURLCitation,
							URLCitation: &openai.TextAnnotationURLCitation{
								URL: "http://example.com", Title: "ex",
							},
						},
					},
				},
			}),
		},
	}
}
