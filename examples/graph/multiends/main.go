//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates per-node named ends (multi-ends) in the graph package.
// A decision node returns symbolic branches (e.g., "approve", "reject") which
// are resolved via node-local ends to concrete destinations.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"reflect"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	nodeStart    = "start"
	nodeDecide   = "decide"
	nodeApproved = "approved"
	nodeRejected = "rejected"
	nodeFinal    = "final"

	keyDecision = "decision"
	keyPath     = "path"
	keyResult   = "result"
)

var (
	choice = flag.String("choice", "approve", "Branch choice: approve|reject")
)

func main() {
	flag.Parse()
	fmt.Println("🚀 Multi-Ends Branching Example")

	// Build graph
	g, err := buildGraph()
	if err != nil {
		log.Fatalf("failed to build graph: %v", err)
	}

	// Create a GraphAgent
	ga, err := graphagent.New(
		"multiends-demo",
		g,
		graphagent.WithDescription("Demonstration of per-node named ends (multi-ends)"),
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		log.Fatalf("failed to create graph agent: %v", err)
	}

	// Create session service and runner
	sess := inmemory.NewSessionService()
	r := runner.NewRunner("multiends-app", ga, runner.WithSessionService(sess))

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

	// Run a single turn using the provided choice as user input
	if err := runOnce(r, *choice); err != nil {
		log.Fatalf("run failed: %v", err)
	}
}

func buildGraph() (*graph.Graph, error) {
	// Define a simple schema
	schema := graph.NewStateSchema().
		AddField(keyDecision, graph.StateField{Type: reflect.TypeOf(""), Reducer: graph.DefaultReducer}).
		AddField(keyPath, graph.StateField{Type: reflect.TypeOf(""), Reducer: graph.DefaultReducer}).
		AddField(keyResult, graph.StateField{Type: reflect.TypeOf(""), Reducer: graph.DefaultReducer})

	sg := graph.NewStateGraph(schema)

	// Add nodes
	sg.AddNode(nodeStart, startNode)
	sg.AddNode(nodeDecide, decideNode, graph.WithEndsMap(map[string]string{
		"approve": nodeApproved,
		"reject":  nodeRejected,
	}))
	sg.AddNode(nodeApproved, approvedNode)
	sg.AddNode(nodeRejected, rejectedNode)
	sg.AddNode(nodeFinal, finalNode)

	// Topology
	sg.SetEntryPoint(nodeStart).SetFinishPoint(nodeFinal)
	sg.AddEdge(nodeStart, nodeDecide)
	sg.AddEdge(nodeApproved, nodeFinal)
	sg.AddEdge(nodeRejected, nodeFinal)

	return sg.Compile()
}

// startNode reads StateKeyUserInput (provided by the runner) and writes it into
// a decision key for the decide node to consume.
func startNode(_ context.Context, state graph.State) (any, error) {
	var val string
	if v, ok := state[graph.StateKeyUserInput].(string); ok && v != "" {
		val = v
	}
	if val == "" {
		val = "approve" // default choice
	}
	return graph.State{keyDecision: val}, nil
}

// decideNode returns a symbolic branch using Command.GoTo.
func decideNode(_ context.Context, state graph.State) (any, error) {
	v, _ := state[keyDecision].(string)
	switch v {
	case "approve":
		return &graph.Command{GoTo: "approve"}, nil
	case "reject":
		return &graph.Command{GoTo: "reject"}, nil
	default:
		// Unknown choice: default to reject
		return &graph.Command{GoTo: "reject"}, nil
	}
}

func approvedNode(_ context.Context, state graph.State) (any, error) {
	return graph.State{keyPath: "approved"}, nil
}

func rejectedNode(_ context.Context, state graph.State) (any, error) {
	return graph.State{keyPath: "rejected"}, nil
}

func finalNode(_ context.Context, state graph.State) (any, error) {
	path, _ := state[keyPath].(string)
	return graph.State{keyResult: fmt.Sprintf("completed via %s", path)}, nil
}

func runOnce(r runner.Runner, userInput string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	evs, err := r.Run(ctx, "user", fmt.Sprintf("sess-%d", time.Now().Unix()), model.NewUserMessage(userInput))
	if err != nil {
		return err
	}

	var final map[string]any
	for ev := range evs {
		if ev.Error != nil {
			// Print error and continue to drain the channel
			fmt.Printf("❌ Error: %s\n", ev.Error.Message)
			continue
		}
		if ev.Done && ev.StateDelta != nil {
			// Extract final state snapshot from the terminal event
			out := make(map[string]any)
			for k, vb := range ev.StateDelta {
				switch k {
				case graph.MetadataKeyNode, graph.MetadataKeyPregel, graph.MetadataKeyChannel, graph.MetadataKeyState, graph.MetadataKeyCompletion:
					continue
				}
				var v any
				if err := json.Unmarshal(vb, &v); err == nil {
					out[k] = v
				}
			}
			final = out
		}
	}

	if final != nil {
		fmt.Printf("✅ Finished. path=%v, result=%v\n", final[keyPath], final[keyResult])
	} else {
		fmt.Println("⚠️  Finished with no final state")
	}
	return nil
}
