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
	"errors"
	"flag"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

const (
	engineBSP  = "bsp"
	engineDAG  = "dag"
	engineBoth = "both"
)

const (
	nodeSplit   = "split"
	nodeSlowA   = "slow_a"
	nodeFastB   = "fast_b"
	nodeMidC    = "mid_c"
	nodeFastNxt = "fast_b_next"
)

const (
	slowDuration = 800 * time.Millisecond
	fastDuration = 200 * time.Millisecond
	midDuration  = 400 * time.Millisecond
	nextDuration = 120 * time.Millisecond
)

func main() {
	var engine string
	flag.StringVar(
		&engine,
		"engine",
		engineBSP,
		"Execution engine: bsp|dag|both",
	)
	flag.Parse()

	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine != engineBSP && engine != engineDAG && engine != engineBoth {
		panic(fmt.Errorf("unknown engine %q", engine))
	}

	switch engine {
	case engineBoth:
		run(engineBSP)
		fmt.Println(strings.Repeat("-", 60))
		run(engineDAG)
	default:
		run(engine)
	}
}

func run(engine string) {
	start := time.Now()
	l := newLogger(start)

	g := buildGraph(l)

	execEngine := graph.ExecutionEngineBSP
	if engine == engineDAG {
		execEngine = graph.ExecutionEngineDAG
	}

	exec, err := graph.NewExecutor(
		g,
		graph.WithExecutionEngine(execEngine),
		graph.WithMaxConcurrency(3),
	)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inv := agent.NewInvocation(agent.WithInvocationID("dag-engine-example"))
	evts, err := exec.Execute(ctx, graph.State{}, inv)
	if err != nil {
		panic(err)
	}

	l.Printf("engine=%s start", engine)
	runErr := drainEvents(evts)
	if runErr != nil {
		panic(runErr)
	}
	l.Printf("engine=%s done", engine)
}

func buildGraph(l *logger) *graph.Graph {
	schema := graph.NewStateSchema()
	sg := graph.NewStateGraph(schema)

	sg.AddNode(nodeSplit, func(ctx context.Context, state graph.State) (any,
		error) {
		l.Printf("%s start", nodeSplit)
		l.Printf("%s end", nodeSplit)
		return nil, nil
	})

	sg.AddNode(nodeSlowA, func(ctx context.Context, state graph.State) (any,
		error) {
		l.Printf("%s start", nodeSlowA)
		time.Sleep(slowDuration)
		l.Printf("%s end", nodeSlowA)
		return nil, nil
	})

	sg.AddNode(nodeFastB, func(ctx context.Context, state graph.State) (any,
		error) {
		l.Printf("%s start", nodeFastB)
		time.Sleep(fastDuration)
		l.Printf("%s end", nodeFastB)
		return nil, nil
	})

	sg.AddNode(nodeMidC, func(ctx context.Context, state graph.State) (any,
		error) {
		l.Printf("%s start", nodeMidC)
		time.Sleep(midDuration)
		l.Printf("%s end", nodeMidC)
		return nil, nil
	})

	sg.AddNode(nodeFastNxt, func(ctx context.Context, state graph.State) (any,
		error) {
		l.Printf("%s start", nodeFastNxt)
		time.Sleep(nextDuration)
		l.Printf("%s end", nodeFastNxt)
		return nil, nil
	})

	sg.SetEntryPoint(nodeSplit)
	sg.AddEdge(nodeSplit, nodeSlowA)
	sg.AddEdge(nodeSplit, nodeFastB)
	sg.AddEdge(nodeSplit, nodeMidC)
	sg.AddEdge(nodeFastB, nodeFastNxt)

	g, err := sg.Compile()
	if err != nil {
		panic(err)
	}
	return g
}

func drainEvents(evts <-chan *event.Event) error {
	for e := range evts {
		if e == nil || e.Error == nil {
			continue
		}
		return errors.New(e.Error.Message)
	}
	return nil
}

type logger struct {
	start time.Time
	mu    sync.Mutex
}

func newLogger(start time.Time) *logger {
	return &logger{start: start}
}

func (l *logger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	elapsed := time.Since(l.start).Truncate(time.Millisecond)
	fmt.Printf("[%6s] ", elapsed)
	fmt.Printf(format, args...)
	fmt.Println()
}
