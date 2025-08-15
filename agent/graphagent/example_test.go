//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graphagent_test

import (
	"context"
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// ExampleNew demonstrates basic usage of GraphAgent.
func ExampleNew() {
	// Create a simple graph using the new StateGraph API
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
		panic(err)
	}

	// Create GraphAgent
	graphAgent, err := graphagent.New("example-agent", g,
		graphagent.WithDescription("Example graph agent"),
		graphagent.WithInitialState(graph.State{"input": "hello world"}))

	if err != nil {
		panic(err)
	}

	fmt.Printf("Created agent: %s\n", graphAgent.Info().Name)
	fmt.Printf("Description: %s\n", graphAgent.Info().Description)

	// Output:
	// Created agent: example-agent
	// Description: Example graph agent
}

// ExampleGraphAgent_Run demonstrates running a GraphAgent.
func ExampleGraphAgent_Run() {
	// Create a simple processing graph
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
		panic(err)
	}

	// Create GraphAgent
	graphAgent, err := graphagent.New("echo-agent", g,
		graphagent.WithInitialState(graph.State{"message": "Hello, Graph!"}))

	if err != nil {
		panic(err)
	}

	// Create invocation
	invocation := &agent.Invocation{
		Agent:        graphAgent,
		AgentName:    "echo-agent",
		InvocationID: "example-invocation",
	}

	// Run the agent
	events, err := graphAgent.Run(context.Background(), invocation)
	if err != nil {
		panic(err)
	}

	// Count events
	eventCount := 0
	for range events {
		eventCount++
	}

	fmt.Printf("Agent executed successfully with %d events\n", eventCount)

	// Output:
	// Agent executed successfully with 2 events
}
