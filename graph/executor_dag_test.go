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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	ichannel "trpc.group/trpc-go/trpc-agent-go/graph/internal/channel"
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

func TestNewDagLoop_ValidatesInputs(t *testing.T) {
	ctx := context.Background()
	inv := &agent.Invocation{InvocationID: "dag-loop"}

	_, err := newDagLoop(nil, ctx, inv, nil)
	require.ErrorContains(t, err, "execution context is nil")

	execCtx := &ExecutionContext{}
	_, err = newDagLoop(nil, ctx, inv, execCtx)
	require.ErrorContains(t, err, "execution channels are nil")

	execCtx.channels = ichannel.NewChannelManager()
	_, err = newDagLoop(&Executor{}, ctx, inv, execCtx)
	require.ErrorContains(t, err, "graph is nil")

	g := New(NewStateSchema())
	_, err = newDagLoop(
		&Executor{graph: g, maxConcurrency: 1},
		ctx,
		inv,
		execCtx,
	)
	require.ErrorContains(t, err, "no entry point defined")
}

func TestExecutor_DagEngine_DrainsOnTaskError(t *testing.T) {
	const errMessage = "dag node error"

	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(dagNodeEntry, func(ctx context.Context, state State) (any, error) {
		return nil, errors.New(errMessage)
	})
	sg.SetEntryPoint(dagNodeEntry)

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g, WithExecutionEngine(ExecutionEngineDAG))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), dagWaitLong)
	defer cancel()

	evts, err := exec.Execute(ctx, State{}, &agent.Invocation{
		InvocationID: "dag-error",
	})
	require.NoError(t, err)

	errCh := startEventDrain(evts)
	got := <-errCh
	require.Error(t, got)
	require.ErrorContains(t, got, errMessage)
}

func TestDagLoop_waitForEvent_StartsDrainingOnContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	loop := &dagLoop{
		ctx:  ctx,
		done: make(chan dagTaskResult, 1),
	}
	loop.waitForEvent()

	require.True(t, loop.draining)
	require.ErrorIs(t, loop.drainErr, context.Canceled)
}

func TestExecutor_DagEngine_RespectsMaxConcurrency(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})

	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(dagNodeEntry, func(ctx context.Context, state State) (any, error) {
		return nil, nil
	})

	blockNode := func(name string) NodeFunc {
		return func(ctx context.Context, state State) (any, error) {
			select {
			case started <- name:
			default:
			}
			select {
			case <-release:
				return nil, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	const nodeA = "a"
	const nodeB = "b"
	sg.AddNode(nodeA, blockNode(nodeA))
	sg.AddNode(nodeB, blockNode(nodeB))
	sg.SetEntryPoint(dagNodeEntry)
	sg.AddEdge(dagNodeEntry, nodeA)
	sg.AddEdge(dagNodeEntry, nodeB)

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(
		g,
		WithExecutionEngine(ExecutionEngineDAG),
		WithMaxConcurrency(1),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), dagWaitLong)
	defer cancel()

	evts, err := exec.Execute(ctx, State{}, &agent.Invocation{
		InvocationID: "dag-max-concurrency",
	})
	require.NoError(t, err)

	errCh := startEventDrain(evts)

	first := <-started
	require.NotEmpty(t, first)

	select {
	case second := <-started:
		t.Fatalf("unexpected parallel start: %q", second)
	case <-time.After(dagWaitShort):
	}

	close(release)

	select {
	case <-started:
	case <-time.After(dagWaitLong):
		t.Fatalf("timeout waiting for second node start")
	}

	require.NoError(t, <-errCh)
}

func TestExecutor_DagEngine_SerializesSameNode(t *testing.T) {
	release := make(chan struct{})
	started := make(chan int32, 2)
	doneFast := make(chan struct{}, 1)

	var slowCalls atomic.Int32

	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(dagNodeEntry, func(ctx context.Context, state State) (any, error) {
		return nil, nil
	})
	sg.AddNode(dagNodeSlow, func(ctx context.Context, state State) (any, error) {
		call := slowCalls.Add(1)
		started <- call
		if call == 1 {
			select {
			case <-release:
				return nil, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return nil, nil
	})
	sg.AddNode(dagNodeFast, func(ctx context.Context, state State) (any, error) {
		select {
		case doneFast <- struct{}{}:
		default:
		}
		return nil, nil
	})
	sg.SetEntryPoint(dagNodeEntry)
	sg.AddEdge(dagNodeEntry, dagNodeSlow)
	sg.AddEdge(dagNodeEntry, dagNodeFast)
	sg.AddEdge(dagNodeFast, dagNodeSlow)

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(
		g,
		WithExecutionEngine(ExecutionEngineDAG),
		WithMaxConcurrency(2),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), dagWaitLong)
	defer cancel()

	evts, err := exec.Execute(ctx, State{}, &agent.Invocation{
		InvocationID: "dag-serialize",
	})
	require.NoError(t, err)

	errCh := startEventDrain(evts)

	require.Equal(t, int32(1), <-started)
	waitForSignal(t, doneFast, "fast done", dagWaitLong)

	select {
	case call := <-started:
		t.Fatalf("unexpected second slow start: %d", call)
	case <-time.After(dagWaitShort):
	}

	close(release)

	require.Equal(t, int32(2), <-started)
	require.NoError(t, <-errCh)
}

func TestExecutor_DagEngine_StopsAtMaxSteps(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})

	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(dagNodeEntry, func(ctx context.Context, state State) (any, error) {
		return nil, nil
	})

	blockNode := func(name string) NodeFunc {
		return func(ctx context.Context, state State) (any, error) {
			select {
			case started <- name:
			default:
			}
			select {
			case <-release:
				return nil, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	const nodeA = "max_a"
	const nodeB = "max_b"
	sg.AddNode(nodeA, blockNode(nodeA))
	sg.AddNode(nodeB, blockNode(nodeB))
	sg.SetEntryPoint(dagNodeEntry)
	sg.AddEdge(dagNodeEntry, nodeA)
	sg.AddEdge(dagNodeEntry, nodeB)

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(
		g,
		WithExecutionEngine(ExecutionEngineDAG),
		WithMaxConcurrency(2),
		WithMaxSteps(2),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), dagWaitLong)
	defer cancel()

	evts, err := exec.Execute(ctx, State{}, &agent.Invocation{
		InvocationID: "dag-max-steps",
	})
	require.NoError(t, err)

	errCh := startEventDrain(evts)

	first := <-started
	require.NotEmpty(t, first)

	select {
	case second := <-started:
		t.Fatalf("unexpected second start: %q", second)
	case <-time.After(dagWaitShort):
	}

	close(release)

	require.NoError(t, <-errCh)

	select {
	case second := <-started:
		t.Fatalf("unexpected late start: %q", second)
	default:
	}
}

func TestExecutor_createDagTask_ReturnsNilForInvalidNode(t *testing.T) {
	g := compileSimpleDagGraph(t)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	require.Nil(t, exec.createDagTask(""))
	require.Nil(t, exec.createDagTask(End))
	require.Nil(t, exec.createDagTask("missing"))
	require.NotNil(t, exec.createDagTask(dagNodeEntry))
}

func TestExecutor_planDagTasks_ReturnsNilWhenContextMissing(t *testing.T) {
	exec := &Executor{}
	require.Nil(t, exec.planDagTasks(nil))
	require.Nil(t, exec.planDagTasks(&ExecutionContext{}))
}

func TestExecutor_planDagTasks_ReturnsAndClearsPendingTasks(t *testing.T) {
	g := compileSimpleDagGraph(t)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	execCtx := &ExecutionContext{channels: exec.buildChannelManager()}
	pending := &Task{NodeID: dagNodeEntry}

	execCtx.pendingTasks = []*Task{pending}
	tasks := exec.planDagTasks(execCtx)
	require.Len(t, tasks, 1)
	require.Same(t, pending, tasks[0])
	require.Empty(t, execCtx.pendingTasks)
}

func TestExecutor_planDagTasks_ConsumesTriggers(t *testing.T) {
	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(dagNodeEntry, func(ctx context.Context, state State) (any, error) {
		return nil, nil
	})
	sg.AddNode(dagNodeSlow, func(ctx context.Context, state State) (any, error) {
		return nil, nil
	})
	sg.SetEntryPoint(dagNodeEntry)
	sg.AddEdge(dagNodeEntry, dagNodeSlow)

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(
		g,
		WithExecutionEngine(ExecutionEngineDAG),
	)
	require.NoError(t, err)

	execCtx := &ExecutionContext{channels: exec.buildChannelManager()}
	channelName := ChannelBranchPrefix + dagNodeSlow
	ch, ok := execCtx.channels.GetChannel(channelName)
	require.True(t, ok)
	require.NotNil(t, ch)

	require.True(t, ch.Update([]any{"update"}, 0))

	tasks := exec.planDagTasks(execCtx)
	require.Len(t, tasks, 1)
	require.Equal(t, dagNodeSlow, tasks[0].NodeID)

	tasks = exec.planDagTasks(execCtx)
	require.Empty(t, tasks)
}

func TestExecutor_planDagTasks_SkipsMissingChannels(t *testing.T) {
	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(dagNodeEntry, func(ctx context.Context, state State) (any, error) {
		return nil, nil
	})
	sg.AddNode(dagNodeSlow, func(ctx context.Context, state State) (any, error) {
		return nil, nil
	})
	sg.SetEntryPoint(dagNodeEntry)
	sg.AddEdge(dagNodeEntry, dagNodeSlow)

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(
		g,
		WithExecutionEngine(ExecutionEngineDAG),
	)
	require.NoError(t, err)

	execCtx := &ExecutionContext{channels: ichannel.NewChannelManager()}

	chName := ChannelBranchPrefix + dagNodeSlow
	g.mu.Lock()
	g.triggerToNodes[chName] = []string{dagNodeSlow}
	g.mu.Unlock()

	require.Empty(t, exec.planDagTasks(execCtx))
}

func TestExecutor_planDagTasks_SkipsNilTasks(t *testing.T) {
	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(dagNodeEntry, func(ctx context.Context, state State) (any, error) {
		return nil, nil
	})
	sg.AddNode(dagNodeSlow, func(ctx context.Context, state State) (any, error) {
		return nil, nil
	})
	sg.SetEntryPoint(dagNodeEntry)
	sg.AddEdge(dagNodeEntry, dagNodeSlow)

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(
		g,
		WithExecutionEngine(ExecutionEngineDAG),
	)
	require.NoError(t, err)

	execCtx := &ExecutionContext{channels: exec.buildChannelManager()}
	chName := ChannelBranchPrefix + dagNodeSlow

	g.mu.Lock()
	g.triggerToNodes[chName] = []string{End}
	g.mu.Unlock()

	ch, ok := execCtx.channels.GetChannel(chName)
	require.True(t, ok)
	require.NotNil(t, ch)
	require.True(t, ch.Update([]any{"update"}, 0))

	require.Empty(t, exec.planDagTasks(execCtx))
}

func TestHasStaticInterrupts_CoversNilAndInvalidNodes(t *testing.T) {
	require.False(t, hasStaticInterrupts(nil))

	g := New(NewStateSchema())
	g.mu.Lock()
	g.nodes["nil-node"] = nil
	g.nodes["interrupt"] = &Node{
		ID:              "interrupt",
		interruptBefore: true,
	}
	g.mu.Unlock()

	require.True(t, hasStaticInterrupts(g))
}

func TestExecutionEngine_validate_RejectsUnknown(t *testing.T) {
	require.NoError(t, ExecutionEngineBSP.validate())
	require.NoError(t, ExecutionEngineDAG.validate())

	err := ExecutionEngine("unknown").validate()
	require.ErrorIs(t, err, errUnknownExecutionEngine)
}

func TestExecutor_executeGraph_DagEngine_RejectsExternalInterrupt(t *testing.T) {
	g := compileSimpleDagGraph(t)
	exec, err := NewExecutor(g, WithExecutionEngine(ExecutionEngineDAG))
	require.NoError(t, err)

	ctx, _ := WithGraphInterrupt(context.Background())
	err = exec.executeGraph(
		ctx,
		State{},
		&agent.Invocation{InvocationID: "dag-ext-interrupt"},
		nil,
		time.Now(),
	)
	require.ErrorIs(t, err, ErrDagEngineInterruptUnsupported)
}

func TestExecutor_executeGraph_DagEngine_RejectsCheckpointSaver(t *testing.T) {
	g := compileSimpleDagGraph(t)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	exec.executionEngine = ExecutionEngineDAG
	exec.checkpointSaver = &mockCheckpointSaver{}
	err = exec.executeGraph(
		context.Background(),
		State{},
		&agent.Invocation{InvocationID: "dag-ckpt"},
		nil,
		time.Now(),
	)
	require.ErrorIs(t, err, ErrDagEngineCheckpointUnsupported)
}

func TestExecutor_executeGraph_DagEngine_RejectsStaticInterrupts(t *testing.T) {
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

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	exec.executionEngine = ExecutionEngineDAG
	err = exec.executeGraph(
		context.Background(),
		State{},
		&agent.Invocation{InvocationID: "dag-static-interrupt"},
		nil,
		time.Now(),
	)
	require.ErrorIs(t, err, ErrDagEngineInterruptUnsupported)
}

func TestExecutor_executeGraph_RejectsUnknownExecutionEngine(t *testing.T) {
	g := compileSimpleDagGraph(t)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	exec.executionEngine = ExecutionEngine("unknown")
	err = exec.executeGraph(
		context.Background(),
		State{},
		&agent.Invocation{InvocationID: "dag-unknown-engine"},
		nil,
		time.Now(),
	)
	require.ErrorIs(t, err, errUnknownExecutionEngine)
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
