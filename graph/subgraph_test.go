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
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// inspectAgent inspects the child's runtime state and emits a custom event.
// Then it emits a terminal graph completion event so the parent can capture
// RawStateDelta/FinalState.
type inspectAgent struct{ name string }

func (a *inspectAgent) Info() agent.Info { return agent.Info{Name: a.name} }
func (a *inspectAgent) Tools() []tool.Tool {
	return nil
}
func (a *inspectAgent) SubAgents() []agent.Agent {
	return nil
}
func (a *inspectAgent) FindSubAgent(name string) agent.Agent {
	_ = name
	return nil
}

func (a *inspectAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		// First, emit an inspection event with booleans for selected keys.
		st := inv.RunOptions.RuntimeState
		flags := map[string]bool{
			"has_exec_context":   st[StateKeyExecContext] != nil,
			"has_session":        st[StateKeySession] != nil,
			"has_current_node":   st[StateKeyCurrentNodeID] != nil,
			"has_parent_agent":   st[StateKeyParentAgent] != nil,
			"has_custom_runtime": st["foo"] != nil,
		}
		b, _ := json.Marshal(flags)
		e := event.New(inv.InvocationID, a.name, event.WithObject("test.inspect"))
		e.StateDelta = map[string][]byte{"inspect": b}
		ch <- e

		// Then, emit a terminal graph completion event with a tiny final state.
		done := NewGraphCompletionEvent(
			WithCompletionEventInvocationID(inv.InvocationID),
			WithCompletionEventFinalState(State{"child_done": true}),
		)
		ch <- done
		close(ch)
	}()
	return ch, nil
}

// messageEchoAgent emits the invocation message content for verification.
type messageEchoAgent struct{ name string }

func (a *messageEchoAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}
func (a *messageEchoAgent) Tools() []tool.Tool {
	return nil
}
func (a *messageEchoAgent) SubAgents() []agent.Agent {
	return nil
}
func (a *messageEchoAgent) FindSubAgent(name string) agent.Agent {
	_ = name
	return nil
}

func (a *messageEchoAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		// Emit the message content as a state delta for parent to inspect.
		e := event.New(inv.InvocationID, a.name, event.WithObject("test.msg"))
		if inv != nil && inv.Message.Content != "" {
			e.StateDelta = map[string][]byte{"msg": []byte(inv.Message.Content)}
		}
		ch <- e
		// Emit terminal graph completion event to close the stream.
		done := NewGraphCompletionEvent(
			WithCompletionEventInvocationID(inv.InvocationID),
			WithCompletionEventFinalState(State{"child_done": true}),
		)
		ch <- done
		close(ch)
	}()
	return ch, nil
}

type stateValueAgent struct{ name string }

func (a *stateValueAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}
func (a *stateValueAgent) Tools() []tool.Tool {
	return nil
}
func (a *stateValueAgent) SubAgents() []agent.Agent {
	return nil
}
func (a *stateValueAgent) FindSubAgent(name string) agent.Agent {
	_ = name
	return nil
}

func (a *stateValueAgent) Run(
	_ context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		invocationID := testEmptyString
		userInput := testEmptyString
		if inv != nil {
			invocationID = inv.InvocationID
			userInput = inv.Message.Content
		}
		childValue := testChildValuePrefix + userInput
		done := NewGraphCompletionEvent(
			WithCompletionEventInvocationID(invocationID),
			WithCompletionEventFinalState(State{
				testChildValueKey:    childValue,
				StateKeyLastResponse: childValue,
			}),
		)
		ch <- done
		close(ch)
	}()
	return ch, nil
}

const (
	testEmptyString        = ""
	testChildAgentName     = "child_handoff"
	testChildValuePrefix   = "computed: "
	testChildValueKey      = "child_value"
	testValueFromChildKey  = "value_from_child"
	testAfterNodeID        = "after"
	testInvocationID       = "inv-handoff"
	testUserInput          = "hello"
	testMissingStateKeyFmt = "missing state key: %s"
)

func TestSubgraph_OutputMapper_HandoffToNextNode(t *testing.T) {
	child := &stateValueAgent{name: testChildAgentName}
	parent := &parentWithSubAgent{a: child}

	schema := NewStateSchema()
	schema.AddField(
		StateKeyLastResponse,
		StateField{Type: reflect.TypeOf(testEmptyString)},
	)
	schema.AddField(
		testValueFromChildKey,
		StateField{Type: reflect.TypeOf(testEmptyString)},
	)
	schema.AddField(
		StateKeyUserInput,
		StateField{Type: reflect.TypeOf(testEmptyString)},
	)

	parentGraph, err := NewStateGraph(schema).
		AddAgentNode(
			testChildAgentName,
			WithSubgraphOutputMapper(func(_ State, r SubgraphResult) State {
				value, ok := GetStateValue[string](
					r.FinalState,
					testChildValueKey,
				)
				if !ok {
					return nil
				}
				return State{testValueFromChildKey: value}
			}),
		).
		AddNode(testAfterNodeID, func(_ context.Context, state State) (any, error) {
			value, ok := GetStateValue[string](state, testValueFromChildKey)
			if !ok {
				return nil, fmt.Errorf(testMissingStateKeyFmt, testValueFromChildKey)
			}
			return State{StateKeyLastResponse: value}, nil
		}).
		AddEdge(testChildAgentName, testAfterNodeID).
		SetEntryPoint(testChildAgentName).
		SetFinishPoint(testAfterNodeID).
		Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(parentGraph)
	require.NoError(t, err)

	inv := agent.NewInvocation(agent.WithInvocationID(testInvocationID))
	initial := State{
		StateKeyParentAgent: parent,
		StateKeyUserInput:   testUserInput,
	}
	eventChan, err := exec.Execute(context.Background(), initial, inv)
	require.NoError(t, err)

	var done *event.Event
	for ev := range eventChan {
		if ev != nil && ev.Done && ev.Object == ObjectTypeGraphExecution {
			done = ev
		}
	}
	require.NotNil(t, done)

	expected := testChildValuePrefix + testUserInput

	var got string
	require.NotNil(t, done.StateDelta)
	raw, ok := done.StateDelta[testValueFromChildKey]
	require.True(t, ok)
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, expected, got)

	require.NotNil(t, done.Response)
	require.Len(t, done.Choices, 1)
	require.Equal(t, expected, done.Choices[0].Message.Content)
}

// TestSubgraph_InputFromLastResponse_MapsUserInput verifies that enabling
// WithSubgraphInputFromLastResponse maps parent's last_response to child
// invocation's user input. When last_response is empty, it falls back to the
// original user_input.
func TestSubgraph_InputFromLastResponse_MapsUserInput(t *testing.T) {
	// Case 1: last_response present; child sees it as message content.
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-map", EventChan: ch}
	child := &messageEchoAgent{name: "child3"}
	parent := &parentWithSubAgent{a: child}
	state := State{
		StateKeyExecContext:   exec,
		StateKeyCurrentNodeID: "agentNode",
		StateKeyParentAgent:   parent,
		StateKeyUserInput:     "original-user-input",
		StateKeyLastResponse:  "from-upstream-A",
	}
	fn := NewAgentNodeFunc("child3", WithSubgraphInputFromLastResponse())
	_, err := fn(context.Background(), state)
	require.NoError(t, err)

	var msg string
	for i := 0; i < 8; i++ {
		select {
		case ev := <-ch:
			if ev != nil && ev.Object == "test.msg" && ev.StateDelta != nil {
				if raw, ok := ev.StateDelta["msg"]; ok {
					msg = string(raw)
				}
			}
		default:
		}
	}
	require.Equal(t, "from-upstream-A", msg)

	// Case 2: last_response empty → falls back to original user_input
	ch2 := make(chan *event.Event, 8)
	exec2 := &ExecutionContext{InvocationID: "inv-fallback", EventChan: ch2}
	state2 := State{
		StateKeyExecContext:   exec2,
		StateKeyCurrentNodeID: "agentNode",
		StateKeyParentAgent:   parent,
		StateKeyUserInput:     "original-user-input",
		// no last_response here
	}
	fn2 := NewAgentNodeFunc("child3", WithSubgraphInputFromLastResponse())
	_, err = fn2(context.Background(), state2)
	require.NoError(t, err)

	var msg2 string
	for i := 0; i < 8; i++ {
		select {
		case ev := <-ch2:
			if ev != nil && ev.Object == "test.msg" && ev.StateDelta != nil {
				if raw, ok := ev.StateDelta["msg"]; ok {
					msg2 = string(raw)
				}
			}
		default:
		}
	}
	require.Equal(t, "original-user-input", msg2)
}

func TestSubgraph_CustomUserInputKey_UsesAndClears(t *testing.T) {
	const (
		testCustomInputKey = "custom_input"
		testCustomInput    = "override-input"
	)

	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-custom", EventChan: ch}
	child := &messageEchoAgent{name: "child-custom"}
	parent := &parentWithSubAgent{a: child}
	state := State{
		StateKeyExecContext:   exec,
		StateKeyCurrentNodeID: "agentNode",
		StateKeyParentAgent:   parent,
		StateKeyUserInput:     "original-user-input",
		testCustomInputKey:    testCustomInput,
	}

	fn := NewAgentNodeFunc(
		"child-custom",
		WithUserInputKey(testCustomInputKey),
	)
	out, err := fn(context.Background(), state)
	require.NoError(t, err)

	var msg string
	for i := 0; i < 8; i++ {
		select {
		case ev := <-ch:
			if ev != nil && ev.Object == "test.msg" && ev.StateDelta != nil {
				if raw, ok := ev.StateDelta["msg"]; ok {
					msg = string(raw)
				}
			}
		default:
		}
	}
	require.Equal(t, testCustomInput, msg)

	updated, ok := out.(State)
	require.True(t, ok)
	require.Equal(t, "", updated[testCustomInputKey])
	_, clearsDefaultKey := updated[StateKeyUserInput]
	require.False(t, clearsDefaultKey)
}

func TestSubgraph_CustomUserInputKey_InputFromLastResponse(t *testing.T) {
	const (
		testCustomInputKey = "custom_input"
	)

	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-custom-last", EventChan: ch}
	child := &messageEchoAgent{name: "child-custom-last"}
	parent := &parentWithSubAgent{a: child}
	state := State{
		StateKeyExecContext:   exec,
		StateKeyCurrentNodeID: "agentNode",
		StateKeyParentAgent:   parent,
		StateKeyUserInput:     "original-user-input",
		testCustomInputKey:    "custom-input",
		StateKeyLastResponse:  "from-last-response",
	}

	fn := NewAgentNodeFunc(
		"child-custom-last",
		WithUserInputKey(testCustomInputKey),
		WithSubgraphInputFromLastResponse(),
	)
	out, err := fn(context.Background(), state)
	require.NoError(t, err)

	var msg string
	for i := 0; i < 8; i++ {
		select {
		case ev := <-ch:
			if ev != nil && ev.Object == "test.msg" && ev.StateDelta != nil {
				if raw, ok := ev.StateDelta["msg"]; ok {
					msg = string(raw)
				}
			}
		default:
		}
	}
	require.Equal(t, "from-last-response", msg)

	updated, ok := out.(State)
	require.True(t, ok)
	require.Equal(t, "", updated[testCustomInputKey])
	_, clearsDefaultKey := updated[StateKeyUserInput]
	require.False(t, clearsDefaultKey)
}

// Verify the default child runtime state copy filters internal keys.
func TestSubgraph_DefaultRuntimeStateFiltersInternalKeys(t *testing.T) {
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-rt", EventChan: ch}
	parent := &parentWithSubAgent{a: &inspectAgent{name: "child"}}
	state := State{
		StateKeyExecContext:   exec,
		StateKeyCurrentNodeID: "agentNode",
		StateKeyParentAgent:   parent,
		StateKeySession:       &session.Session{ID: "s1"},
		StateKeyUserInput:     "hello",
		"foo":                 "bar",
	}
	fn := NewAgentNodeFunc("child")
	_, err := fn(context.Background(), state)
	require.NoError(t, err)

	// Drain events until we find our inspection event.
	var found map[string]bool
	for i := 0; i < 8; i++ {
		select {
		case ev := <-ch:
			if ev != nil && ev.Object == "test.inspect" && ev.StateDelta != nil {
				if raw, ok := ev.StateDelta["inspect"]; ok {
					_ = json.Unmarshal(raw, &found)
				}
			}
		default:
			// no more events immediately available
		}
	}
	require.NotNil(t, found)
	// Internal keys should be filtered from child's runtime state.
	require.False(t, found["has_exec_context"])
	// Session is provided via Invocation.Session, not runtime state.
	require.False(t, found["has_session"])
	require.False(t, found["has_current_node"])
	require.False(t, found["has_parent_agent"])
	// Custom key should survive
	require.True(t, found["has_custom_runtime"])
}

// Verify SubgraphOutputMapper receives RawStateDelta from the terminal event.
func TestSubgraph_OutputMapperGetsRawStateDelta(t *testing.T) {
	ch2 := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-raw", EventChan: ch2}
	child := &inspectAgent{name: "child2"}
	parent := &parentWithSubAgent{a: child}
	state := State{
		StateKeyExecContext:   exec,
		StateKeyCurrentNodeID: "agentNode",
		StateKeyParentAgent:   parent,
		StateKeyUserInput:     "go",
	}
	fn := NewAgentNodeFunc(
		"child2",
		WithSubgraphOutputMapper(func(parent State, r SubgraphResult) State {
			_ = parent
			// Final graph.execution event carries serialized final state only.
			_, ok := r.RawStateDelta["child_done"]
			return State{"raw_has_child_done": ok}
		}),
	)
	out, err := fn(context.Background(), state)
	require.NoError(t, err)
	st, _ := out.(State)
	require.Equal(t, true, st["raw_has_child_done"])
}

type subgraphTestSaver struct {
	mu     sync.Mutex
	byKey  map[string]*CheckpointTuple
	byFlow map[string][]string
}

func newSubgraphTestSaver() *subgraphTestSaver {
	return &subgraphTestSaver{
		byKey:  make(map[string]*CheckpointTuple),
		byFlow: make(map[string][]string),
	}
}

func (s *subgraphTestSaver) Get(
	ctx context.Context,
	config map[string]any,
) (*Checkpoint, error) {
	tuple, err := s.GetTuple(ctx, config)
	if err != nil || tuple == nil {
		return nil, err
	}
	return tuple.Checkpoint, nil
}

func (s *subgraphTestSaver) GetTuple(
	_ context.Context,
	config map[string]any,
) (*CheckpointTuple, error) {
	lineageID := GetLineageID(config)
	if lineageID == "" {
		return nil, nil
	}
	namespace := GetNamespace(config)
	checkpointID := GetCheckpointID(config)
	flowKey := lineageID + ":" + namespace

	s.mu.Lock()
	defer s.mu.Unlock()

	if checkpointID == "" {
		ids := s.byFlow[flowKey]
		if len(ids) == 0 {
			return nil, nil
		}
		checkpointID = ids[len(ids)-1]
	}
	key := flowKey + ":" + checkpointID
	return s.byKey[key], nil
}

func (s *subgraphTestSaver) List(
	_ context.Context,
	config map[string]any,
	filter *CheckpointFilter,
) ([]*CheckpointTuple, error) {
	lineageID := GetLineageID(config)
	if lineageID == "" {
		return nil, nil
	}
	namespace := GetNamespace(config)
	flowKey := lineageID + ":" + namespace

	s.mu.Lock()
	defer s.mu.Unlock()

	ids := s.byFlow[flowKey]
	limit := 0
	if filter != nil {
		limit = filter.Limit
	}
	out := make([]*CheckpointTuple, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		key := flowKey + ":" + ids[i]
		if tuple := s.byKey[key]; tuple != nil {
			out = append(out, tuple)
		}
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *subgraphTestSaver) Put(
	_ context.Context,
	req PutRequest,
) (map[string]any, error) {
	lineageID := GetLineageID(req.Config)
	namespace := GetNamespace(req.Config)
	cfg := CreateCheckpointConfig(lineageID, req.Checkpoint.ID, namespace)

	flowKey := lineageID + ":" + namespace
	key := flowKey + ":" + req.Checkpoint.ID

	s.mu.Lock()
	defer s.mu.Unlock()

	s.byKey[key] = &CheckpointTuple{
		Config:     cfg,
		Checkpoint: req.Checkpoint,
		Metadata:   req.Metadata,
	}
	s.byFlow[flowKey] = append(s.byFlow[flowKey], req.Checkpoint.ID)
	return cfg, nil
}

func (s *subgraphTestSaver) PutWrites(
	_ context.Context,
	_ PutWritesRequest,
) error {
	return nil
}

func (s *subgraphTestSaver) PutFull(
	ctx context.Context,
	req PutFullRequest,
) (map[string]any, error) {
	cfg, err := s.Put(ctx, PutRequest{
		Config:      req.Config,
		Checkpoint:  req.Checkpoint,
		Metadata:    req.Metadata,
		NewVersions: req.NewVersions,
	})
	if err != nil {
		return nil, err
	}

	lineageID := GetLineageID(cfg)
	namespace := GetNamespace(cfg)
	flowKey := lineageID + ":" + namespace
	key := flowKey + ":" + req.Checkpoint.ID

	pending := make([]PendingWrite, len(req.PendingWrites))
	copy(pending, req.PendingWrites)

	s.mu.Lock()
	if tuple := s.byKey[key]; tuple != nil {
		tuple.PendingWrites = pending
	}
	s.mu.Unlock()
	return cfg, nil
}

func (s *subgraphTestSaver) DeleteLineage(
	_ context.Context,
	_ string,
) error {
	return nil
}

func (s *subgraphTestSaver) Close() error { return nil }

type checkpointGraphAgent struct {
	name      string
	exec      *Executor
	subAgents []agent.Agent
}

func (a *checkpointGraphAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *checkpointGraphAgent) Tools() []tool.Tool { return nil }

func (a *checkpointGraphAgent) SubAgents() []agent.Agent {
	return a.subAgents
}

func (a *checkpointGraphAgent) FindSubAgent(name string) agent.Agent {
	for _, sub := range a.subAgents {
		if sub == nil {
			continue
		}
		if sub.Info().Name == name {
			return sub
		}
	}
	return nil
}

func (a *checkpointGraphAgent) Executor() *Executor { return a.exec }

func (a *checkpointGraphAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	initial := make(State)
	if inv != nil && inv.RunOptions.RuntimeState != nil {
		for k, v := range inv.RunOptions.RuntimeState {
			initial[k] = v
		}
	}
	initial[StateKeyParentAgent] = a
	if ns, ok := initial[CfgKeyCheckpointNS].(string); !ok || ns == "" {
		initial[CfgKeyCheckpointNS] = a.name
	}
	return a.exec.Execute(ctx, initial, inv)
}

func TestSubgraph_NestedInterruptResume(t *testing.T) {
	const (
		lineageID    = "ln-subgraph-interrupt"
		namespace    = "ns-subgraph-interrupt"
		childAgentID = "child"
		childNodeID  = "ask"
		stateKeyOut  = "answer"
		interruptMsg = "prompt"
		resumeValue  = "approved"
		resumeInvID  = "inv-resume"
	)

	schema := NewStateSchema()
	schema.AddField(
		stateKeyOut,
		StateField{Type: reflect.TypeOf(testEmptyString)},
	)

	saver := newSubgraphTestSaver()

	childGraph, err := NewStateGraph(schema).
		AddNode(childNodeID, func(ctx context.Context, s State) (any, error) {
			value, err := Interrupt(ctx, s, childNodeID, interruptMsg)
			if err != nil {
				return nil, err
			}
			v, _ := value.(string)
			return State{stateKeyOut: v}, nil
		}).
		SetEntryPoint(childNodeID).
		SetFinishPoint(childNodeID).
		Compile()
	require.NoError(t, err)

	childExec, err := NewExecutor(
		childGraph,
		WithCheckpointSaver(saver),
	)
	require.NoError(t, err)
	childAgent := &checkpointGraphAgent{
		name: childAgentID,
		exec: childExec,
	}

	parent := &parentWithSubAgent{a: childAgent}
	parentGraph, err := NewStateGraph(schema).
		AddAgentNode(
			childAgentID,
			WithSubgraphOutputMapper(func(_ State, r SubgraphResult) State {
				value, ok := GetStateValue[string](
					r.FinalState,
					stateKeyOut,
				)
				if !ok {
					return nil
				}
				return State{stateKeyOut: value}
			}),
		).
		SetEntryPoint(childAgentID).
		SetFinishPoint(childAgentID).
		Compile()
	require.NoError(t, err)

	parentExec, err := NewExecutor(
		parentGraph,
		WithCheckpointSaver(saver),
	)
	require.NoError(t, err)

	initial := State{
		StateKeyParentAgent: parent,
		CfgKeyLineageID:     lineageID,
		CfgKeyCheckpointNS:  namespace,
	}
	ch, err := parentExec.Execute(
		context.Background(),
		initial,
		agent.NewInvocation(agent.WithInvocationID(testInvocationID)),
	)
	require.NoError(t, err)
	for range ch {
	}

	cm := parentExec.CheckpointManager()
	require.NotNil(t, cm)
	parentTuples, err := cm.ListCheckpoints(
		context.Background(),
		CreateCheckpointConfig(lineageID, "", namespace),
		nil,
	)
	require.NoError(t, err)
	childTuples, err := cm.ListCheckpoints(
		context.Background(),
		CreateCheckpointConfig(lineageID, "", childAgentID),
		nil,
	)
	require.NoError(t, err)

	var parentInterrupt *CheckpointTuple
	var childInterrupt *CheckpointTuple
	for _, tuple := range parentTuples {
		if tuple == nil || tuple.Checkpoint == nil {
			continue
		}
		if tuple.Checkpoint.InterruptState == nil {
			continue
		}
		if tuple.Checkpoint.InterruptState.NodeID == childAgentID {
			parentInterrupt = tuple
		}
	}
	for _, tuple := range childTuples {
		if tuple == nil || tuple.Checkpoint == nil {
			continue
		}
		if tuple.Checkpoint.InterruptState == nil {
			continue
		}
		if tuple.Checkpoint.InterruptState.NodeID == childNodeID {
			childInterrupt = tuple
		}
	}
	if parentInterrupt == nil || childInterrupt == nil {
		var checkpoints []string
		for _, tuple := range parentTuples {
			if tuple == nil || tuple.Checkpoint == nil {
				continue
			}
			ck := tuple.Checkpoint
			meta := tuple.Metadata
			source := ""
			step := 0
			if meta != nil {
				source = meta.Source
				step = meta.Step
			}
			intr := ""
			if ck.InterruptState != nil {
				intr = fmt.Sprintf(
					"%s:%s",
					ck.InterruptState.NodeID,
					ck.InterruptState.TaskID,
				)
			}
			checkpoints = append(
				checkpoints,
				fmt.Sprintf(
					"id=%s step=%d source=%s intr=%s",
					ck.ID,
					step,
					source,
					intr,
				),
			)
		}
		for _, tuple := range childTuples {
			if tuple == nil || tuple.Checkpoint == nil {
				continue
			}
			ck := tuple.Checkpoint
			meta := tuple.Metadata
			source := ""
			step := 0
			if meta != nil {
				source = meta.Source
				step = meta.Step
			}
			intr := ""
			if ck.InterruptState != nil {
				intr = fmt.Sprintf(
					"%s:%s",
					ck.InterruptState.NodeID,
					ck.InterruptState.TaskID,
				)
			}
			checkpoints = append(
				checkpoints,
				fmt.Sprintf(
					"id=%s step=%d source=%s intr=%s",
					ck.ID,
					step,
					source,
					intr,
				),
			)
		}
		t.Fatalf("missing interrupt checkpoints: got=%v", checkpoints)
	}
	require.Equal(
		t,
		childNodeID,
		parentInterrupt.Checkpoint.InterruptState.TaskID,
	)

	values := parentInterrupt.Checkpoint.ChannelValues
	rawAny, ok := values[StateKeySubgraphInterrupt]
	require.True(t, ok)

	rawInfo, ok := rawAny.(map[string]any)
	require.True(t, ok)

	parentNode, _ := rawInfo[subgraphInterruptKeyParentNodeID].(string)
	require.Equal(t, childAgentID, parentNode)

	taskID, _ := rawInfo[subgraphInterruptKeyChildTaskID].(string)
	require.Equal(t, childNodeID, taskID)

	gotNS, _ := rawInfo[subgraphInterruptKeyChildCheckpointNS].(string)
	require.Equal(t, childAgentID, gotNS)

	gotLineage, _ := rawInfo[subgraphInterruptKeyChildLineageID].(string)
	require.Equal(t, lineageID, gotLineage)

	gotChildCkptID, _ :=
		rawInfo[subgraphInterruptKeyChildCheckpointID].(string)
	require.Equal(t, childInterrupt.Checkpoint.ID, gotChildCkptID)

	resume := State{
		StateKeyParentAgent: parent,
		CfgKeyLineageID:     lineageID,
		CfgKeyCheckpointNS:  namespace,
		CfgKeyCheckpointID:  parentInterrupt.Checkpoint.ID,
		StateKeyCommand: &Command{
			ResumeMap: map[string]any{
				childNodeID: resumeValue,
			},
		},
	}
	ch2, err := parentExec.Execute(
		context.Background(),
		resume,
		agent.NewInvocation(agent.WithInvocationID(resumeInvID)),
	)
	require.NoError(t, err)

	var done *event.Event
	for ev := range ch2 {
		if ev != nil && ev.Done && ev.Object == ObjectTypeGraphExecution {
			done = ev
		}
	}
	require.NotNil(t, done)
	raw, ok := done.StateDelta[stateKeyOut]
	require.True(t, ok)
	var got string
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, resumeValue, got)
}

func TestSubgraph_NestedInterruptResume_PreservesResumeMapKeys(t *testing.T) {
	const (
		lineageID          = "ln-subgraph-interrupt-preserve"
		namespace          = "ns-subgraph-interrupt-preserve"
		childAgentID       = "child_preserve"
		childNodeID        = "child_node"
		childInterruptKey  = "child_key"
		parentNodeID       = "parent_node"
		parentInterruptKey = "parent_key"
		stateKeyChildOut   = "child_answer"
		stateKeyParentOut  = "parent_answer"
		childPrompt        = "child_prompt"
		parentPrompt       = "parent_prompt"
		childResumeValue   = "child_ok"
		parentResumeValue  = "parent_ok"
		resumeInvID        = "inv-resume-preserve"
	)

	ctx := context.Background()

	schema := NewStateSchema()
	schema.AddField(
		stateKeyChildOut,
		StateField{Type: reflect.TypeOf(testEmptyString)},
	)
	schema.AddField(
		stateKeyParentOut,
		StateField{Type: reflect.TypeOf(testEmptyString)},
	)

	saver := newSubgraphTestSaver()

	childGraph, err := NewStateGraph(schema).
		AddNode(childNodeID, func(ctx context.Context, s State) (any, error) {
			value, err := Interrupt(
				ctx,
				s,
				childInterruptKey,
				childPrompt,
			)
			if err != nil {
				return nil, err
			}
			v, _ := value.(string)
			return State{stateKeyChildOut: v}, nil
		}).
		SetEntryPoint(childNodeID).
		SetFinishPoint(childNodeID).
		Compile()
	require.NoError(t, err)

	childExec, err := NewExecutor(childGraph, WithCheckpointSaver(saver))
	require.NoError(t, err)
	childAgent := &checkpointGraphAgent{
		name: childAgentID,
		exec: childExec,
	}

	parent := &parentWithSubAgent{a: childAgent}
	parentGraph, err := NewStateGraph(schema).
		AddAgentNode(
			childAgentID,
			WithSubgraphOutputMapper(func(_ State, r SubgraphResult) State {
				value, ok := GetStateValue[string](
					r.FinalState,
					stateKeyChildOut,
				)
				if !ok {
					return nil
				}
				return State{stateKeyChildOut: value}
			}),
		).
		AddNode(parentNodeID, func(ctx context.Context, s State) (any, error) {
			value, err := Interrupt(
				ctx,
				s,
				parentInterruptKey,
				parentPrompt,
			)
			if err != nil {
				return nil, err
			}
			v, _ := value.(string)
			return State{stateKeyParentOut: v}, nil
		}).
		AddEdge(childAgentID, parentNodeID).
		SetEntryPoint(childAgentID).
		SetFinishPoint(parentNodeID).
		Compile()
	require.NoError(t, err)

	parentExec, err := NewExecutor(parentGraph, WithCheckpointSaver(saver))
	require.NoError(t, err)

	initial := State{
		StateKeyParentAgent: parent,
		CfgKeyLineageID:     lineageID,
		CfgKeyCheckpointNS:  namespace,
	}
	ch, err := parentExec.Execute(
		ctx,
		initial,
		agent.NewInvocation(agent.WithInvocationID(testInvocationID)),
	)
	require.NoError(t, err)
	for range ch {
	}

	cm := parentExec.CheckpointManager()
	require.NotNil(t, cm)

	parentTuples, err := cm.ListCheckpoints(
		ctx,
		CreateCheckpointConfig(lineageID, "", namespace),
		nil,
	)
	require.NoError(t, err)

	var parentInterrupt *CheckpointTuple
	for _, tuple := range parentTuples {
		if tuple == nil || tuple.Checkpoint == nil {
			continue
		}
		if tuple.Checkpoint.InterruptState == nil {
			continue
		}
		if tuple.Checkpoint.InterruptState.NodeID == childAgentID {
			parentInterrupt = tuple
			break
		}
	}
	require.NotNil(t, parentInterrupt)

	resume := State{
		StateKeyParentAgent: parent,
		CfgKeyLineageID:     lineageID,
		CfgKeyCheckpointNS:  namespace,
		CfgKeyCheckpointID:  parentInterrupt.Checkpoint.ID,
		StateKeyCommand: &Command{
			ResumeMap: map[string]any{
				childInterruptKey:  childResumeValue,
				parentInterruptKey: parentResumeValue,
			},
		},
	}
	ch2, err := parentExec.Execute(
		ctx,
		resume,
		agent.NewInvocation(agent.WithInvocationID(resumeInvID)),
	)
	require.NoError(t, err)

	var done *event.Event
	for ev := range ch2 {
		if ev != nil && ev.Done && ev.Object == ObjectTypeGraphExecution {
			done = ev
		}
	}
	require.NotNil(t, done)

	var gotChild string
	raw, ok := done.StateDelta[stateKeyChildOut]
	require.True(t, ok)
	require.NoError(t, json.Unmarshal(raw, &gotChild))
	require.Equal(t, childResumeValue, gotChild)

	var gotParent string
	raw, ok = done.StateDelta[stateKeyParentOut]
	require.True(t, ok)
	require.NoError(t, json.Unmarshal(raw, &gotParent))
	require.Equal(t, parentResumeValue, gotParent)
}

func TestSubgraph_MultiLevelNestedInterruptResume(t *testing.T) {
	const (
		lineageID         = "ln-subgraph-interrupt-multi"
		namespace         = "ns-subgraph-interrupt-multi"
		childAgentID      = "child_multi"
		grandchildAgentID = "grandchild_multi"
		leafNodeID        = "ask"
		interruptKey      = "approval"
		stateKeyOut       = "answer"
		interruptMsg      = "prompt"
		resumeValue       = "approved"
		resumeInvID       = "inv-resume-multi"
	)

	ctx := context.Background()

	schema := NewStateSchema()
	schema.AddField(
		stateKeyOut,
		StateField{Type: reflect.TypeOf(testEmptyString)},
	)

	saver := newSubgraphTestSaver()

	grandchildGraph, err := NewStateGraph(schema).
		AddNode(leafNodeID, func(ctx context.Context, s State) (any, error) {
			value, err := Interrupt(ctx, s, interruptKey, interruptMsg)
			if err != nil {
				return nil, err
			}
			v, _ := value.(string)
			return State{stateKeyOut: v}, nil
		}).
		SetEntryPoint(leafNodeID).
		SetFinishPoint(leafNodeID).
		Compile()
	require.NoError(t, err)

	grandchildExec, err := NewExecutor(
		grandchildGraph,
		WithCheckpointSaver(saver),
	)
	require.NoError(t, err)
	grandchildAgent := &checkpointGraphAgent{
		name: grandchildAgentID,
		exec: grandchildExec,
	}

	childGraph, err := NewStateGraph(schema).
		AddAgentNode(
			grandchildAgentID,
			WithSubgraphOutputMapper(func(_ State, r SubgraphResult) State {
				value, ok := GetStateValue[string](r.FinalState, stateKeyOut)
				if !ok {
					return nil
				}
				return State{stateKeyOut: value}
			}),
		).
		SetEntryPoint(grandchildAgentID).
		SetFinishPoint(grandchildAgentID).
		Compile()
	require.NoError(t, err)

	childExec, err := NewExecutor(
		childGraph,
		WithCheckpointSaver(saver),
	)
	require.NoError(t, err)
	childAgent := &checkpointGraphAgent{
		name:      childAgentID,
		exec:      childExec,
		subAgents: []agent.Agent{grandchildAgent},
	}

	parent := &parentWithSubAgent{a: childAgent}
	parentGraph, err := NewStateGraph(schema).
		AddAgentNode(
			childAgentID,
			WithSubgraphOutputMapper(func(_ State, r SubgraphResult) State {
				value, ok := GetStateValue[string](r.FinalState, stateKeyOut)
				if !ok {
					return nil
				}
				return State{stateKeyOut: value}
			}),
		).
		SetEntryPoint(childAgentID).
		SetFinishPoint(childAgentID).
		Compile()
	require.NoError(t, err)

	parentExec, err := NewExecutor(
		parentGraph,
		WithCheckpointSaver(saver),
	)
	require.NoError(t, err)

	initial := State{
		StateKeyParentAgent: parent,
		CfgKeyLineageID:     lineageID,
		CfgKeyCheckpointNS:  namespace,
	}
	ch, err := parentExec.Execute(
		ctx,
		initial,
		agent.NewInvocation(agent.WithInvocationID(testInvocationID)),
	)
	require.NoError(t, err)
	for range ch {
	}

	cm := parentExec.CheckpointManager()
	require.NotNil(t, cm)

	parentTuples, err := cm.ListCheckpoints(
		ctx,
		CreateCheckpointConfig(lineageID, "", namespace),
		nil,
	)
	require.NoError(t, err)
	childTuples, err := cm.ListCheckpoints(
		ctx,
		CreateCheckpointConfig(lineageID, "", childAgentID),
		nil,
	)
	require.NoError(t, err)
	grandchildTuples, err := cm.ListCheckpoints(
		ctx,
		CreateCheckpointConfig(lineageID, "", grandchildAgentID),
		nil,
	)
	require.NoError(t, err)

	var parentInterrupt *CheckpointTuple
	for _, tuple := range parentTuples {
		if tuple == nil || tuple.Checkpoint == nil {
			continue
		}
		if tuple.Checkpoint.InterruptState == nil {
			continue
		}
		if tuple.Checkpoint.InterruptState.NodeID == childAgentID {
			parentInterrupt = tuple
			break
		}
	}
	require.NotNil(t, parentInterrupt)
	require.Equal(
		t,
		interruptKey,
		parentInterrupt.Checkpoint.InterruptState.TaskID,
	)

	var childInterrupt *CheckpointTuple
	for _, tuple := range childTuples {
		if tuple == nil || tuple.Checkpoint == nil {
			continue
		}
		if tuple.Checkpoint.InterruptState == nil {
			continue
		}
		if tuple.Checkpoint.InterruptState.NodeID == grandchildAgentID {
			childInterrupt = tuple
			break
		}
	}
	require.NotNil(t, childInterrupt)
	require.Equal(
		t,
		interruptKey,
		childInterrupt.Checkpoint.InterruptState.TaskID,
	)

	var grandchildInterrupt *CheckpointTuple
	for _, tuple := range grandchildTuples {
		if tuple == nil || tuple.Checkpoint == nil {
			continue
		}
		if tuple.Checkpoint.InterruptState == nil {
			continue
		}
		if tuple.Checkpoint.InterruptState.NodeID == leafNodeID {
			grandchildInterrupt = tuple
			break
		}
	}
	require.NotNil(t, grandchildInterrupt)
	require.Equal(
		t,
		interruptKey,
		grandchildInterrupt.Checkpoint.InterruptState.TaskID,
	)

	values := parentInterrupt.Checkpoint.ChannelValues
	rawAny, ok := values[StateKeySubgraphInterrupt]
	require.True(t, ok)

	rawInfo, ok := rawAny.(map[string]any)
	require.True(t, ok)

	gotNS, _ := rawInfo[subgraphInterruptKeyChildCheckpointNS].(string)
	require.Equal(t, childAgentID, gotNS)

	gotLineage, _ := rawInfo[subgraphInterruptKeyChildLineageID].(string)
	require.Equal(t, lineageID, gotLineage)

	taskID, _ := rawInfo[subgraphInterruptKeyChildTaskID].(string)
	require.Equal(t, interruptKey, taskID)

	gotChildCkptID, _ :=
		rawInfo[subgraphInterruptKeyChildCheckpointID].(string)
	require.Equal(t, childInterrupt.Checkpoint.ID, gotChildCkptID)

	resume := State{
		StateKeyParentAgent: parent,
		CfgKeyLineageID:     lineageID,
		CfgKeyCheckpointNS:  namespace,
		CfgKeyCheckpointID:  parentInterrupt.Checkpoint.ID,
		StateKeyCommand: &Command{
			ResumeMap: map[string]any{
				interruptKey: resumeValue,
			},
		},
	}
	ch2, err := parentExec.Execute(
		ctx,
		resume,
		agent.NewInvocation(agent.WithInvocationID(resumeInvID)),
	)
	require.NoError(t, err)

	var done *event.Event
	for ev := range ch2 {
		if ev != nil && ev.Done && ev.Object == ObjectTypeGraphExecution {
			done = ev
		}
	}
	require.NotNil(t, done)
	raw, ok := done.StateDelta[stateKeyOut]
	require.True(t, ok)
	var got string
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, resumeValue, got)
}
