//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a diamond pattern workflow that exposes
// the need for per-node version tracking (versions_seen).
// Without proper versions_seen implementation, the aggregator node
// will execute multiple times instead of once.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	checkpointinmemory "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	// Node names.
	nodeSplitter   = "splitter"
	nodeAnalyzer1  = "analyzer1"
	nodeAnalyzer2  = "analyzer2"
	nodeAggregator = "aggregator"
	nodeFinal      = "final"

	// State keys.
	stateKeyInput      = "input"
	stateKeyAnalysis1  = "analysis1_data"
	stateKeyAnalysis2  = "analysis2_data"
	stateKeyResults    = "results"
	stateKeyExecCounts = "execution_counts"

	// Default values.
	defaultUserID  = "demo-user"
	defaultAppName = "diamond-workflow"
)

// diamondWorkflow manages the diamond pattern workflow.
type diamondWorkflow struct {
	graph      *graph.Graph
	graphAgent agent.Agent
	saver      graph.CheckpointSaver
	runner     runner.Runner

	// Track execution counts to expose the issue.
	executionCounts map[string]int
	executionMutex  sync.Mutex
}

func main() {
	fmt.Println("🔷 Diamond Pattern Workflow Example")
	fmt.Println("Demonstrates per-node version tracking and correct result aggregation.")
	fmt.Println("On resume, versions_seen prevents redundant node executions.")

	// Create and initialize workflow.
	workflow := &diamondWorkflow{
		executionCounts: make(map[string]int),
	}

	if err := workflow.initialize(); err != nil {
		log.Fatalf("Failed to initialize workflow: %v", err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer workflow.runner.Close()

	// Run interactive mode.
	if err := workflow.runInteractive(); err != nil {
		log.Fatalf("Interactive mode failed: %v", err)
	}
}

// initialize sets up the workflow components.
func (w *diamondWorkflow) initialize() error {
	// Create the graph.
	g, err := w.createGraph()
	if err != nil {
		return fmt.Errorf("failed to create graph: %w", err)
	}
	w.graph = g

	// Create checkpoint saver.
	w.saver = checkpointinmemory.NewSaver()

	// Create graph agent with checkpointing.
	w.graphAgent, err = graphagent.New(
		"diamond-agent",
		w.graph,
		graphagent.WithCheckpointSaver(w.saver),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	// Create session service.
	sessionService := sessioninmemory.NewSessionService()

	// Create runner.
	w.runner = runner.NewRunner(
		defaultAppName,
		w.graphAgent,
		runner.WithSessionService(sessionService),
	)

	fmt.Println("✅ Diamond workflow initialized successfully!")
	return nil
}

// createGraph creates the diamond pattern graph.
func (w *diamondWorkflow) createGraph() (*graph.Graph, error) {
	// Create state schema.
	schema := graph.NewStateSchema()

	// Add state fields.
	schema.AddField(stateKeyInput, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
		Default: func() any { return "" },
	})

	schema.AddField(stateKeyAnalysis1, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
		Default: func() any { return "" },
	})

	schema.AddField(stateKeyAnalysis2, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
		Default: func() any { return "" },
	})

	schema.AddField(stateKeyResults, graph.StateField{
		Type:    reflect.TypeOf([]string{}),
		Reducer: graph.StringSliceReducer, // Append results from both analyzers.
		Default: func() any { return []string{} },
	})

	schema.AddField(stateKeyExecCounts, graph.StateField{
		Type:    reflect.TypeOf(map[string]int{}),
		Reducer: graph.DefaultReducer,
		Default: func() any { return map[string]int{} },
	})

	// Create state graph.
	stateGraph := graph.NewStateGraph(schema)

	// Add nodes.
	stateGraph.
		AddNode(nodeSplitter, w.splitterNode).
		AddNode(nodeAnalyzer1, w.analyzer1Node).
		AddNode(nodeAnalyzer2, w.analyzer2Node).
		AddNode(nodeAggregator, w.aggregatorNode).
		AddNode(nodeFinal, w.finalNode).
		SetEntryPoint(nodeSplitter).
		SetFinishPoint(nodeFinal)

	// Add edges for diamond pattern.
	// Splitter fans out to both analyzers.
	stateGraph.AddEdge(nodeSplitter, nodeAnalyzer1)
	stateGraph.AddEdge(nodeSplitter, nodeAnalyzer2)

	// Both analyzers converge to aggregator.
	stateGraph.AddEdge(nodeAnalyzer1, nodeAggregator)
	stateGraph.AddEdge(nodeAnalyzer2, nodeAggregator)

	// Aggregator routes to final only when both results are ready (barrier).
	// Otherwise it does not route further (to End), and will be triggered
	// again when the second analyzer completes.
	condition := func(ctx context.Context, state graph.State) (string, error) {
		results, _ := state[stateKeyResults].([]string)
		if len(results) >= 2 {
			return nodeFinal, nil
		}
		return graph.End, nil
	}
	stateGraph.AddConditionalEdges(nodeAggregator, condition, map[string]string{
		nodeFinal: nodeFinal,
		graph.End: graph.End,
	})

	fmt.Println("📊 Graph structure:")
	fmt.Println("        splitter")
	fmt.Println("        /      \\")
	fmt.Println("   analyzer1  analyzer2")
	fmt.Println("        \\      /")
	fmt.Println("       aggregator")
	fmt.Println("           |")
	fmt.Println("         final")
	fmt.Println()

	return stateGraph.Compile()
}

// Node implementations.

func (w *diamondWorkflow) splitterNode(ctx context.Context, state graph.State) (any, error) {
	execCount := w.recordExecution(nodeSplitter)

	input, _ := state[stateKeyInput].(string)
	if input == "" {
		input = "default-data"
	}

	fmt.Printf("🔄 [%d] SPLITTER: Processing input: %s\n", execCount, input)

	// Split work to both analysis channels.
	return graph.State{
		stateKeyAnalysis1: fmt.Sprintf("A1-%s", input),
		stateKeyAnalysis2: fmt.Sprintf("A2-%s", input),
	}, nil
}

func (w *diamondWorkflow) analyzer1Node(ctx context.Context, state graph.State) (any, error) {
	execCount := w.recordExecution(nodeAnalyzer1)

	data, _ := state[stateKeyAnalysis1].(string)
	fmt.Printf("🔬 [%d] ANALYZER1: Processing: %s\n", execCount, data)

	// Simulate some processing time.
	time.Sleep(100 * time.Millisecond)

	result := fmt.Sprintf("Result1[%s]", data)

	return graph.State{
		stateKeyResults: []string{result},
	}, nil
}

func (w *diamondWorkflow) analyzer2Node(ctx context.Context, state graph.State) (any, error) {
	execCount := w.recordExecution(nodeAnalyzer2)

	data, _ := state[stateKeyAnalysis2].(string)
	fmt.Printf("🔬 [%d] ANALYZER2: Processing: %s\n", execCount, data)

	// Different processing time to ensure analyzers finish at different times.
	time.Sleep(150 * time.Millisecond)

	result := fmt.Sprintf("Result2[%s]", data)

	return graph.State{
		stateKeyResults: []string{result},
	}, nil
}

func (w *diamondWorkflow) aggregatorNode(ctx context.Context, state graph.State) (any, error) {
	execCount := w.recordExecution(nodeAggregator)

	results, _ := state[stateKeyResults].([]string)

	// THIS IS THE KEY ISSUE EXPOSURE.
	fmt.Printf("⚠️  [%d] AGGREGATOR: Processing %d results\n", execCount, len(results))

	if execCount > 1 {
		fmt.Println("❌ Aggregator executed multiple times (likely after resume).")
		fmt.Println("   Per-node versions_seen prevents redundant executions on resume.")
	}

	// Log what we're aggregating.
	for i, result := range results {
		fmt.Printf("   - Result %d: %s\n", i+1, result)
	}

	return graph.State{
		stateKeyExecCounts: w.getExecutionCounts(),
	}, nil
}

func (w *diamondWorkflow) finalNode(ctx context.Context, state graph.State) (any, error) {
	execCount := w.recordExecution(nodeFinal)

	results, _ := state[stateKeyResults].([]string)
	// Read live execution counts to reflect final node's own execution.
	execCounts := w.getExecutionCounts()

	fmt.Printf("\n📊 [%d] FINAL: Workflow Complete\n", execCount)
	fmt.Printf("Results collected: %v\n", results)
	fmt.Printf("\n🔍 Execution Analysis:\n")

	// Analyze execution counts.
	for _, node := range []string{nodeSplitter, nodeAnalyzer1, nodeAnalyzer2, nodeAggregator, nodeFinal} {
		count := execCounts[node]
		expected := 1
		status := "✅"

		if node == nodeAggregator && count > 1 {
			status = "❌"
			fmt.Printf("%s %s: %d executions (expected: %d) - REDUNDANT EXECUTION!\n",
				status, node, count, expected)
		} else {
			fmt.Printf("%s %s: %d execution(s)\n", status, node, count)
		}
	}

	if execCounts[nodeAggregator] > 1 {
		fmt.Println("\n💡 Solution: Implement versions_seen to track per-node channel versions.")
	}

	return nil, nil
}

// Helper methods.

func (w *diamondWorkflow) recordExecution(nodeName string) int {
	w.executionMutex.Lock()
	defer w.executionMutex.Unlock()

	if w.executionCounts == nil {
		w.executionCounts = make(map[string]int)
	}

	w.executionCounts[nodeName]++
	return w.executionCounts[nodeName]
}

func (w *diamondWorkflow) getExecutionCounts() map[string]int {
	w.executionMutex.Lock()
	defer w.executionMutex.Unlock()

	counts := make(map[string]int)
	for k, v := range w.executionCounts {
		counts[k] = v
	}
	return counts
}

func (w *diamondWorkflow) resetExecutionCounts() {
	w.executionMutex.Lock()
	defer w.executionMutex.Unlock()
	w.executionCounts = make(map[string]int)
}

// runInteractive runs the workflow in interactive mode.
func (w *diamondWorkflow) runInteractive() error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("\n📝 Commands:")
	fmt.Println("  run <lid> [input]  - Run with lineage_id and optional input")
	fmt.Println("  resume <lid> [ck]  - Resume latest or specific checkpoint for lineage")
	fmt.Println("  list <lid> [n]     - List latest n checkpoints (default 5)")
	fmt.Println("  reset              - Reset execution counters")
	fmt.Println("  help               - Show this help")
	fmt.Println("  exit/quit          - Exit the program")
	fmt.Println()

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		parts := strings.Fields(input)
		command := strings.ToLower(parts[0])

		switch command {
		case "run":
			if len(parts) < 2 {
				fmt.Println("Usage: run <lineage_id> [input]")
				break
			}
			lineage := parts[1]
			inputData := "test-data"
			if len(parts) > 2 {
				inputData = strings.Join(parts[2:], " ")
			}
			w.resetExecutionCounts()
			if err := w.runWorkflowWithLineage(context.Background(), inputData, lineage); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}

		case "resume":
			if len(parts) < 2 {
				fmt.Println("Usage: resume <lineage_id> [checkpoint_id]")
				break
			}
			lineage := parts[1]
			ckID := ""
			if len(parts) >= 3 {
				ckID = parts[2]
			}
			w.resetExecutionCounts()
			if err := w.resumeWorkflow(context.Background(), lineage, ckID); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}

		case "list":
			if len(parts) < 2 {
				fmt.Println("Usage: list <lineage_id> [n]")
				break
			}
			lineage := parts[1]
			limit := 5
			if len(parts) >= 3 {
				// best-effort parse
				if n, err := fmt.Sscanf(parts[2], "%d", &limit); n == 0 || err != nil {
					limit = 5
				}
			}
			if err := w.listCheckpoints(context.Background(), lineage, limit); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}

		case "reset":
			w.resetExecutionCounts()
			fmt.Println("✅ Execution counters reset")

		case "help":
			fmt.Println("\n📝 Commands:")
			fmt.Println("  run <lid> [input]  - Run with lineage_id and optional input")
			fmt.Println("  resume <lid> [ck]  - Resume latest or specific checkpoint for lineage")
			fmt.Println("  list <lid> [n]     - List latest n checkpoints (default 5)")
			fmt.Println("  reset              - Reset execution counters")
			fmt.Println("  help               - Show this help")
			fmt.Println("  exit/quit          - Exit the program")

		case "exit", "quit":
			fmt.Println("👋 Goodbye!")
			return nil

		default:
			fmt.Printf("❌ Unknown command: %s (type 'help' for commands)\n", command)
		}
	}

	return scanner.Err()
}

// runWorkflow executes the workflow with the given input.
func (w *diamondWorkflow) runWorkflow(ctx context.Context, inputData string) error {
	fmt.Printf("\n🚀 Starting workflow with input: %s\n", inputData)
	startTime := time.Now()

	// Create initial message.
	message := model.NewUserMessage(inputData)

	// Create runtime state with input.
	runtimeState := map[string]any{
		stateKeyInput: inputData,
	}

	// Run the workflow.
	sessionID := fmt.Sprintf("session-%d", time.Now().Unix())
	eventChan, err := w.runner.Run(
		ctx,
		defaultUserID,
		sessionID,
		message,
		agent.WithRuntimeState(runtimeState),
	)
	if err != nil {
		return fmt.Errorf("failed to run workflow: %w", err)
	}

	// Process events.
	for event := range eventChan {
		if event.Error != nil {
			return fmt.Errorf("workflow error: %s", event.Error.Message)
		}
		// Silently consume events for cleaner output.
	}

	duration := time.Since(startTime)
	fmt.Printf("\n⏱️  Execution time: %v\n", duration.Round(time.Millisecond))

	return nil
}

// runWorkflowWithLineage executes the workflow with a fixed lineage_id.
func (w *diamondWorkflow) runWorkflowWithLineage(ctx context.Context, inputData, lineageID string) error {
	fmt.Printf("\n🚀 Starting workflow with input: %s (lineage=%s)\n", inputData, lineageID)
	startTime := time.Now()

	message := model.NewUserMessage(inputData)
	runtimeState := map[string]any{
		stateKeyInput:            inputData,
		graph.CfgKeyLineageID:    lineageID,
		graph.CfgKeyCheckpointNS: "",
	}

	sessionID := fmt.Sprintf("session-%d", time.Now().Unix())
	eventChan, err := w.runner.Run(
		ctx,
		defaultUserID,
		sessionID,
		message,
		agent.WithRuntimeState(runtimeState),
	)
	if err != nil {
		return fmt.Errorf("failed to run workflow: %w", err)
	}

	for event := range eventChan {
		if event.Error != nil {
			return fmt.Errorf("workflow error: %s", event.Error.Message)
		}
	}

	duration := time.Since(startTime)
	fmt.Printf("\n⏱️  Execution time: %v\n", duration.Round(time.Millisecond))
	return nil
}

// resumeWorkflow resumes from latest or specific checkpoint of a lineage.
func (w *diamondWorkflow) resumeWorkflow(ctx context.Context, lineageID, checkpointID string) error {
	label := checkpointID
	if label == "" {
		label = "latest"
	}
	fmt.Printf("\n🔁 Resuming workflow (lineage=%s, checkpoint=%s)\n", lineageID, label)
	startTime := time.Now()

	// For resume, the input message content is not used to reconstruct state;
	// state is restored from checkpoint by the executor.
	message := model.NewUserMessage("resume")

	runtimeState := map[string]any{
		graph.CfgKeyLineageID:    lineageID,
		graph.CfgKeyCheckpointNS: "",
	}
	if checkpointID != "" {
		runtimeState[graph.CfgKeyCheckpointID] = checkpointID
	}

	sessionID := fmt.Sprintf("session-%d", time.Now().Unix())
	eventChan, err := w.runner.Run(
		ctx,
		defaultUserID,
		sessionID,
		message,
		agent.WithRuntimeState(runtimeState),
	)
	if err != nil {
		return fmt.Errorf("failed to resume workflow: %w", err)
	}

	for event := range eventChan {
		if event.Error != nil {
			return fmt.Errorf("workflow error: %s", event.Error.Message)
		}
	}

	duration := time.Since(startTime)
	fmt.Printf("\n⏱️  Execution time: %v\n", duration.Round(time.Millisecond))
	return nil
}

// listCheckpoints lists recent checkpoints for a lineage.
func (w *diamondWorkflow) listCheckpoints(ctx context.Context, lineageID string, limit int) error {
	fmt.Printf("\n🗂️  Checkpoints for lineage %s (latest %d):\n", lineageID, limit)
	cfg := graph.CreateCheckpointConfig(lineageID, "", "")
	tuples, err := w.saver.List(ctx, cfg, &graph.CheckpointFilter{Limit: limit})
	if err != nil {
		return fmt.Errorf("failed to list checkpoints: %w", err)
	}
	if len(tuples) == 0 {
		fmt.Println("(none)")
		return nil
	}
	for i, t := range tuples {
		step := -1
		if t.Metadata != nil {
			step = t.Metadata.Step
		}
		ts := t.Checkpoint.Timestamp.Local().Format(time.RFC3339)
		fmt.Printf("%2d. %s step=%d time=%s next=%v\n", i+1, t.Checkpoint.ID, step, ts, t.Checkpoint.NextNodes)
	}
	return nil
}
