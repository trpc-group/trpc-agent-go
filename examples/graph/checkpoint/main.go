//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates checkpointing features using the graph package.
// It shows how to run a graph with checkpointing enabled, list checkpoints,
// resume from the latest or a specific checkpoint, create manual checkpoints,
// filter checkpoints, and delete a lineage.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"reflect"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
)

const (
	defaultLineagePrefix = "checkpoint-demo"
	defaultNamespace     = ""
	defaultMode          = "run" // Modes: run, list, resume, goto,
	// manual, filter, delete, demo.
	defaultSteps    = 3
	stateKeyCounter = "counter"
	stateKeyMsgs    = "messages"
)

var (
	modeFlag = flag.String("mode", defaultMode,
		"Mode: run|list|resume|goto|manual|filter|delete|demo")
	lineageFlag = flag.String("lineage", "",
		"Lineage ID for checkpointing (default: auto)")
	stepsFlag = flag.Int("steps", defaultSteps,
		"Steps to execute in run/demo modes")
	ckptIDFlag = flag.String("checkpoint", "",
		"Checkpoint ID for goto mode")
	limitFlag    = flag.Int("limit", 0, "Limit for filter/list mode")
	categoryFlag = flag.String("category", "",
		"Metadata category for filter/manual modes")
)

func main() {
	flag.Parse()

	lineageID := *lineageFlag
	if lineageID == "" {
		lineageID = fmt.Sprintf("%s-%d", defaultLineagePrefix, time.Now().Unix())
	}

	fmt.Printf("🔐 Checkpoint Demo\n")
	fmt.Printf("Lineage: %s\n", lineageID)
	fmt.Println(strings.Repeat("=", 50))

	ctx := context.Background()

	// Prepare graph and executor with checkpointing enabled.
	g, err := buildGraph()
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
		if err := runSteps(ctx, exec, lineageID, *stepsFlag); err != nil {
			log.Fatalf("run failed: %v", err)
		}
	case "list":
		if err := listCheckpoints(ctx, manager, lineageID, *limitFlag, nil); err != nil {
			log.Fatalf("list failed: %v", err)
		}
	case "resume":
		if err := resumeLatest(ctx, exec, lineageID); err != nil {
			log.Fatalf("resume failed: %v", err)
		}
	case "goto":
		if *ckptIDFlag == "" {
			log.Fatalf("checkpoint ID required for goto mode")
		}
		if err := resumeSpecific(ctx, exec, manager, lineageID, *ckptIDFlag); err != nil {
			log.Fatalf("goto failed: %v", err)
		}
	case "manual":
		if err := createManualCheckpoint(ctx, saver, lineageID, *categoryFlag); err != nil {
			log.Fatalf("manual failed: %v", err)
		}
	case "filter":
		filter := map[string]any{}
		if *categoryFlag != "" {
			filter["category"] = *categoryFlag
		}
		if err := listCheckpoints(ctx, manager, lineageID, *limitFlag, filter); err != nil {
			log.Fatalf("filter failed: %v", err)
		}
	case "delete":
		if err := manager.DeleteLineage(ctx, lineageID); err != nil {
			log.Fatalf("delete failed: %v", err)
		}
		fmt.Printf("🧹 Deleted lineage %s\n", lineageID)
	case "demo":
		if err := demoAll(ctx, exec, manager, saver, lineageID, *stepsFlag); err != nil {
			log.Fatalf("demo failed: %v", err)
		}
	default:
		log.Fatalf("unknown mode: %s", *modeFlag)
	}
}

func buildGraph() (*graph.Graph, error) {
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

	// Build a simple linear graph with three increment nodes.
	b := graph.NewStateGraph(schema)
	b.AddNode("inc1", func(ctx context.Context, s graph.State) (any, error) {
		v := getInt(s, stateKeyCounter)
		return graph.State{
			stateKeyCounter: v + 1,
			stateKeyMsgs: append(getStrs(s, stateKeyMsgs),
				fmt.Sprintf("inc1 -> %d", v+1)),
		}, nil
	})
	b.AddNode("inc2", func(ctx context.Context, s graph.State) (any, error) {
		v := getInt(s, stateKeyCounter)
		return graph.State{
			stateKeyCounter: v + 1,
			stateKeyMsgs: append(getStrs(s, stateKeyMsgs),
				fmt.Sprintf("inc2 -> %d", v+1)),
		}, nil
	})
	b.AddNode("inc3", func(ctx context.Context, s graph.State) (any, error) {
		v := getInt(s, stateKeyCounter)
		return graph.State{
			stateKeyCounter: v + 1,
			stateKeyMsgs: append(getStrs(s, stateKeyMsgs),
				fmt.Sprintf("inc3 -> %d", v+1)),
		}, nil
	})

	// Entry/finish and edges.
	b.SetEntryPoint("inc1")
	b.SetFinishPoint("inc3")
	b.AddEdge("inc1", "inc2")
	b.AddEdge("inc2", "inc3")

	return b.Compile()
}

func runSteps(ctx context.Context, exec *graph.Executor, lineageID string, steps int) error {
	if steps <= 0 {
		return errors.New("steps must be > 0")
	}
	// Execute graph multiple times to generate checkpoints.
	state := graph.State{
		stateKeyCounter: 0,
		stateKeyMsgs:    []string{"start"},
	}
	for i := 0; i < steps; i++ {
		inv := &agent.Invocation{InvocationID: lineageID}
		events, err := exec.Execute(ctx, state, inv)
		if err != nil {
			return fmt.Errorf("execute failed: %w", err)
		}
		n := consumeAllEvents(events)
		fmt.Printf("✅ Run %d completed (%d events)\n", i+1, n)
	}
	return nil
}

func resumeLatest(ctx context.Context, exec *graph.Executor, lineageID string) error {
	fmt.Printf("⏪ Resuming from latest checkpoint...\n")
	inv := &agent.Invocation{InvocationID: lineageID}
	events, err := exec.Execute(ctx, graph.State{}, inv)
	if err != nil {
		return fmt.Errorf("resume failed: %w", err)
	}
	n := consumeAllEvents(events)
	fmt.Printf("✅ Resume completed (%d events)\n", n)
	return nil
}

func resumeSpecific(ctx context.Context, exec *graph.Executor, manager *graph.CheckpointManager, lineageID, checkpointID string) error {
	fmt.Printf("🎯 Resuming from checkpoint: %s\n", checkpointID)
	cfg := graph.CreateCheckpointConfig(lineageID, checkpointID, defaultNamespace)
	st, err := manager.ResumeFromCheckpoint(ctx, cfg)
	if err != nil {
		return fmt.Errorf("load checkpoint failed: %w", err)
	}
	inv := &agent.Invocation{InvocationID: lineageID}
	events, err := exec.Execute(ctx, st, inv)
	if err != nil {
		return fmt.Errorf("execute failed: %w", err)
	}
	n := consumeAllEvents(events)
	fmt.Printf("✅ Resume from %s completed (%d events)\n", checkpointID, n)
	return nil
}

func createManualCheckpoint(ctx context.Context, saver graph.CheckpointSaver, lineageID, category string) error {
	fmt.Printf("✍️  Creating manual checkpoint...\n")
	cfg := graph.CreateCheckpointConfig(lineageID, "", defaultNamespace)
	state := graph.State{
		stateKeyCounter: 100,
		stateKeyMsgs:    []string{"manual"},
	}
	// Build checkpoint with metadata including category for filtering.
	chVals := map[string]any{
		stateKeyCounter: state[stateKeyCounter],
		stateKeyMsgs:    state[stateKeyMsgs],
	}
	chVers := map[string]any{
		stateKeyCounter: 1,
		stateKeyMsgs:    1,
	}
	ckpt := graph.NewCheckpoint(chVals, chVers, map[string]map[string]any{})
	meta := graph.NewCheckpointMetadata(graph.CheckpointSourceUpdate, 999)
	if category != "" {
		if meta.Extra == nil {
			meta.Extra = map[string]any{}
		}
		meta.Extra["category"] = category
	}
	req := graph.PutRequest{
		Config:      cfg,
		Checkpoint:  ckpt,
		Metadata:    meta,
		NewVersions: chVers,
	}
	_, err := saver.Put(ctx, req)
	if err != nil {
		return fmt.Errorf("manual checkpoint failed: %w", err)
	}
	fmt.Printf("🆔 Manual checkpoint created: %s\n", ckpt.ID)
	return nil
}

func listCheckpoints(ctx context.Context, manager *graph.CheckpointManager, lineageID string, limit int, metadata map[string]any) error {
	fmt.Printf("📜 Listing checkpoints (limit=%d)\n", limit)
	cfg := graph.CreateCheckpointConfig(lineageID, "", defaultNamespace)
	var filter *graph.CheckpointFilter
	if limit > 0 || len(metadata) > 0 {
		filter = &graph.CheckpointFilter{Limit: limit, Metadata: metadata}
	}
	items, err := manager.ListCheckpoints(ctx, cfg, filter)
	if err != nil {
		return fmt.Errorf("list failed: %w", err)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Checkpoint.Timestamp.Before(items[j].Checkpoint.Timestamp)
	})
	for i, t := range items {
		fmt.Printf("%2d. id=%s ts=%s step=%d src=%s updates=%v\n",
			i+1,
			t.Checkpoint.ID,
			t.Checkpoint.Timestamp.Format(time.RFC3339),
			t.Metadata.Step,
			t.Metadata.Source,
			t.Checkpoint.UpdatedChannels,
		)
	}
	if len(items) == 0 {
		fmt.Println("(no checkpoints)")
	}
	return nil
}

func demoAll(
	ctx context.Context,
	exec *graph.Executor,
	manager *graph.CheckpointManager,
	saver graph.CheckpointSaver,
	lineageID string,
	steps int,
) error {
	fmt.Println("▶️  Demo: run -> list -> resume -> manual -> filter -> delete")
	if err := runSteps(ctx, exec, lineageID, steps); err != nil {
		return err
	}
	if err := listCheckpoints(ctx, manager, lineageID, 0, nil); err != nil {
		return err
	}
	if err := resumeLatest(ctx, exec, lineageID); err != nil {
		return err
	}
	if err := createManualCheckpoint(ctx, saver, lineageID, "category_demo"); err != nil {
		return err
	}
	if err := listCheckpoints(ctx, manager, lineageID, 10,
		map[string]any{"category": "category_demo"}); err != nil {
		return err
	}
	if err := manager.DeleteLineage(ctx, lineageID); err != nil {
		return err
	}
	fmt.Printf("🧹 Deleted lineage %s\n", lineageID)
	return nil
}

// Helpers.

//nolint:revive
func getInt(s graph.State, key string) int {
	if v, ok := s[key].(int); ok {
		return v
	}
	return 0
}

//nolint:revive
func getStrs(s graph.State, key string) []string {
	if v, ok := s[key].([]string); ok {
		return v
	}
	return []string{}
}

// consumeAllEvents drains the event channel and returns the number of events.
func consumeAllEvents(ch <-chan *event.Event) int {
	count := 0
	for range ch {
		count++
	}
	return count
}
