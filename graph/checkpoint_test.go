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
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	oteltrace "go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNewCheckpointConfig_PanicsOnEmptyLineage(t *testing.T) {
	assert.Panics(t, func() { _ = NewCheckpointConfig("") })
}

func TestCreateCheckpointConfig_PanicsOnEmptyLineage(t *testing.T) {
	assert.Panics(t, func() { _ = CreateCheckpointConfig("", "", "") })
}

func TestConfigHelpers_Getters(t *testing.T) {
	cfg := NewCheckpointConfig("ln").WithNamespace("ns").WithCheckpointID("id").WithResumeMap(map[string]any{"k": 1})
	m := cfg.ToMap()

	assert.Equal(t, "ln", GetLineageID(m))
	assert.Equal(t, "ns", GetNamespace(m))
	assert.Equal(t, "id", GetCheckpointID(m))
	rm := GetResumeMap(m)
	assert.NotNil(t, rm)
	assert.Equal(t, 1, rm["k"])

	// Defaults on nil config
	assert.Equal(t, "", GetCheckpointID(nil))
	assert.Equal(t, "", GetLineageID(nil))
	assert.Equal(t, DefaultCheckpointNamespace, GetNamespace(nil))
	assert.Nil(t, GetResumeMap(nil))
}

func TestCheckpoint_CopyAndFork(t *testing.T) {
	c := NewCheckpoint(map[string]any{"a": 1, "b": map[string]any{"x": 2}}, map[string]int64{"a": 1}, map[string]map[string]int64{"n": {}})
	c.UpdatedChannels = []string{"a", "b"}
	c.PendingSends = []PendingSend{{Channel: "ch", Value: 123, TaskID: "t1"}}
	c.NextNodes = []string{"n1", "n2"}
	c.NextChannels = []string{"c1"}
	c.SetInterruptState("node", "task", "val", 3, []string{"p1", "p2"})

	copied := c.Copy()
	// Copy preserves ID and timestamp
	assert.Equal(t, c.ID, copied.ID)
	assert.True(t, copied.Timestamp.Equal(c.Timestamp))
	// Deep-copied structures: mutate copy and ensure original unchanged
	// ChannelValues map
	copied.ChannelValues["a"] = 999
	assert.Equal(t, 1, c.ChannelValues["a"])
	if bm, ok := copied.ChannelValues["b"].(map[string]any); ok {
		bm["x"] = 999
		// original nested map should remain intact
		ob := c.ChannelValues["b"].(map[string]any)
		assert.Equal(t, 2, ob["x"])
	}
	// ChannelVersions map
	copied.ChannelVersions["a"] = 99
	assert.Equal(t, int64(1), c.ChannelVersions["a"])
	// VersionsSeen map of map
	if _, ok := copied.VersionsSeen["n"]; ok {
		copied.VersionsSeen["n"]["z"] = 7
		_, exists := c.VersionsSeen["n"]["z"]
		assert.False(t, exists)
	}
	// UpdatedChannels slice
	prevUC := append([]string{}, c.UpdatedChannels...)
	if len(copied.UpdatedChannels) > 0 {
		copied.UpdatedChannels[0] = "modified"
	}
	assert.Equal(t, prevUC, c.UpdatedChannels)
	// PendingSends slice of structs
	prevPS := append([]PendingSend{}, c.PendingSends...)
	if len(copied.PendingSends) > 0 {
		copied.PendingSends[0].Channel = "modified"
	}
	assert.Equal(t, prevPS, c.PendingSends)
	// NextNodes slice
	prevNN := append([]string{}, c.NextNodes...)
	copied.NextNodes = append(copied.NextNodes, "extra")
	assert.Equal(t, prevNN, c.NextNodes)
	// NextChannels slice
	prevNC := append([]string{}, c.NextChannels...)
	copied.NextChannels = append(copied.NextChannels, "extra")
	assert.Equal(t, prevNC, c.NextChannels)
	// InterruptState path
	if copied.InterruptState != nil && len(copied.InterruptState.Path) > 0 {
		prevPath := append([]string{}, c.InterruptState.Path...)
		copied.InterruptState.Path[0] = "changed"
		assert.Equal(t, prevPath, c.InterruptState.Path)
	}

	// Fork creates new ID and sets parent
	time.Sleep(1 * time.Millisecond)
	forked := c.Fork()
	assert.NotNil(t, forked)
	assert.Equal(t, c.ID, forked.ParentCheckpointID)
	assert.NotEqual(t, c.ID, forked.ID)
	assert.True(t, forked.Timestamp.After(c.Timestamp))
}

func TestCheckpoint_InterruptStateHelpers(t *testing.T) {
	c := NewCheckpoint(nil, nil, nil)
	c.SetInterruptState("n1", "t1", 42, 7, []string{"p"})
	assert.NotNil(t, c.InterruptState)
	assert.Equal(t, "n1", c.InterruptState.NodeID)
	c.ClearInterruptState()
	assert.Nil(t, c.InterruptState)
}

func TestCheckpointManager_NilSaver_Errors(t *testing.T) {
	cm := NewCheckpointManager(nil)
	ctx := context.Background()
	cfg := CreateCheckpointConfig("ln", "", "")

	_, err := cm.CreateCheckpoint(ctx, cfg, State{"x": 1}, CheckpointSourceInput, -1)
	assert.Error(t, err)
	_, err = cm.ResumeFromCheckpoint(ctx, cfg)
	assert.NoError(t, err) // returns nil, nil when saver is nil
	_, err = cm.ListCheckpoints(ctx, cfg, nil)
	assert.Error(t, err)
	err = cm.DeleteLineage(ctx, "ln")
	assert.Error(t, err)
	_, err = cm.Latest(ctx, "ln", "")
	assert.Error(t, err)
	_, err = cm.Get(ctx, cfg)
	assert.Error(t, err)
	_, err = cm.GetTuple(ctx, cfg)
	assert.Error(t, err)
	_, err = cm.Goto(ctx, "ln", "", "id")
	assert.Error(t, err)
}

func TestCheckpointConfig_Extras_And_FilterBuilders(t *testing.T) {
	cfg := NewCheckpointConfig("ln-x").WithCheckpointID("ck").WithNamespace("ns").WithResumeMap(map[string]any{"k": "v"}).WithExtra("custom", 123)
	m := cfg.ToMap()
	assert.Equal(t, "ln-x", GetLineageID(m))
	assert.Equal(t, "ck", GetCheckpointID(m))
	assert.Equal(t, "ns", GetNamespace(m))
	r := GetResumeMap(m)
	require.NotNil(t, r)
	assert.Equal(t, "v", r["k"])
	assert.Equal(t, 123, m["custom"]) // extra copied at top-level

	// Filter builders
	f := NewCheckpointFilter().WithBefore(CreateCheckpointConfig("ln-x", "before", "")).WithLimit(7).WithMetadata("k1", "v1")
	require.NotNil(t, f)
	assert.Equal(t, 7, f.Limit)
	require.NotNil(t, f.Before)
	assert.Equal(t, "before", GetCheckpointID(f.Before))
	require.NotNil(t, f.Metadata)
	assert.Equal(t, "v1", f.Metadata["k1"])
}

func TestCheckpointManager_Put_Smoke(t *testing.T) {
	saver := newMockSaver()
	cm := NewCheckpointManager(saver)
	cfg := CreateCheckpointConfig("ln-put", "", "ns")
	ck := NewCheckpoint(map[string]any{"a": 1}, map[string]int64{"a": 1}, nil)
	meta := NewCheckpointMetadata(CheckpointSourceUpdate, 3)
	_, err := cm.Put(context.Background(), PutRequest{Config: cfg, Checkpoint: ck, Metadata: meta, NewVersions: map[string]int64{"a": 1}})
	require.NoError(t, err)
	got, err := cm.Get(context.Background(), CreateCheckpointConfig("ln-put", ck.ID, "ns"))
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, ck.ID, got.ID)
}

// capturingSaver captures the last Put request for assertions.
type capturingSaver struct{ lastPut PutRequest }

func (c *capturingSaver) Get(
	_ context.Context, _ map[string]any,
) (*Checkpoint, error) {
	return nil, nil
}
func (c *capturingSaver) GetTuple(
	_ context.Context, _ map[string]any,
) (*CheckpointTuple, error) {
	return nil, nil
}
func (c *capturingSaver) List(
	_ context.Context, _ map[string]any, _ *CheckpointFilter,
) ([]*CheckpointTuple, error) {
	return nil, nil
}
func (c *capturingSaver) Put(
	_ context.Context, req PutRequest,
) (map[string]any, error) {
	c.lastPut = req
	// echo back config as-is
	return req.Config, nil
}
func (c *capturingSaver) PutWrites(
	_ context.Context, _ PutWritesRequest,
) error {
	return nil
}
func (c *capturingSaver) PutFull(
	_ context.Context, req PutFullRequest,
) (map[string]any, error) {
	// route through Put to capture
	return c.Put(
		context.Background(),
		PutRequest{
			Config:      req.Config,
			Checkpoint:  req.Checkpoint,
			Metadata:    req.Metadata,
			NewVersions: req.NewVersions,
		},
	)
}
func (c *capturingSaver) DeleteLineage(
	_ context.Context, _ string,
) error {
	return nil
}
func (c *capturingSaver) Close() error { return nil }

func TestCreateCheckpoint_SkipsUnsafeKeys(t *testing.T) {
	saver := &capturingSaver{}
	cm := NewCheckpointManager(saver)
	cfg := CreateCheckpointConfig("ln-unsafe", "", "")
	// Include both a safe key and unsafe keys (callbacks, exec context).
	st := State{
		"keep":                 1,
		StateKeyNodeCallbacks:  NewNodeCallbacks(),
		StateKeyExecContext:    &ExecutionContext{InvocationID: "x"},
		StateKeyModelCallbacks: "mc", // unsafe
	}
	ck, err := cm.CreateCheckpoint(
		context.Background(), cfg, st, CheckpointSourceInput, 0,
	)
	require.NoError(t, err)
	require.NotNil(t, ck)

	// Assert that unsafe keys are filtered from persisted channel values.
	vals := saver.lastPut.Checkpoint.ChannelValues
	require.NotNil(t, vals)
	require.Contains(t, vals, "keep")
	require.NotContains(t, vals, StateKeyNodeCallbacks)
	require.NotContains(t, vals, StateKeyExecContext)
	require.NotContains(t, vals, StateKeyModelCallbacks)
}

// minimal in-memory saver mock for manager.put test
type mockSaver struct{ byID map[string]*CheckpointTuple }

func newMockSaver() *mockSaver { return &mockSaver{byID: map[string]*CheckpointTuple{}} }

func (m *mockSaver) Get(_ context.Context, cfg map[string]any) (*Checkpoint, error) {
	t, _ := m.GetTuple(context.Background(), cfg)
	if t == nil {
		return nil, nil
	}
	return t.Checkpoint, nil
}
func (m *mockSaver) GetTuple(_ context.Context, cfg map[string]any) (*CheckpointTuple, error) {
	key := GetLineageID(cfg) + ":" + GetNamespace(cfg) + ":" + GetCheckpointID(cfg)
	if t, ok := m.byID[key]; ok {
		return t, nil
	}
	return nil, nil
}
func (m *mockSaver) List(_ context.Context, cfg map[string]any, _ *CheckpointFilter) ([]*CheckpointTuple, error) {
	lineage := GetLineageID(cfg)
	var out []*CheckpointTuple
	for key, t := range m.byID {
		if len(lineage) == 0 {
			out = append(out, t)
			continue
		}
		// key format lineage:ns:id
		if len(key) >= len(lineage)+1 && key[:len(lineage)] == lineage {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *mockSaver) Put(_ context.Context, req PutRequest) (map[string]any, error) {
	lineage := GetLineageID(req.Config)
	ns := GetNamespace(req.Config)
	key := lineage + ":" + ns + ":" + req.Checkpoint.ID
	m.byID[key] = &CheckpointTuple{Config: CreateCheckpointConfig(lineage, req.Checkpoint.ID, ns), Checkpoint: req.Checkpoint, Metadata: req.Metadata}
	return CreateCheckpointConfig(lineage, req.Checkpoint.ID, ns), nil
}
func (m *mockSaver) PutWrites(_ context.Context, _ PutWritesRequest) error { return nil }
func (m *mockSaver) PutFull(_ context.Context, req PutFullRequest) (map[string]any, error) {
	return m.Put(context.Background(), PutRequest{Config: req.Config, Checkpoint: req.Checkpoint, Metadata: req.Metadata, NewVersions: req.NewVersions})
}
func (m *mockSaver) DeleteLineage(_ context.Context, _ string) error { return nil }
func (m *mockSaver) Close() error                                    { return nil }

func TestEvents_Stringers_And_CheckpointEventBuilders(t *testing.T) {
	// Stringers
	assert.Equal(t, "function", NodeTypeFunction.String())
	assert.Equal(t, "start", ExecutionPhaseStart.String())
	assert.Equal(t, "start", ToolExecutionPhaseStart.String())
	assert.Equal(t, "start", ModelExecutionPhaseStart.String())
	assert.Equal(t, "planning", PregelPhasePlanning.String())

	// Checkpoint event builders
	e1 := NewCheckpointCreatedEvent(
		WithCheckpointEventInvocationID("inv-1"),
		WithCheckpointEventCheckpointID("ck-1"),
		WithCheckpointEventSource("input"),
		WithCheckpointEventStep(1),
		WithCheckpointEventDuration(2*time.Second),
		WithCheckpointEventBytes(100),
		WithCheckpointEventWritesCount(3),
	)
	require.NotNil(t, e1)
	require.Contains(t, e1.StateDelta, MetadataKeyCheckpoint)

	e2 := NewCheckpointCommittedEvent(
		WithCheckpointEventInvocationID("inv-2"),
		WithCheckpointEventCheckpointID("ck-2"),
		WithCheckpointEventSource("loop"),
		WithCheckpointEventStep(2),
		WithCheckpointEventDuration(1*time.Second),
		WithCheckpointEventBytes(50),
		WithCheckpointEventWritesCount(1),
		WithCheckpointEventResumeReplay(true),
		WithCheckpointEventInterruptValue("x"),
	)
	require.NotNil(t, e2)
	require.Contains(t, e2.StateDelta, MetadataKeyCheckpoint)
}

func TestStateSchema_Validate_And_Reducers(t *testing.T) {
	schema := NewStateSchema()
	schema.AddField("req", StateField{Type: reflect.TypeOf("") /* string */, Required: true})
	schema.AddField("nums", StateField{Type: reflect.TypeOf([]any{}), Reducer: AppendReducer})
	schema.AddField("tags", StateField{Type: reflect.TypeOf([]string{}), Reducer: StringSliceReducer})
	schema.AddField("meta", StateField{Type: reflect.TypeOf(map[string]any{}), Reducer: MergeReducer})

	// Missing required should error
	err := schema.Validate(State{})
	require.Error(t, err)

	// Wrong type should error
	err = schema.Validate(State{"req": 123})
	require.Error(t, err)

	// Valid case
	err = schema.Validate(State{"req": "ok"})
	require.NoError(t, err)

	// Reducers
	s := State{"nums": []any{1}, "tags": []string{"a"}, "meta": map[string]any{"k": 1}}
	s2 := schema.ApplyUpdate(s, State{"nums": []any{2, 3}, "tags": []string{"b"}, "meta": map[string]any{"k2": 2}})
	require.ElementsMatch(t, []any{1, 2, 3}, s2["nums"].([]any))
	require.ElementsMatch(t, []string{"a", "b"}, s2["tags"].([]string))
	m := s2["meta"].(map[string]any)
	require.Equal(t, 1, m["k"])
	require.Equal(t, 2, m["k2"])
}

func TestStateGraph_Options_And_MustCompile(t *testing.T) {
	builder := NewStateGraph(NewStateSchema())
	// Function node with options
	fn := func(_ context.Context, _ State) (any, error) { return State{"ok": true}, nil }
	callbacks := NewNodeCallbacks()
	callbacks.RegisterBeforeNode(func(_ context.Context, _ *NodeCallbackContext, _ State) (any, error) { return nil, nil })
	callbacks.RegisterAfterNode(func(_ context.Context, _ *NodeCallbackContext, _ State, _ any, _ error) (any, error) { return nil, nil })
	callbacks.RegisterOnNodeError(func(_ context.Context, _ *NodeCallbackContext, _ State, _ error) {})

	g, err := builder.
		AddNode("n1", fn,
			WithName("N1"), WithDescription("desc"),
			WithPreNodeCallback(func(_ context.Context, _ *NodeCallbackContext, _ State) (any, error) { return nil, nil }),
			WithPostNodeCallback(func(_ context.Context, _ *NodeCallbackContext, _ State, _ any, _ error) (any, error) { return nil, nil }),
			WithNodeErrorCallback(func(_ context.Context, _ *NodeCallbackContext, _ State, _ error) {}),
			WithNodeCallbacks(callbacks),
			WithAgentNodeEventCallback(func(_ context.Context, _ *NodeCallbackContext, _ State, _ *event.Event) {}),
		).
		SetEntryPoint("n1").
		SetFinishPoint("n1").
		Compile()
	require.NoError(t, err)
	require.NotNil(t, g)

	// MustCompile should panic on invalid graph
	bad := NewStateGraph(NewStateSchema())
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic from MustCompile on invalid graph")
		}
	}()
	_ = bad.MustCompile()
}

func TestExecutor_Options_DerivedNodeTimeout(t *testing.T) {
	// minimal graph
	builder := NewStateGraph(NewStateSchema())
	g, err := builder.AddNode("a", func(_ context.Context, _ State) (any, error) { return nil, nil }).SetEntryPoint("a").SetFinishPoint("a").Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g,
		WithChannelBufferSize(10),
		WithMaxSteps(5),
		WithStepTimeout(4*time.Second), // no NodeTimeout provided, derive should be >=1s and 2s here
		WithCheckpointSaveTimeout(2*time.Second),
	)
	require.NoError(t, err)
	require.NotNil(t, exec)
	assert.Equal(t, 10, exec.channelBufferSize)
	assert.Equal(t, 5, exec.maxSteps)
	assert.Equal(t, 4*time.Second, exec.stepTimeout)
	assert.GreaterOrEqual(t, int64(exec.nodeTimeout), int64(time.Second))
	assert.Equal(t, 2*time.Second, exec.checkpointSaveTimeout)

	// Explicit NodeTimeout should override derivation
	exec2, err := NewExecutor(g, WithStepTimeout(6*time.Second), WithNodeTimeout(1*time.Second))
	require.NoError(t, err)
	assert.Equal(t, 1*time.Second, exec2.nodeTimeout)
}

// dummy tool implementing CallableTool
type dummyTool struct{ name string }

func (d *dummyTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: d.name} }
func (d *dummyTool) Call(_ context.Context, jsonArgs []byte) (any, error) {
	// echo back args as map
	var m map[string]any
	_ = json.Unmarshal(jsonArgs, &m)
	if m == nil {
		m = map[string]any{"ok": true}
	}
	return m, nil
}

func TestToolEvents_And_ExecuteSingleToolCall(t *testing.T) {
	// prepare event channel
	ch := make(chan *event.Event, 10)
	// emit start
	emitToolStartEvent(context.Background(), ch, "inv-id", "echo", "id-1", "nodeA", time.Now(), []byte(`{"a":1}`), "")
	// emit complete
	emitToolCompleteEvent(context.Background(), toolCompleteEventConfig{EventChan: ch, InvocationID: "inv-id", ToolName: "echo", ToolID: "id-1", NodeID: "nodeA", StartTime: time.Now(), Result: map[string]any{"x": 1}, Error: nil, Arguments: []byte(`{"a":1}`)})
	// ensure two events were sent
	e1 := <-ch
	e2 := <-ch
	require.NotNil(t, e1)
	require.NotNil(t, e2)

	// executeSingleToolCall success
	ctx := context.Background()
	tracer := oteltrace.NewNoopTracerProvider().Tracer("test")
	_, span := tracer.Start(ctx, "span")
	defer span.End()
	// tool call
	args := []byte(`{"a":1}`)
	msg, err := executeSingleToolCall(ctx, singleToolCallConfig{
		ToolCall:      model.ToolCall{ID: "tid", Function: model.FunctionDefinitionParam{Name: "echo", Arguments: args}},
		Tools:         map[string]tool.Tool{"echo": &dummyTool{name: "echo"}},
		InvocationID:  "inv",
		EventChan:     ch,
		Span:          span,
		ToolCallbacks: nil,
		State:         State{StateKeyCurrentNodeID: "nodeA"},
	})
	require.NoError(t, err)
	require.Equal(t, model.RoleTool, msg.Role)
	require.Equal(t, "tid", msg.ToolID)
	require.Equal(t, "echo", msg.ToolName)

	// executeSingleToolCall error - tool not found
	_, err = executeSingleToolCall(ctx, singleToolCallConfig{ToolCall: model.ToolCall{ID: "tid2", Function: model.FunctionDefinitionParam{Name: "notfound"}}, Tools: map[string]tool.Tool{}, InvocationID: "inv2", EventChan: ch, Span: span, State: State{}})
	require.Error(t, err)
}

// interruptTool is a dummy tool that always interrupts.
type interruptTool struct{ name string }

func (it *interruptTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: it.name}
}

func (it *interruptTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return nil, NewInterruptError("ask?")
}

// When a tool interrupts, executeSingleToolCall should:
// - emit a tool complete event WITHOUT an error payload, and
// - return the interrupt error (not wrapped).
func TestExecuteSingleToolCall_Interrupt_NoErrorInEvent(t *testing.T) {
	ch := make(chan *event.Event, 4)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("test-int")
	ctx, span := tracer.Start(context.Background(), "span")
	defer span.End()

	// Configure a tool call that yields an interrupt.
	args := []byte(`{"x":1}`)
	cfg := singleToolCallConfig{
		ToolCall: model.ToolCall{ID: "tid",
			Function: model.FunctionDefinitionParam{
				Name:      "interrupt",
				Arguments: args,
			}},
		Tools: map[string]tool.Tool{
			"interrupt": &interruptTool{name: "interrupt"},
		},
		InvocationID: "inv-int",
		EventChan:    ch,
		Span:         span,
		State:        State{StateKeyCurrentNodeID: "nodeInt"},
	}

	_, err := executeSingleToolCall(ctx, cfg)
	require.Error(t, err)
	require.True(t, IsInterruptError(err))
	if intr, ok := GetInterruptError(err); ok {
		require.Equal(t, "ask?", intr.Value)
	}

	// Drain events; find the tool complete event and assert no error.
	var complete *event.Event
	drain := len(ch)
	for i := 0; i < drain; i++ {
		e := <-ch
		if e == nil || e.StateDelta == nil {
			continue
		}
		if b, ok := e.StateDelta[MetadataKeyTool]; ok {
			var meta ToolExecutionMetadata
			if json.Unmarshal(b, &meta) == nil &&
				meta.Phase == ToolExecutionPhaseComplete {
				complete = e
				// Meta.Error must be empty for interrupts
				require.Equal(t, "", meta.Error)
				break
			}
		}
	}
	require.NotNil(t, complete, "expected tool complete event")
	// Response.Error must be nil for interrupt tool completes.
	if complete.Response != nil {
		require.Nil(t, complete.Response.Error)
	}
}

// A tool that returns a normal error should produce an error payload in the
// tool complete event and return a wrapped error from executeSingleToolCall.
type errorTool struct{ name string }

func (et *errorTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: et.name}
}

func (et *errorTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return nil, fmt.Errorf("boom")
}

func TestExecuteSingleToolCall_Error_ErrorInEvent(t *testing.T) {
	ch := make(chan *event.Event, 4)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("test-err")
	ctx, span := tracer.Start(context.Background(), "span")
	defer span.End()

	args := []byte(`{"x":1}`)
	cfg := singleToolCallConfig{
		ToolCall: model.ToolCall{ID: "tid2",
			Function: model.FunctionDefinitionParam{
				Name:      "errtool",
				Arguments: args,
			}},
		Tools: map[string]tool.Tool{
			"errtool": &errorTool{name: "errtool"},
		},
		InvocationID: "inv-err",
		EventChan:    ch,
		Span:         span,
		State:        State{StateKeyCurrentNodeID: "nodeErr"},
	}

	_, err := executeSingleToolCall(ctx, cfg)
	require.Error(t, err)
	// Should not be an interrupt error
	require.False(t, IsInterruptError(err))

	// Find the tool complete event and assert error surfaced.
	var complete *event.Event
	drain := len(ch)
	for i := 0; i < drain; i++ {
		e := <-ch
		if e == nil || e.StateDelta == nil {
			continue
		}
		if b, ok := e.StateDelta[MetadataKeyTool]; ok {
			var meta ToolExecutionMetadata
			if json.Unmarshal(b, &meta) == nil &&
				meta.Phase == ToolExecutionPhaseComplete {
				complete = e
				require.NotEqual(t, "", meta.Error)
				break
			}
		}
	}
	require.NotNil(t, complete)
	require.NotNil(t, complete.Response)
	require.NotNil(t, complete.Response.Error)
}

func TestExtractToolCallbacks_NotFound(t *testing.T) {
	_, ok := extractToolCallbacks(State{})
	require.False(t, ok)
}

func TestAgentEvents_Emit(t *testing.T) {
	ch := make(chan *event.Event, 10)
	start := time.Now()
	emitAgentStartEvent(context.Background(), ch, "inv", "nodeZ", start)
	emitAgentCompleteEvent(context.Background(), ch, "inv", "nodeZ", start, time.Now())
	emitAgentErrorEvent(context.Background(), ch, "inv", "nodeZ", start, time.Now(), assert.AnError)
	// three events
	require.NotNil(t, <-ch)
	require.NotNil(t, <-ch)
	require.NotNil(t, <-ch)
}

func TestDeepCopy_FallbackPaths(t *testing.T) {
	// Non-JSON-marshalable value triggers fallback paths
	m := map[string]any{"bad": make(chan int)}
	copied := deepCopyMap(m)
	// Ensure we still have the key
	require.Contains(t, copied, "bad")
}

func TestDeepCopyMap_Normal(t *testing.T) {
	m := map[string]any{"a": 1, "b": []any{1, 2}}
	c := deepCopyMap(m)
	require.Equal(t, 2, len(c))
}

func TestCheckpointTree_OrphanNodeAsRoot(t *testing.T) {
	saver := inMemorySaverForGraph()
	cm := NewCheckpointManager(saver)
	ctx := context.Background()
	ln := "ln-tree"
	ns := "ns"
	// Root
	_, err := cm.CreateCheckpoint(ctx, CreateCheckpointConfig(ln, "", ns), State{"r": 1}, CheckpointSourceInput, -1)
	require.NoError(t, err)
	// Orphan checkpoint whose ParentCheckpointID refers to non-existent
	orphan := NewCheckpoint(map[string]any{"x": 1}, map[string]int64{"x": 1}, nil)
	orphan.ParentCheckpointID = "unknown-parent"
	_, err = saver.Put(ctx, PutRequest{Config: CreateCheckpointConfig(ln, "", ns), Checkpoint: orphan, Metadata: NewCheckpointMetadata(CheckpointSourceUpdate, 0), NewVersions: map[string]int64{"x": 1}})
	require.NoError(t, err)
	tree, err := cm.GetCheckpointTree(ctx, ln)
	require.NoError(t, err)
	require.NotNil(t, tree)
	require.GreaterOrEqual(t, len(tree.Branches), 2)
}

func TestListChildren_ParentNotFound(t *testing.T) {
	saver := inMemorySaverForGraph()
	cm := NewCheckpointManager(saver)
	ctx := context.Background()
	_, err := cm.ListChildren(ctx, CreateCheckpointConfig("ln-none", "not-exist", "ns"))
	require.Error(t, err)
}

// helper: in-memory saver alias from subpackage
func inMemorySaverForGraph() CheckpointSaver { return newMockSaver() }

// Extra tests for runTool branches
type notCallableTool struct{ tool.Tool }

func (n *notCallableTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "nc", Description: "not callable"}
}

func TestRunTool_CallbackShortCircuitAndErrors(t *testing.T) {
	ctx := context.Background()
	tdecl := &dummyTool{name: "echo"}
	call := model.ToolCall{ID: "id", Function: model.FunctionDefinitionParam{Name: "echo", Arguments: []byte(`{"x":1}`)}}

	// Before callback returns custom result
	cbs := tool.NewCallbacks().RegisterBeforeTool(func(ctx context.Context, toolName string, d *tool.Declaration, args *[]byte) (any, error) {
		return map[string]any{"short": true}, nil
	})
	_, res, _, err := runTool(ctx, call, cbs, tdecl)
	require.NoError(t, err)
	m, _ := res.(map[string]any)
	require.Equal(t, true, m["short"])

	// Before callback returns error
	cbs2 := tool.NewCallbacks().RegisterBeforeTool(func(ctx context.Context, toolName string, d *tool.Declaration, args *[]byte) (any, error) {
		return nil, assert.AnError
	})
	_, _, _, err = runTool(ctx, call, cbs2, tdecl)
	require.Error(t, err)

	// After callback returns custom result
	cbs3 := tool.NewCallbacks().RegisterAfterTool(func(ctx context.Context, toolName string, d *tool.Declaration, args []byte, result any, runErr error) (any, error) {
		return map[string]any{"override": true}, nil
	})
	_, res, _, err = runTool(ctx, call, cbs3, tdecl)
	require.NoError(t, err)
	m2, _ := res.(map[string]any)
	require.Equal(t, true, m2["override"])

	// Not callable tool
	_, _, _, err = runTool(ctx, call, nil, &notCallableTool{})
	require.Error(t, err)
}

// AddAgentNode builder coverage
func TestStateGraph_AddAgentNode_Build(t *testing.T) {
	g, err := NewStateGraph(NewStateSchema()).
		AddAgentNode("agent1").
		SetEntryPoint("agent1").
		SetFinishPoint("agent1").
		Compile()
	require.NoError(t, err)
	require.NotNil(t, g)
}

// dummy agent to test buildAgentInvocation
type dummyAgent struct{ name string }

func (d *dummyAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}
func (d *dummyAgent) Tools() []tool.Tool                   { return nil }
func (d *dummyAgent) Info() agent.Info                     { return agent.Info{Name: d.name} }
func (d *dummyAgent) SubAgents() []agent.Agent             { return nil }
func (d *dummyAgent) FindSubAgent(name string) agent.Agent { return nil }

func TestBuildAgentInvocation(t *testing.T) {
	d := &dummyAgent{name: "ag"}
	s := &session.Session{ID: "s1"}
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(d),
		agent.WithInvocationID("inv-x"),
		agent.WithInvocationSession(s),
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	exec := &ExecutionContext{InvocationID: "inv-x"}
	st := State{StateKeyUserInput: "hello", StateKeySession: s, StateKeyExecContext: exec}
	newInv := buildAgentInvocation(ctx, st, d)
	require.NotNil(t, newInv)
	require.Equal(t, "ag", newInv.AgentName)
	require.Equal(t, "hello", newInv.Message.Content)

	newInv = buildAgentInvocation(context.Background(), st, d)
	require.NotNil(t, newInv)
	require.Equal(t, "ag", newInv.AgentName)
	require.Equal(t, "hello", newInv.Message.Content)

}

// Additional manager and builder coverage using configurable fake saver
type fakeSaver2 struct {
	put      func(context.Context, PutRequest) (map[string]any, error)
	putFull  func(context.Context, PutFullRequest) (map[string]any, error)
	get      func(context.Context, map[string]any) (*Checkpoint, error)
	getTuple func(context.Context, map[string]any) (*CheckpointTuple, error)
	list     func(context.Context, map[string]any, *CheckpointFilter) ([]*CheckpointTuple, error)
	del      func(context.Context, string) error
}

func (f fakeSaver2) Put(ctx context.Context, req PutRequest) (map[string]any, error) {
	if f.put != nil {
		return f.put(ctx, req)
	}
	return map[string]any{}, nil
}
func (f fakeSaver2) PutWrites(context.Context, PutWritesRequest) error { return nil }
func (f fakeSaver2) PutFull(ctx context.Context, req PutFullRequest) (map[string]any, error) {
	if f.putFull != nil {
		return f.putFull(ctx, req)
	}
	return map[string]any{}, nil
}
func (f fakeSaver2) Get(ctx context.Context, cfg map[string]any) (*Checkpoint, error) {
	if f.get != nil {
		return f.get(ctx, cfg)
	}
	return nil, nil
}
func (f fakeSaver2) GetTuple(ctx context.Context, cfg map[string]any) (*CheckpointTuple, error) {
	if f.getTuple != nil {
		return f.getTuple(ctx, cfg)
	}
	return nil, nil
}
func (f fakeSaver2) List(ctx context.Context, cfg map[string]any, filter *CheckpointFilter) ([]*CheckpointTuple, error) {
	if f.list != nil {
		return f.list(ctx, cfg, filter)
	}
	return nil, nil
}
func (f fakeSaver2) DeleteLineage(ctx context.Context, id string) error {
	if f.del != nil {
		return f.del(ctx, id)
	}
	return nil
}
func (f fakeSaver2) Close() error { return nil }

func TestGetLineageID_And_GetResumeMap_Branches(t *testing.T) {
	if GetLineageID(nil) != "" {
		t.Fatalf("expected empty lineage id")
	}
	if GetResumeMap(nil) != nil {
		t.Fatalf("expected nil resume map")
	}
	if GetLineageID(map[string]any{"x": 1}) != "" {
		t.Fatalf("expected empty lineage id")
	}
	if GetResumeMap(map[string]any{"x": 1}) != nil {
		t.Fatalf("expected nil resume map")
	}
	if GetLineageID(map[string]any{CfgKeyConfigurable: map[string]any{}}) != "" {
		t.Fatalf("expected empty lineage id")
	}
	if GetResumeMap(map[string]any{CfgKeyConfigurable: map[string]any{}}) != nil {
		t.Fatalf("expected nil resume map")
	}
}

func TestCheckpointManager_CreateCheckpoint_Success(t *testing.T) {
	called := false
	cm := NewCheckpointManager(fakeSaver2{put: func(ctx context.Context, req PutRequest) (map[string]any, error) {
		called = true
		if req.Checkpoint == nil || req.Metadata == nil {
			t.Fatalf("missing data")
		}
		return map[string]any{"ok": true}, nil
	}})
	st := State{"a": 1, "b": "x"}
	ck, err := cm.CreateCheckpoint(context.Background(), map[string]any{"k": "v"}, st, CheckpointSourceInput, 0)
	if err != nil || ck == nil || !called {
		t.Fatalf("unexpected: %v %v %v", ck, err, called)
	}
}

func TestCheckpointManager_Latest_Success(t *testing.T) {
	expect := &CheckpointTuple{Checkpoint: NewCheckpoint(map[string]any{"a": 1}, map[string]int64{}, nil)}
	cm := NewCheckpointManager(fakeSaver2{list: func(context.Context, map[string]any, *CheckpointFilter) ([]*CheckpointTuple, error) {
		return []*CheckpointTuple{expect}, nil
	}})
	tup, err := cm.Latest(context.Background(), "ln", "ns")
	if err != nil || tup != expect {
		t.Fatalf("latest failed: %v %v", tup, err)
	}
}

func TestCheckpointManager_BranchFrom_ErrorsAndSuccess(t *testing.T) {
	cm := NewCheckpointManager(fakeSaver2{getTuple: func(context.Context, map[string]any) (*CheckpointTuple, error) {
		return nil, fmt.Errorf("get source err")
	}})
	if _, err := cm.BranchFrom(context.Background(), "ln", "ns", "cid", "ns2"); err == nil {
		t.Fatalf("expected error")
	}
	cm = NewCheckpointManager(fakeSaver2{getTuple: func(context.Context, map[string]any) (*CheckpointTuple, error) { return nil, nil }})
	if _, err := cm.BranchFrom(context.Background(), "ln", "ns", "cid", "ns2"); err == nil {
		t.Fatalf("expected not found")
	}
	src := &CheckpointTuple{Checkpoint: NewCheckpoint(map[string]any{}, map[string]int64{}, nil), Metadata: NewCheckpointMetadata(CheckpointSourceInput, 3)}
	cm = NewCheckpointManager(fakeSaver2{getTuple: func(context.Context, map[string]any) (*CheckpointTuple, error) { return src, nil }, putFull: func(context.Context, PutFullRequest) (map[string]any, error) { return nil, fmt.Errorf("putfull") }})
	if _, err := cm.BranchFrom(context.Background(), "ln", "ns", "cid", "ns2"); err == nil {
		t.Fatalf("expected putfull error")
	}
	cm = NewCheckpointManager(fakeSaver2{getTuple: func(context.Context, map[string]any) (*CheckpointTuple, error) { return src, nil }, putFull: func(context.Context, PutFullRequest) (map[string]any, error) { return map[string]any{"ok": true}, nil }})
	out, err := cm.BranchFrom(context.Background(), "ln", "ns", "cid", "ns2")
	if err != nil || out == nil || out.Config["ok"] != true {
		t.Fatalf("unexpected: %v %v", out, err)
	}
}

func TestCheckpointManager_BranchToNewLineage_Variants(t *testing.T) {
	src := NewCheckpoint(map[string]any{}, map[string]int64{}, nil)
	src.Timestamp = time.Now().Add(-time.Minute)
	cm := NewCheckpointManager(fakeSaver2{getTuple: func(context.Context, map[string]any) (*CheckpointTuple, error) {
		return &CheckpointTuple{Checkpoint: src, Metadata: NewCheckpointMetadata(CheckpointSourceInput, 2)}, nil
	}, put: func(context.Context, PutRequest) (map[string]any, error) { return map[string]any{"ok": true}, nil }})
	if out, err := cm.BranchToNewLineage(context.Background(), "srcLn", "", "", "newLn", "ns"); err != nil || out == nil || out.Config["ok"] != true {
		t.Fatalf("expected success: %v %v", out, err)
	}
	cm = NewCheckpointManager(fakeSaver2{getTuple: func(context.Context, map[string]any) (*CheckpointTuple, error) { return nil, nil }})
	if _, err := cm.BranchToNewLineage(context.Background(), "srcLn", "nsA", "cidX", "newLn", "nsB"); err == nil {
		t.Fatalf("expected not found")
	}
}

func TestCheckpointManager_ResumeFromLatest_Variants(t *testing.T) {
	cm := NewCheckpointManager(fakeSaver2{list: func(context.Context, map[string]any, *CheckpointFilter) ([]*CheckpointTuple, error) {
		return nil, fmt.Errorf("list err")
	}})
	if _, err := cm.ResumeFromLatest(context.Background(), "ln", "ns", &Command{}); err == nil {
		t.Fatalf("expected error")
	}
	cm = NewCheckpointManager(fakeSaver2{list: func(context.Context, map[string]any, *CheckpointFilter) ([]*CheckpointTuple, error) {
		return []*CheckpointTuple{}, nil
	}})
	if _, err := cm.ResumeFromLatest(context.Background(), "ln", "ns", nil); err == nil {
		t.Fatalf("expected none found error")
	}
	ck := NewCheckpoint(map[string]any{"a": 1}, map[string]int64{}, nil)
	cm = NewCheckpointManager(fakeSaver2{list: func(context.Context, map[string]any, *CheckpointFilter) ([]*CheckpointTuple, error) {
		return []*CheckpointTuple{{Checkpoint: ck}}, nil
	}})
	st, err := cm.ResumeFromLatest(context.Background(), "ln", "ns", &Command{Resume: "v"})
	if err != nil || st[StateKeyCommand] == nil {
		t.Fatalf("expected command in state: %v %v", st, err)
	}
}

func TestCheckpointManager_ResumeFromCheckpoint_NilCheckpointError(t *testing.T) {
	cm := NewCheckpointManager(fakeSaver2{getTuple: func(context.Context, map[string]any) (*CheckpointTuple, error) {
		return &CheckpointTuple{Checkpoint: nil}, nil
	}})
	if _, err := cm.ResumeFromCheckpoint(context.Background(), map[string]any{}); err == nil {
		t.Fatalf("expected error when tuple.Checkpoint is nil")
	}
}

func TestExtractToolCallsFromState_SuccessAndErrors(t *testing.T) {
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	_, span := tracer.Start(context.Background(), "s")
	// success: assistant with tool calls at end
	calls := []model.ToolCall{{ID: "tid", Function: model.FunctionDefinitionParam{Name: "echo"}}}
	msgs := []model.Message{model.NewUserMessage("hi"), {Role: model.RoleAssistant, ToolCalls: calls}}
	tc, err := extractToolCallsFromState(State{StateKeyMessages: msgs}, span)
	require.NoError(t, err)
	require.Equal(t, 1, len(tc))
	// error: no messages
	_, err = extractToolCallsFromState(State{}, span)
	require.Error(t, err)
	// error: user encountered before assistant with tool calls
	msgs2 := []model.Message{model.NewUserMessage("hi")}
	_, err = extractToolCallsFromState(State{StateKeyMessages: msgs2}, span)
	require.Error(t, err)
}

func TestProcessToolCalls(t *testing.T) {
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	_, span := tracer.Start(context.Background(), "s")
	dt := &dummyTool{name: "echo"}
	msgs, err := processToolCalls(context.Background(), toolCallsConfig{
		ToolCalls:    []model.ToolCall{{ID: "id", Function: model.FunctionDefinitionParam{Name: "echo", Arguments: []byte(`{"x":1}`)}}},
		Tools:        map[string]tool.Tool{"echo": dt},
		InvocationID: "inv",
		EventChan:    make(chan *event.Event, 10),
		Span:         span,
		State:        State{StateKeyCurrentNodeID: "n"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, len(msgs))
	require.Equal(t, model.RoleTool, msgs[0].Role)
}

func TestEmitToolCompleteEvent_WithError(t *testing.T) {
	ch := make(chan *event.Event, 10)
	emitToolCompleteEvent(context.Background(), toolCompleteEventConfig{EventChan: ch, InvocationID: "inv", ToolName: "t", ToolID: "id", NodeID: "n", StartTime: time.Now(), Result: nil, Error: fmt.Errorf("err"), Arguments: []byte(`{}`)})
	e := <-ch
	require.NotNil(t, e)
}

func TestMessageReducer(t *testing.T) {
	// nil existing, append message
	out := MessageReducer(nil, model.NewAssistantMessage("x"))
	msgs, _ := out.([]model.Message)
	require.Equal(t, 1, len(msgs))
	// ops
	out2 := MessageReducer([]model.Message{}, []MessageOp{AppendMessages{Items: []model.Message{model.NewUserMessage("u")}}})
	msgs2, _ := out2.([]model.Message)
	require.Equal(t, 1, len(msgs2))
}

// Tools node function end-to-end
func TestNewToolsNodeFunc_SuccessAndError(t *testing.T) {
	// Build state with assistant tool calls and execution context
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	_, span := tracer.Start(context.Background(), "s")
	_ = span // span used implicitly in functions called

	calls := []model.ToolCall{{ID: "tid", Function: model.FunctionDefinitionParam{Name: "echo", Arguments: []byte(`{"x":1}`)}}}
	msgs := []model.Message{model.NewUserMessage("hi"), {Role: model.RoleAssistant, ToolCalls: calls}}
	exec := &ExecutionContext{InvocationID: "inv", EventChan: make(chan *event.Event, 2)}
	state := State{StateKeyMessages: msgs, StateKeyExecContext: exec, StateKeyCurrentNodeID: "N"}

	fn := NewToolsNodeFunc(map[string]tool.Tool{"echo": &dummyTool{name: "echo"}})
	out, err := fn(context.Background(), state)
	require.NoError(t, err)
	st, _ := out.(State)
	require.NotNil(t, st[StateKeyMessages])

	// Error: no messages in state
	_, err = fn(context.Background(), State{})
	require.Error(t, err)
}

// Agent node function success path using a parent that implements FindSubAgent
type parentWithSubAgent struct{ a agent.Agent }

func (p *parentWithSubAgent) FindSubAgent(name string) agent.Agent { return p.a }

func TestNewAgentNodeFunc_SuccessAndParentMissing(t *testing.T) {
	// Success path
	exec := &ExecutionContext{InvocationID: "inv", EventChan: make(chan *event.Event, 4)}
	state := State{StateKeyExecContext: exec, StateKeyCurrentNodeID: "agentNode", StateKeyParentAgent: &parentWithSubAgent{a: &dummyAgent{name: "child"}}}
	fn := NewAgentNodeFunc("child")
	out, err := fn(context.Background(), state)
	require.NoError(t, err)
	_, ok := out.(State)
	require.True(t, ok)

	// Parent missing
	_, err = fn(context.Background(), State{StateKeyExecContext: exec, StateKeyCurrentNodeID: "agentNode"})
	require.Error(t, err)
}

// Sub-agent not found path
func TestNewAgentNodeFunc_SubAgentNotFound(t *testing.T) {
	exec := &ExecutionContext{InvocationID: "inv", EventChan: make(chan *event.Event, 2)}
	state := State{StateKeyExecContext: exec, StateKeyCurrentNodeID: "agentNode", StateKeyParentAgent: &parentWithSubAgent{a: nil}}
	fn := NewAgentNodeFunc("unknown")
	_, err := fn(context.Background(), state)
	require.Error(t, err)
}

// Sub-agent Run returns error path
type errAgent struct{ name string }

func (e *errAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	return nil, fmt.Errorf("run err")
}
func (e *errAgent) Tools() []tool.Tool                   { return nil }
func (e *errAgent) Info() agent.Info                     { return agent.Info{Name: e.name} }
func (e *errAgent) SubAgents() []agent.Agent             { return nil }
func (e *errAgent) FindSubAgent(name string) agent.Agent { return nil }

func TestNewAgentNodeFunc_RunError(t *testing.T) {
	exec := &ExecutionContext{InvocationID: "inv", EventChan: make(chan *event.Event, 4)}
	state := State{StateKeyExecContext: exec, StateKeyCurrentNodeID: "agentNode", StateKeyParentAgent: &parentWithSubAgent{a: &errAgent{name: "child"}}}
	fn := NewAgentNodeFunc("child")
	_, err := fn(context.Background(), state)
	require.Error(t, err)
}

// Dummy model to test one-shot stage
type dummyModel struct{}

func (d *dummyModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("ok")}}}
	close(ch)
	return ch, nil
}
func (d *dummyModel) Info() model.Info { return model.Info{Name: "dummy"} }

func TestLLMRunner_ExecuteOneShotStage(t *testing.T) {
	r := &llmRunner{llmModel: &dummyModel{}, instruction: "inst", tools: nil, nodeID: "node1"}
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	_, span := tracer.Start(context.Background(), "s")
	st, err := r.executeOneShotStage(context.Background(), State{}, []model.Message{model.NewUserMessage("hi")}, span)
	require.NoError(t, err)
	s, _ := st.(State)
	// last_response should be set
	require.NotNil(t, s[StateKeyLastResponse])
}

func TestLLMRunner_ExecuteUserInputAndHistoryStages(t *testing.T) {
	r := &llmRunner{llmModel: &dummyModel{}, instruction: "inst", tools: nil, nodeID: "node1"}
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	_, span := tracer.Start(context.Background(), "s")
	// user input stage
	st1, err := r.executeUserInputStage(context.Background(), State{StateKeyMessages: []model.Message{}, StateKeyUserInput: "hello"}, "hello", span)
	require.NoError(t, err)
	s1, _ := st1.(State)
	require.NotNil(t, s1[StateKeyLastResponse])
	// history stage (no user input, has history)
	st2, err := r.executeHistoryStage(context.Background(), State{StateKeyMessages: []model.Message{model.NewUserMessage("a")}}, span)
	require.NoError(t, err)
	s2, _ := st2.(State)
	require.NotNil(t, s2[StateKeyLastResponse])
}

// dummy model returning no responses
type emptyModel struct{}

func (e *emptyModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}
func (e *emptyModel) Info() model.Info { return model.Info{Name: "empty"} }

type emptyChoicesModel struct{}

func (e *emptyChoicesModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{}
	close(ch)
	return ch, nil
}

func (e *emptyChoicesModel) Info() model.Info {
	return model.Info{Name: "empty_choices"}
}

func TestExecuteModelWithEvents_NoResponseError(t *testing.T) {
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	_, span := tracer.Start(context.Background(), "s")
	_, err := executeModelWithEvents(context.Background(), modelExecutionConfig{
		ModelCallbacks: nil,
		LLMModel:       &emptyModel{},
		Request:        &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}},
		EventChan:      make(chan *event.Event, 1),
		InvocationID:   "inv",
		SessionID:      "sid",
		AppName:        "app",
		UserID:         "user",
		Span:           span,
		NodeID:         "n",
	})
	require.Error(t, err)
}

func TestExecuteModelWithEvents_NoChoicesError(t *testing.T) {
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	_, span := tracer.Start(context.Background(), "s")
	_, err := executeModelWithEvents(context.Background(),
		modelExecutionConfig{
			ModelCallbacks: nil,
			LLMModel:       &emptyChoicesModel{},
			Request: &model.Request{
				Messages: []model.Message{
					model.NewUserMessage("hi"),
				},
			},
			EventChan:    make(chan *event.Event, 1),
			InvocationID: "inv",
			SessionID:    "sid",
			AppName:      "app",
			UserID:       "user",
			Span:         span,
			NodeID:       "n",
		})
	require.Error(t, err)
}

func TestRunModel_BeforeModelCustomResponse(t *testing.T) {
	// Callbacks return custom response, dummy model should not be called
	cbs := model.NewCallbacks().RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		return &model.Response{Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("custom")}}}, nil
	})
	_, ch, err := runModel(context.Background(), cbs, &dummyModel{}, &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
	require.NoError(t, err)
	rsp := <-ch
	require.NotNil(t, rsp)
	require.Equal(t, "custom", rsp.Choices[0].Message.Content)
}

func TestProcessModelResponse_EventAndErrors(t *testing.T) {
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	_, span := tracer.Start(context.Background(), "s")
	// Event emission path
	evch := make(chan *event.Event, 1)
	rsp := &model.Response{Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("ok")}}}
	_, _, err := processModelResponse(context.Background(), modelResponseConfig{
		Response:       rsp,
		ModelCallbacks: nil,
		EventChan:      evch,
		InvocationID:   "inv",
		SessionID:      "sid",
		LLMModel:       &dummyModel{},
		Request:        &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}},
		Span:           span,
		NodeID:         "nodeX",
	})
	require.NoError(t, err)
	require.NotNil(t, <-evch)

	// Model API error path
	errRsp := &model.Response{Error: &model.ResponseError{Message: "boom"}}
	_, _, err = processModelResponse(context.Background(), modelResponseConfig{
		Response:     errRsp,
		EventChan:    make(chan *event.Event, 1),
		InvocationID: "inv",
		SessionID:    "sid",
		LLMModel:     &dummyModel{},
		Request:      &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}},
		Span:         span,
	})
	require.Error(t, err)

	// Context done path when sending event
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// unbuffered channel; since ctx already canceled, select should choose ctx.Done
	_, _, err = processModelResponse(ctx, modelResponseConfig{
		Response:     rsp,
		EventChan:    make(chan *event.Event),
		InvocationID: "inv",
		SessionID:    "sid",
		LLMModel:     &dummyModel{},
		Request:      &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}},
		Span:         span,
	})
	require.Error(t, err)
}

func TestProcessModelResponse_DoneWithContentEmitsEvent(t *testing.T) {
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	_, span := tracer.Start(context.Background(), "s")
	evch := make(chan *event.Event, 1)
	rsp := &model.Response{Done: true, Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("ok")}}}
	_, _, err := processModelResponse(context.Background(), modelResponseConfig{
		Response:     rsp,
		EventChan:    evch,
		InvocationID: "inv",
		SessionID:    "sid",
		LLMModel:     &dummyModel{},
		Request:      &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}},
		Span:         span,
	})
	require.NoError(t, err)
	select {
	case <-evch:
		// Expected: Done responses with meaningful content should be emitted.
	default:
		t.Fatalf("expected event when Done=true and has content")
	}
}

func TestProcessModelResponse_DoneWithoutContentSkipsEvent(t *testing.T) {
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	_, span := tracer.Start(context.Background(), "s")
	evch := make(chan *event.Event, 1)
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
			},
		}},
	}
	_, _, err := processModelResponse(context.Background(), modelResponseConfig{
		Response:     rsp,
		EventChan:    evch,
		InvocationID: "inv",
		SessionID:    "sid",
		LLMModel:     &dummyModel{},
		Request:      &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}},
		Span:         span,
	})
	require.NoError(t, err)
	select {
	case <-evch:
		t.Fatalf("expected no event when Done=true and content is empty")
	default:
	}
}

func TestShouldEmitModelResponse_Cases(t *testing.T) {
	const (
		reasoning = "reasoning"
		errType   = "test_error"
		errMsg    = "boom"
		content   = "content"
	)

	t.Run("nil response", func(t *testing.T) {
		require.False(t, shouldEmitModelResponse(nil))
	})

	t.Run("error response", func(t *testing.T) {
		rsp := &model.Response{
			Error: &model.ResponseError{
				Type:    errType,
				Message: errMsg,
			},
		}
		require.True(t, shouldEmitModelResponse(rsp))
	})

	t.Run("valid content", func(t *testing.T) {
		rsp := &model.Response{
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage(content),
			}},
		}
		require.True(t, shouldEmitModelResponse(rsp))
	})

	t.Run("reasoning content in message", func(t *testing.T) {
		rsp := &model.Response{
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:             model.RoleAssistant,
					ReasoningContent: reasoning,
				},
			}},
		}
		require.True(t, shouldEmitModelResponse(rsp))
	})

	t.Run("reasoning content in delta", func(t *testing.T) {
		rsp := &model.Response{
			Choices: []model.Choice{{
				Index: 0,
				Delta: model.Message{
					Role:             model.RoleAssistant,
					ReasoningContent: reasoning,
				},
			}},
		}
		require.True(t, shouldEmitModelResponse(rsp))
	})
}

func TestProcessModelResponse_AfterModelCustomResponse(t *testing.T) {
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	_, span := tracer.Start(context.Background(), "s")
	// AfterModel returns custom response
	cbs := model.NewCallbacks().RegisterAfterModel(func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
		return &model.Response{Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("after")}}}, nil
	})
	evch := make(chan *event.Event, 1)
	_, _, err := processModelResponse(context.Background(), modelResponseConfig{
		Response:       &model.Response{Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("ok")}}},
		ModelCallbacks: cbs,
		EventChan:      evch,
		InvocationID:   "inv",
		SessionID:      "sid",
		LLMModel:       &dummyModel{},
		Request:        &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}},
		Span:           span,
	})
	require.NoError(t, err)
}

// Model that streams tool calls then final
type toolCallStreamModel struct{}

func (m *toolCallStreamModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 2)
	ch <- &model.Response{Choices: []model.Choice{{Index: 0, Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "t1", Function: model.FunctionDefinitionParam{Name: "echo"}}}}}}}
	ch <- &model.Response{Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("done")}}}
	close(ch)
	return ch, nil
}
func (m *toolCallStreamModel) Info() model.Info { return model.Info{Name: "toolcall"} }

func TestExecuteModelWithEvents_ToolCallsMerged(t *testing.T) {
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	_, span := tracer.Start(context.Background(), "s")
	res, err := executeModelWithEvents(context.Background(), modelExecutionConfig{
		ModelCallbacks: nil,
		LLMModel:       &toolCallStreamModel{},
		Request:        &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}},
		EventChan:      make(chan *event.Event, 2),
		InvocationID:   "inv",
		SessionID:      "sid",
		AppName:        "app",
		UserID:         "user",
		Span:           span,
		NodeID:         "node",
	})
	require.NoError(t, err)
	r := res.(*model.Response)
	require.GreaterOrEqual(t, len(r.Choices[0].Message.ToolCalls), 1)
}

func TestExtractModelInput_Combinations(t *testing.T) {
	// both instruction and user input
	s := extractModelInput(State{StateKeyUserInput: "hi"}, "inst")
	require.Contains(t, s, "inst")
	require.Contains(t, s, "hi")
	// only instruction
	s2 := extractModelInput(State{}, "inst")
	require.Equal(t, "inst", s2)
	// only user input
	s3 := extractModelInput(State{StateKeyUserInput: "hi"}, "")
	require.Equal(t, "hi", s3)
}

func TestFindSubAgentByName_NoProvider(t *testing.T) {
	na := findSubAgentByName(struct{}{}, "child")
	require.Nil(t, na)
}

func TestExtractToolCallbacks_Found(t *testing.T) {
	cbs := tool.NewCallbacks()
	st := State{StateKeyToolCallbacks: cbs}
	got, ok := extractToolCallbacks(st)
	require.True(t, ok)
	require.Equal(t, cbs, got)
}

func TestMessageReducer_NilUpdateAndFallback(t *testing.T) {
	existing := []model.Message{model.NewUserMessage("u")}
	// nil update returns existing
	out := MessageReducer(existing, nil)
	msgs, _ := out.([]model.Message)
	require.Equal(t, 1, len(msgs))
	// fallback: unsupported type returns update as-is
	out2 := MessageReducer(existing, 123)
	require.Equal(t, 123, out2)
}

func TestMessageReducer_AppendDirectAndSlice(t *testing.T) {
	msgs := []model.Message{}
	// append single message
	out1 := MessageReducer(msgs, model.NewAssistantMessage("a"))
	list1, _ := out1.([]model.Message)
	require.Equal(t, 1, len(list1))
	// append slice
	out2 := MessageReducer(list1, []model.Message{model.NewUserMessage("u")})
	list2, _ := out2.([]model.Message)
	require.Equal(t, 2, len(list2))
}

func TestEnsureSystemHead_And_ExtractExecutionContext(t *testing.T) {
	// ensureSystemHead: empty sys
	in := []model.Message{model.NewUserMessage("u")}
	out := ensureSystemHead(in, "")
	require.Equal(t, in, out)
	// ensureSystemHead: with sys when first not system
	out2 := ensureSystemHead(in, "sys")
	require.Equal(t, model.RoleSystem, out2[0].Role)
	// extractExecutionContext: only execctx
	exec := &ExecutionContext{InvocationID: "inv", EventChan: make(chan *event.Event, 1)}
	inv, sess, appName, userID, ch := extractExecutionContext(State{StateKeyExecContext: exec})
	require.Equal(t, "inv", inv)
	require.Equal(t, "", sess)
	require.Equal(t, "", appName)
	require.Equal(t, "", userID)
	require.NotNil(t, ch)
	// extractExecutionContext: only session
	s := &session.Session{ID: "sid", AppName: "app", UserID: "user"}
	inv2, sess2, appName2, userID2, ch2 := extractExecutionContext(State{StateKeySession: s})
	require.Equal(t, "", inv2)
	require.Equal(t, "sid", sess2)
	require.Equal(t, "app", appName2)
	require.Equal(t, "user", userID2)
	require.Nil(t, ch2)
}

func TestReducers_FallbackTypes(t *testing.T) {
	// AppendReducer wrong type -> update returned
	out := AppendReducer(123, []any{1})
	require.Equal(t, []any{1}, out)
	// StringSliceReducer wrong type -> update returned
	out2 := StringSliceReducer(123, []string{"a"})
	require.Equal(t, []string{"a"}, out2)
}

func TestReducers_AppendAndStringSlice(t *testing.T) {
	// AppendReducer merge
	out := AppendReducer([]any{1}, []any{2, 3})
	require.ElementsMatch(t, []any{1, 2, 3}, out.([]any))
	out = AppendReducer(nil, []any{4})
	require.ElementsMatch(t, []any{4}, out.([]any))
	// StringSliceReducer merge
	out2 := StringSliceReducer([]string{"a"}, []string{"b"})
	require.ElementsMatch(t, []string{"a", "b"}, out2.([]string))
	out2 = StringSliceReducer(nil, []string{"c"})
	require.ElementsMatch(t, []string{"c"}, out2.([]string))
}

type gpErrSaver struct{}

func (m *gpErrSaver) Get(ctx context.Context, config map[string]any) (*Checkpoint, error) {
	return nil, nil
}
func (m *gpErrSaver) GetTuple(ctx context.Context, config map[string]any) (*CheckpointTuple, error) {
	return nil, assert.AnError
}
func (m *gpErrSaver) List(ctx context.Context, config map[string]any, filter *CheckpointFilter) ([]*CheckpointTuple, error) {
	return nil, nil
}
func (m *gpErrSaver) Put(ctx context.Context, req PutRequest) (map[string]any, error) {
	return nil, nil
}
func (m *gpErrSaver) PutWrites(ctx context.Context, req PutWritesRequest) error { return nil }
func (m *gpErrSaver) PutFull(ctx context.Context, req PutFullRequest) (map[string]any, error) {
	return nil, nil
}
func (m *gpErrSaver) DeleteLineage(ctx context.Context, lineageID string) error { return nil }
func (m *gpErrSaver) Close() error                                              { return nil }

func TestCheckpointManager_GetParent_SaverError(t *testing.T) {
	cm := NewCheckpointManager(&gpErrSaver{})
	_, err := cm.GetParent(context.Background(), CreateCheckpointConfig("ln", "id", "ns"))
	require.Error(t, err)
}

func TestCheckpointManager_Put_NilSaverError(t *testing.T) {
	cm := NewCheckpointManager(nil)
	_, err := cm.Put(context.Background(), PutRequest{Config: CreateCheckpointConfig("ln", "", "ns"), Checkpoint: NewCheckpoint(nil, nil, nil), Metadata: NewCheckpointMetadata(CheckpointSourceUpdate, 0), NewVersions: map[string]int64{}})
	require.Error(t, err)
}

func TestMessageReducer_MessageOpSingle(t *testing.T) {
	msgs := []model.Message{model.NewUserMessage("u")}
	out := MessageReducer(msgs, AppendMessages{Items: []model.Message{model.NewAssistantMessage("a")}})
	res, _ := out.([]model.Message)
	require.Equal(t, 2, len(res))
}

func TestExtractAssistantMessage(t *testing.T) {
	// nil
	require.Nil(t, extractAssistantMessage(nil))
	// wrong type
	require.Nil(t, extractAssistantMessage("not a response"))
	// response with choices
	rsp := &model.Response{Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("hello")}}}
	msg := extractAssistantMessage(rsp)
	require.NotNil(t, msg)
	require.Equal(t, "hello", msg.Content)
}

func TestProcessModelResponse_AfterModelError(t *testing.T) {
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	_, span := tracer.Start(context.Background(), "s")
	cbs := model.NewCallbacks().RegisterAfterModel(func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
		return nil, assert.AnError
	})
	_, _, err := processModelResponse(context.Background(), modelResponseConfig{
		Response:       &model.Response{Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("ok")}}},
		ModelCallbacks: cbs,
		EventChan:      make(chan *event.Event, 1),
		InvocationID:   "inv",
		SessionID:      "sid",
		LLMModel:       &dummyModel{},
		Request:        &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}},
		Span:           span,
	})
	require.Error(t, err)
}

type errModel struct{}

func (e *errModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	return nil, assert.AnError
}
func (e *errModel) Info() model.Info { return model.Info{Name: "err"} }

func TestRunModel_GenerateContentError(t *testing.T) {
	_, _, err := runModel(context.Background(), nil, &errModel{}, &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
	require.Error(t, err)
}

// TestRunModel_CallbackContextPropagation tests that context values set in
// BeforeModel callback can be retrieved in AfterModel callback through processModelResponse.
func TestRunModel_CallbackContextPropagation(t *testing.T) {
	type contextKey string
	const testKey contextKey = "test-key"
	const testValue = "test-value-from-before"

	// Create callbacks that set and read context values.
	callbacks := model.NewCallbacks()
	var capturedValue any

	// BeforeModel callback sets a context value.
	callbacks.RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		ctxWithValue := context.WithValue(ctx, testKey, testValue)
		return &model.BeforeModelResult{
			Context: ctxWithValue,
		}, nil
	})

	// AfterModel callback reads the context value.
	callbacks.RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
		capturedValue = ctx.Value(testKey)
		return nil, nil
	})

	// Create a dummy model that returns a response.
	dummyModel := &dummyModel{}

	// Run the model.
	ctx := context.Background()
	request := &model.Request{
		Messages: []model.Message{model.NewUserMessage("test")},
	}

	ctx, responseChan, err := runModel(ctx, callbacks, dummyModel, request)
	require.NoError(t, err)

	// Process the response to trigger AfterModel callback.
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	_, span := tracer.Start(ctx, "s")
	defer span.End()

	// Consume all responses.
	for response := range responseChan {
		// Process each response to trigger AfterModel callback.
		_, _, err = processModelResponse(ctx, modelResponseConfig{
			Response:       response,
			ModelCallbacks: callbacks,
			EventChan:      make(chan *event.Event, 1),
			InvocationID:   "test-inv",
			SessionID:      "test-session",
			LLMModel:       dummyModel,
			Request:        request,
			Span:           span,
		})
		require.NoError(t, err)
	}

	// Verify that the context value was captured in AfterModel callback.
	require.Equal(t, testValue, capturedValue, "context value should be propagated from BeforeModel to AfterModel")
}

// TestRunTool_CallbackContextPropagation tests that context values set in
// BeforeTool callback can be retrieved in AfterTool callback.
func TestRunTool_CallbackContextPropagation(t *testing.T) {
	type contextKey string
	const testKey contextKey = "test-key"
	const testValue = "test-value-from-before"

	// Create callbacks that set and read context values.
	callbacks := tool.NewCallbacks()
	var capturedValue any

	// BeforeTool callback sets a context value.
	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		ctxWithValue := context.WithValue(ctx, testKey, testValue)
		return &tool.BeforeToolResult{
			Context: ctxWithValue,
		}, nil
	})

	// AfterTool callback reads the context value.
	callbacks.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		capturedValue = ctx.Value(testKey)
		return nil, nil
	})

	// Create a dummy tool that returns a result.
	dummyTool := &dummyTool{name: "echo"}
	toolCall := model.ToolCall{
		ID:       "id",
		Function: model.FunctionDefinitionParam{Name: "echo", Arguments: []byte(`{"x":1}`)},
	}

	// Run the tool.
	ctx := context.Background()
	ctx, result, _, err := runTool(ctx, toolCall, callbacks, dummyTool)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify that the context value was captured in AfterTool callback.
	require.Equal(t, testValue, capturedValue, "context value should be propagated from BeforeTool to AfterTool")
}

// Mock saver to cover GetParent fallback cross-namespace path
type gpMockSaver struct {
	tuple  *CheckpointTuple
	parent *CheckpointTuple
}

func (m *gpMockSaver) Get(ctx context.Context, config map[string]any) (*Checkpoint, error) {
	return nil, nil
}
func (m *gpMockSaver) GetTuple(ctx context.Context, config map[string]any) (*CheckpointTuple, error) {
	// First call: current tuple by ID
	if GetCheckpointID(config) == m.tuple.Checkpoint.ID {
		return m.tuple, nil
	}
	// If ParentConfig (wrong ns) provided, return nil to force cross-ns fallback
	if GetCheckpointID(config) == m.parent.Checkpoint.ID && GetNamespace(config) == "wrong-ns" {
		return nil, nil
	}
	// Cross-namespace fallback uses empty ns
	if GetCheckpointID(config) == m.parent.Checkpoint.ID && GetNamespace(config) == "" {
		return m.parent, nil
	}
	return nil, nil
}
func (m *gpMockSaver) List(ctx context.Context, config map[string]any, filter *CheckpointFilter) ([]*CheckpointTuple, error) {
	return nil, nil
}
func (m *gpMockSaver) Put(ctx context.Context, req PutRequest) (map[string]any, error) {
	return nil, nil
}
func (m *gpMockSaver) PutWrites(ctx context.Context, req PutWritesRequest) error { return nil }
func (m *gpMockSaver) PutFull(ctx context.Context, req PutFullRequest) (map[string]any, error) {
	return nil, nil
}
func (m *gpMockSaver) DeleteLineage(ctx context.Context, lineageID string) error { return nil }
func (m *gpMockSaver) Close() error                                              { return nil }

func TestCheckpointManager_GetParent_FallbackCrossNS(t *testing.T) {
	// Current tuple with ParentConfig pointing to wrong namespace
	parent := &Checkpoint{ID: "p1", Timestamp: time.Now().UTC()}
	current := &Checkpoint{ID: "c1", Timestamp: time.Now().UTC(), ParentCheckpointID: parent.ID}
	currentTuple := &CheckpointTuple{Checkpoint: current, ParentConfig: CreateCheckpointConfig("ln", parent.ID, "wrong-ns")}
	parentTuple := &CheckpointTuple{Checkpoint: parent}
	saver := &gpMockSaver{tuple: currentTuple, parent: parentTuple}
	cm := NewCheckpointManager(saver)
	got, err := cm.GetParent(context.Background(), CreateCheckpointConfig("ln", current.ID, "ns"))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, parent.ID, got.Checkpoint.ID)
}

// Saver returns tuple with no parent id
type gpNoParentSaver struct{}

func (m *gpNoParentSaver) Get(ctx context.Context, config map[string]any) (*Checkpoint, error) {
	return nil, nil
}
func (m *gpNoParentSaver) GetTuple(ctx context.Context, config map[string]any) (*CheckpointTuple, error) {
	return &CheckpointTuple{Checkpoint: &Checkpoint{ID: "c", ParentCheckpointID: ""}}, nil
}
func (m *gpNoParentSaver) List(ctx context.Context, config map[string]any, filter *CheckpointFilter) ([]*CheckpointTuple, error) {
	return nil, nil
}
func (m *gpNoParentSaver) Put(ctx context.Context, req PutRequest) (map[string]any, error) {
	return nil, nil
}
func (m *gpNoParentSaver) PutWrites(ctx context.Context, req PutWritesRequest) error { return nil }
func (m *gpNoParentSaver) PutFull(ctx context.Context, req PutFullRequest) (map[string]any, error) {
	return nil, nil
}
func (m *gpNoParentSaver) DeleteLineage(ctx context.Context, lineageID string) error { return nil }
func (m *gpNoParentSaver) Close() error                                              { return nil }

func TestCheckpointManager_GetParent_NoParent(t *testing.T) {
	cm := NewCheckpointManager(&gpNoParentSaver{})
	got, err := cm.GetParent(context.Background(), CreateCheckpointConfig("ln", "c", "ns"))
	require.NoError(t, err)
	require.Nil(t, got)
}
