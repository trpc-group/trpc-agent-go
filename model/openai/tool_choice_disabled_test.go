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
	for _, iter := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, callbackMode := range []string{"typed", "fields", "override", "remove", "omit", "rename", "nested_rename"} {
				for _, hasTools := range []bool{false, true} {
					t.Run(fmt.Sprintf("iter=%v/stream=%v/override=%v/tools=%v", iter, stream, callbackMode, hasTools), func(t *testing.T) {
						var captured map[string]any
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
							if stream {
								w.Header().Set("Content-Type", "text/event-stream")
								fmt.Fprint(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
								return
							}
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprint(w, `{"id":"test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
						}))
						defer server.Close()
						controls := map[string]any{"tools": []any{}, "tool_choice": "required", "parallel_tool_calls": true, "function_call": "auto", "functions": []any{}}
						m := New("test",
							WithBaseURL(server.URL), WithAPIKey("test-key"),
							WithToolChoiceNoneWhenToolsDisabled(true),
							WithOpenAIOptions(openaiopt.WithJSONSet("tools", []any{}), openaiopt.WithJSONSet("tool_choice", "required"), openaiopt.WithMaxRetries(0)),
							WithExtraFields(controls),
							WithChatRequestCallback(func(_ context.Context, req *openaigo.ChatCompletionNewParams) {
								// Filter z, normalize a, and attempt to add a new tool.
								normalized := openaigo.ChatCompletionToolParam{Function: openaigo.FunctionDefinitionParam{
									Name: "a", Strict: openaigo.Bool(true), Description: openaigo.String("normalized"),
									Parameters: openaigo.FunctionParameters{"type": "object", "additionalProperties": false},
								}}
								normalized.SetExtraFields(map[string]any{"cache_control": map[string]any{"type": "ephemeral"}})
								candidates := []openaigo.ChatCompletionToolParam{normalized, {Function: openaigo.FunctionDefinitionParam{Name: "injected"}}}
								fields := map[string]any{"tool_choice": "required", "parallel_tool_calls": true,
									"function_call": "auto", "functions": []any{}, "callback_field": "kept"}
								req.ToolChoice = openaigo.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openaigo.String("required")}
								switch callbackMode {
								case "typed":
									req.Tools = candidates
								case "fields":
									fields["tools"] = candidates
								case "override":
									fields["model"] = "test"
									fields["messages"] = []any{map[string]any{"role": "user", "content": "test"}}
									fields["stream"] = stream
									fields["tools"] = candidates
									*req = openaiparam.Override[openaigo.ChatCompletionNewParams](fields)
									return
								case "remove":
									req.Tools = nil
								case "omit":
									fields["tools"] = openaiparam.Omit
								case "rename":
									for i := range req.Tools {
										req.Tools[i].Function.Name = "renamed"
									}
								case "nested_rename":
									for i := range req.Tools {
										req.Tools[i].Function.SetExtraFields(map[string]any{"name": "renamed"})
									}
								}
								req.SetExtraFields(fields)
							}),
						)
						req := &model.Request{
							Messages:         []model.Message{model.NewUserMessage("test")},
							GenerationConfig: model.GenerationConfig{Stream: stream},
							ExtraFields:      controls,
						}
						if hasTools {
							req.Tools = map[string]tool.Tool{"z": stubTool{decl: &tool.Declaration{Name: "z"}}, "a": stubTool{decl: &tool.Declaration{Name: "a"}}}
						}
						wantTools := `[{"type":"function","function":{"name":"a","strict":true,"description":"normalized","parameters":{"type":"object","additionalProperties":false}},"cache_control":{"type":"ephemeral"}}]`
						ctx := imodelrequest.WithToolsDisabled(context.Background())
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
						if hasTools && (callbackMode == "typed" || callbackMode == "fields" || callbackMode == "override") {
							require.Equal(t, "none", captured["tool_choice"])
							got, err := json.Marshal(captured["tools"])
							require.NoError(t, err)
							require.JSONEq(t, wantTools, string(got))
						} else {
							require.NotContains(t, captured, "tools")
							require.NotContains(t, captured, "tool_choice")
						}
						for _, field := range []string{"parallel_tool_calls", "function_call", "functions"} {
							require.NotContains(t, captured, field)
						}
						require.Equal(t, "kept", captured["callback_field"])
					})
				}
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
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
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

func TestToolsDisabledWithNone_InvalidCallbackRequest(t *testing.T) {
	for _, iter := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, invalid := range []string{"tools", "schema", "request"} {
				t.Run(fmt.Sprintf("iter=%v/stream=%v/invalid=%s", iter, stream, invalid), func(t *testing.T) {
					var calls, completions int
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						calls++
						w.WriteHeader(http.StatusBadRequest)
					}))
					defer server.Close()
					m := New("test", WithBaseURL(server.URL), WithAPIKey("test-key"),
						WithToolChoiceNoneWhenToolsDisabled(true), WithOpenAIOptions(openaiopt.WithMaxRetries(0)),
						WithChatStreamCompleteCallback(func(_ context.Context, _ *openaigo.ChatCompletionNewParams, acc *openaigo.ChatCompletionAccumulator, err error) {
							completions++
							require.Nil(t, acc)
							require.Error(t, err)
						}),
						WithChatRequestCallback(func(_ context.Context, req *openaigo.ChatCompletionNewParams) {
							switch invalid {
							case "tools":
								req.SetExtraFields(map[string]any{"tools": 42})
							case "schema":
								req.Tools[0].Function.Parameters = openaigo.FunctionParameters{"invalid": make(chan int)}
							case "request":
								*req = openaiparam.Override[openaigo.ChatCompletionNewParams]([]string{"invalid"})
							}
						}),
					)
					req := &model.Request{
						Messages:         []model.Message{model.NewUserMessage("test")},
						Tools:            map[string]tool.Tool{"lookup": stubTool{decl: &tool.Declaration{Name: "lookup"}}},
						GenerationConfig: model.GenerationConfig{Stream: stream},
					}
					ctx := imodelrequest.WithToolsDisabled(context.Background())
					var responses []*model.Response
					if iter {
						seq, err := m.GenerateContentIter(ctx, req)
						require.NoError(t, err)
						seq(func(resp *model.Response) bool { responses = append(responses, resp); return true })
					} else {
						ch, err := m.GenerateContent(ctx, req)
						require.NoError(t, err)
						for resp := range ch {
							responses = append(responses, resp)
						}
					}
					require.Zero(t, calls)
					require.Equal(t, map[bool]int{false: 0, true: 1}[stream], completions)
					require.Len(t, responses, 1)
					require.True(t, responses[0].Done)
					require.NotNil(t, responses[0].Error)
					require.Equal(t, model.ErrorTypeAPIError, responses[0].Error.Type)
					require.Contains(t, responses[0].Error.Message, "invalid tools-disabled request")
				})
			}
		}
	}
}

func TestToolsDisabledWithNone_KeepsCallbackToolSchema(t *testing.T) {
	var captured []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		captured = append(captured, body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	m := New("test", WithBaseURL(server.URL), WithAPIKey("test-key"),
		WithToolChoiceNoneWhenToolsDisabled(true),
		WithChatRequestCallback(func(_ context.Context, req *openaigo.ChatCompletionNewParams) {
			// An endpoint-wide, deterministic schema normalization applied on every request.
			for i := range req.Tools {
				req.Tools[i].Function.Strict = openaigo.Bool(true)
			}
		}),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("test")},
		Tools:    map[string]tool.Tool{"lookup": stubTool{decl: &tool.Declaration{Name: "lookup"}}},
	}
	for _, ctx := range []context.Context{context.Background(), imodelrequest.WithToolsDisabled(context.Background())} {
		ch, err := m.GenerateContent(ctx, req)
		require.NoError(t, err)
		for response := range ch {
			require.Nil(t, response.Error)
		}
	}
	require.Len(t, captured, 2)
	require.Equal(t, "none", captured[1]["tool_choice"])
	// Turning off calls should preserve the exact schemas used by the preceding request.
	require.Equal(t, captured[0]["tools"], captured[1]["tools"])
}
