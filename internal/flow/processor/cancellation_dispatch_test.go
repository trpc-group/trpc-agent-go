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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcall"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// controlledDeadlineContext expires when its parent is cancelled so tests can
// trigger deadline expiry after tool admission without relying on scheduling.
type controlledDeadlineContext struct {
	context.Context
}

func (c controlledDeadlineContext) Err() error {
	if c.Context.Err() != nil {
		return context.DeadlineExceeded
	}
	return nil
}

func TestCancellationDispatch_PersistsCompletedSequentialResult(t *testing.T) {
	for _, failAppend := range []bool{false, true} {
		name := "append succeeds"
		if failAppend {
			name = "append fails"
		}
		t.Run(name, func(t *testing.T) {
			watchdog, stopWatchdog := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopWatchdog()
			parent, expire := context.WithCancel(watchdog)
			defer expire()
			ctx := controlledDeadlineContext{parent}
			inv := agent.NewInvocation(
				agent.WithInvocationSession(&session.Session{ID: "test-session"}),
				agent.WithInvocationAgent(&mockAgentWithTools{name: "tester"}),
			)
			var persisted []*event.Event
			appender.Attach(inv, func(appendCtx context.Context, evt *event.Event) error {
				require.NoError(t, appendCtx.Err())
				persisted = append(persisted, evt)
				if failAppend {
					return errors.New("append failed")
				}
				return nil
			})
			var executed, afterTools []string
			tools := map[string]tool.Tool{
				"first": &mockInvocationStateDeltaTool{
					declaration: &tool.Declaration{Name: "first"},
					callFn: func(callCtx context.Context, _ []byte) (any, error) {
						require.NoError(t, callCtx.Err())
						executed = append(executed, "first")
						expire()
						return "committed", nil
					},
					delta: map[string][]byte{"completed": []byte("first")},
				},
				"second": &mockCallableTool{
					declaration: &tool.Declaration{Name: "second"},
					callFn: func(context.Context, []byte) (any, error) {
						executed = append(executed, "second")
						return "unexpected", nil
					},
				},
			}
			callbacks := tool.NewCallbacks().RegisterAfterTool(func(
				_ context.Context, args *tool.AfterToolArgs,
			) (*tool.AfterToolResult, error) {
				afterTools = append(afterTools, args.ToolName)
				return nil, nil
			})
			calls := []model.ToolCall{
				{ID: "call-first", Function: model.FunctionDefinitionParam{Name: "first"}},
				{ID: "call-second", Function: model.FunctionDefinitionParam{Name: "second"}},
			}
			eventChan := make(chan *event.Event, 4)
			got, err := NewFunctionCallResponseProcessor(false, callbacks).handleFunctionCallsAndSendEvent(
				ctx, inv, newToolCallResponseWithCalls(calls), tools, eventChan,
			)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Nil(t, got)
			require.Equal(t, []string{"first"}, executed)
			require.Equal(t, []string{"first"}, afterTools)
			require.Len(t, persisted, 1)
			require.Equal(t, []string{"call-first"}, persisted[0].GetToolResultIDs())
			require.Contains(t, persisted[0].Choices[0].Message.Content, "committed")
			require.Equal(t, []byte("first"), persisted[0].StateDelta["completed"])
			require.True(t, persisted[0].RequiresCompletion)
			require.Empty(t, eventChan)
		})
	}
}

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
