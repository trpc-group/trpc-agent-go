//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates node-level caching in a graph using Runner + GraphAgent
// with interactive input and streaming outputs.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	ttlSeconds = flag.Int("ttl", 60, "Cache TTL (seconds) for the compute node")
)

func main() {
	flag.Parse()
	fmt.Printf("🚀 Node Cache Example (Runner + GraphAgent)\n")
	fmt.Printf("Cache TTL: %ds\n", *ttlSeconds)
	fmt.Println(strings.Repeat("=", 50))

	if err := run(); err != nil {
		log.Fatalf("example failed: %v", err)
	}
}

func run() error {
	ctx := context.Background()

	// Build a simple graph with a cached compute node.
	g, err := buildGraph(time.Duration(*ttlSeconds) * time.Second)
	if err != nil {
		return err
	}

	// Create GraphAgent.
	ga, err := graphagent.New(
		"node-cache-agent",
		g,
		graphagent.WithDescription("Demonstrates per-node caching with TTL"),
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		return fmt.Errorf("create graph agent: %w", err)
	}

	// Runner with in-memory (RAM) session service.
	r := runner.NewRunner(
		"node-cache-example",
		ga,
		runner.WithSessionService(inmemory.NewSessionService()),
	)

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

	// Interactive loop: user enters a number; we run the graph with that input.
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("💡 Enter an integer (type 'exit' to quit). Repeated inputs should hit the cache and skip compute.")
	for {
		fmt.Print("👤 Enter integer: ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if strings.EqualFold(line, "exit") {
			fmt.Println("👋 Bye!")
			break
		}
		if line == "" {
			continue
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			fmt.Printf("⚠️ Please enter a valid integer: %v\n", err)
			continue
		}

		// Runner.Run signature: <userID, sessionID, message, options...>.
		userID := "user"
		// To demonstrate cache hits deterministically, use a new session per run
		// so previous outputs do not pollute the cache key via state. The cache
		// itself is graph-level so it will still hit across sessions.
		sessionID := fmt.Sprintf("nodecache-%d", time.Now().UnixNano())
		msg := model.NewUserMessage(fmt.Sprintf("compute %d", n))
		evts, err := r.Run(
			ctx,
			userID,
			sessionID,
			msg,
			agent.WithRequestID(fmt.Sprintf("req-%d", time.Now().UnixNano())),
			agent.WithRuntimeState(map[string]any{"n": n}),
		)
		if err != nil {
			fmt.Printf("Run failed: %v\n", err)
			continue
		}

		// Stream events
		var final graph.State
		for e := range evts {
			if e.Response != nil {
				switch e.Response.Object {
				case graph.ObjectTypeGraphNodeStart:
					fmt.Printf("🟡 Node start: %s\n", e.Author)
				case graph.ObjectTypeGraphNodeComplete:
					if e.StateDelta != nil {
						if _, ok := e.StateDelta[graph.MetadataKeyCacheHit]; ok {
							fmt.Println("✅ Cache hit: skipped node function")
						}
					}
				}
			}
			if e.Done {
				if e.StateDelta != nil {
					final = decodeStateDelta(e.StateDelta)
				}
			}
		}
		if final != nil {
			fmt.Printf("📦 Final result: input=%d, output=%v\n", n, final["out"])
		}
		fmt.Println()
	}

	return nil
}

func buildGraph(ttl time.Duration) (*graph.Graph, error) {
	// Schema: input n:int, output out:int
	schema := graph.NewStateSchema().
		AddField("n", graph.StateField{Type: reflect.TypeOf(0), Reducer: graph.DefaultReducer}).
		AddField("out", graph.StateField{Type: reflect.TypeOf(0), Reducer: graph.DefaultReducer})

	// Slow compute node: simulates expensive work, doubles input.
	compute := func(ctx context.Context, st graph.State) (any, error) {
		// Simulate expensive computation
		time.Sleep(300 * time.Millisecond)
		n := st["n"].(int)
		return graph.State{"out": n * 2}, nil
	}

	// Build graph with cache backend and policy
	sg := graph.NewStateGraph(schema).
		WithCache(graph.NewInMemoryCache()).
		WithCachePolicy(graph.DefaultCachePolicy())

	if ttl > 0 {
		// Use a node-level policy with TTL and a simple field-based cache key.
		pol := &graph.CachePolicy{KeyFunc: graph.DefaultCachePolicy().KeyFunc, TTL: ttl}
		sg.AddNode("compute", compute, graph.WithNodeCachePolicy(pol), graph.WithCacheKeyFields("n"))
	} else {
		sg.AddNode("compute", compute)
	}
	sg.SetEntryPoint("compute").
		SetFinishPoint("compute")

	return sg.Compile()
}

func decodeStateDelta(delta map[string][]byte) graph.State {
	out := make(graph.State)
	for k, v := range delta {
		switch k {
		case graph.MetadataKeyNode, graph.MetadataKeyPregel, graph.MetadataKeyChannel, graph.MetadataKeyState, graph.MetadataKeyCompletion:
			continue
		}
		var anyv any
		_ = json.Unmarshal(v, &anyv)
		out[k] = anyv
	}
	return out
}
