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
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcall"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// #2757: pre-cancelled dispatch must not perform external work.
func TestCancellationDispatch_PreCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	tl := &mockCallableTool{declaration: &tool.Declaration{Name: "write"},
		callFn: func(context.Context, []byte) (any, error) { calls.Add(1); return "written", nil }}
	p := NewFunctionCallResponseProcessor(false, nil)
	_, err := p.executeToolCall(ctx, agent.NewInvocation(),
		model.ToolCall{ID: "a", Function: model.FunctionDefinitionParam{Name: "write"}},
		map[string]tool.Tool{"write": tl}, 0, make(chan *event.Event, 2))
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, calls.Load())
}

type cancellationStreamTool struct{ calls int }

func (*cancellationStreamTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "stream"}
}

func (t *cancellationStreamTool) StreamableCall(context.Context, []byte) (*tool.StreamReader, error) {
	t.calls++
	return nil, nil
}

func TestCancellationDispatch_StreamRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tl := &cancellationStreamTool{}
	_, _, _, err := NewFunctionCallResponseProcessor(false, nil).executeStreamableTool(
		ctx, agent.NewInvocation(), model.ToolCall{}, tl, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, tl.calls)
}

func TestCancellationDispatch_BeforeHookStillRunsCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callbacks := tool.NewCallbacks()
	callbacks.RegisterBeforeTool(func(context.Context, *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		cancel()
		return nil, nil
	})
	cleaned := false
	callbacks.RegisterAfterTool(func(_ context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		cleaned = true
		require.ErrorIs(t, args.Error, context.Canceled)
		return nil, nil
	})
	called := false
	tl := &mockCallableTool{declaration: &tool.Declaration{Name: "write"},
		callFn: func(context.Context, []byte) (any, error) { called = true; return "written", nil }}
	p := NewFunctionCallResponseProcessor(false, callbacks)
	_, _, _, _, _, err := p.executeToolWithCallbacks(ctx, agent.NewInvocation(),
		model.ToolCall{ID: "a", Function: model.FunctionDefinitionParam{Name: "write"}}, tl, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, called)
	require.True(t, cleaned)
}

func TestCancellationDispatch_QueuedToolDoesNotStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	limiter := toolcall.NewLimiter(tool.ConcurrencyConfig{MaxConcurrency: 1})
	release, err := limiter.Acquire(context.Background(), "write")
	require.NoError(t, err)
	defer release()
	ctx = toolcall.WithLimiter(ctx, limiter)
	var calls atomic.Int32
	tl := &mockCallableTool{declaration: &tool.Declaration{Name: "write"},
		callFn: func(context.Context, []byte) (any, error) { calls.Add(1); return "written", nil }}
	done := make(chan error, 1)
	go func() {
		_, err := NewFunctionCallResponseProcessor(false, nil).executeToolCall(ctx, agent.NewInvocation(),
			model.ToolCall{ID: "a", Function: model.FunctionDefinitionParam{Name: "write"}},
			map[string]tool.Tool{"write": tl}, 0, nil)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("queued dispatch did not stop")
	}
	require.Zero(t, calls.Load())
}

func TestCancellationDispatch_SequentialStopsBeforeNextTool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var executed []string
	tools := map[string]tool.Tool{}
	for _, name := range []string{"first", "second"} {
		name := name
		tools[name] = &mockCallableTool{declaration: &tool.Declaration{Name: name},
			callFn: func(context.Context, []byte) (any, error) {
				executed = append(executed, name)
				cancel()
				return "done", nil
			}}
	}
	inv := agent.NewInvocation(agent.WithInvocationAgent(&mockAgentWithTools{name: "test"}))
	_, err := NewFunctionCallResponseProcessor(false, nil).handleFunctionCalls(ctx, inv,
		newToolCallResponseWithCalls([]model.ToolCall{
			{ID: "a", Function: model.FunctionDefinitionParam{Name: "first"}},
			{ID: "b", Function: model.FunctionDefinitionParam{Name: "second"}},
		}), tools, make(chan *event.Event, 4))
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, []string{"first"}, executed)
}
