package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/compiler"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Println("React Planner + Structured Output Test")
	fmt.Println("This example demonstrates using react planner with structured output.")
	fmt.Println("The agent uses tools via React flow, then outputs JSON matching the schema.")
	fmt.Println()
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println()

	// Register calculator tool
	calculatorTool := function.NewFunctionTool(
		calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform basic mathematical calculations (add, subtract, multiply, divide)"),
	)
	registry.DefaultToolRegistry.MustRegister("calculator", calculatorTool)
	fmt.Println("Registered tool: calculator")
	fmt.Println()

	// Load workflow
	parser := dsl.NewParser()
	workflow, err := parser.ParseFile("workflow.json")
	if err != nil {
		return fmt.Errorf("failed to parse workflow: %w", err)
	}
	fmt.Printf("Loaded workflow: %s\n", workflow.Name)
	fmt.Println()

	// Compile workflow
	comp := compiler.New(
		compiler.WithAllowEnvSecrets(true),
		compiler.WithToolProvider(registry.DefaultToolRegistry),
	)

	compiledGraph, err := comp.Compile(workflow)
	if err != nil {
		return fmt.Errorf("failed to compile workflow: %w", err)
	}
	fmt.Println("Workflow compiled successfully!")
	fmt.Println()

	// Create GraphAgent
	graphAgent, err := graphagent.New("react-structured-output-test", compiledGraph,
		graphagent.WithDescription("Test React planner with structured output"),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	// Create Runner
	appRunner := runner.NewRunner(
		"react-structured-output-demo",
		graphAgent,
	)
	defer appRunner.Close()

	fmt.Println("Runner created successfully!")
	fmt.Println()
	fmt.Println("Type a math problem (or 'exit' to quit):")
	fmt.Println("  e.g. \"Calculate 15 + 27\"")
	fmt.Println("       \"What is 100 divided by 4?\"")
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println()

	// Interactive loop
	ctx := context.Background()
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		if strings.ToLower(userInput) == "exit" {
			fmt.Println("Goodbye!")
			break
		}

		// Execute workflow
		if err := executeWorkflow(ctx, appRunner, userInput); err != nil {
			fmt.Printf("Error: %v\n", err)
		}

		fmt.Println()
	}

	return nil
}

func executeWorkflow(ctx context.Context, appRunner runner.Runner, userInput string) error {
	message := model.NewUserMessage(userInput)

	userID := "test-user"
	sessionID := "test-session"

	eventChan, err := appRunner.Run(ctx, userID, sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run workflow: %w", err)
	}

	var (
		hasContent      bool
		transformOutput string
		eventCount      int
		currentNode     string
		nodeStartTime   time.Time
		inLLMStream     bool
		inReasoning     bool
		seenToolCallIDs = make(map[string]bool)
	)

	fmt.Println()

	for evt := range eventChan {
		eventCount++

		if evt.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
			return fmt.Errorf("execution error: %s", evt.Error.Message)
		}

		// === 1. Node Progress Events ===
		handleNodeProgress(evt, &currentNode, &nodeStartTime, &inLLMStream)

		// === 2. LLM Streaming: Delta Content & Reasoning (BEFORE tool calls) ===
		handleDeltaContent(evt, &hasContent, &inLLMStream, &inReasoning)

		// === 3. LLM Streaming: Tool Calls ===
		handleToolCalls(evt, seenToolCallIDs, &inLLMStream)

		// === 4. Transform Output ===
		if output := extractTransformOutput(evt); output != "" {
			transformOutput = output
		}

		// Debug mode
		if os.Getenv("DEBUG") == "1" {
			printDebugEvent(evt, eventCount)
		}
	}

	// Close reasoning if still open
	if inReasoning {
		fmt.Print("\x1b[0m\n")
	}

	// Final newline if we were streaming
	if inLLMStream {
		fmt.Println()
	}

	fmt.Println()
	fmt.Println(strings.Repeat("─", 50))

	// Print transform output if available
	if transformOutput != "" {
		fmt.Printf("📍 Route Result: %s\n", transformOutput)
	}

	fmt.Printf("📊 Processed %d events\n", eventCount)

	return nil
}

// handleNodeProgress displays node start/complete events
func handleNodeProgress(evt *event.Event, currentNode *string, nodeStartTime *time.Time, inLLMStream *bool) {
	switch evt.Object {
	case graph.ObjectTypeGraphNodeStart:
		// End previous LLM stream line if needed
		if *inLLMStream {
			fmt.Println()
			*inLLMStream = false
		}

		nodeID := extractNodeID(evt)
		*currentNode = nodeID
		*nodeStartTime = time.Now()
		fmt.Printf("▶️  [%s] Starting node: %s\n", time.Now().Format("15:04:05"), nodeID)

	case graph.ObjectTypeGraphNodeComplete:
		nodeID := extractNodeID(evt)
		duration := time.Since(*nodeStartTime)
		fmt.Printf("✅ [%s] Completed node: %s (%.2fs)\n", time.Now().Format("15:04:05"), nodeID, duration.Seconds())
	}
}

// handleToolCalls displays tool call information
func handleToolCalls(evt *event.Event, seenToolCallIDs map[string]bool, inLLMStream *bool) {
	if evt.Response == nil || len(evt.Choices) == 0 {
		return
	}

	choice := evt.Choices[0]

	// Check for tool calls in Message (non-streaming) or Delta (streaming)
	var toolCalls []model.ToolCall
	if len(choice.Message.ToolCalls) > 0 {
		toolCalls = choice.Message.ToolCalls
	} else if len(choice.Delta.ToolCalls) > 0 {
		toolCalls = choice.Delta.ToolCalls
	}

	for _, tc := range toolCalls {
		if tc.ID == "" || seenToolCallIDs[tc.ID] {
			continue
		}
		seenToolCallIDs[tc.ID] = true

		// End previous stream line
		if *inLLMStream {
			fmt.Println()
			*inLLMStream = false
		}

		fmt.Printf("🔧 Tool Call: %s\n", tc.Function.Name)
		if len(tc.Function.Arguments) > 0 {
			// Pretty print JSON arguments
			var args map[string]any
			if err := json.Unmarshal(tc.Function.Arguments, &args); err == nil {
				fmt.Printf("   Args: %v\n", args)
			} else {
				fmt.Printf("   Args: %s\n", string(tc.Function.Arguments))
			}
		}
	}

	// Check for tool response
	if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
		fmt.Printf("   ↳ Result: %s\n", strings.TrimSpace(choice.Message.Content))
	}
}

// handleDeltaContent displays streaming LLM content including reasoning
func handleDeltaContent(evt *event.Event, hasContent *bool, inLLMStream *bool, inReasoning *bool) {
	if evt.Response == nil || len(evt.Choices) == 0 {
		return
	}

	choice := evt.Choices[0]

	// === Handle Reasoning Content (thinking) ===
	// Streaming reasoning content
	if choice.Delta.ReasoningContent != "" {
		if !*inReasoning {
			fmt.Print("🧠 Thinking: \x1b[2m") // Start dim text
			*inReasoning = true
		}
		fmt.Print(choice.Delta.ReasoningContent)
	}
	// Non-streaming reasoning content
	if choice.Message.ReasoningContent != "" && !*inReasoning {
		fmt.Printf("🧠 Thinking: \x1b[2m%s\x1b[0m\n", choice.Message.ReasoningContent)
		*inReasoning = true
	}

	// === Handle Normal Content ===
	// Streaming delta content
	if choice.Delta.Content != "" {
		// Close reasoning dim text if we were in reasoning mode
		if *inReasoning {
			fmt.Print("\x1b[0m\n") // End dim text
			*inReasoning = false
		}

		content := choice.Delta.Content

		// Check if this looks like structured JSON output
		if !*inLLMStream && strings.HasPrefix(strings.TrimSpace(content), "{") {
			fmt.Print("💬 LLM (Structured Output): ")
			*inLLMStream = true
		} else if !*inLLMStream {
			fmt.Print("💬 LLM: ")
			*inLLMStream = true
		}
		fmt.Print(content)
		*hasContent = true
	}

	// Also check Message.Content for non-streaming responses
	if choice.Message.Content != "" && choice.Message.Role == model.RoleAssistant && !*hasContent {
		// Close reasoning dim text if we were in reasoning mode
		if *inReasoning {
			fmt.Print("\x1b[0m\n")
			*inReasoning = false
		}

		content := choice.Message.Content

		// Try to pretty print if it's JSON
		if strings.HasPrefix(strings.TrimSpace(content), "{") {
			prettyPrintStructuredOutput(content)
		} else {
			fmt.Printf("💬 LLM: %s", content)
		}
		*hasContent = true
	}
}

// prettyPrintStructuredOutput formats and displays the structured JSON output
func prettyPrintStructuredOutput(jsonStr string) {
	var result map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		fmt.Printf("💬 LLM: %s", jsonStr)
		return
	}

	fmt.Println("💬 LLM Structured Output:")
	
	// Display problem
	if problem, ok := result["problem"].(string); ok {
		fmt.Printf("   📝 Problem: %s\n", problem)
	}
	
	// Display answer
	if answer, ok := result["answer"]; ok {
		fmt.Printf("   🎯 Answer: %v\n", answer)
	}
}

// extractTransformOutput extracts transform node output from StateDelta
func extractTransformOutput(evt *event.Event) string {
	if evt.StateDelta == nil {
		return ""
	}

	raw, ok := evt.StateDelta["node_structured"]
	if !ok {
		return ""
	}

	var nodeStructured map[string]any
	if err := json.Unmarshal(raw, &nodeStructured); err != nil {
		return ""
	}

	for _, key := range []string{"large_result", "medium_result", "small_result"} {
		if nodeData, ok := nodeStructured[key].(map[string]any); ok {
			if output, ok := nodeData["output_parsed"].(string); ok && output != "" {
				return output
			}
		}
	}
	return ""
}

// extractNodeID extracts node ID from event metadata
func extractNodeID(evt *event.Event) string {
	if evt.StateDelta == nil {
		return evt.Author
	}

	raw, ok := evt.StateDelta[graph.MetadataKeyNode]
	if !ok {
		return evt.Author
	}

	var meta graph.NodeExecutionMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return evt.Author
	}

	return meta.NodeID
}

// printDebugEvent prints detailed event information
func printDebugEvent(evt *event.Event, eventCount int) {
	fmt.Printf("\n[DEBUG Event %d] Object: %s, Author: %s, Done: %v\n",
		eventCount, evt.Object, evt.Author, evt.Done)
	if len(evt.Choices) > 0 {
		choice := evt.Choices[0]
		if choice.Delta.Content != "" {
			fmt.Printf("  Delta.Content: %q\n", choice.Delta.Content)
		}
		if choice.Message.Content != "" {
			fmt.Printf("  Message.Content: %q\n", choice.Message.Content)
		}
		if len(choice.Message.ToolCalls) > 0 {
			fmt.Printf("  ToolCalls: %d\n", len(choice.Message.ToolCalls))
			for i, tc := range choice.Message.ToolCalls {
				fmt.Printf("    [%d] %s(%s)\n", i, tc.Function.Name, tc.Function.Arguments)
			}
		}
	}
	if len(evt.StateDelta) > 0 {
		keys := make([]string, 0, len(evt.StateDelta))
		for k := range evt.StateDelta {
			keys = append(keys, k)
		}
		fmt.Printf("  StateDelta keys: %v\n", keys)
	}
}

func calculate(ctx context.Context, args calculatorArgs) (calculatorResult, error) {
	var result float64

	switch strings.ToLower(args.Operation) {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		if args.B != 0 {
			result = args.A / args.B
		} else {
			return calculatorResult{}, fmt.Errorf("division by zero")
		}
	default:
		return calculatorResult{}, fmt.Errorf("unknown operation: %s", args.Operation)
	}

	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}, nil
}

type calculatorArgs struct {
	Operation string  `json:"operation" description:"The operation to perform (add, subtract, multiply, divide)"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}
