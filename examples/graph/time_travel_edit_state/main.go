//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates checkpoint-based "time travel" state editing.
//
// It runs a graph that interrupts, then:
//  1. reads the checkpoint state via graph.TimeTravel.GetState
//  2. writes an "update" checkpoint via graph.TimeTravel.EditState
//  3. resumes execution from the updated checkpoint
package main

import (
	"context"
	"fmt"
	"log"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	checkpointinmemory "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
)

const (
	appName = "time_travel_edit_state"

	lineageID  = "lineage-demo"
	namespace  = appName
	nodeReview = "review"

	stateKeyCounter  = "counter"
	stateKeyDecision = "decision"

	interruptKeyReview = "review_key"

	initialCounter = 1
	editedCounter  = 42
)

func main() {
	ctx := context.Background()

	g, err := buildGraph()
	if err != nil {
		log.Fatalf("build graph: %v", err)
	}

	saver := checkpointinmemory.NewSaver()
	exec, err := graph.NewExecutor(g, graph.WithCheckpointSaver(saver))
	if err != nil {
		log.Fatalf("new executor: %v", err)
	}
	tt, err := exec.TimeTravel()
	if err != nil {
		log.Fatalf("time travel: %v", err)
	}

	runUntilClosed(ctx, exec, &agent.Invocation{
		AgentName:    appName,
		InvocationID: "inv-1",
	}, graph.State{
		graph.CfgKeyLineageID:    lineageID,
		graph.CfgKeyCheckpointNS: namespace,
		stateKeyCounter:          initialCounter,
	})

	h, err := tt.History(ctx, lineageID, namespace, 1)
	if err != nil || len(h) == 0 {
		log.Fatalf("history: %v", err)
	}
	base := h[0].Ref
	fmt.Printf("Base checkpoint: %s (source=%s)\n",
		base.CheckpointID,
		h[0].Source,
	)

	before, err := tt.GetState(ctx, base)
	if err != nil {
		log.Fatalf("get state: %v", err)
	}
	fmt.Printf("Before edit: counter=%v\n",
		before.State[stateKeyCounter],
	)

	updatedRef, err := tt.EditState(ctx, base, graph.State{
		stateKeyCounter: editedCounter,
	})
	if err != nil {
		log.Fatalf("edit state: %v", err)
	}
	fmt.Printf("Updated checkpoint: %s\n", updatedRef.CheckpointID)

	afterEdit, err := tt.GetState(ctx, updatedRef)
	if err != nil {
		log.Fatalf("get edited state: %v", err)
	}
	fmt.Printf("After edit: counter=%v\n",
		afterEdit.State[stateKeyCounter],
	)

	cmd := graph.NewResumeCommand().
		AddResumeValue(interruptKeyReview, "approved")
	resumeState := graph.State(updatedRef.ToRuntimeState())
	resumeState[graph.StateKeyCommand] = cmd

	runUntilClosed(ctx, exec, &agent.Invocation{
		AgentName:    appName,
		InvocationID: "inv-2",
	}, resumeState)

	finalSnap, err := tt.GetState(ctx, graph.CheckpointRef{
		LineageID: lineageID,
		Namespace: namespace,
	})
	if err != nil {
		log.Fatalf("get final state: %v", err)
	}
	fmt.Printf(
		"Final: counter=%v decision=%v\n",
		finalSnap.State[stateKeyCounter],
		finalSnap.State[stateKeyDecision],
	)
	fmt.Println("Done.")
}

func buildGraph() (*graph.Graph, error) {
	schema := graph.NewStateSchema().
		AddField(stateKeyCounter, graph.StateField{
			Type:    reflect.TypeOf(int(0)),
			Default: func() any { return 0 },
		}).
		AddField(stateKeyDecision, graph.StateField{
			Type:    reflect.TypeOf(""),
			Default: func() any { return "" },
		})

	sg := graph.NewStateGraph(schema)
	sg.AddNode(
		nodeReview,
		func(ctx context.Context, st graph.State) (any, error) {
			counter, _ := st[stateKeyCounter].(int)

			v, err := graph.Interrupt(
				ctx,
				st,
				interruptKeyReview,
				fmt.Sprintf("Please review counter=%d", counter),
			)
			if err != nil {
				return nil, err
			}
			decision, _ := v.(string)
			return graph.State{
				stateKeyDecision: decision,
				stateKeyCounter:  counter + 1,
			}, nil
		},
	)
	sg.SetEntryPoint(nodeReview)
	sg.SetFinishPoint(nodeReview)
	return sg.Compile()
}

func runUntilClosed(
	ctx context.Context,
	exec *graph.Executor,
	inv *agent.Invocation,
	state graph.State,
) {
	ch, err := exec.Execute(ctx, state, inv)
	if err != nil {
		log.Fatalf("execute: %v", err)
	}
	for range ch {
	}
}
