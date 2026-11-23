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
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcall"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
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
			inv, traceCtx := newExecutionTraceProcessorInvocation(t)
			watchdog, stopWatchdog := context.WithTimeout(traceCtx, 5*time.Second)
			defer stopWatchdog()
			parent, expire := context.WithCancel(watchdog)
			defer expire()
			ctx := controlledDeadlineContext{parent}
			inv.Session = &session.Session{ID: "test-session"}
			var persisted []*event.Event
			appender.Attach(inv, func(appendCtx context.Context, evt *event.Event) error {
				require.NoError(t, appendCtx.Err())
				persisted = append(persisted, evt)
				if failAppend {
					return errors.New("append failed")
				}
				return nil
			})
			var executed, beforeTools, afterTools []string
			third := &statefulFunctionTool[struct{}, string]{
				FunctionTool: function.NewFunctionTool(func(context.Context, struct{}) (string, error) {
					executed = append(executed, "third")
					return "unexpected", nil
				}, function.WithName("third")),
			}
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
				"third": third,
			}
			callbacks := tool.NewCallbacks().RegisterBeforeTool(func(
				_ context.Context, args *tool.BeforeToolArgs,
			) (*tool.BeforeToolResult, error) {
				beforeTools = append(beforeTools, args.ToolName)
				return nil, nil
			}).RegisterAfterTool(func(
				_ context.Context, args *tool.AfterToolArgs,
			) (*tool.AfterToolResult, error) {
				afterTools = append(afterTools, args.ToolName)
				return nil, nil
			})
			calls := []model.ToolCall{
				{ID: "call-first", Function: model.FunctionDefinitionParam{Name: "first"}},
				{ID: "call-second", Function: model.FunctionDefinitionParam{Name: "second"}},
				{ID: "call-third", Function: model.FunctionDefinitionParam{Name: "third"}},
			}
			eventChan := make(chan *event.Event, 4)
			got, err := NewFunctionCallResponseProcessor(false, callbacks).handleFunctionCallsAndSendEvent(
				ctx, inv, newToolCallResponseWithCalls(calls), tools, eventChan,
			)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Nil(t, got)
			require.Equal(t, []string{"first"}, executed)
			require.Equal(t, []string{"first"}, beforeTools)
			require.Equal(t, []string{"first"}, afterTools)
			require.Zero(t, third.stateDeltaCalls)
			require.Len(t, persisted, 1)
			require.Equal(t, []string{"call-first", "call-second", "call-third"}, persisted[0].GetToolResultIDs())
			for i, choice := range persisted[0].Choices[1:] {
				require.Equal(t, i+1, choice.Index)
				require.JSONEq(t, `{"status":"not_executed","reason":"deadline_exceeded"}`, choice.Message.Content)
			}
			require.Contains(t, persisted[0].Choices[0].Message.Content, "committed")
			require.Equal(t, []byte("first"), persisted[0].StateDelta["completed"])
			require.NotContains(t, persisted[0].StateDelta, "state")
			require.True(t, persisted[0].RequiresCompletion)
			require.Empty(t, eventChan)
			executionTrace := agent.BuildExecutionTrace(inv, atrace.TraceStatusFailed)
			require.NotNil(t, executionTrace)
			require.Len(t, executionTrace.Steps, 1)
			require.Len(t, executionTrace.Steps[0].Tools, 2)
			require.Equal(t, "call-first", executionTrace.Steps[0].Tools[0].ID)
			require.Equal(t, "committed", executionTrace.Steps[0].Tools[0].Result)
			require.Equal(t, "call-second", executionTrace.Steps[0].Tools[1].ID)
			require.Contains(t, executionTrace.Steps[0].Tools[1].Error, context.DeadlineExceeded.Error())
		})
	}
}

func TestCancellationDispatch_ChildContextPreservesCompletedTrace(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			inv, ctx := newExecutionTraceProcessorInvocation(t)
			first := &statefulFunctionTool[struct{}, string]{
				FunctionTool: function.NewFunctionTool(func(context.Context, struct{}) (string, error) {
					return "completed-A", nil
				}, function.WithName("first")),
			}
			var executed, afterTools []string
			second := &mockCallableTool{declaration: &tool.Declaration{Name: "second"},
				callFn: func(context.Context, []byte) (any, error) {
					executed = append(executed, "second")
					return "unexpected", nil
				}}
			callbacks := tool.NewCallbacks().RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
				if args.ToolName != "second" {
					return nil, nil
				}
				var child context.Context
				var cancel context.CancelFunc
				if cause == context.Canceled {
					child, cancel = context.WithCancel(ctx)
				} else {
					child, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				}
				cancel()
				return &tool.BeforeToolResult{Context: child}, nil
			}).RegisterAfterTool(func(_ context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
				afterTools = append(afterTools, args.ToolName)
				return nil, nil
			})
			appends := 0
			appender.Attach(inv, func(context.Context, *event.Event) error { appends++; return nil })
			events := make(chan *event.Event, 4)
			calls := []model.ToolCall{
				{ID: "call-first", Function: model.FunctionDefinitionParam{Name: "first", Arguments: []byte(`{}`)}},
				{ID: "call-second", Function: model.FunctionDefinitionParam{Name: "second", Arguments: []byte(`{}`)}},
			}
			got, err := NewFunctionCallResponseProcessor(false, callbacks).handleFunctionCallsAndSendEvent(
				ctx, inv, newToolCallResponseWithCalls(calls), map[string]tool.Tool{"first": first, "second": second}, events,
			)
			require.ErrorIs(t, err, cause)
			require.Nil(t, got)
			require.NoError(t, ctx.Err())
			require.Empty(t, executed)
			require.Equal(t, []string{"first"}, afterTools)
			require.Zero(t, appends)
			require.Zero(t, first.stateDeltaCalls, "a child deadline must not enable batch finalization")
			require.Len(t, events, 1)
			require.Equal(t, cause.Error(), (<-events).Error.Message)
			trace := agent.BuildExecutionTrace(inv, atrace.TraceStatusFailed)
			require.Len(t, trace.Steps, 1)
			require.Len(t, trace.Steps[0].Tools, 2)
			require.Equal(t, "call-first", trace.Steps[0].Tools[0].ID)
			require.Equal(t, "first", trace.Steps[0].Tools[0].Name)
			require.Empty(t, trace.Steps[0].Tools[0].Error)
			require.Nil(t, trace.Steps[0].Tools[0].Result)
			require.Equal(t, "call-second", trace.Steps[0].Tools[1].ID)
			require.Contains(t, trace.Steps[0].Tools[1].Error, cause.Error())
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
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsupported", true: "stream"}[stream], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			after := 0
			callbacks := tool.NewCallbacks().RegisterAfterTool(func(context.Context, *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
				after++
				return nil, nil
			})
			tl := &cancellationStreamTool{selectStream: func() bool { cancel(); return stream }}
			_, _, _, _, _, err := NewFunctionCallResponseProcessor(false, callbacks).executeToolWithCallbacks(
				ctx, agent.NewInvocation(), model.ToolCall{Function: model.FunctionDefinitionParam{Name: "stream"}}, tl, nil)
			require.ErrorIs(t, err, context.Canceled)
			require.Zero(t, tl.calls)
			require.Zero(t, after)
		})
	}
}
