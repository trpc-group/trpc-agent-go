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
	"encoding/json"
	"errors"
	"sync"
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

func TestNewExecutor_DagEngine_AllowsCheckpointSaver(t *testing.T) {
	g := compileSimpleDagGraph(t)
	_, err := NewExecutor(
		g,
		WithExecutionEngine(ExecutionEngineDAG),
		WithCheckpointSaver(&mockCheckpointSaver{}),
	)
	require.NoError(t, err)
}

func TestExecutor_DagEngine_StaticInterruptBefore_EmitsInterrupt(
	t *testing.T,
) {
	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(
		dagNodeEntry,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
	sg.AddNode(
		dagNodeSlow,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
		WithInterruptBefore(),
	)
	sg.SetEntryPoint(dagNodeEntry)
	sg.AddEdge(dagNodeEntry, dagNodeSlow)

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g, WithExecutionEngine(ExecutionEngineDAG))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), dagWaitLong)
	defer cancel()

	evts, err := exec.Execute(ctx, State{}, &agent.Invocation{
		InvocationID: "dag-static-before",
	})
	require.NoError(t, err)

	meta := waitForNodeInterruptAndDrain(t, evts, dagWaitLong)
	require.Equal(t, dagNodeSlow, meta.NodeID)
}

func TestExecutor_DagEngine_StaticInterruptAfter_EmitsInterrupt(
	t *testing.T,
) {
	const (
		nodeEntry = "entry"
		nodeIntr  = "intr"
		nodeDown  = "down"
	)

	downStarted := make(chan struct{}, 1)
	notify := func(ch chan<- struct{}) {
		select {
		case ch <- struct{}{}:
		default:
		}
	}

	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(
		nodeEntry,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
	sg.AddNode(
		nodeIntr,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
		WithInterruptAfter(),
	)
	sg.AddNode(
		nodeDown,
		func(context.Context, State) (any, error) {
			notify(downStarted)
			return nil, nil
		},
	)
	sg.SetEntryPoint(nodeEntry)
	sg.AddEdge(nodeEntry, nodeIntr)
	sg.AddEdge(nodeIntr, nodeDown)

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g, WithExecutionEngine(ExecutionEngineDAG))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), dagWaitLong)
	defer cancel()

	evts, err := exec.Execute(ctx, State{}, &agent.Invocation{
		InvocationID: "dag-static-after",
	})
	require.NoError(t, err)

	meta := waitForNodeInterruptAndDrain(t, evts, dagWaitLong)
	require.Equal(t, nodeIntr, meta.NodeID)

	select {
	case <-downStarted:
		t.Fatal("unexpected downstream start after interrupt")
	default:
	}
}

func TestNewDagLoop_ValidatesInputs(t *testing.T) {
	ctx := context.Background()
	inv := &agent.Invocation{InvocationID: "dag-loop"}

	_, err := newDagLoop(nil, nil, inv, nil, nil, 0, nil)
	require.ErrorContains(t, err, "context is nil")

	_, err = newDagLoop(nil, ctx, inv, nil, nil, 0, nil)
	require.ErrorContains(t, err, "execution context is nil")

	execCtx := &ExecutionContext{}
	_, err = newDagLoop(nil, ctx, inv, execCtx, nil, 0, nil)
	require.ErrorContains(t, err, "execution channels are nil")

	execCtx.channels = ichannel.NewChannelManager()
	_, err = newDagLoop(&Executor{}, ctx, inv, execCtx, nil, 0, nil)
	require.ErrorContains(t, err, "graph is nil")

	g := New(NewStateSchema())
	_, err = newDagLoop(
		&Executor{graph: g, maxConcurrency: 1},
		ctx,
		inv,
		execCtx,
		nil,
		0,
		nil,
	)
	require.ErrorContains(t, err, "no entry point defined")
}

func TestExecutor_DagEngine_DrainsOnTaskError(t *testing.T) {
	const errMessage = "dag node error"

	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(
		dagNodeEntry,
		func(context.Context, State) (any, error) {
			return nil, errors.New(errMessage)
		},
	)
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

func TestDagLoop_snapshotNextNodes_CollectsAllSources(t *testing.T) {
	const (
		nodeEntry = "entry"
		nodeA     = "a"
		nodeB     = "b"
		nodeC     = "c"

		nodeTriggerName = "trigger_node"
	)

	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(
		nodeEntry,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
	sg.AddNode(
		nodeA,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
	sg.AddNode(
		nodeB,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
	sg.AddNode(
		nodeC,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
	sg.AddNode(
		nodeTriggerName,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
	sg.SetEntryPoint(nodeEntry)
	sg.AddEdge(nodeEntry, nodeA)
	sg.AddEdge(nodeEntry, nodeB)
	sg.AddEdge(nodeEntry, nodeC)
	sg.AddEdge(nodeEntry, nodeTriggerName)

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	execCtx := &ExecutionContext{channels: exec.buildChannelManager()}
	chName := ChannelBranchPrefix + nodeTriggerName
	ch, ok := execCtx.channels.GetChannel(chName)
	require.True(t, ok)
	require.NotNil(t, ch)
	require.True(t, ch.Update([]any{"update"}, 0))

	g.mu.Lock()
	g.triggerToNodes = map[string][]string{
		chName: {nodeB, End, nodeC},
	}
	g.mu.Unlock()

	l := &dagLoop{
		executor: exec,
		execCtx:  execCtx,
		ready: []*Task{
			nil,
			{NodeID: ""},
			{NodeID: End},
			{NodeID: nodeA},
			{NodeID: nodeA},
		},
		waiting: map[string][]*Task{
			"":    {&Task{NodeID: nodeA}},
			nodeA: nil,
			nodeB: {&Task{NodeID: nodeB}, &Task{NodeID: nodeB}},
			End:   {&Task{NodeID: End}},
		},
		rerunNodes: []string{"", End, nodeB},
	}

	got := l.snapshotNextNodes()
	want := []string{nodeA, nodeA, nodeB, nodeB, nodeB, nodeC}
	require.Equal(t, want, got)
}

func TestDagLoop_recordGraphInterruptInput_UsesFallbackSnapshot(
	t *testing.T,
) {
	const (
		nodeID = "node"
		key    = "k"
		value  = "v"
	)

	loop := &dagLoop{
		report: newStepExecutionReport(nil),
	}
	loop.recordGraphInterruptInput(&Task{NodeID: nodeID})

	loop.pendingIntr = newExternalInterruptError(false)
	loop.pendingExtra = forcedExternalInterruptExtra()
	loop.recordGraphInterruptInput(&Task{NodeID: nodeID})

	inputs := loop.pendingExtra[CheckpointMetaKeyGraphInterruptInputs]
	inputMap, ok := inputs.(map[string][]any)
	require.True(t, ok)
	require.Empty(t, inputMap)

	taskState := State{key: value}
	task := &Task{NodeID: nodeID, Input: taskState}

	loop.pendingIntr = newExternalInterruptError(true)
	loop.pendingExtra = map[string]any{
		CheckpointMetaKeyGraphInterruptInputs: "bad",
	}
	loop.recordGraphInterruptInput(task)

	loop.pendingExtra = forcedExternalInterruptExtra()
	loop.recordGraphInterruptInput(task)

	inputs = loop.pendingExtra[CheckpointMetaKeyGraphInterruptInputs]
	inputMap, ok = inputs.(map[string][]any)
	require.True(t, ok)
	require.Len(t, inputMap[nodeID], 1)

	snapshot, ok := inputMap[nodeID][0].(State)
	require.True(t, ok)

	taskState[key] = "mutated"
	require.Equal(t, value, snapshot[key])
}

func TestIsForcedExternalInterrupt(t *testing.T) {
	require.False(t, isForcedExternalInterrupt(nil))
	require.False(t, isForcedExternalInterrupt(&InterruptError{Key: "other"}))
	require.False(t, isForcedExternalInterrupt(&InterruptError{
		Key:   ExternalInterruptKey,
		Value: "bad",
	}))
	require.True(t, isForcedExternalInterrupt(newExternalInterruptError(true)))
}

func TestExecutor_DagEngine_RespectsMaxConcurrency(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})

	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(
		dagNodeEntry,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)

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
	sg.AddNode(
		dagNodeEntry,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
	sg.AddNode(
		dagNodeSlow,
		func(ctx context.Context, state State) (any, error) {
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
		},
	)
	sg.AddNode(
		dagNodeFast,
		func(context.Context, State) (any, error) {
			select {
			case doneFast <- struct{}{}:
			default:
			}
			return nil, nil
		},
	)
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
	sg.AddNode(
		dagNodeEntry,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)

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

	require.Nil(t, exec.createDagTask(nil, ""))
	require.Nil(t, exec.createDagTask(nil, End))
	require.Nil(t, exec.createDagTask(nil, "missing"))
	require.NotNil(t, exec.createDagTask(nil, dagNodeEntry))
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
	sg.AddNode(
		dagNodeEntry,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
	sg.AddNode(
		dagNodeSlow,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
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
	sg.AddNode(
		dagNodeEntry,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
	sg.AddNode(
		dagNodeSlow,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
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
	sg.AddNode(
		dagNodeEntry,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
	sg.AddNode(
		dagNodeSlow,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
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

func TestExecutor_DagEngine_ExternalInterrupt_PausesBeforeDownstream(
	t *testing.T,
) {
	const (
		nodeEntry   = "entry"
		nodeSlow    = "slow"
		nodeNext    = "next"
		waitTimeout = 2 * time.Second
	)

	slowRelease := make(chan struct{})
	slowStarted := make(chan struct{}, 1)
	nextStarted := make(chan struct{}, 1)

	notify := func(ch chan<- struct{}) {
		select {
		case ch <- struct{}{}:
		default:
		}
	}

	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(
		nodeEntry,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
	sg.AddNode(
		nodeSlow,
		func(ctx context.Context, state State) (any, error) {
			notify(slowStarted)
			select {
			case <-slowRelease:
				return nil, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	)
	sg.AddNode(
		nodeNext,
		func(context.Context, State) (any, error) {
			notify(nextStarted)
			return nil, nil
		},
	)
	sg.SetEntryPoint(nodeEntry)
	sg.AddEdge(nodeEntry, nodeSlow)
	sg.AddEdge(nodeSlow, nodeNext)

	g, err := sg.Compile()
	require.NoError(t, err)

	saver := newTestCheckpointSaver()
	exec, err := NewExecutor(
		g,
		WithExecutionEngine(ExecutionEngineDAG),
		WithCheckpointSaver(saver),
		WithMaxConcurrency(2),
	)
	require.NoError(t, err)

	baseCtx, interrupt := WithGraphInterrupt(context.Background())
	ctx, cancel := context.WithTimeout(baseCtx, waitTimeout)
	defer cancel()

	evts, err := exec.Execute(ctx, State{}, &agent.Invocation{
		InvocationID: "dag-ext-interrupt",
	})
	require.NoError(t, err)

	waitForSignal(t, slowStarted, "slow started", waitTimeout)
	interrupt()
	close(slowRelease)

	meta := waitForExternalInterruptAndDrain(t, evts, waitTimeout)
	require.Equal(t, ExternalInterruptKey, meta.InterruptKey)
	require.NotEmpty(t, meta.LineageID)
	require.NotEmpty(t, meta.CheckpointID)

	select {
	case <-nextStarted:
		t.Fatal("unexpected downstream start after interrupt")
	default:
	}
}

func TestExecutor_DagEngine_ExternalInterruptTimeout_RerunsWithInputSnapshot(
	t *testing.T,
) {
	const (
		nodeEntry = "entry"
		nodeSlow  = "slow"
		nodeMut   = "mut"

		stateKeyValue = "v"
		stateKeySeen  = "seen"

		valueOld = "old"
		valueNew = "new"

		slowDelay        = 200 * time.Millisecond
		interruptTimeout = 50 * time.Millisecond
		waitTimeout      = 2 * time.Second
	)

	slowStarted := make(chan struct{}, 1)
	mutDone := make(chan struct{}, 1)

	notify := func(ch chan<- struct{}) {
		select {
		case ch <- struct{}{}:
		default:
		}
	}

	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(
		nodeEntry,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
	sg.AddNode(
		nodeSlow,
		func(ctx context.Context, state State) (any, error) {
			notify(slowStarted)
			seen, _ := state[stateKeyValue].(string)

			timer := time.NewTimer(slowDelay)
			defer timer.Stop()

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
				return State{stateKeySeen: seen}, nil
			}
		},
	)
	sg.AddNode(
		nodeMut,
		func(context.Context, State) (any, error) {
			notify(mutDone)
			return State{stateKeyValue: valueNew}, nil
		},
	)
	sg.SetEntryPoint(nodeEntry)
	sg.AddEdge(nodeEntry, nodeSlow)
	sg.AddEdge(nodeEntry, nodeMut)

	g, err := sg.Compile()
	require.NoError(t, err)

	saver := newTestCheckpointSaver()
	exec, err := NewExecutor(
		g,
		WithExecutionEngine(ExecutionEngineDAG),
		WithCheckpointSaver(saver),
		WithMaxConcurrency(2),
	)
	require.NoError(t, err)

	baseCtx, interrupt := WithGraphInterrupt(context.Background())
	ctx, cancel := context.WithTimeout(baseCtx, waitTimeout)
	defer cancel()

	evts, err := exec.Execute(
		ctx,
		State{stateKeyValue: valueOld},
		&agent.Invocation{InvocationID: "dag-ext-timeout"},
	)
	require.NoError(t, err)

	waitForSignal(t, slowStarted, "slow started", waitTimeout)
	waitForSignal(t, mutDone, "mut done", waitTimeout)

	interrupt(WithGraphInterruptTimeout(interruptTimeout))

	meta := waitForExternalInterruptAndDrain(t, evts, waitTimeout)
	require.Equal(t, ExternalInterruptKey, meta.InterruptKey)
	require.NotEmpty(t, meta.LineageID)
	require.NotEmpty(t, meta.CheckpointID)

	resumeCtx, resumeCancel := context.WithTimeout(
		context.Background(),
		waitTimeout,
	)
	defer resumeCancel()

	evts, err = exec.Execute(
		resumeCtx,
		State{
			CfgKeyLineageID:    meta.LineageID,
			CfgKeyCheckpointID: meta.CheckpointID,
		},
		&agent.Invocation{InvocationID: "dag-ext-timeout-resume"},
	)
	require.NoError(t, err)

	finalState := waitForCompletionAndDrain(t, evts, waitTimeout)
	require.Equal(t, valueNew, finalState[stateKeyValue])
	require.Equal(t, valueOld, finalState[stateKeySeen])
}

func TestExecutor_DagEngine_InternalInterrupt_SavesCheckpointAndResumes(
	t *testing.T,
) {
	const (
		nodeEntry      = "entry"
		nodeAsk        = "ask"
		nodeAfter      = "after"
		stateKeyAnswer = "answer"

		interruptKey    = "k1"
		interruptPrompt = "ask"
		resumeValue     = "ok"
		waitTimeout     = 2 * time.Second
	)

	doneAfter := make(chan struct{}, 1)
	notify := func(ch chan<- struct{}) {
		select {
		case ch <- struct{}{}:
		default:
		}
	}

	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode(
		nodeEntry,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
	sg.AddNode(
		nodeAsk,
		func(ctx context.Context, state State) (any, error) {
			v, err := Interrupt(ctx, state, interruptKey, interruptPrompt)
			if err != nil {
				return nil, err
			}
			return State{stateKeyAnswer: v}, nil
		},
	)
	sg.AddNode(
		nodeAfter,
		func(context.Context, State) (any, error) {
			notify(doneAfter)
			return nil, nil
		},
	)
	sg.SetEntryPoint(nodeEntry)
	sg.AddEdge(nodeEntry, nodeAsk)
	sg.AddEdge(nodeAsk, nodeAfter)

	g, err := sg.Compile()
	require.NoError(t, err)

	saver := newTestCheckpointSaver()
	exec, err := NewExecutor(
		g,
		WithExecutionEngine(ExecutionEngineDAG),
		WithCheckpointSaver(saver),
		WithMaxConcurrency(2),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()

	evts, err := exec.Execute(ctx, State{}, &agent.Invocation{
		InvocationID: "dag-internal-interrupt",
	})
	require.NoError(t, err)

	meta := waitForNodeInterruptAndDrain(t, evts, waitTimeout)
	require.Equal(t, nodeAsk, meta.NodeID)
	require.Equal(t, interruptKey, meta.InterruptKey)
	require.NotEmpty(t, meta.LineageID)
	require.NotEmpty(t, meta.CheckpointID)

	resumeCmd := (&ResumeCommand{}).WithResumeMap(map[string]any{
		interruptKey: resumeValue,
	})
	resumeState := State{
		CfgKeyLineageID:    meta.LineageID,
		CfgKeyCheckpointID: meta.CheckpointID,
		StateKeyCommand:    resumeCmd,
	}

	evts, err = exec.Execute(ctx, resumeState, &agent.Invocation{
		InvocationID: "dag-internal-resume",
	})
	require.NoError(t, err)

	finalState := waitForCompletionAndDrain(t, evts, waitTimeout)
	require.Equal(t, resumeValue, finalState[stateKeyAnswer])

	waitForSignal(t, doneAfter, "after done", waitTimeout)
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
	sg.AddNode(
		dagNodeEntry,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
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
	sg.AddNode(
		dagNodeEntry,
		func(context.Context, State) (any, error) {
			return nil, nil
		},
	)
	sg.AddNode(
		dagNodeSlow,
		func(ctx context.Context, state State) (any, error) {
			notify(slowStarted)
			select {
			case <-slowRelease:
				return nil, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	)
	sg.AddNode(
		dagNodeFast,
		func(context.Context, State) (any, error) {
			notify(fastDone)
			return nil, nil
		},
	)
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

type testCheckpointSaver struct {
	mu     sync.RWMutex
	byKey  map[string]*CheckpointTuple
	latest map[string]string
}

const (
	testSaverKeySep = "|"

	testSaverErrLineageRequired = "lineage_id is required"
	testSaverErrCheckpointNil   = "checkpoint is nil"
)

var _ CheckpointSaver = (*testCheckpointSaver)(nil)

func newTestCheckpointSaver() *testCheckpointSaver {
	return &testCheckpointSaver{
		byKey:  make(map[string]*CheckpointTuple),
		latest: make(map[string]string),
	}
}

func (s *testCheckpointSaver) Get(
	ctx context.Context,
	config map[string]any,
) (*Checkpoint, error) {
	tuple, err := s.GetTuple(ctx, config)
	if err != nil {
		return nil, err
	}
	if tuple == nil {
		return nil, nil
	}
	return tuple.Checkpoint, nil
}

func (s *testCheckpointSaver) GetTuple(
	ctx context.Context,
	config map[string]any,
) (*CheckpointTuple, error) {
	lineageID := GetLineageID(config)
	if lineageID == "" {
		return nil, errors.New(testSaverErrLineageRequired)
	}
	namespace := GetNamespace(config)
	checkpointID := GetCheckpointID(config)

	s.mu.RLock()
	if checkpointID == "" {
		checkpointID = s.latest[s.latestKey(lineageID, namespace)]
	}
	tuple := s.byKey[s.tupleKey(lineageID, namespace, checkpointID)]
	s.mu.RUnlock()

	if checkpointID == "" || tuple == nil {
		return nil, nil
	}
	s.setCheckpointID(config, checkpointID)
	return cloneTestCheckpointTuple(tuple), nil
}

func (s *testCheckpointSaver) List(
	ctx context.Context,
	config map[string]any,
	filter *CheckpointFilter,
) ([]*CheckpointTuple, error) {
	lineageID := GetLineageID(config)
	if lineageID == "" {
		return nil, errors.New(testSaverErrLineageRequired)
	}
	namespace := GetNamespace(config)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []*CheckpointTuple
	for key, tuple := range s.byKey {
		if tuple == nil || tuple.Checkpoint == nil {
			continue
		}
		if !s.keyMatches(key, lineageID, namespace) {
			continue
		}
		out = append(out, cloneTestCheckpointTuple(tuple))
	}
	return out, nil
}

func (s *testCheckpointSaver) Put(
	ctx context.Context,
	req PutRequest,
) (map[string]any, error) {
	return s.PutFull(ctx, PutFullRequest{
		Config:      req.Config,
		Checkpoint:  req.Checkpoint,
		Metadata:    req.Metadata,
		NewVersions: req.NewVersions,
	})
}

func (s *testCheckpointSaver) PutWrites(
	ctx context.Context,
	req PutWritesRequest,
) error {
	return nil
}

func (s *testCheckpointSaver) PutFull(
	ctx context.Context,
	req PutFullRequest,
) (map[string]any, error) {
	lineageID := GetLineageID(req.Config)
	if lineageID == "" {
		return nil, errors.New(testSaverErrLineageRequired)
	}
	if req.Checkpoint == nil {
		return nil, errors.New(testSaverErrCheckpointNil)
	}
	namespace := GetNamespace(req.Config)
	checkpointID := req.Checkpoint.ID
	if checkpointID == "" {
		return nil, errors.New(testSaverErrCheckpointNil)
	}

	s.setCheckpointID(req.Config, checkpointID)
	stored := &CheckpointTuple{
		Config:        cloneTestCheckpointConfig(req.Config),
		Checkpoint:    req.Checkpoint.Copy(),
		Metadata:      req.Metadata,
		PendingWrites: cloneTestPendingWrites(req.PendingWrites),
	}

	s.mu.Lock()
	s.byKey[s.tupleKey(lineageID, namespace, checkpointID)] = stored
	s.latest[s.latestKey(lineageID, namespace)] = checkpointID
	s.mu.Unlock()

	return cloneTestCheckpointConfig(req.Config), nil
}

func (s *testCheckpointSaver) DeleteLineage(
	ctx context.Context,
	lineageID string,
) error {
	if lineageID == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for key := range s.byKey {
		if !s.keyMatchesLineage(key, lineageID) {
			continue
		}
		delete(s.byKey, key)
	}
	for key := range s.latest {
		if !s.keyMatchesLineage(key, lineageID) {
			continue
		}
		delete(s.latest, key)
	}
	return nil
}

func (s *testCheckpointSaver) Close() error {
	return nil
}

func (s *testCheckpointSaver) tupleKey(
	lineageID string,
	namespace string,
	checkpointID string,
) string {
	return lineageID + testSaverKeySep + namespace + testSaverKeySep +
		checkpointID
}

func (s *testCheckpointSaver) latestKey(
	lineageID string,
	namespace string,
) string {
	return lineageID + testSaverKeySep + namespace
}

func (s *testCheckpointSaver) keyMatches(
	key string,
	lineageID string,
	namespace string,
) bool {
	prefix := lineageID + testSaverKeySep + namespace + testSaverKeySep
	return len(key) >= len(prefix) && key[:len(prefix)] == prefix
}

func (s *testCheckpointSaver) keyMatchesLineage(
	key string,
	lineageID string,
) bool {
	prefix := lineageID + testSaverKeySep
	return len(key) >= len(prefix) && key[:len(prefix)] == prefix
}

func (s *testCheckpointSaver) setCheckpointID(
	config map[string]any,
	checkpointID string,
) {
	if config == nil {
		return
	}
	raw := config[CfgKeyConfigurable]
	configurable, ok := raw.(map[string]any)
	if !ok || configurable == nil {
		configurable = make(map[string]any)
		config[CfgKeyConfigurable] = configurable
	}
	configurable[CfgKeyCheckpointID] = checkpointID
}

func cloneTestCheckpointTuple(in *CheckpointTuple) *CheckpointTuple {
	if in == nil {
		return nil
	}
	out := &CheckpointTuple{
		Config:        cloneTestCheckpointConfig(in.Config),
		Checkpoint:    in.Checkpoint.Copy(),
		Metadata:      in.Metadata,
		ParentConfig:  cloneTestCheckpointConfig(in.ParentConfig),
		PendingWrites: cloneTestPendingWrites(in.PendingWrites),
	}
	return out
}

func cloneTestCheckpointConfig(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}

	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}

	raw := out[CfgKeyConfigurable]
	configurable, ok := raw.(map[string]any)
	if !ok || configurable == nil {
		return out
	}
	copied := make(map[string]any, len(configurable))
	for k, v := range configurable {
		copied[k] = v
	}
	out[CfgKeyConfigurable] = copied
	return out
}

func cloneTestPendingWrites(in []PendingWrite) []PendingWrite {
	if len(in) == 0 {
		return nil
	}
	out := make([]PendingWrite, len(in))
	copy(out, in)
	return out
}

func waitForNodeInterruptAndDrain(
	t *testing.T,
	evts <-chan *event.Event,
	timeout time.Duration,
) PregelStepMetadata {
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var (
		found bool
		meta  PregelStepMetadata
	)
	for {
		select {
		case e, ok := <-evts:
			if !ok {
				if !found {
					t.Fatal("missing interrupt event")
				}
				return meta
			}
			m, ok := pregelMetaFromEvent(e)
			if !ok || m.NodeID == "" || m.InterruptValue == nil {
				continue
			}
			meta = m
			found = true
		case <-timer.C:
			t.Fatal("timeout waiting for interrupt event")
		}
	}
}

func waitForExternalInterruptAndDrain(
	t *testing.T,
	evts <-chan *event.Event,
	timeout time.Duration,
) PregelStepMetadata {
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var (
		found bool
		meta  PregelStepMetadata
	)
	for {
		select {
		case e, ok := <-evts:
			if !ok {
				if !found {
					t.Fatal("missing external interrupt event")
				}
				return meta
			}
			m, ok := pregelMetaFromEvent(e)
			if !ok || m.InterruptKey != ExternalInterruptKey {
				continue
			}
			meta = m
			found = true
		case <-timer.C:
			t.Fatal("timeout waiting for external interrupt event")
		}
	}
}

func waitForCompletionAndDrain(
	t *testing.T,
	evts <-chan *event.Event,
	timeout time.Duration,
) map[string]any {
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var (
		found   bool
		state   map[string]any
		rawSeen map[string][]byte
	)
	for {
		select {
		case e, ok := <-evts:
			if !ok {
				if !found {
					t.Fatal("missing completion event")
				}
				return state
			}
			if e == nil || e.Response == nil || !e.Response.Done {
				continue
			}
			rawSeen = e.StateDelta
			state = parseStateDelta(t, rawSeen)
			found = true
		case <-timer.C:
			t.Fatal("timeout waiting for completion event")
		}
	}
}

func pregelMetaFromEvent(
	e *event.Event,
) (PregelStepMetadata, bool) {
	if e == nil || e.Object != ObjectTypeGraphPregelStep {
		return PregelStepMetadata{}, false
	}
	if e.StateDelta == nil {
		return PregelStepMetadata{}, false
	}
	raw := e.StateDelta[MetadataKeyPregel]
	if len(raw) == 0 {
		return PregelStepMetadata{}, false
	}
	var meta PregelStepMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return PregelStepMetadata{}, false
	}
	return meta, true
}

func parseStateDelta(
	t *testing.T,
	raw map[string][]byte,
) map[string]any {
	t.Helper()

	out := make(map[string]any)
	for key, value := range raw {
		if key == "" || len(value) == 0 {
			continue
		}
		if len(key) > 0 && key[0] == '_' {
			continue
		}
		var v any
		if err := json.Unmarshal(value, &v); err != nil {
			continue
		}
		out[key] = v
	}
	return out
}
