//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

const (
	dagNodeEntry      = "preprocess"
	dagNodeSlow       = "slow"
	dagNodeFast       = "fast"
	dagNodeDownstream = "downstream"

	dagWaitShort = 100 * time.Millisecond
	dagWaitLong  = 2 * time.Second
)

func TestExecutor_DagEngine_SchedulesEagerly(t *testing.T) {
	slowRelease := make(chan struct{})
	slowStarted := make(chan struct{}, 1)
	fastDone := make(chan struct{}, 1)
	downStarted := make(chan struct{}, 1)

	g := compileDagSchedulingGraph(
		t,
		slowRelease,
		slowStarted,
		fastDone,
		downStarted,
	)

	exec, err := NewExecutor(
		g,
		WithExecutionEngine(ExecutionEngineDAG),
		WithMaxConcurrency(2),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), dagWaitLong)
	defer cancel()

	evts, err := exec.Execute(ctx, State{}, &agent.Invocation{
		InvocationID: "dag-eager",
	})
	require.NoError(t, err)

	errCh := startEventDrain(evts)

	waitForSignal(t, slowStarted, "slow started", dagWaitLong)
	waitForSignal(t, fastDone, "fast done", dagWaitLong)

	waitForSignal(t, downStarted, "downstream started", dagWaitLong)
	close(slowRelease)

	require.NoError(t, <-errCh)
}

func TestExecutor_BspEngine_WaitsForSuperstep(t *testing.T) {
	slowRelease := make(chan struct{})
	slowStarted := make(chan struct{}, 1)
	fastDone := make(chan struct{}, 1)
	downStarted := make(chan struct{}, 1)

	g := compileDagSchedulingGraph(
		t,
		slowRelease,
		slowStarted,
		fastDone,
		downStarted,
	)

	exec, err := NewExecutor(g, WithMaxConcurrency(2))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), dagWaitLong)
	defer cancel()

	evts, err := exec.Execute(ctx, State{}, &agent.Invocation{
		InvocationID: "bsp-barrier",
	})
	require.NoError(t, err)

	errCh := startEventDrain(evts)

	waitForSignal(t, slowStarted, "slow started", dagWaitLong)
	waitForSignal(t, fastDone, "fast done", dagWaitLong)

	select {
	case <-downStarted:
		t.Fatalf("downstream started before slow finished")
	case <-time.After(dagWaitShort):
	}

	close(slowRelease)
	waitForSignal(t, downStarted, "downstream started", dagWaitLong)

	require.NoError(t, <-errCh)
}

func TestNewExecutor_DagEngine_RejectsCheckpointSaver(t *testing.T) {
	g := compileSimpleDagGraph(t)
	_, err := NewExecutor(
		g,
		WithExecutionEngine(ExecutionEngineDAG),
		WithCheckpointSaver(&mockCheckpointSaver{}),
	)
	require.ErrorIs(t, err, ErrDagEngineCheckpointUnsupported)
}

func TestNewExecutor_DagEngine_RejectsStaticInterrupts(t *testing.T) {
	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(dagNodeEntry, func(ctx context.Context, state State) (any, error) {
		return nil, nil
	})
	sg.AddNode(
		dagNodeSlow,
		func(ctx context.Context, state State) (any, error) {
			return nil, nil
		},
		WithInterruptBefore(),
	)
	sg.SetEntryPoint(dagNodeEntry)
	sg.AddEdge(dagNodeEntry, dagNodeSlow)

	g, err := sg.Compile()
	require.NoError(t, err)

	_, err = NewExecutor(g, WithExecutionEngine(ExecutionEngineDAG))
	require.ErrorIs(t, err, ErrDagEngineInterruptUnsupported)
}

func compileSimpleDagGraph(t *testing.T) *Graph {
	t.Helper()

	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(dagNodeEntry, func(ctx context.Context, state State) (any, error) {
		return nil, nil
	})
	sg.SetEntryPoint(dagNodeEntry)

	g, err := sg.Compile()
	require.NoError(t, err)
	return g
}

func compileDagSchedulingGraph(
	t *testing.T,
	slowRelease <-chan struct{},
	slowStarted chan<- struct{},
	fastDone chan<- struct{},
	downStarted chan<- struct{},
) *Graph {
	t.Helper()

	notify := func(ch chan<- struct{}) {
		select {
		case ch <- struct{}{}:
		default:
		}
	}

	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(dagNodeEntry, func(ctx context.Context, state State) (any, error) {
		return nil, nil
	})
	sg.AddNode(dagNodeSlow, func(ctx context.Context, state State) (any, error) {
		notify(slowStarted)
		select {
		case <-slowRelease:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	sg.AddNode(dagNodeFast, func(ctx context.Context, state State) (any, error) {
		notify(fastDone)
		return nil, nil
	})
	sg.AddNode(
		dagNodeDownstream,
		func(ctx context.Context, state State) (any, error) {
			notify(downStarted)
			return nil, nil
		},
	)
	sg.SetEntryPoint(dagNodeEntry)
	sg.AddEdge(dagNodeEntry, dagNodeSlow)
	sg.AddEdge(dagNodeEntry, dagNodeFast)
	sg.AddEdge(dagNodeFast, dagNodeDownstream)

	g, err := sg.Compile()
	require.NoError(t, err)
	return g
}

func waitForSignal(
	t *testing.T,
	ch <-chan struct{},
	name string,
	timeout time.Duration,
) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
		return
	case <-timer.C:
		t.Fatalf("timeout waiting for %s", name)
	}
}

func startEventDrain(evts <-chan *event.Event) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)

		var firstErr error
		for e := range evts {
			if e == nil || e.Error == nil {
				continue
			}
			if firstErr != nil {
				continue
			}
			firstErr = errors.New(e.Error.Message)
		}
		errCh <- firstErr
	}()
	return errCh
}
