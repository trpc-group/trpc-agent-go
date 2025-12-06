//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates node-level Retry/Backoff with an unstable function node.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Name of the model to use")
	failCount = flag.Int("fail", 2, "Number of initial failures for the unstable node")
	latency   = flag.Duration("latency", 200*time.Millisecond, "Simulated latency per attempt")
	verbose   = flag.Bool("verbose", false, "Enable verbose event logging")
)

func main() {
	flag.Parse()
	fmt.Println("🔁 Graph Retry/Backoff Example")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Println(strings.Repeat("=", 50))

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Build graph
	g, err := createGraph()
	if err != nil {
		return err
	}

	// Create GraphAgent
	ga, err := graphagent.New(
		"retry-demo",
		g,
		graphagent.WithDescription("Node-level retry/backoff demonstration"),
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		return fmt.Errorf("create graph agent: %w", err)
	}

	// Create runner
	sessSvc := inmemory.NewSessionService()
	r := runner.NewRunner("retry-demo", ga, runner.WithSessionService(sessSvc))

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

	user := "user"
	session := fmt.Sprintf("retry-session-%d", time.Now().Unix())

	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("💡 Hint: OPENAI_API_KEY not set. If required by your model, export it.")
	}

	// Interactive loop
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("💬 Interactive Mode — enter a prompt (or 'exit')")
	for {
		fmt.Print("📝 Prompt: ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if text == "exit" || text == "quit" {
			fmt.Println("👋 Bye!")
			return nil
		}

		msg := model.NewUserMessage(text)
		evCh, err := r.Run(context.Background(), user, session, msg)
		if err != nil {
			fmt.Printf("❌ Run error: %v\n", err)
			continue
		}
		if err := handleStreaming(evCh); err != nil {
			fmt.Printf("❌ Stream error: %v\n", err)
		}
		fmt.Println()
	}
	return scanner.Err()
}

func createGraph() (*graph.Graph, error) {
	schema := graph.MessagesStateSchema()
	llm := openai.New(*modelName)

	sg := graph.NewStateGraph(schema)

	// Unstable node: fails N-1 times then succeeds.
	// Use a retry policy that matches any error for demo purposes.
	demoRetry := graph.RetryPolicy{
		MaxAttempts:     3,
		InitialInterval: 200 * time.Millisecond,
		BackoffFactor:   2.0,
		MaxInterval:     1 * time.Second,
		Jitter:          true,
		RetryOn:         []graph.RetryCondition{graph.RetryConditionFunc(func(error) bool { return true })},
	}
	sg.AddNode("unstable_api", unstableAPINode, graph.WithRetryPolicy(demoRetry))

	// LLM answer node: summarizes fetched data and answers the user.
	sg.AddLLMNode(
		"answer",
		llm,
		`You are a helpful assistant.
Given user's prompt and fetched data, provide a concise helpful answer.
If fetched data exists, include it.
Respond with the final answer only.`,
		map[string]tool.Tool{},
	)

	// Wiring
	sg.SetEntryPoint("unstable_api")
	sg.AddEdge("unstable_api", "answer")
	sg.SetFinishPoint("answer")

	return sg.Compile()
}

// attemptTracker stores per-invocation attempt counts for the unstable node.
var attemptTracker sync.Map // key: invocationID+":"+nodeID -> int

func unstableAPINode(ctx context.Context, state graph.State) (any, error) {
	// Simulate external call that may fail a few times
	input := ""
	if v, ok := state[graph.StateKeyUserInput].(string); ok {
		input = v
	}
	if input == "" {
		return nil, errors.New("no user input provided")
	}

	invID := ""
	if ec, ok := state[graph.StateKeyExecContext].(*graph.ExecutionContext); ok && ec != nil {
		invID = ec.InvocationID
	}
	nodeID := ""
	if nid, ok := state[graph.StateKeyCurrentNodeID].(string); ok {
		nodeID = nid
	}

	key := invID + ":" + nodeID
	var cur int
	if v, ok := attemptTracker.Load(key); ok {
		cur, _ = v.(int)
	}
	cur++
	attemptTracker.Store(key, cur)

	// Simulated latency
	if *latency > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(*latency):
		}
	}

	if cur <= *failCount {
		return nil, fmt.Errorf("unstable_api simulated failure on attempt %d", cur)
	}

	// on success, cleanup tracker
	attemptTracker.Delete(key)

	// Success: return fetched data stored in state
	fetched := map[string]any{
		"attempts":  cur,
		"timestamp": time.Now().Format(time.RFC3339),
	}
	payload, _ := json.Marshal(fetched)
	return graph.State{
		"fetched_data":             string(payload),
		graph.StateKeyLastResponse: fmt.Sprintf("[fetched] %s", string(payload)),
	}, nil
}

func handleStreaming(ch <-chan *event.Event) error {
	var (
		started bool
	)
	for ev := range ch {
		// Show retry metadata when verbose
		if *verbose && ev.StateDelta != nil {
			if b, ok := ev.StateDelta[graph.MetadataKeyNode]; ok {
				var meta graph.NodeExecutionMetadata
				if err := json.Unmarshal(b, &meta); err == nil {
					if meta.Phase == graph.ExecutionPhaseError && meta.Retrying {
						fmt.Printf("⏳ Retrying node %s: attempt %d/%d, next delay %v\n", meta.NodeID, meta.Attempt, meta.MaxAttempts, meta.NextDelay)
					}
					if meta.Phase == graph.ExecutionPhaseStart && meta.Attempt > 0 && meta.MaxAttempts > 0 {
						fmt.Printf("🚀 Start node %s (attempt %d/%d)\n", meta.NodeID, meta.Attempt, meta.MaxAttempts)
					}
					if meta.Phase == graph.ExecutionPhaseComplete {
						fmt.Printf("✅ Completed node %s\n", meta.NodeID)
					}
				}
			}
		}
		if ev.Error != nil {
			fmt.Printf("❌ Error: %s\n", ev.Error.Message)
			continue
		}
		// Stream model deltas
		if len(ev.Response.Choices) > 0 {
			ch0 := ev.Response.Choices[0]
			if ch0.Delta.Content != "" {
				if !started {
					fmt.Print("🤖 ")
					started = true
				}
				fmt.Print(ch0.Delta.Content)
			}
			if ch0.Delta.Content == "" && started {
				fmt.Println()
				started = false
			}
		}
		if ev.Done {
			break
		}
	}
	return nil
}
