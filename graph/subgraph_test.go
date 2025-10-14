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
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// inspectAgent inspects the child's RuntimeState and emits a custom event that
// encodes presence of selected keys. Then it emits a terminal graph completion
// event to allow parent node to capture RawStateDelta/FinalState when needed.
type inspectAgent struct{ name string }

func (a *inspectAgent) Info() agent.Info                     { return agent.Info{Name: a.name} }
func (a *inspectAgent) Tools() []tool.Tool                   { return nil }
func (a *inspectAgent) SubAgents() []agent.Agent             { return nil }
func (a *inspectAgent) FindSubAgent(name string) agent.Agent { return nil }

func (a *inspectAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
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
	// Internal keys should be filtered from child's RuntimeState
	require.False(t, found["has_exec_context"]) // exec_context must be filtered
	require.False(t, found["has_session"])      // session is provided via Invocation.Session, not RuntimeState
	require.False(t, found["has_current_node"]) // current node id is internal
	require.False(t, found["has_parent_agent"]) // parent agent must be filtered
	// Custom key should survive
	require.True(t, found["has_custom_runtime"])
}

// Verify SubgraphOutputMapper receives RawStateDelta from the subgraph's terminal event.
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
	fn := NewAgentNodeFunc("child2", WithSubgraphOutputMapper(func(parent State, r SubgraphResult) State {
		// Final graph.execution event carries serialized final state only.
		_, ok := r.RawStateDelta["child_done"]
		return State{"raw_has_child_done": ok}
	}))
	out, err := fn(context.Background(), state)
	require.NoError(t, err)
	st, _ := out.(State)
	require.Equal(t, true, st["raw_has_child_done"])
}
