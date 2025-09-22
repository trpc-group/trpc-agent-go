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
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	ichannel "trpc.group/trpc-go/trpc-agent-go/graph/internal/channel"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

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

// Interrupt test to cover handleInterrupt path
func TestExecutor_HandleInterrupt(t *testing.T) {
	g, err := NewStateGraph(NewStateSchema()).
		AddNode("i", func(ctx context.Context, state State) (any, error) {
			return nil, &InterruptError{Value: "stop", NodeID: "i", TaskID: "t1", Path: []string{"i"}}
		}).
		SetEntryPoint("i").
		SetFinishPoint("i").
		Compile()
	require.NoError(t, err)
	saver := newMockSaver()
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)
	ch, err := exec.Execute(context.Background(), State{}, &agent.Invocation{InvocationID: "inv-int"})
	require.NoError(t, err)
	for range ch { /* drain */
	}
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
	channels := exec.graph.getAllChannels()
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
	// getNextChannelsInStep should include our channel
	got := exec.getNextChannelsInStep(5)
	require.Contains(t, got, "branch:to:x")
	// clear marks
	exec.clearChannelStepMarks()
	require.False(t, c.IsUpdatedInStep(5))

	// updateVersionsSeen should record current version for triggers
	ec := exec.buildExecutionContext(nil, "inv", State{}, false, nil)
	exec.updateVersionsSeen(ec, "nodeA", []string{"branch:to:x"})
	require.Equal(t, c.Version, ec.versionsSeen["nodeA"]["branch:to:x"])
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

func TestExecutor_CreateCheckpoint_SuccessAndError(t *testing.T) {
	g := New(NewStateSchema())
	exec := &Executor{graph: g}
	// nil saver no-op
	err := exec.createCheckpoint(context.Background(), CreateCheckpointConfig("ln", "", "ns"), State{"a": 1}, CheckpointSourceUpdate, 0)
	require.NoError(t, err)
	// success path
	pm := &putMockSaver{}
	exec.checkpointSaver = pm
	err = exec.createCheckpoint(context.Background(), CreateCheckpointConfig("ln", "", "ns"), State{"a": 1}, CheckpointSourceUpdate, 0)
	require.NoError(t, err)
	require.True(t, pm.called)
	// error path
	pm2 := &putMockSaver{retErr: fmt.Errorf("err")}
	exec.checkpointSaver = pm2
	err = exec.createCheckpoint(context.Background(), CreateCheckpointConfig("ln", "", "ns"), State{"a": 1}, CheckpointSourceUpdate, 0)
	require.Error(t, err)
}

// resumeFromCheckpoint paths
type resumeMockSaver struct {
	tuple *CheckpointTuple
	err   error
}

func (m *resumeMockSaver) Get(ctx context.Context, config map[string]any) (*Checkpoint, error) {
	return nil, nil
}
func (m *resumeMockSaver) GetTuple(ctx context.Context, config map[string]any) (*CheckpointTuple, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.tuple, nil
}
func (m *resumeMockSaver) List(ctx context.Context, config map[string]any, filter *CheckpointFilter) ([]*CheckpointTuple, error) {
	return nil, nil
}
func (m *resumeMockSaver) Put(ctx context.Context, req PutRequest) (map[string]any, error) {
	return req.Config, nil
}
func (m *resumeMockSaver) PutWrites(ctx context.Context, req PutWritesRequest) error { return nil }
func (m *resumeMockSaver) PutFull(ctx context.Context, req PutFullRequest) (map[string]any, error) {
	return req.Config, nil
}
func (m *resumeMockSaver) DeleteLineage(ctx context.Context, lineageID string) error { return nil }
func (m *resumeMockSaver) Close() error                                              { return nil }

func TestExecutor_ResumeFromCheckpoint_Paths(t *testing.T) {
	g := New(NewStateSchema())
	exec := &Executor{graph: g}
	// nil saver
	st, ckpt, writes, err := exec.resumeFromCheckpoint(context.Background(), nil, CreateCheckpointConfig("ln", "id", "ns"))
	require.NoError(t, err)
	require.Nil(t, st)
	require.Nil(t, ckpt)
	require.Nil(t, writes)

	// saver error
	exec.checkpointSaver = &resumeMockSaver{err: fmt.Errorf("err")}
	_, _, _, err = exec.resumeFromCheckpoint(context.Background(), nil, CreateCheckpointConfig("ln", "id", "ns"))
	require.Error(t, err)

	// tuple with pending writes
	g.addChannel("branch:to:N1", ichannel.BehaviorLastValue)
	ck := &Checkpoint{ID: "c1", ChannelValues: map[string]any{"x": 1}}
	tuple := &CheckpointTuple{Checkpoint: ck, PendingWrites: []PendingWrite{{Channel: "branch:to:N1", Value: 2, Sequence: 1}}}
	exec.checkpointSaver = &resumeMockSaver{tuple: tuple}
	st, ckpt, writes, err = exec.resumeFromCheckpoint(context.Background(), nil, CreateCheckpointConfig("ln", "id", "ns"))
	require.NoError(t, err)
	require.Equal(t, 1, st["x"])
	require.NotNil(t, ckpt)
	require.Len(t, writes, 1)

	// tuple with NextNodes fallback
	tuple2 := &CheckpointTuple{Checkpoint: &Checkpoint{ID: "c2", ChannelValues: map[string]any{"y": 3}, NextNodes: []string{"A"}}}
	exec.checkpointSaver = &resumeMockSaver{tuple: tuple2}
	st, ckpt, writes, err = exec.resumeFromCheckpoint(context.Background(), nil, CreateCheckpointConfig("ln", "id", "ns"))
	require.NoError(t, err)
	require.NotNil(t, st[StateKeyNextNodes])
	require.NotNil(t, ckpt)
	require.Len(t, writes, 0)
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
	// applyExecutableNextNodes (pendingWrites empty and NextNodes has A)
	tuple := &CheckpointTuple{Checkpoint: &Checkpoint{NextNodes: []string{"A", End, ""}}}
	restored := make(State)
	exec.applyExecutableNextNodes(restored, tuple)
	require.NotNil(t, restored[StateKeyNextNodes])
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
	c1, _ := g.getChannel("branch:to:dup")
	c2, _ := g.getChannel("branch:to:dup2")
	c1.Update([]any{"v"}, 1)
	c2.Update([]any{"v"}, 1)
	exec := &Executor{graph: g}
	nodes := exec.getNextNodes(State{})
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
	// Set channel available
	chX, _ := g.getChannel("branch:to:nodeX")
	chX.Update([]any{"v"}, 1)
	exec := &Executor{graph: g}
	// getNextNodes should include nodeX
	n := exec.getNextNodes(State{})
	require.Contains(t, n, "nodeX")

	// buildTaskStateCopy with overlay
	ec := exec.buildExecutionContext(nil, "inv", State{"a": 1}, false, nil)
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

func TestRunModel_BeforeModelError(t *testing.T) {
	cbs := model.NewCallbacks().RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		return nil, fmt.Errorf("boom")
	})
	_, err := runModel(context.Background(), cbs, &dummyModel{}, &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
	require.Error(t, err)
}
