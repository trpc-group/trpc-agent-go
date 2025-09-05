//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates parallel fan-out execution using the graph package.
// This example shows how to build and execute graphs with parallel task distribution,
// LLM nodes, and function nodes using []*Command for dynamic fan-out.
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
	"reflect"
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
	fmt.Printf("🚀 Parallel Fan-out Workflow Example\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Println(strings.Repeat("=", 50))
	// Create and run the workflow.
	workflow := &fanoutWorkflow{
		modelName: *modelName,
	}
	if err := workflow.run(); err != nil {
		log.Fatalf("Workflow failed: %v", err)
	}
}

// fanoutWorkflow manages the parallel fan-out workflow.
type fanoutWorkflow struct {
	modelName string
	runner    runner.Runner
	userID    string
	sessionID string
}

// run starts the parallel fan-out workflow.
func (w *fanoutWorkflow) run() error {
	ctx := context.Background()
	// Setup the workflow.
	if err := w.setup(); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	return w.startInteractiveMode(ctx)
}

// setup creates the graph agent and runner.
func (w *fanoutWorkflow) setup() error {
	// Create the parallel fan-out graph.
	workflowGraph, err := w.createFanoutGraph()
	if err != nil {
		return fmt.Errorf("failed to create graph: %w", err)
	}

	// Create GraphAgent from the compiled graph.
	graphAgent, err := graphagent.New("parallel-fanout", workflowGraph,
		graphagent.WithDescription("Parallel fan-out execution workflow"),
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	// Create session service.
	sessionService := inmemory.NewSessionService()

	// Create runner with the graph agent.
	appName := "fanout-workflow"
	w.runner = runner.NewRunner(
		appName,
		graphAgent,
		runner.WithSessionService(sessionService),
	)

	// Setup identifiers.
	w.userID = "user"
	w.sessionID = fmt.Sprintf("fanout-session-%d", time.Now().Unix())

	fmt.Printf("✅ Parallel fan-out workflow ready! Session: %s\n\n", w.sessionID)
	return nil
}

// createFanoutGraph creates a parallel fan-out workflow graph.
func (w *fanoutWorkflow) createFanoutGraph() (*graph.Graph, error) {
	// Create extended state schema for messages and metadata.
	schema := graph.MessagesStateSchema().
		AddField("results", graph.StateField{
			Type:    reflect.TypeOf([]string{}),
			Reducer: graph.StringSliceReducer,
			Default: func() any { return []string{} },
		})

	// Create model instance.
	modelInstance := openai.New(*modelName)

	// Create analysis tools.
	taskAnalysisTool := function.NewFunctionTool(
		w.analyzeTaskComplexity,
		function.WithName("analyze_task_complexity"),
		function.WithDescription("Analyzes task complexity and suggests processing strategy"),
	)

	// Create node callbacks for monitoring and performance tracking.
	callbacks := w.createNodeCallbacks()

	// Create stateGraph with schema and callbacks.
	stateGraph := graph.NewStateGraph(schema).WithNodeCallbacks(callbacks)
	tools := map[string]tool.Tool{
		"analyze_task_complexity": taskAnalysisTool,
	}

	// Build the workflow graph.
	stateGraph.
		// Add input analysis node.
		AddNode("analyze_input", w.analyzeInput).

		// Add LLM task planning node.
		AddLLMNode("plan_tasks", modelInstance,
			`You are a task planning expert. Analyze the input and create a plan for parallel task distribution.

IMPORTANT: You MUST use the analyze_task_complexity tool to analyze the input first.

Steps:
1. Call the analyze_task_complexity tool with the input text
2. Based on the tool's analysis, determine how many parallel tasks to create
3. Respond with only the number of tasks (1-5) as a single number

You MUST use the tool - this is not optional.`,
			tools).
		AddToolsNode("tools", tools).

		// Add fan-out node that returns []*Command.
		AddNode("create_fanout", w.createFanoutTasks).

		// Add LLM worker node for processing individual tasks.
		AddLLMNode("process_task", modelInstance,
			`You are a task processing expert. Process the given task with the specified parameters.

Focus on:
1. Understanding the task parameters and priority
2. Applying appropriate processing strategy
3. Providing clear, actionable results
4. Maintaining quality and consistency

Remember: only output the final processed result itself, no other text.`,
			map[string]tool.Tool{}).

		// Add aggregation node.
		AddNode("aggregate_results", w.aggregateResults).

		// Set up the workflow routing.
		SetEntryPoint("analyze_input").
		SetFinishPoint("aggregate_results")

	// Add workflow edges - this is the key fix for the tool execution flow.
	stateGraph.AddEdge("analyze_input", "plan_tasks")
	stateGraph.AddToolsConditionalEdges("plan_tasks", "tools", "create_fanout")
	stateGraph.AddEdge("tools", "plan_tasks") // This edge allows the LLM to continue after tool execution
	stateGraph.AddEdge("create_fanout", "process_task")
	stateGraph.AddEdge("process_task", "aggregate_results")

	// Build and return the graph.
	return stateGraph.Compile()
}

// Node function implementations.

func (w *fanoutWorkflow) analyzeInput(ctx context.Context, state graph.State) (any, error) {
	// Get input from GraphAgent's state fields.
	var input string
	if userInput, ok := state[graph.StateKeyUserInput].(string); ok {
		input = userInput
	}
	if input == "" {
		return nil, fmt.Errorf("no input found (checked %s field)", graph.StateKeyUserInput)
	}

	// Basic input analysis.
	input = strings.TrimSpace(input)
	if len(input) < 10 {
		return nil, errors.New("input too short for processing (minimum 10 characters)")
	}

	// Return state with input analysis results.
	return graph.State{
		graph.StateKeyUserInput: input,
		"input_length":          len(input),
		"word_count":            len(strings.Fields(input)),
		"processing_stage":      "input_analysis",
	}, nil
}

func (w *fanoutWorkflow) createFanoutTasks(ctx context.Context, state graph.State) (any, error) {
	// Get the number of tasks from the LLM's response.
	var numTasks int
	if lastResponse, ok := state[graph.StateKeyLastResponse].(string); ok {
		// Parse the response to get the number of tasks.
		if _, err := fmt.Sscanf(lastResponse, "%d", &numTasks); err != nil {
			numTasks = 3 // Default fallback.
		}
	} else {
		numTasks = 3 // Default fallback.
	}

	// Ensure reasonable bounds.
	if numTasks < 1 {
		numTasks = 1
	}
	if numTasks > 5 {
		numTasks = 5
	}

	fmt.Printf("📋 Creating %d parallel tasks...\n", numTasks)

	// Generate commands for parallel execution.
	cmds := make([]*graph.Command, numTasks)
	for i := 0; i < numTasks; i++ {
		taskID := fmt.Sprintf("task-%c", 'A'+i)
		priority := []string{"high", "medium", "low"}[i%3]

		cmds[i] = &graph.Command{
			Update: graph.State{
				"task_id":    taskID,
				"priority":   priority,
				"worker_id":  i + 1,
				"created_at": time.Now().Format("15:04:05"),
				"input_text": state[graph.StateKeyUserInput],
			},
			GoTo: "process_task",
		}

		fmt.Printf("✅ %s (priority: %s) created\n", taskID, priority)
	}

	fmt.Printf("\n🔄 Executing %d parallel tasks...\n", numTasks)
	return cmds, nil
}

func (w *fanoutWorkflow) aggregateResults(ctx context.Context, state graph.State) (any, error) {
	// Extract results from the parallel tasks.
	var results []string
	if resultsData, ok := state["results"]; ok {
		if resultsSlice, ok := resultsData.([]string); ok {
			results = resultsSlice
		}
	}

	// Extract execution metadata.
	var executionStats string
	if history, ok := state["node_execution_history"].([]map[string]any); ok && len(history) > 0 {
		executionStats = w.formatExecutionStats(history)
	}

	var errorStats string
	if errorCount, ok := state["error_count"].(int); ok && errorCount > 0 {
		errorStats = fmt.Sprintf("   • Errors Encountered: %d\n", errorCount)
	}

	// Create final aggregated output.
	finalOutput := fmt.Sprintf(`
╔══════════════════════════════════════════════════════════════════╗
║                    PARALLEL FAN-OUT RESULTS                      ║
╚══════════════════════════════════════════════════════════════════╝

📊 Task Results (%d completed):
%s

╔══════════════════════════════════════════════════════════════════╗
║                         EXECUTION DETAILS                        ║
╚══════════════════════════════════════════════════════════════════╝

📈 Execution Statistics:
   • Total Tasks: %d
   • Completed At: %s%s

%s

✅ Parallel fan-out execution completed successfully!
`,
		len(results),
		w.formatTaskResults(results),
		len(results),
		time.Now().Format("2006-01-02 15:04:05"),
		errorStats,
		executionStats)

	return graph.State{
		graph.StateKeyLastResponse: finalOutput,
		"total_tasks":              len(results),
		"final_results":            results,
	}, nil
}

// formatTaskResults formats the task results into a readable string.
func (w *fanoutWorkflow) formatTaskResults(results []string) string {
	if len(results) == 0 {
		return "   No results available"
	}

	var output strings.Builder
	for i, result := range results {
		output.WriteString(fmt.Sprintf("   %d. %s\n", i+1, result))
	}
	return output.String()
}

// formatExecutionStats formats the execution history into a readable string.
func (w *fanoutWorkflow) formatExecutionStats(history []map[string]any) string {
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

	// Calculate average execution time.
	if len(history) > 0 {
		avgTime := totalExecutionTime / time.Duration(len(history))
		stats.WriteString(fmt.Sprintf("   • Average Node Time: %v\n", avgTime))
	}
	return stats.String()
}

// Tool function implementations.

func (w *fanoutWorkflow) analyzeTaskComplexity(ctx context.Context, args taskAnalysisArgs) (taskAnalysisResult, error) {
	text := args.Text

	// Simple complexity analysis.
	wordCount := len(strings.Fields(text))
	sentenceCount := strings.Count(text, ".") + strings.Count(text, "!") +
		strings.Count(text, "?")

	var complexity string
	var suggestedTasks int

	if wordCount < 50 {
		complexity = "simple"
		suggestedTasks = 2
	} else if wordCount < 200 {
		complexity = "moderate"
		suggestedTasks = 3
	} else {
		complexity = "complex"
		suggestedTasks = 4
	}

	return taskAnalysisResult{
		Complexity:     complexity,
		WordCount:      wordCount,
		SentenceCount:  sentenceCount,
		SuggestedTasks: suggestedTasks,
	}, nil
}

// startInteractiveMode starts the interactive parallel fan-out mode.
func (w *fanoutWorkflow) startInteractiveMode(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Interactive Parallel Fan-out Mode")
	fmt.Println("   Enter your content to process with parallel fan-out (or 'exit' to quit)")
	fmt.Println("   Type 'help' for available commands")
	fmt.Println()

	for {
		fmt.Print("📄 Content: ")
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

		// Process the content through parallel fan-out.
		if err := w.processContent(ctx, input); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // Add spacing between content.
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

// processContent processes a single content through the parallel fan-out workflow.
func (w *fanoutWorkflow) processContent(ctx context.Context, content string) error {
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
func (w *fanoutWorkflow) processStreamingResponse(eventChan <-chan *event.Event) error {
	const maxPreviewLen = 100
	var (
		workflowStarted bool
		stageCount      int
	)
	for event := range eventChan {
		if w.handleErrorEvent(event) {
			continue
		}
		if event.Author == graph.AuthorGraphNode {
			w.handleGraphNodeEvent(event, maxPreviewLen)
		}
		w.handleStreamingChoices(event, &workflowStarted)
		w.trackStageProgress(event, &stageCount)
		if event.Done {
			break
		}
	}
	return nil
}

func (w *fanoutWorkflow) handleErrorEvent(e *event.Event) bool {
	if e.Error != nil {
		fmt.Printf("❌ Error: %s\n", e.Error.Message)
		return true
	}
	return false
}

func (w *fanoutWorkflow) handleGraphNodeEvent(e *event.Event, maxPreviewLen int) {
	if e.StateDelta == nil {
		return
	}
	if nodeData, exists := e.StateDelta[graph.MetadataKeyNode]; exists {
		w.processNodeDelta(nodeData, maxPreviewLen)
	}
	if toolData, exists := e.StateDelta[graph.MetadataKeyTool]; exists {
		w.processToolDelta(toolData)
	}
	if modelData, exists := e.StateDelta[graph.MetadataKeyModel]; exists {
		w.processModelDelta(modelData, maxPreviewLen)
	}
}

func (w *fanoutWorkflow) processNodeDelta(data []byte, maxPreviewLen int) {
	var nodeMetadata graph.NodeExecutionMetadata
	if err := json.Unmarshal(data, &nodeMetadata); err != nil {
		return
	}
	switch nodeMetadata.Phase {
	case graph.ExecutionPhaseStart:
		fmt.Printf("\n🚀 Entering node: %s (%s)\n",
			nodeMetadata.NodeID, nodeMetadata.NodeType)
		if nodeMetadata.NodeType == graph.NodeTypeLLM {
			fmt.Printf("   🤖 Using model: %s\n", w.modelName)
			if nodeMetadata.ModelInput != "" {
				fmt.Printf("   📝 Model Input: %s\n",
					truncateString(nodeMetadata.ModelInput, maxPreviewLen))
			}
		}
		if nodeMetadata.NodeType == graph.NodeTypeTool {
			fmt.Printf("   🔧 Executing tool node\n")
		}
	case graph.ExecutionPhaseComplete:
		fmt.Printf("✅ Completed node: %s (%s)\n",
			nodeMetadata.NodeID, nodeMetadata.NodeType)
	case graph.ExecutionPhaseError:
		fmt.Printf("❌ Error in node: %s (%s)\n",
			nodeMetadata.NodeID, nodeMetadata.NodeType)
	}
}

func (w *fanoutWorkflow) processToolDelta(data []byte) {
	var toolMetadata graph.ToolExecutionMetadata
	if err := json.Unmarshal(data, &toolMetadata); err != nil {
		return
	}
	switch toolMetadata.Phase {
	case graph.ToolExecutionPhaseStart:
		fmt.Printf("🔧 [TOOL] Starting: %s (ID: %s)\n",
			toolMetadata.ToolName, toolMetadata.ToolID)
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

func (w *fanoutWorkflow) processModelDelta(data []byte, maxPreviewLen int) {
	var modelMetadata graph.ModelExecutionMetadata
	if err := json.Unmarshal(data, &modelMetadata); err != nil {
		return
	}
	switch modelMetadata.Phase {
	case graph.ModelExecutionPhaseStart:
		fmt.Printf("🤖 [MODEL] Starting: %s (Node: %s)\n",
			modelMetadata.ModelName, modelMetadata.NodeID)
		if modelMetadata.Input != "" {
			fmt.Printf("   📝 Input: %s\n",
				truncateString(modelMetadata.Input, maxPreviewLen))
		}
	case graph.ModelExecutionPhaseComplete:
		fmt.Printf("✅ [MODEL] Completed: %s (Node: %s) in %v\n",
			modelMetadata.ModelName, modelMetadata.NodeID, modelMetadata.Duration)
		if modelMetadata.Output != "" {
			fmt.Printf("   📤 Output: %s\n",
				truncateString(modelMetadata.Output, maxPreviewLen))
		}
		if modelMetadata.Error != "" {
			fmt.Printf("   ❌ Error: %s\n", modelMetadata.Error)
		}
	case graph.ModelExecutionPhaseError:
		fmt.Printf("❌ [MODEL] Error: %s (Node: %s) - %s\n",
			modelMetadata.ModelName, modelMetadata.NodeID, modelMetadata.Error)
	}
}

func (w *fanoutWorkflow) handleStreamingChoices(e *event.Event, started *bool) {
	if len(e.Choices) == 0 {
		return
	}
	choice := e.Choices[0]
	if choice.Delta.Content != "" {
		if !*started {
			fmt.Print("🤖 LLM Streaming: ")
			*started = true
		}
		fmt.Print(choice.Delta.Content)
	}
	if choice.Delta.Content == "" && *started {
		fmt.Println()
		*started = false
	}
}

func (w *fanoutWorkflow) trackStageProgress(e *event.Event, stageCount *int) {
	if e.Author != graph.AuthorGraphExecutor {
		return
	}
	*stageCount++
	if *stageCount >= 1 && len(e.Response.Choices) > 0 {
		content := e.Response.Choices[0].Message.Content
		if content != "" {
			fmt.Printf("\n🔄 Stage %d completed, %s\n", *stageCount, content)
			return
		}
		fmt.Printf("\n🔄 Stage %d completed\n", *stageCount)
	}
}

// showHelp displays available commands.
func (w *fanoutWorkflow) showHelp() {
	fmt.Println("📚 Available Commands:")
	fmt.Println("   help  - Show this help message")
	fmt.Println("   exit  - Exit the application")
	fmt.Println()
	fmt.Println("💡 Usage:")
	fmt.Println("   Simply paste or type your content")
	fmt.Println("   The workflow will automatically:")
	fmt.Println("   • Analyze input complexity")
	fmt.Println("   • Plan optimal number of parallel tasks")
	fmt.Println("   • Create multiple parallel execution paths")
	fmt.Println("   • Process each task with LLM")
	fmt.Println("   • Aggregate results from all tasks")
	fmt.Println()
}

// formatJSON formats JSON strings for better readability.
func formatJSON(jsonStr string) string {
	if jsonStr == "" {
		return ""
	}
	// Try to pretty print the JSON.
	var prettyJSON any
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

type taskAnalysisArgs struct {
	Text string `json:"text" description:"Text to analyze for task complexity"`
}

type taskAnalysisResult struct {
	Complexity     string `json:"complexity"`
	WordCount      int    `json:"word_count"`
	SentenceCount  int    `json:"sentence_count"`
	SuggestedTasks int    `json:"suggested_tasks"`
}
