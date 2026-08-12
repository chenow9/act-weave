package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/modelconfig"
)

// ---------------------------------------------------------------------------
// Positive fixtures — one per registry member (pinned-valid shapes).
// ---------------------------------------------------------------------------

func fixtureOutputItemMessage() map[string]any {
	return map[string]any{
		"type": "message", "id": "msg_1", "status": "completed", "role": "assistant",
		"content": []any{
			map[string]any{"type": "output_text", "text": "hi", "annotations": []any{}},
		},
	}
}

func fixtureOutputItemFunctionCall() map[string]any {
	return map[string]any{
		"type": "function_call", "call_id": "c1", "name": "echo", "arguments": `{"q":1}`,
		"status": "completed",
	}
}

func fixtureOutputItemReasoning() map[string]any {
	return map[string]any{
		"type": "reasoning", "id": "r1", "status": "completed",
		"summary": []any{map[string]any{"type": "summary_text", "text": "sum"}},
		"content": []any{map[string]any{"type": "reasoning_text", "text": "think"}},
	}
}

func fixtureOutputItemWebSearch() map[string]any {
	return map[string]any{
		"type": "web_search_call", "id": "ws1", "status": "completed",
		"action": map[string]any{"type": "search", "query": "go"},
	}
}

func fixtureOutputItemFileSearch() map[string]any {
	return map[string]any{
		"type": "file_search_call", "id": "fs1", "status": "completed",
		"queries": []any{"q1"},
		"results": []any{map[string]any{
			"file_id": "f1", "filename": "a.txt", "text": "body", "score": 0.9,
			"attributes": map[string]any{"k": "v", "n": 1.0, "b": true},
		}},
	}
}

func fixtureOutputItemCodeInterpreter() map[string]any {
	return map[string]any{
		"type": "code_interpreter_call", "id": "ci1", "code": "print(1)",
		"container_id": "ctr", "status": "completed",
		"outputs": []any{
			map[string]any{"type": "logs", "logs": "1\n"},
			map[string]any{"type": "image", "url": "https://img"},
		},
	}
}

func fixtureOutputItemImageGen() map[string]any {
	return map[string]any{
		"type": "image_generation_call", "id": "ig1", "result": "base64...", "status": "completed",
	}
}

func fixtureOutputItemMCPCall() map[string]any {
	return map[string]any{
		"type": "mcp_call", "id": "m1", "name": "tool", "server_label": "srv", "arguments": `{}`,
		"approval_request_id": "ar1", "error": nil, "output": "ok", "status": "completed",
	}
}

func fixtureOutputItemMCPListTools() map[string]any {
	return map[string]any{
		"type": "mcp_list_tools", "id": "ml1", "server_label": "srv",
		"tools": []any{
			map[string]any{
				"name": "t1", "input_schema": map[string]any{"type": "object"},
				"description": "d", "annotations": map[string]any{"x": 1},
			},
		},
	}
}

func fixtureOutputItemMCPApproval() map[string]any {
	return map[string]any{
		"type": "mcp_approval_request", "id": "ma1", "name": "t", "server_label": "srv", "arguments": `{}`,
	}
}

func fixtureOutputItemToolSearchCall() map[string]any {
	return map[string]any{
		"type": "tool_search_call", "id": "ts1", "call_id": "tsc1",
		"arguments": map[string]any{"query": "x"}, "execution": "server", "status": "completed",
	}
}

func fixtureValidFunctionTool() map[string]any {
	return map[string]any{
		"type": "function", "name": "echo", "strict": true,
		"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
		"description": "echo tool", "defer_loading": false,
	}
}

func fixtureOutputItemToolSearchOutput() map[string]any {
	return map[string]any{
		"type": "tool_search_output", "id": "tso1", "call_id": "tsc1",
		"execution": "client", "status": "completed",
		"tools": []any{fixtureValidFunctionTool()},
	}
}

func fixtureOutputItemShellCall() map[string]any {
	return map[string]any{
		"type": "shell_call", "id": "sh1", "call_id": "shc1", "status": "completed",
		"action": map[string]any{
			"commands": []any{"echo hi"}, "max_output_length": 100, "timeout_ms": 1000,
		},
		"environment": map[string]any{"type": "local"},
	}
}

func fixtureOutputItemShellCallOutput() map[string]any {
	return map[string]any{
		"type": "shell_call_output", "id": "sho1", "call_id": "shc1",
		"max_output_length": 100, "status": "completed",
		"output": []any{
			map[string]any{
				"stdout": "hi\n", "stderr": "",
				"outcome": map[string]any{"type": "exit", "exit_code": 0},
			},
		},
	}
}

// outputItemPositiveFixtures maps every registry key to a positive fixture builder.
var outputItemPositiveFixtures = map[string]func() map[string]any{
	"message":               fixtureOutputItemMessage,
	"function_call":         fixtureOutputItemFunctionCall,
	"reasoning":             fixtureOutputItemReasoning,
	"web_search_call":       fixtureOutputItemWebSearch,
	"file_search_call":      fixtureOutputItemFileSearch,
	"code_interpreter_call": fixtureOutputItemCodeInterpreter,
	"image_generation_call": fixtureOutputItemImageGen,
	"mcp_call":              fixtureOutputItemMCPCall,
	"mcp_list_tools":        fixtureOutputItemMCPListTools,
	"mcp_approval_request":  fixtureOutputItemMCPApproval,
	"tool_search_call":      fixtureOutputItemToolSearchCall,
	"tool_search_output":    fixtureOutputItemToolSearchOutput,
	"shell_call":            fixtureOutputItemShellCall,
	"shell_call_output":     fixtureOutputItemShellCallOutput,
}

// ---------------------------------------------------------------------------
// Registry / spec / fixture exact equality
// ---------------------------------------------------------------------------

func TestPinnedOutputItemRegistry_SpecFixtureEquality(t *testing.T) {
	regKeys := sortedKeys(pinnedOutputItemRegistry)
	specKeys := sortedKeys(outputItemSpecs)
	fixKeys := sortedKeys(outputItemPositiveFixtures)
	if !reflect.DeepEqual(regKeys, specKeys) {
		t.Fatalf("registry keys != outputItemSpecs\nreg=%v\nspec=%v", regKeys, specKeys)
	}
	if !reflect.DeepEqual(regKeys, fixKeys) {
		t.Fatalf("registry keys != fixture keys\nreg=%v\nfix=%v", regKeys, fixKeys)
	}
	for _, k := range regKeys {
		if pinnedOutputItemRegistry[k] == nil {
			t.Fatalf("nil validator for %q", k)
		}
		if len(outputItemSpecs[k]) == 0 && k != "" {
			// shell local may be empty; output items always have fields
			t.Fatalf("empty field specs for output item %q", k)
		}
		if outputItemPositiveFixtures[k] == nil {
			t.Fatalf("nil fixture for %q", k)
		}
	}
}

func TestPinnedNestedRegistries_NoNilValidators(t *testing.T) {
	check := func(name string, reg map[string]pinnedUnionMemberValidator) {
		t.Helper()
		if len(reg) == 0 {
			t.Fatalf("%s registry empty", name)
		}
		for _, k := range sortedKeys(reg) {
			if reg[k] == nil {
				t.Fatalf("%s nil validator for %q", name, k)
			}
		}
	}
	check("output", pinnedOutputItemRegistry)
	check("content", pinnedContentPartRegistry)
	check("annotation", pinnedAnnotationRegistry)
	check("web_action", pinnedWebSearchActionRegistry)
	check("ci_output", pinnedCodeInterpreterOutputRegistry)
	check("shell_env", pinnedShellEnvironmentRegistry)
	check("shell_outcome", pinnedShellOutcomeRegistry)

	// Spec matrices must match annotation registry keys.
	if !reflect.DeepEqual(sortedKeys(pinnedAnnotationRegistry), sortedKeys(annotationSpecs)) {
		t.Fatalf("annotation registry != annotationSpecs\nreg=%v\nspec=%v",
			sortedKeys(pinnedAnnotationRegistry), sortedKeys(annotationSpecs))
	}
	if !reflect.DeepEqual(sortedKeys(pinnedContentPartRegistry), sortedKeys(contentPartSpecs)) {
		t.Fatalf("content registry != contentPartSpecs")
	}
	if !reflect.DeepEqual(sortedKeys(pinnedWebSearchActionRegistry), sortedKeys(webSearchActionSpecs)) {
		t.Fatalf("web action registry != webSearchActionSpecs")
	}
}

func TestPinnedOutputItemRegistry_PositiveFixturesAccept(t *testing.T) {
	for _, k := range sortedKeys(pinnedOutputItemRegistry) {
		k := k
		t.Run(k, func(t *testing.T) {
			item := outputItemPositiveFixtures[k]()
			ev := map[string]any{
				"type": "response.output_item.added", "output_index": 0, "item": item,
			}
			err := validateVerificationResponsesPayload([]byte(sseWithCompleted(ev)), "text/event-stream")
			if err != nil {
				t.Fatalf("positive fixture must accept: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Spec-driven missing / null / wrong-type for every field of every member
// ---------------------------------------------------------------------------

func TestPinnedOutputItemSpecs_MissingNullWrongType(t *testing.T) {
	for _, member := range sortedKeys(outputItemSpecs) {
		member := member
		specs := outputItemSpecs[member]
		t.Run(member, func(t *testing.T) {
			base := outputItemPositiveFixtures[member]()
			for _, sp := range specs {
				sp := sp
				t.Run(sp.Key, func(t *testing.T) {
					if sp.Required {
						t.Run("missing", func(t *testing.T) {
							item := cloneMap(base)
							delete(item, sp.Key)
							assertStreamInvalid(t, outputItemEvent(item))
						})
					} else {
						t.Run("missing_optional_ok", func(t *testing.T) {
							item := cloneMap(base)
							delete(item, sp.Key)
							assertStreamOK(t, outputItemEvent(item))
						})
					}

					t.Run("null", func(t *testing.T) {
						item := cloneMap(base)
						item[sp.Key] = nil
						err := validateVerificationResponsesPayload(
							[]byte(sseWithCompleted(outputItemEvent(item))), "text/event-stream")
						if sp.Nullable {
							if err != nil {
								t.Fatalf("nullable field %s must accept null: %v", sp.Key, err)
							}
							return
						}
						if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
							t.Fatalf("non-nullable %s null: want stream invalid, got %v", sp.Key, err)
						}
						assertNoSecretLeak(t, err)
					})

					// tool_search_call.arguments is pinned any — any non-null is valid.
					if member == "tool_search_call" && sp.Key == "arguments" {
						t.Run("wrongtype_skipped_any", func(t *testing.T) {
							item := cloneMap(base)
							item[sp.Key] = "still-any"
							assertStreamOK(t, outputItemEvent(item))
						})
						return
					}

					t.Run("wrongtype", func(t *testing.T) {
						item := cloneMap(base)
						// Ensure key is present with wrong type even if optional.
						item[sp.Key] = adversarialWrongTypeForKind(sp)
						assertStreamInvalid(t, outputItemEvent(item))
					})
				})
			}
		})
	}
}

func TestCodeInterpreter_RequiredButNullableCodeOutputs(t *testing.T) {
	base := fixtureOutputItemCodeInterpreter()

	t.Run("code_null_ok", func(t *testing.T) {
		item := cloneMap(base)
		item["code"] = nil
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("outputs_null_ok", func(t *testing.T) {
		item := cloneMap(base)
		item["outputs"] = nil
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("both_null_ok", func(t *testing.T) {
		item := cloneMap(base)
		item["code"] = nil
		item["outputs"] = nil
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("code_missing_fail", func(t *testing.T) {
		item := cloneMap(base)
		delete(item, "code")
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("outputs_missing_fail", func(t *testing.T) {
		item := cloneMap(base)
		delete(item, "outputs")
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("code_wrongtype_fail", func(t *testing.T) {
		item := cloneMap(base)
		item["code"] = 123
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("outputs_wrongtype_fail", func(t *testing.T) {
		item := cloneMap(base)
		item["outputs"] = "not-array"
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("outputs_element_unknown_type_fail", func(t *testing.T) {
		item := cloneMap(base)
		item["outputs"] = []any{map[string]any{"type": "fabricated", "logs": "x"}}
		assertStreamInvalid(t, outputItemEvent(item))
	})
}

// ---------------------------------------------------------------------------
// A. tool_search_output.tools[] — function only
// ---------------------------------------------------------------------------

func TestToolSearchOutput_FunctionOnlyTools(t *testing.T) {
	base := fixtureOutputItemToolSearchOutput()

	cases := []struct {
		name  string
		tools []any
		ok    bool
	}{
		{
			name:  "function_valid",
			tools: []any{fixtureValidFunctionTool()},
			ok:    true,
		},
		{
			name: "function_minimal_no_optionals",
			tools: []any{map[string]any{
				"type": "function", "name": "echo", "strict": true,
				"parameters": map[string]any{"type": "object"},
			}},
			ok: true,
		},
		{
			name: "function_description_null_ok",
			tools: []any{map[string]any{
				"type": "function", "name": "echo", "strict": true,
				"parameters": map[string]any{}, "description": nil,
			}},
			ok: true,
		},
		{
			name: "function_missing_name",
			tools: []any{map[string]any{
				"type": "function", "strict": true,
				"parameters": map[string]any{"type": "object"},
			}},
		},
		{
			name: "function_missing_parameters",
			tools: []any{map[string]any{
				"type": "function", "name": "echo", "strict": true,
			}},
		},
		{
			name: "function_missing_strict",
			tools: []any{map[string]any{
				"type": "function", "name": "echo",
				"parameters": map[string]any{"type": "object"},
			}},
		},
		{
			name: "function_parameters_not_object",
			tools: []any{map[string]any{
				"type": "function", "name": "echo", "strict": true,
				"parameters": "not-object",
			}},
		},
		{
			name: "function_parameters_null",
			tools: []any{map[string]any{
				"type": "function", "name": "echo", "strict": true,
				"parameters": nil,
			}},
		},
		{
			name: "function_strict_wrong_type",
			tools: []any{map[string]any{
				"type": "function", "name": "echo", "strict": "yes",
				"parameters": map[string]any{},
			}},
		},
		{
			name: "function_description_wrong_type",
			tools: []any{map[string]any{
				"type": "function", "name": "echo", "strict": true,
				"parameters": map[string]any{}, "description": 99,
			}},
		},
		{
			name: "function_defer_loading_wrong_type",
			tools: []any{map[string]any{
				"type": "function", "name": "echo", "strict": true,
				"parameters": map[string]any{}, "defer_loading": "yes",
			}},
		},
		{
			name: "function_defer_loading_null",
			tools: []any{map[string]any{
				"type": "function", "name": "echo", "strict": true,
				"parameters": map[string]any{}, "defer_loading": nil,
			}},
		},
		{
			name: "mcp_rejected",
			tools: []any{map[string]any{
				"type": "mcp", "server_label": "s", "server_url": "https://x",
			}},
		},
		{
			name: "file_search_rejected",
			tools: []any{map[string]any{
				"type": "file_search", "vector_store_ids": []any{"vs1"},
			}},
		},
		{
			name: "totally_fabricated_rejected",
			tools: []any{map[string]any{
				"type": "totally_fabricated", "name": "x",
			}},
		},
		{
			name:  "empty_object_tool_rejected",
			tools: []any{map[string]any{}},
		},
		{
			name: "function_empty_name",
			tools: []any{map[string]any{
				"type": "function", "name": "", "strict": true,
				"parameters": map[string]any{},
			}},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			item := cloneMap(base)
			item["tools"] = tc.tools
			// Inject secret-looking string to ensure non-leak on failure paths.
			if !tc.ok && len(tc.tools) > 0 {
				if m, ok := tc.tools[0].(map[string]any); ok {
					m["secret_marker"] = "sk-leaked-key-should-not-appear"
				}
			}
			if tc.ok {
				assertStreamOK(t, outputItemEvent(item))
			} else {
				assertStreamInvalid(t, outputItemEvent(item))
			}
		})
	}

	// Spec matrix for function tools themselves.
	t.Run("functionToolSpecs_matrix", func(t *testing.T) {
		for _, sp := range functionToolSpecs {
			sp := sp
			t.Run(sp.Key, func(t *testing.T) {
				tool := fixtureValidFunctionTool()
				if sp.Required {
					delete(tool, sp.Key)
					item := cloneMap(base)
					item["tools"] = []any{tool}
					assertStreamInvalid(t, outputItemEvent(item))
				}
				tool = fixtureValidFunctionTool()
				tool[sp.Key] = nil
				item := cloneMap(base)
				item["tools"] = []any{tool}
				err := validateVerificationResponsesPayload(
					[]byte(sseWithCompleted(outputItemEvent(item))), "text/event-stream")
				if sp.Nullable {
					if err != nil {
						t.Fatalf("nullable %s null must accept: %v", sp.Key, err)
					}
				} else if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
					t.Fatalf("non-nullable %s null must fail: %v", sp.Key, err)
				}
			})
		}
	})
}

// ---------------------------------------------------------------------------
// B. file_search result fields
// ---------------------------------------------------------------------------

func TestFileSearchResult_PinnedFields(t *testing.T) {
	base := fixtureOutputItemFileSearch()

	t.Run("positive_full", func(t *testing.T) {
		assertStreamOK(t, outputItemEvent(base))
	})
	t.Run("results_null_ok", func(t *testing.T) {
		item := cloneMap(base)
		item["results"] = nil
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("score_out_of_range", func(t *testing.T) {
		item := cloneMap(base)
		item["results"] = []any{map[string]any{"score": 1.5}}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("score_negative", func(t *testing.T) {
		item := cloneMap(base)
		item["results"] = []any{map[string]any{"score": -0.1}}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("score_wrong_type", func(t *testing.T) {
		item := cloneMap(base)
		item["results"] = []any{map[string]any{"score": "high"}}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("file_id_null_fail", func(t *testing.T) {
		item := cloneMap(base)
		item["results"] = []any{map[string]any{"file_id": nil}}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("attributes_null_ok", func(t *testing.T) {
		item := cloneMap(base)
		item["results"] = []any{map[string]any{"file_id": "f1", "attributes": nil}}
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("attributes_wrong_value_type", func(t *testing.T) {
		item := cloneMap(base)
		item["results"] = []any{map[string]any{
			"file_id":    "f1",
			"attributes": map[string]any{"k": []any{1}},
		}}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("attributes_too_many_keys", func(t *testing.T) {
		attrs := map[string]any{}
		for i := 0; i < 17; i++ {
			attrs[fmt.Sprintf("k%d", i)] = "v"
		}
		item := cloneMap(base)
		item["results"] = []any{map[string]any{"attributes": attrs}}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	// Spec matrix for file search result fields
	t.Run("fileSearchResultSpecs_matrix", func(t *testing.T) {
		for _, sp := range fileSearchResultSpecs {
			sp := sp
			t.Run("wrongtype_"+sp.Key, func(t *testing.T) {
				item := cloneMap(base)
				el := map[string]any{"file_id": "f1"}
				el[sp.Key] = adversarialWrongTypeForKind(sp)
				item["results"] = []any{el}
				assertStreamInvalid(t, outputItemEvent(item))
			})
		}
	})
}

// ---------------------------------------------------------------------------
// C. web search action optional nested data
// ---------------------------------------------------------------------------

func TestWebSearchAction_OptionalNested(t *testing.T) {
	cases := []struct {
		name   string
		action map[string]any
		ok     bool
	}{
		{
			name:   "search_minimal",
			action: map[string]any{"type": "search", "query": "go"},
			ok:     true,
		},
		{
			name: "search_with_queries_and_sources",
			action: map[string]any{
				"type": "search", "query": "go",
				"queries": []any{"a", "b"},
				"sources": []any{map[string]any{"type": "url", "url": "https://ex.com"}},
			},
			ok: true,
		},
		{
			name: "search_source_missing_url",
			action: map[string]any{
				"type": "search", "query": "go",
				"sources": []any{map[string]any{"type": "url"}},
			},
		},
		{
			name: "search_source_bad_type",
			action: map[string]any{
				"type": "search", "query": "go",
				"sources": []any{map[string]any{"type": "html", "url": "https://ex.com"}},
			},
		},
		{
			name: "search_queries_element_not_string",
			action: map[string]any{
				"type": "search", "query": "go",
				"queries": []any{1},
			},
		},
		{
			name:   "open_page_url_null_ok",
			action: map[string]any{"type": "open_page", "url": nil},
			ok:     true,
		},
		{
			name:   "open_page_url_missing_ok",
			action: map[string]any{"type": "open_page"},
			ok:     true,
		},
		{
			name:   "open_page_url_wrong_type",
			action: map[string]any{"type": "open_page", "url": 123},
		},
		{
			name:   "find_url_required",
			action: map[string]any{"type": "find_in_page", "pattern": "x"},
		},
		{
			name:   "find_url_null",
			action: map[string]any{"type": "find_in_page", "pattern": "x", "url": nil},
		},
		{
			name:   "find_ok",
			action: map[string]any{"type": "find_in_page", "pattern": "x", "url": "https://a"},
			ok:     true,
		},
		{
			name:   "fabricated_action",
			action: map[string]any{"type": "crawl", "query": "x"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			item := map[string]any{
				"type": "web_search_call", "id": "ws1", "status": "completed",
				"action": tc.action,
			}
			if tc.ok {
				assertStreamOK(t, outputItemEvent(item))
			} else {
				assertStreamInvalid(t, outputItemEvent(item))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// D. MCP optional fields
// ---------------------------------------------------------------------------

func TestMCPCall_OptionalNullableFields(t *testing.T) {
	base := fixtureOutputItemMCPCall()
	t.Run("positive", func(t *testing.T) { assertStreamOK(t, outputItemEvent(base)) })

	for _, field := range []string{"approval_request_id", "error", "output"} {
		field := field
		t.Run(field+"_null_ok", func(t *testing.T) {
			item := cloneMap(base)
			item[field] = nil
			assertStreamOK(t, outputItemEvent(item))
		})
		t.Run(field+"_wrong_type", func(t *testing.T) {
			item := cloneMap(base)
			item[field] = 123
			assertStreamInvalid(t, outputItemEvent(item))
		})
	}
	for _, st := range []string{"in_progress", "completed", "incomplete", "calling", "failed"} {
		st := st
		t.Run("status_"+st, func(t *testing.T) {
			item := cloneMap(base)
			item["status"] = st
			assertStreamOK(t, outputItemEvent(item))
		})
	}
	t.Run("status_fabricated", func(t *testing.T) {
		item := cloneMap(base)
		item["status"] = "running"
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("status_wrong_type", func(t *testing.T) {
		item := cloneMap(base)
		item["status"] = 1
		assertStreamInvalid(t, outputItemEvent(item))
	})
}

func TestMCPListTools_ElementOptionals(t *testing.T) {
	t.Run("description_null_ok", func(t *testing.T) {
		item := fixtureOutputItemMCPListTools()
		item["tools"] = []any{map[string]any{
			"name": "t1", "input_schema": map[string]any{}, "description": nil,
		}}
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("annotations_null_ok", func(t *testing.T) {
		item := fixtureOutputItemMCPListTools()
		item["tools"] = []any{map[string]any{
			"name": "t1", "input_schema": map[string]any{}, "annotations": nil,
		}}
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("annotations_any_nonnull_ok", func(t *testing.T) {
		item := fixtureOutputItemMCPListTools()
		item["tools"] = []any{map[string]any{
			"name": "t1", "input_schema": map[string]any{}, "annotations": []any{1, "x"},
		}}
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("input_schema_scalar_any_ok", func(t *testing.T) {
		// Pinned InputSchema is `any` required — scalar valid.
		item := fixtureOutputItemMCPListTools()
		item["tools"] = []any{map[string]any{
			"name": "t1", "input_schema": "not-object",
		}}
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("input_schema_array_any_ok", func(t *testing.T) {
		item := fixtureOutputItemMCPListTools()
		item["tools"] = []any{map[string]any{
			"name": "t1", "input_schema": []any{1, "x"},
		}}
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("input_schema_null_fail", func(t *testing.T) {
		item := fixtureOutputItemMCPListTools()
		item["tools"] = []any{map[string]any{
			"name": "t1", "input_schema": nil,
		}}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("input_schema_missing_fail", func(t *testing.T) {
		item := fixtureOutputItemMCPListTools()
		item["tools"] = []any{map[string]any{"name": "t1"}}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("parent_error_null_ok", func(t *testing.T) {
		item := fixtureOutputItemMCPListTools()
		item["error"] = nil
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("parent_error_wrong_type", func(t *testing.T) {
		item := fixtureOutputItemMCPListTools()
		item["error"] = 99
		assertStreamInvalid(t, outputItemEvent(item))
	})
}

// ---------------------------------------------------------------------------
// E. output_text logprobs presence/nullability
// ---------------------------------------------------------------------------

func TestValidateVerificationSSE_LogprobsExhaustive(t *testing.T) {
	validLP := []any{
		map[string]any{
			"token": "hi", "bytes": []any{104, 105}, "logprob": -0.1,
			"top_logprobs": []any{
				map[string]any{"token": "hi", "bytes": []any{104, 105}, "logprob": -0.1},
			},
		},
	}
	cases := []struct {
		name string
		ev   map[string]any
		ok   bool
	}{
		{
			name: "delta empty logprobs ok",
			ev: map[string]any{
				"type": "response.output_text.delta", "item_id": "m1",
				"output_index": 0, "content_index": 0, "delta": "x", "logprobs": []any{},
			},
			ok: true,
		},
		{
			name: "delta nonempty logprobs ok",
			ev: map[string]any{
				"type": "response.output_text.delta", "item_id": "m1",
				"output_index": 0, "content_index": 0, "delta": "x", "logprobs": validLP,
			},
			ok: true,
		},
		{
			name: "delta logprobs null fail",
			ev: map[string]any{
				"type": "response.output_text.delta", "item_id": "m1",
				"output_index": 0, "content_index": 0, "delta": "x", "logprobs": nil,
			},
		},
		{
			name: "delta logprobs empty object element",
			ev: map[string]any{
				"type": "response.output_text.delta", "item_id": "m1",
				"output_index": 0, "content_index": 0, "delta": "x",
				"logprobs": []any{map[string]any{}},
			},
		},
		{
			name: "delta token wrong type",
			ev: map[string]any{
				"type": "response.output_text.delta", "item_id": "m1",
				"output_index": 0, "content_index": 0, "delta": "x",
				"logprobs": []any{map[string]any{
					"token": 1, "bytes": []any{1}, "logprob": 0.0, "top_logprobs": []any{},
				}},
			},
		},
		{
			name: "delta bytes null",
			ev: map[string]any{
				"type": "response.output_text.delta", "item_id": "m1",
				"output_index": 0, "content_index": 0, "delta": "x",
				"logprobs": []any{map[string]any{
					"token": "a", "bytes": nil, "logprob": 0.0, "top_logprobs": []any{},
				}},
			},
		},
		{
			name: "delta fractional byte",
			ev: map[string]any{
				"type": "response.output_text.delta", "item_id": "m1",
				"output_index": 0, "content_index": 0, "delta": "x",
				"logprobs": []any{map[string]any{
					"token": "a", "bytes": []any{1.5}, "logprob": 0.0, "top_logprobs": []any{},
				}},
			},
		},
		{
			name: "delta logprob string",
			ev: map[string]any{
				"type": "response.output_text.delta", "item_id": "m1",
				"output_index": 0, "content_index": 0, "delta": "x",
				"logprobs": []any{map[string]any{
					"token": "a", "bytes": []any{1}, "logprob": "x", "top_logprobs": []any{},
				}},
			},
		},
		{
			name: "delta missing top_logprobs",
			ev: map[string]any{
				"type": "response.output_text.delta", "item_id": "m1",
				"output_index": 0, "content_index": 0, "delta": "x",
				"logprobs": []any{map[string]any{
					"token": "a", "bytes": []any{1}, "logprob": 0.0,
				}},
			},
		},
		{
			name: "delta nested top_logprobs empty object",
			ev: map[string]any{
				"type": "response.output_text.delta", "item_id": "m1",
				"output_index": 0, "content_index": 0, "delta": "x",
				"logprobs": []any{map[string]any{
					"token": "a", "bytes": []any{1}, "logprob": 0.0,
					"top_logprobs": []any{map[string]any{}},
				}},
			},
		},
		{
			name: "done wrong token type",
			ev: map[string]any{
				"type": "response.output_text.done", "item_id": "m1",
				"output_index": 0, "content_index": 0, "text": "full",
				"logprobs": []any{map[string]any{
					"token": true, "bytes": []any{1}, "logprob": 0.0, "top_logprobs": []any{},
				}},
			},
		},
		{
			name: "content_part logprobs missing ok",
			ev: map[string]any{
				"type": "response.content_part.added", "item_id": "m1",
				"output_index": 0, "content_index": 0,
				"part": map[string]any{
					"type": "output_text", "text": "x", "annotations": []any{},
				},
			},
			ok: true,
		},
		{
			name: "content_part logprobs null fail",
			ev: map[string]any{
				"type": "response.content_part.added", "item_id": "m1",
				"output_index": 0, "content_index": 0,
				"part": map[string]any{
					"type": "output_text", "text": "x", "annotations": []any{},
					"logprobs": nil,
				},
			},
		},
		{
			name: "content_part logprobs empty object element",
			ev: map[string]any{
				"type": "response.content_part.added", "item_id": "m1",
				"output_index": 0, "content_index": 0,
				"part": map[string]any{
					"type": "output_text", "text": "x", "annotations": []any{},
					"logprobs": []any{map[string]any{}},
				},
			},
		},
		{
			name: "content_part logprobs ok",
			ev: map[string]any{
				"type": "response.content_part.added", "item_id": "m1",
				"output_index": 0, "content_index": 0,
				"part": map[string]any{
					"type": "output_text", "text": "x", "annotations": []any{},
					"logprobs": validLP,
				},
			},
			ok: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := validateVerificationResponsesPayload([]byte(sseWithCompleted(tc.ev)), "text/event-stream")
			if tc.ok {
				if err != nil {
					t.Fatalf("want accept: %v", err)
				}
				return
			}
			if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
				t.Fatalf("want stream invalid, got %v", err)
			}
			assertNoSecretLeak(t, err)
		})
	}
}

// ---------------------------------------------------------------------------
// Overlay reproductions + status domain / nested adversarial
// ---------------------------------------------------------------------------

func TestValidateVerificationSSE_NestedUnionOverlayReproductions(t *testing.T) {
	cases := []struct {
		name string
		ev   map[string]any
		ok   bool
	}{
		{
			name: "web_search missing action",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"type": "web_search_call", "id": "w1", "status": "completed"},
			},
		},
		{
			name: "web_search fabricated status",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{
					"type": "web_search_call", "id": "w1", "status": "cancelled",
					"action": map[string]any{"type": "search", "query": "q"},
				},
			},
		},
		{
			name: "web_search action search ok",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": fixtureOutputItemWebSearch(),
			},
			ok: true,
		},
		{
			name: "web_search action find ok",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{
					"type": "web_search_call", "id": "w1", "status": "searching",
					"action": map[string]any{"type": "find_in_page", "pattern": "x", "url": "https://a"},
				},
			},
			ok: true,
		},
		{
			name: "reasoning summary empty object",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{
					"type": "reasoning", "id": "r1",
					"summary": []any{map[string]any{}},
				},
			},
		},
		{
			name: "tool_search_call type id only",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"type": "tool_search_call", "id": "ts1"},
			},
		},
		{
			name: "tool_search_output type id only",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"type": "tool_search_output", "id": "tso1"},
			},
		},
		{
			name: "shell_call type id only",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"type": "shell_call", "id": "s1"},
			},
		},
		{
			name: "shell_call_output type id only",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"type": "shell_call_output", "id": "s1"},
			},
		},
		{
			name: "mcp_list_tools tools empty object",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{
					"type": "mcp_list_tools", "id": "m1", "server_label": "s",
					"tools": []any{map[string]any{}},
				},
			},
		},
		{
			name: "annotation reversed ranges",
			ev: map[string]any{
				"type": "response.output_text.annotation.added", "item_id": "m1",
				"output_index": 0, "content_index": 0, "annotation_index": 0,
				"annotation": map[string]any{
					"type": "url_citation", "url": "https://x", "title": "t",
					"start_index": 10, "end_index": 2,
				},
			},
		},
		{
			name: "file_search fabricated status",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{
					"type": "file_search_call", "id": "f1", "status": "running", "queries": []any{"q"},
				},
			},
		},
		{
			name: "code_interpreter outputs empty object",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{
					"type": "code_interpreter_call", "id": "c1", "code": "", "container_id": "ctr",
					"status": "completed", "outputs": []any{map[string]any{}},
				},
			},
		},
		{
			name: "image_gen fabricated status",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{
					"type": "image_generation_call", "id": "i1", "result": "", "status": "pending",
				},
			},
		},
		{
			name: "shell_call environment container ok",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{
					"type": "shell_call", "id": "sh1", "call_id": "c1", "status": "in_progress",
					"action": map[string]any{
						"commands": []any{"ls"}, "max_output_length": 10, "timeout_ms": 1,
					},
					"environment": map[string]any{"type": "container_reference", "container_id": "ctr"},
				},
			},
			ok: true,
		},
		{
			name: "shell_call environment missing container_id",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{
					"type": "shell_call", "id": "sh1", "call_id": "c1", "status": "in_progress",
					"action": map[string]any{
						"commands": []any{"ls"}, "max_output_length": 10, "timeout_ms": 1,
					},
					"environment": map[string]any{"type": "container_reference"},
				},
			},
		},
		{
			name: "message status in_progress ok",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": validPinnedMessageItem("m1", "in_progress"),
			},
			ok: true,
		},
		{
			name: "message status incomplete ok",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": validPinnedMessageItem("m1", "incomplete"),
			},
			ok: true,
		},
		{
			name: "tool_search execution client ok",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{
					"type": "tool_search_call", "id": "ts1", "call_id": "c1",
					"arguments": []any{}, "execution": "client", "status": "in_progress",
				},
			},
			ok: true,
		},
		{
			name: "tool_search execution fabricated",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{
					"type": "tool_search_call", "id": "ts1", "call_id": "c1",
					"arguments": map[string]any{}, "execution": "edge", "status": "completed",
				},
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := validateVerificationResponsesPayload([]byte(sseWithCompleted(tc.ev)), "text/event-stream")
			if tc.ok {
				if err != nil {
					t.Fatalf("want accept: %v", err)
				}
				return
			}
			if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
				t.Fatalf("want stream invalid, got %v", err)
			}
			assertNoSecretLeak(t, err)
		})
	}
}

func TestPinnedContentPartAndAnnotationRegistries_Exhaustive(t *testing.T) {
	wantParts := []string{"output_text", "reasoning_text", "refusal"}
	gotParts := sortedKeys(pinnedContentPartRegistry)
	if !reflect.DeepEqual(wantParts, gotParts) {
		t.Fatalf("content part registry: want %v got %v", wantParts, gotParts)
	}

	// Spec-driven missing/null for content parts.
	partFixtures := map[string]map[string]any{
		"output_text":    validPinnedOutputTextPart("x"),
		"refusal":        {"type": "refusal", "refusal": "no"},
		"reasoning_text": {"type": "reasoning_text", "text": "why"},
	}
	for k, specs := range contentPartSpecs {
		fix := partFixtures[k]
		for _, sp := range specs {
			sp := sp
			t.Run(fmt.Sprintf("%s_%s", k, sp.Key), func(t *testing.T) {
				if sp.Required {
					bad := cloneMap(fix)
					delete(bad, sp.Key)
					ev := map[string]any{
						"type": "response.content_part.added", "item_id": "m1",
						"output_index": 0, "content_index": 0, "part": bad,
					}
					assertStreamInvalid(t, ev)
				}
				// null
				bad := cloneMap(fix)
				bad[sp.Key] = nil
				ev := map[string]any{
					"type": "response.content_part.added", "item_id": "m1",
					"output_index": 0, "content_index": 0, "part": bad,
				}
				err := validateVerificationResponsesPayload([]byte(sseWithCompleted(ev)), "text/event-stream")
				if sp.Nullable {
					if err != nil {
						t.Fatalf("nullable %s.%s null must accept: %v", k, sp.Key, err)
					}
				} else if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
					t.Fatalf("non-nullable %s.%s null must fail: %v", k, sp.Key, err)
				}
			})
		}
	}

	// Annotation positive + spec missing
	annFixtures := map[string]map[string]any{
		"file_citation": validPinnedFileCitationAnnotation(),
		"url_citation":  validPinnedURLCitationAnnotation(),
		"file_path":     {"type": "file_path", "file_id": "f1", "index": 0},
		"container_file_citation": {
			"type": "container_file_citation", "container_id": "c1", "file_id": "f1",
			"filename": "a.txt", "start_index": 0, "end_index": 1,
		},
	}
	for k, fix := range annFixtures {
		evOK := map[string]any{
			"type": "response.output_text.annotation.added", "item_id": "m1",
			"output_index": 0, "content_index": 0, "annotation_index": 0, "annotation": fix,
		}
		if err := validateVerificationResponsesPayload([]byte(sseWithCompleted(evOK)), "text/event-stream"); err != nil {
			t.Fatalf("annotation %s positive: %v", k, err)
		}
		for _, sp := range annotationSpecs[k] {
			if !sp.Required {
				continue
			}
			bad := cloneMap(fix)
			delete(bad, sp.Key)
			ev := map[string]any{
				"type": "response.output_text.annotation.added", "item_id": "m1",
				"output_index": 0, "content_index": 0, "annotation_index": 0, "annotation": bad,
			}
			if err := validateVerificationResponsesPayload([]byte(sseWithCompleted(ev)), "text/event-stream"); !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
				t.Fatalf("annotation %s missing %s: %v", k, sp.Key, err)
			}
		}
	}
}

func TestPinnedNestedUnionRegistries_NoDefaultSuccess(t *testing.T) {
	for _, itemType := range []string{"computer_call", "custom_tool_call", "compaction", "not_a_type"} {
		ev := map[string]any{
			"type": "response.output_item.added", "output_index": 0,
			"item": map[string]any{"type": itemType, "id": "x"},
		}
		err := validateVerificationResponsesPayload([]byte(sseWithCompleted(ev)), "text/event-stream")
		if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
			t.Fatalf("%s must reject, got %v", itemType, err)
		}
	}
	// Nested unions: unknown action/env/outcome/ci output
	unknowns := []map[string]any{
		{
			"type": "web_search_call", "id": "w", "status": "completed",
			"action": map[string]any{"type": "unknown_action"},
		},
		{
			"type": "shell_call", "id": "s", "call_id": "c", "status": "completed",
			"action": map[string]any{
				"commands": []any{"x"}, "max_output_length": 1, "timeout_ms": 1,
			},
			"environment": map[string]any{"type": "remote"},
		},
		{
			"type": "code_interpreter_call", "id": "c", "code": "x", "container_id": "ctr",
			"status":  "completed",
			"outputs": []any{map[string]any{"type": "video", "url": "u"}},
		},
	}
	for i, item := range unknowns {
		err := validateVerificationResponsesPayload(
			[]byte(sseWithCompleted(outputItemEvent(item))), "text/event-stream")
		if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
			t.Fatalf("unknown nested[%d] must reject, got %v", i, err)
		}
	}
}

// Behavior check: every registry validator rejects a bare type-only object
// (no nil/fallback success path).
func TestPinnedRegistries_NoFallbackSuccessBehavior(t *testing.T) {
	for _, k := range sortedKeys(pinnedOutputItemRegistry) {
		k := k
		t.Run(k, func(t *testing.T) {
			item := map[string]any{"type": k}
			// tool items that only need type would still fail required fields.
			assertStreamInvalid(t, outputItemEvent(item))
		})
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func outputItemEvent(item map[string]any) map[string]any {
	return map[string]any{
		"type": "response.output_item.added", "output_index": 0, "item": item,
	}
}

func assertStreamOK(t *testing.T, ev map[string]any) {
	t.Helper()
	err := validateVerificationResponsesPayload([]byte(sseWithCompleted(ev)), "text/event-stream")
	if err != nil {
		t.Fatalf("want accept: %v", err)
	}
}

func assertStreamInvalid(t *testing.T, ev map[string]any) {
	t.Helper()
	err := validateVerificationResponsesPayload([]byte(sseWithCompleted(ev)), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("want stream invalid, got %v", err)
	}
	assertNoSecretLeak(t, err)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func cloneMap(in map[string]any) map[string]any {
	b, _ := json.Marshal(in)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

// adversarialWrongTypeForKind returns a JSON value with a kind incompatible with sp.
func adversarialWrongTypeForKind(sp pinnedFieldSpec) any {
	switch sp.Kind {
	case kindNonemptyString, kindString, kindDomainString, kindURI:
		return 12345
	case kindBool:
		return "not-a-bool"
	case kindNonNegInt, kindInt, kindFiniteNumber:
		return "not-a-number"
	case kindObject:
		return "not-an-object"
	case kindArray:
		return "not-an-array"
	case kindAnyNonNull:
		// any non-null is valid — no wrong JSON type; wrongtype subtest skipped/uses null path.
		return nil
	default:
		return "wrong"
	}
}

func assertNoSecretLeak(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	s := err.Error()
	if strings.Contains(s, "sk-") || strings.Contains(s, "Bearer ") || strings.Contains(s, "api_key") {
		t.Fatalf("error leaked secret material: %v", err)
	}
	if strings.Contains(s, "sk-leaked-key-should-not-appear") {
		t.Fatalf("error leaked injected secret marker: %v", err)
	}
	// Hostile schema body fragments must not appear.
	if strings.Contains(s, "example.com/secret") || strings.Contains(s, "9007199254740993") {
		t.Fatalf("error leaked schema body fragment: %v", err)
	}
	if len(s) > 512 {
		t.Fatalf("error message suspiciously long (possible body leak): %d bytes", len(s))
	}
}

// ---------------------------------------------------------------------------
// Cycle 12 overlays: role/phase, encrypted_content, URI, schema security, shell created_by
// ---------------------------------------------------------------------------

func TestMessageRoleAndPhase_SSE(t *testing.T) {
	base := fixtureOutputItemMessage()
	cases := []struct {
		name string
		mut  func(map[string]any)
		ok   bool
	}{
		{"role_assistant_ok", func(m map[string]any) { m["role"] = "assistant" }, true},
		{"role_missing", func(m map[string]any) { delete(m, "role") }, false},
		{"role_null", func(m map[string]any) { m["role"] = nil }, false},
		{"role_user", func(m map[string]any) { m["role"] = "user" }, false},
		{"role_wrong_type", func(m map[string]any) { m["role"] = 1 }, false},
		{"phase_missing_ok", func(m map[string]any) { delete(m, "phase") }, true},
		{"phase_null_ok", func(m map[string]any) { m["phase"] = nil }, true},
		{"phase_commentary_ok", func(m map[string]any) { m["phase"] = "commentary" }, true},
		{"phase_final_answer_ok", func(m map[string]any) { m["phase"] = "final_answer" }, true},
		{"phase_fabricated", func(m map[string]any) { m["phase"] = "draft" }, false},
		{"phase_wrong_type", func(m map[string]any) { m["phase"] = true }, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			item := cloneMap(base)
			tc.mut(item)
			if tc.ok {
				assertStreamOK(t, outputItemEvent(item))
			} else {
				assertStreamInvalid(t, outputItemEvent(item))
			}
		})
	}
}

func TestReasoningEncryptedContent_Nullable(t *testing.T) {
	base := fixtureOutputItemReasoning()
	t.Run("missing_ok", func(t *testing.T) {
		item := cloneMap(base)
		delete(item, "encrypted_content")
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("null_ok", func(t *testing.T) {
		item := cloneMap(base)
		item["encrypted_content"] = nil
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("string_ok", func(t *testing.T) {
		item := cloneMap(base)
		item["encrypted_content"] = "enc"
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("wrong_type", func(t *testing.T) {
		item := cloneMap(base)
		item["encrypted_content"] = 99
		assertStreamInvalid(t, outputItemEvent(item))
	})
}

func TestURIFields_StrictHTTP(t *testing.T) {
	// url_citation
	t.Run("url_citation_https_ok", func(t *testing.T) {
		ev := map[string]any{
			"type": "response.output_text.annotation.added", "item_id": "m1",
			"output_index": 0, "content_index": 0, "annotation_index": 0,
			"annotation": map[string]any{
				"type": "url_citation", "url": "https://example.com/path?q=1", "title": "t",
				"start_index": 0, "end_index": 1,
			},
		}
		assertStreamOK(t, ev)
	})
	t.Run("url_citation_http_ok", func(t *testing.T) {
		ev := map[string]any{
			"type": "response.output_text.annotation.added", "item_id": "m1",
			"output_index": 0, "content_index": 0, "annotation_index": 0,
			"annotation": map[string]any{
				"type": "url_citation", "url": "http://example.com", "title": "t",
				"start_index": 0, "end_index": 1,
			},
		}
		assertStreamOK(t, ev)
	})
	for _, bad := range []string{"", "/relative", "example.com", "javascript:alert(1)", "file:///etc/passwd", "https://", "https://user:pass@evil.com"} {
		bad := bad
		t.Run("url_citation_bad_"+bad, func(t *testing.T) {
			ev := map[string]any{
				"type": "response.output_text.annotation.added", "item_id": "m1",
				"output_index": 0, "content_index": 0, "annotation_index": 0,
				"annotation": map[string]any{
					"type": "url_citation", "url": bad, "title": "t",
					"start_index": 0, "end_index": 1,
				},
			}
			assertStreamInvalid(t, ev)
		})
	}
	// web search sources + open_page + find + CI image
	t.Run("web_source_relative_fail", func(t *testing.T) {
		item := map[string]any{
			"type": "web_search_call", "id": "ws1", "status": "completed",
			"action": map[string]any{
				"type": "search", "query": "q",
				"sources": []any{map[string]any{"url": "/rel"}},
			},
		}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("web_source_https_ok", func(t *testing.T) {
		item := map[string]any{
			"type": "web_search_call", "id": "ws1", "status": "completed",
			"action": map[string]any{
				"type": "search", "query": "q",
				"sources": []any{map[string]any{"type": "url", "url": "https://example.com"}},
			},
		}
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("open_page_null_ok", func(t *testing.T) {
		item := map[string]any{
			"type": "web_search_call", "id": "ws1", "status": "completed",
			"action": map[string]any{"type": "open_page", "url": nil},
		}
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("open_page_relative_fail", func(t *testing.T) {
		item := map[string]any{
			"type": "web_search_call", "id": "ws1", "status": "completed",
			"action": map[string]any{"type": "open_page", "url": "../x"},
		}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("find_relative_fail", func(t *testing.T) {
		item := map[string]any{
			"type": "web_search_call", "id": "ws1", "status": "searching",
			"action": map[string]any{"type": "find_in_page", "pattern": "x", "url": "not-a-url"},
		}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("ci_image_https_ok", func(t *testing.T) {
		item := fixtureOutputItemCodeInterpreter()
		item["outputs"] = []any{map[string]any{"type": "image", "url": "https://cdn.example.com/i.png"}}
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("ci_image_data_scheme_fail", func(t *testing.T) {
		item := fixtureOutputItemCodeInterpreter()
		item["outputs"] = []any{map[string]any{"type": "image", "url": "data:image/png;base64,xxx"}}
		assertStreamInvalid(t, outputItemEvent(item))
	})
}

func TestFunctionToolParameters_CatalogSchemaSecurity(t *testing.T) {
	base := fixtureOutputItemToolSearchOutput()
	mk := func(params any) map[string]any {
		item := cloneMap(base)
		item["tools"] = []any{map[string]any{
			"type": "function", "name": "echo", "strict": true,
			"parameters": params,
		}}
		return item
	}
	t.Run("safe_schema_ok", func(t *testing.T) {
		assertStreamOK(t, outputItemEvent(mk(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"q": map[string]any{"type": "string"},
			},
			"required": []any{"q"},
		})))
	})
	t.Run("empty_object_ok", func(t *testing.T) {
		assertStreamOK(t, outputItemEvent(mk(map[string]any{})))
	})

	// Build SSE with raw parameters JSON embedded (preserves non-object roots).
	// completed envelope matches validCompletedEnvelope shape used by sseWithCompleted.
	sseWithRawParams := func(paramsJSON string) string {
		data := `{"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":{"type":"tool_search_output","id":"tso1","call_id":"c1","execution":"client","status":"completed","tools":[{"type":"function","name":"echo","strict":true,"parameters":` + paramsJSON + `}]}}`
		// Use helper for completed terminal via empty intermediate then completed —
		// construct full stream with completed only after our event.
		return "event: response.output_item.added\ndata: " + data + "\n\n" +
			sseWithCompleted()[strings.Index(sseWithCompleted(), "event: response.completed"):]
	}

	hostiles := []struct {
		name      string
		rawParams string
	}{
		{"array_root", `[]`},
		{"string_root", `"x"`},
		{"null_root", `null`},
		{"external_ref", `{"$ref":"https://example.com/secret/schema.json"}`},
		{"local_ref", `{"$ref":"#/definitions/foo"}`},
		{"unsupported_allOf", `{"type":"object","allOf":[]}`},
		{"non_object_type", `{"type":"string"}`},
		{"numeric_unrepresentable", `{"type":"object","properties":{"n":{"type":"number","const":9007199254740993}}}`},
	}
	for _, h := range hostiles {
		h := h
		t.Run(h.name, func(t *testing.T) {
			err := validateVerificationResponsesPayload([]byte(sseWithRawParams(h.rawParams)), "text/event-stream")
			if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
				t.Fatalf("want stream invalid, got %v", err)
			}
			assertNoSecretLeak(t, err)
		})
	}

	// Oversized valid-looking object (raw byte cap before parse).
	t.Run("oversized", func(t *testing.T) {
		// MaxToolSchemaBytes is 32KiB; build object larger than cap.
		pad := strings.Repeat("z", einoruntime.MaxToolSchemaBytes)
		params := `{"type":"object","properties":{"p":{"type":"string","description":"` + pad + `"}}}`
		if len(params) <= einoruntime.MaxToolSchemaBytes {
			t.Fatalf("fixture not oversized: %d", len(params))
		}
		err := validateVerificationResponsesPayload([]byte(sseWithRawParams(params)), "text/event-stream")
		if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
			t.Fatalf("want stream invalid, got %v", err)
		}
		assertNoSecretLeak(t, err)
	})

	// Nested duplicate keys in SSE event/protocol payload → StreamInvalid only.
	t.Run("duplicate_key_raw_stream_invalid", func(t *testing.T) {
		data := `{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"tool_search_output","id":"tso1","call_id":"c1","execution":"client","status":"completed","tools":[{"type":"function","name":"echo","strict":true,"parameters":{"type":"object","type":"object"}}]}}`
		sse := "event: response.output_item.added\ndata: " + data + "\n\n" +
			sseWithCompleted()[strings.Index(sseWithCompleted(), "event: response.completed"):]
		err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
		if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
			t.Fatalf("want ErrAgenticStreamInvalid, got %v", err)
		}
		if errors.Is(err, modelconfig.ErrAgenticUsageInvalid) {
			t.Fatalf("SSE duplicate must not classify as usage invalid: %v", err)
		}
		assertNoSecretLeak(t, err)
	})
	t.Run("duplicate_event_type_stream_invalid", func(t *testing.T) {
		data := `{"type":"response.output_item.added","type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"message","id":"m1","status":"completed","role":"assistant","content":[]}}`
		sse := "event: response.output_item.added\ndata: " + data + "\n\n" +
			sseWithCompleted()[strings.Index(sseWithCompleted(), "event: response.completed"):]
		err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
		if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
			t.Fatalf("want ErrAgenticStreamInvalid, got %v", err)
		}
		if errors.Is(err, modelconfig.ErrAgenticUsageInvalid) {
			t.Fatalf("must not be usage invalid: %v", err)
		}
	})
	// Hostile default with secret marker.
	t.Run("default_secret_rejected", func(t *testing.T) {
		err := validateVerificationResponsesPayload([]byte(sseWithRawParams(
			`{"type":"object","properties":{"q":{"type":"string","default":"TASK3_M_SECRET"}}}`,
		)), "text/event-stream")
		if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
			t.Fatalf("want stream invalid for default, got %v", err)
		}
		assertNoSecretLeak(t, err)
		if strings.Contains(err.Error(), "TASK3_M_SECRET") {
			t.Fatalf("leaked secret: %v", err)
		}
	})
	t.Run("default_nested_object_rejected", func(t *testing.T) {
		err := validateVerificationResponsesPayload([]byte(sseWithRawParams(
			`{"type":"object","properties":{"o":{"type":"object","default":{"x":1}}}}`,
		)), "text/event-stream")
		if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
			t.Fatalf("want stream invalid, got %v", err)
		}
	})
	t.Run("default_array_rejected", func(t *testing.T) {
		err := validateVerificationResponsesPayload([]byte(sseWithRawParams(
			`{"type":"object","properties":{"a":{"type":"array","items":{"type":"string"},"default":["x"]}}}`,
		)), "text/event-stream")
		if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
			t.Fatalf("want stream invalid, got %v", err)
		}
	})

	// Direct nested schema path: parameters with dups reject without embedding body.
	t.Run("duplicate_key_nested_schema_path", func(t *testing.T) {
		obj := map[string]json.RawMessage{
			"parameters": json.RawMessage(`{"type":"object","type":"object"}`),
		}
		err := validateFunctionParametersSchemaField(obj, "parameters")
		if err == nil {
			t.Fatal("expected schema security failure")
		}
		if strings.Contains(err.Error(), "object\",\"type") {
			t.Fatalf("leaked schema body: %v", err)
		}
	})
}

func TestMCPListTool_InputSchemaAnyNonNull_SSE(t *testing.T) {
	// Permanent positives: pinned InputSchema is `any` required.
	for _, tc := range []struct {
		name   string
		schema any
		ok     bool
	}{
		{"object", map[string]any{"type": "object"}, true},
		{"array", []any{1, "x"}, true},
		{"string", "s", true},
		{"number", 3.14, true},
		{"boolean", true, true},
		{"null", nil, false},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			item := fixtureOutputItemMCPListTools()
			tool := map[string]any{"name": "t1", "input_schema": tc.schema}
			if tc.name == "null" {
				tool["input_schema"] = nil
			}
			item["tools"] = []any{tool}
			if tc.ok {
				assertStreamOK(t, outputItemEvent(item))
			} else {
				assertStreamInvalid(t, outputItemEvent(item))
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		item := fixtureOutputItemMCPListTools()
		item["tools"] = []any{map[string]any{"name": "t1"}}
		assertStreamInvalid(t, outputItemEvent(item))
	})
}

func TestShellOutputCreatedBy_Optional(t *testing.T) {
	base := fixtureOutputItemShellCallOutput()
	t.Run("missing_ok", func(t *testing.T) {
		assertStreamOK(t, outputItemEvent(base))
	})
	t.Run("string_ok", func(t *testing.T) {
		item := cloneMap(base)
		outs := item["output"].([]any)
		el := outs[0].(map[string]any)
		el["created_by"] = "actor-1"
		item["output"] = []any{el}
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("null_fail", func(t *testing.T) {
		item := cloneMap(base)
		outs := item["output"].([]any)
		el := outs[0].(map[string]any)
		el["created_by"] = nil
		item["output"] = []any{el}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("number_fail", func(t *testing.T) {
		item := cloneMap(base)
		outs := item["output"].([]any)
		el := outs[0].(map[string]any)
		el["created_by"] = 1
		item["output"] = []any{el}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("object_fail", func(t *testing.T) {
		item := cloneMap(base)
		outs := item["output"].([]any)
		el := outs[0].(map[string]any)
		el["created_by"] = map[string]any{"x": 1}
		item["output"] = []any{el}
		assertStreamInvalid(t, outputItemEvent(item))
	})
}

// Cycle 16 product probes: wrong/missing type discriminators fail at real SSE entry
// for empty shell env/outcome and overlapping MCP members.
func TestValidateVerificationSSE_TypeDiscriminator_ShellAndMCP(t *testing.T) {
	t.Run("shell_env_local_ok", func(t *testing.T) {
		item := fixtureOutputItemShellCall()
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("shell_env_missing_type", func(t *testing.T) {
		item := fixtureOutputItemShellCall()
		item["environment"] = map[string]any{}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("shell_env_wrong_type_timeout", func(t *testing.T) {
		// type timeout is a valid shell_outcome discriminator but not shell_env.
		item := fixtureOutputItemShellCall()
		item["environment"] = map[string]any{"type": "timeout"}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("shell_outcome_exit_ok", func(t *testing.T) {
		item := fixtureOutputItemShellCallOutput()
		assertStreamOK(t, outputItemEvent(item))
	})
	t.Run("shell_outcome_missing_type", func(t *testing.T) {
		item := fixtureOutputItemShellCallOutput()
		item["output"] = []any{map[string]any{
			"stdout": "x", "stderr": "",
			"outcome": map[string]any{"exit_code": 0},
		}}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("shell_outcome_wrong_type_local", func(t *testing.T) {
		item := fixtureOutputItemShellCallOutput()
		item["output"] = []any{map[string]any{
			"stdout": "x", "stderr": "",
			"outcome": map[string]any{"type": "local"},
		}}
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("mcp_call_ok", func(t *testing.T) {
		assertStreamOK(t, outputItemEvent(fixtureOutputItemMCPCall()))
	})
	t.Run("mcp_call_type_missing", func(t *testing.T) {
		item := fixtureOutputItemMCPCall()
		delete(item, "type")
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("mcp_call_type_fabricated", func(t *testing.T) {
		item := fixtureOutputItemMCPCall()
		item["type"] = "not_a_union_member"
		assertStreamInvalid(t, outputItemEvent(item))
	})
	t.Run("mcp_approval_ok", func(t *testing.T) {
		assertStreamOK(t, outputItemEvent(fixtureOutputItemMCPApproval()))
	})
	t.Run("mcp_approval_type_null", func(t *testing.T) {
		item := fixtureOutputItemMCPApproval()
		item["type"] = nil
		assertStreamInvalid(t, outputItemEvent(item))
	})
	// Overlapping MCP shapes: status "calling" is only valid on mcp_call domain.
	// mcp_approval_request does not list status — but type discriminator remains required.
	t.Run("mcp_call_status_calling_ok", func(t *testing.T) {
		item := fixtureOutputItemMCPCall()
		item["status"] = "calling"
		assertStreamOK(t, outputItemEvent(item))
	})
}
