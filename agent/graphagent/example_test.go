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

package graphagent_test

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockAgent is a simple mock agent for testing.
type mockAgent struct {
	name        string
	description string
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, 1)
	
	// Create a simple response event
	response := &model.Response{
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Mock agent response: " + invocation.Message.Content,
				},
			},
		},
		Done: true,
	}
	
	event := event.NewResponseEvent(invocation.InvocationID, m.name, response)
	eventChan <- event
	close(eventChan)
	
	return eventChan, nil
}

func (m *mockAgent) Tools() []tool.Tool {
	return nil
}

func (m *mockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: m.description,
	}
}

func (m *mockAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *mockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func TestGraphAgent(t *testing.T) {
	// Create mock sub-agents
	analyzerAgent := &mockAgent{
		name:        "analyzer",
		description: "Analyzes input data",
	}
	
	processorAgent := &mockAgent{
		name:        "processor", 
		description: "Processes analyzed data",
	}

	// Create a graph that uses these agents
	g, err := graph.NewBuilder().
		AddStartNode("start", "Start Processing").
		AddAgentNode("analyze", "Analyze Data", "Analyzes the input using analyzer agent", "analyzer").
		AddFunctionNode("transform", "Transform", "Transforms data between agents", func(ctx context.Context, state graph.State) (graph.State, error) {
			// Transform the output from analyzer for processor
			if output, ok := state["last_agent_output"].(string); ok {
				state["input"] = "Transformed: " + output
			}
			return state, nil
		}).
		AddAgentNode("process", "Process Data", "Processes the analyzed data using processor agent", "processor").
		AddEndNode("end", "End Processing").
		AddEdge("start", "analyze").
		AddEdge("analyze", "transform").
		AddEdge("transform", "process").
		AddEdge("process", "end").
		Build()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Create graph agent with sub-agents
	graphAgent, err := graphagent.New("workflow-agent", g,
		graphagent.WithDescription("A workflow agent that coordinates multiple sub-agents"),
		graphagent.WithSubAgents([]agent.Agent{analyzerAgent, processorAgent}),
		graphagent.WithInitialState(graph.State{
			"workflow_id": "test-workflow-123",
		}),
	)

	if err != nil {
		t.Fatalf("Failed to create graph agent: %v", err)
	}

	// Test the agent info
	info := graphAgent.Info()
	if info.Name != "workflow-agent" {
		t.Errorf("Expected agent name 'workflow-agent', got '%s'", info.Name)
	}

	// Test sub-agents
	subAgents := graphAgent.SubAgents()
	if len(subAgents) != 2 {
		t.Errorf("Expected 2 sub-agents, got %d", len(subAgents))
	}

	// Test finding sub-agent
	foundAgent := graphAgent.FindSubAgent("analyzer")
	if foundAgent == nil {
		t.Error("Expected to find 'analyzer' sub-agent")
	}
	if foundAgent != nil && foundAgent.Info().Name != "analyzer" {
		t.Errorf("Expected found agent name 'analyzer', got '%s'", foundAgent.Info().Name)
	}
}

func ExampleGraphAgent() {
	// Create a simple processing agent
	processingAgent := &mockAgent{
		name:        "processor",
		description: "Processes data",
	}

	// Create a simple workflow graph
	workflowGraph, _ := graph.NewBuilder().
		AddStartNode("start", "Start").
		AddFunctionNode("prepare", "Prepare Data", "Prepares data for processing", func(ctx context.Context, state graph.State) (graph.State, error) {
			state["prepared"] = true
			return state, nil
		}).
		AddAgentNode("process", "Process", "Process the data", "processor").
		AddEndNode("end", "End").
		AddEdge("start", "prepare").
		AddEdge("prepare", "process").
		AddEdge("process", "end").
		Build()

	// Create the graph agent
	workflowAgent, _ := graphagent.New("workflow", workflowGraph,
		graphagent.WithDescription("A simple workflow agent"),
		graphagent.WithSubAgents([]agent.Agent{processingAgent}),
	)

	// The workflow agent can now be used like any other agent
	_ = workflowAgent.Info().Name // "workflow"
}