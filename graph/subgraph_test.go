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
