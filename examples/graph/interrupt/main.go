//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates interrupt and resume functionality using the graph package.
// It shows how to create a graph that can be interrupted and resumed from specific nodes.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
)

const (
	defaultThreadPrefix = "interrupt-demo"
	defaultNamespace    = ""
	defaultMode         = "run" // Modes: run, interrupt, resume, demo.
	stateKeyCounter     = "counter"
	stateKeyMsgs        = "messages"
	stateKeyUserInput   = "user_input"
	stateKeyApproved    = "approved"
)

var (
	modeFlag = flag.String("mode", defaultMode,
		"Mode: run|interrupt|resume|demo")
	threadFlag = flag.String("thread", "",
		"Thread ID for checkpointing (default: auto)")
	userInputFlag = flag.String("input", "",
		"User input for resume mode")
)

func main() {
	flag.Parse()

	threadID := *threadFlag
	if threadID == "" {
		threadID = fmt.Sprintf("%s-%d", defaultThreadPrefix, time.Now().Unix())
	}

	fmt.Printf("🔄 Interrupt & Resume Demo\n")
	fmt.Printf("Thread: %s\n", threadID)
	fmt.Println(strings.Repeat("=", 50))

	ctx := context.Background()

	// Prepare graph and executor with checkpointing enabled.
	g, err := buildInterruptGraph()
	if err != nil {
		log.Fatalf("failed to build graph: %v", err)
	}

	saver := inmemory.NewSaver()
	exec, err := graph.NewExecutor(g, graph.WithCheckpointSaver(saver))
	if err != nil {
		log.Fatalf("failed to create executor: %v", err)
	}
	manager := graph.NewCheckpointManager(saver)

	switch strings.ToLower(strings.TrimSpace(*modeFlag)) {
	case "run":
		if err := runNormalExecution(ctx, exec, threadID); err != nil {
			log.Fatalf("run failed: %v", err)
		}
	case "interrupt":
		if err := runWithInterrupt(ctx, exec, threadID); err != nil {
			log.Fatalf("interrupt failed: %v", err)
		}
	case "resume":
		if *userInputFlag == "" {
			log.Fatalf("user input required for resume mode")
		}
		if err := resumeFromInterrupt(ctx, exec, threadID, *userInputFlag); err != nil {
			log.Fatalf("resume failed: %v", err)
		}
	case "demo":
		if err := demoInterruptResume(ctx, exec, manager, threadID); err != nil {
			log.Fatalf("demo failed: %v", err)
		}
	default:
		log.Fatalf("unknown mode: %s", *modeFlag)
	}
}

func buildInterruptGraph() (*graph.Graph, error) {
	// Define schema.
	schema := graph.NewStateSchema()
	schema.AddField(stateKeyCounter, graph.StateField{
		Type:    reflect.TypeOf(0),
		Reducer: graph.DefaultReducer,
		Default: func() any { return 0 },
	})
	schema.AddField(stateKeyMsgs, graph.StateField{
		Type:    reflect.TypeOf([]string{}),
		Reducer: graph.StringSliceReducer,
		Default: func() any { return []string{} },
	})
	schema.AddField(stateKeyUserInput, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
		Default: func() any { return "" },
	})
	schema.AddField(stateKeyApproved, graph.StateField{
		Type:    reflect.TypeOf(false),
		Reducer: graph.DefaultReducer,
		Default: func() any { return false },
	})

	// Build a graph with interrupt capability.
	b := graph.NewStateGraph(schema)

	// Node 1: Increment counter
	b.AddNode("increment", func(ctx context.Context, s graph.State) (any, error) {
		v := getInt(s, stateKeyCounter)
		return graph.State{
			stateKeyCounter: v + 1,
			stateKeyMsgs: append(getStrs(s, stateKeyMsgs),
				fmt.Sprintf("increment -> %d", v+1)),
		}, nil
	})

	// Node 2: Request user approval (interrupt point)
	b.AddNode("request_approval", func(ctx context.Context, s graph.State) (any, error) {
		// Use the new Interrupt helper for cleaner interrupt/resume handling
		interruptValue := map[string]any{
			"message":  "Please approve the current state (yes/no):",
			"counter":  getInt(s, stateKeyCounter),
			"messages": getStrs(s, stateKeyMsgs),
		}

		// Interrupt execution and wait for user input
		resumeValue, err := graph.Interrupt(ctx, s, "approval", interruptValue)
		if err != nil {
			return nil, err
		}

		// Process the resume value
		approved := false
		if resumeStr, ok := resumeValue.(string); ok {
			approved = strings.ToLower(resumeStr) == "yes" || strings.ToLower(resumeStr) == "y"
		}

		return graph.State{
			stateKeyApproved: approved,
			stateKeyMsgs: append(getStrs(s, stateKeyMsgs),
				fmt.Sprintf("user approved: %t", approved)),
		}, nil
	})

	// Node 3: Process approval
	b.AddNode("process_approval", func(ctx context.Context, s graph.State) (any, error) {
		approved := getBool(s, stateKeyApproved)
		if !approved {
			return graph.State{
				stateKeyMsgs: append(getStrs(s, stateKeyMsgs),
					"user rejected - stopping execution"),
			}, nil
		}

		return graph.State{
			stateKeyMsgs: append(getStrs(s, stateKeyMsgs),
				"user approved - continuing execution"),
		}, nil
	})

	// Node 4: Final step
	b.AddNode("finalize", func(ctx context.Context, s graph.State) (any, error) {
		return graph.State{
			stateKeyMsgs: append(getStrs(s, stateKeyMsgs),
				"execution completed successfully"),
		}, nil
	})

	// Entry/finish and edges.
	b.SetEntryPoint("increment")
	b.SetFinishPoint("finalize")
	b.AddEdge("increment", "request_approval")
	b.AddEdge("request_approval", "process_approval")
	b.AddEdge("process_approval", "finalize")

	return b.Compile()
}

func runNormalExecution(ctx context.Context, exec *graph.Executor, threadID string) error {
	fmt.Printf("▶️  Running normal execution...\n")
	state := graph.State{
		stateKeyCounter: 0,
		stateKeyMsgs:    []string{"start"},
	}
	inv := &agent.Invocation{InvocationID: threadID}
	events, err := exec.Execute(ctx, state, inv)
	if err != nil {
		if graph.IsInterruptError(err) {
			fmt.Printf("⚠️  Execution interrupted: %v\n", err)
			return nil
		}
		return fmt.Errorf("execute failed: %w", err)
	}
	for range events {
	}
	fmt.Printf("✅ Normal execution completed\n")
	return nil
}

func runWithInterrupt(ctx context.Context, exec *graph.Executor, threadID string) error {
	fmt.Printf("🔄 Running with interrupt...\n")
	state := graph.State{
		stateKeyCounter: 0,
		stateKeyMsgs:    []string{"start"},
	}
	inv := &agent.Invocation{InvocationID: threadID}
	events, err := exec.Execute(ctx, state, inv)
	if err != nil {
		return fmt.Errorf("execute failed: %w", err)
	}

	// Process events and check for interrupts
	interrupted := false
	for event := range events {
		// Check if this is an interrupt event by checking the author
		if event.Author == "graph-pregel" {
			// Check if the event contains interrupt information
			if event.StateDelta != nil {
				fmt.Printf("📊 Event from: %s, StateDelta keys: %v\n", event.Author, getKeys(event.StateDelta))
				// Check for interrupt metadata
				if metadata, ok := event.StateDelta["_pregel_metadata"]; ok {
					fmt.Printf("📋 Metadata: %s\n", string(metadata))
					// Check if this contains interrupt information
					if strings.Contains(string(metadata), "interrupt") {
						fmt.Printf("⚠️  Detected interrupt event!\n")
						interrupted = true
					}
				}
			}
		}
	}

	if interrupted {
		fmt.Printf("💾 Execution interrupted, checkpoint saved\n")
		return nil
	}

	// Check if we have a checkpoint with interrupt state
	// This is a simplified approach - in a real implementation,
	// you would check the checkpoint saver for interrupt state
	fmt.Printf("✅ Execution completed\n")
	return nil
}

func resumeFromInterrupt(ctx context.Context, exec *graph.Executor, threadID, userInput string) error {
	fmt.Printf("⏪ Resuming from interrupt with input: %s\n", userInput)

	// Create resume command with ResumeMap for better key-based resume
	cmd := &graph.Command{
		ResumeMap: map[string]any{
			"approval": userInput,
		},
	}

	// Resume from the latest checkpoint
	state := graph.State{
		"__command__": cmd,
	}

	inv := &agent.Invocation{InvocationID: threadID}
	events, err := exec.Execute(ctx, state, inv)
	if err != nil {
		return fmt.Errorf("resume failed: %w", err)
	}

	// Process events
	for event := range events {
		// Just consume events for now
		_ = event
	}

	fmt.Printf("✅ Resume completed\n")
	return nil
}

func demoInterruptResume(ctx context.Context, exec *graph.Executor, manager *graph.CheckpointManager, threadID string) error {
	fmt.Println("🎬 Demo: interrupt -> resume -> complete")

	// Step 1: Run until interrupt
	fmt.Println("\n1️⃣  Running until interrupt...")
	if err := runWithInterrupt(ctx, exec, threadID); err != nil {
		return err
	}

	// Step 2: Resume with "yes"
	fmt.Println("\n2️⃣  Resuming with 'yes'...")
	if err := resumeFromInterrupt(ctx, exec, threadID, "yes"); err != nil {
		return err
	}

	// Step 3: List checkpoints
	fmt.Println("\n3️⃣  Listing checkpoints...")
	cfg := graph.CreateCheckpointConfig(threadID, "", defaultNamespace)
	filter := &graph.CheckpointFilter{Limit: 10}
	items, err := manager.ListCheckpoints(ctx, cfg, filter)
	if err != nil {
		return fmt.Errorf("list failed: %w", err)
	}

	fmt.Printf("Found %d checkpoints:\n", len(items))
	for i, t := range items {
		fmt.Printf("  %d. id=%s step=%d src=%s\n",
			i+1,
			t.Checkpoint.ID,
			t.Metadata.Step,
			t.Metadata.Source,
		)
		if t.Checkpoint.IsInterrupted() {
			fmt.Printf("     ⚠️  Interrupted at node: %s\n", t.Checkpoint.InterruptState.NodeID)
		}
	}

	return nil
}

// Helpers.

func getInt(s graph.State, key string) int {
	if v, ok := s[key].(int); ok {
		return v
	}
	return 0
}

func getStr(s graph.State, key string) string {
	if v, ok := s[key].(string); ok {
		return v
	}
	return ""
}

func getBool(s graph.State, key string) bool {
	if v, ok := s[key].(bool); ok {
		return v
	}
	return false
}

func getStrs(s graph.State, key string) []string {
	if v, ok := s[key].([]string); ok {
		return v
	}
	return []string{}
}

func getKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
