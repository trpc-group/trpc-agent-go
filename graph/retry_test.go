//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
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
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

// Test that a node with retry policy succeeds after initial failures and downstream runs once.
func TestNodeRetry_SucceedsAfterFailures(t *testing.T) {
	t.Parallel()

	// Build a simple graph: unstable -> sink
	schema := NewStateSchema()
	var attempts int32
	var sinkRuns int32
	failFirst := int32(2)

	unstable := func(ctx context.Context, state State) (any, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n <= failFirst {
			return nil, fmt.Errorf("simulated failure #%d", n)
		}
		return State{"ok": true}, nil
	}
	sink := func(ctx context.Context, state State) (any, error) {
		atomic.AddInt32(&sinkRuns, 1)
		return nil, nil
	}

	sg := NewStateGraph(schema)
	// Tiny backoff to keep tests fast.
	policy := RetryPolicy{
		MaxAttempts:     3,
		InitialInterval: 1, // 1ns
		BackoffFactor:   1.0,
		MaxInterval:     1,
		Jitter:          false,
		RetryOn:         []RetryCondition{RetryConditionFunc(func(error) bool { return true })},
	}
	sg.AddNode("unstable", unstable, WithRetryPolicy(policy))
	sg.AddNode("sink", sink)
	sg.SetEntryPoint("unstable")
	sg.AddEdge("unstable", "sink")

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: "inv-retry-success"}
	ch, err := exec.Execute(context.Background(), State{}, inv)
	require.NoError(t, err)

	// Drain events until done.
	for e := range ch {
		_ = e
	}

	require.Equal(t, failFirst+1, atomic.LoadInt32(&attempts), "should attempt until first success")
	require.Equal(t, int32(1), atomic.LoadInt32(&sinkRuns), "downstream should run exactly once")
}

// Test that without any retry policy, a failing node is not retried.
func TestNodeRetry_NoPolicy_NoRetry(t *testing.T) {
	t.Parallel()

	schema := NewStateSchema()
	var attempts int32
	var sinkRuns int32

	unstable := func(ctx context.Context, state State) (any, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, fmt.Errorf("always fails")
	}
	sink := func(ctx context.Context, state State) (any, error) {
		atomic.AddInt32(&sinkRuns, 1)
		return nil, nil
	}

	sg := NewStateGraph(schema)
	sg.AddNode("unstable", unstable) // no retry policy
	sg.AddNode("sink", sink)
	sg.SetEntryPoint("unstable")
	sg.AddEdge("unstable", "sink")

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: "inv-no-retry"}
	ch, err := exec.Execute(context.Background(), State{}, inv)
	require.NoError(t, err)

	// Drain events.
	for e := range ch {
		_ = e
	}

	require.Equal(t, int32(1), atomic.LoadInt32(&attempts), "should not retry without policy")
	require.Equal(t, int32(0), atomic.LoadInt32(&sinkRuns), "downstream should not run")
}

// Test that interrupts are not retried even if a retry policy is present.
func TestNodeRetry_Interrupt_NoRetry(t *testing.T) {
	t.Parallel()

	schema := NewStateSchema()
	var attempts int32
	var sinkRuns int32

	node := func(ctx context.Context, state State) (any, error) {
		atomic.AddInt32(&attempts, 1)
		// Trigger an interrupt that should propagate and not be retried.
		_, err := Interrupt(ctx, state, "ask_key", "ask: continue?")
		return nil, err
	}
	sink := func(ctx context.Context, state State) (any, error) {
		atomic.AddInt32(&sinkRuns, 1)
		return nil, nil
	}

	sg := NewStateGraph(schema)
	sg.AddNode("ask", node, WithRetryPolicy(WithSimpleRetry(3)))
	sg.AddNode("sink", sink)
	sg.SetEntryPoint("ask")
	sg.AddEdge("ask", "sink")

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: "inv-interrupt"}
	ch, err := exec.Execute(context.Background(), State{}, inv)
	require.NoError(t, err)

	// Drain events.
	for e := range ch {
		_ = e
	}

	require.Equal(t, int32(1), atomic.LoadInt32(&attempts), "interrupts must not be retried")
	require.Equal(t, int32(0), atomic.LoadInt32(&sinkRuns), "downstream should not run on interrupt")
}

// Test that retry-related metadata is emitted on node error events.
func TestNodeRetry_MetadataOnRetryErrors(t *testing.T) {
	t.Parallel()

	schema := NewStateSchema()
	var attempts int32
	failFirst := int32(2)

	unstable := func(ctx context.Context, state State) (any, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n <= failFirst {
			return nil, fmt.Errorf("simulated failure #%d", n)
		}
		return State{"ok": true}, nil
	}
	sink := func(ctx context.Context, state State) (any, error) { return nil, nil }

	// Fast backoff
	policy := RetryPolicy{
		MaxAttempts:     3,
		InitialInterval: 1,
		BackoffFactor:   1.0,
		MaxInterval:     1,
		Jitter:          false,
		RetryOn:         []RetryCondition{RetryConditionFunc(func(error) bool { return true })},
	}

	sg := NewStateGraph(schema)
	sg.AddNode("unstable", unstable, WithRetryPolicy(policy))
	sg.AddNode("sink", sink)
	sg.SetEntryPoint("unstable")
	sg.AddEdge("unstable", "sink")

	g, err := sg.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: "inv-retry-meta"}
	ch, err := exec.Execute(context.Background(), State{}, inv)
	require.NoError(t, err)

	var starts []NodeExecutionMetadata
	var errs []NodeExecutionMetadata
	for e := range ch {
		if e.StateDelta == nil {
			continue
		}
		if b, ok := e.StateDelta[MetadataKeyNode]; ok {
			var meta NodeExecutionMetadata
			if err := json.Unmarshal(b, &meta); err == nil {
				switch meta.Phase {
				case ExecutionPhaseStart:
					starts = append(starts, meta)
				case ExecutionPhaseError:
					errs = append(errs, meta)
				}
			}
		}
	}

	// We expect at least one start for the node, with attempt metadata present.
	require.NotEmpty(t, starts)
	// Find start for unstable node
	var s NodeExecutionMetadata
	for _, st := range starts {
		if st.NodeID == "unstable" {
			s = st
			break
		}
	}
	require.Equal(t, "unstable", s.NodeID)
	require.Equal(t, 1, s.Attempt)
	require.Equal(t, 3, s.MaxAttempts)

	// Two error events (attempts 1 and 2), both retrying with nextDelay set.
	require.Len(t, errs, int(failFirst))
	require.Equal(t, 1, errs[0].Attempt)
	require.Equal(t, 3, errs[0].MaxAttempts)
	require.True(t, errs[0].Retrying)
	require.Greater(t, int64(errs[0].NextDelay), int64(0))

	require.Equal(t, 2, errs[1].Attempt)
	require.Equal(t, 3, errs[1].MaxAttempts)
	require.True(t, errs[1].Retrying)
	require.Greater(t, int64(errs[1].NextDelay), int64(0))
}

// Test that step deadline clamps the reported nextDelay and prevents long sleeps.
func TestNodeRetry_StepDeadlineClamp(t *testing.T) {
	t.Parallel()

	schema := NewStateSchema()
	var attempts int32

	unstable := func(ctx context.Context, state State) (any, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, fmt.Errorf("fail for clamp test")
	}

	sg := NewStateGraph(schema)
	// Policy with large backoff to force clamp by step timeout.
	policy := RetryPolicy{
		MaxAttempts:     5,
		InitialInterval: 500 * time.Millisecond,
		BackoffFactor:   2.0,
		MaxInterval:     2 * time.Second,
		Jitter:          false,
		RetryOn:         []RetryCondition{RetryConditionFunc(func(error) bool { return true })},
	}
	sg.AddNode("unstable", unstable, WithRetryPolicy(policy))
	sg.SetEntryPoint("unstable")

	g, err := sg.Compile()
	require.NoError(t, err)

	// Small step timeout to ensure clamp.
	exec, err := NewExecutor(g, WithStepTimeout(30*time.Millisecond))
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: "inv-clamp"}
	ch, err := exec.Execute(context.Background(), State{}, inv)
	require.NoError(t, err)

	var nextDelays []time.Duration
	for e := range ch {
		if e.StateDelta == nil {
			continue
		}
		if b, ok := e.StateDelta[MetadataKeyNode]; ok {
			var meta NodeExecutionMetadata
			if err := json.Unmarshal(b, &meta); err == nil {
				if meta.Phase == ExecutionPhaseError && meta.Retrying {
					nextDelays = append(nextDelays, meta.NextDelay)
				}
			}
		}
	}
	// At least one retry planned before step deadline expiry.
	require.NotEmpty(t, nextDelays)
	// All planned delays should be <= step timeout window (~30ms), allow tiny overhead.
	for _, d := range nextDelays {
		require.LessOrEqual(t, d, 50*time.Millisecond)
	}
}

// Test that MaxElapsedTime prevents retries when exceeded.
func TestNodeRetry_MaxElapsedBudget(t *testing.T) {
	t.Parallel()

	schema := NewStateSchema()
	var attempts int32
	unstable := func(ctx context.Context, state State) (any, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, fmt.Errorf("always fail budget")
	}

	sg := NewStateGraph(schema)
	policy := RetryPolicy{
		MaxAttempts:     5,
		InitialInterval: 10 * time.Millisecond,
		BackoffFactor:   2,
		MaxInterval:     100 * time.Millisecond,
		Jitter:          false,
		RetryOn:         []RetryCondition{RetryConditionFunc(func(error) bool { return true })},
		MaxElapsedTime:  1 * time.Nanosecond, // effectively no retry
	}
	sg.AddNode("unstable", unstable, WithRetryPolicy(policy))
	sg.SetEntryPoint("unstable")

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: "inv-budget"}
	ch, err := exec.Execute(context.Background(), State{}, inv)
	require.NoError(t, err)
	for range ch { /* drain */
	}

	require.Equal(t, int32(1), atomic.LoadInt32(&attempts), "no retry when budget exhausted")
}

// Test executor-level default retry policy applies when node has none.
func TestExecutor_DefaultRetryPolicy(t *testing.T) {
	t.Parallel()

	schema := NewStateSchema()
	var attempts int32
	var sinkRuns int32
	unstable := func(ctx context.Context, state State) (any, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			return nil, fmt.Errorf("first fail")
		}
		return State{"ok": true}, nil
	}
	sink := func(ctx context.Context, state State) (any, error) { atomic.AddInt32(&sinkRuns, 1); return nil, nil }

	sg := NewStateGraph(schema)
	sg.AddNode("unstable", unstable) // no node-level policy
	sg.AddNode("sink", sink)
	sg.SetEntryPoint("unstable")
	sg.AddEdge("unstable", "sink")

	g, err := sg.Compile()
	require.NoError(t, err)

	// Executor default retry to kick in for any error (retry-on predicate true).
	execDefault := RetryPolicy{
		MaxAttempts:     2,
		InitialInterval: 1,
		BackoffFactor:   1,
		MaxInterval:     1,
		RetryOn:         []RetryCondition{RetryConditionFunc(func(error) bool { return true })},
	}
	exec, err := NewExecutor(g, WithDefaultRetryPolicy(execDefault))
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: "inv-default"}
	ch, err := exec.Execute(context.Background(), State{}, inv)
	require.NoError(t, err)
	for range ch { /* drain */
	}

	require.Equal(t, int32(2), atomic.LoadInt32(&attempts))
	require.Equal(t, int32(1), atomic.LoadInt32(&sinkRuns))
}

// Additional retry helpers coverage
type fakeNetErr struct{ timeout, temp bool }

func (e fakeNetErr) Error() string   { return "fake" }
func (e fakeNetErr) Timeout() bool   { return e.timeout }
func (e fakeNetErr) Temporary() bool { return e.temp }

func TestRetry_DefaultTransientCondition_Matches(t *testing.T) {
	cond := DefaultTransientCondition()
	if !cond.Match(context.DeadlineExceeded) {
		t.Fatalf("expected match on deadline exceeded")
	}
	if !cond.Match(fakeNetErr{timeout: true}) {
		t.Fatalf("expected match on net.Error Timeout")
	}
	if !cond.Match(fakeNetErr{temp: true}) {
		t.Fatalf("expected match on net.Error Temporary")
	}
	if cond.Match(fmt.Errorf("perm")) {
		t.Fatalf("did not expect match on arbitrary error")
	}
}

func TestRetry_RetryOnHelpers_AndShouldRetry(t *testing.T) {
	e1 := fmt.Errorf("e1")
	e2 := fmt.Errorf("e2")
	p := RetryPolicy{RetryOn: []RetryCondition{RetryOnErrors(e1), RetryOnPredicate(func(err error) bool { return err == e2 })}}
	if !p.ShouldRetry(e1) || !p.ShouldRetry(e2) || p.ShouldRetry(fmt.Errorf("x")) {
		t.Fatalf("ShouldRetry mismatch")
	}
}

func TestRetry_NextDelay_JitterAndClamps(t *testing.T) {
	p := RetryPolicy{MaxAttempts: 3, InitialInterval: 10 * time.Millisecond, BackoffFactor: 2, MaxInterval: 15 * time.Millisecond, Jitter: true}
	d1 := p.NextDelay(1)
	if d1 < 10*time.Millisecond || d1 >= 20*time.Millisecond {
		t.Fatalf("unexpected d1=%v", d1)
	}
	d3 := p.NextDelay(3)
	if d3 < 15*time.Millisecond || d3 >= 30*time.Millisecond {
		t.Fatalf("unexpected d3=%v", d3)
	}
}

func TestRetry_WithSimpleRetry_SetsDefaults(t *testing.T) {
	p := WithSimpleRetry(0)
	if p.MaxAttempts < 1 || p.InitialInterval <= 0 || p.BackoffFactor <= 0 || p.MaxInterval <= 0 || len(p.RetryOn) == 0 {
		t.Fatalf("unexpected simple retry defaults: %+v", p)
	}
}

func TestRetry_NextDelay_EdgeBranches(t *testing.T) {
	p := RetryPolicy{MaxAttempts: 3, InitialInterval: 5 * time.Millisecond, BackoffFactor: 0, MaxInterval: 0, Jitter: false}
	d := p.NextDelay(0)
	if d != 5*time.Millisecond {
		t.Fatalf("expected 5ms, got %v", d)
	}
}

func TestRetry_ShouldRetry_Negative(t *testing.T) {
	p := RetryPolicy{}
	if p.ShouldRetry(fmt.Errorf("e")) {
		t.Fatalf("expected false without conditions")
	}
}
