//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
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
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ExampleGraphAgent_Run demonstrates running a GraphAgent.
func ExampleGraphAgent_Run() {
	// Create a simple processing graph
	schema := graph.NewStateSchema().
		AddField("input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("processed", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			input := state["input"].(string)
			return graph.State{"processed": "Processed: " + input}, nil
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

	// Run the agent
	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("process this")),
	)

	events, err := graphAgent.Run(context.Background(), invocation)
	if err != nil {
		panic(err)
	}

	// Process events
	eventCount := 0
	for range events {
		eventCount++
	}

	fmt.Printf("Processed %d events\n", eventCount)

	// Output:
	// Processed 9 events
}
