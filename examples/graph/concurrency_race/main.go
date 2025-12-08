//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main provides a minimal graph example to expose the current
// concurrency issue when reusing a shared GraphAgent/Executor across
// concurrent invocations.
//
// The graph is intentionally simple:
//
//	start -> worker
//
// Node "worker" increments a per-run counter field. When the executor is
// reused concurrently and channels are shared at the Graph level, some
// runs will skip the "worker" node entirely, leaving the counter at 0.
// After we refactor channel state into per-execution context, all runs
// should consistently see counter == 1.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	stateKeyCounter = "counter"

	defaultUserID      = "user-concurrency-demo"
	defaultConcurrency = 32
	defaultRounds      = 64
)

func main() {
	ctx := context.Background()

	r, err := newConcurrentRunner()
	if err != nil {
		panic(fmt.Errorf("failed to create runner: %w", err))
	}
	defer r.Close()

	fmt.Printf("🚀 concurrency_race example: %d goroutines × %d rounds\n",
		defaultConcurrency, defaultRounds)

	failures := runConcurrentInvocations(ctx, r, defaultConcurrency, defaultRounds)
	if len(failures) == 0 {
		fmt.Println("✅ No missing worker executions observed (try increasing rounds/concurrency if needed).")
		return
	}

	fmt.Printf("❌ Detected %d runs where worker node did not execute:\n", len(failures))
	for _, f := range failures {
		fmt.Println("   -", f)
	}
}

// newConcurrentRunner builds a minimal graph, wraps it in a GraphAgent and
// Runner, and returns the Runner instance for shared reuse across goroutines.
func newConcurrentRunner() (runner.Runner, error) {
	g, err := buildTestGraph()
	if err != nil {
		return nil, err
	}

	graphAgent, err := graphagent.New("concurrency-race-graph", g,
		graphagent.WithDescription("Minimal graph to reproduce concurrency race"),
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create graph agent: %w", err)
	}

	sessionService := inmemory.NewSessionService()
	r := runner.NewRunner(
		"concurrency-race-app",
		graphAgent,
		runner.WithSessionService(sessionService),
	)
	return r, nil
}

// buildTestGraph constructs:
//
//	start -> worker
//
// The worker node increments stateKeyCounter by 1. With correct per-execution
// channel isolation, every run should see counter == 1 at completion.
func buildTestGraph() (*graph.Graph, error) {
	schema := graph.MessagesStateSchema()
	schema.AddField(stateKeyCounter, graph.StateField{
		Type:    reflect.TypeOf(int(0)),
		Reducer: graph.DefaultReducer,
		Default: func() any { return 0 },
	})

	sg := graph.NewStateGraph(schema)

	sg.AddNode("start", func(ctx context.Context, state graph.State) (any, error) {
		// No-op; the edge start -> worker is what matters for the branch channel.
		return nil, nil
	})

	sg.AddNode("worker", func(ctx context.Context, state graph.State) (any, error) {
		current, _ := state[stateKeyCounter].(int)
		return graph.State{
			stateKeyCounter: current + 1,
		}, nil
	})

	sg.SetEntryPoint("start").
		SetFinishPoint("worker")

	// Static edge start -> worker. The underlying implementation will create
	// a shared branch channel "branch:to:worker", which is where the race
	// manifests when multiple executions share the same Graph channels.
	sg.AddEdge("start", "worker")

	return sg.Compile()
}

// runConcurrentInvocations executes many runs concurrently against the same
// Runner. It returns a slice of human-readable failure descriptions where
// the worker node did not increment the counter.
func runConcurrentInvocations(
	ctx context.Context,
	r runner.Runner,
	concurrency, rounds int,
) []string {
	var (
		wg       sync.WaitGroup
		failMu   sync.Mutex
		failures []string
	)

	for round := 0; round < rounds; round++ {
		wg.Add(concurrency)
		for i := 0; i < concurrency; i++ {
			go func(round, idx int) {
				defer wg.Done()

				sessionID := fmt.Sprintf("session-%d-%d-%d",
					round, idx, time.Now().UnixNano())

				counter, err := runSingle(ctx, r, defaultUserID, sessionID)
				if err != nil {
					failMu.Lock()
					failures = append(failures,
						fmt.Sprintf("round=%d idx=%d session=%s error=%v",
							round, idx, sessionID, err))
					failMu.Unlock()
					return
				}

				if counter != 1 {
					failMu.Lock()
					failures = append(failures,
						fmt.Sprintf("round=%d idx=%d session=%s final_counter=%d (expected 1)",
							round, idx, sessionID, counter))
					failMu.Unlock()
				}
			}(round, i)
		}
	}

	wg.Wait()
	return failures
}

// runSingle executes a single run and returns the final counter value observed
// from the graph.execution completion event.
func runSingle(
	ctx context.Context,
	r runner.Runner,
	userID, sessionID string,
) (int, error) {
	msg := model.NewUserMessage("ping")
	events, err := r.Run(ctx, userID, sessionID, msg)
	if err != nil {
		return 0, fmt.Errorf("run failed: %w", err)
	}

	finalCounter := 0
	seenCompletion := false

	for ev := range events {
		if ev == nil {
			continue
		}

		if ev.Error != nil {
			return 0, fmt.Errorf("graph error: %s", ev.Error.Message)
		}

		if ev.Object == graph.ObjectTypeGraphExecution && ev.Done && ev.StateDelta != nil {
			if raw, ok := ev.StateDelta[stateKeyCounter]; ok && raw != nil {
				var v int
				if err := json.Unmarshal(raw, &v); err == nil {
					finalCounter = v
				}
			}
			seenCompletion = true
		}
	}

	if !seenCompletion {
		return 0, fmt.Errorf("no graph.execution completion event observed")
	}

	return finalCounter, nil
}
