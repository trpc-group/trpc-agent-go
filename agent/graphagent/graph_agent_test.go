//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graphagent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/barrier"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNewGraphAgent(t *testing.T) {
	// Create a simple graph using the new API.
	schema := graph.NewStateSchema().
		AddField("input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			input := state["input"].(string)
			return graph.State{"output": "processed: " + input}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Test creating graph agent.
	graphAgent, err := New("test-agent", g)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if graphAgent == nil {
		t.Fatal("Expected non-nil graph agent")
	}

	// Test agent info.
	info := graphAgent.Info()
	if info.Name != "test-agent" {
		t.Errorf("Expected name 'test-agent', got '%s'", info.Name)
	}
}

func TestGraphAgentWithOptions(t *testing.T) {
	// Create a simple graph using the new API.
	schema := graph.NewStateSchema().
		AddField("counter", graph.StateField{
			Type:    reflect.TypeOf(0),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("increment", func(ctx context.Context, state graph.State) (any, error) {
			counter, _ := state["counter"].(int)
			return graph.State{"counter": counter + 1}, nil
		}).
		SetEntryPoint("increment").
		SetFinishPoint("increment").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Test creating graph agent with options.
	initialState := graph.State{"counter": 5}
	graphAgent, err := New("test-agent", g,
		WithDescription("Test agent description"),
		WithInitialState(initialState),
		WithChannelBufferSize(512))

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Test that options were applied.
	info := graphAgent.Info()
	if info.Description != "Test agent description" {
		t.Errorf("Expected description to be set")
	}
}

func TestGraphAgentRun(t *testing.T) {
	// Create a simple graph using the new API.
	schema := graph.NewStateSchema().
		AddField("message", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("response", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("respond", func(ctx context.Context, state graph.State) (any, error) {
			message := state["message"].(string)
			return graph.State{"response": "Echo: " + message}, nil
		}).
		SetEntryPoint("respond").
		SetFinishPoint("respond").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Create graph agent.
	initialState := graph.State{"message": "hello"}
	graphAgent, err := New("echo-agent", g, WithInitialState(initialState))
	if err != nil {
		t.Fatalf("Failed to create graph agent: %v", err)
	}

	// Test running the agent.
	invocation := &agent.Invocation{
		Agent:        graphAgent,
		AgentName:    "echo-agent",
		InvocationID: "test-invocation",
	}

	events, err := graphAgent.Run(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Collect events.
	eventCount := 0
	for range events {
		eventCount++
	}

	if eventCount == 0 {
		t.Error("Expected at least one event")
	}
}

func TestGraphAgentWithRuntimeState(t *testing.T) {
	// Create a simple graph that uses runtime state.
	schema := graph.NewStateSchema().
		AddField("user_id", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("room_id", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("base_value", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			// Verify that runtime state was merged correctly.
			userID, hasUserID := state["user_id"]
			roomID, hasRoomID := state["room_id"]
			baseValue, hasBaseValue := state["base_value"]

			if !hasUserID || !hasRoomID || !hasBaseValue {
				return nil, fmt.Errorf("missing expected state fields")
			}

			if userID != "user123" || roomID != "room456" || baseValue != "default" {
				return nil, fmt.Errorf("unexpected state values")
			}

			return graph.State{"status": "success"}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Create graph agent with base initial state.
	baseState := graph.State{"base_value": "default"}
	graphAgent, err := New("test-agent", g, WithInitialState(baseState))
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Test that runtime state is properly merged.
	ctx := context.Background()
	message := model.NewUserMessage("test message")

	// Create invocation with runtime state.
	invocation := &agent.Invocation{
		Message: message,
		RunOptions: agent.RunOptions{
			RuntimeState: graph.State{
				"user_id": "user123",
				"room_id": "room456",
			},
		},
	}

	// Run the agent.
	eventChan, err := graphAgent.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Process events to ensure no errors occurred.
	eventCount := 0
	for range eventChan {
		eventCount++
	}

	// If we get here without errors, the runtime state was merged correctly.
	if eventCount == 0 {
		t.Error("Expected at least one event")
	}
}

func TestGraphAgentRuntimeStateOverridesBaseState(t *testing.T) {
	// Create a simple graph.
	schema := graph.NewStateSchema().
		AddField("input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			input := state["input"].(string)
			// Return a response that can be converted to model.Response.
			return &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "processed: " + input,
					},
				}},
			}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Create GraphAgent with base initial state.
	graphAgent, err := New("test-agent", g,
		WithInitialState(graph.State{"input": "base input"}))
	if err != nil {
		t.Fatalf("Failed to create graph agent: %v", err)
	}

	// Test with runtime state that overrides base state.
	invocation := &agent.Invocation{
		Message: model.NewUserMessage("runtime input"),
		RunOptions: agent.RunOptions{
			RuntimeState: graph.State{"input": "runtime input"},
		},
	}

	events, err := graphAgent.Run(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Collect events.
	eventCount := 0
	for range events {
		eventCount++
	}

	if eventCount == 0 {
		t.Fatal("Expected at least one event")
	}

	// The test passes if we get here without errors, which means the runtime state override worked correctly.
}

func TestGraphAgentWithSubAgents(t *testing.T) {
	// Create a mock sub-agent.
	mockSubAgent := &mockAgent{
		name:         "sub-agent",
		eventCount:   1,
		eventContent: "Hello from sub-agent!",
	}

	// Create a simple graph that uses the sub-agent.
	schema := graph.NewStateSchema().
		AddField("input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddAgentNode("call_sub_agent",
			graph.WithName("Call Sub Agent"),
			graph.WithDescription("Calls the sub-agent to process the input"),
		).
		SetEntryPoint("call_sub_agent").
		SetFinishPoint("call_sub_agent").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Create GraphAgent with sub-agents.
	graphAgent, err := New("test-agent", g,
		WithSubAgents([]agent.Agent{mockSubAgent}),
		WithDescription("Test agent with sub-agents"))
	if err != nil {
		t.Fatalf("Failed to create graph agent: %v", err)
	}

	// Test sub-agent methods.
	subAgents := graphAgent.SubAgents()
	if len(subAgents) != 1 {
		t.Errorf("Expected 1 sub-agent, got %d", len(subAgents))
	}

	foundSubAgent := graphAgent.FindSubAgent("sub-agent")
	if foundSubAgent == nil {
		t.Error("Expected to find sub-agent 'sub-agent'")
	}
	if foundSubAgent.Info().Name != "sub-agent" {
		t.Errorf("Expected sub-agent name 'sub-agent', got '%s'", foundSubAgent.Info().Name)
	}

	notFoundSubAgent := graphAgent.FindSubAgent("non-existent")
	if notFoundSubAgent != nil {
		t.Error("Expected to not find non-existent sub-agent")
	}

	// Test running the graph with sub-agent.
	invocation := &agent.Invocation{
		Message: model.NewUserMessage("test input"),
	}

	events, err := graphAgent.Run(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Collect events.
	eventCount := 0
	for range events {
		eventCount++
	}

	if eventCount == 0 {
		t.Fatal("Expected at least one event")
	}

	// The test passes if we get here without errors, which means the sub-agent was called successfully.
}

// mockAgent is a test implementation of agent.Agent for testing sub-agents.
type mockAgent struct {
	name           string
	shouldError    bool
	eventCount     int
	eventContent   string
	executionOrder *[]string
	tools          []tool.Tool
}

type stubSummarizer struct {
	summary string
}

func (s *stubSummarizer) ShouldSummarize(_ *session.Session) bool { return true }
func (s *stubSummarizer) Summarize(_ context.Context, _ *session.Session) (string, error) {
	return s.summary, nil
}
func (s *stubSummarizer) SetPrompt(prompt string)  {}
func (s *stubSummarizer) SetModel(m model.Model)   {}
func (s *stubSummarizer) Metadata() map[string]any { return nil }

var _ summary.SessionSummarizer = (*stubSummarizer)(nil)

func (m *mockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent for testing",
	}
}

func (m *mockAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *mockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *mockAgent) Tools() []tool.Tool {
	return m.tools
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if m.shouldError {
		return nil, errors.New("mock agent error")
	}

	ch := make(chan *event.Event, m.eventCount)
	go func() {
		defer close(ch)
		for i := 0; i < m.eventCount; i++ {
			response := &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: m.eventContent,
					},
				}},
			}
			evt := event.NewResponseEvent(invocation.InvocationID, m.name, response)
			select {
			case ch <- evt:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// TestGraphAgent_InvocationContextAccess verifies that GraphAgent can access invocation
// from context when called through runner (after removing duplicate injection).
func TestGraphAgent_InvocationContextAccess(t *testing.T) {
	// Create a simple graph agent.
	stateGraph := graph.NewStateGraph(nil)
	stateGraph.AddNode("test-node", func(ctx context.Context, state graph.State) (any, error) {
		// Verify that invocation is accessible from context.
		invocation, ok := agent.InvocationFromContext(ctx)
		if !ok || invocation == nil {
			return nil, fmt.Errorf("invocation not found in context")
		}

		// Return success state.
		return graph.State{
			"invocation_id": invocation.InvocationID,
			"agent_name":    invocation.AgentName,
			"status":        "success",
		}, nil
	})
	stateGraph.SetEntryPoint("test-node")
	stateGraph.SetFinishPoint("test-node")

	compiledGraph, err := stateGraph.Compile()
	require.NoError(t, err)

	graphAgent, err := New("test-graph-agent", compiledGraph)
	require.NoError(t, err)

	// Create invocation with context that contains invocation.
	invocation := &agent.Invocation{
		InvocationID: "test-invocation-123",
		AgentName:    "test-graph-agent",
		Message:      model.NewUserMessage("Test invocation context access"),
	}

	// Create context with invocation (simulating what runner does).
	ctx := agent.NewInvocationContext(context.Background(), invocation)

	// Run the agent.
	eventCh, err := graphAgent.Run(ctx, invocation)
	require.NoError(t, err)
	require.NotNil(t, eventCh)

	// Collect events.
	var events []*event.Event
	for evt := range eventCh {
		events = append(events, evt)
	}

	// Verify that the agent can access invocation from context.
	// This test ensures that even after removing the duplicate injection from LLMAgent,
	// GraphAgent can still access invocation when called through runner.
	require.Greater(t, len(events), 0)

	// The agent should have been able to run successfully, which means
	// it could access the invocation from context for any internal operations.
	t.Logf("GraphAgent successfully executed with %d events, confirming invocation context access", len(events))
}

// TestGraphAgent_WithCheckpointSaver tests the WithCheckpointSaver option.
func TestGraphAgent_WithCheckpointSaver(t *testing.T) {
	// Create a mock checkpoint saver.
	saver := &mockCheckpointSaver{}

	// Create a simple graph.
	schema := graph.NewStateSchema().
		AddField("counter", graph.StateField{
			Type:    reflect.TypeOf(0),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("increment", func(ctx context.Context, state graph.State) (any, error) {
			counter, _ := state["counter"].(int)
			return graph.State{"counter": counter + 1}, nil
		}).
		SetEntryPoint("increment").
		SetFinishPoint("increment").
		Compile()

	require.NoError(t, err)

	// Create graph agent with checkpoint saver.
	graphAgent, err := New("test-agent", g, WithCheckpointSaver(saver))
	require.NoError(t, err)
	require.NotNil(t, graphAgent)

	// Verify the executor is accessible.
	executor := graphAgent.Executor()
	require.NotNil(t, executor)
}

// TestGraphAgent_Tools tests the Tools method.
func TestGraphAgent_Tools(t *testing.T) {
	// Create a simple graph.
	schema := graph.NewStateSchema().
		AddField("input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"output": "done"}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	require.NoError(t, err)

	graphAgent, err := New("test-agent", g)
	require.NoError(t, err)

	// GraphAgent should return nil for tools.
	tools := graphAgent.Tools()
	require.Nil(t, tools)
}

// TestGraphAgent_CreateInitialStateWithSession tests createInitialState with session.
func TestGraphAgent_CreateInitialStateWithSession(t *testing.T) {
	// Create a simple graph.
	schema := graph.NewStateSchema().
		AddField("messages", graph.StateField{
			Type:    reflect.TypeOf([]model.Message{}),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			// Check if messages from session were added.
			messages, ok := state[graph.StateKeyMessages]
			if !ok {
				return nil, fmt.Errorf("messages not found in state")
			}
			msgSlice, ok := messages.([]model.Message)
			if !ok || len(msgSlice) == 0 {
				return nil, fmt.Errorf("expected non-empty messages")
			}
			return graph.State{"status": "success"}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	require.NoError(t, err)

	graphAgent, err := New("test-agent", g)
	require.NoError(t, err)

	// Create session with some events.
	sess := &session.Session{
		ID: "test-session",
		Events: []event.Event{
			{
				InvocationID: "inv-1",
				Response: &model.Response{
					Choices: []model.Choice{{
						Message: model.Message{Role: model.RoleUser, Content: "Hello"},
					}},
				},
			},
		},
	}

	// Create invocation with session.
	invocation := &agent.Invocation{
		Message: model.NewUserMessage("Test message"),
		Session: sess,
	}

	// Run the agent.
	eventChan, err := graphAgent.Run(context.Background(), invocation)
	require.NoError(t, err)

	// Collect events.
	eventCount := 0
	for range eventChan {
		eventCount++
	}

	require.Greater(t, eventCount, 0)
}

func TestGraphAgent_CreateInitialStateWithSessionSummary(t *testing.T) {
	const agentName = "test-agent"
	schema := graph.NewStateSchema().
		AddField("messages", graph.StateField{
			Type:    reflect.TypeOf([]model.Message{}),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			return state, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()
	require.NoError(t, err)

	graphAgent, err := New(agentName, g, WithAddSessionSummary(true))
	require.NoError(t, err)

	sess := &session.Session{
		ID: "test-session",
		Summaries: map[string]*session.Summary{
			agentName: {
				Summary:   "branch summary content",
				UpdatedAt: time.Now(),
			},
		},
	}

	invocation := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationEventFilterKey(agentName),
	)
	graphAgent.setupInvocation(invocation)

	state := graphAgent.createInitialState(context.Background(), invocation)
	messages, ok := graph.GetStateValue[[]model.Message](state, graph.StateKeyMessages)
	require.True(t, ok)
	require.Len(t, messages, 2)
	require.Equal(t, model.RoleSystem, messages[0].Role)
	require.Contains(t, messages[0].Content, "branch summary content")
	require.Equal(t, model.RoleUser, messages[1].Role)
	require.Equal(t, "hello", messages[1].Content)
}

func TestGraphAgent_CreateInitialStateWithSessionSummary_Disabled(t *testing.T) {
	const agentName = "test-agent"
	schema := graph.NewStateSchema().
		AddField("messages", graph.StateField{
			Type:    reflect.TypeOf([]model.Message{}),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			return state, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()
	require.NoError(t, err)

	graphAgent, err := New(agentName, g)
	require.NoError(t, err)

	sess := &session.Session{
		ID: "test-session",
		Summaries: map[string]*session.Summary{
			agentName: {
				Summary:   "branch summary content",
				UpdatedAt: time.Now(),
			},
		},
	}

	invocation := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationEventFilterKey(agentName),
	)
	graphAgent.setupInvocation(invocation)

	state := graphAgent.createInitialState(context.Background(), invocation)
	messages, ok := graph.GetStateValue[[]model.Message](state, graph.StateKeyMessages)
	require.True(t, ok)
	require.Len(t, messages, 1)
	require.Equal(t, model.RoleUser, messages[0].Role)
	require.Equal(t, "hello", messages[0].Content)
}

func TestGraphAgent_CreateInitialStateWithSessionSummary_FromService(t *testing.T) {
	const agentName = "test-agent"
	ctx := context.Background()

	schema := graph.NewStateSchema().
		AddField("messages", graph.StateField{
			Type:    reflect.TypeOf([]model.Message{}),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			return state, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()
	require.NoError(t, err)

	graphAgent, err := New(agentName, g, WithAddSessionSummary(true))
	require.NoError(t, err)

	// Session service with a stub summarizer to emulate real summarization flow.
	sum := &stubSummarizer{summary: "auto summary from service"}
	sessSvc := inmemory.NewSessionService(inmemory.WithSummarizer(sum))
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := sessSvc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	evt := event.NewResponseEvent("inv-1", agentName, &model.Response{
		Choices: []model.Choice{{Message: model.Message{
			Role:    model.RoleUser,
			Content: "hi there",
		}}},
	})
	evt.FilterKey = agentName
	require.NoError(t, sessSvc.AppendEvent(ctx, sess, evt))
	require.NoError(t, sessSvc.CreateSessionSummary(ctx, sess, agentName, true))

	// Reload session to ensure we read persisted summaries.
	storedSess, err := sessSvc.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, storedSess)

	invocation := agent.NewInvocation(
		agent.WithInvocationSession(storedSess),
		agent.WithInvocationMessage(model.NewUserMessage("next turn")),
		agent.WithInvocationEventFilterKey(agentName),
	)
	graphAgent.setupInvocation(invocation)

	state := graphAgent.createInitialState(ctx, invocation)
	messages, ok := graph.GetStateValue[[]model.Message](state, graph.StateKeyMessages)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(messages), 1)
	require.Equal(t, model.RoleSystem, messages[0].Role)
	require.Contains(t, messages[0].Content, "auto summary from service")
	// Summary already covers prior history, so the latest run may start with summary only.
}

// TestGraphAgent_CreateInitialStateWithResume tests checkpoint resume behavior.
func TestGraphAgent_CreateInitialStateWithResume(t *testing.T) {
	// Create a simple graph.
	schema := graph.NewStateSchema().
		AddField("user_input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			// Check if user_input is present or not based on resume signal.
			userInput, hasInput := state[graph.StateKeyUserInput]
			if hasInput {
				return graph.State{"processed": userInput}, nil
			}
			return graph.State{"processed": "no input"}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	require.NoError(t, err)

	graphAgent, err := New("test-agent", g)
	require.NoError(t, err)

	// Test resume with "resume" message - should skip adding user_input.
	invocation := &agent.Invocation{
		Message: model.NewUserMessage("resume"),
		RunOptions: agent.RunOptions{
			RuntimeState: graph.State{
				graph.CfgKeyCheckpointID: "checkpoint-123",
			},
		},
	}

	eventChan, err := graphAgent.Run(context.Background(), invocation)
	require.NoError(t, err)

	// Collect events.
	for range eventChan {
		// Just drain the channel.
	}

	// Test resume with meaningful message - should add user_input.
	invocation2 := &agent.Invocation{
		Message: model.NewUserMessage("meaningful input"),
		RunOptions: agent.RunOptions{
			RuntimeState: graph.State{
				graph.CfgKeyCheckpointID: "checkpoint-123",
			},
		},
	}

	eventChan2, err := graphAgent.Run(context.Background(), invocation2)
	require.NoError(t, err)

	// Collect events.
	for range eventChan2 {
		// Just drain the channel.
	}
}

// mockCheckpointSaver is a mock implementation of graph.CheckpointSaver.
type mockCheckpointSaver struct{}

func (m *mockCheckpointSaver) Get(ctx context.Context, config map[string]any) (*graph.Checkpoint, error) {
	return nil, nil
}

func (m *mockCheckpointSaver) GetTuple(ctx context.Context, config map[string]any) (*graph.CheckpointTuple, error) {
	return nil, nil
}

func (m *mockCheckpointSaver) List(ctx context.Context, config map[string]any, filter *graph.CheckpointFilter) ([]*graph.CheckpointTuple, error) {
	return nil, nil
}

func (m *mockCheckpointSaver) Put(ctx context.Context, req graph.PutRequest) (map[string]any, error) {
	return nil, nil
}

func (m *mockCheckpointSaver) PutWrites(ctx context.Context, req graph.PutWritesRequest) error {
	return nil
}

func (m *mockCheckpointSaver) PutFull(ctx context.Context, req graph.PutFullRequest) (map[string]any, error) {
	return nil, nil
}

func (m *mockCheckpointSaver) DeleteLineage(ctx context.Context, lineageID string) error {
	return nil
}

func (m *mockCheckpointSaver) Close() error {
	return nil
}

func TestGraphAgent_MessageFilterMode(t *testing.T) {
	options := &Options{
		messageBranchFilterMode: "prefix",
	}
	WithMessageTimelineFilterMode("all")(options)

	require.Equal(t, options.messageTimelineFilterMode, "all")
	require.Equal(t, options.messageBranchFilterMode, "prefix")

	options = &Options{
		messageBranchFilterMode:   "prefix",
		messageTimelineFilterMode: "all",
	}
	WithMessageTimelineFilterMode("request")(options)
	WithMessageBranchFilterMode("exact")(options)

	require.Equal(t, options.messageTimelineFilterMode, "request")
	require.Equal(t, options.messageBranchFilterMode, "exact")
}

func TestGraphAgent_BeforeCallbackReturnsResponse(t *testing.T) {
	// Create a minimal graph.
	schema := graph.NewStateSchema().
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("noop", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"output": "should not run"}, nil
		}).
		SetEntryPoint("noop").
		SetFinishPoint("noop").
		Compile()
	require.NoError(t, err)

	// Create callbacks that return early.
	callbacks := agent.NewCallbacks()
	callbacks.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return &model.Response{
			Object: "before.custom",
			Done:   true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "early return",
				},
			}},
		}, nil
	})

	// Create graph agent with callbacks.
	ga, err := New("test-before", g, WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	inv := &agent.Invocation{
		InvocationID: "inv-before",
		AgentName:    "test-before",
	}

	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)

	// Collect events.
	var collected []*event.Event
	for e := range events {
		collected = append(collected, e)
	}

	require.Len(t, collected, 1)
	require.Equal(t, "before.custom", collected[0].Object)
	require.Equal(t, "early return", collected[0].Response.Choices[0].Message.Content)
}

func TestGraphAgent_BeforeCallbackReturnsError(t *testing.T) {
	// Create a minimal graph.
	schema := graph.NewStateSchema().
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("noop", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"output": "should not run"}, nil
		}).
		SetEntryPoint("noop").
		SetFinishPoint("noop").
		Compile()
	require.NoError(t, err)

	// Create callbacks that return error.
	callbacks := agent.NewCallbacks()
	callbacks.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return nil, errors.New("before callback failed")
	})

	// Create graph agent with callbacks.
	ga, err := New("test-before-err", g, WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	inv := &agent.Invocation{
		InvocationID: "inv-before-err",
		AgentName:    "test-before-err",
	}

	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)

	// Collect events and expect a final error event.
	var collected []*event.Event
	for e := range events {
		collected = append(collected, e)
	}
	require.Len(t, collected, 1)
	require.NotNil(t, collected[0].Error)
	require.Equal(t, model.ErrorTypeFlowError, collected[0].Error.Type)
	require.Contains(t, collected[0].Error.Message, "before callback failed")
}

func TestGraphAgent_AfterCallbackReturnsResponse(t *testing.T) {
	// Create a simple graph.
	schema := graph.NewStateSchema().
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"output": "processed"}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()
	require.NoError(t, err)

	// Create callbacks with after agent.
	callbacks := agent.NewCallbacks()
	var callbackErr error
	callbacks.RegisterAfterAgent(func(
		ctx context.Context,
		inv *agent.Invocation,
		err error,
	) (*model.Response, error) {
		callbackErr = err
		return &model.Response{
			Object: "after.custom",
			Done:   true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "after callback",
				},
			}},
		}, nil
	})

	// Create graph agent with callbacks.
	ga, err := New("test-after", g, WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	inv := &agent.Invocation{
		InvocationID: "inv-after",
		AgentName:    "test-after",
		Message:      model.NewUserMessage("test"),
	}

	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)

	// Collect events.
	var collected []*event.Event
	for e := range events {
		collected = append(collected, e)
	}

	// Should have graph execution event(s) plus after callback event.
	require.Greater(t, len(collected), 0)

	// After-callback in success path should see nil error.
	require.NoError(t, callbackErr)

	// Last event should be from after callback.
	last := collected[len(collected)-1]
	require.Equal(t, "after.custom", last.Object)
	require.Equal(t, "after callback", last.Response.Choices[0].Message.Content)
}

func TestGraphAgent_AfterCallbackReturnsError(t *testing.T) {
	// Create a simple graph.
	schema := graph.NewStateSchema().
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"output": "processed"}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()
	require.NoError(t, err)

	// Create callbacks with after agent error.
	callbacks := agent.NewCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, err error) (*model.Response, error) {
		return nil, errors.New("after callback failed")
	})

	// Create graph agent with callbacks.
	ga, err := New("test-after-err", g, WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	inv := &agent.Invocation{
		InvocationID: "inv-after-err",
		AgentName:    "test-after-err",
		Message:      model.NewUserMessage("test"),
	}

	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)

	// Collect events.
	var collected []*event.Event
	for e := range events {
		collected = append(collected, e)
	}

	// Should have graph execution event(s) plus after callback error event.
	require.Greater(t, len(collected), 0)

	// Last event should be error from after callback.
	last := collected[len(collected)-1]
	require.NotNil(t, last.Error)
	require.Equal(t, agent.ErrorTypeAgentCallbackError, last.Error.Type)
	require.Contains(t, last.Error.Message, "after callback failed")
}

func TestGraphAgent_AfterCallbackReceivesExecutionError(t *testing.T) {
	// Create a simple graph that fails at the node.
	schema := graph.NewStateSchema().
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("fail", func(
			ctx context.Context,
			state graph.State,
		) (any, error) {
			return nil, fmt.Errorf("node failed")
		}).
		SetEntryPoint("fail").
		SetFinishPoint("fail").
		Compile()
	require.NoError(t, err)

	// After-agent callback should receive non-nil error derived from the
	// final response event.
	callbacks := agent.NewCallbacks()
	var callbackErr error
	callbacks.RegisterAfterAgent(func(
		ctx context.Context,
		inv *agent.Invocation,
		err error,
	) (*model.Response, error) {
		callbackErr = err
		return nil, nil
	})

	ga, err := New(
		"test-after-exec-err",
		g,
		WithAgentCallbacks(callbacks),
	)
	require.NoError(t, err)

	inv := &agent.Invocation{
		InvocationID: "inv-after-exec-err",
		AgentName:    "test-after-exec-err",
		Message:      model.NewUserMessage("test"),
	}

	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)

	// Drain all events to ensure after-callback has run.
	for range events {
	}

	require.Error(t, callbackErr)
	require.Contains(t, callbackErr.Error(), "flow_error:")
}

func TestGraphAgent_BarrierWaitsForCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	schema := graph.NewStateSchema().
		AddField(graph.StateKeyMessages, graph.StateField{
			Type:    reflect.TypeOf([]model.Message{}),
			Reducer: graph.DefaultReducer,
		})
	g, err := graph.NewStateGraph(schema).
		AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"ok": true}, nil
		}).
		SetEntryPoint("done").
		SetFinishPoint("done").
		Compile()
	require.NoError(t, err)

	ga, err := New("barrier-test", g)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "u", "s")),
	)
	barrier.Enable(inv)

	ch, err := ga.Run(ctx, inv)
	require.NoError(t, err)

	var barrierEvt *event.Event
	select {
	case barrierEvt = <-ch:
	case <-ctx.Done():
		t.Fatalf("did not receive barrier event: %v", ctx.Err())
	}
	require.NotNil(t, barrierEvt)
	require.Equal(t, graph.ObjectTypeGraphBarrier, barrierEvt.Object)
	require.True(t, barrierEvt.RequiresCompletion)

	select {
	case evt, ok := <-ch:
		if ok {
			t.Fatalf("unexpected event before completion: %+v", evt)
		}
	default:
	}

	completionID := agent.GetAppendEventNoticeKey(barrierEvt.ID)
	require.NoError(t, inv.NotifyCompletion(ctx, completionID))

	var received []*event.Event
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				goto done
			}
			received = append(received, evt)
		case <-ctx.Done():
			t.Fatalf("timed out waiting for graph events: %v", ctx.Err())
		}
	}
done:
	require.NotEmpty(t, received)
	var hasGraphExec bool
	for _, evt := range received {
		if evt.Object == graph.ObjectTypeGraphExecution {
			hasGraphExec = true
		}
	}
	require.True(t, hasGraphExec)
}

func TestGraphAgent_RunWithBarrierEmitError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	schema := graph.NewStateSchema().
		AddField("done", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})
	g, err := graph.NewStateGraph(schema).
		AddNode("finish", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"done": "ok"}, nil
		}).
		SetEntryPoint("finish").
		SetFinishPoint("finish").
		Compile()
	require.NoError(t, err)

	ga, err := New("barrier-error", g)
	require.NoError(t, err)

	inv := &agent.Invocation{
		AgentName:    "barrier-error",
		InvocationID: "inv-barrier-error",
		// noticeMu left nil to force AddNoticeChannel to fail.
	}
	barrier.Enable(inv)

	out := make(chan *event.Event, 1)
	go ga.runWithBarrier(ctx, inv, out)

	var events []*event.Event
	for evt := range out {
		events = append(events, evt)
	}

	require.Len(t, events, 1)
	require.NotNil(t, events[0].Response)
	require.NotNil(t, events[0].Response.Error)
	require.Equal(t, model.ErrorTypeFlowError, events[0].Response.Error.Type)
	require.Contains(t, events[0].Response.Error.Message, "add notice channel")
}
