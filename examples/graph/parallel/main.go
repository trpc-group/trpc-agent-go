//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a parallel execution workflow using the graph package.
// This example shows how to build and execute graphs with multiple edges from the same node,
// parallel node execution, and conditional routing.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	// Default model name for deepseek-chat.
	defaultModelName = "deepseek-chat"
)

var (
	modelName = flag.String("model", defaultModelName,
		"Name of the model to use")
	verbose     = flag.Bool("verbose", false, "Enable verbose logging for all nodes")
	stream      = flag.Bool("stream", true, "Fake streaming output at completion")
	streamDelay = flag.Duration("stream-delay", 30*time.Millisecond, "Delay per chunk for fake stream output")
	streamChunk = flag.Int("stream-chunk", 8, "Chunk size (runes) per print in fake stream output")
)

func main() {
	// Parse command line flags.
	flag.Parse()
	fmt.Printf("🚀 Parallel Execution Workflow Example\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Println(strings.Repeat("=", 50))
	// Create and run the workflow.
	workflow := &parallelWorkflow{
		modelName:   *modelName,
		verbose:     *verbose,
		fakeStream:  *stream,
		streamDelay: *streamDelay,
		streamChunk: *streamChunk,
	}
	if err := workflow.run(); err != nil {
		log.Fatalf("Workflow failed: %v", err)
	}
}

// parallelWorkflow manages the parallel execution workflow.
type parallelWorkflow struct {
	modelName   string
	runner      runner.Runner
	userID      string
	sessionID   string
	verbose     bool
	fakeStream  bool
	streamDelay time.Duration
	streamChunk int
}

// run starts the parallel execution workflow.
func (w *parallelWorkflow) run() error {
	ctx := context.Background()
	// Setup the workflow.
	if err := w.setup(); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer w.runner.Close()

	return w.startInteractiveMode(ctx)
}

// setup creates the graph agent and runner.
func (w *parallelWorkflow) setup() error {
	// Create the parallel execution graph.
	workflowGraph, err := w.createParallelExecutionGraph()
	if err != nil {
		return fmt.Errorf("failed to create graph: %w", err)
	}

	// Create GraphAgent from the compiled graph.
	graphAgent, err := graphagent.New("parallel-processor", workflowGraph,
		graphagent.WithDescription("Parallel execution workflow with multiple edges"),
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	// Create session service.
	sessionService := inmemory.NewSessionService()

	// Create runner with the graph agent.
	appName := "parallel-workflow"
	w.runner = runner.NewRunner(
		appName,
		graphAgent,
		runner.WithSessionService(sessionService),
	)

	// Setup identifiers.
	w.userID = "user"
	w.sessionID = fmt.Sprintf("parallel-session-%d", time.Now().Unix())

	fmt.Printf("✅ Parallel workflow ready! Session: %s\n\n", w.sessionID)
	return nil
}

const (
	stateKeyInputText      = "input_text"
	stateKeyAnalysisResult = "analysis_result"
	stateKeySummaryResult  = "summary_result"
	stateKeyEnhanceResult  = "enhance_result"
	stateKeyFinalResult    = "final_result"
	stateKeyParallelNodes  = "parallel_nodes"
	stateKeyExecutionOrder = "execution_order"
)

// createParallelExecutionGraph creates a parallel execution workflow graph.
func (w *parallelWorkflow) createParallelExecutionGraph() (*graph.Graph, error) {
	// Create extended state schema for messages and metadata.
	schema := graph.MessagesStateSchema()

	// Create model instance.
	modelInstance := openai.New(*modelName)

	// Create analysis tools.
	analyzeTool := function.NewFunctionTool(
		w.analyzeText,
		function.WithName("analyze_text"),
		function.WithDescription("Analyzes text content and provides insights"),
	)

	// Create node callbacks for monitoring and performance tracking.
	callbacks := w.createNodeCallbacks()

	// Create stateGraph with schema and callbacks.
	stateGraph := graph.NewStateGraph(schema).WithNodeCallbacks(callbacks)
	tools := map[string]tool.Tool{
		"analyze_text": analyzeTool,
	}

	// Build the workflow graph.
	stateGraph.
		// Add input preprocessing node.
		AddNode("preprocess", w.preprocessInput).

		// Add LLM analyzer node.
		AddLLMNode("analyze", modelInstance,
			`You are a text analysis expert. Analyze the provided text and provide insights about:
1. Content type and genre
2. Key themes and topics
3. Writing style and tone
4. Complexity level
5. Target audience

Use the analyze_text tool to get additional insights, then provide your comprehensive analysis.
Respond with a structured analysis in JSON format.`,
			tools).
		AddToolsNode("tools", tools).

		// Add routing node to distribute to parallel nodes.
		AddNode("route_to_parallel", w.routeToParallel).

		// Add parallel processing nodes.
		AddLLMNode("summarize", modelInstance,
			`You are a summarization expert. Create a concise summary of the provided text.
Focus on:
1. Main points and key ideas
2. Important details
3. Logical structure
4. Core message

Provide a well-structured summary that captures the essence of the text.
Remember: only output the final result itself, no other text.`,
			map[string]tool.Tool{}).
		AddLLMNode("enhance", modelInstance,
			`You are a content enhancement expert. Improve the provided text by:
1. Enhancing clarity and readability
2. Improving structure and flow
3. Adding relevant details where appropriate
4. Ensuring consistency and coherence

Focus on making the content more engaging and professional while preserving the original meaning.
Remember: only output the final result itself, no other text.`,
			map[string]tool.Tool{}).
		AddLLMNode("classify", modelInstance,
			`You are a content classification expert. Classify the provided text by:
1. Content type (article, story, technical, etc.)
2. Genre or category
3. Complexity level (simple, moderate, complex)
4. Target audience (general, technical, academic, etc.)
5. Purpose (informative, persuasive, entertaining, etc.)

Provide a clear classification with reasoning.
Remember: only output the final result itself, no other text.`,
			map[string]tool.Tool{}).

		// Add final aggregation node.
		AddNode("aggregate", w.aggregateResults).

		// Add final formatting.
		AddNode("format_output", w.formatOutput).

		// Set up the workflow routing.
		SetEntryPoint("preprocess").
		SetFinishPoint("format_output")

	// Add workflow edges - this is where we test multiple edges from the same node.
	stateGraph.AddEdge("preprocess", "analyze")
	stateGraph.AddToolsConditionalEdges("analyze", "tools", "route_to_parallel")
	stateGraph.AddEdge("tools", "analyze")

	// Add multiple edges from the routing node to parallel nodes.
	stateGraph.AddEdge("route_to_parallel", "summarize")
	stateGraph.AddEdge("route_to_parallel", "enhance")
	stateGraph.AddEdge("route_to_parallel", "classify")

	// Add edges from parallel nodes to aggregation.
	stateGraph.AddEdge("summarize", "aggregate")
	stateGraph.AddEdge("enhance", "aggregate")
	stateGraph.AddEdge("classify", "aggregate")

	// Add final edge to output formatting.
	stateGraph.AddEdge("aggregate", "format_output")

	// Build and return the graph.
	return stateGraph.Compile()
}

// createNodeCallbacks creates callbacks for performance tracking without verbose logging.
func (w *parallelWorkflow) createNodeCallbacks() *graph.NodeCallbacks {
	callbacks := graph.NewNodeCallbacks()

	// Before node callback: Track performance and metadata silently.
	callbacks.RegisterBeforeNode(func(
		ctx context.Context,
		callbackCtx *graph.NodeCallbackContext,
		state graph.State,
	) (any, error) {
		// Track execution start time in state for performance monitoring.
		if state["node_timings"] == nil {
			state["node_timings"] = make(map[string]time.Time)
		}
		timings := state["node_timings"].(map[string]time.Time)
		timings[callbackCtx.NodeID] = time.Now()

		// Add node metadata to state for tracking.
		if state["node_execution_history"] == nil {
			state["node_execution_history"] = make([]map[string]any, 0)
		}
		history := state["node_execution_history"].([]map[string]any)
		history = append(history, map[string]any{
			"node_id":       callbackCtx.NodeID,
			"node_name":     callbackCtx.NodeName,
			"node_type":     callbackCtx.NodeType,
			"step_number":   callbackCtx.StepNumber,
			"start_time":    time.Now(),
			"invocation_id": callbackCtx.InvocationID,
		})
		state["node_execution_history"] = history

		// Track parallel execution order.
		if state[stateKeyExecutionOrder] == nil {
			state[stateKeyExecutionOrder] = make([]string, 0)
		}
		execOrder := state[stateKeyExecutionOrder].([]string)
		execOrder = append(execOrder, callbackCtx.NodeID)
		state[stateKeyExecutionOrder] = execOrder

		return nil, nil // Continue with normal execution.
	})

	// After node callback: Track completion silently.
	callbacks.RegisterAfterNode(func(
		ctx context.Context,
		callbackCtx *graph.NodeCallbackContext,
		state graph.State,
		result any,
		nodeErr error,
	) (any, error) {
		// Calculate execution time.
		var executionTime time.Duration
		if timings, ok := state["node_timings"].(map[string]time.Time); ok {
			if startTime, exists := timings[callbackCtx.NodeID]; exists {
				executionTime = time.Since(startTime)
			}
		}

		// Update execution history with completion info.
		if history, ok := state["node_execution_history"].([]map[string]any); ok && len(history) > 0 {
			lastEntry := history[len(history)-1]
			lastEntry["end_time"] = time.Now()
			lastEntry["execution_time"] = executionTime
			lastEntry["success"] = nodeErr == nil
			if nodeErr != nil {
				lastEntry["error"] = nodeErr.Error()
			}
		}

		// Add execution metadata to result if it's a State.
		if result != nil && nodeErr == nil {
			if stateResult, ok := result.(graph.State); ok {
				stateResult["last_executed_node"] = callbackCtx.NodeID
				stateResult["last_execution_time"] = executionTime
				stateResult["total_nodes_executed"] = len(state["node_execution_history"].([]map[string]any))
				return stateResult, nil
			}
		}

		return result, nodeErr
	})
	// Error callback: Handle node execution errors silently.
	callbacks.RegisterOnNodeError(func(
		ctx context.Context,
		callbackCtx *graph.NodeCallbackContext,
		state graph.State,
		err error,
	) {
		// Silent error handling - errors will be shown in node execution logs
	})
	return callbacks
}

// preprocessInput prepares the input text for processing.
func (w *parallelWorkflow) preprocessInput(ctx context.Context, state graph.State) (any, error) {
	// Get input text from state.
	inputText, exists := state[stateKeyInputText]
	if !exists {
		return nil, errors.New("no input text provided")
	}

	text, ok := inputText.(string)
	if !ok {
		return nil, errors.New("input text is not a string")
	}

	// Basic preprocessing.
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("input text is empty after preprocessing")
	}

	return graph.State{
		stateKeyInputText:        text,
		"preprocessing_complete": true,
	}, nil
}

// Type definitions for tool functions.
type analyzeTextArgs struct {
	Text string `json:"text" description:"Text to analyze"`
}

type analyzeTextResult struct {
	WordCount     int     `json:"word_count"`
	CharCount     int     `json:"char_count"`
	SentenceCount int     `json:"sentence_count"`
	AvgWordLength float64 `json:"avg_word_length"`
	Complexity    string  `json:"complexity"`
}

// analyzeText is a tool function that analyzes text content.
func (w *parallelWorkflow) analyzeText(ctx context.Context, args analyzeTextArgs) (analyzeTextResult, error) {
	if args.Text == "" {
		return analyzeTextResult{}, errors.New("text parameter is required")
	}

	// Perform analysis.
	wordCount := len(strings.Fields(args.Text))
	charCount := len(args.Text)
	sentenceCount := len(strings.Split(args.Text, "."))

	// Calculate average word length.
	var avgWordLength float64
	words := strings.Fields(args.Text)
	if len(words) > 0 {
		total := 0
		for _, word := range words {
			total += len(word)
		}
		avgWordLength = float64(total) / float64(len(words))
	}

	// Determine complexity.
	var complexity string
	if wordCount < 50 {
		complexity = "simple"
	} else if wordCount < 200 {
		complexity = "moderate"
	} else {
		complexity = "complex"
	}

	result := analyzeTextResult{
		WordCount:     wordCount,
		CharCount:     charCount,
		SentenceCount: sentenceCount,
		AvgWordLength: avgWordLength,
		Complexity:    complexity,
	}

	return result, nil
}

// routeToParallel routes the workflow to parallel processing nodes.
func (w *parallelWorkflow) routeToParallel(ctx context.Context, state graph.State) (any, error) {
	// Verify analysis result exists - LLM nodes store results in StateKeyLastResponse.
	if _, exists := state[graph.StateKeyLastResponse]; !exists {
		return nil, errors.New("no analysis result found")
	}

	// Track parallel nodes in state.
	parallelNodes := []string{"summarize", "enhance", "classify"}
	state[stateKeyParallelNodes] = parallelNodes

	fmt.Printf("🔄 Routing to parallel nodes: [%s]\n", strings.Join(parallelNodes, ", "))

	return graph.State{
		stateKeyParallelNodes: parallelNodes,
		"routing_complete":    true,
	}, nil
}

// aggregateResults aggregates results from parallel nodes.
func (w *parallelWorkflow) aggregateResults(ctx context.Context, state graph.State) (any, error) {
	// Prefer standard per-node responses map populated by LLM nodes.
	var results map[string]any
	if nr, ok := state[graph.StateKeyNodeResponses].(map[string]any); ok && nr != nil {
		results = nr
	} else {
		// Fallback: collect legacy keys if present.
		results = make(map[string]any)
		if v, ok := state["summarize_result"]; ok {
			results["summarize"] = v
		}
		if v, ok := state["enhance_result"]; ok {
			results["enhance"] = v
		}
		if v, ok := state["classify_result"]; ok {
			results["classify"] = v
		}
	}

	// Count only known parallel nodes to form the summary.
	count := 0
	for _, k := range []string{"summarize", "enhance", "classify"} {
		if _, ok := results[k]; ok {
			count++
		}
	}

	// Set a concise final status; detailed printing is handled in streaming completion.
	aggregatedResult := fmt.Sprintf("Parallel execution completed: %d results aggregated", count)
	return graph.State{
		graph.StateKeyLastResponse: aggregatedResult,
		// Preserve node_responses as-is for later consumption.
		graph.StateKeyNodeResponses: results,
	}, nil
}

// formatOutput formats the final output for display.
func (w *parallelWorkflow) formatOutput(ctx context.Context, state graph.State) (any, error) {
	// Get the aggregated result from the last response.
	if lastResponse, exists := state[graph.StateKeyLastResponse]; exists {
		if responseStr, ok := lastResponse.(string); ok {
			return graph.State{
				graph.StateKeyLastResponse: responseStr,
			}, nil
		}
	}

	return nil, errors.New("no final result found")
}

// startInteractiveMode starts the interactive command-line interface.
func (w *parallelWorkflow) startInteractiveMode(ctx context.Context) error {
	fmt.Printf("💡 Interactive Parallel Processing Mode\n")
	fmt.Printf("   Enter your text content (or 'exit' to quit)\n")
	fmt.Printf("   Type 'help' for available commands\n\n")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Printf("📄 Text: ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		if input == "exit" {
			fmt.Println("👋 Goodbye!")
			break
		}

		if input == "help" {
			w.showHelp()
			continue
		}

		// Process the input.
		if err := w.processInput(ctx, input); err != nil {
			fmt.Printf("❌ Processing failed: %v\n\n", err)
		}
	}

	return scanner.Err()
}

// showHelp displays available commands.
func (w *parallelWorkflow) showHelp() {
	fmt.Println("\n📚 Available Commands:")
	fmt.Println("  <text>     - Process the given text through parallel workflow")
	fmt.Println("  help       - Show this help message")
	fmt.Println("  exit       - Exit the application")
	fmt.Println()
}

// processInput processes a single input through the workflow.
func (w *parallelWorkflow) processInput(ctx context.Context, input string) error {
	fmt.Printf("\n🔄 Processing input: %s\n", truncateString(input, 50))
	fmt.Println(strings.Repeat("-", 60))

	// Create user message.
	message := model.NewUserMessage(input)

	// Run the workflow through the runner.
	eventChan, err := w.runner.Run(
		ctx,
		w.userID,
		w.sessionID,
		message,
		// Set runtime state for each run.
		agent.WithRuntimeState(map[string]any{
			"user_id":         w.userID,
			stateKeyInputText: input,
		}),
	)
	if err != nil {
		return fmt.Errorf("failed to run workflow: %w", err)
	}

	// Process streaming response.
	return w.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming workflow response.
func (w *parallelWorkflow) processStreamingResponse(eventChan <-chan *event.Event) error {
	var (
		parallelNodes = make(map[string]bool)
		// Track completion status for each parallel node
		parallelCompleted = map[string]bool{
			"summarize": false,
			"enhance":   false,
			"classify":  false,
		}
		allParallelNodesComplete bool
	)

	for event := range eventChan {
		// Handle node events for parallel execution tracking.
		if event.Author == graph.AuthorGraphNode && event.StateDelta != nil {
			if nodeData, exists := event.StateDelta[graph.MetadataKeyNode]; exists {
				var nodeMetadata graph.NodeExecutionMetadata
				if err := json.Unmarshal(nodeData, &nodeMetadata); err == nil {
					nodeID := nodeMetadata.NodeID
					isParallel := nodeID == "summarize" || nodeID == "enhance" || nodeID == "classify"
					// Print all nodes when verbose; otherwise only the parallel trio.
					if w.verbose || isParallel {
						switch nodeMetadata.Phase {
						case "start":
							if w.verbose {
								fmt.Printf("   🚀 %s: Starting (%s)\n", nodeID, nodeMetadata.NodeType)
							} else {
								fmt.Printf("   🚀 %s: Starting\n", nodeID)
							}
							if isParallel {
								parallelNodes[nodeID] = true
							}
						case "complete":
							if w.verbose {
								fmt.Printf("   ✅ %s: Completed (%s)\n", nodeID, nodeMetadata.NodeType)
							} else {
								fmt.Printf("   ✅ %s: Completed\n", nodeID)
							}
							if isParallel {
								parallelCompleted[nodeID] = true
							}
						}
					}
				}
			}
		}

		// Check if all parallel nodes are complete
		if !allParallelNodesComplete {
			allComplete := true
			for _, completed := range parallelCompleted {
				if !completed {
					allComplete = false
					break
				}
			}
			if allComplete {
				allParallelNodesComplete = true
				fmt.Println("🔄 All parallel nodes completed")
				fmt.Println()
				// Don't display results here - let the aggregate function handle it
			}
		}

		// On graph completion event, print aggregated results from final state snapshot.
		if event.StateDelta != nil {
			if _, isCompletion := event.StateDelta[graph.MetadataKeyCompletion]; isCompletion {
				// Extract node_responses and show in a stable, user-friendly order.
				var nodeResponses map[string]any
				if data, ok := event.StateDelta[graph.StateKeyNodeResponses]; ok && data != nil {
					_ = json.Unmarshal(data, &nodeResponses)
				}
				// Fallback: try legacy keys if node_responses missing.
				if nodeResponses == nil {
					nodeResponses = make(map[string]any)
					if data, ok := event.StateDelta["summarize_result"]; ok && data != nil {
						var s any
						_ = json.Unmarshal(data, &s)
						nodeResponses["summarize"] = s
					}
					if data, ok := event.StateDelta["enhance_result"]; ok && data != nil {
						var s any
						_ = json.Unmarshal(data, &s)
						nodeResponses["enhance"] = s
					}
					if data, ok := event.StateDelta["classify_result"]; ok && data != nil {
						var s any
						_ = json.Unmarshal(data, &s)
						nodeResponses["classify"] = s
					}
				}

				// Print results if any available.
				if len(nodeResponses) > 0 {
					fmt.Println("📋 Parallel Execution Results:")
					fmt.Println("============================================================")
					fmt.Println()

					// Preferred display order
					order := []string{"summarize", "enhance", "classify"}
					seen := map[string]bool{}
					for _, k := range order {
						if v, ok := nodeResponses[k]; ok {
							fmt.Printf("🔹 %s:\n", strings.Title(k))
							w.printNodeContent(v)
							fmt.Println("----------------------------------------")
							fmt.Println()
							seen[k] = true
						}
					}
					// Print any additional nodes not in the preferred order.
					for k, v := range nodeResponses {
						if seen[k] {
							continue
						}
						fmt.Printf("🔹 %s:\n", strings.Title(k))
						w.printNodeContent(v)
						fmt.Println("----------------------------------------")
						fmt.Println()
					}
				}
			}
		}
	}

	return nil
}

// truncateString truncates a string to the specified length.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// printNodeContent prints content either directly or via fake streaming depending on flags.
func (w *parallelWorkflow) printNodeContent(v any) {
	var content string
	switch t := v.(type) {
	case string:
		content = t
	default:
		content = fmt.Sprintf("%v", t)
	}
	if !w.fakeStream {
		fmt.Println(content)
		return
	}
	w.fakeStreamText(content)
}

// fakeStreamText prints text in chunks with small delays to simulate streaming.
func (w *parallelWorkflow) fakeStreamText(s string) {
	if w.streamChunk <= 0 {
		w.streamChunk = 8
	}
	runes := []rune(s)
	for i := 0; i < len(runes); i += w.streamChunk {
		end := i + w.streamChunk
		if end > len(runes) {
			end = len(runes)
		}
		fmt.Print(string(runes[i:end]))
		time.Sleep(w.streamDelay)
	}
	fmt.Println()
}
