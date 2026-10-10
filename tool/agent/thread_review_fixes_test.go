//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	coreagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	checkpointinmemory "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/internal/agenttoolgraph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type callResult struct {
	out any
	err error
}

func startAsync(fn func() (any, error)) <-chan callResult {
	ch := make(chan callResult, 1)
	go func() {
		out, err := fn()
		ch <- callResult{out: out, err: err}
	}()
	return ch
}

func waitAsync(t *testing.T, ch <-chan callResult) callResult {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(eventuallyTimeout):
		t.Fatal("timed out waiting for call")
		return callResult{}
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(eventuallyTimeout):
		t.Fatal("timed out waiting for child")
	}
}

func callResumable(
	t *testing.T,
	at *Tool,
	ctx context.Context,
	parent *coreagent.Invocation,
	args []byte,
	viaGraph bool,
) (any, error) {
	t.Helper()
	if !viaGraph {
		return at.Call(ctx, args)
	}
	state := parent.RunOptions.RuntimeState
	if state == nil {
		state = map[string]any{}
	}
	return at.CallWithAgentToolGraphRuntime(ctx, args, agenttoolgraph.RuntimeContext{
		ParentInvocation: parent,
		State:            state,
		ParentNodeID:     "tools",
		ToolCallID:       "call-1",
		ToolCallKey:      "0:child:call-1",
	})
}

func signalAndWait(ready chan struct{}) func(context.Context, *coreagent.AfterAgentArgs) (*coreagent.AfterAgentResult, error) {
	return func(ctx context.Context, _ *coreagent.AfterAgentArgs) (*coreagent.AfterAgentResult, error) {
		close(ready)
		<-ctx.Done()
		return nil, nil
	}
}

// holdAfterEventAgent forwards the inner agent's events and then parks the
// stream after the selected event has already been handed to the caller.
// Parking inside AfterAgent is too early: GraphAgent can still be copying that
// event onto the channel the tool reads, and a cancel at that moment drops it.
type holdAfterEventAgent struct {
	coreagent.Agent
	ready chan struct{}
	match func(*event.Event) bool
}

func (a *holdAfterEventAgent) Run(ctx context.Context, inv *coreagent.Invocation) (<-chan *event.Event, error) {
	inner, err := a.Agent.Run(ctx, inv)
	if err != nil {
		return nil, err
	}
	out := make(chan *event.Event)
	go func() {
		defer close(out)
		held := false
		for evt := range inner {
			out <- evt
			if held || !a.match(evt) {
				continue
			}
			held = true
			close(a.ready)
			<-ctx.Done()
		}
	}()
	return out, nil
}

type reviewAgentStub struct{ name string }

func (a reviewAgentStub) Tools() []tool.Tool                  { return nil }
func (a reviewAgentStub) Info() coreagent.Info                { return coreagent.Info{Name: a.name} }
func (a reviewAgentStub) SubAgents() []coreagent.Agent        { return nil }
func (a reviewAgentStub) FindSubAgent(string) coreagent.Agent { return nil }

type runtimeCaptureAgent struct {
	reviewAgentStub
	mu      sync.Mutex
	state   map[string]any
	resume  bool
	content string
}

func (a *runtimeCaptureAgent) Run(_ context.Context, inv *coreagent.Invocation) (<-chan *event.Event, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if inv != nil {
		a.resume = inv.RunOptions.Resume
		a.state = inv.RunOptions.RuntimeState
		a.content = inv.Message.Content
	}
	return singleAssistantEvent("ok"), nil
}

func (a *runtimeCaptureAgent) snapshot() (map[string]any, bool, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state, a.resume, a.content
}

type graphProbe struct {
	mu        sync.Mutex
	calls     int
	proceeded bool
	leaks     []string
	room      any
	namespace string
}

func (p *graphProbe) observe(state graph.State) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.room = state["room_id"]
	p.namespace, _ = state[graph.CfgKeyCheckpointNS].(string)
	if lineage, ok := state[graph.CfgKeyLineageID].(string); ok && lineage == "parent-lineage" {
		p.leaks = append(p.leaks, graph.CfgKeyLineageID)
	}
	if _, ok := state[graph.CfgKeyCheckpointID]; ok {
		p.leaks = append(p.leaks, graph.CfgKeyCheckpointID)
	}
	if p.namespace == "parent-ns" {
		p.leaks = append(p.leaks, graph.CfgKeyCheckpointNS)
	}
	for _, key := range []string{
		graph.StateKeyCommand,
		graph.ResumeChannel,
		graph.StateKeyResumeMap,
		graph.StateKeyUsedInterrupts,
		graph.StateKeySubgraphInterrupt,
	} {
		if _, ok := state[key]; ok {
			p.leaks = append(p.leaks, key)
		}
	}
}

func (p *graphProbe) markProceeded() {
	p.mu.Lock()
	p.proceeded = true
	p.mu.Unlock()
}

func (p *graphProbe) snapshot() (calls int, proceeded bool, leaks []string, room any, namespace string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.proceeded, append([]string(nil), p.leaks...), p.room, p.namespace
}

func newProbeGraph(t *testing.T, name string, saver, interrupt bool, probe *graphProbe) coreagent.Agent {
	t.Helper()
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddNode("work", func(ctx context.Context, state graph.State) (any, error) {
		probe.observe(state)
		if !interrupt {
			return graph.State{graph.StateKeyLastResponse: "fresh child executed"}, nil
		}
		_, err := graph.Interrupt(ctx, state, "child-approval", "child needs its own approval")
		if err != nil {
			return nil, err
		}
		probe.markProceeded()
		return graph.State{graph.StateKeyLastResponse: "consumed parent resume"}, nil
	})
	compiled := sg.SetEntryPoint("work").SetFinishPoint("work").MustCompile()
	var opts []graphagent.Option
	if saver {
		opts = append(opts, graphagent.WithCheckpointSaver(checkpointinmemory.NewSaver()))
	}
	child, err := graphagent.New(name, compiled, opts...)
	require.NoError(t, err)
	return child
}

func TestNewTool_ResumableSubAgents_DropsParentGraphResumeState(t *testing.T) {
	cases := []struct {
		name              string
		saver             bool
		interrupt         bool
		agentName         string
		requireCheckpoint bool
		state             func() map[string]any
	}{
		{
			name:              "reserved keys without saver",
			agentName:         "graph-child",
			requireCheckpoint: true,
			state: func() map[string]any {
				return map[string]any{
					graph.CfgKeyLineageID:           "parent-lineage",
					graph.CfgKeyCheckpointID:        "",
					graph.CfgKeyCheckpointNS:        "parent-ns",
					graph.StateKeyCommand:           &graph.Command{Resume: "parent-only-answer"},
					graph.ResumeChannel:             "direct-resume",
					graph.StateKeyResumeMap:         map[string]any{"child-approval": "from-parent"},
					graph.StateKeyUsedInterrupts:    map[string]any{"child-approval": "already-used"},
					graph.StateKeySubgraphInterrupt: map[string]any{"parent_node_id": "tools"},
					"room_id":                       "r1",
				}
			},
		},
		{
			name:              "nonempty checkpoint",
			saver:             true,
			agentName:         "graph-child",
			requireCheckpoint: true,
			state: func() map[string]any {
				state := graph.CheckpointRef{
					LineageID:    "parent-lineage",
					Namespace:    "parent-ns",
					CheckpointID: "outer-checkpoint",
				}.ToRuntimeState()
				state["room_id"] = "r1"
				return state
			},
		},
		{
			name:              "empty checkpoint",
			saver:             true,
			agentName:         "graph-child",
			requireCheckpoint: true,
			state: func() map[string]any {
				state := graph.CheckpointRef{
					LineageID:    "parent-lineage",
					Namespace:    "parent-ns",
					CheckpointID: "",
				}.ToRuntimeState()
				state["room_id"] = "r1"
				return state
			},
		},
		{
			name:      "lineage and namespace",
			saver:     true,
			agentName: "graph-child",
			state: func() map[string]any {
				return map[string]any{
					graph.CfgKeyLineageID:    "parent-lineage",
					graph.CfgKeyCheckpointNS: "parent-ns",
					"room_id":                "r1",
				}
			},
		},
		{
			name:      "command",
			interrupt: true,
			agentName: "approval-child",
			state: func() map[string]any {
				return map[string]any{
					graph.StateKeyCommand: &graph.Command{Resume: "parent-only-answer"},
					"room_id":             "r1",
				}
			},
		},
		{
			name:      "command with saver",
			saver:     true,
			interrupt: true,
			agentName: "approval-child",
			state: func() map[string]any {
				return map[string]any{
					graph.StateKeyCommand: &graph.Command{Resume: "parent-only-answer"},
					"room_id":             "r1",
				}
			},
		},
		{
			name:      "direct resume",
			interrupt: true,
			agentName: "approval-child",
			state: func() map[string]any {
				return map[string]any{
					graph.ResumeChannel: "parent-only-answer",
					"room_id":           "r1",
				}
			},
		},
		{
			name:      "resume map",
			interrupt: true,
			agentName: "approval-child",
			state: func() map[string]any {
				return map[string]any{
					graph.StateKeyResumeMap: map[string]any{"child-approval": "from-parent"},
					"room_id":               "r1",
				}
			},
		},
		{
			name:      "used interrupts",
			interrupt: true,
			agentName: "approval-child",
			state: func() map[string]any {
				return map[string]any{
					graph.StateKeyUsedInterrupts: map[string]any{"child-approval": "already-used"},
					"room_id":                    "r1",
				}
			},
		},
	}
	for _, tc := range cases {
		for _, viaGraph := range []bool{false, true} {
			name := tc.name + "/call"
			if viaGraph {
				name = tc.name + "/graph-runtime"
			}
			t.Run(name, func(t *testing.T) {
				state := tc.state()
				if tc.requireCheckpoint {
					_, ok := state[graph.CfgKeyCheckpointID]
					require.True(t, ok, "parent runtime state must contain checkpoint_id")
				}
				snapshot := make(map[string]any, len(state))
				for key, value := range state {
					snapshot[key] = value
				}
				probe := &graphProbe{}
				at := NewTool(newProbeGraph(t, tc.agentName, tc.saver, tc.interrupt, probe), WithResumableSubAgents())
				ctx, _, parent := newThreadParent(t)
				parent.RunOptions.RuntimeState = state
				parent.RunOptions.Resume = true

				out, err := callResumable(t, at, ctx, parent, []byte(`{"input":{"request":"fresh"}}`), viaGraph)
				require.NoError(t, err)
				require.Equal(t, snapshot, state)
				if cmd, ok := state[graph.StateKeyCommand].(*graph.Command); ok {
					require.Equal(t, "parent-only-answer", cmd.Resume)
				}
				if resumeMap, ok := state[graph.StateKeyResumeMap].(map[string]any); ok {
					require.Equal(t, "from-parent", resumeMap["child-approval"])
				}
				require.True(t, parent.RunOptions.Resume)
				result := requireSubAgentResult(t, out)
				calls, proceeded, leaks, room, namespace := probe.snapshot()
				require.Equal(t, 1, calls)
				require.Empty(t, leaks)
				require.Equal(t, "r1", room)
				require.Equal(t, tc.agentName, namespace)
				require.False(t, proceeded)
				if tc.interrupt {
					require.Equal(t, subAgentStatusFailed, result.Status)
					require.Equal(t, errGraphInterrupt, result.Error)
					require.NotNil(t, result.Retryable)
					require.False(t, *result.Retryable)
					return
				}
				require.Equal(t, subAgentStatusCompleted, result.Status)
				require.Equal(t, "fresh child executed", result.Output)
			})
		}
	}
}

func TestNewTool_ResumableSubAgents_CopiesRuntimeMapPreservesBusinessValues(t *testing.T) {
	for _, viaGraph := range []bool{false, true} {
		name := "call"
		if viaGraph {
			name = "graph-runtime"
		}
		t.Run(name, func(t *testing.T) {
			profile := map[string]any{"team": "core"}
			tags := []any{map[string]any{"n": "1"}}
			cyclic := map[string]any{}
			cyclic["self"] = cyclic
			parentState := map[string]any{
				graph.CfgKeyCheckpointID:        "outer",
				graph.CfgKeyLineageID:           "parent-lineage",
				graph.CfgKeyCheckpointNS:        "parent-ns",
				graph.StateKeyCommand:           &graph.Command{Resume: "parent-only-answer"},
				graph.ResumeChannel:             "direct-resume",
				graph.StateKeyResumeMap:         map[string]any{"child-approval": "from-parent"},
				graph.StateKeyUsedInterrupts:    map[string]any{"child-approval": "already-used"},
				graph.StateKeySubgraphInterrupt: map[string]any{"parent_node_id": "tools"},
				"room_id":                       "r1",
				"profile":                       profile,
				"tags":                          tags,
				"gs":                            graph.State{"k": "v"},
				"cyclic":                        cyclic,
			}
			child := &runtimeCaptureAgent{reviewAgentStub: reviewAgentStub{name: "capture-child"}}
			at := NewTool(child, WithResumableSubAgents())
			ctx, _, parent := newThreadParent(t)
			parent.RunOptions.RuntimeState = parentState
			parent.RunOptions.Resume = true

			out, err := callResumable(t, at, ctx, parent, []byte(`{"input":{"request":"kept"}}`), viaGraph)
			require.NoError(t, err)
			require.Equal(t, subAgentStatusCompleted, requireSubAgentResult(t, out).Status)
			state, resume, content := child.snapshot()
			require.Equal(t, `{"request":"kept"}`, content)
			require.True(t, resume)
			require.NotNil(t, state)
			require.NotEqual(t, reflect.ValueOf(parentState).Pointer(), reflect.ValueOf(state).Pointer())
			require.Equal(t, "r1", state["room_id"])
			for _, key := range []string{
				graph.CfgKeyLineageID,
				graph.CfgKeyCheckpointID,
				graph.CfgKeyCheckpointNS,
				graph.StateKeyCommand,
				graph.ResumeChannel,
				graph.StateKeyResumeMap,
				graph.StateKeyUsedInterrupts,
				graph.StateKeySubgraphInterrupt,
			} {
				require.NotContains(t, state, key)
			}
			gotProfile := state["profile"].(map[string]any)
			require.Equal(t, reflect.ValueOf(profile).Pointer(), reflect.ValueOf(gotProfile).Pointer())
			gotTag := state["tags"].([]any)[0].(map[string]any)
			require.Equal(t, reflect.ValueOf(tags[0]).Pointer(), reflect.ValueOf(gotTag).Pointer())
			gotGS := state["gs"].(graph.State)
			require.Equal(t, reflect.ValueOf(parentState["gs"]).Pointer(), reflect.ValueOf(gotGS).Pointer())
			require.Equal(t, reflect.ValueOf(cyclic).Pointer(), reflect.ValueOf(state["cyclic"]).Pointer())
			state["room_id"] = "child-room"
			require.Equal(t, "r1", parentState["room_id"])
			require.Equal(t, "outer", parentState[graph.CfgKeyCheckpointID])
			require.Equal(t, "parent-only-answer", parentState[graph.StateKeyCommand].(*graph.Command).Resume)
			require.Equal(t, "from-parent", parentState[graph.StateKeyResumeMap].(map[string]any)["child-approval"])
		})
	}
}

func TestNewTool_OrdinaryRuntimeStateKeepsParentGraphKeys(t *testing.T) {
	parentState := map[string]any{
		graph.CfgKeyCheckpointID: "outer",
		"room_id":                "r1",
	}
	child := &runtimeCaptureAgent{reviewAgentStub: reviewAgentStub{name: "capture-child"}}
	at := NewTool(child)
	ctx, _, parent := newThreadParent(t)
	parent.RunOptions.RuntimeState = parentState
	parent.RunOptions.Resume = true

	out, err := at.Call(ctx, []byte(`{"request":"x"}`))
	require.NoError(t, err)
	require.Equal(t, "ok", out)
	state, resume, content := child.snapshot()
	require.Equal(t, `{"request":"x"}`, content)
	require.True(t, resume)
	require.NotNil(t, state)
	require.Equal(t, reflect.ValueOf(parentState).Pointer(), reflect.ValueOf(state).Pointer())
	require.Equal(t, "outer", state[graph.CfgKeyCheckpointID])

	runtime := map[string]any{
		graph.CfgKeyCheckpointID: "from-graph-runtime",
		"room_id":                "r1",
	}
	graphChild := &runtimeCaptureAgent{reviewAgentStub: reviewAgentStub{name: "capture-child"}}
	graphTool := NewTool(graphChild)
	_, err = graphTool.CallWithAgentToolGraphRuntime(ctx, []byte(`{"request":"x"}`), agenttoolgraph.RuntimeContext{
		ParentInvocation: parent,
		State:            runtime,
		ParentNodeID:     "tools",
		ToolCallID:       "call-1",
		ToolCallKey:      "0:capture-child:call-1",
	})
	require.NoError(t, err)
	state, resume, _ = graphChild.snapshot()
	require.True(t, resume)
	require.NotNil(t, state)
	require.Equal(t, reflect.ValueOf(runtime).Pointer(), reflect.ValueOf(state).Pointer())
	require.Equal(t, "from-graph-runtime", state[graph.CfgKeyCheckpointID])
	require.Equal(t, "r1", state["room_id"])
}

func compileWrappedSchema(t *testing.T, schema *tool.Schema, rawURL string) *jsonschema.Schema {
	t.Helper()
	encoded, err := json.Marshal(schema)
	require.NoError(t, err)
	var doc any
	require.NoError(t, json.Unmarshal(encoded, &doc))
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource(rawURL, doc))
	compiled, err := compiler.Compile(rawURL)
	require.NoError(t, err)
	return compiled
}

func TestNewTool_ResumableSubAgents_NullInputPassesThrough(t *testing.T) {
	ctx, _, _ := newThreadParent(t)
	nullable := &threadSchemaAgent{
		name:        "nullable-child",
		inputSchema: map[string]any{"enum": []any{nil}},
	}
	resumable := NewTool(nullable, WithResumableSubAgents())
	compiled := compileWrappedSchema(t, resumable.Declaration().InputSchema, "https://example.com/nullable.json")
	require.NoError(t, compiled.Validate(map[string]any{"input": nil}))

	nullable.lastMessage = ""
	_, err := NewTool(nullable).Call(ctx, []byte(`null`))
	require.NoError(t, err)
	require.Equal(t, "null", nullable.lastMessage)

	nullable.lastMessage = ""
	out, err := resumable.Call(ctx, []byte(`{"input":null}`))
	require.NoError(t, err)
	result := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusCompleted, result.Status)
	require.Equal(t, "null", nullable.lastMessage)
	require.NoError(t, validateThreadID(result.SubAgentID))

	nullable.lastMessage = ""
	out, err = resumable.Call(ctx, []byte(`{"subagent_id":null,"input":null}`))
	require.NoError(t, err)
	result = requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusCompleted, result.Status)
	require.Equal(t, "null", nullable.lastMessage)

	plain := &threadHistoryAgent{name: "child"}
	defaultTool := NewTool(plain, WithResumableSubAgents())
	defaultSchema := compileWrappedSchema(t, defaultTool.Declaration().InputSchema, "https://example.com/default.json")
	require.Error(t, defaultSchema.Validate(map[string]any{"input": nil}))
	out, err = defaultTool.Call(ctx, []byte(`{"input":null}`))
	require.NoError(t, err)
	require.Equal(t, subAgentStatusCompleted, requireSubAgentResult(t, out).Status)
	_, _, inputs := plain.snapshot()
	require.Equal(t, []string{"null"}, inputs)

	out, err = defaultTool.Call(ctx, []byte(`{}`))
	require.NoError(t, err)
	missing := requireSubAgentResult(t, out)
	require.Equal(t, subAgentStatusFailed, missing.Status)
	require.Contains(t, missing.Error, "input is required")
	require.NotNil(t, missing.Retryable)
	require.False(t, *missing.Retryable)
	require.Empty(t, missing.SubAgentID)
}

type blockOnCancelModel struct{ entered chan struct{} }

func (m *blockOnCancelModel) Info() model.Info { return model.Info{Name: "block-on-cancel"} }

func (m *blockOnCancelModel) GenerateContent(ctx context.Context, _ *model.Request) (<-chan *model.Response, error) {
	if m.entered != nil {
		select {
		case m.entered <- struct{}{}:
		default:
		}
	}
	<-ctx.Done()
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

type partialThenCancelModel struct {
	mu      sync.Mutex
	calls   int
	entered chan struct{}
}

func (m *partialThenCancelModel) Info() model.Info { return model.Info{Name: "partial-then-cancel"} }

func (m *partialThenCancelModel) GenerateContent(ctx context.Context, _ *model.Request) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.mu.Unlock()
	if call > 1 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		IsPartial: true,
		Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: "partial-text"},
		}},
	}
	close(ch)
	select {
	case m.entered <- struct{}{}:
	default:
	}
	return ch, nil
}

type finalResponseModel struct{ content string }

func (m *finalResponseModel) Info() model.Info { return model.Info{Name: "final-response"} }

func (m *finalResponseModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: m.content},
		}},
	}
	close(ch)
	return ch, nil
}

func newHoldLLM(t *testing.T, name string, mdl model.Model, ready chan struct{}) coreagent.Agent {
	t.Helper()
	callbacks := coreagent.NewCallbacks()
	callbacks.RegisterAfterAgent(signalAndWait(ready))
	return llmagent.New(name, llmagent.WithModel(mdl), llmagent.WithAgentCallbacks(callbacks))
}

type errorThenCancelAgent struct {
	reviewAgentStub
	sent chan struct{}
}

type nestedCompletionThenCancelAgent struct {
	reviewAgentStub
	ready chan struct{}
	graph bool
}

func (a *nestedCompletionThenCancelAgent) Run(ctx context.Context, inv *coreagent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	go func() {
		defer close(ch)
		resp := &model.Response{
			Done:    true,
			Choices: []model.Choice{{Message: model.NewAssistantMessage("nested result")}},
		}
		if a.graph {
			resp.Object = graph.ObjectTypeGraphExecution
		}
		evt := event.NewResponseEvent(inv.InvocationID+"-nested", "nested-child", resp)
		evt.ParentInvocationID = inv.InvocationID
		ch <- evt
		close(a.ready)
		<-ctx.Done()
	}()
	return ch, nil
}

func (a *errorThenCancelAgent) Run(ctx context.Context, inv *coreagent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		invocationID := ""
		if inv != nil {
			invocationID = inv.InvocationID
		}
		ch <- event.NewErrorEvent(invocationID, a.name, "test_error", "boom")
		close(a.sent)
		<-ctx.Done()
	}()
	return ch, nil
}

func requireCanceledFailure(t *testing.T, result SubAgentResult, needle string) {
	t.Helper()
	require.Equal(t, subAgentStatusFailed, result.Status)
	require.Empty(t, result.Output)
	require.Contains(t, result.Error, needle)
	require.NotNil(t, result.Retryable)
	require.True(t, *result.Retryable)
	require.NotEmpty(t, result.SubAgentID)
}

func TestNewTool_ResumableSubAgents_CancelWithoutNormalCompletion(t *testing.T) {
	const input = `{"input":{"request":"x"}}`

	for _, nestedGraph := range []bool{false, true} {
		name := "nested llm completion is not outer completion"
		if nestedGraph {
			name = "nested graph completion is not outer completion"
		}
		t.Run(name, func(t *testing.T) {
			child := &nestedCompletionThenCancelAgent{
				reviewAgentStub: reviewAgentStub{name: "outer-child"},
				ready:           make(chan struct{}),
				graph:           nestedGraph,
			}
			at := NewTool(child, WithResumableSubAgents())
			ctx, _, _ := newThreadParent(t)
			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			done := startAsync(func() (any, error) { return at.Call(callCtx, []byte(input)) })
			waitSignal(t, child.ready)
			cancel()
			got := waitAsync(t, done)
			require.NoError(t, got.err)
			requireCanceledFailure(t, requireSubAgentResult(t, got.out), "context canceled")
		})
	}

	t.Run("graph canceled after nested llm finishes", func(t *testing.T) {
		waiting := make(chan struct{})
		nested := llmagent.New("nested-leaf", llmagent.WithModel(&finalResponseModel{content: "leaf answer"}))
		sg := graph.NewStateGraph(graph.MessagesStateSchema())
		sg.AddAgentNode("nested-leaf")
		sg.AddNode("wait", func(ctx context.Context, _ graph.State) (any, error) {
			close(waiting)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		compiled := sg.AddEdge("nested-leaf", "wait").SetEntryPoint("nested-leaf").SetFinishPoint("wait").MustCompile()
		outer, err := graphagent.New("outer-graph", compiled, graphagent.WithSubAgents([]coreagent.Agent{nested}))
		require.NoError(t, err)
		at := NewTool(outer, WithResumableSubAgents())
		ctx, _, _ := newThreadParent(t)
		callCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := startAsync(func() (any, error) { return at.Call(callCtx, []byte(input)) })
		waitSignal(t, waiting)
		cancel()
		got := waitAsync(t, done)
		require.NoError(t, got.err)
		requireCanceledFailure(t, requireSubAgentResult(t, got.out), "context canceled")
	})

	t.Run("llm close after cancel", func(t *testing.T) {
		entered := make(chan struct{}, 1)
		at := NewTool(llmagent.New("llm-child", llmagent.WithModel(&blockOnCancelModel{entered: entered})), WithResumableSubAgents())
		ctx, sess, _ := newThreadParent(t)
		callCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := startAsync(func() (any, error) { return at.Call(callCtx, []byte(input)) })
		waitSignal(t, entered)
		cancel()
		got := waitAsync(t, done)
		require.NoError(t, got.err)
		result := requireSubAgentResult(t, got.out)
		requireCanceledFailure(t, result, "context canceled")
		key := threadFilterKey("llm-child", result.SubAgentID)
		require.True(t, sessionHasThreadEvents(sess, key))
		require.Equal(t, []string{`{"request":"x"}`}, sessionUserContents(sess, key))
	})

	t.Run("partial then cancel", func(t *testing.T) {
		entered := make(chan struct{}, 1)
		at := NewTool(llmagent.New("llm-child", llmagent.WithModel(&partialThenCancelModel{entered: entered})), WithResumableSubAgents())
		ctx, _, _ := newThreadParent(t)
		callCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := startAsync(func() (any, error) { return at.Call(callCtx, []byte(input)) })
		waitSignal(t, entered)
		cancel()
		got := waitAsync(t, done)
		require.NoError(t, got.err)
		result := requireSubAgentResult(t, got.out)
		requireCanceledFailure(t, result, "context canceled")
		require.NotContains(t, result.Output, "partial-text")
	})

	t.Run("final then cancel", func(t *testing.T) {
		ready := make(chan struct{})
		at := NewTool(newHoldLLM(t, "llm-child", &finalResponseModel{content: "final-answer"}, ready), WithResumableSubAgents())
		ctx, _, _ := newThreadParent(t)
		callCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := startAsync(func() (any, error) { return at.Call(callCtx, []byte(input)) })
		waitSignal(t, ready)
		cancel()
		got := waitAsync(t, done)
		require.NoError(t, got.err)
		result := requireSubAgentResult(t, got.out)
		require.Equal(t, subAgentStatusCompleted, result.Status)
		require.Equal(t, "final-answer", result.Output)
		require.Nil(t, result.Retryable)
		require.NotEmpty(t, result.SubAgentID)
	})

	t.Run("empty final then cancel", func(t *testing.T) {
		ready := make(chan struct{})
		at := NewTool(newHoldLLM(t, "llm-child", &finalResponseModel{content: ""}, ready), WithResumableSubAgents())
		ctx, _, _ := newThreadParent(t)
		callCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := startAsync(func() (any, error) { return at.Call(callCtx, []byte(input)) })
		waitSignal(t, ready)
		cancel()
		got := waitAsync(t, done)
		require.NoError(t, got.err)
		result := requireSubAgentResult(t, got.out)
		require.Equal(t, subAgentStatusCompleted, result.Status)
		require.Empty(t, result.Output)
		require.Nil(t, result.Retryable)
	})

	t.Run("deadline", func(t *testing.T) {
		at := NewTool(llmagent.New("llm-child", llmagent.WithModel(&blockOnCancelModel{})), WithResumableSubAgents())
		ctx, _, _ := newThreadParent(t)
		callCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()
		out, err := at.Call(callCtx, []byte(input))
		require.NoError(t, err)
		requireCanceledFailure(t, requireSubAgentResult(t, out), "context deadline exceeded")
	})

	t.Run("graph completion then cancel", func(t *testing.T) {
		ready := make(chan struct{})
		sg := graph.NewStateGraph(graph.MessagesStateSchema())
		sg.AddNode("answer", func(context.Context, graph.State) (any, error) {
			return graph.State{graph.StateKeyLastResponse: "graph-answer"}, nil
		})
		compiled := sg.SetEntryPoint("answer").SetFinishPoint("answer").MustCompile()
		inner, err := graphagent.New("graph-child", compiled)
		require.NoError(t, err)
		at := NewTool(&holdAfterEventAgent{
			Agent: inner,
			ready: ready,
			match: isGraphCompletionSnapshotEvent,
		}, WithResumableSubAgents())
		ctx, _, _ := newThreadParent(t)
		callCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := startAsync(func() (any, error) { return at.Call(callCtx, []byte(input)) })
		waitSignal(t, ready)
		cancel()
		got := waitAsync(t, done)
		require.NoError(t, got.err)
		result := requireSubAgentResult(t, got.out)
		require.Equal(t, subAgentStatusCompleted, result.Status)
		require.Equal(t, "graph-answer", result.Output)
		require.Nil(t, result.Retryable)
	})

	t.Run("error precedes cancel", func(t *testing.T) {
		child := &errorThenCancelAgent{reviewAgentStub: reviewAgentStub{name: "err-child"}, sent: make(chan struct{})}
		at := NewTool(child, WithResumableSubAgents())
		ctx, _, _ := newThreadParent(t)
		callCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := startAsync(func() (any, error) { return at.Call(callCtx, []byte(input)) })
		waitSignal(t, child.sent)
		cancel()
		got := waitAsync(t, done)
		require.NoError(t, got.err)
		result := requireSubAgentResult(t, got.out)
		require.Equal(t, subAgentStatusFailed, result.Status)
		require.Contains(t, result.Error, "boom")
		require.NotContains(t, result.Error, "context canceled")
		require.Nil(t, result.Retryable)
	})

	t.Run("interrupt precedes cancel", func(t *testing.T) {
		ready := make(chan struct{})
		sg := graph.NewStateGraph(graph.MessagesStateSchema())
		sg.AddNode("ask", func(ctx context.Context, state graph.State) (any, error) {
			_, err := graph.Interrupt(ctx, state, "child-approval", "child needs its own approval")
			return nil, err
		})
		compiled := sg.SetEntryPoint("ask").SetFinishPoint("ask").MustCompile()
		inner, err := graphagent.New("approval-child", compiled)
		require.NoError(t, err)
		at := NewTool(&holdAfterEventAgent{
			Agent: inner,
			ready: ready,
			match: func(evt *event.Event) bool {
				_, _, ok := pregelStepInterrupt(evt)
				return ok
			},
		}, WithResumableSubAgents())
		ctx, _, _ := newThreadParent(t)
		callCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := startAsync(func() (any, error) { return at.Call(callCtx, []byte(input)) })
		waitSignal(t, ready)
		cancel()
		got := waitAsync(t, done)
		require.NoError(t, got.err)
		result := requireSubAgentResult(t, got.out)
		require.Equal(t, subAgentStatusFailed, result.Status)
		require.Equal(t, errGraphInterrupt, result.Error)
		require.NotNil(t, result.Retryable)
		require.False(t, *result.Retryable)
	})

	t.Run("streamable final envelope", func(t *testing.T) {
		entered := make(chan struct{}, 1)
		at := NewTool(
			llmagent.New("llm-child", llmagent.WithModel(&blockOnCancelModel{entered: entered})),
			WithResumableSubAgents(),
			WithStreamInner(true),
		)
		ctx, _, _ := newThreadParent(t)
		callCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		reader, err := at.StreamableCall(tool.WithFinalResultChunks(callCtx), []byte(input))
		require.NoError(t, err)
		t.Cleanup(func() { reader.Close() })
		waitSignal(t, entered)
		cancel()
		var chunks []tool.StreamChunk
		for {
			chunk, recvErr := reader.Recv()
			if recvErr == io.EOF {
				break
			}
			require.NoError(t, recvErr)
			chunks = append(chunks, chunk)
		}
		require.Len(t, chunks, 1)
		final, ok := chunks[0].Content.(tool.FinalResultChunk)
		require.True(t, ok)
		requireCanceledFailure(t, requireSubAgentResult(t, final.Result), "context canceled")
	})
}

func TestNewTool_OrdinaryCancelCloseStaysSuccessful(t *testing.T) {
	entered := make(chan struct{}, 1)
	at := NewTool(llmagent.New("llm-child", llmagent.WithModel(&blockOnCancelModel{entered: entered})))
	ctx, _, _ := newThreadParent(t)
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := startAsync(func() (any, error) { return at.Call(callCtx, []byte(`{"request":"x"}`)) })
	waitSignal(t, entered)
	cancel()
	got := waitAsync(t, done)
	require.NoError(t, got.err)
	require.Equal(t, "", got.out)
}
