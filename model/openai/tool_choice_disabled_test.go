//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	openaigo "github.com/openai/openai-go"
	openaiopt "github.com/openai/openai-go/option"
	openaiparam "github.com/openai/openai-go/packages/param"
	"github.com/stretchr/testify/require"
	imodelrequest "trpc.group/trpc-go/trpc-agent-go/internal/modelrequest"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestToolsDisabledWithNone_WireContract(t *testing.T) {
	makeTool := func(name string) map[string]any {
		return map[string]any{
			"type":          "function",
			"function":      map[string]any{"name": name, "vendor_schema": "preserved"},
			"cache_control": map[string]any{"type": "ephemeral"},
		}
	}
	cases := []struct {
		name          string
		noTools       bool
		callback      func(*openaigo.ChatCompletionNewParams)
		modelFields   map[string]any
		requestFields map[string]any
		sdkOptions    []openaiopt.RequestOption
		wantNames     []string
		wantAbsent    bool
		wantValue     any
		check         func(*testing.T, []any)
	}{
		{
			name: "callback_add_rename_duplicate",
			callback: func(req *openaigo.ChatCompletionNewParams) {
				req.Tools[0].Function.Name = "renamed"
				req.Tools[0].SetExtraFields(map[string]any{
					"cache_control": map[string]any{"type": "ephemeral"},
				})
				req.Tools = append(req.Tools, req.Tools[0], openaigo.ChatCompletionToolParam{
					Function: openaigo.FunctionDefinitionParam{Name: "added"},
				})
			},
			wantNames: []string{"renamed", "omega", "renamed", "added"},
			check: func(t *testing.T, tools []any) {
				require.Equal(t, map[string]any{"type": "ephemeral"}, tools[0].(map[string]any)["cache_control"])
				require.Equal(t, tools[0], tools[2])
			},
		},
		{
			name:       "callback_remove",
			callback:   func(req *openaigo.ChatCompletionNewParams) { req.Tools = nil },
			wantAbsent: true,
		},
		{
			name: "callback_nested_fields",
			callback: func(req *openaigo.ChatCompletionNewParams) {
				req.Tools[0].Function.SetExtraFields(map[string]any{
					"name": "nested_rename", "vendor_schema": "preserved",
				})
			},
			wantNames: []string{"nested_rename", "omega"},
			check: func(t *testing.T, tools []any) {
				fn := tools[0].(map[string]any)["function"].(map[string]any)
				require.Equal(t, "preserved", fn["vendor_schema"])
			},
		},
		{
			name: "callback_nested_override",
			callback: func(req *openaigo.ChatCompletionNewParams) {
				req.Tools[0] = openaiparam.Override[openaigo.ChatCompletionToolParam](
					json.RawMessage(`{"type":"function","function":{"name":"nested_override","custom":true},"vendor":"kept"}`),
				)
			},
			wantNames: []string{"nested_override", "omega"},
			check: func(t *testing.T, tools []any) {
				require.Equal(t, "kept", tools[0].(map[string]any)["vendor"])
			},
		},
		{
			name: "callback_extra_fields",
			callback: func(req *openaigo.ChatCompletionNewParams) {
				req.SetExtraFields(map[string]any{
					"tools":       []any{makeTool("extra"), makeTool("extra")},
					"tool_choice": "required", "function_call": "auto",
					"functions":      []any{map[string]any{"name": "legacy"}},
					"callback_field": "kept",
				})
			},
			wantNames: []string{"extra", "extra"},
			check: func(t *testing.T, tools []any) {
				require.Equal(t, makeTool("extra"), tools[0])
			},
		},
		{
			name: "callback_omit_tools",
			callback: func(req *openaigo.ChatCompletionNewParams) {
				req.SetExtraFields(map[string]any{"tools": openaiparam.Omit, "tool_choice": "required"})
			},
			wantAbsent: true,
		},
		{
			name: "callback_whole_override",
			callback: func(req *openaigo.ChatCompletionNewParams) {
				*req = openaiparam.Override[openaigo.ChatCompletionNewParams](map[string]any{
					"model": "test", "messages": []any{map[string]any{"role": "user", "content": "test"}},
					"tools":       []any{makeTool("whole"), makeTool("whole")},
					"tool_choice": "required", "function_call": "auto",
					"functions":      []any{map[string]any{"name": "legacy"}},
					"callback_field": "kept",
				})
			},
			wantNames: []string{"whole", "whole"},
			check: func(t *testing.T, tools []any) {
				require.Equal(t, makeTool("whole"), tools[0])
			},
		},
		{
			name: "model_extra_fields",
			modelFields: map[string]any{
				"tools": []any{makeTool("model")}, "tool_choice": "required",
				"functions": []any{map[string]any{"name": "model"}}, "function_call": "auto",
			},
			wantNames: []string{"model"},
			check:     func(t *testing.T, tools []any) { require.Equal(t, makeTool("model"), tools[0]) },
		},
		{
			name:        "request_extra_fields_override_model",
			modelFields: map[string]any{"tools": []any{makeTool("model")}, "tool_choice": "required"},
			requestFields: map[string]any{
				"tools": []any{makeTool("request")}, "tool_choice": "required",
				"functions": []any{map[string]any{"name": "request"}}, "function_call": "auto",
			},
			wantNames: []string{"request"},
			check:     func(t *testing.T, tools []any) { require.Equal(t, makeTool("request"), tools[0]) },
		},
		{
			name: "sdk_tools_and_nested_override",
			sdkOptions: []openaiopt.RequestOption{
				openaiopt.WithJSONSet("tools", []any{makeTool("sdk"), makeTool("sdk")}),
				openaiopt.WithJSONSet("tools.0.function.name", "sdk_nested"),
			},
			wantNames: []string{"sdk_nested", "sdk"},
			check: func(t *testing.T, tools []any) {
				tool := tools[0].(map[string]any)
				require.Equal(t, map[string]any{"type": "ephemeral"}, tool["cache_control"])
				require.Equal(t, "preserved", tool["function"].(map[string]any)["vendor_schema"])
			},
		},
		{
			name:    "callback_adds_to_empty",
			noTools: true,
			callback: func(req *openaigo.ChatCompletionNewParams) {
				req.Tools = []openaigo.ChatCompletionToolParam{{Function: openaigo.FunctionDefinitionParam{Name: "added"}}}
			},
			wantNames: []string{"added"},
		},
		{name: "no_tools", noTools: true, wantAbsent: true},
		{
			name: "tools_are_not_validated",
			callback: func(req *openaigo.ChatCompletionNewParams) {
				req.SetExtraFields(map[string]any{"tools": 42})
			},
			wantValue: float64(42),
		},
		{
			name: "provider_specific_tool",
			callback: func(req *openaigo.ChatCompletionNewParams) {
				req.SetExtraFields(map[string]any{"tools": []any{
					map[string]any{"type": "web_search", "vendor_option": "kept"},
				}})
			},
			wantValue: []any{map[string]any{"type": "web_search", "vendor_option": "kept"}},
		},
	}

	for _, iter := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("iter=%v/stream=%v/%s", iter, stream, tc.name), func(t *testing.T) {
					captured := make(chan []byte, 2)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						body, err := io.ReadAll(r.Body)
						if err != nil {
							http.Error(w, err.Error(), http.StatusBadRequest)
							return
						}
						captured <- body
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprint(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
							return
						}
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{"id":"test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
					}))
					defer server.Close()
					sdkOptions := []openaiopt.RequestOption{
						openaiopt.WithMaxRetries(0),
						openaiopt.WithJSONSet("tool_choice", "required"),
						openaiopt.WithJSONSet("function_call", "auto"),
						openaiopt.WithJSONSet("functions", []any{map[string]any{"name": "legacy"}}),
						openaiopt.WithJSONSet("parallel_tool_calls", true),
					}
					sdkOptions = append(sdkOptions, tc.sdkOptions...)
					m := New("test", WithBaseURL(server.URL), WithAPIKey("test-key"),
						WithToolChoiceNoneWhenToolsDisabled(true), WithOpenAIOptions(sdkOptions...),
						WithExtraFields(tc.modelFields),
						WithChatRequestCallback(func(_ context.Context, req *openaigo.ChatCompletionNewParams) {
							req.ToolChoice = openaigo.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openaigo.String("required")}
							req.FunctionCall = openaigo.ChatCompletionNewParamsFunctionCallUnion{OfFunctionCallMode: openaigo.String("auto")}
							if tc.callback != nil {
								tc.callback(req)
							}
						}),
					)
					req := &model.Request{
						Messages:         []model.Message{model.NewUserMessage("test")},
						GenerationConfig: model.GenerationConfig{Stream: stream},
						ExtraFields:      tc.requestFields,
					}
					if !tc.noTools {
						req.Tools = map[string]tool.Tool{
							"alpha": stubTool{decl: &tool.Declaration{Name: "alpha"}},
							"omega": stubTool{decl: &tool.Declaration{Name: "omega"}},
						}
					}
					var bodies []map[string]any
					for _, ctx := range []context.Context{context.Background(), imodelrequest.WithToolsDisabled(context.Background())} {
						if iter {
							seq, err := m.GenerateContentIter(ctx, req)
							require.NoError(t, err)
							seq(func(resp *model.Response) bool { require.Nil(t, resp.Error); return true })
						} else {
							ch, err := m.GenerateContent(ctx, req)
							require.NoError(t, err)
							for resp := range ch {
								require.Nil(t, resp.Error)
							}
						}
						var body map[string]any
						require.NoError(t, json.Unmarshal(<-captured, &body))
						bodies = append(bodies, body)
					}
					require.Equal(t, "required", bodies[0]["tool_choice"])
					require.Equal(t, "none", bodies[1]["tool_choice"])
					require.Equal(t, bodies[0]["tools"], bodies[1]["tools"])
					require.Equal(t, bodies[0]["parallel_tool_calls"], bodies[1]["parallel_tool_calls"])
					require.NotContains(t, bodies[1], "functions")
					require.NotContains(t, bodies[1], "function_call")
					if tc.wantAbsent {
						require.NotContains(t, bodies[0], "tools")
						require.NotContains(t, bodies[1], "tools")
					} else if tc.wantValue != nil {
						require.Equal(t, tc.wantValue, bodies[1]["tools"])
					} else {
						tools, ok := bodies[1]["tools"].([]any)
						require.True(t, ok)
						require.Len(t, tools, len(tc.wantNames))
						for i, name := range tc.wantNames {
							fn := tools[i].(map[string]any)["function"].(map[string]any)
							require.Equal(t, name, fn["name"])
						}
						if tc.check != nil {
							tc.check(t, tools)
						}
					}
				})
			}
		}
	}
}

func TestToolsDisabledWithNone_OrdinaryCallsAndBudget(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("test")},
		Tools:    map[string]tool.Tool{"lookup": stubTool{decl: &tool.Declaration{Name: "lookup", Description: "A long tool description counted in the input budget."}}},
	}
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	m := New("test", WithBaseURL(server.URL), WithAPIKey("test-key"), WithContextWindow(4096), WithToolChoiceNoneWhenToolsDisabled(true), WithExtraFields(map[string]any{"tool_choice": "required"}))
	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)
	for resp := range ch {
		require.Nil(t, resp.Error)
	}
	require.Equal(t, "required", captured["tool_choice"])
	require.Len(t, captured["tools"], 1)
	require.True(t, m.PreservesToolsWhenDisabled())
	require.False(t, New("test").PreservesToolsWhenDisabled())
	require.False(t, New("test", WithToolChoiceNoneWhenToolsDisabled(true), WithToolChoiceNoneWhenToolsDisabled(false)).PreservesToolsWhenDisabled())

	withoutTools := *req
	withoutTools.Tools = nil
	require.Less(t, m.InputTokenBudget(context.Background(), req), m.InputTokenBudget(context.Background(), &withoutTools))
}

func TestToolsDisabledWithNone_InvalidSchemaUsesNormalCallback(t *testing.T) {
	for _, iter := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, disabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("iter=%v/stream=%v/disabled=%v", iter, stream, disabled), func(t *testing.T) {
					var jsonCallbacks int
					var jsonErr error
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusInternalServerError)
					}))
					defer server.Close()
					m := New("test", WithBaseURL(server.URL), WithAPIKey("test-key"),
						WithToolChoiceNoneWhenToolsDisabled(true), WithOpenAIOptions(openaiopt.WithMaxRetries(0)),
						WithChatRequestCallback(func(_ context.Context, req *openaigo.ChatCompletionNewParams) {
							req.Tools[0].Function.Parameters = openaigo.FunctionParameters{"invalid": make(chan int)}
						}),
						WithChatRequestJSONCallback(func(_ context.Context, _ []byte, err error) {
							jsonCallbacks++
							jsonErr = err
						}),
					)
					req := &model.Request{
						Messages:         []model.Message{model.NewUserMessage("test")},
						Tools:            map[string]tool.Tool{"lookup": stubTool{decl: &tool.Declaration{Name: "lookup"}}},
						GenerationConfig: model.GenerationConfig{Stream: stream},
					}
					ctx := context.Background()
					if disabled {
						ctx = imodelrequest.WithToolsDisabled(ctx)
					}
					var responses []*model.Response
					if iter {
						seq, err := m.GenerateContentIter(ctx, req)
						require.NoError(t, err)
						seq(func(response *model.Response) bool {
							responses = append(responses, response)
							return true
						})
					} else {
						ch, err := m.GenerateContent(ctx, req)
						require.NoError(t, err)
						for response := range ch {
							responses = append(responses, response)
						}
					}
					require.Equal(t, 1, jsonCallbacks)
					require.Error(t, jsonErr)
					require.Len(t, responses, 1)
					require.NotNil(t, responses[0].Error)
					require.NotContains(t, responses[0].Error.Message, "invalid tools-disabled request")
				})
			}
		}
	}
}
