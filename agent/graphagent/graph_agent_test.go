//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package graphagent

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockTool is a simple mock tool for testing.
type mockTool struct {
	name        string
	description string
}

func (mt *mockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        mt.name,
		Description: mt.description,
		InputSchema: &tool.Schema{
			Type:       "object",
			Properties: map[string]*tool.Schema{},
		},
	}
}

func (mt *mockTool) Call(ctx context.Context,
	jsonArgs []byte) (interface{}, error) {
	return "mock tool result", nil
}

// testAgent is a simple test agent for unit testing.
type testAgent struct {
	name        string
	description string
	tools       []tool.Tool
}

func (ta *testAgent) Run(ctx context.Context,
	invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, 1)

	go func() {
		defer close(eventChan)

		response := &model.Response{
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Test response from " + ta.name,
					},
				},
			},
			Done: true,
		}

		eventChan <- event.NewResponseEvent(invocation.InvocationID, ta.name,
			response)
	}()

	return eventChan, nil
}

func (ta *testAgent) Tools() []tool.Tool {
	return ta.tools
}

func (ta *testAgent) Info() agent.Info {
	return agent.Info{
		Name:        ta.name,
		Description: ta.description,
	}
}

func (ta *testAgent) SubAgents() []agent.Agent {
	return nil
}

func (ta *testAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func TestNew(t *testing.T) {
	// Create a simple graph.
	g, err := graph.NewBuilder().
		AddStartNode("start", "Start").
		AddEndNode("end", "End").
		AddEdge("start", "end").
		Build()
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

func TestNewWithOptions(t *testing.T) {
	// Create a simple graph.
	g, err := graph.NewBuilder().
		AddStartNode("start", "Start").
		AddEndNode("end", "End").
		AddEdge("start", "end").
		Build()
	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Create test sub-agent.
	subAgent := &testAgent{
		name:        "sub-agent",
		description: "Test sub-agent",
	}

	// Create test tools.
	testTool := &mockTool{
		name:        "test-tool",
		description: "Test tool",
	}

	// Test creating graph agent with options.
	graphAgent, err := New("test-agent", g,
		WithDescription("Test graph agent"),
		WithTools([]tool.Tool{testTool}),
		WithSubAgents([]agent.Agent{subAgent}),
		WithInitialState(graph.State{"test": true}),
	)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Test description.
	info := graphAgent.Info()
	if info.Description != "Test graph agent" {
		t.Errorf("Expected description 'Test graph agent', got '%s'",
			info.Description)
	}

	// Test tools.
	tools := graphAgent.Tools()
	if len(tools) != 1 {
		t.Errorf("Expected 1 tool, got %d", len(tools))
	}
	if tools[0].Declaration().Name != "test-tool" {
		t.Errorf("Expected tool name 'test-tool', got '%s'",
			tools[0].Declaration().Name)
	}

	// Test sub-agents.
	subAgents := graphAgent.SubAgents()
	if len(subAgents) != 1 {
		t.Errorf("Expected 1 sub-agent, got %d", len(subAgents))
	}
	if subAgents[0].Info().Name != "sub-agent" {
		t.Errorf("Expected sub-agent name 'sub-agent', got '%s'",
			subAgents[0].Info().Name)
	}
}

func TestNewWithInvalidGraph(t *testing.T) {
	// Create invalid graph.
	invalidGraph := graph.New()

	_, err := New("test-agent", invalidGraph)
	if err == nil {
		t.Error("Expected error for invalid graph")
	}
}

func TestFindSubAgent(t *testing.T) {
	// Create a simple graph.
	g, err := graph.NewBuilder().
		AddStartNode("start", "Start").
		AddEndNode("end", "End").
		AddEdge("start", "end").
		Build()
	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Create test sub-agents.
	subAgent1 := &testAgent{name: "agent1", description: "Agent 1"}
	subAgent2 := &testAgent{name: "agent2", description: "Agent 2"}

	graphAgent, err := New("test-agent", g,
		WithSubAgents([]agent.Agent{subAgent1, subAgent2}),
	)
	if err != nil {
		t.Fatalf("Failed to create graph agent: %v", err)
	}

	// Test finding existing sub-agent.
	found := graphAgent.FindSubAgent("agent1")
	if found == nil {
		t.Error("Expected to find 'agent1'")
	}
	if found != nil && found.Info().Name != "agent1" {
		t.Errorf("Expected found agent name 'agent1', got '%s'",
			found.Info().Name)
	}

	// Test finding non-existent sub-agent.
	notFound := graphAgent.FindSubAgent("nonexistent")
	if notFound != nil {
		t.Error("Expected not to find 'nonexistent' agent")
	}
}

func TestAgentResolver(t *testing.T) {
	// Create test agent.
	testAgent := &testAgent{name: "test-agent", description: "Test"}

	// Create resolver.
	resolver := &agentResolver{
		subAgents: map[string]agent.Agent{
			"test-agent": testAgent,
		},
	}

	// Test resolving existing agent.
	executor, err := resolver.ResolveAgent("test-agent")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if executor == nil {
		t.Fatal("Expected non-nil executor")
	}

	// Test resolving non-existent agent.
	_, err = resolver.ResolveAgent("nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent agent")
	}
}

func TestAgentExecutorWrapper(t *testing.T) {
	// Create test agent.
	testAgent := &testAgent{name: "test-agent", description: "Test"}

	// Create wrapper.
	wrapper := &agentExecutor{agent: testAgent, channelBufferSize: 256}

	// Test execution with message in state.
	state := graph.State{
		"message": model.Message{
			Role:    model.RoleUser,
			Content: "test message",
		},
	}

	newState, events, err := wrapper.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if newState == nil {
		t.Fatal("Expected non-nil new state")
	}
	if events == nil {
		t.Fatal("Expected non-nil events channel")
	}

	// Collect events.
	var eventCount int
	for range events {
		eventCount++
	}
	if eventCount == 0 {
		t.Error("Expected at least one event")
	}
}

func TestAgentExecutorWrapperWithInput(t *testing.T) {
	// Create test agent.
	testAgent := &testAgent{name: "test-agent", description: "Test"}

	// Create wrapper.
	wrapper := &agentExecutor{agent: testAgent, channelBufferSize: 256}

	// Test execution with input in state.
	state := graph.State{
		"input": "test input",
	}

	newState, events, err := wrapper.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if newState == nil {
		t.Fatal("Expected non-nil new state")
	}

	// Collect events.
	var eventCount int
	for range events {
		eventCount++
	}
	if eventCount == 0 {
		t.Error("Expected at least one event")
	}
}

func TestAgentExecutorWrapperDefault(t *testing.T) {
	// Create test agent.
	testAgent := &testAgent{name: "test-agent", description: "Test"}

	// Create wrapper.
	wrapper := &agentExecutor{agent: testAgent, channelBufferSize: 256}

	// Test execution with empty state.
	state := graph.State{}

	newState, events, err := wrapper.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if newState == nil {
		t.Fatal("Expected non-nil new state")
	}

	// Collect events.
	var eventCount int
	for range events {
		eventCount++
	}
	if eventCount == 0 {
		t.Error("Expected at least one event")
	}
}
