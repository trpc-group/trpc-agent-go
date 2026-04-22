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

// This file hosts compatibility tests for issue #1432: NewAgentNodeFunc
// routes sub-agents through agent.RunWithPlugins so that Runner-scoped
// PluginManager AgentCallbacks consistently fire for sub-agents invoked
// through graph agent-nodes (matching chain/parallel/cycle/transfer).
//
// The tests are organized into groups:
//
//   - A: baseline — no plugins / no AgentCallbacks registered. These guard
//     the fast path (RunWithPlugins must fall through to ag.Run unchanged).
//   - B: BeforeAgent semantics (fires per sub-invocation, context
//     propagation, error, and CustomResponse short-circuit).
//   - C: AfterAgent semantics (receives FullResponseEvent, CustomResponse
//     appends a terminal event, errors become ErrorTypeAgentCallbackError
//     events, response-errors are surfaced as args.Error).
//   - D: co-existence with existing graph features (GraphCompletionEvent,
//     NodeCallbacks.AgentEvent, InterruptError through the wrapper).
//   - E: regression guard on Invocation.Clone inheriting Plugins.

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
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// -------- test fixtures ----------------------------------------------------

// recordingAgent records each Run invocation and can emit a scripted event
// sequence. It is intentionally minimal so that tests stay focused on the
// Run/RunWithPlugins boundary rather than graph semantics.
type recordingAgent struct {
	name string

	mu      sync.Mutex
	runs    int
	lastCtx context.Context
	lastInv *agent.Invocation

	// emit, if set, produces the events pushed on the channel. When nil, the
	// agent emits a single non-partial assistant response with content "orig".
	emit func(ctx context.Context, inv *agent.Invocation, out chan<- *event.Event)
}

func (a *recordingAgent) Info() agent.Info                     { return agent.Info{Name: a.name} }
func (a *recordingAgent) Tools() []tool.Tool                   { return nil }
func (a *recordingAgent) SubAgents() []agent.Agent             { return nil }
func (a *recordingAgent) FindSubAgent(name string) agent.Agent { _ = name; return nil }

func (a *recordingAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	a.mu.Lock()
	a.runs++
	a.lastCtx = ctx
	a.lastInv = inv
	a.mu.Unlock()

	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)
		if a.emit != nil {
			a.emit(ctx, inv, ch)
			return
		}
		rsp := &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage("orig"),
			}},
		}
		_ = agent.EmitEvent(
			ctx,
			inv,
			ch,
			event.NewResponseEvent(inv.InvocationID, a.name, rsp),
		)
	}()
	return ch, nil
}

func (a *recordingAgent) ranCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runs
}

func (a *recordingAgent) capturedInvocation() *agent.Invocation {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastInv
}

func (a *recordingAgent) capturedContext() context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastCtx
}

// newPluginManagerWithRegister builds a plugin.Manager with a single hookPlugin
// that registers the given callbacks. hookPlugin is defined in
// state_graph_ends_test.go.
func newPluginManagerWithRegister(reg func(r *plugin.Registry)) agent.PluginManager {
	return plugin.MustNewManager(&hookPlugin{name: "stage2", reg: reg})
}

// buildAgentNodeState builds the minimal parent graph state required by
// NewAgentNodeFunc(...)(ctx, state), plus a buffered event channel returned
// alongside so tests can inspect forwarded events.
func buildAgentNodeState(sub agent.Agent) (State, chan *event.Event) {
	ch := make(chan *event.Event, 16)
	exec := &ExecutionContext{InvocationID: "inv-stage2", EventChan: ch}
	state := State{
		StateKeyExecContext:   exec,
		StateKeyCurrentNodeID: "agentNode",
		StateKeyParentAgent:   &parentWithSubAgent{a: sub},
		StateKeyUserInput:     "hi",
	}
	return state, ch
}

// invokeAgentNode calls NewAgentNodeFunc(subName) with a parent invocation
// that carries the supplied PluginManager, and returns the result together
// with any events the node forwarded to the parent channel.
func invokeAgentNode(
	t *testing.T,
	sub agent.Agent,
	pm agent.PluginManager,
	opts ...Option,
) (any, error, []*event.Event) {
	t.Helper()
	state, ch := buildAgentNodeState(sub)

	invOpts := []agent.InvocationOptions{agent.WithInvocationID("parent-inv")}
	if pm != nil {
		invOpts = append(invOpts, agent.WithInvocationPlugins(pm))
	}
	parentInv := agent.NewInvocation(invOpts...)
	ctx := agent.NewInvocationContext(context.Background(), parentInv)

	fn := NewAgentNodeFunc(sub.Info().Name, opts...)
	out, err := fn(ctx, state)

	// fn returns only after processAgentEventStream drains the sub-agent
	// event channel, which already accounts for events appended by the
	// AfterAgent wrapper goroutine (close-on-completion). Drain the parent
	// event channel directly without a timing-dependent wait.
	events := drainEvents(ch)
	return out, err, events
}

func drainEvents(ch <-chan *event.Event) []*event.Event {
	var events []*event.Event
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return events
			}
			if e != nil {
				events = append(events, e)
			}
		default:
			return events
		}
	}
}

// -------- Group A: baseline (no plugins / no AgentCallbacks) --------------

// A1: when the parent invocation has no PluginManager, NewAgentNodeFunc must
// call targetAgent.Run exactly once and forward its events verbatim. This is
// the pre-fix behavior and must survive the stage-2 change.
func TestAgentNode_Stage2_NoPlugins_CallsRunDirectly(t *testing.T) {
	sub := &recordingAgent{name: "child"}
	out, err, _ := invokeAgentNode(t, sub, nil)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, 1, sub.ranCount(), "sub-agent Run must be invoked exactly once")
}

// A2: PluginManager present but without any AgentCallbacks registered must
// behave identically to the no-plugin case (RunWithPlugins' fast path).
func TestAgentNode_Stage2_PluginsWithoutAgentCallbacks_CallsRunDirectly(t *testing.T) {
	sub := &recordingAgent{name: "child"}
	pm := newPluginManagerWithRegister(func(r *plugin.Registry) {
		// Intentionally register no agent callbacks; a model callback is
		// registered instead to ensure the manager is non-empty but
		// AgentCallbacks() returns nil.
		r.BeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return nil, nil
		})
	})
	out, err, _ := invokeAgentNode(t, sub, pm)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, 1, sub.ranCount())
}

// -------- Group B: BeforeAgent ---------------------------------------------

// B1: BeforeAgent fires for the sub-agent, and its args carry the child
// invocation (not the parent/root invocation).
func TestAgentNode_Stage2_BeforeAgent_FiresWithSubInvocation(t *testing.T) {
	sub := &recordingAgent{name: "child"}
	var seenInv *agent.Invocation
	pm := newPluginManagerWithRegister(func(r *plugin.Registry) {
		r.BeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
			seenInv = args.Invocation
			return nil, nil
		})
	})

	_, err, _ := invokeAgentNode(t, sub, pm)
	require.NoError(t, err)
	require.Equal(t, 1, sub.ranCount())
	require.NotNil(t, seenInv)
	require.NotEqual(t, "parent-inv", seenInv.InvocationID,
		"BeforeAgent must see the child invocation, not the parent invocation")
	require.Equal(t, "child", seenInv.AgentName)
}

// B2: BeforeAgent may attach a new context that sub-agent's Run observes.
type stage2CtxKey struct{}

func TestAgentNode_Stage2_BeforeAgent_ContextPropagatesToSubAgentRun(t *testing.T) {
	sub := &recordingAgent{name: "child"}
	pm := newPluginManagerWithRegister(func(r *plugin.Registry) {
		r.BeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
			return &agent.BeforeAgentResult{
				Context: context.WithValue(ctx, stage2CtxKey{}, "v"),
			}, nil
		})
	})

	_, err, _ := invokeAgentNode(t, sub, pm)
	require.NoError(t, err)

	got := sub.capturedContext().Value(stage2CtxKey{})
	require.Equal(t, "v", got, "sub-agent Run must observe the BeforeAgent-derived context")
}

// B3: BeforeAgent returning an error fails the node and surfaces an error
// event upstream, matching the existing sub-agent Run-error path.
func TestAgentNode_Stage2_BeforeAgent_ErrorFailsNode(t *testing.T) {
	sub := &recordingAgent{name: "child"}
	pm := newPluginManagerWithRegister(func(r *plugin.Registry) {
		r.BeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
			return nil, errors.New("blocked")
		})
	})

	_, err, evts := invokeAgentNode(t, sub, pm)
	require.Error(t, err)
	require.Equal(t, 0, sub.ranCount(), "sub-agent Run must NOT be called when BeforeAgent errors")
	require.NotEmpty(t, evts, "node should emit an error event upstream")
}

// B4: BeforeAgent returning CustomResponse short-circuits sub-agent execution.
// The graph node must still finalize and produce non-nil output; the sub
// agent's Run must not be called.
func TestAgentNode_Stage2_BeforeAgent_CustomResponse_ShortCircuits(t *testing.T) {
	sub := &recordingAgent{name: "child"}
	pm := newPluginManagerWithRegister(func(r *plugin.Registry) {
		r.BeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
			return &agent.BeforeAgentResult{
				CustomResponse: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Index:   0,
						Message: model.NewAssistantMessage("early"),
					}},
				},
			}, nil
		})
	})

	out, err, _ := invokeAgentNode(t, sub, pm)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, 0, sub.ranCount())

	// The short-circuit response should be reflected as the node's last
	// response value (downstream nodes read StateKeyLastResponse).
	st, ok := out.(State)
	require.True(t, ok, "expected node output to be a graph.State")
	last, ok := st[StateKeyLastResponse].(string)
	require.True(t, ok, "expected StateKeyLastResponse to be a string")
	require.Equal(t, "early", last)
}

// -------- Group C: AfterAgent ----------------------------------------------

// C1: AfterAgent fires after the sub-agent finishes and receives the final
// non-partial response event.
func TestAgentNode_Stage2_AfterAgent_ReceivesFullResponseEvent(t *testing.T) {
	sub := &recordingAgent{name: "child"}
	var seenFull *event.Event
	pm := newPluginManagerWithRegister(func(r *plugin.Registry) {
		r.AfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
			seenFull = args.FullResponseEvent
			return nil, nil
		})
	})

	_, err, _ := invokeAgentNode(t, sub, pm)
	require.NoError(t, err)
	require.NotNil(t, seenFull)
	require.NotNil(t, seenFull.Response)
	require.Equal(t, "orig", seenFull.Response.Choices[0].Message.Content)
}

// C2: AfterAgent returning CustomResponse appends an extra response event
// that overrides lastResponse downstream.
func TestAgentNode_Stage2_AfterAgent_CustomResponse_AppendedAndOverrides(t *testing.T) {
	sub := &recordingAgent{name: "child"}
	pm := newPluginManagerWithRegister(func(r *plugin.Registry) {
		r.AfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
			return &agent.AfterAgentResult{
				CustomResponse: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Index:   0,
						Message: model.NewAssistantMessage("after"),
					}},
				},
			}, nil
		})
	})

	out, err, evts := invokeAgentNode(t, sub, pm)
	require.NoError(t, err)
	require.NotNil(t, out)

	// Expect at least two response events forwarded: sub-agent's own "orig"
	// and the AfterAgent-appended "after".
	responseTexts := responseTextsOf(evts)
	require.Contains(t, responseTexts, "orig")
	require.Contains(t, responseTexts, "after")

	st, ok := out.(State)
	require.True(t, ok, "expected node output to be a graph.State")
	last, ok := st[StateKeyLastResponse].(string)
	require.True(t, ok, "expected StateKeyLastResponse to be a string")
	require.Equal(t, "after", last,
		"AfterAgent CustomResponse must override lastResponse")
}

// C3: AfterAgent returning an error appends an error event at the tail of
// the stream.
func TestAgentNode_Stage2_AfterAgent_Error_AppendsErrorEvent(t *testing.T) {
	sub := &recordingAgent{name: "child"}
	pm := newPluginManagerWithRegister(func(r *plugin.Registry) {
		r.AfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
			return nil, errors.New("boom")
		})
	})

	_, _, evts := invokeAgentNode(t, sub, pm)
	require.NotEmpty(t, evts)
	require.True(t, hasErrorEventOfType(evts, agent.ErrorTypeAgentCallbackError),
		"expected an AgentCallback error event in the forwarded stream")
}

// C4: AfterAgent receives a non-nil Error argument when the sub-agent's own
// final response carries a model error.
func TestAgentNode_Stage2_AfterAgent_ReceivesSubAgentResponseError(t *testing.T) {
	sub := &recordingAgent{
		name: "child",
		emit: func(ctx context.Context, inv *agent.Invocation, out chan<- *event.Event) {
			rsp := &model.Response{
				Done: true,
				Error: &model.ResponseError{
					Type:    "boom-type",
					Message: "boom-msg",
				},
			}
			_ = agent.EmitEvent(ctx, inv, out,
				event.NewResponseEvent(inv.InvocationID, "child", rsp))
		},
	}
	var sawErr string
	pm := newPluginManagerWithRegister(func(r *plugin.Registry) {
		r.AfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
			if args != nil && args.Error != nil {
				sawErr = args.Error.Error()
			}
			return nil, nil
		})
	})

	_, _, _ = invokeAgentNode(t, sub, pm)
	require.Contains(t, sawErr, "boom-type")
	require.Contains(t, sawErr, "boom-msg")
}

// -------- Group D: co-existence with existing graph features --------------

// D1: Sub-agent emits a GraphCompletionEvent AND AfterAgent is registered.
// Both must be honored: completion snapshot is captured into SubgraphResult,
// and AfterAgent still fires.
func TestAgentNode_Stage2_AfterAgent_CoExistsWithGraphCompletion(t *testing.T) {
	sub := &recordingAgent{
		name: "child",
		emit: func(ctx context.Context, inv *agent.Invocation, out chan<- *event.Event) {
			// Emit a full response event first.
			rsp := &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Index:   0,
					Message: model.NewAssistantMessage("orig"),
				}},
			}
			_ = agent.EmitEvent(ctx, inv, out,
				event.NewResponseEvent(inv.InvocationID, "child", rsp))
			// Then terminate with a graph completion event.
			done := NewGraphCompletionEvent(
				WithCompletionEventInvocationID(inv.InvocationID),
				WithCompletionEventFinalState(State{"child_done": true}),
			)
			out <- done
		},
	}
	var afterCalled bool
	pm := newPluginManagerWithRegister(func(r *plugin.Registry) {
		r.AfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
			afterCalled = true
			return nil, nil
		})
	})

	var capturedFinal State
	out, err, _ := invokeAgentNode(t, sub, pm,
		WithSubgraphOutputMapper(func(_ State, r SubgraphResult) State {
			capturedFinal = r.FinalState
			return State{"ok": true}
		}),
	)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.True(t, afterCalled, "AfterAgent must fire even with GraphCompletionEvent present")
	require.NotNil(t, capturedFinal, "graph completion snapshot must be captured")
	require.Equal(t, true, capturedFinal["child_done"])
}

// D2: NodeCallbacks.AgentEvent must still observe every event forwarded by
// the agent node, including the extra response event appended by
// AfterAgent's CustomResponse. Pre-fix this gated test is skipped because
// AfterAgent does not fire at all through NewAgentNodeFunc; post-fix, the
// counter should observe N+1 events (sub-agent's N events + 1 appended).
func TestAgentNode_Stage2_NodeCallbacks_ObserveAppendedAfterAgentEvent(t *testing.T) {
	sub := &recordingAgent{name: "child"}
	pm := newPluginManagerWithRegister(func(r *plugin.Registry) {
		r.AfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
			return &agent.AfterAgentResult{
				CustomResponse: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Index:   0,
						Message: model.NewAssistantMessage("after"),
					}},
				},
			}, nil
		})
	})

	var observed int32
	cbs := NewNodeCallbacks().RegisterAgentEvent(func(
		ctx context.Context,
		cbCtx *NodeCallbackContext,
		state State,
		evt *event.Event,
	) {
		if evt != nil && evt.Response != nil && !evt.Response.IsPartial {
			atomic.AddInt32(&observed, 1)
		}
	})

	_, err, _ := invokeAgentNode(t, sub, pm, WithNodeCallbacks(cbs))
	require.NoError(t, err)
	require.Equal(t, int32(2), atomic.LoadInt32(&observed),
		"NodeCallbacks.AgentEvent must observe the sub-agent's response AND the AfterAgent-appended response")
}

// D3: Sub-agent signals an interrupt via a pregel-step event. Even with the
// RunWithPlugins wrapper (which spawns an extra goroutine to run AfterAgent
// callbacks) sitting between the sub-agent and processAgentEventStream, the
// interrupt signal must still reach the node layer so NewAgentNodeFunc
// returns an *InterruptError. This guards against the wrapper swallowing
// terminal signals or leaking goroutines.
func TestAgentNode_Stage2_Interrupts_StillPropagateThroughWrapper(t *testing.T) {
	const interruptNodeID = "agentNode"
	interruptValue := "need-human-approval"

	sub := &recordingAgent{
		name: "child",
		emit: func(ctx context.Context, inv *agent.Invocation, out chan<- *event.Event) {
			meta := PregelStepMetadata{
				NodeID:         interruptNodeID,
				InterruptValue: interruptValue,
			}
			b, err := json.Marshal(meta)
			if err != nil {
				return
			}
			e := event.New(
				inv.InvocationID,
				"child",
				event.WithObject(ObjectTypeGraphPregelStep),
				event.WithStateDelta(map[string][]byte{
					MetadataKeyPregel: b,
				}),
			)
			_ = agent.EmitEvent(ctx, inv, out, e)
		},
	}

	// Register an AfterAgent so the RunWithPlugins wrapper is actually
	// engaged; without it RunWithPlugins would fall through to ag.Run.
	var afterInvoked int32
	pm := newPluginManagerWithRegister(func(r *plugin.Registry) {
		r.AfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
			atomic.AddInt32(&afterInvoked, 1)
			return nil, nil
		})
	})

	done := make(chan struct{})
	var (
		out any
		err error
	)
	go func() {
		defer close(done)
		out, err, _ = invokeAgentNode(t, sub, pm)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("agent node hung: wrapper did not drain interrupt stream in time")
	}

	require.Nil(t, out, "interrupt must not produce normal output")
	var intr *InterruptError
	require.ErrorAs(t, err, &intr,
		"interrupt signal must propagate through the RunWithPlugins wrapper")
	require.Equal(t, interruptValue, intr.Value)
	require.Equal(t, int32(1), atomic.LoadInt32(&afterInvoked),
		"AfterAgent must still be invoked (stream drained before interrupt check)")
}

// -------- Group E: Clone inheritance (regression guard) --------------------

// E1: The sub-invocation built by buildAgentInvocationWithStateScopeAndInputKey
// must inherit Plugins from the parent invocation. Without this, RunWithPlugins
// in the agent node would never observe callbacks even after stage-2 lands.
// This test guards against a silent regression in Invocation.Clone.
func TestAgentNode_Stage2_SubInvocation_InheritsPluginsFromParent(t *testing.T) {
	pm := newPluginManagerWithRegister(func(r *plugin.Registry) {})

	// Emulate the same state/parent invocation construction used by the
	// agent node, then build the sub-invocation directly.
	sub := &recordingAgent{name: "child"}
	state, _ := buildAgentNodeState(sub)
	parentInv := agent.NewInvocation(
		agent.WithInvocationID("parent-inv"),
		agent.WithInvocationPlugins(pm),
	)
	ctx := agent.NewInvocationContext(context.Background(), parentInv)

	childInv := buildAgentInvocationWithStateScopeAndInputKey(
		ctx, state, State{}, sub, "agentNode", "", "",
	)
	require.NotNil(t, childInv)
	require.NotNil(t, childInv.Plugins, "child invocation must inherit parent Plugins via Clone")
	require.Same(t, pm, childInv.Plugins,
		"child invocation must reuse the exact same PluginManager instance")
}

// -------- helpers ---------------------------------------------------------

func responseTextsOf(evts []*event.Event) []string {
	var out []string
	for _, e := range evts {
		if e == nil || e.Response == nil {
			continue
		}
		for _, c := range e.Response.Choices {
			if c.Message.Content != "" {
				out = append(out, c.Message.Content)
			}
		}
	}
	return out
}

func hasErrorEventOfType(evts []*event.Event, typ string) bool {
	for _, e := range evts {
		if e != nil && e.Error != nil && e.Error.Type == typ {
			return true
		}
	}
	return false
}
