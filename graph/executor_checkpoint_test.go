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

	// updateVersionsSeen should record current version for triggers
	exec.updateVersionsSeen(ec, "nodeA", []string{"branch:to:x"})
	require.Equal(t, perRunCh.Version, ec.versionsSeen["nodeA"]["branch:to:x"])
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
