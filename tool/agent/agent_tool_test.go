//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/barrier"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/flush"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockAgent is a simple mock agent for testing.
type mockAgent struct {
	name        string
	description string
}

type fixedResponseModel struct {
	response string
}

type visibleCompletionThenAfterAgent struct {
	name string
}

type assistantThenVisibleCompletionAgent struct {
	name string
}

type assistantThenVisibleStateOnlyCompletionAgent struct {
	name string
}

type assistantThenVisibleStateOnlyCompletionWithoutResponseIDAgent struct {
	name string
}

type visibleCompletionThenErrorAgent struct {
	name string
}

func newGraphAgentWithAfterCallback(
	t *testing.T,
	state graph.State,
	customResponse *model.Response,
) *graphagent.GraphAgent {
	t.Helper()
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddNode("done", func(ctx context.Context, input graph.State) (any, error) {
		return state, nil
	})
	compiled := sg.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	callbacks := agent.NewCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		return &agent.AfterAgentResult{
			CustomResponse: customResponse,
		}, nil
	})
	ga, err := graphagent.New("graph-child-callback", compiled, graphagent.WithAgentCallbacks(callbacks))
	require.NoError(t, err)
	return ga
}

func newGraphAgentWithAfterCallbackError(
	t *testing.T,
	state graph.State,
	err error,
) *graphagent.GraphAgent {
	t.Helper()
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddNode("done", func(ctx context.Context, input graph.State) (any, error) {
		return state, nil
	})
	compiled := sg.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	callbacks := agent.NewCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		return nil, err
	})
	ga, runErr := graphagent.New("graph-child-callback-error", compiled, graphagent.WithAgentCallbacks(callbacks))
	require.NoError(t, runErr)
	return ga
}

func (m *fixedResponseModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	go func() {
		defer close(ch)
		ch <- &model.Response{
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage(m.response),
			}},
			Done: true,
		}
	}()
	return ch, nil
}

func (m *fixedResponseModel) Info() model.Info {
	return model.Info{Name: "fixed-response-model"}
}

func (a *visibleCompletionThenAfterAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)
		invocationID := "visible-completion-agent"
		author := a.name
		if invocation != nil {
			invocationID = invocation.InvocationID
			if invocation.AgentName != "" {
				author = invocation.AgentName
			}
		}
		rawCompletion := graph.NewGraphCompletionEvent(
			graph.WithCompletionEventInvocationID(invocationID),
			graph.WithCompletionEventFinalState(graph.State{
				graph.StateKeyLastResponse: "child-final",
			}),
		)
		visibleCompletion, ok := graph.VisibleGraphCompletionEvent(rawCompletion)
		if !ok {
			return
		}
		ch <- visibleCompletion
		ch <- event.NewResponseEvent(invocationID, author, &model.Response{
			Object: "after.custom",
			Done:   true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("after callback"),
			}},
		})
	}()
	return ch, nil
}

func (a *assistantThenVisibleCompletionAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)
		invocationID := "assistant-then-visible-completion-agent"
		author := a.name
		if invocation != nil {
			invocationID = invocation.InvocationID
			if invocation.AgentName != "" {
				author = invocation.AgentName
			}
		}
		assistantEvent := event.NewResponseEvent(invocationID, author, &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("draft"),
			}},
		})
		rawCompletion := graph.NewGraphCompletionEvent(
			graph.WithCompletionEventInvocationID(invocationID),
			graph.WithCompletionEventFinalState(graph.State{
				graph.StateKeyLastResponse: "child-final",
			}),
		)
		visibleCompletion, ok := graph.VisibleGraphCompletionEvent(rawCompletion)
		if !ok {
			return
		}
		_ = agent.EmitEvent(ctx, invocation, ch, assistantEvent)
		_ = agent.EmitEvent(ctx, invocation, ch, visibleCompletion)
	}()
	return ch, nil
}

func (a *assistantThenVisibleStateOnlyCompletionAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)
		invocationID := "assistant-then-visible-completion-agent"
		author := a.name
		if invocation != nil {
			invocationID = invocation.InvocationID
			if invocation.AgentName != "" {
				author = invocation.AgentName
			}
		}
		assistantEvent := event.NewResponseEvent(invocationID, author, &model.Response{
			ID:     "resp-1",
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("wrapped-final"),
			}},
		})
		rawCompletion := graph.NewGraphCompletionEvent(
			graph.WithCompletionEventInvocationID(invocationID),
			graph.WithCompletionEventFinalState(graph.State{
				graph.StateKeyLastResponseID: "resp-1",
				graphStateKey:                "child-state",
			}),
		)
		visibleCompletion, ok := graph.VisibleGraphCompletionEvent(rawCompletion)
		if !ok {
			return
		}
		_ = agent.EmitEvent(ctx, invocation, ch, assistantEvent)
		_ = agent.EmitEvent(ctx, invocation, ch, visibleCompletion)
	}()
	return ch, nil
}

func (a *assistantThenVisibleStateOnlyCompletionWithoutResponseIDAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)
		invocationID := "assistant-then-visible-completion-without-response-id-agent"
		author := a.name
		if invocation != nil {
			invocationID = invocation.InvocationID
			if invocation.AgentName != "" {
				author = invocation.AgentName
			}
		}
		assistantEvent := event.NewResponseEvent(invocationID, author, &model.Response{
			ID:     "resp-1",
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("wrapped-final"),
			}},
		})
		rawCompletion := graph.NewGraphCompletionEvent(
			graph.WithCompletionEventInvocationID(invocationID),
			graph.WithCompletionEventFinalState(graph.State{
				graphStateKey: "child-state",
			}),
		)
		visibleCompletion, ok := graph.VisibleGraphCompletionEvent(rawCompletion)
		if !ok {
			return
		}
		_ = agent.EmitEvent(ctx, invocation, ch, assistantEvent)
		_ = agent.EmitEvent(ctx, invocation, ch, visibleCompletion)
	}()
	return ch, nil
}

func (a *visibleCompletionThenErrorAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)
		invocationID := "visible-completion-then-error-agent"
		author := a.name
		if invocation != nil {
			invocationID = invocation.InvocationID
			if invocation.AgentName != "" {
				author = invocation.AgentName
			}
		}
		rawCompletion := graph.NewGraphCompletionEvent(
			graph.WithCompletionEventInvocationID(invocationID),
			graph.WithCompletionEventFinalState(graph.State{
				graph.StateKeyLastResponse: "child-final",
			}),
		)
		visibleCompletion, ok := graph.VisibleGraphCompletionEvent(rawCompletion)
		if !ok {
			return
		}
		_ = agent.EmitEvent(ctx, invocation, ch, visibleCompletion)
		_ = agent.EmitEvent(
			ctx,
			invocation,
			ch,
			event.NewErrorEvent(
				invocationID,
				author,
				"after_callback_error",
				"after callback failed",
			),
		)
	}()
	return ch, nil
}

func (a *visibleCompletionThenAfterAgent) Tools() []tool.Tool {
	return nil
}

func (a *visibleCompletionThenAfterAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *visibleCompletionThenAfterAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *visibleCompletionThenAfterAgent) FindSubAgent(name string) agent.Agent {
	_ = name
	return nil
}

func (a *assistantThenVisibleCompletionAgent) Tools() []tool.Tool {
	return nil
}

func (a *assistantThenVisibleCompletionAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *assistantThenVisibleCompletionAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *assistantThenVisibleCompletionAgent) FindSubAgent(name string) agent.Agent {
	_ = name
	return nil
}

func (a *assistantThenVisibleStateOnlyCompletionAgent) Tools() []tool.Tool {
	return nil
}

func (a *assistantThenVisibleStateOnlyCompletionAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *assistantThenVisibleStateOnlyCompletionAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *assistantThenVisibleStateOnlyCompletionAgent) FindSubAgent(name string) agent.Agent {
	_ = name
	return nil
}

func (a *assistantThenVisibleStateOnlyCompletionWithoutResponseIDAgent) Tools() []tool.Tool {
	return nil
}

func (a *assistantThenVisibleStateOnlyCompletionWithoutResponseIDAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *assistantThenVisibleStateOnlyCompletionWithoutResponseIDAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *assistantThenVisibleStateOnlyCompletionWithoutResponseIDAgent) FindSubAgent(name string) agent.Agent {
	_ = name
	return nil
}

func (a *visibleCompletionThenErrorAgent) Tools() []tool.Tool {
	return nil
}

func (a *visibleCompletionThenErrorAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *visibleCompletionThenErrorAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *visibleCompletionThenErrorAgent) FindSubAgent(name string) agent.Agent {
	_ = name
	return nil
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Mock implementation - return a simple response.
	eventChan := make(chan *event.Event, 1)

	response := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.NewAssistantMessage("Hello from mock agent!"),
				},
			},
		},
	}

	go func() {
		eventChan <- response
		close(eventChan)
	}()

	return eventChan, nil
}

func (m *mockAgent) Tools() []tool.Tool {
	return []tool.Tool{}
}

func (m *mockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: m.description,
	}
}

func (m *mockAgent) SubAgents() []agent.Agent {
	return []agent.Agent{}
}

func (m *mockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func TestNewTool(t *testing.T) {
	mockAgent := &mockAgent{
		name:        "test-agent",
		description: "A test agent for testing",
	}

	agentTool := NewTool(mockAgent)

	if agentTool.name != "test-agent" {
		t.Errorf("Expected name 'test-agent', got '%s'", agentTool.name)
	}

	if agentTool.description != "A test agent for testing" {
		t.Errorf("Expected description 'A test agent for testing', got '%s'", agentTool.description)
	}

	if agentTool.agent != mockAgent {
		t.Error("Expected agent to be the same as the input agent")
	}
}

func TestTool_Declaration(t *testing.T) {
	mockAgent := &mockAgent{
		name:        "test-agent",
		description: "A test agent for testing",
	}

	agentTool := NewTool(mockAgent)
	declaration := agentTool.Declaration()

	if declaration.Name != "test-agent" {
		t.Errorf("Expected name 'test-agent', got '%s'", declaration.Name)
	}

	if declaration.Description != "A test agent for testing" {
		t.Errorf("Expected description 'A test agent for testing', got '%s'", declaration.Description)
	}

	if declaration.InputSchema == nil {
		t.Error("Expected InputSchema to not be nil")
	}

	if declaration.OutputSchema == nil {
		t.Error("Expected OutputSchema to not be nil")
	}
}

func TestNewTool_WithDescription(t *testing.T) {
	mockAgent := &mockAgent{
		name:        "test-agent",
		description: "A test agent for testing",
	}

	agentTool := NewTool(mockAgent, WithDescription("Custom tool description"))

	if agentTool.description != "Custom tool description" {
		t.Errorf("Expected description 'Custom tool description', got '%s'", agentTool.description)
	}
}

func TestTool_Declaration_WithDescription(t *testing.T) {
	mockAgent := &mockAgent{
		name:        "test-agent",
		description: "A test agent for testing",
	}

	declaration := NewTool(mockAgent, WithDescription("Custom tool description")).Declaration()

	if declaration.Description != "Custom tool description" {
		t.Errorf("Expected description 'Custom tool description', got '%s'", declaration.Description)
	}
}

func TestTool_Declaration_WithEmptyDescription(t *testing.T) {
	mockAgent := &mockAgent{
		name:        "test-agent",
		description: "A test agent for testing",
	}

	declaration := NewTool(mockAgent, WithDescription("")).Declaration()

	if declaration.Description != "" {
		t.Errorf("Expected empty description, got '%s'", declaration.Description)
	}
}

func TestTool_Call(t *testing.T) {
	mockAgent := &mockAgent{
		name:        "test-agent",
		description: "A test agent for testing",
	}

	agentTool := NewTool(mockAgent)

	// Test input
	input := struct {
		Request string `json:"request"`
	}{
		Request: "Hello, agent!",
	}

	jsonArgs, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("Failed to marshal input: %v", err)
	}

	// Call the agent tool.
	result, err := agentTool.Call(context.Background(), jsonArgs)
	if err != nil {
		t.Fatalf("Failed to call agent tool: %v", err)
	}

	// Check the result.
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("Expected result to be string, got %T", result)
	}

	if resultStr == "" {
		t.Error("Expected non-empty result")
	}
}

func TestTool_Call_DisableGraphCompletionEvent_KeepsFinalText(t *testing.T) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "child-final"}, nil
	})
	compiled := sg.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	ga, err := graphagent.New("graph-child", compiled)
	require.NoError(t, err)
	at := NewTool(ga, WithHistoryScope(HistoryScopeParentBranch))
	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	result, err := at.Call(ctx, []byte(`{"request":"ignored"}`))
	require.NoError(t, err)
	resultText, ok := result.(string)
	require.True(t, ok)
	require.Equal(t, "child-final", resultText)
}

func TestTool_Call_DisableGraphCompletionEvent_DedupsCapturedGraphCompletionAfterFinalModelResponse(t *testing.T) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddLLMNode("llm", &fixedResponseModel{response: "child-final"}, "", nil)
	compiled := sg.SetEntryPoint("llm").SetFinishPoint("llm").MustCompile()
	ga, err := graphagent.New("graph-child", compiled)
	require.NoError(t, err)
	at := NewTool(ga, WithHistoryScope(HistoryScopeParentBranch))
	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
			agent.WithGraphEmitFinalModelResponses(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	result, err := at.Call(ctx, []byte(`{"request":"ignored"}`))
	require.NoError(t, err)
	resultText, ok := result.(string)
	require.True(t, ok)
	require.Equal(t, "child-final", resultText)
}

func TestTool_Call_DisableGraphCompletionEvent_PrefersAfterCallbackCustomResponse(t *testing.T) {
	ga := newGraphAgentWithAfterCallback(
		t,
		graph.State{graph.StateKeyLastResponse: "child-final"},
		&model.Response{
			Object: "after.custom",
			Done:   true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("after callback"),
			}},
		},
	)
	at := NewTool(ga, WithHistoryScope(HistoryScopeParentBranch))
	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	result, err := at.Call(ctx, []byte(`{"request":"ignored"}`))
	require.NoError(t, err)
	resultText, ok := result.(string)
	require.True(t, ok)
	require.Equal(t, "after callback", resultText)
}

func TestTool_Call_DisableGraphCompletionEvent_PreservesFinalTextInSharedSession(t *testing.T) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "child-final"}, nil
	})
	compiled := sg.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	ga, err := graphagent.New("graph-child", compiled)
	require.NoError(t, err)
	at := NewTool(ga, WithHistoryScope(HistoryScopeParentBranch))
	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	result, err := at.Call(ctx, []byte(`{"request":"ignored"}`))
	require.NoError(t, err)
	resultText, ok := result.(string)
	require.True(t, ok)
	require.Equal(t, "child-final", resultText)
	require.True(t, sessionHasAssistantContent(sess, "child-final"))
}

func TestTool_Call_DisableGraphCompletionEvent_PreservesConsecutiveVisibleCompletionStatesInSharedSession(
	t *testing.T,
) {
	at := NewTool(
		&doubleVisibleCompletionAgent{name: "double-visible-agent"},
		WithHistoryScope(HistoryScopeParentBranch),
	)
	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	result, err := at.Call(ctx, []byte(`{"request":"ignored"}`))
	require.NoError(t, err)
	resultText, ok := result.(string)
	require.True(t, ok)
	require.Equal(t, "second-final", resultText)
	firstValue, ok := sess.GetState("first_key")
	require.True(t, ok)
	require.Equal(t, []byte(`"first-value"`), firstValue)
	secondValue, ok := sess.GetState("second_key")
	require.True(t, ok)
	require.Equal(t, []byte(`"second-value"`), secondValue)
}

func TestTool_Call_DisableGraphCompletionEvent_SharedSessionPrefersAfterCallbackCustomResponse(t *testing.T) {
	ga := newGraphAgentWithAfterCallback(
		t,
		graph.State{
			graph.StateKeyLastResponse: "child-final",
		},
		&model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("after callback"),
			}},
		},
	)
	at := NewTool(ga, WithHistoryScope(HistoryScopeParentBranch))
	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	result, err := at.Call(ctx, []byte(`{"request":"ignored"}`))
	require.NoError(t, err)
	resultText, ok := result.(string)
	require.True(t, ok)
	require.Equal(t, "after callback", resultText)
	require.True(t, sessionHasAssistantContent(sess, "after callback"))
	require.False(t, sessionHasAssistantContent(sess, "child-final"))
	stateValue, ok := sess.GetState(graph.StateKeyLastResponse)
	require.True(t, ok)
	require.Equal(t, []byte(`"child-final"`), stateValue)
}

func TestTool_Call_DisableGraphCompletionEvent_FlushesVisibleCompletionBeforeBarrierNotification(t *testing.T) {
	at := NewTool(
		&visibleCompletionBarrierAgent{name: "visible-barrier-agent"},
		WithHistoryScope(HistoryScopeParentBranch),
	)
	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	result, err := at.Call(ctx, []byte(`{"request":"ignored"}`))
	require.NoError(t, err)
	resultText, ok := result.(string)
	require.True(t, ok)
	require.Equal(t, "child-final", resultText)
	require.True(t, sessionHasAssistantContent(sess, "child-final"))
}

func TestTool_Call_DisableGraphExecutorEvents_SuppressesBarrierEvents(t *testing.T) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "child-final"}, nil
	})
	compiled := sg.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	ga, err := graphagent.New("graph-child", compiled)
	require.NoError(t, err)
	at := NewTool(ga, WithHistoryScope(HistoryScopeParentBranch))
	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphExecutorEvents(true),
		)),
	)
	barrier.Enable(parent)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	result, err := at.Call(ctx, []byte(`{"request":"ignored"}`))
	require.NoError(t, err)
	resultText, ok := result.(string)
	require.True(t, ok)
	require.Equal(t, "child-final", resultText)
}

func TestTool_Call_VisibleCompletionSnapshot_PreservesLegacyConcatenation(t *testing.T) {
	at := NewTool(&assistantThenVisibleCompletionAgent{name: "visible-agent"})
	result, err := at.Call(context.Background(), []byte(`{"request":"ignored"}`))
	require.NoError(t, err)
	resultText, ok := result.(string)
	require.True(t, ok)
	require.Equal(t, "draftchild-finalchild-final", resultText)
}

func TestTool_DefaultSkipSummarization(t *testing.T) {
	mockAgent := &mockAgent{
		name:        "test-agent",
		description: "A test agent for testing",
	}

	agentTool := NewTool(mockAgent)

	if agentTool.skipSummarization {
		t.Error("Expected skip summarization to be false by default")
	}
}

func TestTool_WithSkipSummarization(t *testing.T) {
	mockAgent := &mockAgent{
		name:        "test-agent",
		description: "A test agent for testing",
	}

	agentTool := NewTool(mockAgent, WithSkipSummarization(true))

	if !agentTool.skipSummarization {
		t.Error("Expected skip summarization to be true")
	}
}

// streamingMockAgent streams a few delta events then a final full message.
type streamingMockAgent struct {
	name string
	// capture the event filter key seen by Run for assertion.
	seenFilterKey string
}

func (m *streamingMockAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	// record the filter key used so tests can assert it equals agent name.
	m.seenFilterKey = inv.GetEventFilterKey()
	ch := make(chan *event.Event, 3)
	go func() {
		defer close(ch)
		// delta 1
		ch <- &event.Event{Response: &model.Response{IsPartial: true, Choices: []model.Choice{{Delta: model.Message{Content: "hello"}}}}}
		// delta 2
		ch <- &event.Event{Response: &model.Response{IsPartial: true, Choices: []model.Choice{{Delta: model.Message{Content: " world"}}}}}
		// final full assistant message (should not be forwarded by UI typically)
		ch <- &event.Event{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "ignored full"}}}}}
	}()
	return ch, nil
}

func (m *streamingMockAgent) Tools() []tool.Tool { return nil }
func (m *streamingMockAgent) Info() agent.Info {
	return agent.Info{Name: m.name, Description: "streaming mock"}
}
func (m *streamingMockAgent) SubAgents() []agent.Agent        { return nil }
func (m *streamingMockAgent) FindSubAgent(string) agent.Agent { return nil }

type completionWaitAgent struct {
	name string
}

func (m *completionWaitAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)

		barrier := event.New(inv.InvocationID, m.name)
		barrier.RequiresCompletion = true
		completionID := agent.GetAppendEventNoticeKey(barrier.ID)
		_ = inv.AddNoticeChannel(ctx, completionID)
		_ = agent.EmitEvent(ctx, inv, ch, barrier)

		if err := inv.AddNoticeChannelAndWait(ctx, completionID, 500*time.Millisecond); err != nil {
			errEvt := event.NewErrorEvent(inv.InvocationID, m.name, model.ErrorTypeFlowError, err.Error())
			_ = agent.EmitEvent(ctx, inv, ch, errEvt)
			return
		}

		done := event.NewResponseEvent(inv.InvocationID, m.name, &model.Response{
			Done:    true,
			Choices: []model.Choice{{Message: model.NewAssistantMessage("done")}},
		})
		_ = agent.EmitEvent(ctx, inv, ch, done)
	}()
	return ch, nil
}

func (m *completionWaitAgent) Tools() []tool.Tool { return nil }
func (m *completionWaitAgent) Info() agent.Info {
	return agent.Info{Name: m.name, Description: "wait completion"}
}
func (m *completionWaitAgent) SubAgents() []agent.Agent        { return nil }
func (m *completionWaitAgent) FindSubAgent(string) agent.Agent { return nil }

type sessionMirrorAgent struct {
	name string
	inv  string
}

const (
	graphCompletionMsg   = "graph-done"
	graphCompletionAgent = "graph-completion"
	graphStateKey        = "graph_key"
	graphStateValue      = "graph_value"
)

type graphCompletionMockAgent struct {
	name      string
	stateOnly bool
}

type visibleCompletionBarrierAgent struct {
	name string
}

type doubleVisibleCompletionAgent struct {
	name string
}

func (m *graphCompletionMockAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)

		evt := event.NewResponseEvent(
			inv.InvocationID,
			m.name,
			&model.Response{
				Object: graph.ObjectTypeGraphExecution,
				Done:   true,
			},
		)
		if !m.stateOnly {
			evt.Response.Choices = []model.Choice{{
				Message: model.NewAssistantMessage(
					graphCompletionMsg,
				),
			}}
		}
		evt.StateDelta = map[string][]byte{
			graphStateKey: []byte(graphStateValue),
		}
		_ = agent.EmitEvent(ctx, inv, ch, evt)
	}()
	return ch, nil
}

func (m *graphCompletionMockAgent) Tools() []tool.Tool { return nil }
func (m *graphCompletionMockAgent) Info() agent.Info {
	return agent.Info{Name: m.name, Description: "graph completion"}
}
func (m *graphCompletionMockAgent) SubAgents() []agent.Agent {
	return nil
}
func (m *graphCompletionMockAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func (m *visibleCompletionBarrierAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 3)
	go func() {
		defer close(ch)
		rawCompletion := graph.NewGraphCompletionEvent(
			graph.WithCompletionEventInvocationID(inv.InvocationID),
			graph.WithCompletionEventFinalState(graph.State{
				graph.StateKeyLastResponse: "child-final",
			}),
		)
		visibleCompletion, ok := graph.VisibleGraphCompletionEvent(rawCompletion)
		if !ok {
			return
		}
		_ = agent.EmitEvent(ctx, inv, ch, visibleCompletion)
		barrier := event.New(inv.InvocationID, m.name)
		barrier.RequiresCompletion = true
		completionID := agent.GetAppendEventNoticeKey(barrier.ID)
		_ = inv.AddNoticeChannel(ctx, completionID)
		_ = agent.EmitEvent(ctx, inv, ch, barrier)
		if err := inv.AddNoticeChannelAndWait(ctx, completionID, 500*time.Millisecond); err != nil {
			errEvt := event.NewErrorEvent(inv.InvocationID, m.name, model.ErrorTypeFlowError, err.Error())
			_ = agent.EmitEvent(ctx, inv, ch, errEvt)
			return
		}
		if !sessionHasAssistantContent(inv.Session, "child-final") {
			errEvt := event.NewErrorEvent(
				inv.InvocationID,
				m.name,
				model.ErrorTypeFlowError,
				"visible completion not mirrored before barrier completion",
			)
			_ = agent.EmitEvent(ctx, inv, ch, errEvt)
		}
	}()
	return ch, nil
}

func (m *visibleCompletionBarrierAgent) Tools() []tool.Tool { return nil }
func (m *visibleCompletionBarrierAgent) Info() agent.Info {
	return agent.Info{Name: m.name, Description: "visible completion barrier"}
}
func (m *visibleCompletionBarrierAgent) SubAgents() []agent.Agent {
	return nil
}
func (m *visibleCompletionBarrierAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func (m *doubleVisibleCompletionAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)
		firstRaw := graph.NewGraphCompletionEvent(
			graph.WithCompletionEventInvocationID(inv.InvocationID),
			graph.WithCompletionEventFinalState(graph.State{
				graph.StateKeyLastResponse: "first-final",
				"first_key":                "first-value",
			}),
		)
		firstVisible, ok := graph.VisibleGraphCompletionEvent(firstRaw)
		if !ok {
			return
		}
		_ = agent.EmitEvent(ctx, inv, ch, firstVisible)
		secondRaw := graph.NewGraphCompletionEvent(
			graph.WithCompletionEventInvocationID(inv.InvocationID),
			graph.WithCompletionEventFinalState(graph.State{
				graph.StateKeyLastResponse: "second-final",
				"second_key":               "second-value",
			}),
		)
		secondVisible, ok := graph.VisibleGraphCompletionEvent(secondRaw)
		if !ok {
			return
		}
		_ = agent.EmitEvent(ctx, inv, ch, secondVisible)
	}()
	return ch, nil
}

func (m *doubleVisibleCompletionAgent) Tools() []tool.Tool { return nil }
func (m *doubleVisibleCompletionAgent) Info() agent.Info {
	return agent.Info{Name: m.name, Description: "double visible completion"}
}
func (m *doubleVisibleCompletionAgent) SubAgents() []agent.Agent {
	return nil
}
func (m *doubleVisibleCompletionAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func (m *sessionMirrorAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	m.inv = inv.InvocationID
	ch := make(chan *event.Event, 3)
	go func() {
		defer close(ch)

		const toolID = "tool-call-1"
		toolResult := event.NewResponseEvent(
			inv.InvocationID,
			m.name,
			&model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleTool,
						ToolID:  toolID,
						Content: "ok",
					},
				}},
			},
		)
		_ = agent.EmitEvent(ctx, inv, ch, toolResult)

		barrier := event.New(inv.InvocationID, m.name)
		barrier.RequiresCompletion = true
		completionID := agent.GetAppendEventNoticeKey(barrier.ID)
		_ = inv.AddNoticeChannel(ctx, completionID)
		_ = agent.EmitEvent(ctx, inv, ch, barrier)

		if err := inv.AddNoticeChannelAndWait(
			ctx, completionID, 500*time.Millisecond,
		); err != nil {
			errEvt := event.NewErrorEvent(
				inv.InvocationID,
				m.name,
				model.ErrorTypeFlowError,
				err.Error(),
			)
			_ = agent.EmitEvent(ctx, inv, ch, errEvt)
			return
		}

		if !sessionHasToolResult(inv.Session, inv.InvocationID, toolID) {
			errEvt := event.NewErrorEvent(
				inv.InvocationID,
				m.name,
				model.ErrorTypeFlowError,
				"tool result not mirrored to session",
			)
			_ = agent.EmitEvent(ctx, inv, ch, errEvt)
			return
		}

		done := event.NewResponseEvent(inv.InvocationID, m.name, &model.Response{
			Done:    true,
			Choices: []model.Choice{{Message: model.NewAssistantMessage("done")}},
		})
		_ = agent.EmitEvent(ctx, inv, ch, done)
	}()
	return ch, nil
}

func (m *sessionMirrorAgent) Tools() []tool.Tool { return nil }
func (m *sessionMirrorAgent) Info() agent.Info {
	return agent.Info{Name: m.name, Description: "mirror session"}
}
func (m *sessionMirrorAgent) SubAgents() []agent.Agent        { return nil }
func (m *sessionMirrorAgent) FindSubAgent(string) agent.Agent { return nil }

func sessionHasToolResult(
	sess *session.Session,
	invocationID string,
	toolID string,
) bool {
	if sess == nil {
		return false
	}
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	for i := range sess.Events {
		evt := sess.Events[i]
		if evt.InvocationID != invocationID || evt.Response == nil {
			continue
		}
		if !evt.Response.IsToolResultResponse() {
			continue
		}
		for _, id := range evt.Response.GetToolResultIDs() {
			if id == toolID {
				return true
			}
		}
	}
	return false
}

func sessionHasAssistantContent(sess *session.Session, content string) bool {
	return countSessionAssistantContent(sess, content) > 0
}

func countSessionAssistantContent(sess *session.Session, content string) int {
	if sess == nil {
		return 0
	}
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()
	var count int
	for i := range sess.Events {
		evt := sess.Events[i]
		if evt.Response == nil || evt.IsPartial || !evt.IsValidContent() {
			continue
		}
		for _, choice := range evt.Response.Choices {
			if choice.Message.Role == model.RoleAssistant &&
				choice.Message.Content == content {
				count++
			}
		}
	}
	return count
}

type finalChunkView struct {
	Result     any
	StateDelta map[string][]byte
}

func requireFinalChunkView(t *testing.T, content any) finalChunkView {
	t.Helper()

	switch v := content.(type) {
	case tool.FinalResultChunk:
		return finalChunkView{Result: v.Result}
	case *tool.FinalResultChunk:
		if v == nil {
			require.FailNow(t, "unexpected nil final result chunk")
		}
		return finalChunkView{Result: v.Result}
	case tool.FinalResultStateChunk:
		return finalChunkView{Result: v.Result, StateDelta: v.StateDelta}
	case *tool.FinalResultStateChunk:
		if v == nil {
			require.FailNow(t, "unexpected nil final result state chunk")
		}
		return finalChunkView{Result: v.Result, StateDelta: v.StateDelta}
	default:
		require.FailNowf(t, "unexpected final result chunk type", "%T", content)
		return finalChunkView{}
	}
}

type filterKeyAgent struct {
	name string
	seen string
}

func (m *filterKeyAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	m.seen = inv.GetEventFilterKey()
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{Response: &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage(m.seen)}}}}
	close(ch)
	return ch, nil
}
func (m *filterKeyAgent) Tools() []tool.Tool              { return nil }
func (m *filterKeyAgent) Info() agent.Info                { return agent.Info{Name: m.name, Description: "fk"} }
func (m *filterKeyAgent) SubAgents() []agent.Agent        { return nil }
func (m *filterKeyAgent) FindSubAgent(string) agent.Agent { return nil }

type structuredOutputCaptureModel struct {
	name       string
	mu         sync.Mutex
	seen       bool
	schemaName string
}

func (m *structuredOutputCaptureModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	if req != nil &&
		req.StructuredOutput != nil &&
		req.StructuredOutput.JSONSchema != nil {
		m.seen = true
		m.schemaName = req.StructuredOutput.JSONSchema.Name
	}
	m.mu.Unlock()
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage(`{"status":"ok"}`),
		}},
	}
	close(ch)
	return ch, nil
}

func (m *structuredOutputCaptureModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *structuredOutputCaptureModel) Snapshot() (bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seen, m.schemaName
}

func TestTool_Call_MirrorsChildEventsToSession(t *testing.T) {
	sa := &sessionMirrorAgent{name: "session-mirror"}
	at := NewTool(sa)

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	got, err := at.Call(ctx, []byte(`{"request":"hi"}`))
	require.NoError(t, err)
	require.Equal(t, "done", got)
	require.NotEmpty(t, sa.inv)
	require.True(t, sessionHasToolResult(sess, sa.inv, "tool-call-1"))
}

func TestTool_Call_UsesSessionAppender(t *testing.T) {
	sa := &sessionMirrorAgent{name: "session-mirror"}
	at := NewTool(sa)

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)

	var appendCount int
	appender.Attach(parent, func(
		ctx context.Context,
		evt *event.Event,
	) error {
		appendCount++
		sess.UpdateUserSession(evt)
		return nil
	})

	ctx := agent.NewInvocationContext(context.Background(), parent)
	got, err := at.Call(ctx, []byte(`{"request":"hi"}`))
	require.NoError(t, err)
	require.Equal(t, "done", got)
	require.NotEmpty(t, sa.inv)
	require.Greater(t, appendCount, 0)
	require.True(t, sessionHasToolResult(sess, sa.inv, "tool-call-1"))
}

func TestTool_Call_AppenderError_NoDuplicateEvents(t *testing.T) {
	sa := &sessionMirrorAgent{name: "session-mirror"}
	at := NewTool(sa)

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	appender.Attach(parent, func(
		ctx context.Context,
		evt *event.Event,
	) error {
		sess.UpdateUserSession(evt)
		return errors.New("append failed")
	})

	ctx := agent.NewInvocationContext(context.Background(), parent)
	got, err := at.Call(ctx, []byte(`{"request":"hi"}`))
	require.NoError(t, err)
	require.Equal(t, "done", got)
	require.NotEmpty(t, sa.inv)

	const toolID = "tool-call-1"
	require.Equal(t, 1, countToolResultEvents(sess, sa.inv, toolID))
	require.Equal(t, 3, sess.GetEventCount())
}

func TestTool_Call_GraphCompletion_StripsChoicesForPersistence(t *testing.T) {
	sa := &graphCompletionMockAgent{name: graphCompletionAgent}
	at := NewTool(sa)

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)

	var persistedGraph *event.Event
	appender.Attach(parent, func(
		ctx context.Context,
		evt *event.Event,
	) error {
		if evt != nil && evt.Done &&
			evt.Object == graph.ObjectTypeGraphExecution {
			copyEvt := *evt
			if evt.Response != nil {
				copyEvt.Response = evt.Response.Clone()
			}
			persistedGraph = &copyEvt
		}
		sess.UpdateUserSession(evt)
		return nil
	})

	ctx := agent.NewInvocationContext(context.Background(), parent)
	got, err := at.Call(ctx, []byte(`{"request":"hi"}`))
	require.NoError(t, err)
	require.Equal(t, graphCompletionMsg, got)

	require.NotNil(t, persistedGraph)
	require.NotNil(t, persistedGraph.Response)
	require.Len(t, persistedGraph.Response.Choices, 0)
	require.Contains(t, persistedGraph.StateDelta, graphStateKey)
	require.Equal(
		t,
		[]byte(graphStateValue),
		persistedGraph.StateDelta[graphStateKey],
	)
	require.Contains(t, sess.State, graphStateKey)
	require.Equal(t, []byte(graphStateValue), sess.State[graphStateKey])
}

func TestTool_shouldMirrorEventToSession_Cases(t *testing.T) {
	t.Run("nil event", func(t *testing.T) {
		require.False(t, shouldMirrorEventToSession(nil))
	})

	t.Run("state delta", func(t *testing.T) {
		evt := event.New("inv", "author")
		evt.StateDelta = map[string][]byte{"k": []byte("v")}
		require.True(t, shouldMirrorEventToSession(evt))
	})

	t.Run("no response", func(t *testing.T) {
		evt := &event.Event{}
		require.False(t, shouldMirrorEventToSession(evt))
	})

	t.Run("partial response", func(t *testing.T) {
		evt := event.NewResponseEvent("inv", "author", &model.Response{
			IsPartial: true,
			Choices: []model.Choice{{
				Delta: model.Message{Content: "x"},
			}},
		})
		require.False(t, shouldMirrorEventToSession(evt))
	})

	t.Run("invalid content", func(t *testing.T) {
		evt := event.NewResponseEvent("inv", "author", &model.Response{
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage(""),
			}},
		})
		require.False(t, shouldMirrorEventToSession(evt))
	})
}

func TestTool_visibleCompletionSessionEvent_RestoresAgentAuthor(t *testing.T) {
	rawCompletion := graph.NewGraphCompletionEvent(
		graph.WithCompletionEventInvocationID("inv"),
		graph.WithCompletionEventFinalState(graph.State{
			graph.StateKeyLastResponse: "answer",
		}),
	)

	visible := visibleCompletionSessionEvent(rawCompletion, "child-agent")

	require.NotNil(t, visible)
	require.Equal(t, "child-agent", visible.Author)
	require.Equal(t, model.ObjectTypeChatCompletion, visible.Object)
}

func TestTool_sessionHasEventID_Cases(t *testing.T) {
	require.False(t, sessionHasEventID(nil, "id"))

	inv := agent.NewInvocation()
	require.False(t, sessionHasEventID(inv, "id"))
	require.False(t, sessionHasEventID(inv, ""))

	sess := session.NewSession("app", "user", "session")
	inv.Session = sess

	const (
		existsID  = "exists"
		missingID = "missing"
	)
	evt := event.NewResponseEvent(inv.InvocationID, "a", &model.Response{
		Choices: []model.Choice{{
			Message: model.NewUserMessage("seed"),
		}},
	})
	evt.ID = existsID
	sess.UpdateUserSession(evt)

	require.True(t, sessionHasEventID(inv, existsID))
	require.False(t, sessionHasEventID(inv, missingID))
}

func TestTool_appendEvent_AppenderError_FallbackUpdatesSession(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})
	sess := session.NewSession("app", "user", "session")
	inv := agent.NewInvocation(agent.WithInvocationSession(sess))

	const appendErrMsg = "append failed"
	appender.Attach(inv, func(
		ctx context.Context,
		evt *event.Event,
	) error {
		return errors.New(appendErrMsg)
	})

	evt := event.NewResponseEvent(inv.InvocationID, "a", &model.Response{
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("ok"),
		}},
	})

	sess.UpdateUserSession(event.NewResponseEvent(
		inv.InvocationID,
		"user",
		&model.Response{
			Choices: []model.Choice{{
				Message: model.NewUserMessage("seed"),
			}},
		},
	))
	at.appendEvent(context.Background(), inv, evt)

	require.Equal(t, 2, sess.GetEventCount())
}

func TestTool_appendEvent_AppenderError_EmptyIDUpdatesSession(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})
	sess := session.NewSession("app", "user", "session")
	inv := agent.NewInvocation(agent.WithInvocationSession(sess))

	appender.Attach(inv, func(
		ctx context.Context,
		evt *event.Event,
	) error {
		return errors.New("append failed")
	})

	evt := event.NewResponseEvent(inv.InvocationID, "a", &model.Response{
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("ok"),
		}},
	})
	evt.ID = ""

	sess.UpdateUserSession(event.NewResponseEvent(
		inv.InvocationID,
		"user",
		&model.Response{
			Choices: []model.Choice{{
				Message: model.NewUserMessage("seed"),
			}},
		},
	))
	at.appendEvent(context.Background(), inv, evt)

	require.Equal(t, 2, sess.GetEventCount())
}

func TestTool_ensureUserMessageForCall_EarlyReturns(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})
	sess := session.NewSession("app", "user", "session")

	t.Run("non user role", func(t *testing.T) {
		inv := agent.NewInvocation(
			agent.WithInvocationSession(sess),
			agent.WithInvocationMessage(model.NewAssistantMessage("x")),
		)
		at.ensureUserMessageForCall(context.Background(), inv)
		require.Equal(t, 0, sess.GetEventCount())
	})

	t.Run("empty content", func(t *testing.T) {
		inv := agent.NewInvocation(
			agent.WithInvocationSession(sess),
			agent.WithInvocationMessage(model.NewUserMessage("")),
		)
		at.ensureUserMessageForCall(context.Background(), inv)
		require.Equal(t, 0, sess.GetEventCount())
	})
}

func TestTool_ensureUserMessageForCall_SkipsWhenUserExists(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})
	sess := session.NewSession("app", "user", "session")
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
	)

	userEvt := event.NewResponseEvent(inv.InvocationID, "user", &model.Response{
		Choices: []model.Choice{{
			Message: model.NewUserMessage("seed"),
		}},
	})
	sess.UpdateUserSession(userEvt)

	at.ensureUserMessageForCall(context.Background(), inv)
	require.Equal(t, 1, sess.GetEventCount())
}

func TestTool_ensureUserMessageForCall_NilCases(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})
	at.ensureUserMessageForCall(context.Background(), nil)

	inv := agent.NewInvocation()
	at.ensureUserMessageForCall(context.Background(), inv)
}

func TestTool_wrapWithCompletion_NotifyCompletionError(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})

	src := make(chan *event.Event, 1)
	evt := event.New("inv", "author")
	evt.RequiresCompletion = true
	src <- evt
	close(src)

	badInv := &agent.Invocation{}
	out := at.wrapWithCompletion(context.Background(), badInv, src)
	got, ok := <-out
	require.True(t, ok)
	require.Same(t, evt, got)

	_, ok = <-out
	require.False(t, ok)
}

func TestTool_wrapWithCallSemantics_NilInvocationReturnsSrc(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})

	src := make(chan *event.Event, 1)
	src <- event.New("inv", "author")
	close(src)

	out := at.wrapWithCallSemantics(context.Background(), nil, src)
	require.Equal(
		t,
		reflect.ValueOf(src).Pointer(),
		reflect.ValueOf(out).Pointer(),
	)
}

func TestTool_wrapWithCallSemantics_NotifyCompletionError(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})

	sess := session.NewSession("app", "user", "session")
	badInv := &agent.Invocation{
		Session: sess,
		Message: model.NewAssistantMessage("x"),
	}

	src := make(chan *event.Event, 1)
	evt := event.New("inv", "author")
	evt.RequiresCompletion = true
	src <- evt
	close(src)

	out := at.wrapWithCallSemantics(context.Background(), badInv, src)
	_, ok := <-out
	require.True(t, ok)
	_, ok = <-out
	require.False(t, ok)
}

func TestTool_wrapWithCallSemantics_ForwardsNilEvents(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})
	sess := session.NewSession("app", "user", "session")
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(model.NewAssistantMessage("x")),
	)

	src := make(chan *event.Event, 1)
	src <- nil
	close(src)

	out := at.wrapWithCallSemantics(context.Background(), inv, src)
	got, ok := <-out
	require.True(t, ok)
	require.Nil(t, got)
	_, ok = <-out
	require.False(t, ok)
}

func TestTool_wrapWithCallSemantics_SessionNilUsesCompletion(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})
	inv := agent.NewInvocation()

	src := make(chan *event.Event, 1)
	evt := event.New(inv.InvocationID, "author")
	evt.RequiresCompletion = true
	completionID := agent.GetAppendEventNoticeKey(evt.ID)
	require.NotNil(t, inv.AddNoticeChannel(context.Background(), completionID))
	src <- evt
	close(src)

	out := at.wrapWithCallSemantics(context.Background(), inv, src)
	_, ok := <-out
	require.True(t, ok)
	_, ok = <-out
	require.False(t, ok)

	require.NoError(t, inv.AddNoticeChannelAndWait(
		context.Background(), completionID, time.Second,
	))
}

func TestTool_isGraphCompletionEvent_NilCases(t *testing.T) {
	require.False(t, isGraphCompletionEvent(nil))
	require.False(t, isGraphCompletionEvent(&event.Event{}))
}

func TestTool_appendEvent_NilCases(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})
	at.appendEvent(context.Background(), nil, nil)

	inv := agent.NewInvocation()
	at.appendEvent(context.Background(), inv, nil)
	at.appendEvent(context.Background(), inv, event.New("inv", "author"))
}

func TestTool_StreamInner_And_StreamableCall(t *testing.T) {
	sa := &streamingMockAgent{name: "stream-agent"}
	at := NewTool(sa, WithStreamInner(true))

	if !at.StreamInner() {
		t.Fatalf("expected StreamInner to be true")
	}

	// Prepare a parent invocation context with a session and a different
	// filter key, to ensure sub agent overrides it.
	sess := &session.Session{}
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	// Invoke stream
	reader, err := at.StreamableCall(ctx, []byte(`{"request":"hi"}`))
	if err != nil {
		t.Fatalf("StreamableCall error: %v", err)
	}
	defer reader.Close()

	// Expect to receive forwarded event chunks
	var got []string
	for i := 0; i < 3; i++ { // Now expecting 4 events: tool input + original 3 events
		chunk, err := reader.Recv()
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
		if ev, ok := chunk.Content.(*event.Event); ok {
			if len(ev.Choices) > 0 {
				if ev.Choices[0].Delta.Content != "" {
					got = append(got, ev.Choices[0].Delta.Content)
				} else if ev.Choices[0].Message.Content != "" {
					got = append(got, ev.Choices[0].Message.Content)
				}
			}
		} else {
			t.Fatalf("expected chunk content to be *event.Event, got %T", chunk.Content)
		}
	}
	// We now get 4 events: tool input event + original 3 events (delta1, delta2, final full)
	if got[0] != "hello" || got[1] != " world" || got[2] != "ignored full" {
		t.Fatalf("unexpected forwarded contents: %#v", got)
	}

	// Assert the sub agent saw a filter key starting with its own name (now includes UUID suffix).
	expectedPrefix := sa.name + "-"
	if !strings.HasPrefix(sa.seenFilterKey, expectedPrefix) {
		t.Fatalf("expected sub agent filter key to start with %q, got %q", expectedPrefix, sa.seenFilterKey)
	}
}

func countToolResultEvents(
	sess *session.Session,
	invocationID string,
	toolID string,
) int {
	if sess == nil {
		return 0
	}
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	var count int
	for i := range sess.Events {
		evt := sess.Events[i]
		if evt.InvocationID != invocationID || evt.Response == nil {
			continue
		}
		if !evt.Response.IsToolResultResponse() {
			continue
		}
		for _, id := range evt.Response.GetToolResultIDs() {
			if id == toolID {
				count++
				break
			}
		}
	}
	return count
}

func TestTool_HistoryScope_ParentBranch_Streamable_FilterKeyPrefix(t *testing.T) {
	sa := &streamingMockAgent{name: "stream-agent"}
	at := NewTool(sa, WithStreamInner(true), WithHistoryScope(HistoryScopeParentBranch))

	// Parent invocation with base filter key.
	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	r, err := at.StreamableCall(ctx, []byte(`{"request":"hi"}`))
	if err != nil {
		t.Fatalf("StreamableCall error: %v", err)
	}
	defer r.Close()
	// Drain stream
	for i := 0; i < 3; i++ {
		if _, err := r.Recv(); err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
	}

	// Expect child filter key prefixed by parent key.
	if !strings.HasPrefix(sa.seenFilterKey, "parent-agent/"+sa.name+"-") {
		t.Fatalf("expected child filter key to start with %q, got %q", "parent-agent/"+sa.name+"-", sa.seenFilterKey)
	}
}

func TestTool_StreamableCall_FlushesParentSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	flushCh := make(chan *flush.FlushRequest, 1)
	flush.Attach(ctx, parent, flushCh)

	at := NewTool(&streamingMockAgent{name: "stream-agent"}, WithStreamInner(true))
	toolCtx := agent.NewInvocationContext(ctx, parent)

	acked := make(chan struct{}, 1)
	go func() {
		select {
		case req := <-flushCh:
			require.NotNil(t, req)
			require.NotNil(t, req.ACK)
			close(req.ACK)
			acked <- struct{}{}
		case <-ctx.Done():
		}
	}()

	reader, err := at.StreamableCall(toolCtx, []byte(`{"request":"hi"}`))
	require.NoError(t, err)
	defer reader.Close()

	recvCount := 0
	for {
		_, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		recvCount++
	}
	require.Equal(t, 3, recvCount)

	select {
	case <-acked:
	default:
		t.Fatalf("expected flush request to be handled")
	}
}

func TestTool_StreamableCall_NotifiesCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	at := NewTool(&completionWaitAgent{name: "waiter"}, WithStreamInner(true))
	toolCtx := agent.NewInvocationContext(ctx, parent)

	reader, err := at.StreamableCall(toolCtx, []byte(`{"request":"payload"}`))
	require.NoError(t, err)
	defer reader.Close()

	var contents []string
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		ev, ok := chunk.Content.(*event.Event)
		require.True(t, ok)
		require.Nil(t, ev.Error)
		if ev.Response != nil && len(ev.Response.Choices) > 0 {
			msg := ev.Response.Choices[0].Message
			if msg.Content != "" {
				contents = append(contents, msg.Content)
			}
		}
	}

	require.Contains(t, contents, "done")
}

func TestTool_StreamableCall_DefersCompletionToRunner(t *testing.T) {
	const toolCallID = "call-1"

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	appender.Attach(parent, func(_ context.Context, evt *event.Event) error {
		if evt == nil {
			return nil
		}
		parent.Session.UpdateUserSession(evt)
		return nil
	})

	at := NewTool(&completionWaitAgent{name: "waiter"}, WithStreamInner(true))

	toolCtx := agent.NewInvocationContext(ctx, parent)
	ctxWithToolCallID := context.WithValue(
		toolCtx,
		tool.ContextKeyToolCallID{},
		toolCallID,
	)

	reader, err := at.StreamableCall(
		tool.WithFinalResultChunks(ctxWithToolCallID),
		[]byte(`{"request":"payload"}`),
	)
	require.NoError(t, err)
	defer reader.Close()

	first, err := reader.Recv()
	require.NoError(t, err)
	barrierEvt, ok := first.Content.(*event.Event)
	require.True(t, ok)
	require.True(t, barrierEvt.RequiresCompletion)

	completionID := agent.GetAppendEventNoticeKey(barrierEvt.ID)
	noticeCh := parent.AddNoticeChannel(ctx, completionID)
	select {
	case <-noticeCh:
		t.Fatalf("expected completion to be deferred to runner")
	default:
	}

	require.Len(t, parent.Session.Events, 0)
	require.NoError(t, parent.NotifyCompletion(ctx, completionID))

	var contents []string
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		ev, ok := chunk.Content.(*event.Event)
		require.True(t, ok)
		require.Nil(t, ev.Error)
		if ev.Response != nil && len(ev.Response.Choices) > 0 {
			msg := ev.Response.Choices[0].Message
			if msg.Content != "" {
				contents = append(contents, msg.Content)
			}
		}
	}
	require.Contains(t, contents, "done")
	require.Len(t, parent.Session.Events, 0)
}

func TestTool_StreamableCall_DefersCompletion_FlushesVisibleCompletionBeforeBarrierNotification(
	t *testing.T,
) {
	const toolCallID = "call-1"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	appender.Attach(parent, func(_ context.Context, evt *event.Event) error {
		if evt == nil {
			return nil
		}
		parent.Session.UpdateUserSession(evt)
		return nil
	})
	at := NewTool(
		&visibleCompletionBarrierAgent{name: "visible-barrier-agent"},
		WithStreamInner(true),
	)
	toolCtx := agent.NewInvocationContext(ctx, parent)
	ctxWithToolCallID := context.WithValue(
		toolCtx,
		tool.ContextKeyToolCallID{},
		toolCallID,
	)
	reader, err := at.StreamableCall(
		tool.WithFinalResultChunks(ctxWithToolCallID),
		[]byte(`{"request":"payload"}`),
	)
	require.NoError(t, err)
	defer reader.Close()
	first, err := reader.Recv()
	require.NoError(t, err)
	barrierEvt, ok := first.Content.(*event.Event)
	require.True(t, ok)
	require.True(t, barrierEvt.RequiresCompletion)
	require.True(t, sessionHasAssistantContent(sess, "child-final"))
	completionID := agent.GetAppendEventNoticeKey(barrierEvt.ID)
	require.NoError(t, parent.NotifyCompletion(ctx, completionID))
	var sawFinalChunk bool
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		if evt, ok := chunk.Content.(*event.Event); ok {
			require.False(t, evt.IsError())
			continue
		}
		finalChunk := requireFinalChunkView(t, chunk.Content)
		sawFinalChunk = true
		require.Equal(t, "child-final", finalChunk.Result)
	}
	require.True(t, sawFinalChunk)
}

func TestTool_StreamableCall_DefersCompletion_PersistsStateOnlyCompletionToSharedSession(
	t *testing.T,
) {
	const toolCallID = "call-1"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	appender.Attach(parent, func(_ context.Context, evt *event.Event) error {
		if evt == nil {
			return nil
		}
		parent.Session.UpdateUserSession(evt)
		return nil
	})
	at := NewTool(
		&graphCompletionMockAgent{name: graphCompletionAgent, stateOnly: true},
		WithStreamInner(true),
		WithHistoryScope(HistoryScopeParentBranch),
	)
	toolCtx := agent.NewInvocationContext(ctx, parent)
	ctxWithToolCallID := context.WithValue(
		toolCtx,
		tool.ContextKeyToolCallID{},
		toolCallID,
	)
	reader, err := at.StreamableCall(
		tool.WithFinalResultChunks(ctxWithToolCallID),
		[]byte(`{"request":"payload"}`),
	)
	require.NoError(t, err)
	defer reader.Close()
	var sawFinalChunk bool
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		if _, ok := chunk.Content.(*event.Event); ok {
			continue
		}
		finalChunk := requireFinalChunkView(t, chunk.Content)
		sawFinalChunk = true
		require.Equal(t, []byte(graphStateValue), finalChunk.StateDelta[graphStateKey])
	}
	require.True(t, sawFinalChunk)
	stateValue, ok := sess.GetState(graphStateKey)
	require.True(t, ok)
	require.Equal(t, []byte(graphStateValue), stateValue)
	require.False(t, sessionHasAssistantContent(sess, graphCompletionMsg))
}

func TestTool_StreamableCall_DefersCompletion_PreservesConsecutiveVisibleCompletionStatesInSharedSession(
	t *testing.T,
) {
	const toolCallID = "call-1"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	appender.Attach(parent, func(_ context.Context, evt *event.Event) error {
		if evt == nil {
			return nil
		}
		parent.Session.UpdateUserSession(evt)
		return nil
	})
	at := NewTool(
		&doubleVisibleCompletionAgent{name: "double-visible-agent"},
		WithStreamInner(true),
	)
	toolCtx := agent.NewInvocationContext(ctx, parent)
	ctxWithToolCallID := context.WithValue(
		toolCtx,
		tool.ContextKeyToolCallID{},
		toolCallID,
	)
	reader, err := at.StreamableCall(
		tool.WithFinalResultChunks(ctxWithToolCallID),
		[]byte(`{"request":"payload"}`),
	)
	require.NoError(t, err)
	defer reader.Close()
	for {
		_, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
	}
	firstValue, ok := sess.GetState("first_key")
	require.True(t, ok)
	require.Equal(t, []byte(`"first-value"`), firstValue)
	secondValue, ok := sess.GetState("second_key")
	require.True(t, ok)
	require.Equal(t, []byte(`"second-value"`), secondValue)
}

func TestTool_StreamableCall_DefersCompletion_SuppressesBarrierEventsWhenDisableGraphExecutorEvents(
	t *testing.T,
) {
	const toolCallID = "call-1"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "child-final"}, nil
	})
	compiled := sg.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	ga, err := graphagent.New("graph-child", compiled)
	require.NoError(t, err)
	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
			agent.WithDisableGraphExecutorEvents(true),
		)),
	)
	barrier.Enable(parent)
	appender.Attach(parent, func(_ context.Context, evt *event.Event) error {
		if evt == nil {
			return nil
		}
		parent.Session.UpdateUserSession(evt)
		return nil
	})
	at := NewTool(ga, WithStreamInner(true))
	toolCtx := agent.NewInvocationContext(ctx, parent)
	ctxWithToolCallID := context.WithValue(
		toolCtx,
		tool.ContextKeyToolCallID{},
		toolCallID,
	)
	reader, err := at.StreamableCall(
		tool.WithFinalResultChunks(ctxWithToolCallID),
		[]byte(`{"request":"payload"}`),
	)
	require.NoError(t, err)
	defer reader.Close()
	var sawBarrier bool
	var sawFinalChunk bool
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		if evt, ok := chunk.Content.(*event.Event); ok {
			if evt.Object == graph.ObjectTypeGraphBarrier ||
				evt.Object == graph.ObjectTypeGraphNodeBarrier {
				sawBarrier = true
			}
			continue
		}
		finalChunk := requireFinalChunkView(t, chunk.Content)
		sawFinalChunk = true
		require.Equal(t, "child-final", finalChunk.Result)
	}
	require.False(t, sawBarrier)
	require.True(t, sawFinalChunk)
	require.True(t, sessionHasAssistantContent(sess, "child-final"))
}

type toolCallIDDroppingContext struct {
	context.Context
}

func (c toolCallIDDroppingContext) Value(key any) any {
	if _, ok := key.(tool.ContextKeyToolCallID); ok {
		return nil
	}
	return c.Context.Value(key)
}

func TestTool_StreamableCall_DefersCompletion_ContextCloneDropsToolCallID(
	t *testing.T,
) {
	const (
		testToolCallID    = "call-1"
		testSessionApp    = "app"
		testSessionUser   = "user"
		testSessionID     = "session"
		testParentFilter  = "parent-agent"
		testWaitAgentName = "waiter"
		testToolPayload   = `{"request":"payload"}`
	)

	t.Cleanup(func() { agent.SetGoroutineContextCloner(nil) })
	agent.SetGoroutineContextCloner(func(ctx context.Context) context.Context {
		return toolCallIDDroppingContext{Context: ctx}
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	parent := agent.NewInvocation(
		agent.WithInvocationSession(
			session.NewSession(
				testSessionApp,
				testSessionUser,
				testSessionID,
			),
		),
		agent.WithInvocationEventFilterKey(testParentFilter),
	)
	appender.Attach(parent, func(_ context.Context, evt *event.Event) error {
		if evt == nil {
			return nil
		}
		parent.Session.UpdateUserSession(evt)
		return nil
	})

	at := NewTool(
		&completionWaitAgent{name: testWaitAgentName},
		WithStreamInner(true),
	)

	toolCtx := agent.NewInvocationContext(ctx, parent)
	ctxWithToolCallID := context.WithValue(
		toolCtx,
		tool.ContextKeyToolCallID{},
		testToolCallID,
	)

	reader, err := at.StreamableCall(
		ctxWithToolCallID,
		[]byte(testToolPayload),
	)
	require.NoError(t, err)
	defer reader.Close()

	first, err := reader.Recv()
	require.NoError(t, err)
	barrierEvt, ok := first.Content.(*event.Event)
	require.True(t, ok)
	require.True(t, barrierEvt.RequiresCompletion)

	require.Len(t, parent.Session.Events, 0)

	completionID := agent.GetAppendEventNoticeKey(barrierEvt.ID)
	require.NoError(t, parent.NotifyCompletion(ctx, completionID))

	for {
		_, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
	}

	require.Len(t, parent.Session.Events, 0)
}

func TestShouldDeferStreamCompletion_NoSession(t *testing.T) {
	ctxWithID := context.WithValue(
		context.Background(),
		tool.ContextKeyToolCallID{},
		"call-1",
	)
	require.False(t, shouldDeferStreamCompletion(ctxWithID, nil))

	inv := agent.NewInvocation()
	require.False(t, shouldDeferStreamCompletion(ctxWithID, inv))
}

func TestTool_wrapWithCompletion_NilInvocation(t *testing.T) {
	at := NewTool(&mockAgent{name: "wrap", description: "wrap"})
	src := make(chan *event.Event, 1)
	src <- &event.Event{Response: &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("ok")}}}}
	close(src)

	out := at.wrapWithCompletion(context.Background(), nil, src)
	require.Equal(t, reflect.ValueOf(src).Pointer(), reflect.ValueOf(out).Pointer())

	evt, ok := <-out
	require.True(t, ok)
	require.NotNil(t, evt)

	_, ok = <-out
	require.False(t, ok)
}

// inspectAgent collects matched contents from session using the invocation's filter key
// and returns them joined by '|'.
type inspectAgent struct{ name string }

func (m *inspectAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	fk := inv.GetEventFilterKey()
	var matched []string
	if inv.Session != nil {
		for i := range inv.Session.Events {
			evt := inv.Session.Events[i]
			if evt.Filter(fk) && evt.Response != nil && len(evt.Response.Choices) > 0 {
				msg := evt.Response.Choices[0].Message
				if msg.Content != "" {
					matched = append(matched, msg.Content)
				}
			}
		}
	}
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{Response: &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage(strings.Join(matched, "|"))}}}}
	close(ch)
	return ch, nil
}

func (m *inspectAgent) Tools() []tool.Tool              { return nil }
func (m *inspectAgent) Info() agent.Info                { return agent.Info{Name: m.name, Description: "inspect"} }
func (m *inspectAgent) SubAgents() []agent.Agent        { return nil }
func (m *inspectAgent) FindSubAgent(string) agent.Agent { return nil }

func TestTool_HistoryScope_ParentBranch_Call_InheritsParentHistory(t *testing.T) {
	ia := &inspectAgent{name: "child"}
	at := NewTool(ia, WithHistoryScope(HistoryScopeParentBranch))

	// Build parent session with a prior user event under parent branch so that
	// session filtering preserves it when seeding the snapshot.
	sess := session.NewSession("parent-app", "parent-user", "parent-session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-branch"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	// Append a parent user event (content "PARENT") so that snapshot/session
	// filtering retains it as part of history.
	parentEvt := event.NewResponseEvent(parent.InvocationID, "parent", &model.Response{
		Choices: []model.Choice{{Message: model.NewUserMessage("PARENT")}},
	})
	agent.InjectIntoEvent(parent, parentEvt)
	sess.Events = append(sess.Events, *parentEvt)

	// Call the tool with child input.
	out, err := at.Call(ctx, []byte(`{"request":"CHILD"}`))
	if err != nil {
		t.Fatalf("call error: %v", err)
	}
	s, _ := out.(string)
	// Expect both parent content and tool input to be visible via filter inheritance.
	if !strings.Contains(s, "PARENT") || strings.Contains(s, `{"request":"CHILD"}`) {
		t.Fatalf("expected output to contain parent content (not raw child request), got: %q", s)
	}
}

func TestTool_HistoryScope_Isolated_Streamable_NoParentPrefix(t *testing.T) {
	sa := &streamingMockAgent{name: "stream-agent"}
	at := NewTool(sa, WithStreamInner(true), WithHistoryScope(HistoryScopeIsolated))

	sess := &session.Session{}
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	r, err := at.StreamableCall(ctx, []byte(`{"request":"hi"}`))
	if err != nil {
		t.Fatalf("StreamableCall error: %v", err)
	}
	defer r.Close()
	for i := 0; i < 3; i++ { // drain
		if _, err := r.Recv(); err != nil {
			t.Fatalf("stream read error: %v", err)
		}
	}
	// Expect isolated (no parent prefix)
	if !strings.HasPrefix(sa.seenFilterKey, sa.name+"-") || strings.HasPrefix(sa.seenFilterKey, "parent-agent/") {
		t.Fatalf("expected isolated child key starting with %q, got %q", sa.name+"-", sa.seenFilterKey)
	}
}

func TestTool_StreamableCall_DisableGraphCompletionEvent_WithFinalResultChunks_KeepsFinalResult(t *testing.T) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "child-final"}, nil
	})
	compiled := sg.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	ga, err := graphagent.New("graph-child-stream", compiled)
	require.NoError(t, err)
	at := NewTool(ga, WithStreamInner(true), WithHistoryScope(HistoryScopeParentBranch))
	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	reader, err := at.StreamableCall(
		tool.WithFinalResultChunks(ctx),
		[]byte(`{"request":"ignored"}`),
	)
	require.NoError(t, err)
	defer reader.Close()
	var finalResult any
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		if evt, ok := chunk.Content.(*event.Event); ok {
			require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
			continue
		}
		finalChunk := requireFinalChunkView(t, chunk.Content)
		finalResult = finalChunk.Result
		require.Equal(t, []byte(`"child-final"`), finalChunk.StateDelta[graph.StateKeyLastResponse])
	}
	require.Equal(t, "child-final", finalResult)
}

func TestTool_StreamableCall_DisableGraphCompletionEvent_DefaultsToVisibleCompletionEvents(t *testing.T) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "child-final"}, nil
	})
	compiled := sg.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	ga, err := graphagent.New("graph-child-stream", compiled)
	require.NoError(t, err)
	at := NewTool(ga, WithStreamInner(true), WithHistoryScope(HistoryScopeParentBranch))
	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	reader, err := at.StreamableCall(ctx, []byte(`{"request":"ignored"}`))
	require.NoError(t, err)
	defer reader.Close()
	var sawVisibleCompletion bool
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		evt, ok := chunk.Content.(*event.Event)
		require.True(t, ok)
		require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
		if graph.IsVisibleGraphCompletionEvent(evt) {
			sawVisibleCompletion = true
			require.Equal(t, []byte(`"child-final"`), evt.StateDelta[graph.StateKeyLastResponse])
			require.Len(t, evt.Response.Choices, 1)
			require.Equal(t, "child-final", evt.Response.Choices[0].Message.Content)
		}
	}
	require.True(t, sawVisibleCompletion)
}

func TestTool_StreamableCall_DisableGraphCompletionEvent_PreservesFinalTextInSharedSession(t *testing.T) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "child-final"}, nil
	})
	compiled := sg.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	ga, err := graphagent.New("graph-child-stream", compiled)
	require.NoError(t, err)
	at := NewTool(ga, WithStreamInner(true), WithHistoryScope(HistoryScopeParentBranch))
	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	reader, err := at.StreamableCall(ctx, []byte(`{"request":"ignored"}`))
	require.NoError(t, err)
	defer reader.Close()
	for {
		_, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
	}
	require.True(t, sessionHasAssistantContent(sess, "child-final"))
	require.Equal(t, 1, countSessionAssistantContent(sess, "child-final"))
}

func TestTool_StreamableCall_FallbackRunnerPreservesDisableGraphCompletionEvent(t *testing.T) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "child-final"}, nil
	})
	compiled := sg.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	ga, err := graphagent.New("graph-child-fallback", compiled)
	require.NoError(t, err)
	at := NewTool(ga, WithStreamInner(true))
	parent := agent.NewInvocation(
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	reader, err := at.StreamableCall(
		tool.WithFinalResultChunks(ctx),
		[]byte(`{"request":"ignored"}`),
	)
	require.NoError(t, err)
	defer reader.Close()
	var contents []string
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		if evt, ok := chunk.Content.(*event.Event); ok {
			require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
			if content, ok := assistantMessageContent(evt); ok && content != "" {
				contents = append(contents, content)
			}
			continue
		}
	}
	require.Contains(t, contents, "child-final")
}

func TestTool_FallbackRunnerRunOptions_PreserveOnlyCompatibilityControls(t *testing.T) {
	at := NewTool(&mockAgent{name: "child"}, WithStreamInner(true))
	parent := agent.NewInvocation(
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithStreamMode(agent.StreamModeUpdates),
			agent.WithGraphEmitFinalModelResponses(true),
			agent.WithDisableGraphCompletionEvent(true),
			agent.WithDisableGraphExecutorEvents(true),
			agent.WithEventChannelBufferSize(7),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	runOptions := agent.NewRunOptions(at.fallbackRunnerRunOptions(ctx)...)
	child := agent.NewInvocation(agent.WithInvocationRunOptions(runOptions))

	require.False(t, runOptions.StreamModeEnabled)
	require.False(t, runOptions.GraphEmitFinalModelResponses)
	require.True(t, agent.IsGraphCompletionEventDisabled(child))
	require.True(t, agent.IsGraphExecutorEventsDisabled(child))
	require.Equal(t, 7, agent.GetEventChannelBufferSize(child))
}

func TestTool_StreamableCall_DisableGraphCompletionEvent_PreservesPriorAssistantResultForVisibleStateOnlyCompletion(
	t *testing.T,
) {
	at := NewTool(
		&assistantThenVisibleStateOnlyCompletionAgent{name: "assistant-visible-agent"},
		WithStreamInner(true),
		WithHistoryScope(HistoryScopeParentBranch),
	)
	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	reader, err := at.StreamableCall(
		tool.WithFinalResultChunks(ctx),
		[]byte(`{"request":"ignored"}`),
	)
	require.NoError(t, err)
	defer reader.Close()

	var assistantEvents int
	var finalChunk finalChunkView
	var sawFinalChunk bool
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		if evt, ok := chunk.Content.(*event.Event); ok {
			if content, ok := assistantMessageContent(evt); ok {
				require.Equal(t, "wrapped-final", content)
				assistantEvents++
			}
			continue
		}
		finalChunk = requireFinalChunkView(t, chunk.Content)
		sawFinalChunk = true
	}

	require.Equal(t, 1, assistantEvents)
	require.True(t, sawFinalChunk)
	require.Equal(t, "wrapped-final", finalChunk.Result)
	require.Equal(t, []byte(`"resp-1"`), finalChunk.StateDelta[graph.StateKeyLastResponseID])
	require.Equal(t, []byte(`"child-state"`), finalChunk.StateDelta[graphStateKey])
}

func TestTool_StreamableCall_DisableGraphCompletionEvent_PreservesPriorAssistantResultForVisibleStateOnlyCompletionWithoutResponseID(
	t *testing.T,
) {
	at := NewTool(
		&assistantThenVisibleStateOnlyCompletionWithoutResponseIDAgent{
			name: "assistant-visible-agent",
		},
		WithStreamInner(true),
		WithHistoryScope(HistoryScopeParentBranch),
	)
	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	reader, err := at.StreamableCall(
		tool.WithFinalResultChunks(ctx),
		[]byte(`{"request":"ignored"}`),
	)
	require.NoError(t, err)
	defer reader.Close()

	var finalChunk finalChunkView
	var sawFinalChunk bool
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		if _, ok := chunk.Content.(*event.Event); ok {
			continue
		}
		finalChunk = requireFinalChunkView(t, chunk.Content)
		sawFinalChunk = true
	}

	require.True(t, sawFinalChunk)
	require.Equal(t, "wrapped-final", finalChunk.Result)
	require.Equal(t, []byte(`"child-state"`), finalChunk.StateDelta[graphStateKey])
}

func TestTool_StreamableCall_DisableGraphCompletionEvent_SuppressesStateOnlyCompletion(t *testing.T) {
	at := NewTool(
		&graphCompletionMockAgent{name: graphCompletionAgent, stateOnly: true},
		WithStreamInner(true),
		WithHistoryScope(HistoryScopeParentBranch),
	)
	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	reader, err := at.StreamableCall(
		tool.WithFinalResultChunks(ctx),
		[]byte(`{"request":"ignored"}`),
	)
	require.NoError(t, err)
	defer reader.Close()
	eventChunks := 0
	resultChunks := 0
	var finalChunk finalChunkView
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		if evt, ok := chunk.Content.(*event.Event); ok {
			eventChunks++
			require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
			continue
		}
		finalChunk = requireFinalChunkView(t, chunk.Content)
		resultChunks++
	}
	require.Zero(t, eventChunks)
	require.Equal(t, 1, resultChunks)
	require.Nil(t, finalChunk.Result)
	require.Equal(t, []byte(graphStateValue), finalChunk.StateDelta[graphStateKey])
}

func TestTool_StreamableCall_DisableGraphCompletionEvent_PrefersAfterCallbackCustomResponse(t *testing.T) {
	ga := newGraphAgentWithAfterCallback(
		t,
		graph.State{graph.StateKeyLastResponse: "child-final"},
		&model.Response{
			Object: "after.custom",
			Done:   true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("after callback"),
			}},
		},
	)
	at := NewTool(ga, WithStreamInner(true), WithHistoryScope(HistoryScopeParentBranch))
	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	reader, err := at.StreamableCall(
		tool.WithFinalResultChunks(ctx),
		[]byte(`{"request":"ignored"}`),
	)
	require.NoError(t, err)
	defer reader.Close()
	var sawAfterCallback bool
	var finalChunk finalChunkView
	var sawFinalChunk bool
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		if evt, ok := chunk.Content.(*event.Event); ok {
			require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
			if evt.Object == "after.custom" {
				sawAfterCallback = true
				require.Len(t, evt.Response.Choices, 1)
				require.Equal(t, "after callback", evt.Response.Choices[0].Message.Content)
			}
			continue
		}
		finalChunk = requireFinalChunkView(t, chunk.Content)
		sawFinalChunk = true
	}
	require.True(t, sawAfterCallback)
	require.True(t, sawFinalChunk)
	require.Equal(t, "after callback", finalChunk.Result)
	require.Equal(t, []byte(`"child-final"`), finalChunk.StateDelta[graph.StateKeyLastResponse])
}

func TestTool_StreamableCall_DisableGraphCompletionEvent_PrefersAfterCallbackCustomResponseForStateOnlyCompletion(t *testing.T) {
	ga := newGraphAgentWithAfterCallback(
		t,
		graph.State{graphStateKey: "child-state"},
		&model.Response{
			Object: "after.custom",
			Done:   true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("after callback"),
			}},
		},
	)
	at := NewTool(ga, WithStreamInner(true), WithHistoryScope(HistoryScopeParentBranch))
	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	reader, err := at.StreamableCall(
		tool.WithFinalResultChunks(ctx),
		[]byte(`{"request":"ignored"}`),
	)
	require.NoError(t, err)
	defer reader.Close()
	var finalChunk finalChunkView
	var sawFinalChunk bool
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		if _, ok := chunk.Content.(*event.Event); ok {
			continue
		}
		finalChunk = requireFinalChunkView(t, chunk.Content)
		sawFinalChunk = true
	}
	require.True(t, sawFinalChunk)
	require.Equal(t, "after callback", finalChunk.Result)
	require.Equal(t, []byte(`"child-state"`), finalChunk.StateDelta[graphStateKey])
}

func TestTool_StreamableCall_DisableGraphCompletionEvent_PrefersAfterCallbackCustomResponseForVisibleCompletionSnapshot(t *testing.T) {
	at := NewTool(
		&visibleCompletionThenAfterAgent{name: "visible-completion-agent"},
		WithStreamInner(true),
		WithHistoryScope(HistoryScopeParentBranch),
	)
	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	reader, err := at.StreamableCall(
		tool.WithFinalResultChunks(ctx),
		[]byte(`{"request":"ignored"}`),
	)
	require.NoError(t, err)
	defer reader.Close()

	var sawAfterCallback bool
	var finalChunk finalChunkView
	var sawFinalChunk bool
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		if evt, ok := chunk.Content.(*event.Event); ok {
			require.False(t, graph.IsVisibleGraphCompletionEvent(evt))
			if evt.Object == "after.custom" {
				sawAfterCallback = true
			}
			continue
		}
		finalChunk = requireFinalChunkView(t, chunk.Content)
		sawFinalChunk = true
	}

	require.True(t, sawAfterCallback)
	require.True(t, sawFinalChunk)
	require.Equal(t, "after callback", finalChunk.Result)
	require.Equal(t, []byte(`"child-final"`), finalChunk.StateDelta[graph.StateKeyLastResponse])
	require.True(t, sessionHasAssistantContent(sess, "after callback"))
	require.False(t, sessionHasAssistantContent(sess, "child-final"))
	stateValue, ok := sess.GetState(graph.StateKeyLastResponse)
	require.True(t, ok)
	require.Equal(t, []byte(`"child-final"`), stateValue)
}

func TestTool_StreamableCall_DisableGraphCompletionEvent_SuppressesVisibleCompletionBeforeError(
	t *testing.T,
) {
	at := NewTool(
		&visibleCompletionThenErrorAgent{name: "visible-completion-then-error-agent"},
		WithStreamInner(true),
		WithHistoryScope(HistoryScopeParentBranch),
	)
	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	reader, err := at.StreamableCall(ctx, []byte(`{"request":"ignored"}`))
	require.NoError(t, err)
	defer reader.Close()

	var sawError bool
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		if evt, ok := chunk.Content.(*event.Event); ok {
			require.False(t, graph.IsVisibleGraphCompletionEvent(evt))
			if evt.Object == model.ObjectTypeError {
				sawError = true
			}
			continue
		}
		require.Failf(t, "unexpected final result chunk", "%#v", chunk.Content)
	}

	require.True(t, sawError)
}

func TestTool_StreamableCall_DisableGraphCompletionEvent_DropsPendingFinalResultAfterCallbackError(t *testing.T) {
	ga := newGraphAgentWithAfterCallbackError(
		t,
		graph.State{
			graph.StateKeyLastResponse: "child-final",
			graphStateKey:              "child-state",
		},
		errors.New("after callback failed"),
	)
	at := NewTool(ga, WithStreamInner(true), WithHistoryScope(HistoryScopeParentBranch))
	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	reader, err := at.StreamableCall(ctx, []byte(`{"request":"ignored"}`))
	require.NoError(t, err)
	defer reader.Close()
	var sawError bool
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		if evt, ok := chunk.Content.(*event.Event); ok {
			if evt.Object == model.ObjectTypeError &&
				evt.Error != nil &&
				evt.Error.Message == "after callback failed" {
				sawError = true
			}
			continue
		}
		t.Fatalf("unexpected final result chunk after callback error: %#v", chunk.Content)
	}
	require.True(t, sawError)
	require.False(t, sessionHasAssistantContent(sess, "child-final"))
}

func TestTool_StreamInner_FlagFalse(t *testing.T) {
	a := &mockAgent{name: "agent-x", description: "d"}
	at := NewTool(a, WithStreamInner(false))
	if at.StreamInner() {
		t.Fatalf("expected StreamInner to be false")
	}
}

// errorMockAgent returns error from Run
type errorMockAgent struct{ name string }

func (m *errorMockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	return nil, fmt.Errorf("boom")
}
func (m *errorMockAgent) Tools() []tool.Tool              { return nil }
func (m *errorMockAgent) Info() agent.Info                { return agent.Info{Name: m.name, Description: "err"} }
func (m *errorMockAgent) SubAgents() []agent.Agent        { return nil }
func (m *errorMockAgent) FindSubAgent(string) agent.Agent { return nil }

func TestTool_Call_RunError(t *testing.T) {
	at := NewTool(&errorMockAgent{name: "err-agent"})
	_, err := at.Call(context.Background(), []byte(`{"request":"x"}`))
	if err == nil {
		t.Fatalf("expected error from Call when agent run fails")
	}
}

func TestTool_StreamableCall_RunErrorReturnsStreamError(t *testing.T) {
	at := NewTool(&errorMockAgent{name: "err-agent"}, WithStreamInner(true))
	r, err := at.StreamableCall(context.Background(), []byte(`{}`))
	require.NoError(t, err)
	defer r.Close()
	chunk, err := r.Recv()
	require.NoError(t, err)
	require.Equal(t, "agent tool run error: boom", chunk.Content)
	_, err = r.Recv()
	require.Equal(t, io.EOF, err)
}

func TestTool_StreamableCall_RunErrorWithToolCallIDReturnsPlainTextByDefault(t *testing.T) {
	at := NewTool(&errorMockAgent{name: "err-agent"}, WithStreamInner(true))
	ctx := context.WithValue(
		context.Background(),
		tool.ContextKeyToolCallID{},
		"call-1",
	)
	r, err := at.StreamableCall(ctx, []byte(`{}`))
	require.NoError(t, err)
	defer r.Close()
	chunk, err := r.Recv()
	require.NoError(t, err)
	require.Equal(t, "agent tool run error: boom", chunk.Content)
	_, err = r.Recv()
	require.Equal(t, io.EOF, err)
}

func TestTool_StreamableCall_RunErrorWithStructuredStreamErrorsReturnsErrorEvent(t *testing.T) {
	at := NewTool(&errorMockAgent{name: "err-agent"}, WithStreamInner(true))
	ctx := context.WithValue(
		tool.WithStructuredStreamErrors(context.Background()),
		tool.ContextKeyToolCallID{},
		"call-1",
	)
	r, err := at.StreamableCall(ctx, []byte(`{}`))
	require.NoError(t, err)
	defer r.Close()
	chunk, err := r.Recv()
	require.NoError(t, err)
	ev, ok := chunk.Content.(*event.Event)
	require.True(t, ok)
	require.Equal(t, model.ObjectTypeError, ev.Object)
	require.NotNil(t, ev.Error)
	require.Contains(t, ev.Error.Message, "agent tool run error")
	_, err = r.Recv()
	require.Equal(t, io.EOF, err)
}

// agentWithSchemaMock returns input/output schema maps in Info()
type agentWithSchemaMock struct{ name, desc string }

func (m *agentWithSchemaMock) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}
func (m *agentWithSchemaMock) Tools() []tool.Tool { return nil }
func (m *agentWithSchemaMock) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: m.desc,
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"request": map[string]any{"type": "string"}},
			"required":   []any{"request"},
		},
		OutputSchema: map[string]any{
			"type":        "string",
			"description": "out",
		},
	}
}
func (m *agentWithSchemaMock) SubAgents() []agent.Agent        { return nil }
func (m *agentWithSchemaMock) FindSubAgent(string) agent.Agent { return nil }

func TestNewTool_UsesAgentSchemas(t *testing.T) {
	at := NewTool(&agentWithSchemaMock{name: "s-agent", desc: "d"})
	decl := at.Declaration()
	if decl.InputSchema == nil || decl.InputSchema.Type != "object" {
		t.Fatalf("expected converted input schema, got: %#v", decl.InputSchema)
	}
	if decl.OutputSchema == nil || decl.OutputSchema.Type != "string" {
		t.Fatalf("expected converted output schema, got: %#v", decl.OutputSchema)
	}
}

func TestTool_SkipSummarization(t *testing.T) {
	a := &mockAgent{name: "test", description: "test"}

	// Test default (false)
	at1 := NewTool(a)
	if at1.SkipSummarization() {
		t.Errorf("Expected SkipSummarization to be false by default")
	}

	// Test with true
	at2 := NewTool(a, WithSkipSummarization(true))
	if !at2.SkipSummarization() {
		t.Errorf("Expected SkipSummarization to be true")
	}

	// Test with false explicitly
	at3 := NewTool(a, WithSkipSummarization(false))
	if at3.SkipSummarization() {
		t.Errorf("Expected SkipSummarization to be false")
	}
}

func TestTool_StructuredStreamErrors(t *testing.T) {
	a := &mockAgent{name: "test", description: "test"}
	at1 := NewTool(a)
	require.False(t, at1.StructuredStreamErrors())
	require.False(t, at1.TRPCAgentGoStructuredStreamErrorsOptIn())
	at2 := NewTool(a, WithStructuredStreamErrors(true))
	require.True(t, at2.StructuredStreamErrors())
	require.True(t, at2.TRPCAgentGoStructuredStreamErrorsOptIn())
}

// eventErrorMockAgent returns an event with error
type eventErrorMockAgent struct{ name string }

func (m *eventErrorMockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Error: &model.ResponseError{Message: "event error occurred"},
		},
	}
	close(ch)
	return ch, nil
}
func (m *eventErrorMockAgent) Tools() []tool.Tool              { return nil }
func (m *eventErrorMockAgent) Info() agent.Info                { return agent.Info{Name: m.name, Description: "err"} }
func (m *eventErrorMockAgent) SubAgents() []agent.Agent        { return nil }
func (m *eventErrorMockAgent) FindSubAgent(string) agent.Agent { return nil }

func TestTool_Call_EventError(t *testing.T) {
	at := NewTool(&eventErrorMockAgent{name: "err-event-agent"})
	_, err := at.Call(context.Background(), []byte(`{"request":"x"}`))
	if err == nil {
		t.Fatalf("expected error from Call when event contains error")
	}
	if !strings.Contains(err.Error(), "event error occurred") {
		t.Fatalf("expected error message to contain 'event error occurred', got: %v", err)
	}
}

func TestTool_Call_WithParentInvocation_EventError(t *testing.T) {
	at := NewTool(&eventErrorMockAgent{name: "err-event-agent"})

	sess := &session.Session{
		ID:     "s",
		UserID: "u",
	}
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	_, err := at.Call(ctx, []byte(`{"request":"x"}`))
	if err == nil {
		t.Fatalf("expected error from Call when event contains error")
	}
	if !strings.Contains(err.Error(), "event error occurred") {
		t.Fatalf("expected error message to contain 'event error occurred', got: %v", err)
	}
}

func TestTool_StreamableCall_WithParentInvocation_RunError(t *testing.T) {
	at := NewTool(&errorMockAgent{name: "err-agent"}, WithStreamInner(true))

	sess := &session.Session{}
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	r, err := at.StreamableCall(ctx, []byte(`{}`))
	require.NoError(t, err)
	defer r.Close()
	chunk, err := r.Recv()
	require.NoError(t, err)
	require.Equal(t, "agent tool run error: boom", chunk.Content)
	_, err = r.Recv()
	require.Equal(t, io.EOF, err)
}

func TestTool_StreamableCall_WithParentInvocation_FlushError(t *testing.T) {
	at := NewTool(&mockAgent{name: "test-agent", description: "desc"}, WithStreamInner(true))
	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationEventFilterKey("parent"),
	)
	flush.Attach(context.Background(), parent, make(chan *flush.FlushRequest))
	baseCtx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx := agent.NewInvocationContext(baseCtx, parent)

	r, err := at.StreamableCall(ctx, []byte(`{}`))
	require.NoError(t, err)
	defer r.Close()
	chunk, err := r.Recv()
	require.NoError(t, err)
	require.Contains(t, chunk.Content, "flush parent invocation session")
	_, err = r.Recv()
	require.Equal(t, io.EOF, err)
}

func TestTool_StreamableCall_EmptyMessage(t *testing.T) {
	sa := &streamingMockAgent{name: "stream-agent"}
	at := NewTool(sa, WithStreamInner(true))

	sess := &session.Session{}
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	// Call with empty message content
	r, err := at.StreamableCall(ctx, []byte(``))
	if err != nil {
		t.Fatalf("StreamableCall error: %v", err)
	}
	defer r.Close()

	// Should still receive events (3 from streaming mock)
	for i := 0; i < 3; i++ {
		if _, err := r.Recv(); err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
	}
}

func TestConvertMapToToolSchema(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected *tool.Schema
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: nil,
		},
		{
			name: "invalid JSON - channel type",
			input: map[string]any{
				"invalid": make(chan int), // channels cannot be marshaled to JSON
			},
			expected: nil,
		},
		{
			name: "valid schema",
			input: map[string]any{
				"type":        "object",
				"description": "test schema",
				"properties": map[string]any{
					"field1": map[string]any{"type": "string"},
				},
			},
			expected: &tool.Schema{
				Type:        "object",
				Description: "test schema",
				Properties: map[string]*tool.Schema{
					"field1": {Type: "string"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertMapToToolSchema(tt.input)
			if tt.expected == nil {
				if result != nil {
					t.Errorf("Expected nil result, got: %#v", result)
				}
			} else {
				if result == nil {
					t.Errorf("Expected non-nil result, got nil")
				} else if result.Type != tt.expected.Type || result.Description != tt.expected.Description {
					t.Errorf("Expected %+v, got %+v", tt.expected, result)
				}
			}
		})
	}
}

func TestTool_Call_WithParentInvocation_NoSession(t *testing.T) {
	a := &mockAgent{name: "test", description: "test"}
	at := NewTool(a)

	// Create parent invocation without session
	parent := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), parent)

	// Should fall back to isolated runner
	result, err := at.Call(ctx, []byte(`{"request":"test"}`))
	if err != nil {
		t.Fatalf("Call error: %v", err)
	}
	if result == nil {
		t.Fatalf("Expected non-nil result")
	}
}

func TestTool_callWithParentInvocation_NoSessionFallback(t *testing.T) {
	at := NewTool(&mockAgent{name: "test", description: "test"})
	parent := agent.NewInvocation()

	res, err := at.callWithParentInvocation(context.Background(), parent, model.NewUserMessage("hi"))
	require.NoError(t, err)
	require.Equal(t, "Hello from mock agent!", res)
}

func TestTool_callWithParentInvocation_PreservesRunStructuredOutput(t *testing.T) {
	modelImpl := &structuredOutputCaptureModel{name: "capture-model"}
	child := llmagent.New("structured-output-child", llmagent.WithModel(modelImpl))
	at := NewTool(child)
	runOpts := agent.RunOptions{}
	agent.WithStructuredOutputJSONSchema(
		"tool_output",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{"type": "string"},
			},
		},
		true,
		"Return one object.",
	)(&runOpts)
	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationRunOptions(runOpts),
		agent.WithInvocationStructuredOutput(runOpts.StructuredOutput),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	res, err := at.callWithParentInvocation(
		context.Background(),
		parent,
		model.NewUserMessage("hi"),
	)
	require.NoError(t, err)
	require.NotEmpty(t, res)
	seen, schemaName := modelImpl.Snapshot()
	require.True(t, seen)
	require.Equal(t, "tool_output", schemaName)
}

func TestTool_Call_WithParentInvocation_FlushError(t *testing.T) {
	at := NewTool(&mockAgent{name: "test-agent", description: "desc"})

	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationEventFilterKey("parent"),
	)
	flush.Attach(context.Background(), parent, make(chan *flush.FlushRequest))

	baseCtx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx := agent.NewInvocationContext(baseCtx, parent)

	result, err := at.Call(ctx, []byte(`{"request":"hello"}`))
	require.Error(t, err)
	require.Empty(t, result)
	require.Contains(t, err.Error(), "flush parent invocation session")
}

func TestTool_Call_WithParentInvocation_RunError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationEventFilterKey("parent"),
	)
	flushCh := make(chan *flush.FlushRequest, 1)
	flush.Attach(ctx, parent, flushCh)

	flushed := make(chan struct{}, 1)
	go func() {
		select {
		case req := <-flushCh:
			if req != nil && req.ACK != nil {
				close(req.ACK)
			}
			flushed <- struct{}{}
		case <-ctx.Done():
		}
	}()

	at := NewTool(&errorMockAgent{name: "err-agent"})
	_, err := at.Call(agent.NewInvocationContext(ctx, parent), []byte(`{"request":"x"}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to run agent")

	select {
	case <-flushed:
	default:
		t.Fatalf("expected flush to be triggered")
	}
}

func TestTool_Call_WithParentInvocation_FlushesAndCompletes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	flushCh := make(chan *flush.FlushRequest, 1)
	flush.Attach(ctx, parent, flushCh)

	flushed := make(chan struct{}, 1)
	go func() {
		select {
		case req := <-flushCh:
			if req != nil && req.ACK != nil {
				close(req.ACK)
			}
			flushed <- struct{}{}
		case <-ctx.Done():
		}
	}()

	a := &filterKeyAgent{name: "child-agent"}
	at := NewTool(a, WithHistoryScope(HistoryScopeParentBranch))
	res, err := at.Call(agent.NewInvocationContext(ctx, parent), []byte(`{"request":"hi"}`))
	require.NoError(t, err)
	resStr, ok := res.(string)
	require.True(t, ok)
	require.True(t, strings.HasPrefix(resStr, "parent-agent/"+a.name+"-"))
	require.Equal(t, a.seen, resStr)

	select {
	case <-flushed:
	default:
		t.Fatalf("expected flush to be triggered")
	}
}

// nilEventMockAgent sends nil event in stream
type nilEventMockAgent struct{ name string }

func (m *nilEventMockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		ch <- nil // Send nil event
		ch <- &event.Event{Response: &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("ok")}}}}
		close(ch)
	}()
	return ch, nil
}
func (m *nilEventMockAgent) Tools() []tool.Tool              { return nil }
func (m *nilEventMockAgent) Info() agent.Info                { return agent.Info{Name: m.name, Description: "nil"} }
func (m *nilEventMockAgent) SubAgents() []agent.Agent        { return nil }
func (m *nilEventMockAgent) FindSubAgent(string) agent.Agent { return nil }

func TestTool_StreamableCall_NilEvent(t *testing.T) {
	at := NewTool(&nilEventMockAgent{name: "nil-agent"}, WithStreamInner(true))

	r, err := at.StreamableCall(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("StreamableCall error: %v", err)
	}
	defer r.Close()

	// Should receive the non-nil event (nil event is skipped in fallback path)
	ch, err := r.Recv()
	if err != nil {
		t.Fatalf("unexpected stream read error: %v", err)
	}
	if ch.Content == nil {
		t.Fatalf("expected non-nil content")
	}
}
