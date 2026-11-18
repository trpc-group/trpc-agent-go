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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCancellationDispatch_ToolCallbacks(t *testing.T) {
	cases := []struct {
		name  string
		calls int
		err   error
	}{
		{"pre_cancelled", 0, context.Canceled},
		{"before_cancelled", 0, context.Canceled},
		{"inherited", 0, context.Canceled},
		{"background", 1, nil},
		{"without_cancel", 1, nil},
		{"cancelled_replacement", 0, context.Canceled},
		{"expired_replacement", 0, context.DeadlineExceeded},
		{"permission_error", 0, context.Canceled},
		{"permission_allow_cancelled", 0, context.Canceled},
		{"tool_cancelled", 1, context.Canceled},
		{"retry_cancelled", 1, context.Canceled},
		{"success", 1, nil},
	}
	for _, mode := range []string{"callable", "retry", "stream"} {
		for _, pluginBefore := range []bool{false, true} {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("%s/plugin_before=%t/%s", mode, pluginBefore, tc.name), func(t *testing.T) {
					if tc.name == "retry_cancelled" && mode != "retry" {
						t.Skip("requires a retry policy")
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					beforeCalls, calls, pluginAfter, localAfter := 0, 0, 0, 0
					before := func(ctx context.Context, _ *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
						beforeCalls++
						switch tc.name {
						case "before_cancelled":
							cancel()
						case "inherited":
							cancel()
							return &tool.BeforeToolResult{Context: context.WithValue(ctx, struct{}{}, "value")}, nil
						case "background":
							cancel()
							return &tool.BeforeToolResult{Context: context.Background()}, nil
						case "without_cancel":
							cancel()
							return &tool.BeforeToolResult{Context: context.WithoutCancel(ctx)}, nil
						case "cancelled_replacement":
							replacement, stop := context.WithCancel(context.Background())
							stop()
							return &tool.BeforeToolResult{Context: replacement}, nil
						case "expired_replacement":
							replacement, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
							defer stop()
							return &tool.BeforeToolResult{Context: replacement}, nil
						}
						return nil, nil
					}
					callbacks := tool.NewCallbacks()
					mgr := plugin.MustNewManager(&hookPlugin{name: "cancel", reg: func(r *plugin.Registry) {
						if pluginBefore {
							r.BeforeTool(before)
						}
						r.AfterTool(func(_ context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
							pluginAfter++
							require.ErrorIs(t, args.Error, tc.err)
							return nil, nil
						})
					}})
					if !pluginBefore {
						callbacks.RegisterBeforeTool(before)
					}
					callbacks.RegisterAfterTool(func(_ context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
						localAfter++
						require.ErrorIs(t, args.Error, tc.err)
						return nil, nil
					})
					inv := agent.NewInvocation(agent.WithInvocationPlugins(mgr), agent.WithInvocationRunOptions(agent.NewRunOptions(
						agent.WithToolPermissionPolicyFunc(func(ctx context.Context, _ *tool.PermissionRequest) (tool.PermissionDecision, error) {
							if tc.name == "permission_error" {
								cancel()
								return tool.PermissionDecision{}, ctx.Err()
							}
							if tc.name == "permission_allow_cancelled" {
								cancel()
							}
							return tool.PermissionDecision{Action: tool.PermissionActionAllow}, nil
						}),
					)))
					ctx = agent.NewInvocationContext(ctx, inv)
					if tc.name == "pre_cancelled" {
						cancel()
					}
					call := func(ctx context.Context, _ []byte) (any, error) {
						calls++
						require.NoError(t, ctx.Err())
						if tc.name == "tool_cancelled" {
							cancel()
							return nil, ctx.Err()
						}
						if tc.name == "retry_cancelled" {
							return nil, errors.New("retryable")
						}
						return "done", nil
					}
					var retry *tool.RetryPolicy
					if mode == "retry" {
						retry = &tool.RetryPolicy{MaxAttempts: 2, RetryOn: func(context.Context, *tool.RetryInfo) (bool, error) { cancel(); return true, nil }}
					}
					var tl tool.Tool = &mockCallableTool{declaration: &tool.Declaration{Name: "write"}, callFn: call}
					if mode == "stream" {
						tl = &cancellationStreamTool{call: func(ctx context.Context) error { _, err := call(ctx, nil); return err }}
					}
					p := NewFunctionCallResponseProcessor(false, callbacks)
					p.toolRetryPolicy = retry
					_, _, _, _, _, err := p.executeToolWithCallbacks(ctx, inv, model.ToolCall{ID: "call", Function: model.FunctionDefinitionParam{Name: "write"}}, tl, nil)
					require.ErrorIs(t, err, tc.err)
					require.Equal(t, tc.calls, calls)
					require.Equal(t, tc.calls, pluginAfter)
					require.Equal(t, tc.calls, localAfter)
					if tc.name == "pre_cancelled" {
						require.Zero(t, beforeCalls)
					} else {
						require.Equal(t, 1, beforeCalls)
					}
				})
			}
		}
	}
}
