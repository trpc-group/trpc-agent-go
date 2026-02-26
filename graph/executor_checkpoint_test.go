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
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	ichannel "trpc.group/trpc-go/trpc-agent-go/graph/internal/channel"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func collectObjectCounts(ch <-chan *event.Event) map[string]int {
	counts := make(map[string]int)
	for evt := range ch {
		if evt == nil {
			continue
		}
		counts[evt.Object]++
	}
	return counts
}

// Test that executor with a saver triggers initial checkpoint creation (covering getNext* helpers)
func TestExecutor_WithSaver_CreatesInitialCheckpoint(t *testing.T) {
	// simple graph with single node
	g, err := NewStateGraph(NewStateSchema()).
		AddNode("a", func(ctx context.Context, state State) (any, error) { return nil, nil }).
		SetEntryPoint("a").
		SetFinishPoint("a").
		Compile()
	require.NoError(t, err)

	saver := newMockSaver()
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	ch, err := exec.Execute(context.Background(), State{}, &agent.Invocation{InvocationID: "inv-getnext"})
	require.NoError(t, err)
	for range ch { /* drain */
	}
}

func TestExecutor_CheckpointLifecycleEvents_DefaultDisabled(
	t *testing.T,
) {
	g, err := NewStateGraph(NewStateSchema()).
		AddNode("a",
			func(ctx context.Context, state State) (any, error) {
				return nil, nil
			},
		).
		SetEntryPoint("a").
		SetFinishPoint("a").
		Compile()
	require.NoError(t, err)

	saver := newMockSaver()
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: "inv-default"}
	ch, err := exec.Execute(context.Background(), State{}, inv)
	require.NoError(t, err)

	counts := collectObjectCounts(ch)
	require.Zero(t, counts[ObjectTypeGraphCheckpointCreated])
	require.Zero(t, counts[ObjectTypeGraphCheckpointCommitted])
	require.Zero(t, counts[ObjectTypeGraphCheckpointInterrupt])
}

func TestExecutor_CheckpointLifecycleEvents_EmittedWhenEnabled(
	t *testing.T,
) {
	g, err := NewStateGraph(NewStateSchema()).
		AddNode("a",
			func(ctx context.Context, state State) (any, error) {
				return nil, nil
			},
		).
		SetEntryPoint("a").
		SetFinishPoint("a").
		Compile()
	require.NoError(t, err)

	saver := newMockSaver()
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: "inv-enabled"}
	agent.WithStreamMode(agent.StreamModeCheckpoints)(&inv.RunOptions)
	ch, err := exec.Execute(context.Background(), State{}, inv)
	require.NoError(t, err)

	counts := collectObjectCounts(ch)
	require.Greater(t, counts[ObjectTypeGraphCheckpointCreated], 0)
	require.Greater(t, counts[ObjectTypeGraphCheckpointCommitted], 0)
}

type failingPutFullSaver struct {
	*mockSaver
}

func (f *failingPutFullSaver) PutFull(
	ctx context.Context,
	req PutFullRequest,
) (map[string]any, error) {
	const errPutFull = "putfull failed"
	return nil, errors.New(errPutFull)
}

type panicGetTupleSaver struct {
	*mockSaver
}

func (p *panicGetTupleSaver) GetTuple(
	ctx context.Context,
	config map[string]any,
) (*CheckpointTuple, error) {
	panic("gettuple panic")
}

type failingGetTupleSaver struct {
	*mockSaver
	retErr error
}

func (f *failingGetTupleSaver) GetTuple(
	_ context.Context,
	_ map[string]any,
) (*CheckpointTuple, error) {
	return nil, f.retErr
}

func TestExecutor_CheckpointSaveError_DoesNotStopRun(
	t *testing.T,
) {
	g, err := NewStateGraph(NewStateSchema()).
		AddNode("a", func(ctx context.Context, state State) (any, error) {
			return nil, nil
		}).
		SetEntryPoint("a").
		SetFinishPoint("a").
		Compile()
	require.NoError(t, err)

	baseSaver := newMockSaver()
	saver := &failingPutFullSaver{mockSaver: baseSaver}
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	ch, err := exec.Execute(
		context.Background(),
		State{},
		&agent.Invocation{InvocationID: "inv-save-fail"},
	)
	require.NoError(t, err)
	for range ch {
	}
}

func TestExecutor_PanicInSaver_IsRecovered(
	t *testing.T,
) {
	g, err := NewStateGraph(NewStateSchema()).
		AddNode("a", func(ctx context.Context, state State) (any, error) {
			return nil, nil
		}).
		SetEntryPoint("a").
		SetFinishPoint("a").
		Compile()
	require.NoError(t, err)

	baseSaver := newMockSaver()
	saver := &panicGetTupleSaver{mockSaver: baseSaver}
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	// Empty invocation id triggers lineage generation before panic.
	ch, err := exec.Execute(
		context.Background(),
		State{},
		&agent.Invocation{},
	)
	require.NoError(t, err)
	for range ch {
	}
}

func TestExecutor_NonResume_GetTupleError_DoesNotStopRun(t *testing.T) {
	const (
		getErrMsg = "gettuple failed"
		invID     = "inv-gettuple-nonresume"
	)

	g, err := NewStateGraph(NewStateSchema()).
		AddNode("a", func(ctx context.Context, state State) (any, error) {
			return nil, nil
		}).
		SetEntryPoint("a").
		SetFinishPoint("a").
		Compile()
	require.NoError(t, err)

	baseSaver := newMockSaver()
	saver := &failingGetTupleSaver{
		mockSaver: baseSaver,
		retErr:    errors.New(getErrMsg),
	}
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	ch, err := exec.Execute(
		context.Background(),
		State{},
		&agent.Invocation{InvocationID: invID},
	)
	require.NoError(t, err)

	var gotDone bool
	for evt := range ch {
		require.Nil(t, evt.Error)
		if evt.Done {
			gotDone = true
		}
	}
	require.True(t, gotDone)
	require.NotZero(t, len(baseSaver.byID))
}

func TestExecutor_Resume_GetTupleError_ReturnsError(t *testing.T) {
	const (
		lineageID = "ln-gettuple-err"
		checkID   = "ck-gettuple-err"
		getErrMsg = "gettuple failed"
		invID     = "inv-gettuple-resume"
	)

	g, err := NewStateGraph(NewStateSchema()).
		AddNode("a", func(ctx context.Context, state State) (any, error) {
			return nil, nil
		}).
		SetEntryPoint("a").
		SetFinishPoint("a").
		Compile()
	require.NoError(t, err)

	baseSaver := newMockSaver()
	saver := &failingGetTupleSaver{
		mockSaver: baseSaver,
		retErr:    errors.New(getErrMsg),
	}
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	init := State{
		CfgKeyLineageID:    lineageID,
		CfgKeyCheckpointID: checkID,
	}
	ch, err := exec.Execute(
		context.Background(),
		init,
		&agent.Invocation{InvocationID: invID},
	)
	require.NoError(t, err)

	var gotErr *model.ResponseError
	var gotDone bool
	for evt := range ch {
		if evt.Error != nil {
			gotErr = evt.Error
		}
		if evt.Done {
			gotDone = true
		}
	}
	require.False(t, gotDone)
	require.NotNil(t, gotErr)
	require.Contains(t, gotErr.Message, getErrMsg)
	require.Zero(t, len(baseSaver.byID))
}

func TestExecutor_Resume_CheckpointNotFound_ReturnsError(t *testing.T) {
	const (
		lineageID = "ln-missing-ckpt"
		checkID   = "ck-missing"
		invID     = "inv-missing-ckpt"
	)

	g, err := NewStateGraph(NewStateSchema()).
		AddNode("a", func(ctx context.Context, state State) (any, error) {
			return nil, nil
		}).
		SetEntryPoint("a").
		SetFinishPoint("a").
		Compile()
	require.NoError(t, err)

	saver := newMockSaver()
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	init := State{
		CfgKeyLineageID:    lineageID,
		CfgKeyCheckpointID: checkID,
	}
	ch, err := exec.Execute(
		context.Background(),
		init,
		&agent.Invocation{InvocationID: invID},
	)
	require.NoError(t, err)

	var gotErr *model.ResponseError
	var gotDone bool
	for evt := range ch {
		if evt.Error != nil {
			gotErr = evt.Error
		}
		if evt.Done {
			gotDone = true
		}
	}
	require.False(t, gotDone)
	require.NotNil(t, gotErr)
	require.Contains(t, gotErr.Message, ErrCheckpointNotFound.Error())
	require.Zero(t, len(saver.byID))
}

func TestExecutor_CreateTask_LogsStepCountAndFinalNode(
	t *testing.T,
) {
	g, err := NewStateGraph(NewStateSchema()).
		AddNode("final", func(ctx context.Context, state State) (any, error) {
			return nil, nil
		}).
		SetEntryPoint("final").
		SetFinishPoint("final").
		Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	state := State{
		StateFieldCounter:   1,
		StateFieldStepCount: 2,
	}
	task := exec.createTask("final", state, 0)
	require.NotNil(t, task)
}

// Test resuming from a checkpoint converts values by schema (restoreCheckpointValueWithSchema)
func TestExecutor_Resume_RestoreSchemaValues(t *testing.T) {
	// schema with tags []string
	schema := NewStateSchema().AddField("tags", StateField{Type: reflect.TypeOf([]string{}), Reducer: DefaultReducer})
	g, err := NewStateGraph(schema).
		AddNode("noop", func(ctx context.Context, state State) (any, error) { return nil, nil }).
		SetEntryPoint("noop").
		SetFinishPoint("noop").
		Compile()
	require.NoError(t, err)

	saver := newMockSaver()
	// create a checkpoint with tags as []any to force schema conversion
	ck := NewCheckpoint(map[string]any{"tags": []any{"a", "b"}}, map[string]int64{}, nil)
	cfg := CreateCheckpointConfig("ln-resume", "", "ns")
	_, err = saver.Put(context.Background(), PutRequest{Config: cfg, Checkpoint: ck, Metadata: NewCheckpointMetadata(CheckpointSourceInput, 0), NewVersions: map[string]int64{}})
	require.NoError(t, err)

	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	// Resume using lineage/ns/id
	init := State{CfgKeyLineageID: GetLineageID(cfg), CfgKeyCheckpointNS: GetNamespace(cfg), CfgKeyCheckpointID: ck.ID}
	ch, err := exec.Execute(context.Background(), init, &agent.Invocation{InvocationID: "inv-resume"})
	require.NoError(t, err)
	for range ch { /* drain */
	}
}

func TestExecutor_Resume_AppliesPendingWrites(t *testing.T) {
	const (
		lineageID = "ln-pending"
		namespace = "ns"
		checkID   = "ck-pending"
	)

	g, err := NewStateGraph(NewStateSchema()).
		AddNode("noop",
			func(ctx context.Context, state State) (any, error) {
				return nil, nil
			},
		).
		SetEntryPoint("noop").
		SetFinishPoint("noop").
		Compile()
	require.NoError(t, err)

	saver := newMockSaver()
	ck := NewCheckpoint(
		map[string]any{},
		map[string]int64{},
		nil,
	)
	ck.ID = checkID
	cfg := CreateCheckpointConfig(lineageID, checkID, namespace)
	key := lineageID + ":" + namespace + ":" + checkID
	saver.byID[key] = &CheckpointTuple{
		Config:     cfg,
		Checkpoint: ck,
		Metadata:   NewCheckpointMetadata(CheckpointSourceLoop, 0),
		PendingWrites: []PendingWrite{
			{
				TaskID:   "t1",
				Channel:  ChannelInputPrefix + "x",
				Value:    1,
				Sequence: 1,
			},
		},
	}

	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	init := State{
		CfgKeyLineageID:    lineageID,
		CfgKeyCheckpointNS: namespace,
		CfgKeyCheckpointID: checkID,
	}
	ch, err := exec.Execute(
		context.Background(),
		init,
		&agent.Invocation{InvocationID: "inv-pending"},
	)
	require.NoError(t, err)
	for range ch {
	}
}

// Interrupt test to cover handleInterrupt path
func TestExecutor_HandleInterrupt(t *testing.T) {
	g, err := NewStateGraph(NewStateSchema()).
		AddNode("i", func(ctx context.Context, state State) (any, error) {
			return nil, &InterruptError{
				Value:  "stop",
				NodeID: "i",
				TaskID: "t1",
				Path:   []string{"i"},
			}
		}).
		SetEntryPoint("i").
		SetFinishPoint("i").
		Compile()
	require.NoError(t, err)
	saver := newMockSaver()
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: "inv-int"}
	ch, err := exec.Execute(context.Background(), State{}, inv)
	require.NoError(t, err)
	counts := collectObjectCounts(ch)
	require.Zero(t, counts[ObjectTypeGraphCheckpointInterrupt])
}

func TestExecutor_HandleInterrupt_EmitsCheckpointInterruptWhenEnabled(
	t *testing.T,
) {
	g, err := NewStateGraph(NewStateSchema()).
		AddNode("i",
			func(ctx context.Context, state State) (any, error) {
				return nil, &InterruptError{
					Value:  "stop",
					NodeID: "i",
					TaskID: "t1",
					Path:   []string{"i"},
				}
			},
		).
		SetEntryPoint("i").
		SetFinishPoint("i").
		Compile()
	require.NoError(t, err)

	saver := newMockSaver()
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: "inv-int-enabled"}
	agent.WithStreamMode(agent.StreamModeCheckpoints)(&inv.RunOptions)
	ch, err := exec.Execute(context.Background(), State{}, inv)
	require.NoError(t, err)

	counts := collectObjectCounts(ch)
	require.Greater(t, counts[ObjectTypeGraphCheckpointInterrupt], 0)
}

func TestExecutor_ExternalInterrupt_PausesBeforeNextStep(t *testing.T) {
	const lineageID = "ln-external-interrupt"

	started := make(chan struct{}, 1)
	unblock := make(chan struct{})
	var bRuns int

	g, err := NewStateGraph(NewStateSchema()).
		AddNode("a", func(ctx context.Context, state State) (any, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-unblock
			return nil, nil
		}).
		AddNode("b", func(ctx context.Context, state State) (any, error) {
			bRuns++
			return nil, nil
		}).
		AddEdge("a", "b").
		SetEntryPoint("a").
		SetFinishPoint("b").
		Compile()
	require.NoError(t, err)

	saver := newMockSaver()
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: lineageID}
	agent.WithStreamMode(agent.StreamModeCheckpoints)(&inv.RunOptions)

	ctx, interrupt := WithGraphInterrupt(context.Background())
	ch, err := exec.Execute(ctx, State{}, inv)
	require.NoError(t, err)

	<-started
	interrupt()
	close(unblock)

	for range ch {
	}

	require.Zero(t, bRuns)
	intTuple := latestInterruptTuple(t, saver, lineageID)
	require.Equal(t, []string{"b"}, intTuple.Checkpoint.NextNodes)

	intState := intTuple.Checkpoint.InterruptState
	require.NotNil(t, intState)
	payload, ok := intState.InterruptValue.(ExternalInterruptPayload)
	require.True(t, ok)
	require.False(t, payload.Forced)
}

func TestExecutor_ExternalInterruptTimeout_ForcesInterrupt(
	t *testing.T,
) {
	const lineageID = "ln-external-interrupt-timeout"
	const timeout = 20 * time.Millisecond

	started := make(chan struct{}, 2)
	allowComplete := make(chan struct{})

	g, err := NewStateGraph(NewStateSchema()).
		AddNode("a", func(ctx context.Context, state State) (any, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			select {
			case <-allowComplete:
				return nil, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}).
		SetEntryPoint("a").
		SetFinishPoint("a").
		Compile()
	require.NoError(t, err)

	saver := newMockSaver()
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: lineageID}
	agent.WithStreamMode(agent.StreamModeCheckpoints)(&inv.RunOptions)

	ctx, interrupt := WithGraphInterrupt(context.Background())
	ch, err := exec.Execute(ctx, State{}, inv)
	require.NoError(t, err)

	<-started
	interrupt(WithGraphInterruptTimeout(timeout))

	for range ch {
	}

	intTuple := latestInterruptTuple(t, saver, lineageID)
	intState := intTuple.Checkpoint.InterruptState
	require.NotNil(t, intState)
	payload, ok := intState.InterruptValue.(ExternalInterruptPayload)
	require.True(t, ok)
	require.True(t, payload.Forced)

	require.NotNil(t, intTuple.Metadata)
	require.NotNil(t, intTuple.Metadata.Extra)
	raw := intTuple.Metadata.Extra[CheckpointMetaKeyGraphInterruptInputs]
	require.NotNil(t, raw)
	switch m := raw.(type) {
	case map[string][]any:
		inputs, ok := m["a"]
		require.True(t, ok)
		require.NotEmpty(t, inputs)
	case map[string]any:
		v, ok := m["a"]
		require.True(t, ok)
		require.NotNil(t, v)
	case map[string]State:
		_, ok := m["a"]
		require.True(t, ok)
	default:
		t.Fatalf("unexpected interrupt inputs type: %T", raw)
	}

	close(allowComplete)
	resumeInv := &agent.Invocation{InvocationID: lineageID}
	agent.WithStreamMode(agent.StreamModeCheckpoints)(&resumeInv.RunOptions)

	resumeState := State{
		CfgKeyCheckpointID: intTuple.Checkpoint.ID,
		CfgKeyLineageID:    lineageID,
	}
	ch2, err := exec.Execute(context.Background(), resumeState, resumeInv)
	require.NoError(t, err)
	for range ch2 {
	}
}

func TestExecutor_ExternalInterrupt_PreservesFanOutTasks(t *testing.T) {
	const lineageID = "ln-external-interrupt-fanout"
	const nodeA = "a"
	const nodeB = "b"
	const key = "v"

	started := make(chan struct{}, 1)
	unblock := make(chan struct{})

	var mu sync.Mutex
	var values []int

	g, err := NewStateGraph(NewStateSchema()).
		AddNode(nodeA, func(ctx context.Context, state State) (any, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-unblock
			return []*Command{
				{GoTo: nodeB, Update: State{key: 1}},
				{GoTo: nodeB, Update: State{key: 2}},
			}, nil
		}).
		AddNode(nodeB, func(ctx context.Context, state State) (any, error) {
			v, _ := state[key].(int)
			mu.Lock()
			values = append(values, v)
			mu.Unlock()
			return nil, nil
		}).
		SetEntryPoint(nodeA).
		SetFinishPoint(nodeB).
		Compile()
	require.NoError(t, err)

	saver := newMockSaver()
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: lineageID}
	agent.WithStreamMode(agent.StreamModeCheckpoints)(&inv.RunOptions)

	ctx, interrupt := WithGraphInterrupt(context.Background())
	ch, err := exec.Execute(ctx, State{}, inv)
	require.NoError(t, err)

	<-started
	interrupt()
	close(unblock)

	for range ch {
	}

	mu.Lock()
	require.Empty(t, values)
	mu.Unlock()

	intTuple := latestInterruptTuple(t, saver, lineageID)
	require.Equal(t, []string{nodeB, nodeB}, intTuple.Checkpoint.NextNodes)

	resumeInv := &agent.Invocation{InvocationID: lineageID}
	agent.WithStreamMode(agent.StreamModeCheckpoints)(&resumeInv.RunOptions)
	resumeState := State{
		CfgKeyCheckpointID: intTuple.Checkpoint.ID,
		CfgKeyLineageID:    lineageID,
	}
	ch2, err := exec.Execute(context.Background(), resumeState, resumeInv)
	require.NoError(t, err)
	for range ch2 {
	}

	mu.Lock()
	require.Len(t, values, 2)
	counts := make(map[int]int)
	for _, v := range values {
		counts[v]++
	}
	mu.Unlock()
	require.Equal(t, 1, counts[1])
	require.Equal(t, 1, counts[2])
}

func TestExecutor_ExternalInterruptTimeout_PreservesFanOutTasks(
	t *testing.T,
) {
	const lineageID = "ln-external-interrupt-fanout-timeout"
	const nodeA = "a"
	const nodeB = "b"
	const key = "v"
	const timeout = 20 * time.Millisecond

	v2Started := make(chan struct{}, 2)
	v1Done := make(chan struct{}, 1)
	allowV2Complete := make(chan struct{})

	g, err := NewStateGraph(NewStateSchema()).
		AddNode(nodeA, func(ctx context.Context, state State) (any, error) {
			return []*Command{
				{GoTo: nodeB, Update: State{key: 1}},
				{GoTo: nodeB, Update: State{key: 2}},
			}, nil
		}).
		AddNode(nodeB, func(ctx context.Context, state State) (any, error) {
			v, _ := state[key].(int)
			if v == 2 {
				select {
				case v2Started <- struct{}{}:
				default:
				}
				select {
				case <-allowV2Complete:
					return nil, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if v == 1 {
				select {
				case v1Done <- struct{}{}:
				default:
				}
			}
			return nil, nil
		}).
		SetEntryPoint(nodeA).
		SetFinishPoint(nodeB).
		Compile()
	require.NoError(t, err)

	saver := newMockSaver()
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: lineageID}
	agent.WithStreamMode(agent.StreamModeCheckpoints)(&inv.RunOptions)

	ctx, interrupt := WithGraphInterrupt(context.Background())
	ch, err := exec.Execute(ctx, State{}, inv)
	require.NoError(t, err)

	<-v2Started
	<-v1Done
	interrupt(WithGraphInterruptTimeout(timeout))

	for range ch {
	}

	intTuple := latestInterruptTuple(t, saver, lineageID)
	require.Contains(t, intTuple.Checkpoint.NextNodes, nodeB)

	close(allowV2Complete)
	resumeInv := &agent.Invocation{InvocationID: lineageID}
	agent.WithStreamMode(agent.StreamModeCheckpoints)(&resumeInv.RunOptions)
	resumeState := State{
		CfgKeyCheckpointID: intTuple.Checkpoint.ID,
		CfgKeyLineageID:    lineageID,
	}
	ch2, err := exec.Execute(context.Background(), resumeState, resumeInv)
	require.NoError(t, err)
	for range ch2 {
	}

	select {
	case <-v2Started:
	default:
		t.Fatal("expected v2 to rerun after resume")
	}
}

func TestExecutor_ApplyGraphInterruptInputs_MapState(t *testing.T) {
	const nodeID = "node-a"
	const key = "k"

	exec := &Executor{}
	restored := make(State)
	tuple := &CheckpointTuple{
		Metadata: &CheckpointMetadata{
			Extra: map[string]any{
				CheckpointMetaKeyGraphInterruptInputs: map[string]State{
					nodeID: {key: "v"},
					"":     {key: "skip-empty-node"},
					"b":    nil,
				},
			},
		},
	}
	exec.applyGraphInterruptInputs(restored, tuple)

	raw, ok := restored[StateKeyGraphInterruptInputs]
	require.True(t, ok)

	m, ok := raw.(map[string]State)
	require.True(t, ok)
	require.Len(t, m, 1)
	require.Contains(t, m, nodeID)
}

func TestExecutor_ApplyGraphInterruptInputs_MapAny(t *testing.T) {
	const nodeID = "node-a"
	const key = "k"

	exec := &Executor{}
	restored := make(State)
	tuple := &CheckpointTuple{
		Metadata: &CheckpointMetadata{
			Extra: map[string]any{
				CheckpointMetaKeyGraphInterruptInputs: map[string]any{
					nodeID: State{key: "v"},
					"":     State{key: "skip-empty-node"},
					"b":    nil,
				},
			},
		},
	}
	exec.applyGraphInterruptInputs(restored, tuple)

	raw, ok := restored[StateKeyGraphInterruptInputs]
	require.True(t, ok)

	m, ok := raw.(map[string]any)
	require.True(t, ok)
	require.Len(t, m, 1)
	require.Contains(t, m, nodeID)
}

func TestExecutor_ApplyGraphInterruptInputs_MapStateSlice(t *testing.T) {
	const nodeID = "node-a"
	const key = "k"

	exec := &Executor{}
	restored := make(State)
	tuple := &CheckpointTuple{
		Metadata: &CheckpointMetadata{
			Extra: map[string]any{
				CheckpointMetaKeyGraphInterruptInputs: map[string][]State{
					nodeID: {
						{key: "v"},
						nil,
					},
					"": {
						{key: "skip-empty-node"},
					},
					"b": nil,
				},
			},
		},
	}
	exec.applyGraphInterruptInputs(restored, tuple)

	raw, ok := restored[StateKeyGraphInterruptInputs]
	require.True(t, ok)

	m, ok := raw.(map[string]any)
	require.True(t, ok)
	require.Len(t, m, 1)

	rawInputs, ok := m[nodeID]
	require.True(t, ok)
	inputs, ok := rawInputs.([]any)
	require.True(t, ok)
	require.Len(t, inputs, 1)

	st, ok := inputs[0].(State)
	require.True(t, ok)
	require.Equal(t, "v", st[key])
}

func TestExecutor_ApplyGraphInterruptInputs_IgnoresInvalidTuples(t *testing.T) {
	exec := &Executor{}
	exec.applyGraphInterruptInputs(nil, nil)

	restored := make(State)
	exec.applyGraphInterruptInputs(restored, nil)
	exec.applyGraphInterruptInputs(restored, &CheckpointTuple{})
	exec.applyGraphInterruptInputs(restored, &CheckpointTuple{
		Metadata: &CheckpointMetadata{},
	})
	exec.applyGraphInterruptInputs(restored, &CheckpointTuple{
		Metadata: &CheckpointMetadata{
			Extra: map[string]any{},
		},
	})
	require.Empty(t, restored)
}

func TestGraphInterruptInputCopyHelpers_EmptyInputs(t *testing.T) {
	require.Nil(t, copyGraphInterruptInputsState(nil))
	require.Nil(t, copyGraphInterruptInputsState(map[string]State{}))
	require.Nil(t, copyGraphInterruptInputsAny(nil))
	require.Nil(t, copyGraphInterruptInputsAny(map[string]any{}))
	require.Nil(t, copyGraphInterruptInputsAnySlice(nil))
	require.Nil(t, copyGraphInterruptInputsAnySlice(map[string][]any{}))
	require.Nil(t, copyGraphInterruptInputsStateSlice(nil))
	require.Nil(t, copyGraphInterruptInputsStateSlice(map[string][]State{}))
}

func TestGraphInterruptConsumeStateHelpers(t *testing.T) {
	st := State{"k": "v"}

	_, ok := consumeStateFromStateSlice(nil)
	require.False(t, ok)
	_, ok = consumeStateFromStateSlice([]State{})
	require.False(t, ok)
	_, ok = consumeStateFromStateSlice([]State{nil})
	require.False(t, ok)
	got, ok := consumeStateFromStateSlice([]State{st})
	require.True(t, ok)
	require.Equal(t, st, got)

	_, ok = consumeStateFromAnySlice(nil)
	require.False(t, ok)
	_, ok = consumeStateFromAnySlice([]any{})
	require.False(t, ok)
	_, ok = consumeStateFromAnySlice([]any{123})
	require.False(t, ok)
	got, ok = consumeStateFromAnySlice([]any{st})
	require.True(t, ok)
	require.Equal(t, st, got)
}

func TestGraphInterruptHelpers_NilSafeAndIdempotent(t *testing.T) {
	var watcher *externalInterruptWatcher
	watcher.stop()
	require.False(t, watcher.requested())
	require.False(t, watcher.forced(nil))

	var state *graphInterruptState
	require.False(t, state.requested())
	require.Nil(t, state.timeoutOrNil())
	require.Nil(t, state.doneCh())

	require.Nil(t, graphInterruptFromContext(nil))
	require.Nil(t, graphInterruptFromContext(context.Background()))

	wrongType := context.WithValue(
		context.Background(),
		graphInterruptKey{},
		"bad",
	)
	require.Nil(t, graphInterruptFromContext(wrongType))

	opt := WithGraphInterruptTimeout(time.Second)
	opt(nil)

	ctx, interrupt := WithGraphInterrupt(context.Background())
	st := graphInterruptFromContext(ctx)
	require.NotNil(t, st)

	require.False(t, st.requested())
	select {
	case <-st.doneCh():
		t.Fatal("unexpected interrupt request")
	default:
	}

	interrupt(nil)
	require.True(t, st.requested())
	select {
	case <-st.doneCh():
	default:
		t.Fatal("expected interrupt request")
	}
	require.Nil(t, st.timeoutOrNil())

	interrupt(WithGraphInterruptTimeout(time.Second))
	require.Nil(t, st.timeoutOrNil())
}

func TestExternalInterruptWatcher_StopBeforeInterrupt(t *testing.T) {
	const timeout = 5 * time.Millisecond

	ctx, interrupt := WithGraphInterrupt(context.Background())
	state := graphInterruptFromContext(ctx)
	require.NotNil(t, state)

	runCtx, watcher := newExternalInterruptWatcher(ctx, state)
	require.NotNil(t, watcher)
	watcher.stop()

	interrupt(WithGraphInterruptTimeout(timeout))

	select {
	case <-runCtx.Done():
		t.Fatalf("unexpected cancellation: %v", context.Cause(runCtx))
	case <-time.After(20 * time.Millisecond):
	}
}

func TestExternalInterruptWatcher_ZeroTimeoutCancelsImmediately(t *testing.T) {
	ctx, interrupt := WithGraphInterrupt(context.Background())
	state := graphInterruptFromContext(ctx)
	require.NotNil(t, state)

	runCtx, watcher := newExternalInterruptWatcher(ctx, state)
	require.NotNil(t, watcher)
	defer watcher.stop()

	interrupt(WithGraphInterruptTimeout(0))

	select {
	case <-runCtx.Done():
		require.ErrorIs(t, context.Cause(runCtx), errGraphInterruptTimeout)
		require.True(t, watcher.forced(runCtx))
	case <-time.After(time.Second):
		t.Fatal("expected cancellation")
	}
}

func TestExternalInterruptWatcher_StopAfterInterrupt(t *testing.T) {
	const timeout = time.Second

	ctx, interrupt := WithGraphInterrupt(context.Background())
	state := graphInterruptFromContext(ctx)
	require.NotNil(t, state)

	runCtx, watcher := newExternalInterruptWatcher(ctx, state)
	require.NotNil(t, watcher)
	defer watcher.stop()

	interrupt(WithGraphInterruptTimeout(timeout))
	watcher.stop()

	select {
	case <-runCtx.Done():
		t.Fatalf("unexpected cancellation: %v", context.Cause(runCtx))
	case <-time.After(20 * time.Millisecond):
	}
}

func TestExecutor_CreateTask_ConsumesGraphInterruptInputs(t *testing.T) {
	const nodeA = "a"
	const key = "k"
	const value = "v"
	const v1 = 1
	const v2 = 2

	g, err := NewStateGraph(NewStateSchema()).
		AddNode(nodeA, func(ctx context.Context, state State) (any, error) {
			return nil, nil
		}).
		SetEntryPoint(nodeA).
		SetFinishPoint(nodeA).
		Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	stateMap := State{
		StateKeyGraphInterruptInputs: map[string]State{
			nodeA: {key: value},
		},
	}
	task := exec.createTask(nodeA, stateMap, 0)
	require.NotNil(t, task)
	input, ok := task.Input.(State)
	require.True(t, ok)
	require.Equal(t, value, input[key])
	_, ok = stateMap[StateKeyGraphInterruptInputs]
	require.False(t, ok)

	stateAny := State{
		StateKeyGraphInterruptInputs: map[string]any{
			nodeA: map[string]any{
				key: value,
			},
		},
	}
	task = exec.createTask(nodeA, stateAny, 0)
	require.NotNil(t, task)
	input, ok = task.Input.(State)
	require.True(t, ok)
	require.Equal(t, value, input[key])
	_, ok = stateAny[StateKeyGraphInterruptInputs]
	require.False(t, ok)

	stateAnyState := State{
		StateKeyGraphInterruptInputs: map[string]any{
			nodeA: State{key: value},
		},
	}
	task = exec.createTask(nodeA, stateAnyState, 0)
	require.NotNil(t, task)
	input, ok = task.Input.(State)
	require.True(t, ok)
	require.Equal(t, value, input[key])
	_, ok = stateAnyState[StateKeyGraphInterruptInputs]
	require.False(t, ok)

	stateSlices := State{
		StateKeyGraphInterruptInputs: map[string][]State{
			nodeA: {
				{key: v1},
				{key: v2},
			},
		},
	}
	task = exec.createTask(nodeA, stateSlices, 0)
	require.NotNil(t, task)
	input, ok = task.Input.(State)
	require.True(t, ok)
	require.Equal(t, v1, input[key])

	task = exec.createTask(nodeA, stateSlices, 0)
	require.NotNil(t, task)
	input, ok = task.Input.(State)
	require.True(t, ok)
	require.Equal(t, v2, input[key])
	_, ok = stateSlices[StateKeyGraphInterruptInputs]
	require.False(t, ok)

	stateAnyValueSlices := State{
		StateKeyGraphInterruptInputs: map[string]any{
			nodeA: []State{
				{key: v1},
				{key: v2},
			},
		},
	}
	task = exec.createTask(nodeA, stateAnyValueSlices, 0)
	require.NotNil(t, task)
	input, ok = task.Input.(State)
	require.True(t, ok)
	require.Equal(t, v1, input[key])

	task = exec.createTask(nodeA, stateAnyValueSlices, 0)
	require.NotNil(t, task)
	input, ok = task.Input.(State)
	require.True(t, ok)
	require.Equal(t, v2, input[key])
	_, ok = stateAnyValueSlices[StateKeyGraphInterruptInputs]
	require.False(t, ok)

	stateAnyValueAnySlice := State{
		StateKeyGraphInterruptInputs: map[string]any{
			nodeA: []any{
				State{key: v1},
				map[string]any{key: v2},
			},
		},
	}
	task = exec.createTask(nodeA, stateAnyValueAnySlice, 0)
	require.NotNil(t, task)
	input, ok = task.Input.(State)
	require.True(t, ok)
	require.Equal(t, v1, input[key])

	task = exec.createTask(nodeA, stateAnyValueAnySlice, 0)
	require.NotNil(t, task)
	input, ok = task.Input.(State)
	require.True(t, ok)
	require.Equal(t, v2, input[key])
	_, ok = stateAnyValueAnySlice[StateKeyGraphInterruptInputs]
	require.False(t, ok)

	stateAnySlices := State{
		StateKeyGraphInterruptInputs: map[string][]any{
			nodeA: {
				State{key: v1},
				map[string]any{key: v2},
			},
		},
	}
	task = exec.createTask(nodeA, stateAnySlices, 0)
	require.NotNil(t, task)
	input, ok = task.Input.(State)
	require.True(t, ok)
	require.Equal(t, v1, input[key])

	task = exec.createTask(nodeA, stateAnySlices, 0)
	require.NotNil(t, task)
	input, ok = task.Input.(State)
	require.True(t, ok)
	require.Equal(t, v2, input[key])
	_, ok = stateAnySlices[StateKeyGraphInterruptInputs]
	require.False(t, ok)
}

func TestExecutor_CreateTask_DoesNotConsumeInvalidInterruptInputs(t *testing.T) {
	const nodeA = "a"
	const key = "k"

	g, err := NewStateGraph(NewStateSchema()).
		AddNode(nodeA, func(ctx context.Context, state State) (any, error) {
			return nil, nil
		}).
		SetEntryPoint(nodeA).
		SetFinishPoint(nodeA).
		Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	stateEmptySlices := State{
		StateKeyGraphInterruptInputs: map[string][]State{
			nodeA: {},
		},
		key: "v",
	}
	task := exec.createTask(nodeA, stateEmptySlices, 0)
	require.NotNil(t, task)
	_, ok := stateEmptySlices[StateKeyGraphInterruptInputs]
	require.True(t, ok)

	stateNilSlice := State{
		StateKeyGraphInterruptInputs: map[string][]State{
			nodeA: {nil},
		},
		key: "v",
	}
	task = exec.createTask(nodeA, stateNilSlice, 0)
	require.NotNil(t, task)
	_, ok = stateNilSlice[StateKeyGraphInterruptInputs]
	require.True(t, ok)

	stateAnyEmpty := State{
		StateKeyGraphInterruptInputs: map[string][]any{
			nodeA: {},
		},
		key: "v",
	}
	task = exec.createTask(nodeA, stateAnyEmpty, 0)
	require.NotNil(t, task)
	_, ok = stateAnyEmpty[StateKeyGraphInterruptInputs]
	require.True(t, ok)

	stateAnyBad := State{
		StateKeyGraphInterruptInputs: map[string][]any{
			nodeA: {123},
		},
		key: "v",
	}
	task = exec.createTask(nodeA, stateAnyBad, 0)
	require.NotNil(t, task)
	_, ok = stateAnyBad[StateKeyGraphInterruptInputs]
	require.True(t, ok)

	stateAnyValueBad := State{
		StateKeyGraphInterruptInputs: map[string]any{
			nodeA: "bad",
		},
		key: "v",
	}
	task = exec.createTask(nodeA, stateAnyValueBad, 0)
	require.NotNil(t, task)
	_, ok = stateAnyValueBad[StateKeyGraphInterruptInputs]
	require.True(t, ok)

	stateAnyValueNil := State{
		StateKeyGraphInterruptInputs: map[string]any{
			nodeA: nil,
		},
		key: "v",
	}
	task = exec.createTask(nodeA, stateAnyValueNil, 0)
	require.NotNil(t, task)
	_, ok = stateAnyValueNil[StateKeyGraphInterruptInputs]
	require.True(t, ok)

	stateAnyValueNilState := State{
		StateKeyGraphInterruptInputs: map[string]any{
			nodeA: State(nil),
		},
		key: "v",
	}
	task = exec.createTask(nodeA, stateAnyValueNilState, 0)
	require.NotNil(t, task)
	_, ok = stateAnyValueNilState[StateKeyGraphInterruptInputs]
	require.True(t, ok)

	stateAnyValueNilMap := State{
		StateKeyGraphInterruptInputs: map[string]any{
			nodeA: map[string]any(nil),
		},
		key: "v",
	}
	task = exec.createTask(nodeA, stateAnyValueNilMap, 0)
	require.NotNil(t, task)
	_, ok = stateAnyValueNilMap[StateKeyGraphInterruptInputs]
	require.True(t, ok)

	stateAnyValueEmptySlice := State{
		StateKeyGraphInterruptInputs: map[string]any{
			nodeA: []any{},
		},
		key: "v",
	}
	task = exec.createTask(nodeA, stateAnyValueEmptySlice, 0)
	require.NotNil(t, task)
	_, ok = stateAnyValueEmptySlice[StateKeyGraphInterruptInputs]
	require.True(t, ok)

	stateAnyValueBadSlice := State{
		StateKeyGraphInterruptInputs: map[string]any{
			nodeA: []any{123},
		},
		key: "v",
	}
	task = exec.createTask(nodeA, stateAnyValueBadSlice, 0)
	require.NotNil(t, task)
	_, ok = stateAnyValueBadSlice[StateKeyGraphInterruptInputs]
	require.True(t, ok)

	stateAnyValueBadStateSlice := State{
		StateKeyGraphInterruptInputs: map[string]any{
			nodeA: []State{nil},
		},
		key: "v",
	}
	task = exec.createTask(nodeA, stateAnyValueBadStateSlice, 0)
	require.NotNil(t, task)
	_, ok = stateAnyValueBadStateSlice[StateKeyGraphInterruptInputs]
	require.True(t, ok)

	stateUnknown := State{
		StateKeyGraphInterruptInputs: 123,
		key:                          "v",
	}
	task = exec.createTask(nodeA, stateUnknown, 0)
	require.NotNil(t, task)
	_, ok = stateUnknown[StateKeyGraphInterruptInputs]
	require.True(t, ok)
}

func TestGraphInterruptStateHelpers(t *testing.T) {
	const nodeA = "a"
	const nodeB = "b"
	const key = "k"

	require.Nil(t, (&Executor{}).stateFields())
	require.Nil(t, (&Executor{graph: &Graph{}}).stateFields())

	g, err := NewStateGraph(NewStateSchema()).
		AddNode(nodeA, func(ctx context.Context, state State) (any, error) {
			return nil, nil
		}).
		SetEntryPoint(nodeA).
		SetFinishPoint(nodeA).
		Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)
	require.NotNil(t, exec.stateFields())

	st := State{key: "v"}
	_, ok := stateFromAny(nil)
	require.False(t, ok)
	_, ok = stateFromAny(1)
	require.False(t, ok)

	got, ok := stateFromAny(st)
	require.True(t, ok)
	require.Equal(t, st, got)

	got, ok = stateFromAny(map[string]any{key: "v"})
	require.True(t, ok)
	require.Equal(t, "v", got[key])

	next := nextNodesFromTasks([]*Task{
		nil,
		{NodeID: End},
		{NodeID: nodeA},
		{NodeID: nodeB},
	})
	require.Equal(t, []string{nodeA, nodeB}, next)
	require.Nil(t, nextNodesFromTasks(nil))

	meta := exec.metaExtraForPlannedExternalInterrupt([]*Task{
		nil,
		{NodeID: ""},
		{NodeID: nodeA, Input: State{key: "v"}},
		{NodeID: nodeB, Input: 123},
	})
	require.NotNil(t, meta)
	raw := meta[CheckpointMetaKeyGraphInterruptInputs]
	require.NotNil(t, raw)

	inputs, ok := raw.(map[string][]any)
	require.True(t, ok)
	require.Len(t, inputs[nodeA], 1)
	require.Nil(t, exec.metaExtraForPlannedExternalInterrupt(nil))
}

func TestExecutor_MetaExtraForForcedInterrupt_FallbackToTaskInput(t *testing.T) {
	const nodeA = "a"
	const key = "k"

	g, err := NewStateGraph(NewStateSchema()).
		AddNode(nodeA, func(ctx context.Context, state State) (any, error) {
			return nil, nil
		}).
		SetEntryPoint(nodeA).
		SetFinishPoint(nodeA).
		Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	report := newStepExecutionReport(nil)
	task := &Task{NodeID: nodeA, Input: State{key: "v"}}
	meta := exec.metaExtraForForcedInterrupt(report, []*Task{task})
	require.NotNil(t, meta)

	raw := meta[CheckpointMetaKeyGraphInterruptInputs]
	require.NotNil(t, raw)
	m, ok := raw.(map[string][]any)
	require.True(t, ok)
	require.Len(t, m[nodeA], 1)

	meta = exec.metaExtraForForcedInterrupt(
		report,
		[]*Task{{NodeID: nodeA, Input: 123}},
	)
	require.Nil(t, meta)
}

func TestExecutor_PlanTasksForBspStep_ResumedStartStep(t *testing.T) {
	const nodeA = "a"

	g, err := NewStateGraph(NewStateSchema()).
		AddNode(nodeA, func(ctx context.Context, state State) (any, error) {
			return nil, nil
		}).
		SetEntryPoint(nodeA).
		SetFinishPoint(nodeA).
		Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	execCtx := &ExecutionContext{
		resumed: true,
	}
	tasks, err := exec.planTasksForBspStep(
		context.Background(),
		nil,
		execCtx,
		1,
		0,
	)
	require.NoError(t, err)
	require.Empty(t, tasks)
}

func TestExecutor_PlanTasksForBspStep_WrapsPlanErrors(t *testing.T) {
	exec := &Executor{
		graph: &Graph{},
	}
	execCtx := &ExecutionContext{
		InvocationID: "inv",
		State:        make(State),
	}
	_, err := exec.planTasksForBspStep(
		context.Background(),
		nil,
		execCtx,
		0,
		0,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "planning failed at step")
}

func TestExecutor_ShouldForceExternalInterrupt(t *testing.T) {
	exec := &Executor{}
	watcher := &externalInterruptWatcher{}

	require.False(t, exec.shouldForceExternalInterrupt(
		nil,
		context.Background(),
		context.Canceled,
	))
	require.False(t, exec.shouldForceExternalInterrupt(
		watcher,
		context.Background(),
		context.Canceled,
	))

	forcedCtx, cancel := context.WithCancelCause(context.Background())
	cancel(errGraphInterruptTimeout)

	require.False(t, exec.shouldForceExternalInterrupt(
		watcher,
		forcedCtx,
		nil,
	))
	require.True(t, exec.shouldForceExternalInterrupt(
		watcher,
		forcedCtx,
		context.Canceled,
	))
	require.True(t, exec.shouldForceExternalInterrupt(
		watcher,
		forcedCtx,
		context.DeadlineExceeded,
	))
	require.False(t, exec.shouldForceExternalInterrupt(
		watcher,
		forcedCtx,
		errors.New("other"),
	))
}

func TestExecutor_ForcedInterruptHelpers(t *testing.T) {
	const nodeA = "a"
	const nodeB = "b"

	g, err := NewStateGraph(NewStateSchema()).
		AddNode(nodeA, func(ctx context.Context, state State) (any, error) {
			return nil, nil
		}).
		AddNode(nodeB, func(ctx context.Context, state State) (any, error) {
			return nil, nil
		}).
		AddEdge(nodeA, nodeB).
		SetEntryPoint(nodeA).
		SetFinishPoint(nodeB).
		Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	taskA := &Task{NodeID: nodeA}
	taskB := &Task{NodeID: nodeB}
	tasks := []*Task{
		nil,
		{NodeID: ""},
		taskA,
		taskB,
	}
	report := newStepExecutionReport(nil)
	report.recordInput(taskB, State{"x": "y"})
	report.markCompleted(taskA)

	rerun := exec.tasksToRerun(tasks, report)
	require.NotContains(t, rerun, taskA)
	require.Contains(t, rerun, taskB)

	require.Nil(t, exec.metaExtraForForcedInterrupt(nil, rerun))
	require.Nil(t, exec.metaExtraForForcedInterrupt(report, nil))

	metaExtra := exec.metaExtraForForcedInterrupt(report, rerun)
	require.NotNil(t, metaExtra)
	raw := metaExtra[CheckpointMetaKeyGraphInterruptInputs]
	_, ok := raw.(map[string][]any)
	require.True(t, ok)
	require.Nil(t, exec.metaExtraForForcedInterrupt(
		report,
		[]*Task{{NodeID: "missing"}},
	))

	execCtx := &ExecutionContext{
		channels: exec.buildChannelManager(),
	}
	branchB := ChannelBranchPrefix + nodeB
	branchEnd := ChannelBranchPrefix + End

	ch, ok := execCtx.channels.GetChannel(branchB)
	require.True(t, ok)
	require.True(t, ch.Update([]any{channelUpdateMarker}, 0))

	ch, ok = execCtx.channels.GetChannel(branchEnd)
	require.True(t, ok)
	require.True(t, ch.Update([]any{channelUpdateMarker}, 0))

	next := exec.nextNodesForForcedInterrupt(execCtx, rerun)
	require.Contains(t, next, nodeB)
	require.NotContains(t, next, End)
}

func TestStepExecutionReport_RecordsAndSkipsInputs(t *testing.T) {
	const nodeID = "a"

	var report *stepExecutionReport
	task := &Task{NodeID: nodeID}
	report.recordInput(task, State{})
	report.markCompleted(task)
	require.False(t, report.isCompleted(task))
	_, ok := report.inputFor(task)
	require.False(t, ok)

	report = newStepExecutionReport(nil)
	report.recordInput(&Task{NodeID: ""}, State{})
	report.recordInput(task, nil)

	orig := State{"x": "y"}
	report.recordInput(task, orig)
	report.recordInput(task, State{"x": "ignored"})

	in, ok := report.inputFor(&Task{NodeID: "missing"})
	require.False(t, ok)
	require.Nil(t, in)

	in, ok = report.inputFor(task)
	require.True(t, ok)
	require.Equal(t, "y", in["x"])

	report.markCompleted(&Task{NodeID: ""})
	report.markCompleted(task)
	require.True(t, report.isCompleted(task))
}

func latestInterruptTuple(
	t *testing.T,
	saver *mockSaver,
	lineageID string,
) *CheckpointTuple {
	t.Helper()

	var latest *CheckpointTuple
	var latestTime time.Time

	for key, tuple := range saver.byID {
		if !strings.HasPrefix(key, lineageID+":") {
			continue
		}
		if tuple == nil || tuple.Metadata == nil || tuple.Checkpoint == nil {
			continue
		}
		if tuple.Metadata.Source != CheckpointSourceInterrupt {
			continue
		}
		if latest == nil || tuple.Checkpoint.Timestamp.After(latestTime) {
			latest = tuple
			latestTime = tuple.Checkpoint.Timestamp
		}
	}

	require.NotNil(t, latest)
	return latest
}

func TestExecutor_StaticInterruptBefore_ResumeRunsNodeOnce(t *testing.T) {
	const lineageID = "ln-static-before"

	var aRuns int
	var bRuns int

	g, err := NewStateGraph(NewStateSchema()).
		AddNode("a", func(ctx context.Context, state State) (any, error) {
			aRuns++
			return nil, nil
		}).
		AddNode(
			"b",
			func(ctx context.Context, state State) (any, error) {
				bRuns++
				return nil, nil
			},
			WithInterruptBefore(),
		).
		SetEntryPoint("a").
		AddEdge("a", "b").
		SetFinishPoint("b").
		Compile()
	require.NoError(t, err)

	saver := newMockSaver()
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	run1 := State{CfgKeyLineageID: lineageID}
	ch, err := exec.Execute(
		context.Background(),
		run1,
		&agent.Invocation{InvocationID: "inv-static-before-1"},
	)
	require.NoError(t, err)
	for range ch {
	}

	require.Equal(t, 1, aRuns)
	require.Equal(t, 0, bRuns)

	intTuple := latestInterruptTuple(t, saver, lineageID)
	require.Equal(t, "b", intTuple.Checkpoint.InterruptState.NodeID)
	require.Equal(t, []string{"b"}, intTuple.Checkpoint.NextNodes)

	raw, ok := intTuple.Checkpoint.ChannelValues[StateKeyStaticInterruptSkips]
	require.True(t, ok)
	skips, ok := raw.(map[string]any)
	require.True(t, ok)
	_, ok = skips["b"]
	require.True(t, ok)

	run2 := State{
		CfgKeyLineageID:    lineageID,
		CfgKeyCheckpointID: intTuple.Checkpoint.ID,
	}
	ch, err = exec.Execute(
		context.Background(),
		run2,
		&agent.Invocation{InvocationID: "inv-static-before-2"},
	)
	require.NoError(t, err)
	for range ch {
	}

	require.Equal(t, 1, aRuns)
	require.Equal(t, 1, bRuns)
}

func TestExecutor_StaticInterruptAfter_ResumeSkipsRerun(t *testing.T) {
	const lineageID = "ln-static-after"

	var aRuns int
	var bRuns int
	var cRuns int

	g, err := NewStateGraph(NewStateSchema()).
		AddNode("a", func(ctx context.Context, state State) (any, error) {
			aRuns++
			return nil, nil
		}).
		AddNode("b", func(ctx context.Context, state State) (any, error) {
			bRuns++
			return nil, nil
		}, WithInterruptAfter()).
		AddNode("c", func(ctx context.Context, state State) (any, error) {
			cRuns++
			return nil, nil
		}).
		SetEntryPoint("a").
		AddEdge("a", "b").
		AddEdge("b", "c").
		SetFinishPoint("c").
		Compile()
	require.NoError(t, err)

	saver := newMockSaver()
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	run1 := State{CfgKeyLineageID: lineageID}
	ch, err := exec.Execute(
		context.Background(),
		run1,
		&agent.Invocation{InvocationID: "inv-static-after-1"},
	)
	require.NoError(t, err)
	for range ch {
	}

	require.Equal(t, 1, aRuns)
	require.Equal(t, 1, bRuns)
	require.Equal(t, 0, cRuns)

	intTuple := latestInterruptTuple(t, saver, lineageID)
	require.Equal(t, "b", intTuple.Checkpoint.InterruptState.NodeID)
	require.Equal(t, []string{"c"}, intTuple.Checkpoint.NextNodes)

	run2 := State{
		CfgKeyLineageID:    lineageID,
		CfgKeyCheckpointID: intTuple.Checkpoint.ID,
	}
	ch, err = exec.Execute(
		context.Background(),
		run2,
		&agent.Invocation{InvocationID: "inv-static-after-2"},
	)
	require.NoError(t, err)
	for range ch {
	}

	require.Equal(t, 1, aRuns)
	require.Equal(t, 1, bRuns)
	require.Equal(t, 1, cRuns)
}

func TestExecutor_StaticInterruptAfterThenBefore_Chains(t *testing.T) {
	const lineageID = "ln-static-chain"

	var aRuns int
	var bRuns int
	var cRuns int

	g, err := NewStateGraph(NewStateSchema()).
		AddNode("a", func(ctx context.Context, state State) (any, error) {
			aRuns++
			return nil, nil
		}).
		AddNode("b", func(ctx context.Context, state State) (any, error) {
			bRuns++
			return nil, nil
		}, WithInterruptAfter()).
		AddNode("c", func(ctx context.Context, state State) (any, error) {
			cRuns++
			return nil, nil
		}, WithInterruptBefore()).
		SetEntryPoint("a").
		AddEdge("a", "b").
		AddEdge("b", "c").
		SetFinishPoint("c").
		Compile()
	require.NoError(t, err)

	saver := newMockSaver()
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	run1 := State{CfgKeyLineageID: lineageID}
	ch, err := exec.Execute(
		context.Background(),
		run1,
		&agent.Invocation{InvocationID: "inv-static-chain-1"},
	)
	require.NoError(t, err)
	for range ch {
	}

	require.Equal(t, 1, aRuns)
	require.Equal(t, 1, bRuns)
	require.Equal(t, 0, cRuns)

	int1 := latestInterruptTuple(t, saver, lineageID)
	require.Equal(t, "b", int1.Checkpoint.InterruptState.NodeID)

	run2 := State{
		CfgKeyLineageID:    lineageID,
		CfgKeyCheckpointID: int1.Checkpoint.ID,
	}
	ch, err = exec.Execute(
		context.Background(),
		run2,
		&agent.Invocation{InvocationID: "inv-static-chain-2"},
	)
	require.NoError(t, err)
	for range ch {
	}

	require.Equal(t, 1, aRuns)
	require.Equal(t, 1, bRuns)
	require.Equal(t, 0, cRuns)

	int2 := latestInterruptTuple(t, saver, lineageID)
	require.Equal(t, "c", int2.Checkpoint.InterruptState.NodeID)

	run3 := State{
		CfgKeyLineageID:    lineageID,
		CfgKeyCheckpointID: int2.Checkpoint.ID,
	}
	ch, err = exec.Execute(
		context.Background(),
		run3,
		&agent.Invocation{InvocationID: "inv-static-chain-3"},
	)
	require.NoError(t, err)
	for range ch {
	}

	require.Equal(t, 1, aRuns)
	require.Equal(t, 1, bRuns)
	require.Equal(t, 1, cRuns)
}

func TestExecutor_StaticInterruptBefore_ParallelStep(t *testing.T) {
	const lineageID = "ln-static-parallel"

	var rootRuns int
	var bRuns int
	var cRuns int

	g, err := NewStateGraph(NewStateSchema()).
		AddNode("root", func(ctx context.Context, state State) (any, error) {
			rootRuns++
			return nil, nil
		}).
		AddNode("b", func(ctx context.Context, state State) (any, error) {
			bRuns++
			return nil, nil
		}, WithInterruptBefore()).
		AddNode("c", func(ctx context.Context, state State) (any, error) {
			cRuns++
			return nil, nil
		}, WithInterruptBefore()).
		SetEntryPoint("root").
		AddEdge("root", "b").
		AddEdge("root", "c").
		SetFinishPoint("b").
		SetFinishPoint("c").
		Compile()
	require.NoError(t, err)

	saver := newMockSaver()
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	run1 := State{CfgKeyLineageID: lineageID}
	ch, err := exec.Execute(
		context.Background(),
		run1,
		&agent.Invocation{InvocationID: "inv-static-parallel-1"},
	)
	require.NoError(t, err)
	for range ch {
	}

	require.Equal(t, 1, rootRuns)
	require.Equal(t, 0, bRuns)
	require.Equal(t, 0, cRuns)

	int1 := latestInterruptTuple(t, saver, lineageID)
	require.ElementsMatch(t, []string{"b", "c"}, int1.Checkpoint.NextNodes)

	run2 := State{
		CfgKeyLineageID:    lineageID,
		CfgKeyCheckpointID: int1.Checkpoint.ID,
	}
	ch, err = exec.Execute(
		context.Background(),
		run2,
		&agent.Invocation{InvocationID: "inv-static-parallel-2"},
	)
	require.NoError(t, err)
	for range ch {
	}

	require.Equal(t, 1, rootRuns)
	require.Equal(t, 1, bRuns)
	require.Equal(t, 1, cRuns)
}

func TestExecutor_VersionBasedPlanning(t *testing.T) {
	g, err := NewStateGraph(NewStateSchema()).
		AddNode("a", func(ctx context.Context, state State) (any, error) { return nil, nil }).
		AddNode("b", func(ctx context.Context, state State) (any, error) { return nil, nil }).
		SetEntryPoint("a").
		AddEdge("a", "b").
		SetFinishPoint("b").
		Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	// Build execution context as resumed with a last checkpoint
	last := &Checkpoint{VersionsSeen: map[string]map[string]int64{"b": {}}}
	ec := exec.buildExecutionContext(make(chan *event.Event, 1), "inv-pln", State{}, true, last)

	// Make trigger channel available and version > seen
	channels := ec.channels.GetAllChannels()
	for name, ch := range channels {
		if strings.HasPrefix(name, "branch:to:b") {
			ch.Update([]any{"x"}, 1) // Version becomes 1
			ec.lastCheckpoint.VersionsSeen["b"][name] = 0
		}
	}

	tasks := exec.planBasedOnChannelTriggers(ec, 1)
	require.GreaterOrEqual(t, len(tasks), 1)
}

// Minimal test to trigger emitNodeErrorEvent path
func TestExecutor_EmitNodeErrorEvent(t *testing.T) {
	b := NewStateGraph(NewStateSchema())
	// Node always returns error
	boom := func(ctx context.Context, state State) (any, error) { return nil, errors.New("boom") }
	g, err := b.AddNode("boom", boom).SetEntryPoint("boom").SetFinishPoint("boom").Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	ch, err := exec.Execute(context.Background(), State{}, &agent.Invocation{InvocationID: "inv-boom"})
	require.NoError(t, err)
	// Drain channel until closed
	for range ch {
		// ignore; event emission path is covered by execution
	}
}

func TestExecutor_GetNextChannelsInStep_And_ClearMarks_And_UpdateVersionsSeen(t *testing.T) {
	g := New(NewStateSchema())
	// add a channel and mark updated at step 5
	g.addChannel("branch:to:x", ichannel.BehaviorLastValue)
	c, ok := g.getChannel("branch:to:x")
	require.True(t, ok)
	c.Update([]any{"v"}, 5)

	exec := &Executor{graph: g}
	// Build execution context so per-run channels are created from definitions.
	ec := exec.buildExecutionContext(nil, "inv", State{}, false, nil)
	// Mark the corresponding per-run channel as updated in step 5.
	perRunCh, ok2 := ec.channels.GetChannel("branch:to:x")
	require.True(t, ok2)
	perRunCh.Update([]any{"v"}, 5)

	// getNextChannelsInStep should include our channel
	got := exec.getNextChannelsInStep(ec, 5)
	require.Contains(t, got, "branch:to:x")
	// clear marks
	exec.clearChannelStepMarks(ec)
	require.False(t, perRunCh.IsUpdatedInStep(5))
	require.False(t, perRunCh.IsUpdatedInStep(0))
	require.Equal(t, ichannel.StepUnmarked, perRunCh.LastUpdatedStep)

	// updateVersionsSeen should record current version for triggers
	exec.updateVersionsSeen(ec, "nodeA", []string{"branch:to:x"})
	require.Equal(t, perRunCh.Version, ec.versionsSeen["nodeA"]["branch:to:x"])
}

func TestExecutor_ProcessChannelWrites_SetsStepMark(t *testing.T) {
	const (
		channelName = "branch:to:x"
		step        = 7
	)

	g := New(NewStateSchema())
	g.addChannel(channelName, ichannel.BehaviorLastValue)
	exec := &Executor{graph: g}
	ec := exec.buildExecutionContext(nil, "inv", State{}, false, nil)

	exec.processChannelWrites(
		context.Background(),
		nil,
		ec,
		"task",
		[]channelWriteEntry{{Channel: channelName, Value: "v"}},
		step,
	)

	perRunCh, ok := ec.channels.GetChannel(channelName)
	require.True(t, ok)
	require.True(t, perRunCh.IsUpdatedInStep(step))

	checkpoint := exec.createCheckpointFromState(ec.State, step, ec)
	require.Contains(t, checkpoint.UpdatedChannels, channelName)
}

func TestExecutor_HandleCommandRouting_SetsStepMark(t *testing.T) {
	const (
		targetNode = "x"
		step       = 3
		taskID     = "task"
	)

	g := New(NewStateSchema())
	exec := &Executor{graph: g}
	ec := exec.buildExecutionContext(nil, "inv", State{}, false, nil)

	exec.handleCommandRouting(
		context.Background(),
		nil,
		ec,
		taskID,
		targetNode,
		step,
	)

	channelName := fmt.Sprintf("%s%s", ChannelTriggerPrefix, targetNode)
	defCh, ok := g.getChannel(channelName)
	require.True(t, ok)
	require.NotNil(t, defCh)
	require.Equal(t, ichannel.BehaviorLastValue, defCh.Behavior)

	perRunCh, ok := ec.channels.GetChannel(channelName)
	require.True(t, ok)
	require.True(t, perRunCh.IsUpdatedInStep(step))

	require.Len(t, ec.pendingWrites, 1)
	require.Equal(t, PendingWrite{
		Channel:  channelName,
		Value:    channelUpdateMarker,
		TaskID:   taskID,
		Sequence: 1,
	}, ec.pendingWrites[0])

	checkpoint := exec.createCheckpointFromState(ec.State, step, ec)
	require.Contains(t, checkpoint.UpdatedChannels, channelName)
}

func TestExecutor_ProcessConditionalResult_SetsStepMark(t *testing.T) {
	const (
		fromNode   = "a"
		targetNode = "b"
		resultKey  = "route"
		step       = 9
	)

	g := New(NewStateSchema())
	require.NoError(t, g.addNode(&Node{ID: fromNode}))
	require.NoError(t, g.addNode(&Node{ID: targetNode}))
	exec := &Executor{graph: g}
	ec := exec.buildExecutionContext(nil, "inv", State{}, false, nil)
	condEdge := &ConditionalEdge{
		From:    fromNode,
		PathMap: map[string]string{resultKey: targetNode},
	}

	err := exec.processConditionalResult(
		context.Background(),
		nil,
		ec,
		condEdge,
		resultKey,
		step,
	)
	require.NoError(t, err)

	channelName := fmt.Sprintf("%s%s", ChannelBranchPrefix, targetNode)
	perRunCh, ok := ec.channels.GetChannel(channelName)
	require.True(t, ok)
	require.True(t, perRunCh.IsUpdatedInStep(step))

	checkpoint := exec.createCheckpointFromState(ec.State, step, ec)
	require.Contains(t, checkpoint.UpdatedChannels, channelName)
}

// mock saver for createCheckpoint
type putMockSaver struct {
	called bool
	retErr error
}

func (m *putMockSaver) Get(ctx context.Context, config map[string]any) (*Checkpoint, error) {
	return nil, nil
}
func (m *putMockSaver) GetTuple(ctx context.Context, config map[string]any) (*CheckpointTuple, error) {
	return nil, nil
}
func (m *putMockSaver) List(ctx context.Context, config map[string]any, filter *CheckpointFilter) ([]*CheckpointTuple, error) {
	return nil, nil
}
func (m *putMockSaver) Put(ctx context.Context, req PutRequest) (map[string]any, error) {
	m.called = true
	return req.Config, m.retErr
}
func (m *putMockSaver) PutWrites(ctx context.Context, req PutWritesRequest) error { return nil }
func (m *putMockSaver) PutFull(ctx context.Context, req PutFullRequest) (map[string]any, error) {
	return req.Config, nil
}
func (m *putMockSaver) DeleteLineage(ctx context.Context, lineageID string) error { return nil }
func (m *putMockSaver) Close() error                                              { return nil }

// Verify restoreStateFromCheckpoint fills schema defaults and zero values
// for fields missing from the checkpoint.
func TestExecutor_RestoreStateFromCheckpoint_DefaultsAndZero(t *testing.T) {
	schema := NewStateSchema().
		AddField("opt", StateField{
			Type:    reflect.TypeOf(0),
			Default: func() any { return 42 },
			Reducer: DefaultReducer,
		}).
		AddField("names", StateField{
			Type:    reflect.TypeOf([]string{}),
			Reducer: StringSliceReducer,
		})
	g := New(schema)
	exec := &Executor{graph: g}

	tuple := &CheckpointTuple{Checkpoint: &Checkpoint{
		ID:            "ck",
		ChannelValues: map[string]any{"x": 1},
	}}

	st := exec.restoreStateFromCheckpoint(tuple)
	// Existing non-schema key remains.
	require.Equal(t, 1, st["x"])
	// Missing schema field with Default gets populated.
	require.Equal(t, 42, st["opt"])
	// Missing slice field present with zero (typed nil) value.
	v, ok := st["names"]
	require.True(t, ok)
	_, isSlice := v.([]string)
	require.True(t, isSlice)
}

func TestExecutor_RestoreStateFromCheckpoint_OneShotMessages(t *testing.T) {
	exec := &Executor{graph: New(MessagesStateSchema())}
	ckpt := &Checkpoint{
		ID: "ck",
		ChannelValues: map[string]any{
			StateKeyOneShotMessages: []model.Message{
				model.NewUserMessage("hi"),
			},
		},
	}
	tuple := &CheckpointTuple{Checkpoint: ckpt.Copy()}

	st := exec.restoreStateFromCheckpoint(tuple)
	raw, ok := st[StateKeyOneShotMessages]
	require.True(t, ok)

	msgs, ok := raw.([]model.Message)
	require.True(t, ok)
	require.Len(t, msgs, 1)
	require.Equal(t, model.RoleUser, msgs[0].Role)
	require.Equal(t, "hi", msgs[0].Content)
}

func TestExecutor_HelperMethods(t *testing.T) {
	exec := &Executor{graph: New(NewStateSchema())}
	// getConfigKeys
	keys := getConfigKeys(map[string]any{"a": 1, "b": 2})
	require.Len(t, keys, 2)
	// CheckpointManager getter
	require.Nil(t, exec.CheckpointManager())
	exec.checkpointManager = NewCheckpointManager(nil)
	require.NotNil(t, exec.CheckpointManager())
}

func TestExecutor_RestoreCheckpointValueWithSchema(t *testing.T) {
	exec := &Executor{graph: New(NewStateSchema())}
	field := StateField{Type: reflect.TypeOf([]string{}), Default: func() any { return []string{} }}
	v := exec.restoreCheckpointValueWithSchema([]any{"a", "b"}, field)
	s, ok := v.([]string)
	require.True(t, ok)
	require.Equal(t, []string{"a", "b"}, s)
}

func TestExecutor_ProcessResumeCommand_And_ApplyExecutableNextNodes(t *testing.T) {
	exec := &Executor{graph: New(NewStateSchema())}
	// processResumeCommand
	init := State{StateKeyCommand: &Command{Resume: "v", ResumeMap: map[string]any{"t": 1}}}
	out := exec.processResumeCommand(make(State), init)
	require.Equal(t, "v", out[ResumeChannel])
	require.NotNil(t, out[StateKeyResumeMap])
	init2 := State{
		StateKeyCommand: NewResumeCommand().
			WithResume("v2").
			AddResumeValue("t", 2),
	}
	out2 := exec.processResumeCommand(make(State), init2)
	require.Equal(t, "v2", out2[ResumeChannel])
	require.Equal(t, 2, out2[StateKeyResumeMap].(map[string]any)["t"])
	require.NotContains(t, out2, StateKeyCommand)
	// applyExecutableNextNodes (pendingWrites empty and NextNodes has A)
	tuple := &CheckpointTuple{Checkpoint: &Checkpoint{NextNodes: []string{"A", End, ""}}}
	restored := make(State)
	exec.applyExecutableNextNodes(restored, tuple)
	require.NotNil(t, restored[StateKeyNextNodes])
}

type recordingSaver struct {
	mu     sync.Mutex
	tuples map[string]*CheckpointTuple
}

func newRecordingSaver() *recordingSaver {
	return &recordingSaver{tuples: make(map[string]*CheckpointTuple)}
}

func (s *recordingSaver) Get(
	ctx context.Context,
	config map[string]any,
) (*Checkpoint, error) {
	tuple, err := s.GetTuple(ctx, config)
	if err != nil || tuple == nil {
		return nil, err
	}
	return tuple.Checkpoint, nil
}

func (s *recordingSaver) GetTuple(
	_ context.Context,
	config map[string]any,
) (*CheckpointTuple, error) {
	key := GetLineageID(config) + ":" + GetNamespace(config) + ":" +
		GetCheckpointID(config)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tuples[key], nil
}

func (s *recordingSaver) List(
	_ context.Context,
	_ map[string]any,
	_ *CheckpointFilter,
) ([]*CheckpointTuple, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*CheckpointTuple, 0, len(s.tuples))
	for _, tuple := range s.tuples {
		out = append(out, tuple)
	}
	return out, nil
}

func (s *recordingSaver) Put(
	_ context.Context,
	req PutRequest,
) (map[string]any, error) {
	cfg := CreateCheckpointConfig(
		GetLineageID(req.Config),
		req.Checkpoint.ID,
		GetNamespace(req.Config),
	)
	key := GetLineageID(cfg) + ":" + GetNamespace(cfg) + ":" +
		GetCheckpointID(cfg)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tuples[key] = &CheckpointTuple{
		Config:     cfg,
		Checkpoint: req.Checkpoint,
		Metadata:   req.Metadata,
	}
	return cfg, nil
}

func (s *recordingSaver) PutWrites(
	_ context.Context,
	_ PutWritesRequest,
) error {
	return nil
}

func (s *recordingSaver) PutFull(
	_ context.Context,
	req PutFullRequest,
) (map[string]any, error) {
	cfg, err := s.Put(context.Background(), PutRequest{
		Config:      req.Config,
		Checkpoint:  req.Checkpoint,
		Metadata:    req.Metadata,
		NewVersions: req.NewVersions,
	})
	if err != nil {
		return nil, err
	}
	key := GetLineageID(cfg) + ":" + GetNamespace(cfg) + ":" +
		GetCheckpointID(cfg)

	pending := make([]PendingWrite, len(req.PendingWrites))
	copy(pending, req.PendingWrites)

	s.mu.Lock()
	if tuple := s.tuples[key]; tuple != nil {
		tuple.PendingWrites = pending
	}
	s.mu.Unlock()

	return cfg, nil
}

func (s *recordingSaver) DeleteLineage(
	_ context.Context,
	_ string,
) error {
	return nil
}

func (s *recordingSaver) Close() error { return nil }

func (s *recordingSaver) findLoopCheckpointWithBarrierSet(
	channelName string,
	mustContain string,
	mustNotContain string,
) *CheckpointTuple {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, tuple := range s.tuples {
		if tuple == nil || tuple.Metadata == nil {
			continue
		}
		if tuple.Metadata.Source != CheckpointSourceLoop {
			continue
		}
		if tuple.Checkpoint == nil {
			continue
		}
		seen := tuple.Checkpoint.BarrierSets[channelName]
		if !containsString(seen, mustContain) {
			continue
		}
		if mustNotContain != "" &&
			containsString(seen, mustNotContain) {
			continue
		}
		return tuple
	}
	return nil
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func TestExecutor_JoinEdge_ResumeRestoresBarrierSet(t *testing.T) {
	const (
		orderKey  = "order"
		ticksKey  = "ticks"
		lineageID = "ln-join-resume"
	)

	schema := NewStateSchema().
		AddField(orderKey, StateField{
			Type:    reflect.TypeOf([]string{}),
			Reducer: StringSliceReducer,
			Default: func() any { return []string{} },
		}).
		AddField(ticksKey, StateField{
			Type:    reflect.TypeOf(0),
			Reducer: DefaultReducer,
			Default: func() any { return 0 },
		})

	sg := NewStateGraph(schema)
	sg.AddNode("start", func(ctx context.Context, state State) (any, error) {
		return State{orderKey: []string{"start"}}, nil
	})
	sg.AddNode("b", func(ctx context.Context, state State) (any, error) {
		return State{orderKey: []string{"b"}}, nil
	})
	sg.AddNode("ticker", func(ctx context.Context, state State) (any, error) {
		ticks := state[ticksKey].(int) + 1
		return State{
			orderKey: []string{"ticker"},
			ticksKey: ticks,
		}, nil
	})
	sg.AddNode("c", func(ctx context.Context, state State) (any, error) {
		return State{orderKey: []string{"c"}}, nil
	})
	sg.AddNode("join", func(ctx context.Context, state State) (any, error) {
		return State{orderKey: []string{"join"}}, nil
	})

	sg.SetEntryPoint("start")
	sg.AddEdge("start", "b")
	sg.AddEdge("start", "ticker")
	sg.AddConditionalEdges(
		"ticker",
		func(ctx context.Context, state State) (string, error) {
			ticks := state[ticksKey].(int)
			if ticks < 3 {
				return "ticker", nil
			}
			return "c", nil
		},
		map[string]string{
			"ticker": "ticker",
			"c":      "c",
		},
	)
	sg.AddJoinEdge([]string{"b", "c"}, "join")
	sg.SetFinishPoint("join")

	g, err := sg.Compile()
	require.NoError(t, err)

	saver := newRecordingSaver()
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)

	inv := &agent.Invocation{InvocationID: lineageID}
	ch, err := exec.Execute(context.Background(), State{}, inv)
	require.NoError(t, err)
	for range ch {
	}

	starts := []string{"b", "c"}
	joinChan := joinChannelName("join", starts)

	tuple := saver.findLoopCheckpointWithBarrierSet(joinChan, "b", "c")
	require.NotNil(t, tuple)
	require.NotNil(t, tuple.Checkpoint)

	seen, ok := tuple.Checkpoint.BarrierSets[joinChan]
	require.True(t, ok)
	require.Contains(t, seen, "b")
	require.NotContains(t, seen, "c")

	resumeState := State{
		CfgKeyLineageID:    lineageID,
		CfgKeyCheckpointNS: DefaultCheckpointNamespace,
		CfgKeyCheckpointID: tuple.Checkpoint.ID,
	}
	resumeInv := &agent.Invocation{InvocationID: "inv-join-resume"}
	resumeCh, err := exec.Execute(context.Background(), resumeState, resumeInv)
	require.NoError(t, err)

	var doneEvent *event.Event
	for evt := range resumeCh {
		if evt.Done {
			doneEvent = evt
			break
		}
	}
	require.NotNil(t, doneEvent)
	raw, ok := doneEvent.StateDelta[orderKey]
	require.True(t, ok)

	var order []string
	require.NoError(t, json.Unmarshal(raw, &order))
	require.Contains(t, order, "join")
}

func TestExecutor_SyncResumeState_RemovesKeysWhenMissing(t *testing.T) {
	exec := &Executor{graph: New(NewStateSchema())}
	execCtx := &ExecutionContext{
		State: State{
			ResumeChannel:          "pending",
			StateKeyResumeMap:      map[string]any{"node": "old"},
			StateKeyUsedInterrupts: map[string]any{"node": "value"},
		},
	}
	exec.syncResumeState(execCtx, State{})
	if _, exists := execCtx.State[ResumeChannel]; exists {
		t.Fatalf("resume channel should be cleared")
	}
	if _, exists := execCtx.State[StateKeyResumeMap]; exists {
		t.Fatalf("resume map should be cleared")
	}
	if _, exists := execCtx.State[StateKeyUsedInterrupts]; exists {
		t.Fatalf("used interrupts should be cleared")
	}
}

func TestExecutor_SyncResumeState_CopiesMapMutations(t *testing.T) {
	exec := &Executor{graph: New(NewStateSchema())}
	execCtx := &ExecutionContext{State: make(State)}
	nodeState := State{
		ResumeChannel:     "answer",
		StateKeyResumeMap: map[string]any{"node": "new"},
		StateKeyUsedInterrupts: map[string]any{
			"node": "new",
		},
	}
	exec.syncResumeState(execCtx, nodeState)
	// Mutate node state after sync to verify executor state holds a copy.
	nodeState[StateKeyResumeMap].(map[string]any)["node"] = "mutated"
	require.Equal(t, "answer", execCtx.State[ResumeChannel])
	require.Equal(t, "new", execCtx.State[StateKeyResumeMap].(map[string]any)["node"])
	require.Equal(t, "new", execCtx.State[StateKeyUsedInterrupts].(map[string]any)["node"])
}

func TestExecutor_SyncResumeState_NilInputs(t *testing.T) {
	exec := &Executor{graph: New(NewStateSchema())}
	require.NotPanics(t, func() {
		exec.syncResumeState(nil, State{ResumeChannel: "value"})
	})
	execCtx := &ExecutionContext{State: State{ResumeChannel: "existing"}}
	require.NotPanics(t, func() {
		exec.syncResumeState(execCtx, nil)
	})
	require.Equal(t, "existing", execCtx.State[ResumeChannel])
}

func TestExecutor_SyncResumeState_InitializesNilState(t *testing.T) {
	exec := &Executor{graph: New(NewStateSchema())}
	execCtx := &ExecutionContext{}
	nodeState := State{
		ResumeChannel: "value",
	}
	exec.syncResumeState(execCtx, nodeState)
	require.NotNil(t, execCtx.State)
	require.Equal(t, "value", execCtx.State[ResumeChannel])
}

func TestSyncResumeKey_NilValueDeletesKey(t *testing.T) {
	target := State{ResumeChannel: "to-delete"}
	source := State{ResumeChannel: nil}
	syncResumeKey(target, source, ResumeChannel)
	_, exists := target[ResumeChannel]
	require.False(t, exists)
}

func TestExecutor_BuildExecutionContext_ResumedVersionsSeen(t *testing.T) {
	exec := &Executor{graph: New(NewStateSchema())}
	last := &Checkpoint{VersionsSeen: map[string]map[string]int64{"n": {"ch": 2}}}
	ec := exec.buildExecutionContext(nil, "inv", State{}, true, last)
	require.Equal(t, int64(2), ec.versionsSeen["n"]["ch"])
}

func TestExecutor_GetNextNodes_Dedup(t *testing.T) {
	g := New(NewStateSchema())
	// Two different channels trigger the same node "dup"
	g.addChannel("branch:to:dup", ichannel.BehaviorLastValue)
	g.addChannel("branch:to:dup2", ichannel.BehaviorLastValue)
	g.addNodeTrigger("branch:to:dup", "dup")
	g.addNodeTrigger("branch:to:dup2", "dup")
	exec := &Executor{graph: g}
	// Build execution context and mark per-run channels available.
	ec := exec.buildExecutionContext(nil, "inv", State{}, false, nil)
	if ch1, ok := ec.channels.GetChannel("branch:to:dup"); ok && ch1 != nil {
		ch1.Update([]any{"v"}, 1)
	}
	if ch2, ok := ec.channels.GetChannel("branch:to:dup2"); ok && ch2 != nil {
		ch2.Update([]any{"v"}, 1)
	}

	nodes := exec.getNextNodes(ec)
	// dedup should keep only one instance of "dup"
	count := 0
	for _, n := range nodes {
		if n == "dup" {
			count++
		}
	}
	require.Equal(t, 1, count)
}

func TestExecutor_NodeHelpers(t *testing.T) {
	g := New(NewStateSchema())
	// Node present
	node := &Node{ID: "n1", Name: "Name1", Type: NodeTypeTool}
	_ = g.addNode(node)
	exec := &Executor{graph: g}
	require.Equal(t, NodeTypeTool, exec.getNodeType("n1"))
	require.Equal(t, "Name1", exec.getNodeName("n1"))
	// Node missing -> fallbacks
	require.Equal(t, NodeTypeFunction, exec.getNodeType("missing"))
	require.Equal(t, "missing", exec.getNodeName("missing"))
	// newNodeContext branches (no timeout)
	ctx, cancel := exec.newNodeContext(context.Background())
	cancel()
	require.NotNil(t, ctx)
	// with timeout branch
	exec.nodeTimeout = time.Millisecond
	ctx2, cancel2 := exec.newNodeContext(context.Background())
	cancel2()
	require.NotNil(t, ctx2)
	// newNodeCallbackContext uses getSessionID
	ec := &ExecutionContext{State: State{StateKeySession: &session.Session{ID: "sid"}}, InvocationID: "inv"}
	cb := exec.newNodeCallbackContext(ec, "n1", NodeTypeTool, 1, time.Now())
	require.Equal(t, "sid", cb.SessionID)
	// getSessionID nil
	require.Equal(t, "", exec.getSessionID(nil))
}

func TestDeepCopyAny_NestedStructures(t *testing.T) {
	nested := map[string]any{"m": map[string]any{"k": []any{1, 2}}}
	c := deepCopyAny(nested).(map[string]any)
	require.NotNil(t, c["m"])
}

func TestDeepCopyAny_SliceBranch(t *testing.T) {
	arr := []any{map[string]any{"k": 1}, []any{2, 3}}
	out := deepCopyAny(arr).([]any)
	require.Equal(t, 2, len(out))
}

func TestExecutor_GetNextNodes_And_BuildTaskStateCopy_And_MergeNodeCallbacks(t *testing.T) {
	g := New(NewStateSchema())
	// Setup trigger mapping for nodeX
	g.addChannel("branch:to:nodeX", ichannel.BehaviorLastValue)
	g.addNodeTrigger("branch:to:nodeX", "nodeX")
	exec := &Executor{graph: g}
	// Build execution context so per-run channels are created and mark them
	// as available.
	ec := exec.buildExecutionContext(nil, "inv", State{"a": 1}, false, nil)
	if chX, ok := ec.channels.GetChannel("branch:to:nodeX"); ok && chX != nil {
		chX.Update([]any{"v"}, 1)
	}
	// getNextNodes should include nodeX
	n := exec.getNextNodes(ec)
	require.Contains(t, n, "nodeX")

	// buildTaskStateCopy with overlay
	tsk := &Task{NodeID: "nodeX", Overlay: State{"b": 2}}
	st := exec.buildTaskStateCopy(ec, tsk)
	require.Equal(t, 1, st["a"])
	require.Equal(t, 2, st["b"])

	// mergeNodeCallbacks combine global and per-node via getMergedCallbacks
	gcb := &NodeCallbacks{}
	gcb.RegisterBeforeNode(func(ctx context.Context, c *NodeCallbackContext, s State) (any, error) { return nil, nil })
	pcb := &NodeCallbacks{}
	pcb.RegisterAfterNode(func(ctx context.Context, c *NodeCallbackContext, s State, r any, e error) (any, error) {
		return nil, nil
	})
	// attach per-node callbacks to nodeX in graph
	node := &Node{ID: "nodeX"}
	node.callbacks = pcb
	_ = g.addNode(node)
	st2 := State{StateKeyNodeCallbacks: gcb}
	merged := exec.getMergedCallbacks(st2, "nodeX")
	require.Equal(t, 1, len(merged.BeforeNode))
	require.Equal(t, 1, len(merged.AfterNode))
}

// Ensure buildExecutionContext seeds per-run channel versions from the last checkpoint.
func TestExecutor_BuildExecutionContext_SeedsChannelVersions(t *testing.T) {
	g := New(NewStateSchema())
	g.addChannel("branch:to:X", ichannel.BehaviorLastValue)
	exec := &Executor{graph: g}

	last := &Checkpoint{
		ChannelVersions: map[string]int64{
			"branch:to:X": 7,
		},
	}
	ec := exec.buildExecutionContext(nil, "inv", State{}, true, last)

	ch, ok := ec.channels.GetChannel("branch:to:X")
	require.True(t, ok)
	require.NotNil(t, ch)
	require.Equal(t, int64(7), ch.Version)
}

func TestExecutor_Resume_UsesAckedChannelVersions(t *testing.T) {
	const (
		lineageID = "ln-resume-acked-versions"
		namespace = "ns-resume-acked-versions"
		nodeA     = "A"
		nodeB     = "B"
		stateKeyA = "a_count"
		stateKeyB = "b_count"
	)

	schema := NewStateSchema()
	schema.AddField(
		stateKeyA,
		StateField{Type: reflect.TypeOf(0)},
	)
	schema.AddField(
		stateKeyB,
		StateField{Type: reflect.TypeOf(0)},
	)

	g, err := NewStateGraph(schema).
		AddNode(nodeA, func(ctx context.Context, state State) (any, error) {
			count, _ := state[stateKeyA].(int)
			return State{stateKeyA: count + 1}, nil
		}).
		AddNode(nodeB, func(ctx context.Context, state State) (any, error) {
			count, _ := state[stateKeyB].(int)
			return State{stateKeyB: count + 1}, nil
		}).
		SetEntryPoint(nodeA).
		AddEdge(nodeA, nodeB).
		AddEdge(nodeB, nodeA).
		Compile()
	require.NoError(t, err)

	saver := newSubgraphTestSaver()

	exec1, err := NewExecutor(
		g,
		WithCheckpointSaver(saver),
		WithMaxSteps(3),
	)
	require.NoError(t, err)

	init := State{
		CfgKeyLineageID:    lineageID,
		CfgKeyCheckpointNS: namespace,
	}
	ch, err := exec1.Execute(
		context.Background(),
		init,
		&agent.Invocation{InvocationID: "inv-acked-init"},
	)
	require.NoError(t, err)
	for range ch {
	}

	tuples, err := saver.List(
		context.Background(),
		CreateCheckpointConfig(lineageID, "", namespace),
		nil,
	)
	require.NoError(t, err)

	var resumeTuple *CheckpointTuple
	for _, tuple := range tuples {
		if tuple == nil || tuple.Metadata == nil || tuple.Checkpoint == nil {
			continue
		}
		if tuple.Metadata.Source != CheckpointSourceLoop {
			continue
		}
		if tuple.Metadata.Step != 2 {
			continue
		}
		resumeTuple = tuple
		break
	}
	require.NotNil(t, resumeTuple)

	exec2, err := NewExecutor(
		g,
		WithCheckpointSaver(saver),
		WithMaxSteps(5),
	)
	require.NoError(t, err)

	resume := State{
		CfgKeyLineageID:    lineageID,
		CfgKeyCheckpointNS: namespace,
		CfgKeyCheckpointID: resumeTuple.Checkpoint.ID,
	}
	ch2, err := exec2.Execute(
		context.Background(),
		resume,
		&agent.Invocation{InvocationID: "inv-acked-resume"},
	)
	require.NoError(t, err)

	var done *event.Event
	for ev := range ch2 {
		if ev == nil {
			continue
		}
		if ev.Done && ev.Object == ObjectTypeGraphExecution {
			done = ev
		}
	}
	require.NotNil(t, done)
	require.NotNil(t, done.StateDelta)

	var aCount int
	require.NoError(t, json.Unmarshal(done.StateDelta[stateKeyA], &aCount))
	var bCount int
	require.NoError(t, json.Unmarshal(done.StateDelta[stateKeyB], &bCount))

	require.Equal(t, 3, aCount)
	require.Equal(t, 2, bCount)
}

func TestExecutor_BuildExecutionContext_ResumedNilCheckpoint(t *testing.T) {
	const (
		barrierChannel = "barrier:ch"
		invocationID   = "inv"
	)
	expectedSenders := []string{"a", "b"}

	g := New(NewStateSchema())
	g.addChannel(barrierChannel, ichannel.BehaviorBarrier)
	template, ok := g.getChannel(barrierChannel)
	require.True(t, ok)
	template.SetBarrierExpected(expectedSenders)

	exec := &Executor{graph: g}
	ec := exec.buildExecutionContext(nil, invocationID, State{}, true, nil)

	require.Empty(t, ec.versionsSeen)
	runCh, ok := ec.channels.GetChannel(barrierChannel)
	require.True(t, ok)
	require.NotNil(t, runCh)
	require.Equal(t, expectedSenders, runCh.BarrierExpected)
}

func TestExecutor_BuildExecutionContext_SkipsMissingCheckpointChannels(
	t *testing.T,
) {
	const (
		knownChannel   = "branch:to:X"
		missingChannel = "missing:ch"
		barrierChannel = "barrier:ch"
		barrierSender  = "sender"
		invocationID   = "inv"
		nodeID         = "node"
	)

	g := New(NewStateSchema())
	g.addChannel(knownChannel, ichannel.BehaviorLastValue)
	g.addChannel(barrierChannel, ichannel.BehaviorBarrier)
	template, ok := g.getChannel(barrierChannel)
	require.True(t, ok)
	template.SetBarrierExpected([]string{barrierSender})

	exec := &Executor{graph: g}
	last := &Checkpoint{
		VersionsSeen: map[string]map[string]int64{
			nodeID: {
				knownChannel: 1,
			},
		},
		ChannelVersions: map[string]int64{
			knownChannel:   7,
			missingChannel: 3,
		},
		BarrierSets: map[string][]string{
			barrierChannel: {barrierSender},
			missingChannel: {barrierSender},
		},
	}

	ec := exec.buildExecutionContext(nil, invocationID, State{}, true, last)

	require.Equal(t, int64(1), ec.versionsSeen[nodeID][knownChannel])

	ch, ok := ec.channels.GetChannel(knownChannel)
	require.True(t, ok)
	require.NotNil(t, ch)
	require.Equal(t, int64(7), ch.Version)

	barrierCh, ok := ec.channels.GetChannel(barrierChannel)
	require.True(t, ok)
	require.NotNil(t, barrierCh)
	require.Equal(
		t,
		[]string{barrierSender},
		barrierCh.BarrierSeenSnapshot(),
	)
}

// Ensure applyPendingWrites replays writes into per-execution channels (not Graph channels)
// and respects the PendingWrite.Sequence ordering.
func TestExecutor_ApplyPendingWrites_UsesExecCtxChannels(t *testing.T) {
	g := New(NewStateSchema())
	g.addChannel("x", ichannel.BehaviorLastValue)
	exec := &Executor{graph: g}

	ec := exec.buildExecutionContext(nil, "inv", State{}, false, nil)
	writes := []PendingWrite{
		{Channel: "x", Value: 1, Sequence: 2},
		{Channel: "x", Value: 2, Sequence: 1},
	}

	exec.applyPendingWrites(context.Background(), nil, ec, writes)

	// Graph-level channel definition should remain untouched.
	graphCh, _ := g.getChannel("x")
	require.NotNil(t, graphCh)
	require.Equal(t, int64(0), graphCh.Version)

	// Per-run channel should have applied both writes in sequence order
	// (Sequence=1 then Sequence=2), ending with value 1 and version 2.
	runCh, ok := ec.channels.GetChannel("x")
	require.True(t, ok)
	require.NotNil(t, runCh)
	require.Equal(t, int64(2), runCh.Version)
	require.Equal(t, 1, runCh.Get())
}

// Guard: ensure applyPendingWrites safely handles nil execCtx.
func TestExecutor_ApplyPendingWrites_NilExecCtx_NoPanic(t *testing.T) {
	exec := &Executor{graph: New(NewStateSchema())}
	require.NotPanics(t, func() {
		exec.applyPendingWrites(context.Background(), nil, nil, []PendingWrite{
			{Channel: "x", Value: 1, Sequence: 1},
		})
	})
}

func TestRunModel_BeforeModelError(t *testing.T) {
	cbs := model.NewCallbacks().RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		return nil, fmt.Errorf("boom")
	})
	_, _, err := runModel(context.Background(), cbs, &dummyModel{}, &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
	require.Error(t, err)
}
