//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/tooljournal"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Tracking must preserve the effective-context and started-attempt contracts
// when durable admission is combined with the updated cancellation guards.
func TestToolTracking_CallbackCancellation(t *testing.T) {
	for _, mode := range []string{"callable", "retry", "stream"} {
		for _, stage := range []string{"before", "detached", "attempt"} {
			t.Run(mode+"/"+stage, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				inv := agent.NewInvocation(agent.WithInvocationSession(session.NewSession("a", "u", "s")))
				var saved tooljournal.Record
				tooljournal.Attach(inv, func(_ context.Context, e *event.Event) error {
					for key, value := range e.StateDelta {
						if len(key) >= len(tooljournal.Prefix) && key[:len(tooljournal.Prefix)] == tooljournal.Prefix {
							require.NoError(t, json.Unmarshal(value, &saved))
							if stage == "attempt" && saved.Calls[0].Attempts == 1 {
								cancel()
							}
						}
					}
					return nil
				})
				tc := model.ToolCall{ID: "call", Function: model.FunctionDefinitionParam{Name: "write", Arguments: []byte(`{}`)}}
				var err error
				ctx, err = tooljournal.Begin(tooljournal.WithSource(ctx, "source"), inv, newToolCallResponseWithCalls([]model.ToolCall{tc}))
				require.NoError(t, err)
				require.NoError(t, tooljournal.Dispatch(ctx, tc.ID))
				callbacks := tool.NewCallbacks()
				callbacks.RegisterBeforeTool(func(current context.Context, _ *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
					if stage == "attempt" {
						return nil, nil
					}
					cancel()
					if stage == "detached" {
						return &tool.BeforeToolResult{Context: context.Background()}, nil
					}
					return nil, nil
				})
				calls, after := 0, 0
				callbacks.RegisterAfterTool(func(context.Context, *tool.AfterToolArgs) (*tool.AfterToolResult, error) { after++; return nil, nil })
				call := func(current context.Context, _ []byte) (any, error) {
					require.NoError(t, current.Err())
					calls++
					return "done", nil
				}
				var tl tool.Tool = &mockCallableTool{declaration: &tool.Declaration{Name: "write"}, callFn: call}
				if mode == "stream" {
					tl = &cancellationStreamTool{call: func(current context.Context) error { _, err := call(current, nil); return err }}
				}
				p := NewFunctionCallResponseProcessor(false, callbacks)
				if mode == "retry" {
					p.toolRetryPolicy = &tool.RetryPolicy{MaxAttempts: 2}
				}
				_, _, _, _, _, err = p.executeToolWithCallbacks(ctx, inv, tc, tl, nil)
				if stage == "detached" {
					require.NoError(t, err)
					require.Equal(t, 1, calls)
					require.Equal(t, 1, after)
					require.Equal(t, 1, saved.Calls[0].Attempts, "replacing the context must retain attempt evidence")
				} else {
					require.ErrorIs(t, err, context.Canceled)
					require.Zero(t, calls)
					require.Zero(t, after, "an acknowledged attempt is not proof that the tool function started")
				}
			})
		}
	}
}
