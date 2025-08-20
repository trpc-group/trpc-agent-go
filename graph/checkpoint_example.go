//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

// ExampleCheckpointUsage demonstrates how to use checkpoint functionality.
func ExampleCheckpointUsage() {
	// Create a state schema for a simple counter application.
	schema := NewStateSchema()
	schema.AddField("counter", StateField{
		Type:    reflect.TypeOf(0),
		Reducer: DefaultReducer,
		Default: func() any { return 0 },
	})
	schema.AddField("messages", StateField{
		Type:    reflect.TypeOf([]string{}),
		Reducer: AppendReducer,
		Default: func() any { return []string{} },
	})

	// Create a graph with checkpoint support.
	graph := NewStateGraph(schema)

	// Add nodes that increment counter and add messages.
	graph.AddNode("increment", func(ctx context.Context, state State) (any, error) {
		counter := state["counter"].(int)
		messages := state["messages"].([]string)

		newCounter := counter + 1
		newMessages := append(messages, fmt.Sprintf("Incremented to %d", newCounter))

		return State{
			"counter":  newCounter,
			"messages": newMessages,
		}, nil
	})

	graph.AddNode("double", func(ctx context.Context, state State) (any, error) {
		counter := state["counter"].(int)
		messages := state["messages"].([]string)

		newCounter := counter * 2
		newMessages := append(messages, fmt.Sprintf("Doubled to %d", newCounter))

		return State{
			"counter":  newCounter,
			"messages": newMessages,
		}, nil
	})

	// Set entry and finish points.
	graph.SetEntryPoint("increment")
	graph.SetFinishPoint("double")

	// Compile the graph.
	compiledGraph, err := graph.Compile()
	if err != nil {
		log.Fatalf("Failed to compile graph: %v", err)
	}

	// Create an in-memory checkpoint saver.
	// In production, you might use a Redis or database-based saver.
	checkpointSaver := NewInMemoryCheckpointSaver()

	// Create executor with checkpoint support.
	executor, err := NewExecutor(compiledGraph, WithCheckpointSaver(checkpointSaver))
	if err != nil {
		log.Fatalf("Failed to create executor: %v", err)
	}

	// Create a checkpoint manager for high-level operations.
	checkpointManager := NewCheckpointManager(checkpointSaver)

	// Example 1: Basic execution with checkpointing.
	fmt.Println("=== Example 1: Basic execution with checkpointing ===")

	ctx := context.Background()
	threadID := fmt.Sprintf("thread_%d", time.Now().UnixNano())

	// Initial state.
	initialState := State{
		"counter":  0,
		"messages": []string{"Starting counter"},
	}

	// Create invocation.
	invocation := &agent.Invocation{
		InvocationID: threadID,
	}

	// Execute the graph.
	eventChan, err := executor.Execute(ctx, initialState, invocation)
	if err != nil {
		log.Fatalf("Failed to execute graph: %v", err)
	}

	// Collect events.
	var events []*event.Event
	for event := range eventChan {
		events = append(events, event)
	}

	fmt.Printf("Execution completed with %d events\n", len(events))

	// Example 2: List checkpoints.
	fmt.Println("\n=== Example 2: List checkpoints ===")

	config := CreateCheckpointConfig(threadID, "", "")
	checkpoints, err := checkpointManager.ListCheckpoints(ctx, config, nil)
	if err != nil {
		log.Fatalf("Failed to list checkpoints: %v", err)
	}

	fmt.Printf("Found %d checkpoints for thread %s:\n", len(checkpoints), threadID)
	for i, tuple := range checkpoints {
		fmt.Printf("  %d. ID: %s, Step: %d, Source: %s\n",
			i+1, tuple.Checkpoint.ID, tuple.Metadata.Step, tuple.Metadata.Source)
	}

	// Example 3: Resume from a specific checkpoint.
	fmt.Println("\n=== Example 3: Resume from checkpoint ===")

	if len(checkpoints) > 0 {
		// Resume from the first checkpoint.
		firstCheckpoint := checkpoints[0]
		resumeConfig := CreateCheckpointConfig(threadID, firstCheckpoint.Checkpoint.ID, "")

		resumedState, err := checkpointManager.ResumeFromCheckpoint(ctx, resumeConfig)
		if err != nil {
			log.Fatalf("Failed to resume from checkpoint: %v", err)
		}

		fmt.Printf("Resumed state - Counter: %d, Messages: %v\n",
			resumedState["counter"], resumedState["messages"])
	}

	// Example 4: Manual checkpoint creation.
	fmt.Println("\n=== Example 4: Manual checkpoint creation ===")

	// Create a custom state.
	customState := State{
		"counter":  100,
		"messages": []string{"Custom checkpoint"},
	}

	// Create checkpoint manually.
	checkpoint, err := checkpointManager.CreateCheckpoint(ctx, config, customState, CheckpointSourceUpdate, 999)
	if err != nil {
		log.Fatalf("Failed to create manual checkpoint: %v", err)
	}

	fmt.Printf("Created manual checkpoint: %s\n", checkpoint.ID)

	// Example 5: Thread management.
	fmt.Println("\n=== Example 5: Thread management ===")

	// List all checkpoints again.
	checkpoints, err = checkpointManager.ListCheckpoints(ctx, config, nil)
	if err != nil {
		log.Fatalf("Failed to list checkpoints: %v", err)
	}

	fmt.Printf("Total checkpoints after manual creation: %d\n", len(checkpoints))

	// Delete the thread (cleanup).
	err = checkpointManager.DeleteThread(ctx, threadID)
	if err != nil {
		log.Fatalf("Failed to delete thread: %v", err)
	}

	fmt.Printf("Deleted thread %s\n", threadID)

	// Verify deletion.
	checkpoints, err = checkpointManager.ListCheckpoints(ctx, config, nil)
	if err != nil {
		log.Fatalf("Failed to list checkpoints after deletion: %v", err)
	}

	fmt.Printf("Checkpoints after deletion: %d\n", len(checkpoints))
}

// ExampleCheckpointFiltering demonstrates filtering checkpoints.
func ExampleCheckpointFiltering() {
	fmt.Println("\n=== Example: Checkpoint Filtering ===")

	saver := NewInMemoryCheckpointSaver()
	manager := NewCheckpointManager(saver)
	ctx := context.Background()

	threadID := "filter-example"
	config := CreateCheckpointConfig(threadID, "", "")

	// Create multiple checkpoints with different metadata.
	for i := 0; i < 5; i++ {
		state := State{"step": i}
		metadata := NewCheckpointMetadata(CheckpointSourceLoop, i)
		metadata.Extra["category"] = fmt.Sprintf("category_%d", i%3)
		metadata.Extra["priority"] = i % 2

		_, err := manager.CreateCheckpoint(ctx, config, state, metadata.Source, metadata.Step)
		if err != nil {
			log.Printf("Failed to create checkpoint %d: %v", i, err)
		}
	}

	// List all checkpoints.
	allCheckpoints, err := manager.ListCheckpoints(ctx, config, nil)
	if err != nil {
		log.Fatalf("Failed to list checkpoints: %v", err)
	}
	fmt.Printf("Total checkpoints: %d\n", len(allCheckpoints))

	// Filter by limit.
	limitFilter := &CheckpointFilter{Limit: 3}
	limitedCheckpoints, err := manager.ListCheckpoints(ctx, config, limitFilter)
	if err != nil {
		log.Fatalf("Failed to list limited checkpoints: %v", err)
	}
	fmt.Printf("Limited to 3 checkpoints: %d\n", len(limitedCheckpoints))

	// Filter by metadata.
	metadataFilter := &CheckpointFilter{
		Metadata: map[string]any{
			"category": "category_1",
		},
	}
	categoryCheckpoints, err := manager.ListCheckpoints(ctx, config, metadataFilter)
	if err != nil {
		log.Fatalf("Failed to list category checkpoints: %v", err)
	}
	fmt.Printf("Checkpoints in category_1: %d\n", len(categoryCheckpoints))

	// Filter by priority.
	priorityFilter := &CheckpointFilter{
		Metadata: map[string]any{
			"priority": 1,
		},
	}
	priorityCheckpoints, err := manager.ListCheckpoints(ctx, config, priorityFilter)
	if err != nil {
		log.Fatalf("Failed to list priority checkpoints: %v", err)
	}
	fmt.Printf("High priority checkpoints: %d\n", len(priorityCheckpoints))
}

