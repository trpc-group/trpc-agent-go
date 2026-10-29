//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
)

// traceMarshalCount counts JSON encodings of a node result. Trace snapshots
// encode node inputs and outputs, so the count grows with tracing enabled and
// stays flat when tracing is disabled.
var traceMarshalCount int32

// traceMarshalProbe is a node result that records each JSON encoding.
type traceMarshalProbe struct{}

func (traceMarshalProbe) MarshalJSON() ([]byte, error) {
	atomic.AddInt32(&traceMarshalCount, 1)
	return []byte(`{"counted":true}`), nil
}

func TestExecutorExecutionTraceDisabledSkipsSnapshots(t *testing.T) {
	// runGraph returns how many times the node result was JSON-encoded during
	// one run. The completion event serializes the final state once whether or
	// not tracing is enabled, so only the difference between the two runs is
	// attributable to trace snapshots.
	runGraph := func(t *testing.T, traceEnabled bool) int32 {
		t.Helper()

		atomic.StoreInt32(&traceMarshalCount, 0)
		builder := NewStateGraph(NewStateSchema())
		builder.AddNode("work", func(context.Context, State) (any, error) {
			return State{"result": traceMarshalProbe{}}, nil
		})
		compiled, err := builder.
			SetEntryPoint("work").
			SetFinishPoint("work").
			Compile()
		require.NoError(t, err)

		exec, err := NewExecutor(compiled)
		require.NoError(t, err)
		invocation := agent.NewInvocation(
			agent.WithInvocationID("inv-trace-snapshot"),
			agent.WithInvocationRunOptions(agent.NewRunOptions(
				agent.WithExecutionTraceEnabled(traceEnabled),
			)),
		)
		events, err := exec.Execute(context.Background(), State{}, invocation)
		require.NoError(t, err)
		for range events {
		}

		trace := agent.BuildExecutionTrace(invocation, atrace.TraceStatusCompleted)
		if traceEnabled {
			require.NotNil(t, trace, "enabled tracing should build a trace")
		} else {
			require.Nil(t, trace, "disabled tracing should not build a trace")
		}
		return atomic.LoadInt32(&traceMarshalCount)
	}

	withoutTrace := runGraph(t, false)
	withTrace := runGraph(t, true)

	// The completion event serializes the final state exactly once. Any extra
	// encoding in a disabled run would mean a trace snapshot was still built.
	require.EqualValues(t, 1, withoutTrace,
		"a run without tracing must only encode the node result for the completion event")
	require.Greater(t, withTrace, withoutTrace,
		"tracing should add snapshot encodings; without tracing=%d, with tracing=%d",
		withoutTrace, withTrace)
}

// TestExecutorCacheHitAfterCallbackErrorSkipsSnapshot drives the cache-hit path
// through a failing after-node callback. That path ends a trace step with the
// callback error, and the snapshot it hands to the trace must be skipped when
// tracing is disabled.
func TestExecutorCacheHitAfterCallbackErrorSkipsSnapshot(t *testing.T) {
	sg := NewStateGraph(NewStateSchema()).
		WithCache(NewInMemoryCache()).
		WithCachePolicy(DefaultCachePolicy())
	sg.AddNode("work", func(ctx context.Context, state State) (any, error) {
		return State{"result": traceMarshalProbe{}}, nil
	}).SetEntryPoint("work").SetFinishPoint("work")
	var callbacks int32
	sg.WithNodeCallbacks(NewNodeCallbacks().RegisterAfterNode(func(
		ctx context.Context,
		cb *NodeCallbackContext,
		state State,
		result any,
		nodeErr error,
	) (any, error) {
		if atomic.AddInt32(&callbacks, 1) == 2 {
			return nil, fmt.Errorf("after boom")
		}
		return nil, nil
	}))
	compiled, err := sg.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(compiled)
	require.NoError(t, err)

	run := func(id string) {
		invocation := agent.NewInvocation(
			agent.WithInvocationID(id),
			agent.WithInvocationRunOptions(agent.NewRunOptions(agent.WithExecutionTraceEnabled(false))),
		)
		events, err := exec.Execute(context.Background(), State{}, invocation)
		require.NoError(t, err)
		for range events {
		}
	}

	// The first run executes the node and fills the cache; the second hits the
	// cache and fails in the after-node callback.
	run("inv-cache-after-callback-miss")
	run("inv-cache-after-callback-hit")
	require.EqualValues(t, 2, atomic.LoadInt32(&callbacks))
}

var _ json.Marshaler = traceMarshalProbe{}
