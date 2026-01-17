//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates static interrupts (debug breakpoints) that pause
// execution before/after a node runs, without calling graph.Interrupt inside
// the node logic.
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
	checkpointinmemory "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
)

const (
	nodeStart  = "start"
	nodeMiddle = "middle"
	nodeEnd    = "end"

	stateKeyOrder = "order"
)

var (
	lineageID = flag.String(
		"lineage",
		fmt.Sprintf("static-interrupt-%d", time.Now().Unix()),
		"Checkpoint lineage ID (used for resume)",
	)
)

type interruptMeta struct {
	NodeID         string `json:"nodeID,omitempty"`
	InterruptKey   string `json:"interruptKey,omitempty"`
	LineageID      string `json:"lineageId,omitempty"`
	CheckpointID   string `json:"checkpointId,omitempty"`
	InterruptValue any    `json:"interruptValue,omitempty"`
}

func main() {
	flag.Parse()

	saver := checkpointinmemory.NewSaver()

	g, err := buildGraph()
	if err != nil {
		log.Fatalf("build graph failed: %v", err)
	}
	exec, err := graph.NewExecutor(g, graph.WithCheckpointSaver(saver))
	if err != nil {
		log.Fatalf("create executor failed: %v", err)
	}

	ctx := context.Background()

	fmt.Printf("Lineage: %s\n", *lineageID)

	meta1, done1, err := runOnce(ctx, exec, *lineageID, "")
	if err != nil {
		log.Fatalf("run 1 failed: %v", err)
	}
	if done1 != nil {
		log.Fatalf("run 1 unexpectedly completed")
	}
	fmt.Printf("Interrupt #1: node=%s key=%s checkpoint=%s\n",
		meta1.NodeID,
		meta1.InterruptKey,
		meta1.CheckpointID,
	)

	meta2, done2, err := runOnce(ctx, exec, *lineageID, meta1.CheckpointID)
	if err != nil {
		log.Fatalf("run 2 failed: %v", err)
	}
	if done2 != nil {
		log.Fatalf("run 2 unexpectedly completed")
	}
	fmt.Printf("Interrupt #2: node=%s key=%s checkpoint=%s\n",
		meta2.NodeID,
		meta2.InterruptKey,
		meta2.CheckpointID,
	)

	_, done3, err := runOnce(ctx, exec, *lineageID, meta2.CheckpointID)
	if err != nil {
		log.Fatalf("run 3 failed: %v", err)
	}
	if done3 == nil {
		log.Fatalf("run 3 unexpectedly interrupted")
	}

	order, err := finalOrder(done3)
	if err != nil {
		log.Fatalf("decode final state failed: %v", err)
	}
	fmt.Printf("Final order: %v\n", order)
}

func buildGraph() (*graph.Graph, error) {
	schema := graph.NewStateSchema().AddField(stateKeyOrder, graph.StateField{
		Type:    reflect.TypeOf([]string{}),
		Reducer: graph.StringSliceReducer,
		Default: func() any { return []string{} },
	})

	sg := graph.NewStateGraph(schema)

	sg.AddNode(nodeStart, func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{stateKeyOrder: []string{nodeStart}}, nil
	})

	sg.AddNode(
		nodeMiddle,
		func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{stateKeyOrder: []string{nodeMiddle}}, nil
		},
		graph.WithInterruptBefore(),
		graph.WithInterruptAfter(),
	)

	sg.AddNode(nodeEnd, func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{stateKeyOrder: []string{nodeEnd}}, nil
	})

	sg.SetEntryPoint(nodeStart)
	sg.AddEdge(nodeStart, nodeMiddle)
	sg.AddEdge(nodeMiddle, nodeEnd)
	sg.SetFinishPoint(nodeEnd)

	return sg.Compile()
}

func runOnce(
	ctx context.Context,
	exec *graph.Executor,
	lineageID string,
	checkpointID string,
) (*interruptMeta, *event.Event, error) {
	st := graph.State{graph.CfgKeyLineageID: lineageID}
	if checkpointID != "" {
		st[graph.CfgKeyCheckpointID] = checkpointID
	}

	inv := &agent.Invocation{
		InvocationID: fmt.Sprintf("%s-%d", lineageID, time.Now().UnixNano()),
	}
	ch, err := exec.Execute(ctx, st, inv)
	if err != nil {
		return nil, nil, err
	}

	var done *event.Event
	for evt := range ch {
		if evt == nil {
			continue
		}
		if evt.Done {
			done = evt
			break
		}
		if meta := extractInterruptMeta(evt); meta != nil {
			return meta, nil, nil
		}
	}
	return nil, done, nil
}

func extractInterruptMeta(evt *event.Event) *interruptMeta {
	if evt == nil || evt.Object != graph.ObjectTypeGraphPregelStep {
		return nil
	}
	if evt.StateDelta == nil {
		return nil
	}
	raw, ok := evt.StateDelta[graph.MetadataKeyPregel]
	if !ok {
		return nil
	}

	var meta interruptMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil
	}
	if meta.InterruptKey == "" || meta.CheckpointID == "" {
		return nil
	}
	return &meta
}

func finalOrder(doneEvent *event.Event) ([]string, error) {
	if doneEvent == nil || doneEvent.StateDelta == nil {
		return nil, fmt.Errorf("missing done event or state delta")
	}

	raw, ok := doneEvent.StateDelta[stateKeyOrder]
	if !ok {
		return nil, fmt.Errorf("state delta missing %q", stateKeyOrder)
	}

	var order []string
	if err := json.Unmarshal(raw, &order); err != nil {
		return nil, fmt.Errorf("decode %q: %w", stateKeyOrder, err)
	}
	return order, nil
}
