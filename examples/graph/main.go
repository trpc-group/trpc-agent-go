//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a document processing workflow using the graph package.
// This example shows how to build and execute graphs with conditional routing,
// LLM nodes, and function nodes.
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
)

func main() {
	// Parse command line flags.
	flag.Parse()
	fmt.Printf("🚀 Document Processing Workflow Example\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Println(strings.Repeat("=", 50))
	// Create and run the workflow.
	workflow := &documentWorkflow{
		modelName: *modelName,
	}
	if err := workflow.run(); err != nil {
		log.Fatalf("Workflow failed: %v", err)
	}
}

// documentWorkflow manages the document processing workflow.
type documentWorkflow struct {
	modelName string
	runner    runner.Runner
	userID    string
	sessionID string
}

// run starts the document processing workflow.
func (w *documentWorkflow) run() error {
	ctx := context.Background()
	// Setup the workflow.
	if err := w.setup(); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	return w.startInteractiveMode(ctx)
}

// setup creates the graph agent and runner.
func (w *documentWorkflow) setup() error {
	// Create the document processing graph.
	workflowGraph, err := w.createDocumentProcessingGraph()
	if err != nil {
		return fmt.Errorf("failed to create graph: %w", err)
	}

	// Create GraphAgent from the compiled graph.
	graphAgent, err := graphagent.New("document-processor", workflowGraph,
		graphagent.WithDescription("Comprehensive document processing workflow"),
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	// Create session service.
	sessionService := inmemory.NewSessionService()

	// Create runner with the graph agent.
	appName := "document-workflow"
	w.runner = runner.NewRunner(
		appName,
		graphAgent,
		runner.WithSessionService(sessionService),
	)

	// Setup identifiers.
	w.userID = "user"
	w.sessionID = fmt.Sprintf("workflow-session-%d", time.Now().Unix())

	fmt.Printf("✅ Document workflow ready! Session: %s\n\n", w.sessionID)
	return nil
}

const (
	complexitySimple   = "simple"
	complexityModerate = "moderate"
	complexityComplex  = "complex"
)

const (
	stateKeyDocumentLength  = "document_length"
	stateKeyWordCount       = "word_count"
	stateKeyComplexityLevel = "complexity_level"
	stateKeyProcessingStage = "processing_stage"
)

// createDocumentProcessingGraph creates a document processing workflow graph.
func (w *documentWorkflow) createDocumentProcessingGraph() (*graph.Graph, error) {
	// Create extended state schema for messages and metadata.
	schema := graph.MessagesStateSchema()

	// Create model instance.
	modelInstance := openai.New(*modelName)

	// Create analysis tools.
	complexityTool := function.NewFunctionTool(
		w.analyzeComplexity,
		function.WithName("analyze_complexity"),
		function.WithDescription("Analyzes document complexity level"),
	)

	// Create node callbacks for monitoring and performance tracking.
	callbacks := w.createNodeCallbacks()

	// Create stateGraph with schema and callbacks.
	stateGraph := graph.NewStateGraph(schema).WithNodeCallbacks(callbacks)
	tools := map[string]tool.Tool{
		"analyze_complexity": complexityTool,
	}

	// Build the workflow graph.
	stateGraph.
		// Add preprocessing node.
		AddNode("preprocess", w.preprocessDocument).

		// Add LLM analyzer node.
		AddLLMNode("analyze", modelInstance,
			`You are a document analysis expert. You MUST use the analyze_complexity tool to analyze the provided document.

IMPORTANT: You are REQUIRED to call the analyze_complexity tool with the document text as input. 
Do not provide your own analysis without using the tool first.

Steps:
1. Call the analyze_complexity tool with the document text
2. Based on the tool's analysis, provide your final complexity assessment
3. Respond with only the complexity level: "simple", "moderate", or "complex"

You MUST use the tool - this is not optional.`,
			tools).
		AddToolsNode("tools", tools).

		// Add complexity routing.
		AddNode("route_complexity", w.routeComplexity).

		// Add LLM summarizer node for complex documents.
		AddLLMNode("summarize", modelInstance,
			`You are a document summarization expert. Create a comprehensive yet concise summary of the provided document.
Focus on:
1. Key points and main arguments
2. Important details and insights
3. Logical structure and flow
4. Conclusions and implications
Provide a well-structured summary that preserves the essential information.
Remember: only output the final result itself, no other text.`,
			map[string]tool.Tool{}).

		// Add LLM enhancer for low-quality content.
		AddLLMNode("enhance", modelInstance,
			`You are a content enhancement expert. Improve the provided content by:
1. Enhancing clarity and readability
2. Improving structure and organization
3. Adding relevant details where appropriate
4. Ensuring consistency and coherence
Focus on making the content more engaging and professional while preserving the original meaning.
Remember: only output the final result itself, no other text.`,
			map[string]tool.Tool{}).

		// Add final formatting.
		AddNode("format_output", w.formatOutput).

		// Set up the workflow routing.
		SetEntryPoint("preprocess").
		SetFinishPoint("format_output")

	// Add workflow edges.
	stateGraph.AddEdge("preprocess", "analyze")
	stateGraph.AddToolsConditionalEdges("analyze", "tools", "route_complexity")
	stateGraph.AddEdge("tools", "analyze")

	// Add conditional routing for complexity.
	stateGraph.AddConditionalEdges("route_complexity", w.complexityCondition, map[string]string{
		complexitySimple:   "enhance",
		complexityModerate: "enhance", // Moderate documents also go to enhance
		complexityComplex:  "summarize",
	})

	stateGraph.AddEdge("enhance", "format_output")
	stateGraph.AddEdge("summarize", "format_output")

	// Build and return the graph.
	return stateGraph.Compile()
}

// createNodeCallbacks creates comprehensive callbacks for monitoring and performance tracking.
func (w *documentWorkflow) createNodeCallbacks() *graph.NodeCallbacks {
	callbacks := graph.NewNodeCallbacks()

	// Before node callback: Track performance and metadata (no duplicate logging).
	callbacks.RegisterBeforeNode(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State) (any, error) {
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

		return nil, nil // Continue with normal execution.
	})

	// After node callback: Track completion and performance metrics (no duplicate logging).
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

		// Performance monitoring: Alert on slow nodes.
		if executionTime > 25*time.Second {
			fmt.Printf("⚠️  [CALLBACK] Performance alert: Node %s took %v to execute\n",
				callbackCtx.NodeName, executionTime)
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
		return result, nil
	})

	// Error callback: Comprehensive error logging and recovery.
	callbacks.RegisterOnNodeError(func(
		ctx context.Context,
		callbackCtx *graph.NodeCallbackContext,
		state graph.State,
		err error,
	) {
		// Log detailed error information.
		fmt.Printf("❌ [CALLBACK] Error in node: %s (%s) at step %d\n",
			callbackCtx.NodeName, callbackCtx.NodeType, callbackCtx.StepNumber)
		fmt.Printf("   Error details: %v\n", err)

		// Track error statistics.
		if state["error_count"] == nil {
			state["error_count"] = 0
		}
		errorCount := state["error_count"].(int)
		state["error_count"] = errorCount + 1

		// Update execution history with error info.
		if history, ok := state["node_execution_history"].([]map[string]any); ok && len(history) > 0 {
			lastEntry := history[len(history)-1]
			lastEntry["end_time"] = time.Now()
			lastEntry["success"] = false
			lastEntry["error"] = err.Error()
		}

		// Special error handling for different node types.
		switch callbackCtx.NodeType {
		case graph.NodeTypeLLM:
			fmt.Printf("   🤖 LLM node error - this might be a model API issue\n")
		case graph.NodeTypeTool:
			fmt.Printf("   🔧 Tool execution error - check tool implementation\n")
		case graph.NodeTypeFunction:
			fmt.Printf("   ⚙️  Function node error - check business logic\n")
		}

		// Add error context to state for debugging.
		if state["error_context"] == nil {
			state["error_context"] = make([]map[string]any, 0)
		}
		errorContext := state["error_context"].([]map[string]any)
		errorContext = append(errorContext, map[string]any{
			"node_id":     callbackCtx.NodeID,
			"node_name":   callbackCtx.NodeName,
			"step_number": callbackCtx.StepNumber,
			"error":       err.Error(),
			"timestamp":   time.Now(),
		})
		state["error_context"] = errorContext
	})

	return callbacks
}

// formatExecutionStats formats the execution history into a readable string.
func (w *documentWorkflow) formatExecutionStats(history []map[string]any) string {
	if len(history) == 0 {
		return ""
	}

	var stats strings.Builder
	stats.WriteString("🚀 Execution Flow:\n")

	totalExecutionTime := time.Duration(0)
	for i, entry := range history {
		nodeName, _ := entry["node_name"].(string)
		nodeType, _ := entry["node_type"].(string)
		success, _ := entry["success"].(bool)
		executionTime, _ := entry["execution_time"].(time.Duration)

		status := "✅"
		if !success {
			status = "❌"
		}

		stats.WriteString(fmt.Sprintf("   %d. %s %s (%s) - %v\n",
			i+1, status, nodeName, nodeType, executionTime))

		if executionTime > 0 {
			totalExecutionTime += executionTime
		}
	}

	stats.WriteString("\n📈 Performance Summary:\n")
	stats.WriteString(fmt.Sprintf("   • Total Nodes Executed: %d\n", len(history)))
	stats.WriteString(fmt.Sprintf("   • Total Execution Time: %v\n", totalExecutionTime))

	// Calculate average execution time
	if len(history) > 0 {
		avgTime := totalExecutionTime / time.Duration(len(history))
		stats.WriteString(fmt.Sprintf("   • Average Node Time: %v\n", avgTime))
	}
	return stats.String()
}

// Node function implementations.

func (w *documentWorkflow) preprocessDocument(ctx context.Context, state graph.State) (any, error) {
	// Get input from GraphAgent's state fields
	var input string
	if userInput, ok := state[graph.StateKeyUserInput].(string); ok {
		input = userInput
	}
	if input == "" {
		return nil, errors.New("no input document found (checked input field)")
	}
	// Basic preprocessing
	input = strings.TrimSpace(input)
	if len(input) < 10 {
		return nil, errors.New("document too short for processing (minimum 10 characters)")
	}
	// Return state with preprocessing results.
	return graph.State{
		stateKeyDocumentLength:  len(input),
		stateKeyWordCount:       len(strings.Fields(input)),
		graph.StateKeyUserInput: input,
		stateKeyProcessingStage: "preprocessing",
	}, nil
}

func (w *documentWorkflow) routeComplexity(ctx context.Context, state graph.State) (any, error) {
	// This is just a pass-through node; actual routing happens via conditional edges.
	return graph.State{
		stateKeyProcessingStage: "complexity_routing",
	}, nil
}

func (w *documentWorkflow) complexityCondition(ctx context.Context, state graph.State) (level string, err error) {
	defer func() {
		state[stateKeyComplexityLevel] = level
	}()
	// First, try to extract complexity from the LLM's response after tool usage.
	if lastResponse, ok := state[graph.StateKeyLastResponse].(string); ok {
		responseLower := strings.ToLower(lastResponse)
		if strings.Contains(responseLower, " complex ") {
			return complexityComplex, nil
		} else if strings.Contains(responseLower, " moderate ") {
			return complexityModerate, nil
		} else if strings.Contains(responseLower, " simple ") {
			return complexitySimple, nil
		}
	}
	// If no complexity found in LLM response, use the tool's analysis result.
	// The tool result should be in the messages as a tool message.
	if msgs, ok := state[graph.StateKeyMessages].([]model.Message); ok {
		for _, msg := range msgs {
			if msg.Role == model.RoleTool {
				// Parse the tool result to extract complexity level.
				var result complexityResult
				if err := json.Unmarshal([]byte(msg.Content), &result); err == nil {
					return result.Level, nil
				}
			}
		}
	}
	// Final fallback to document length heuristic (should rarely be used).
	const complexityThreshold = 200
	if wordCount, ok := state[stateKeyWordCount].(int); ok {
		if wordCount > complexityThreshold {
			return complexityComplex, nil
		} else if wordCount > 50 {
			return complexityModerate, nil
		}
	}
	return complexitySimple, nil
}

func (w *documentWorkflow) formatOutput(ctx context.Context, state graph.State) (any, error) {
	content, ok := state[graph.StateKeyLastResponse].(string)
	if !ok {
		return nil, fmt.Errorf("no content found for formatting")
	}
	// Create final formatted output.
	complexityLevel, _ := state[stateKeyComplexityLevel].(string)
	wordCount, _ := state[stateKeyWordCount].(int)

	// Extract callback-generated metadata for enhanced output.
	var executionStats string
	if history, ok := state["node_execution_history"].([]map[string]any); ok && len(history) > 0 {
		executionStats = w.formatExecutionStats(history)
	}

	var errorStats string
	if errorCount, ok := state["error_count"].(int); ok && errorCount > 0 {
		errorStats = fmt.Sprintf("   • Errors Encountered: %d\n", errorCount)
	}

	finalOutput := fmt.Sprintf(`
╔══════════════════════════════════════════════════════════════════╗
║                    DOCUMENT PROCESSING RESULTS                   ║
╚══════════════════════════════════════════════════════════════════╝

%s

╔══════════════════════════════════════════════════════════════════╗
║                         PROCESSING DETAILS                       ║
╚══════════════════════════════════════════════════════════════════╝

📊 Processing Statistics:
   • Complexity Level: %s
   • Word Count: %d
   • Completed At: %s%s

%s

✅ Processing completed successfully!
`,
		content,
		complexityLevel,
		wordCount,
		time.Now().Format("2006-01-02 15:04:05"),
		errorStats,
		executionStats)

	return graph.State{
		graph.StateKeyLastResponse: finalOutput,
	}, nil
}

// Tool function implementations.

func (w *documentWorkflow) analyzeComplexity(ctx context.Context, args complexityArgs) (complexityResult, error) {
	text := args.Text

	// Simple complexity analysis.
	wordCount := len(strings.Fields(text))
	sentenceCount := strings.Count(text, ".") + strings.Count(text, "!") +
		strings.Count(text, "?")

	var level string
	var score float64

	if wordCount < 50 {
		level = complexitySimple
		score = 0.3
	} else if wordCount < 200 {
		level = complexityModerate
		score = 0.6
	} else {
		level = complexityComplex
		score = 0.9
	}
	return complexityResult{
		Level:         level,
		Score:         score,
		WordCount:     wordCount,
		SentenceCount: sentenceCount,
	}, nil
}

// startInteractiveMode starts the interactive document processing mode.
func (w *documentWorkflow) startInteractiveMode(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Interactive Document Processing Mode")
	fmt.Println("   Enter your document content (or 'exit' to quit)")
	fmt.Println("   Type 'help' for available commands")
	fmt.Println()

	for {
		fmt.Print("📄 Document: ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch strings.ToLower(input) {
		case "exit", "quit":
			fmt.Println("👋 Goodbye!")
			return nil
		case "help":
			w.showHelp()
			continue
		}

		// Process the document.
		if err := w.processDocument(ctx, input); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // Add spacing between documents.
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

// processDocument processes a single document through the workflow.
func (w *documentWorkflow) processDocument(ctx context.Context, content string) error {
	// Create user message.
	message := model.NewUserMessage(content)
	// Run the workflow through the runner.
	eventChan, err := w.runner.Run(
		ctx,
		w.userID,
		w.sessionID,
		message,
		// Set runtime state for each run.
		agent.WithRuntimeState(map[string]any{
			"user_id": w.userID,
		}),
	)
	if err != nil {
		return fmt.Errorf("failed to run workflow: %w", err)
	}
	// Process streaming response.
	return w.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming workflow response.
func (w *documentWorkflow) processStreamingResponse(eventChan <-chan *event.Event) error {
	var (
		workflowStarted bool
		stageCount      int
	)
	for event := range eventChan {
		// Handle errors.
		if event.Error != nil {
			fmt.Printf("❌ Error: %s\n", event.Error.Message)
			continue
		}
		// Track node execution events.
		if event.Author == graph.AuthorGraphNode {
			// Try to extract node metadata from StateDelta.
			if event.StateDelta != nil {
				if nodeData, exists := event.StateDelta[graph.MetadataKeyNode]; exists {
					var nodeMetadata graph.NodeExecutionMetadata
					if err := json.Unmarshal(nodeData, &nodeMetadata); err == nil {
						switch nodeMetadata.Phase {
						case graph.ExecutionPhaseStart:
							fmt.Printf("\n🚀 Entering node: %s (%s)\n", nodeMetadata.NodeID, nodeMetadata.NodeType)

							// Add model information for LLM nodes.
							if nodeMetadata.NodeType == graph.NodeTypeLLM {
								fmt.Printf("   🤖 Using model: %s\n", w.modelName)

								// Display model input if available.
								if nodeMetadata.ModelInput != "" {
									fmt.Printf("   📝 Model Input: %s\n", truncateString(nodeMetadata.ModelInput, 100))
								}
							}

							// Add tool information for tool nodes.
							if nodeMetadata.NodeType == graph.NodeTypeTool {
								fmt.Printf("   🔧 Executing tool node\n")
							}
						case graph.ExecutionPhaseComplete:
							fmt.Printf("✅ Completed node: %s (%s)\n", nodeMetadata.NodeID, nodeMetadata.NodeType)
						case graph.ExecutionPhaseError:
							fmt.Printf("❌ Error in node: %s (%s)\n", nodeMetadata.NodeID, nodeMetadata.NodeType)
						}
					}
				}

				// Handle tool execution events for input/output display.
				if toolData, exists := event.StateDelta[graph.MetadataKeyTool]; exists {
					var toolMetadata graph.ToolExecutionMetadata
					if err := json.Unmarshal(toolData, &toolMetadata); err == nil {
						switch toolMetadata.Phase {
						case graph.ToolExecutionPhaseStart:
							fmt.Printf("🔧 [TOOL] Starting: %s (ID: %s)\n", toolMetadata.ToolName, toolMetadata.ToolID)
							if toolMetadata.Input != "" {
								fmt.Printf("   📥 Input: %s\n", formatJSON(toolMetadata.Input))
							}
						case graph.ToolExecutionPhaseComplete:
							fmt.Printf("✅ [TOOL] Completed: %s (ID: %s) in %v\n",
								toolMetadata.ToolName, toolMetadata.ToolID, toolMetadata.Duration)
							if toolMetadata.Output != "" {
								fmt.Printf("   📤 Output: %s\n", formatJSON(toolMetadata.Output))
							}
							if toolMetadata.Error != "" {
								fmt.Printf("   ❌ Error: %s\n", toolMetadata.Error)
							}
						case graph.ToolExecutionPhaseError:
							fmt.Printf("❌ [TOOL] Error: %s (ID: %s) - %s\n",
								toolMetadata.ToolName, toolMetadata.ToolID, toolMetadata.Error)
						}
					}
				}

				// Handle model execution events for input/output display.
				if modelData, exists := event.StateDelta[graph.MetadataKeyModel]; exists {
					var modelMetadata graph.ModelExecutionMetadata
					if err := json.Unmarshal(modelData, &modelMetadata); err == nil {
						switch modelMetadata.Phase {
						case graph.ModelExecutionPhaseStart:
							fmt.Printf("🤖 [MODEL] Starting: %s (Node: %s)\n", modelMetadata.ModelName, modelMetadata.NodeID)
							if modelMetadata.Input != "" {
								fmt.Printf("   📝 Input: %s\n", truncateString(modelMetadata.Input, 100))
							}
						case graph.ModelExecutionPhaseComplete:
							fmt.Printf("✅ [MODEL] Completed: %s (Node: %s) in %v\n",
								modelMetadata.ModelName, modelMetadata.NodeID, modelMetadata.Duration)
							if modelMetadata.Output != "" {
								fmt.Printf("   📤 Output: %s\n", truncateString(modelMetadata.Output, 100))
							}
							if modelMetadata.Error != "" {
								fmt.Printf("   ❌ Error: %s\n", modelMetadata.Error)
							}
						case graph.ModelExecutionPhaseError:
							fmt.Printf("❌ [MODEL] Error: %s (Node: %s) - %s\n",
								modelMetadata.ModelName, modelMetadata.NodeID, modelMetadata.Error)
						}
					}
				}
			}
		}
		// Process streaming content from LLM nodes (events with model names as authors).
		if len(event.Choices) > 0 {
			choice := event.Choices[0]
			// Handle streaming delta content.
			if choice.Delta.Content != "" {
				if !workflowStarted {
					fmt.Print("🤖 LLM Streaming: ")
					workflowStarted = true
				}
				fmt.Print(choice.Delta.Content)
			}
			// Add newline when LLM streaming is complete (when choice is done).
			if choice.Delta.Content == "" && workflowStarted {
				fmt.Println()           // Add newline after LLM streaming completes
				workflowStarted = false // Reset for next LLM node
			}
		}
		// Track workflow stages.
		if event.Author == graph.AuthorGraphExecutor {
			stageCount++
			if stageCount >= 1 && len(event.Response.Choices) > 0 {
				content := event.Response.Choices[0].Message.Content
				if content != "" {
					fmt.Printf("\n🔄 Stage %d completed, %s\n", stageCount, content)
				} else {
					fmt.Printf("\n🔄 Stage %d completed\n", stageCount)
				}
			}
		}
		// Handle completion.
		if event.Done {
			break
		}
	}
	return nil
}

// showHelp displays available commands.
func (w *documentWorkflow) showHelp() {
	fmt.Println("📚 Available Commands:")
	fmt.Println("   help  - Show this help message")
	fmt.Println("   exit  - Exit the application")
	fmt.Println()
	fmt.Println("💡 Usage:")
	fmt.Println("   Simply paste or type your document content")
	fmt.Println("   The workflow will automatically:")
	fmt.Println("   • Validate and preprocess the document")
	fmt.Println("   • Analyze complexity and themes")
	fmt.Println("   • Route to appropriate processing path")
	fmt.Println("   • Assess and enhance quality if needed")
	fmt.Println("   • Format the final output")
	fmt.Println()
}

// formatJSON formats JSON strings for better readability.
func formatJSON(jsonStr string) string {
	if jsonStr == "" {
		return ""
	}
	// Try to pretty print the JSON.
	var prettyJSON interface{}
	if err := json.Unmarshal([]byte(jsonStr), &prettyJSON); err == nil {
		if prettyBytes, err := json.MarshalIndent(prettyJSON, "", "  "); err == nil {
			return string(prettyBytes)
		}
	}
	// Fallback to original string if not valid JSON.
	return jsonStr
}

// truncateString truncates a string to the specified length and adds ellipsis if needed.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// Type definitions for tool functions.

type complexityArgs struct {
	Text string `json:"text" description:"Text to analyze for complexity"`
}

type complexityResult struct {
	Level         string  `json:"level"`
	Score         float64 `json:"score"`
	WordCount     int     `json:"word_count"`
	SentenceCount int     `json:"sentence_count"`
}
