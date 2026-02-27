//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
)

const (
	engineBSP = "bsp"
	engineDAG = "dag"

	nodeEntry = "entry"
	nodeAsk   = "ask"
	nodeAfter = "after"

	stateKeyAnswer = "answer"

	interruptKey    = "approval"
	interruptPrompt = "please approve: ok?"

	defaultEngine = engineDAG
	defaultResume = "ok"

	runTimeout = 2 * time.Second
)

type interruptMeta struct {
	NodeID       string `json:"nodeID,omitempty"`
	InterruptKey string `json:"interruptKey,omitempty"`
	LineageID    string `json:"lineageId,omitempty"`
	CheckpointID string `json:"checkpointId,omitempty"`
}

func main() {
	var (
		engine = flag.String(
			"engine",
			defaultEngine,
			"Execution engine: bsp|dag",
		)
		resumeValue = flag.String(
			"resume",
			defaultResume,
			"Resume value for the interrupt",
		)
	)
	flag.Parse()

	selectedEngine, err := parseEngine(*engine)
	if err != nil {
		log.Fatalf("parse -engine failed: %v", err)
	}

	g := buildGraph()
	saver := inmemory.NewSaver()

	exec, err := graph.NewExecutor(
		g,
		graph.WithExecutionEngine(selectedEngine),
		graph.WithCheckpointSaver(saver),
		graph.WithMaxConcurrency(2),
	)
	if err != nil {
		log.Fatalf("create executor failed: %v", err)
	}

	lineageID := fmt.Sprintf("dag-interrupt-%d", time.Now().UnixNano())

	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Engine: %s\n", *engine)
	fmt.Printf("Lineage: %s\n", lineageID)
	fmt.Println("Run #1: expect interrupt")

	ctx1, cancel1 := context.WithTimeout(context.Background(), runTimeout)
	defer cancel1()

	evts1, err := exec.Execute(
		ctx1,
		graph.State{graph.CfgKeyLineageID: lineageID},
		&agent.Invocation{InvocationID: lineageID},
	)
	if err != nil {
		log.Fatalf("execute run #1 failed: %v", err)
	}

	meta, err := waitForInterrupt(evts1, runTimeout)
	if err != nil {
		log.Fatalf("run #1 wait failed: %v", err)
	}
	fmt.Printf(
		"Interrupted: node=%s key=%s checkpoint=%s\n",
		meta.NodeID,
		meta.InterruptKey,
		meta.CheckpointID,
	)

	fmt.Println(strings.Repeat("-", 60))
	fmt.Println("Run #2: resume and expect completion")

	ctx2, cancel2 := context.WithTimeout(context.Background(), runTimeout)
	defer cancel2()

	resumeCmd := (&graph.ResumeCommand{}).WithResumeMap(map[string]any{
		interruptKey: *resumeValue,
	})
	resumeState := graph.State{
		graph.CfgKeyLineageID:    meta.LineageID,
		graph.CfgKeyCheckpointID: meta.CheckpointID,
		graph.StateKeyCommand:    resumeCmd,
	}
	evts2, err := exec.Execute(
		ctx2,
		resumeState,
		&agent.Invocation{InvocationID: lineageID + "-resume"},
	)
	if err != nil {
		log.Fatalf("execute run #2 failed: %v", err)
	}

	finalState, err := waitForCompletion(evts2, runTimeout)
	if err != nil {
		log.Fatalf("run #2 wait failed: %v", err)
	}
	fmt.Printf("Completed: answer=%v\n", finalState[stateKeyAnswer])
}

func parseEngine(raw string) (graph.ExecutionEngine, error) {
	engine := strings.ToLower(strings.TrimSpace(raw))
	switch engine {
	case engineBSP:
		return graph.ExecutionEngineBSP, nil
	case engineDAG:
		return graph.ExecutionEngineDAG, nil
	default:
		return "", fmt.Errorf("unknown engine %q", raw)
	}
}

func buildGraph() *graph.Graph {
	schema := graph.NewStateSchema()
	sg := graph.NewStateGraph(schema)

	sg.AddNode(
		nodeEntry,
		func(context.Context, graph.State) (any, error) {
			return nil, nil
		},
	)
	sg.AddNode(
		nodeAsk,
		func(ctx context.Context, state graph.State) (any, error) {
			v, err := graph.Interrupt(
				ctx,
				state,
				interruptKey,
				interruptPrompt,
			)
			if err != nil {
				return nil, err
			}
			return graph.State{stateKeyAnswer: v}, nil
		},
	)
	sg.AddNode(
		nodeAfter,
		func(context.Context, graph.State) (any, error) {
			return nil, nil
		},
	)

	sg.SetEntryPoint(nodeEntry)
	sg.AddEdge(nodeEntry, nodeAsk)
	sg.AddEdge(nodeAsk, nodeAfter)

	g, err := sg.Compile()
	if err != nil {
		panic(err)
	}
	return g
}

func waitForInterrupt(
	evts <-chan *event.Event,
	timeout time.Duration,
) (interruptMeta, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var meta interruptMeta
	for {
		select {
		case e, ok := <-evts:
			if !ok {
				return interruptMeta{}, fmt.Errorf("missing interrupt event")
			}
			m, ok := interruptMetaFromEvent(e)
			if !ok {
				continue
			}
			meta = m
			if meta.CheckpointID == "" || meta.LineageID == "" {
				continue
			}
			return meta, nil
		case <-timer.C:
			return interruptMeta{}, fmt.Errorf("timeout waiting for interrupt")
		}
	}
}

func waitForCompletion(
	evts <-chan *event.Event,
	timeout time.Duration,
) (map[string]any, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case e, ok := <-evts:
			if !ok {
				return nil, fmt.Errorf("missing completion event")
			}
			if e == nil || e.Response == nil || !e.Response.Done {
				continue
			}
			return parseStateDelta(e.StateDelta), nil
		case <-timer.C:
			return nil, fmt.Errorf("timeout waiting for completion")
		}
	}
}

func interruptMetaFromEvent(e *event.Event) (interruptMeta, bool) {
	if e == nil || e.Object != graph.ObjectTypeGraphPregelStep {
		return interruptMeta{}, false
	}
	if e.StateDelta == nil {
		return interruptMeta{}, false
	}

	raw := e.StateDelta[graph.MetadataKeyPregel]
	if len(raw) == 0 {
		return interruptMeta{}, false
	}

	var meta graph.PregelStepMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return interruptMeta{}, false
	}
	if meta.InterruptValue == nil {
		return interruptMeta{}, false
	}

	return interruptMeta{
		NodeID:       meta.NodeID,
		InterruptKey: meta.InterruptKey,
		LineageID:    meta.LineageID,
		CheckpointID: meta.CheckpointID,
	}, true
}

func parseStateDelta(raw map[string][]byte) map[string]any {
	out := make(map[string]any)
	if raw == nil {
		return out
	}
	for key, value := range raw {
		if key == "" || len(value) == 0 {
			continue
		}
		if key[0] == '_' {
			continue
		}
		var v any
		if err := json.Unmarshal(value, &v); err != nil {
			continue
		}
		out[key] = v
	}
	return out
}
