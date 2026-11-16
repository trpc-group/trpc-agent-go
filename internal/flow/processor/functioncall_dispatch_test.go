//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcall"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

type dispatchProbeTool struct {
	name string
	call func(context.Context, []byte) (any, error)
}

func (t *dispatchProbeTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name, Description: "Dispatch regression probe", InputSchema: &tool.Schema{Type: "object"}}
}

func (t *dispatchProbeTool) Call(ctx context.Context, args []byte) (any, error) {
	return t.call(ctx, args)
}

func dispatchProbeInvocation() *agent.Invocation {
	inv := agent.NewInvocation(agent.WithInvocationSession(&session.Session{
		State: session.StateMap{"baseline": []byte("original")},
	}))
	inv.AgentName = "dispatch-test"
	inv.RunOptions.DisableTracing = true
	return inv
}

func dispatchProbeCalls(n int, name string) []model.ToolCall {
	calls := make([]model.ToolCall, n)
	for i := range calls {
		calls[i] = model.ToolCall{ID: fmt.Sprintf("call-%d", i), Type: "function",
			Function: model.FunctionDefinitionParam{Name: name, Arguments: []byte(fmt.Sprintf(`{"index":%d}`, i))}}
	}
	return calls
}

// A context-cloning hook represents integration-owned allocations. Limiting
// tool function entries alone must not leave one such allocation per queued call.
func TestParallelDispatchBoundsPreparation(t *testing.T) {
	for _, count := range []int{1024, 28071} {
		t.Run(fmt.Sprintf("calls=%d", count), func(t *testing.T) {
			testParallelDispatchBoundsPreparation(t, count)
		})
	}
}

func testParallelDispatchBoundsPreparation(t *testing.T, count int) {
	t.Helper()
	const limit = 4
	type probeKey struct{}
	var clones atomic.Int32
	agent.SetGoroutineContextCloner(func(ctx context.Context) context.Context {
		if ctx.Value(probeKey{}) != nil {
			clones.Add(1)
		}
		return ctx
	})
	t.Cleanup(func() { agent.SetGoroutineContextCloner(nil) })
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), probeKey{}, true), 5*time.Second)
	defer cancel()
	ctx = toolcall.WithLimiter(ctx, toolcall.NewLimiter(tool.ConcurrencyConfig{MaxConcurrency: limit}))
	started := make(chan struct{}, count)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	blocking := &dispatchProbeTool{name: "blocked", call: func(ctx context.Context, _ []byte) (any, error) {
		started <- struct{}{}
		select {
		case <-release:
			return "ok", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	p := NewFunctionCallResponseProcessor(true, nil)
	results, wait := p.startParallelToolCalls(ctx, dispatchProbeInvocation(), &model.Response{},
		dispatchProbeCalls(count, "blocked"), map[string]tool.Tool{"blocked": blocking}, nil, false)
	done := make(chan error, 1)
	go func() {
		for range results {
		}
		done <- wait()
	}()
	t.Cleanup(func() {
		unblock()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("dispatch did not finish")
		}
	})
	for i := 0; i < limit; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	require.Never(t, func() bool { return clones.Load() > limit }, 100*time.Millisecond, time.Millisecond)
	require.EqualValues(t, limit, clones.Load())
	cancel()
	require.Never(t, func() bool { return clones.Load() > limit }, 50*time.Millisecond, time.Millisecond)
	t.Logf("queued=%d admitted=%d context_clones=%d", count, limit, clones.Load())
}

func TestParallelDispatchBusyGroupDoesNotBlockOtherTools(t *testing.T) {
	for _, overall := range []int{2, 0} {
		t.Run(fmt.Sprintf("overall=%d", overall), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx = toolcall.WithLimiter(ctx, toolcall.NewLimiter(tool.ConcurrencyConfig{
				MaxConcurrency: overall, Groups: []tool.ConcurrencyGroup{{ToolNames: []string{"slow", "also-slow"}, Limit: 1}},
			}))
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			started := make(chan string, 16)
			tools := make(map[string]tool.Tool)
			for _, name := range []string{"slow", "also-slow", "free"} {
				name := name
				tools[name] = &dispatchProbeTool{name: name, call: func(ctx context.Context, _ []byte) (any, error) {
					started <- name
					select {
					case <-release:
						return name, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}}
			}
			calls := dispatchProbeCalls(8, "slow")
			calls[1].Function.Name = "also-slow"
			for i := 5; i < len(calls); i++ {
				calls[i].Function.Name = "free"
			}
			p := NewFunctionCallResponseProcessor(true, nil)
			results, wait := p.startParallelToolCalls(ctx, dispatchProbeInvocation(), &model.Response{}, calls, tools, nil, false)
			done := make(chan error, 1)
			var indexes []int
			go func() {
				for r := range results {
					indexes = append(indexes, r.index)
				}
				done <- wait()
			}()
			want := 2
			if overall == 0 {
				want = 4
			}
			seen := make(map[string]int)
			for i := 0; i < want; i++ {
				select {
				case name := <-started:
					seen[name]++
				case <-ctx.Done():
					t.Fatal("busy group blocked unrelated tools")
				}
			}
			require.Equal(t, 1, seen["slow"]+seen["also-slow"])
			require.Equal(t, want-1, seen["free"])
			unblock()
			require.NoError(t, <-done)
			require.ElementsMatch(t, []int{0, 1, 2, 3, 4, 5, 6, 7}, indexes)
		})
	}
}

func TestParallelDispatchQueuedPerCallUsesInitialSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = toolcall.WithLimiter(ctx, toolcall.NewLimiter(tool.ConcurrencyConfig{MaxConcurrency: 1}))
	inv := dispatchProbeInvocation()
	started := make(chan string, 2)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	probe := &dispatchProbeTool{name: "probe", call: func(ctx context.Context, _ []byte) (any, error) {
		view, _ := agent.InvocationFromContext(ctx)
		value, _ := view.Session.GetState("baseline")
		started <- string(value)
		select {
		case <-release:
			return "ok", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	results, wait := NewFunctionCallResponseProcessor(true, nil).startParallelToolCalls(ctx, inv,
		&model.Response{}, dispatchProbeCalls(2, "probe"), map[string]tool.Tool{"probe": probe}, nil, true)
	done := make(chan error, 1)
	go func() {
		for range results {
		}
		done <- wait()
	}()
	select {
	case first := <-started:
		require.Equal(t, "original", first)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	inv.Session.SetState("baseline", []byte("persisted-result"))
	unblock()
	require.NoError(t, <-done)
	require.Equal(t, "original", <-started)
}

func TestParallelDispatchMergedResultsKeepCallOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = toolcall.WithLimiter(ctx, toolcall.NewLimiter(tool.ConcurrencyConfig{MaxConcurrency: 3}))
	calls := dispatchProbeCalls(64, "echo")
	probe := &dispatchProbeTool{name: "echo", call: func(_ context.Context, args []byte) (any, error) {
		return json.RawMessage(args), nil
	}}
	merged, results, err := NewFunctionCallResponseProcessor(true, nil).executeToolCallsInParallel(ctx,
		dispatchProbeInvocation(), &model.Response{}, calls, map[string]tool.Tool{"echo": probe}, nil)
	require.NoError(t, err)
	require.Len(t, results, len(calls))
	require.Len(t, merged.Choices, len(calls))
	for i, choice := range merged.Choices {
		require.Equal(t, calls[i].ID, choice.Message.ToolID)
		require.JSONEq(t, string(calls[i].Function.Arguments), choice.Message.Content)
	}
}

func TestParallelDispatchUsesRemappedToolGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	limiter := toolcall.NewLimiter(tool.ConcurrencyConfig{Groups: []tool.ConcurrencyGroup{{ToolNames: []string{transfer.TransferToolName}, Limit: 1}}})
	inv := dispatchProbeInvocation()
	inv.Agent = &mockTransferAgent{subAgents: []agent.Agent{&mockTransferSubAgent{info: &mockTransferAgentInfo{name: "child"}}}}
	tools := map[string]tool.Tool{transfer.TransferToolName: &dispatchProbeTool{name: transfer.TransferToolName}}
	calls := dispatchProbeCalls(2, "child")
	first := make(chan func(), 1)
	done := make(chan error, 1)
	go func() {
		done <- NewFunctionCallResponseProcessor(true, nil).dispatchParallelToolCalls(ctx, limiter, inv, calls, tools,
			func(_ int, release func()) { first <- release })
	}()
	release := <-first
	select {
	case extra := <-first:
		extra()
		t.Error("compatibility remap bypassed the transfer group limit")
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	release()
	require.ErrorIs(t, <-done, context.Canceled)
}

type dispatchPanicDeclaration struct{ tool.Tool }

func (*dispatchPanicDeclaration) Declaration() *tool.Declaration {
	panic("broken declaration")
}

func TestParallelDispatchRemappingPanicProducesToolResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = toolcall.WithLimiter(ctx, toolcall.NewLimiter(tool.ConcurrencyConfig{MaxConcurrency: 1}))
	inv := dispatchProbeInvocation()
	inv.Agent = &mockTransferAgent{subAgents: []agent.Agent{&mockTransferSubAgent{info: &mockTransferAgentInfo{name: "child"}}}}
	tools := map[string]tool.Tool{transfer.TransferToolName: &dispatchPanicDeclaration{}}
	merged, results, err := NewFunctionCallResponseProcessor(true, nil).executeToolCallsInParallel(
		ctx, inv, &model.Response{}, dispatchProbeCalls(2, "child"), tools, nil)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Len(t, merged.Choices, 2)
	for i, choice := range merged.Choices {
		require.Equal(t, fmt.Sprintf("call-%d", i), choice.Message.ToolID)
		require.Contains(t, choice.Message.Content, "broken declaration")
	}
}
