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
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/barrier"
)

// fixedDeadlineContext returns a fixed deadline while never reporting cancellation.
type fixedDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c fixedDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

func (fixedDeadlineContext) Done() <-chan struct{} { return nil }

func (fixedDeadlineContext) Err() error { return nil }

// errAfterFirstCallContext returns nil on the first Err call and a fixed error afterwards.
type errAfterFirstCallContext struct {
	context.Context
	deadline time.Time
	err      error
	errCalls int32
}

func (c *errAfterFirstCallContext) Deadline() (time.Time, bool) { return c.deadline, true }

func (*errAfterFirstCallContext) Done() <-chan struct{} { return nil }

func (c *errAfterFirstCallContext) Err() error {
	if atomic.AddInt32(&c.errCalls, 1) == 1 {
		return nil
	}
	return c.err
}

func TestEmitNodeBarrierAndWait_NilInvocationOrExecCtx(t *testing.T) {
	t.Parallel()

	exec := &Executor{}
	err := exec.emitNodeBarrierAndWait(context.Background(), nil, &ExecutionContext{}, "N", 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invocation is nil")

	inv := agent.NewInvocation(agent.WithInvocationID("inv-nil-exec-ctx"))
	barrier.Enable(inv)
	err = exec.emitNodeBarrierAndWait(context.Background(), inv, nil, "N", 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "execution context is nil")
}

func TestEmitNodeBarrierAndWait_NilEventChanError(t *testing.T) {
	t.Parallel()

	exec := &Executor{}
	inv := agent.NewInvocation(agent.WithInvocationID("inv-nil-event-chan"))
	barrier.Enable(inv)
	execCtx := &ExecutionContext{EventChan: nil, InvocationID: inv.InvocationID}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := exec.emitNodeBarrierAndWait(ctx, inv, execCtx, "N", 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "event channel is nil")
}

func TestEmitNodeBarrierAndWait_AddNoticeChannelFailure(t *testing.T) {
	t.Parallel()

	exec := &Executor{}
	inv := &agent.Invocation{InvocationID: "inv-notice-mu-nil"}
	barrier.Enable(inv)
	execCtx := &ExecutionContext{EventChan: make(chan *event.Event, 1), InvocationID: inv.InvocationID}

	err := exec.emitNodeBarrierAndWait(context.Background(), inv, execCtx, "N", 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "add notice channel")
}

func TestEmitNodeBarrierAndWait_EmitEventFailure(t *testing.T) {
	t.Parallel()

	exec := &Executor{}
	inv := agent.NewInvocation(agent.WithInvocationID("inv-emit-failure"))
	barrier.Enable(inv)
	execCtx := &ExecutionContext{EventChan: make(chan *event.Event, 1), InvocationID: inv.InvocationID}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := exec.emitNodeBarrierAndWait(ctx, inv, execCtx, "N", 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "emit node barrier event")
}

func TestEmitNodeBarrierAndWait_DeadlineWaitsForCompletion(t *testing.T) {
	t.Parallel()

	exec := &Executor{}
	inv := agent.NewInvocation(agent.WithInvocationID("inv-deadline-barrier"))
	barrier.Enable(inv)
	eventCh := make(chan *event.Event, 1)
	execCtx := &ExecutionContext{EventChan: eventCh, InvocationID: inv.InvocationID}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- exec.emitNodeBarrierAndWait(ctx, inv, execCtx, "N", 1)
	}()

	var barrierEvt *event.Event
	select {
	case barrierEvt = <-eventCh:
	case <-ctx.Done():
		require.NoError(t, ctx.Err(), "did not receive node barrier event")
	}
	require.NotNil(t, barrierEvt)
	require.Equal(t, ObjectTypeGraphNodeBarrier, barrierEvt.Object)
	require.True(t, barrierEvt.RequiresCompletion)

	completionID := agent.GetAppendEventNoticeKey(barrierEvt.ID)
	require.NoError(t, inv.NotifyCompletion(context.Background(), completionID))

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		require.NoError(t, ctx.Err(), "timed out waiting for barrier completion")
	}
}

func TestEmitNodeBarrierAndWait_DeadlineAlreadyExceeded_StillWaits(t *testing.T) {
	t.Parallel()

	exec := &Executor{}
	inv := agent.NewInvocation(agent.WithInvocationID("inv-deadline-exceeded-still-waits"))
	barrier.Enable(inv)
	eventCh := make(chan *event.Event, 1)
	execCtx := &ExecutionContext{EventChan: eventCh, InvocationID: inv.InvocationID}
	ctx := fixedDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Second)}

	done := make(chan error, 1)
	go func() {
		done <- exec.emitNodeBarrierAndWait(ctx, inv, execCtx, "N", 1)
	}()

	var barrierEvt *event.Event
	select {
	case barrierEvt = <-eventCh:
	case <-time.After(200 * time.Millisecond):
		require.FailNow(t, "did not receive node barrier event")
	}
	require.NotNil(t, barrierEvt)
	require.Equal(t, ObjectTypeGraphNodeBarrier, barrierEvt.Object)
	require.True(t, barrierEvt.RequiresCompletion)

	completionID := agent.GetAppendEventNoticeKey(barrierEvt.ID)
	require.NoError(t, inv.NotifyCompletion(context.Background(), completionID))

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(200 * time.Millisecond):
		require.FailNow(t, "timed out waiting for barrier completion")
	}
}

func TestEmitNodeBarrierAndWait_DeadlineAlreadyExceeded_ContextErrorReturns(t *testing.T) {
	t.Parallel()

	exec := &Executor{}
	inv := agent.NewInvocation(agent.WithInvocationID("inv-deadline-exceeded-context-error"))
	barrier.Enable(inv)
	eventCh := make(chan *event.Event, 1)
	execCtx := &ExecutionContext{EventChan: eventCh, InvocationID: inv.InvocationID}
	ctx := &errAfterFirstCallContext{
		Context:  context.Background(),
		deadline: time.Now().Add(-time.Second),
		err:      context.DeadlineExceeded,
	}

	err := exec.emitNodeBarrierAndWait(ctx, inv, execCtx, "N", 1)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	select {
	case barrierEvt := <-eventCh:
		require.NotNil(t, barrierEvt)
		require.Equal(t, ObjectTypeGraphNodeBarrier, barrierEvt.Object)
		completionID := agent.GetAppendEventNoticeKey(barrierEvt.ID)
		require.NoError(t, inv.NotifyCompletion(context.Background(), completionID))
	case <-time.After(200 * time.Millisecond):
		require.FailNow(t, "did not receive node barrier event")
	}
}

func TestEmitNodeBarrierAndWait_WaitTimeout(t *testing.T) {
	t.Parallel()

	exec := &Executor{}
	inv := agent.NewInvocation(agent.WithInvocationID("inv-wait-timeout"))
	barrier.Enable(inv)
	execCtx := &ExecutionContext{EventChan: make(chan *event.Event, 1), InvocationID: inv.InvocationID}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	err := exec.emitNodeBarrierAndWait(ctx, inv, execCtx, "N", 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "wait for node barrier completion")
}

func TestRunBeforeCallbacks_BarrierWaitErrorPropagates(t *testing.T) {
	t.Parallel()

	exec := &Executor{}
	inv := agent.NewInvocation(agent.WithInvocationID("inv-before-callback-timeout"))
	barrier.Enable(inv)
	execCtx := &ExecutionContext{
		EventChan:      make(chan *event.Event, 8),
		InvocationID:   inv.InvocationID,
		versionsSeen:   make(map[string]map[string]int64),
		Graph:          nil,
		State:          make(State),
		lastCheckpoint: nil,
	}
	callbacks := NewNodeCallbacks().RegisterBeforeNode(func(ctx context.Context, cbCtx *NodeCallbackContext, state State) (any, error) {
		return nil, fmt.Errorf("before callback failed")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	handled, err := exec.runBeforeCallbacks(ctx, inv, callbacks, nil, State{}, execCtx, &Task{NodeID: "N"}, NodeTypeFunction, time.Now(), 1)
	require.True(t, handled)
	require.Error(t, err)
	require.Contains(t, err.Error(), "node barrier")
}

func TestRunAfterCallbacks_BarrierWaitErrorPropagates(t *testing.T) {
	t.Parallel()

	exec := &Executor{}
	inv := agent.NewInvocation(agent.WithInvocationID("inv-after-callback-timeout"))
	barrier.Enable(inv)
	execCtx := &ExecutionContext{
		EventChan:      make(chan *event.Event, 8),
		InvocationID:   inv.InvocationID,
		versionsSeen:   make(map[string]map[string]int64),
		Graph:          nil,
		State:          make(State),
		lastCheckpoint: nil,
	}
	callbacks := NewNodeCallbacks().RegisterAfterNode(func(ctx context.Context, cbCtx *NodeCallbackContext, state State, result any, nodeErr error) (any, error) {
		return nil, fmt.Errorf("after callback failed")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	result, err := exec.runAfterCallbacks(ctx, inv, callbacks, nil, State{}, nil, execCtx, "N", NodeTypeFunction, 1)
	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "node barrier")
}

func TestExecuteGraph_BarrierWaitTimeoutOnSuccess(t *testing.T) {
	t.Parallel()

	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("N", func(ctx context.Context, state State) (any, error) {
		return State{"ok": true}, nil
	})
	sg.SetEntryPoint("N")
	sg.SetFinishPoint("N")

	g, err := sg.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	inv := agent.NewInvocation(agent.WithInvocationID("inv-success-barrier-timeout"))
	barrier.Enable(inv)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	err = exec.executeGraph(ctx, State{}, inv, make(chan *event.Event, 128), time.Now())
	require.Error(t, err)
	require.Contains(t, err.Error(), "node barrier")
}

func TestExecuteGraph_BeforeCallbackCustomResult_BarrierWaitTimeout(t *testing.T) {
	t.Parallel()

	var nodeRuns int32
	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("N", func(ctx context.Context, state State) (any, error) {
		atomic.AddInt32(&nodeRuns, 1)
		return State{"ok": true}, nil
	})
	sg.SetEntryPoint("N")
	sg.SetFinishPoint("N")

	sg.WithNodeCallbacks(NewNodeCallbacks().RegisterBeforeNode(func(ctx context.Context, cbCtx *NodeCallbackContext, state State) (any, error) {
		return State{"from_callback": true}, nil
	}))

	g, err := sg.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	inv := agent.NewInvocation(agent.WithInvocationID("inv-before-custom-result-timeout"))
	barrier.Enable(inv)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	err = exec.executeGraph(ctx, State{}, inv, make(chan *event.Event, 128), time.Now())
	require.Error(t, err)
	require.Contains(t, err.Error(), "node barrier")
	require.Equal(t, int32(0), atomic.LoadInt32(&nodeRuns))
}

func TestExecuteGraph_CacheHit_BarrierWaitTimeout(t *testing.T) {
	t.Parallel()

	var runs int32
	worker := func(ctx context.Context, state State) (any, error) {
		atomic.AddInt32(&runs, 1)
		return State{"ok": true}, nil
	}

	sg := NewStateGraph(NewStateSchema()).
		WithCache(NewInMemoryCache()).
		WithCachePolicy(DefaultCachePolicy())
	sg.AddNode("work", worker).
		SetEntryPoint("work").
		SetFinishPoint("work")

	g, err := sg.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	err = exec.executeGraph(context.Background(), State{}, agent.NewInvocation(agent.WithInvocationID("inv-cache-run-1")), nil, time.Now())
	require.NoError(t, err)
	require.Equal(t, int32(1), atomic.LoadInt32(&runs))

	inv2 := agent.NewInvocation(agent.WithInvocationID("inv-cache-run-2"))
	barrier.Enable(inv2)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	err = exec.executeGraph(ctx, State{}, inv2, make(chan *event.Event, 128), time.Now())
	require.Error(t, err)
	require.Contains(t, err.Error(), "node barrier")
	require.Equal(t, int32(1), atomic.LoadInt32(&runs))
}
