//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to use EventEmitter to emit custom events from NodeFunc.
// This example shows:
// - Emitting custom events with payload
// - Emitting progress events during long-running operations
// - Emitting streaming text events
// - AGUI Server integration to receive these events as AG-UI protocol events
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"reflect"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
)

const (
	nodeStart    = "start"
	nodeProcess  = "process"
	nodeAnalyze  = "analyze"
	nodeComplete = "complete"
)

var (
	address = flag.String("address", "127.0.0.1:8080", "Listen address")
	path    = flag.String("path", "/agui", "HTTP path")
)

func main() {
	flag.Parse()

	// Build the graph with event emitter demonstration
	g, err := buildGraph()
	if err != nil {
		log.Fatalf("Failed to build graph: %v", err)
	}

	// Create GraphAgent
	ga, err := graphagent.New(
		"event-emitter-demo",
		g,
		graphagent.WithDescription("Demonstration of Node EventEmitter functionality"),
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		log.Fatalf("Failed to create graph agent: %v", err)
	}

	// Create runner
	r := runner.NewRunner(ga.Info().Name, ga)
	defer r.Close()

	// Create AG-UI server
	server, err := agui.New(r, agui.WithPath(*path))
	if err != nil {
		log.Fatalf("Failed to create AG-UI server: %v", err)
	}

	log.Infof("🚀 Starting AG-UI server with EventEmitter demo at http://%s%s", *address, *path)
	log.Info("📝 This example demonstrates:")
	log.Info("   - Custom events with payload (workflow.started, workflow.completed)")
	log.Info("   - Progress events (node.progress)")
	log.Info("   - Streaming text events (node.text)")
	log.Info("")
	log.Info("💡 Run the client example to test:")
	log.Info("   go run ./client/event_emitter")

	if err = http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("Server stopped with error: %v", err)
	}
}

// buildGraph creates a graph that demonstrates EventEmitter usage in NodeFunc.
func buildGraph() (*graph.Graph, error) {
	schema := graph.NewStateSchema().
		AddField("input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("result", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("status", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	sg := graph.NewStateGraph(schema)

	// Node 1: Start - emit custom event with initial status
	sg.AddNode(nodeStart, startNode)

	// Node 2: Process - emit progress events during processing
	sg.AddNode(nodeProcess, processNode)

	// Node 3: Analyze - emit streaming text events
	sg.AddNode(nodeAnalyze, analyzeNode)

	// Node 4: Complete - emit final custom event
	sg.AddNode(nodeComplete, completeNode)

	// Set up edges
	sg.SetEntryPoint(nodeStart)
	sg.AddEdge(nodeStart, nodeProcess)
	sg.AddEdge(nodeProcess, nodeAnalyze)
	sg.AddEdge(nodeAnalyze, nodeComplete)
	sg.SetFinishPoint(nodeComplete)

	return sg.Compile()
}

// startNode demonstrates emitting a custom event with payload.
func startNode(ctx context.Context, state graph.State) (any, error) {
	log.Info("[startNode] Starting workflow...")

	// Get EventEmitter from state
	emitter := graph.GetEventEmitter(state)

	// Get user input from messages
	var userInput string
	if messages, ok := state[graph.StateKeyMessages].([]model.Message); ok && len(messages) > 0 {
		for _, msg := range messages {
			if msg.Role == model.RoleUser {
				userInput = msg.Content
			}
		}
	}
	if userInput == "" {
		userInput = "default input"
	}

	// Emit custom event: workflow started
	if err := emitter.EmitCustom("workflow.started", map[string]any{
		"timestamp":  time.Now().Format(time.DateTime),
		"user_input": userInput,
		"version":    "1.0.0",
	}); err != nil {
		// Note: Do not return error when event sending fails,
		// to prevent client disconnection from affecting Agent workflow
		log.Warnf("Failed to emit custom event: %+v", err)
	}

	log.Info("[startNode] Emitted 'workflow.started' custom event")

	return graph.State{
		"input":  userInput,
		"status": "started",
	}, nil
}

// processNode demonstrates emitting progress events during a long-running operation.
func processNode(ctx context.Context, state graph.State) (any, error) {
	log.Info("[processNode] Processing data with progress reporting...")

	emitter := graph.GetEventEmitter(state)

	// Simulate a long-running process with progress updates
	totalSteps := 5
	for i := 1; i <= totalSteps; i++ {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Calculate progress percentage
		progress := float64(i) / float64(totalSteps) * 100

		// Emit progress event
		if err := emitter.EmitProgress(progress,
			fmt.Sprintf("Processing step %d of %d", i, totalSteps)); err != nil {
			log.Warnf("Failed to emit progress event: %v", err)
		}

		log.Infof("[processNode] Emitted progress: %.0f%% - Step %d/%d", progress, i, totalSteps)

		// Simulate work
		time.Sleep(time.Second)
	}

	return graph.State{"status": "processed"}, nil
}

// analyzeNode demonstrates emitting streaming text events.
func analyzeNode(ctx context.Context, state graph.State) (any, error) {
	log.Info("[analyzeNode] Analyzing results with streaming output...")

	emitter := graph.GetEventEmitter(state)

	input, _ := state["input"].(string)

	// Simulate streaming analysis output
	analysisLines := []string{
		"📊 Starting analysis...\n",
		fmt.Sprintf("📝 Input received: \"%s\"\n", input),
		"🔍 Analyzing patterns...\n",
		"✅ Pattern analysis complete.\n",
		"📈 Generating insights...\n",
		"💡 Key findings:\n",
		"   - Data processed successfully\n",
		"   - No anomalies detected\n",
		"   - Performance metrics within expected range\n",
	}

	for _, line := range analysisLines {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Emit streaming text event
		if err := emitter.EmitText(line); err != nil {
			log.Warnf("Failed to emit text event: %v", err)
		}

		log.Infof("[analyzeNode] Emitted text: %s", line[:len(line)-1]) // trim newline for log

		// Simulate streaming delay
		time.Sleep(time.Second)
	}

	return graph.State{
		"status": "analyzed",
		"result": "Analysis completed successfully with no issues found.",
	}, nil
}

// completeNode demonstrates emitting a final custom event with results.
func completeNode(ctx context.Context, state graph.State) (any, error) {
	log.Info("[completeNode] Completing workflow...")

	emitter := graph.GetEventEmitter(state)

	result, _ := state["result"].(string)

	// Emit custom event: workflow completed
	err := emitter.EmitCustom("workflow.completed", map[string]any{
		"timestamp":     time.Now().Format(time.RFC3339),
		"result":        result,
		"duration_ms":   2500, // Simulated duration
		"success":       true,
		"nodes_visited": []string{nodeStart, nodeProcess, nodeAnalyze, nodeComplete},
	})
	if err != nil {
		log.Warnf("Failed to emit custom event: %v", err)
	}

	log.Info("[completeNode] Emitted 'workflow.completed' custom event")

	// Also emit a final progress event to indicate 100% complete
	emitter.EmitProgress(100, "Workflow completed successfully!")

	return graph.State{
		"status": "completed",
	}, nil
}

// Ensure we implement agent.Agent interface requirements
var _ agent.Agent = (*graphagent.GraphAgent)(nil)
