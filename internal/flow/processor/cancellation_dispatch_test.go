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
	"sync"
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

type cancellationStreamTool struct {
	calls        int
	call         func(context.Context) error
	selectStream func() bool
}

func (*cancellationStreamTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "stream"}
}

func (t *cancellationStreamTool) StreamableCall(ctx context.Context, _ []byte) (*tool.StreamReader, error) {
	t.calls++
	if t.call != nil {
		if err := t.call(ctx); err != nil {
			return nil, err
		}
	}
	stream := tool.NewStream(0)
	stream.Writer.Close()
	return stream.Reader, nil
}

func TestCancellationDispatch_StreamRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tl := &cancellationStreamTool{}
	_, _, _, _, err := NewFunctionCallResponseProcessor(false, nil).executeStreamableTool(
		ctx, agent.NewInvocation(), model.ToolCall{}, tl, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, tl.calls)
}

func TestCancellationDispatch_QueuedToolDoesNotStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	limiter := toolcall.NewLimiter(tool.ConcurrencyConfig{MaxConcurrency: 1})
	release, err := limiter.Acquire(context.Background(), "write")
	require.NoError(t, err)
	defer release()
	waiting := &dispatchWaitContext{Context: ctx, entered: make(chan struct{})}
	ctx = toolcall.WithLimiter(waiting, limiter)
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
	select {
	case <-waiting.entered:
	case <-time.After(time.Second):
		t.Fatal("dispatch never reached the occupied concurrency gate")
	}
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

type dispatchWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *dispatchWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func (t *cancellationStreamTool) StreamInner() bool {
	if t.selectStream != nil {
		return t.selectStream()
	}
	return true
}

func TestCancellationDispatch_CancelDuringToolSelection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	after := 0
	callbacks := tool.NewCallbacks().RegisterAfterTool(func(context.Context, *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		after++
		return nil, nil
	})
	tl := &cancellationStreamTool{selectStream: func() bool { cancel(); return true }}
	_, _, _, _, _, err := NewFunctionCallResponseProcessor(false, callbacks).executeToolWithCallbacks(
		ctx, agent.NewInvocation(), model.ToolCall{Function: model.FunctionDefinitionParam{Name: "stream"}}, tl, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, tl.calls)
	require.Zero(t, after)
}
