//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package subtask

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

type mockAgent struct {
	name  string
	resp  string
	tools []tool.Tool

	lastInstruction string // records the instruction from the last Run call
}

func (m *mockAgent) Run(_ context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	if inv != nil {
		m.lastInstruction = inv.RunOptions.Instruction
	}
	ch := make(chan *event.Event, 1)
	go func() {
		ch <- &event.Event{
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage(m.resp),
				}},
			},
		}
		close(ch)
	}()
	return ch, nil
}

func (m *mockAgent) Tools() []tool.Tool              { return m.tools }
func (m *mockAgent) Info() agent.Info                { return agent.Info{Name: m.name} }
func (m *mockAgent) SubAgents() []agent.Agent        { return nil }
func (m *mockAgent) FindSubAgent(string) agent.Agent { return nil }

type simpleTool struct{ name string }

func (s *simpleTool) Declaration() *tool.Declaration            { return &tool.Declaration{Name: s.name} }
func (s *simpleTool) Call(context.Context, []byte) (any, error) { return nil, nil }

func parentCtx(t *testing.T) context.Context {
	t.Helper()
	sess := session.NewSession("app", "user", "sess")
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(&mockAgent{name: "parent", resp: "task result"}),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("app"),
	)
	return agent.NewInvocationContext(context.Background(), inv)
}

func TestDeclaration(t *testing.T) {
	tt := NewSubtaskTool()
	decl := tt.Declaration()
	require.Equal(t, ToolName, decl.Name)
	require.NotNil(t, decl.InputSchema)
	require.Contains(t, decl.InputSchema.Required, "request")
	require.Contains(t, decl.InputSchema.Properties, "instruction")
	require.Contains(t, decl.InputSchema.Properties, "model")
	require.Contains(t, decl.InputSchema.Properties, "tools")
	require.Contains(t, decl.InputSchema.Properties, "inherit_context")
}

func TestDeclaration_CustomName(t *testing.T) {
	tt := NewSubtaskTool(WithSubtaskName("sub_task"))
	require.Equal(t, "sub_task", tt.Declaration().Name)
}

func TestCall_Success(t *testing.T) {
	ctx := parentCtx(t)
	tt := NewSubtaskTool()

	args, _ := json.Marshal(SubtaskRequest{Request: "analyze this code"})
	result, err := tt.Call(ctx, args)
	require.NoError(t, err)
	require.Equal(t, "task result", result)
}

func TestCall_WithInstruction(t *testing.T) {
	parentAgent := &mockAgent{name: "parent", resp: "task result"}
	sess := session.NewSession("app", "user", "sess")
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(parentAgent),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("app"),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	tt := NewSubtaskTool()

	args, _ := json.Marshal(SubtaskRequest{
		Request:     "find security issues",
		Instruction: "You are a security auditor.",
	})
	result, err := tt.Call(ctx, args)
	require.NoError(t, err)
	require.Equal(t, "task result", result)
	require.Equal(t, "You are a security auditor.", parentAgent.lastInstruction)
}

func TestCall_EmptyRequest(t *testing.T) {
	ctx := parentCtx(t)
	tt := NewSubtaskTool()

	args, _ := json.Marshal(SubtaskRequest{Request: ""})
	result, err := tt.Call(ctx, args)
	require.NoError(t, err)
	require.Equal(t, "request is required", result)
}

func TestCall_InvalidJSON(t *testing.T) {
	ctx := parentCtx(t)
	tt := NewSubtaskTool()

	result, err := tt.Call(ctx, []byte("not json"))
	require.NoError(t, err)
	require.Contains(t, result, "invalid arguments")
}

func TestCall_NoContext(t *testing.T) {
	tt := NewSubtaskTool()
	args, _ := json.Marshal(SubtaskRequest{Request: "x"})
	result, err := tt.Call(context.Background(), args)
	require.NoError(t, err)
	require.Equal(t, "no invocation context available", result)
}

func TestCall_NoSession(t *testing.T) {
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(&mockAgent{name: "a", resp: "r"}),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	tt := NewSubtaskTool()

	args, _ := json.Marshal(SubtaskRequest{Request: "x"})
	result, err := tt.Call(ctx, args)
	require.NoError(t, err)
	require.Equal(t, "no session available for subtask isolation", result)
}

func TestCall_ContextIsolation(t *testing.T) {
	sess := session.NewSession("app", "user", "sess")
	parentAgent := &mockAgent{name: "parent", resp: "isolated result"}
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(parentAgent),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("app"),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	tt := NewSubtaskTool()

	args, _ := json.Marshal(SubtaskRequest{Request: "do something complex"})
	result, err := tt.Call(ctx, args)
	require.NoError(t, err)
	require.Equal(t, "isolated result", result)

	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()
	childEvents := 0
	for _, evt := range sess.Events {
		if evt.FilterKey != "" {
			childEvents++
			require.NotEqual(t, "app", evt.FilterKey,
				"child event should not have parent's filter key")
			require.False(t, strings.HasPrefix(evt.FilterKey, "app/"),
				"isolated child key should NOT be a descendant of parent key")
		}
	}
	require.Greater(t, childEvents, 0, "expected at least one child event with a filter key")
}

func TestCall_InheritContext(t *testing.T) {
	sess := session.NewSession("app", "user", "sess")
	parentAgent := &mockAgent{name: "parent", resp: "context-aware result"}
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(parentAgent),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("app"),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	tt := NewSubtaskTool()

	args, _ := json.Marshal(SubtaskRequest{
		Request:        "analyze with full context",
		InheritContext: true,
	})
	result, err := tt.Call(ctx, args)
	require.NoError(t, err)
	require.Equal(t, "context-aware result", result)

	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()
	childEvents := 0
	for _, evt := range sess.Events {
		if evt.FilterKey != "" && evt.FilterKey != "app" {
			childEvents++
			require.True(t, strings.HasPrefix(evt.FilterKey, "app/"),
				"inherit_context child key should be a descendant of parent key, got: %s", evt.FilterKey)
		}
	}
	require.Greater(t, childEvents, 0, "expected at least one child event with inherited filter key")
}

func TestScopeTools_StripsExcludedTools(t *testing.T) {
	calc := &simpleTool{name: "calculator"}
	taskT := &simpleTool{name: ToolName}
	transferT := &simpleTool{name: transfer.TransferToolName}
	knowledgeT := &simpleTool{name: "knowledge_search"}

	all := []tool.Tool{calc, taskT, transferT, knowledgeT}

	scoped := scopeTools(all, nil)
	names := toolNames(scoped)
	require.ElementsMatch(t, []string{"calculator", "knowledge_search"}, names,
		"default scope should keep user and non-excluded framework tools")
}

func TestScopeTools_WithExplicitNames(t *testing.T) {
	calc := &simpleTool{name: "calculator"}
	weather := &simpleTool{name: "weather"}
	taskT := &simpleTool{name: ToolName}
	knowledgeT := &simpleTool{name: "knowledge_search"}

	all := []tool.Tool{calc, weather, taskT, knowledgeT}

	scoped := scopeTools(all, []string{"calculator", "knowledge_search"})
	names := toolNames(scoped)
	require.ElementsMatch(t, []string{"calculator", "knowledge_search"}, names,
		"explicit names should be respected, excluded tools stripped")
}

func TestScopeTools_ExplicitNameCannotForceExcludedTool(t *testing.T) {
	calc := &simpleTool{name: "calculator"}
	taskT := &simpleTool{name: ToolName}

	all := []tool.Tool{calc, taskT}

	scoped := scopeTools(all, []string{"calculator", ToolName})
	names := toolNames(scoped)
	require.Equal(t, []string{"calculator"}, names,
		"explicitly naming an excluded tool should not include it")
}

func TestToolScopedAgent_DelegatesToParent(t *testing.T) {
	parent := &mockAgent{name: "parent", resp: "result"}
	calc := &simpleTool{name: "calculator"}
	scoped := &toolScopedAgent{Agent: parent, scopedTools: []tool.Tool{calc}}

	require.Equal(t, "parent", scoped.Info().Name)
	require.Nil(t, scoped.SubAgents())
	require.Nil(t, scoped.FindSubAgent("x"))
	require.Equal(t, []tool.Tool{calc}, scoped.Tools())
	require.Equal(t, []tool.Tool{calc}, scoped.FilterTools(context.Background()))
	require.Equal(t, []tool.Tool{calc}, scoped.UserTools())
}

func TestToolScopedAgent_ForwardsCodeExecutor(t *testing.T) {
	exec := &stubCodeExecutor{}
	parent := &codeExecAgent{mockAgent: mockAgent{name: "parent", resp: "r"}, exec: exec}
	var a agent.Agent = &toolScopedAgent{Agent: parent, scopedTools: nil}

	ce, ok := a.(agent.CodeExecutor)
	require.True(t, ok, "wrapper should satisfy agent.CodeExecutor when parent does")
	require.Equal(t, exec, ce.CodeExecutor())
}

func TestToolScopedAgent_NilCodeExecutorWhenParentLacks(t *testing.T) {
	parent := &mockAgent{name: "parent", resp: "r"}
	var a agent.Agent = &toolScopedAgent{Agent: parent, scopedTools: nil}

	ce, ok := a.(agent.CodeExecutor)
	require.True(t, ok, "wrapper always implements the interface")
	require.Nil(t, ce.CodeExecutor(), "should return nil when parent has no CodeExecutor")
}

type stubCodeExecutor struct{}

func (s *stubCodeExecutor) ExecuteCode(context.Context, codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{Output: "ok"}, nil
}
func (s *stubCodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

type codeExecAgent struct {
	mockAgent
	exec codeexecutor.CodeExecutor
}

func (a *codeExecAgent) CodeExecutor() codeexecutor.CodeExecutor { return a.exec }

func TestShouldMirror(t *testing.T) {
	tests := []struct {
		name   string
		evt    *event.Event
		expect bool
	}{
		{
			name:   "event with state delta",
			evt:    &event.Event{StateDelta: map[string][]byte{"k": []byte("v")}},
			expect: true,
		},
		{
			name:   "nil response",
			evt:    &event.Event{},
			expect: false,
		},
		{
			name:   "partial event",
			evt:    &event.Event{Response: &model.Response{IsPartial: true}},
			expect: false,
		},
		{
			name: "valid content event",
			evt: &event.Event{
				Response: &model.Response{
					Done:    true,
					Choices: []model.Choice{{Message: model.NewAssistantMessage("hello")}},
				},
			},
			expect: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expect, shouldMirror(tc.evt))
		})
	}
}

func TestPersistable(t *testing.T) {
	t.Run("non-graph event is unchanged", func(t *testing.T) {
		evt := &event.Event{
			Response: &model.Response{Done: true, Choices: []model.Choice{{}}},
		}
		require.Same(t, evt, persistable(evt))
	})

	t.Run("nil response is unchanged", func(t *testing.T) {
		evt := &event.Event{}
		require.Same(t, evt, persistable(evt))
	})

	t.Run("graph execution event strips choices", func(t *testing.T) {
		evt := &event.Event{
			Response: &model.Response{
				Done:    true,
				Object:  graph.ObjectTypeGraphExecution,
				Choices: []model.Choice{{Message: model.NewAssistantMessage("big")}},
			},
		}
		result := persistable(evt)
		require.NotSame(t, evt, result)
		require.Nil(t, result.Response.Choices)
		require.NotNil(t, evt.Response.Choices, "original should not be mutated")
	})
}

func TestSessionHasEventID(t *testing.T) {
	sess := session.NewSession("app", "user", "sess")
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(&mockAgent{name: "a", resp: "r"}),
		agent.WithInvocationSession(sess),
	)

	sess.Events = append(sess.Events, event.Event{ID: "evt-1"})
	sess.Events = append(sess.Events, event.Event{ID: "evt-2"})

	require.True(t, sessionHasEventID(inv, "evt-1"))
	require.True(t, sessionHasEventID(inv, "evt-2"))
	require.False(t, sessionHasEventID(inv, "evt-3"))
}

func TestCollectResponse_Error(t *testing.T) {
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Error: &model.ResponseError{Message: "something went wrong"},
		},
	}
	close(ch)

	result, err := collectResponse(ch)
	require.Error(t, err)
	require.Contains(t, err.Error(), "something went wrong")
	require.Empty(t, result)
}

func TestCollectResponse_MultipleChunks(t *testing.T) {
	ch := make(chan *event.Event, 3)
	ch <- &event.Event{
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.NewAssistantMessage("hello "),
		}}},
	}
	ch <- &event.Event{
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.NewAssistantMessage("world"),
		}}},
	}
	ch <- &event.Event{
		Response: &model.Response{}, // no choices
	}
	close(ch)

	result, err := collectResponse(ch)
	require.NoError(t, err)
	require.Equal(t, "hello world", result)
}

func TestCollectResponse_EmptyChannel(t *testing.T) {
	ch := make(chan *event.Event)
	close(ch)

	result, err := collectResponse(ch)
	require.NoError(t, err)
	require.Empty(t, result)
}

func TestWrapCallSemantics_NilInvocation(t *testing.T) {
	ch := make(chan *event.Event, 1)
	evt := &event.Event{Response: &model.Response{Done: true}}
	ch <- evt
	close(ch)

	result := wrapCallSemantics(context.Background(), nil, ch)
	got := <-result
	require.Same(t, evt, got, "should pass through events when inv is nil")
}

func TestWrapCallSemantics_NilSession(t *testing.T) {
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(&mockAgent{name: "a", resp: "r"}),
	)
	ch := make(chan *event.Event, 1)
	evt := &event.Event{Response: &model.Response{Done: true}}
	ch <- evt
	close(ch)

	result := wrapCallSemantics(context.Background(), inv, ch)
	got := <-result
	require.Same(t, evt, got, "should pass through events when session is nil")
}

func TestAppendEvent_NilArgs(t *testing.T) {
	require.NotPanics(t, func() {
		appendEvent(context.Background(), nil, nil)
	})
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(&mockAgent{name: "a", resp: "r"}),
	)
	require.NotPanics(t, func() {
		appendEvent(context.Background(), inv, nil)
	})
}

func toolNames(tools []tool.Tool) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Declaration().Name
	}
	return names
}
