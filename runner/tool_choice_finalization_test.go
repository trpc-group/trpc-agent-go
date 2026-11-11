//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	openaiopt "github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryfork"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestRunner_CallLimitFinalizationToolChoice(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, limit := range []string{"tool", "llm"} {
			for _, finalResponse := range []string{"answer", "tool_call", "http_error"} {
				t.Run(fmt.Sprintf("enabled=%v/limit=%s/final=%s", enabled, limit, finalResponse), func(t *testing.T) {
					var calls, executions, callbacks int
					var firstTools any
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body map[string]any
						require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
						calls++
						require.LessOrEqual(t, calls, 2)
						w.Header().Set("Content-Type", "application/json")
						if calls == 1 {
							firstTools = body["tools"]
						} else {
							if enabled {
								require.Equal(t, firstTools, body["tools"])
								require.Equal(t, "none", body["tool_choice"])
							} else {
								require.NotContains(t, body, "tools")
								require.NotContains(t, body, "tool_choice")
							}
							messages := body["messages"].([]any)
							roles := []string{}
							for _, m := range messages {
								roles = append(roles, m.(map[string]any)["role"].(string))
							}
							require.Equal(t, []string{"system", "user", "assistant", "tool", "user"}, roles)
							require.Equal(t, "finish", messages[len(messages)-1].(map[string]any)["content"])
							require.Contains(t, messages[len(messages)-2].(map[string]any)["content"], "ORCHID-91")
						}
						if calls == 2 && finalResponse == "http_error" {
							w.WriteHeader(http.StatusBadRequest)
							fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"none unsupported"}}`)
							return
						}
						if calls == 1 || finalResponse == "tool_call" {
							fmt.Fprint(w, `{"id":"tool","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"lookup-1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
						} else {
							fmt.Fprint(w, `{"id":"final","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ORCHID-91"},"finish_reason":"stop"}]}`)
						}
					}))
					defer server.Close()
					m := openai.New("test", openai.WithBaseURL(server.URL), openai.WithAPIKey("test-key"),
						openai.WithToolChoiceNoneWhenToolsDisabled(enabled),
						openai.WithOpenAIOptions(openaiopt.WithMaxRetries(0)))
					lookup := function.NewFunctionTool(func(context.Context, struct{}) (string, error) {
						executions++
						return "ORCHID-91", nil
					}, function.WithName("lookup"))
					modelCallbacks := model.NewCallbacks().RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
						callbacks++
						require.Equal(t, callbacks, calls+1)
						if callbacks == 2 {
							require.Len(t, args.Request.Tools, map[bool]int{false: 0, true: 1}[enabled])
							// Replacing the callback context must preserve the chosen policy.
							if args.Request.Tools == nil {
								args.Request.Tools = make(map[string]tool.Tool)
							}
							args.Request.Tools["injected"] = lookup
							args.Request.ExtraFields = map[string]any{"tool_choice": "required"}
							return &model.BeforeModelResult{Context: context.Background()}, nil
						}
						return nil, nil
					}).RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
						if callbacks == 2 {
							if enabled {
								require.Contains(t, args.Request.Tools, "lookup")
							} else {
								require.Empty(t, args.Request.Tools)
							}
							require.NotContains(t, args.Request.Tools, "injected")
							require.NotContains(t, args.Request.ExtraFields, "tool_choice")
							inv, ok := agent.InvocationFromContext(ctx)
							require.True(t, ok)
							// Summary forks are attached after callbacks and keep the same tools.
							_ = inv
						}
						return nil, nil
					}).RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
						if calls == 2 {
							inv, ok := agent.InvocationFromContext(ctx)
							require.True(t, ok)
							fork, ok := summaryfork.Request(inv)
							require.True(t, ok)
							require.Len(t, fork.Tools, map[bool]int{false: 0, true: 1}[enabled])
						}
						return nil, nil
					})
					opts := []llmagent.Option{llmagent.WithModel(m), llmagent.WithInstruction("use lookup"),
						llmagent.WithTools([]tool.Tool{lookup}), llmagent.WithModelCallbacks(modelCallbacks), llmagent.WithMaxLLMCalls(2)}
					if limit == "tool" {
						opts = append(opts, llmagent.WithMaxToolIterations(1), llmagent.WithToolIterationLimitFinalization("finish"))
					} else {
						opts = append(opts, llmagent.WithLLMCallLimitFinalization("finish"))
					}
					r := runner.NewRunner("probe", llmagent.New("assistant", opts...))
					defer r.Close()
					events, err := r.Run(context.Background(), "user", "session", model.NewUserMessage("lookup"))
					require.NoError(t, err)
					var errors int
					var answer string
					for e := range events {
						if e.Response == nil {
							continue
						}
						if e.Error != nil {
							errors++
						}
						for _, c := range e.Choices {
							if c.Message.Role == model.RoleAssistant {
								answer += c.Message.Content
							}
						}
					}
					require.Equal(t, 2, calls)
					require.Equal(t, 1, executions)
					require.Equal(t, 2, callbacks)
					if finalResponse != "answer" {
						require.Positive(t, errors)
					} else {
						require.Zero(t, errors)
						require.Equal(t, "ORCHID-91", answer)
					}
				})
			}
		}
	}
}

func TestRunner_CallLimitFinalizationHonorsBeforeModelToolFilter(t *testing.T) {
	var captured []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		captured = append(captured, body)
		w.Header().Set("Content-Type", "application/json")
		if len(captured) == 1 {
			fmt.Fprint(w, `{"id":"tool","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"lookup-1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
		} else {
			fmt.Fprint(w, `{"id":"final","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
		}
	}))
	defer server.Close()
	m := openai.New("test", openai.WithBaseURL(server.URL), openai.WithAPIKey("test-key"),
		openai.WithToolChoiceNoneWhenToolsDisabled(true), openai.WithOpenAIOptions(openaiopt.WithMaxRetries(0)))
	lookup := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { return "ok", nil }, function.WithName("lookup"))
	restricted := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { return "restricted", nil }, function.WithName("restricted"))
	callbacks := model.NewCallbacks().RegisterBeforeModel(func(_ context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		// A per-user filter applied to every request, including finalization.
		delete(args.Request.Tools, "restricted")
		return nil, nil
	})
	a := llmagent.New("assistant", llmagent.WithModel(m), llmagent.WithTools([]tool.Tool{lookup, restricted}),
		llmagent.WithModelCallbacks(callbacks), llmagent.WithMaxToolIterations(1), llmagent.WithToolIterationLimitFinalization("finish"))
	r := runner.NewRunner("filter", a)
	defer r.Close()
	ch, err := r.Run(context.Background(), "user", "session", model.NewUserMessage("lookup"))
	require.NoError(t, err)
	for e := range ch {
		if e.Response != nil {
			require.Nil(t, e.Error)
		}
	}
	require.Len(t, captured, 2)
	require.Equal(t, "none", captured[1]["tool_choice"])
	require.Equal(t, captured[0]["tools"], captured[1]["tools"])
}
