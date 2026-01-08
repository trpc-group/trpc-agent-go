//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the join edge (wait-all fan-in) behavior using
// graph.StateGraph.AddJoinEdge.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"reflect"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

const (
	nodeStart = "start"
	nodeA     = "a"
	nodeB     = "b"
	nodeJoin  = "join"

	stateKeyOrder = "order"
)

var (
	sleepA = flag.Duration(
		"sleep-a",
		120*time.Millisecond,
		"Simulated work duration for node a",
	)
	sleepB = flag.Duration(
		"sleep-b",
		40*time.Millisecond,
		"Simulated work duration for node b",
	)
)

func main() {
	flag.Parse()

	g, err := buildGraph(*sleepA, *sleepB)
	if err != nil {
		log.Fatalf("build graph failed: %v", err)
	}
	exec, err := graph.NewExecutor(g)
	if err != nil {
		log.Fatalf("create executor failed: %v", err)
	}

	inv := &agent.Invocation{InvocationID: "join-edge-demo"}
	ch, err := exec.Execute(context.Background(), graph.State{}, inv)
	if err != nil {
		log.Fatalf("execute failed: %v", err)
	}

	order, err := waitForFinalOrder(ch)
	if err != nil {
		log.Fatalf("read final order failed: %v", err)
	}
	fmt.Printf("Final execution order: %v\n", order)
}

func buildGraph(sleepA, sleepB time.Duration) (*graph.Graph, error) {
	schema := graph.NewStateSchema().AddField(stateKeyOrder, graph.StateField{
		Type:    reflect.TypeOf([]string{}),
		Reducer: graph.StringSliceReducer,
		Default: func() any { return []string{} },
	})

	sg := graph.NewStateGraph(schema)
	sg.AddNode(nodeStart, func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{stateKeyOrder: []string{nodeStart}}, nil
	})
	sg.AddNode(nodeA, func(ctx context.Context, state graph.State) (any, error) {
		time.Sleep(sleepA)
		return graph.State{stateKeyOrder: []string{nodeA}}, nil
	})
	sg.AddNode(nodeB, func(ctx context.Context, state graph.State) (any, error) {
		time.Sleep(sleepB)
		return graph.State{stateKeyOrder: []string{nodeB}}, nil
	})
	sg.AddNode(nodeJoin, func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{stateKeyOrder: []string{nodeJoin}}, nil
	})

	sg.SetEntryPoint(nodeStart)
	sg.AddEdge(nodeStart, nodeA)
	sg.AddEdge(nodeStart, nodeB)
	sg.AddJoinEdge([]string{nodeA, nodeB}, nodeJoin)
	sg.SetFinishPoint(nodeJoin)

	return sg.Compile()
}

func waitForFinalOrder(
	ch <-chan *event.Event,
) ([]string, error) {
	var doneEvent *event.Event
	for evt := range ch {
		if evt.Done {
			doneEvent = evt
			break
		}
	}
	if doneEvent == nil || doneEvent.StateDelta == nil {
		return nil, fmt.Errorf("missing done event or state delta")
	}

	raw, ok := doneEvent.StateDelta[stateKeyOrder]
	if !ok {
		return nil, fmt.Errorf("state delta missing %q", stateKeyOrder)
	}

	var order []string
	if err := json.Unmarshal(raw, &order); err != nil {
		return nil, fmt.Errorf("decode state %q: %w", stateKeyOrder, err)
	}
	return order, nil
}
