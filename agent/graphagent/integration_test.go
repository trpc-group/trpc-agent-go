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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// TestGraphAgentIntegration tests the full integration of graph and graph agent.
func TestGraphAgentIntegration(t *testing.T) {
	// Create a simple test agent
	testAgent := &integrationTestAgent{name: "test-agent"}

	// Create a simple workflow graph
	workflowGraph, err := graph.NewBuilder().
		AddStartNode("start", "Start").
		AddFunctionNode("prepare", "Prepare", "Prepares data", func(ctx context.Context, state graph.State) (graph.State, error) {
			state["prepared"] = true
			state["input"] = "prepared data"
			return state, nil
		}).
		AddAgentNode("process", "Process", "Processes with agent", "test-agent").
		AddFunctionNode("finalize", "Finalize", "Finalizes result", func(ctx context.Context, state graph.State) (graph.State, error) {
			state["finalized"] = true
			return state, nil
		}).
		AddEndNode("end", "End").
		AddEdge("start", "prepare").
		AddEdge("prepare", "process").
		AddEdge("process", "finalize").
		AddEdge("finalize", "end").
		Build()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Create graph agent
	graphAgent, err := New("integration-test", workflowGraph,
		WithDescription("Integration test agent"),
		WithSubAgents([]agent.Agent{testAgent}),
		WithInitialState(graph.State{"test": true}),
	)

	if err != nil {
		t.Fatalf("Failed to create graph agent: %v", err)
	}

	// Create invocation
	invocation := &agent.Invocation{
		Agent:        graphAgent,
		AgentName:    graphAgent.Info().Name,
		InvocationID: "integration-test-123",
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "Test message",
		},
	}

	// Run the graph agent
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events, err := graphAgent.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("Failed to run graph agent: %v", err)
	}

	// Collect events
	var eventCount int
	var completionFound bool

	for event := range events {
		eventCount++
		t.Logf("Event %d: Author=%s, Done=%v", eventCount, event.Author, event.Response.Done)
		
		if event.Response != nil && event.Response.Done {
			completionFound = true
			break
		}
	}

	if !completionFound {
		t.Error("Expected to find completion event")
	}

	if eventCount == 0 {
		t.Error("Expected to receive events")
	}
}

// integrationTestAgent is a simple test agent implementation.
type integrationTestAgent struct {
	name string
}

func (ta *integrationTestAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, 1)
	
	go func() {
		defer close(eventChan)
		
		// Simulate some processing
		time.Sleep(10 * time.Millisecond)
		
		response := &model.Response{
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Test agent processed: " + invocation.Message.Content,
					},
				},
			},
			Done: true,
		}
		
		eventChan <- event.NewResponseEvent(invocation.InvocationID, ta.name, response)
	}()
	
	return eventChan, nil
}

func (ta *integrationTestAgent) Tools() []tool.Tool {
	return nil
}

func (ta *integrationTestAgent) Info() agent.Info {
	return agent.Info{
		Name:        ta.name,
		Description: "Test agent for integration testing",
	}
}

func (ta *integrationTestAgent) SubAgents() []agent.Agent {
	return nil
}

func (ta *integrationTestAgent) FindSubAgent(name string) agent.Agent {
	return nil
}