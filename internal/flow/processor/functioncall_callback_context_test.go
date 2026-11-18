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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestParallelToolResults_PreserveCachedResultWithCanceledCallbackContext(t *testing.T) {
	callbacks := tool.NewCallbacks().RegisterBeforeTool(func(
		ctx context.Context, args *tool.BeforeToolArgs,
	) (*tool.BeforeToolResult, error) {
		if args.ToolName != "cached" {
			return nil, nil
		}
		child, cancel := context.WithCancel(ctx)
		cancel()
		return &tool.BeforeToolResult{Context: child}, nil
	}).RegisterBeforeTool(func(
		ctx context.Context, args *tool.BeforeToolArgs,
	) (*tool.BeforeToolResult, error) {
		if args.ToolName != "cached" {
			return nil, nil
		}
		if ctx.Err() != context.Canceled {
			t.Error("later callback did not receive the cancelled replacement")
		}
		return &tool.BeforeToolResult{CustomResult: "cached"}, nil
	})
	var cachedCalls, siblingCalls atomic.Int32
	tools := map[string]tool.Tool{
		"cached": &mockCallableTool{
			declaration: &tool.Declaration{Name: "cached"},
			callFn: func(context.Context, []byte) (any, error) {
				cachedCalls.Add(1)
				return "unexpected", nil
			},
		},
		"sibling": &mockCallableTool{
			declaration: &tool.Declaration{Name: "sibling"},
			callFn: func(context.Context, []byte) (any, error) {
				siblingCalls.Add(1)
				return "sibling result", nil
			},
		},
	}
	calls := []model.ToolCall{
		{ID: "call-cached", Function: model.FunctionDefinitionParam{Name: "cached"}},
		{ID: "call-sibling", Function: model.FunctionDefinitionParam{Name: "sibling"}},
	}
	p := NewFunctionCallResponseProcessor(true, callbacks)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Exercise the old select race between a ready result channel and the
	// callback's cancelled context. Every completed result must be retained.
	const rounds = 50
	for i := 0; i < rounds; i++ {
		inv := agent.NewInvocation(agent.WithInvocationAgent(&mockAgentWithTools{name: "tester"}))
		evt, err := p.handleFunctionCalls(ctx, inv, newToolCallResponseWithCalls(calls), tools, nil)
		require.NoError(t, err)
		require.NotNil(t, evt)
		require.Equal(t, []string{"call-cached", "call-sibling"}, evt.GetToolResultIDs())
		require.Contains(t, evt.Choices[0].Message.Content, "cached")
		require.Contains(t, evt.Choices[1].Message.Content, "sibling result")
	}
	require.Zero(t, cachedCalls.Load())
	require.EqualValues(t, rounds, siblingCalls.Load())
}

func TestParallelToolResults_WorkerCancellationStopsPublication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callbacks := tool.NewCallbacks().RegisterBeforeTool(func(
		context.Context, *tool.BeforeToolArgs,
	) (*tool.BeforeToolResult, error) {
		cancel()
		return &tool.BeforeToolResult{
			Context:      context.Background(),
			CustomResult: "cached",
		}, nil
	})
	var calls atomic.Int32
	tools := map[string]tool.Tool{
		"cached": &mockCallableTool{
			declaration: &tool.Declaration{Name: "cached"},
			callFn: func(context.Context, []byte) (any, error) {
				calls.Add(1)
				return "unexpected", nil
			},
		},
	}
	inv := agent.NewInvocation(agent.WithInvocationAgent(&mockAgentWithTools{name: "tester"}))
	tc := model.ToolCall{ID: "call-cached", Function: model.FunctionDefinitionParam{Name: "cached"}}
	results := make(chan toolResult)
	done := make(chan error, 1)
	go func() {
		done <- NewFunctionCallResponseProcessor(true, callbacks).runParallelToolCall(
			ctx, inv, newToolCallResponseWithCalls([]model.ToolCall{tc}),
			tools, nil, results, false, 0, tc,
		)
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		// Release a broken publisher before failing so the regression does
		// not leave its worker blocked on the unbuffered result channel.
		select {
		case <-results:
		case <-time.After(time.Second):
		}
		t.Fatal("worker cancellation did not stop result publication")
	}
	require.Zero(t, calls.Load())
}
