package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin" // Auto-register builtin components
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Println("🚀 LLMAgent with MCP Tools Example")
	fmt.Println("This example demonstrates how to configure MCP tools directly in DSL")
	fmt.Println("without needing to create separate nodes or edges.")
	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println()

	// Step 1: Create model registry
	modelRegistry := registry.NewModelRegistry()

	// Step 2: Register model
	modelName := getEnv("MODEL_NAME", "deepseek-chat")
	llmModel := openai.New(modelName)
	modelRegistry.MustRegister("deepseek-chat", llmModel)
	fmt.Printf("✅ Registered model: %s\n", modelName)

	// Step 3: Register calculator tool to DefaultToolRegistry
	// Note: Built-in tools (duckduckgo_search) are already auto-registered
	calculatorTool := function.NewFunctionTool(
		calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform basic mathematical calculations (add, subtract, multiply, divide)"),
	)
	registry.DefaultToolRegistry.MustRegister("calculator", calculatorTool)
	fmt.Println("✅ Registered tool: calculator")
	fmt.Println("✅ Built-in tools available: duckduckgo_search (auto-registered)")
	fmt.Println()

	// Step 4: Load workflow
	parser := dsl.NewParser()
	workflow, err := parser.ParseFile("workflow.json")
	if err != nil {
		return fmt.Errorf("failed to parse workflow: %w", err)
	}
	fmt.Printf("✅ Loaded workflow: %s\n", workflow.Name)
	fmt.Println()

	// Step 5: Compile workflow
	compiler := dsl.NewCompiler(registry.DefaultRegistry).
		WithModelRegistry(modelRegistry).
		WithToolRegistry(registry.DefaultToolRegistry)

	compiledGraph, err := compiler.Compile(workflow)
	if err != nil {
		return fmt.Errorf("failed to compile workflow: %w", err)
	}
	fmt.Println("✅ Workflow compiled successfully!")
	fmt.Println()

	// Step 6: Create GraphAgent
	graphAgent, err := graphagent.New("llmagent-mcp-workflow", compiledGraph,
		graphagent.WithDescription("LLMAgent with MCP tools configuration"),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	// Step 7: Create Runner
	appRunner := runner.NewRunner(
		"llmagent-mcp-demo",
		graphAgent,
	)
	defer appRunner.Close()

	fmt.Println("✅ Runner created successfully!")
	fmt.Println()
	fmt.Println("Type your message (or 'exit' to quit):")
	fmt.Println("  e.g. MCP echo: \"Use the echo tool to say Hello World with prefix MCP:\"")
	fmt.Println("       calculator: \"Use the calculator tool to add 3 and 5\"")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println()

	// Step 8: Interactive loop
	ctx := context.Background()
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		if strings.ToLower(userInput) == "exit" {
			fmt.Println("👋 Goodbye!")
			break
		}

		// Execute workflow
		if err := executeWorkflow(ctx, appRunner, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println()
	}

	return nil
}

func executeWorkflow(ctx context.Context, appRunner runner.Runner, userInput string) error {
	// Create user message
	message := model.NewUserMessage(userInput)

	// Run the workflow
	userID := "demo-user"
	sessionID := "demo-session"

	eventChan, err := appRunner.Run(ctx, userID, sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run workflow: %w", err)
	}

	// Process events
	fmt.Print("🤖 Assistant: ")
	var hasContent bool
	eventCount := 0

	for evt := range eventChan {
		eventCount++

		// Handle errors
		if evt.Error != nil {
			fmt.Printf("\n❌ Error (event %d): %s\n", eventCount, evt.Error.Message)
			return fmt.Errorf("execution error: %s", evt.Error.Message)
		}

		// Debug: Print event info
		if os.Getenv("DEBUG") == "1" {
			fmt.Printf("\n[DEBUG Event %d] Object: %s, Author: %s, Choices: %d, Done: %v, IsPartial: %v, RequiresCompletion: %v\n",
				eventCount, evt.Object, evt.Author, len(evt.Choices), evt.Done, evt.IsPartial, evt.RequiresCompletion)

			// Check Response methods
			if evt.Response != nil {
				fmt.Printf("  Response.IsToolCallResponse(): %v\n", evt.Response.IsToolCallResponse())
				fmt.Printf("  Response.IsFinalResponse(): %v\n", evt.Response.IsFinalResponse())
			}

			// Check for tool response events
			if evt.Object == "tool.response" {
				fmt.Printf("  *** TOOL RESPONSE EVENT ***\n")
			}

			if len(evt.Choices) > 0 {
				choice := evt.Choices[0]
				fmt.Printf("  Delta.Content: %q\n", choice.Delta.Content)
				fmt.Printf("  Delta.Role: %q\n", choice.Delta.Role)
				if len(choice.Delta.ToolCalls) > 0 {
					fmt.Printf("  Delta.ToolCalls: %d\n", len(choice.Delta.ToolCalls))
					for i, tc := range choice.Delta.ToolCalls {
						fmt.Printf("    [%d] %s(%s)\n", i, tc.Function.Name, tc.Function.Arguments)
					}
				}
				fmt.Printf("  Message.Content: %q\n", choice.Message.Content)
				fmt.Printf("  Message.Role: %q\n", choice.Message.Role)
				if len(choice.Message.ToolCalls) > 0 {
					fmt.Printf("  Message.ToolCalls: %d\n", len(choice.Message.ToolCalls))
					for i, tc := range choice.Message.ToolCalls {
						fmt.Printf("    [%d] %s(%s)\n", i, tc.Function.Name, tc.Function.Arguments)
					}
				}
			}
		}

		// Print streaming content if available
		if len(evt.Choices) > 0 {
			choice := evt.Choices[0]

			// Prefer streaming deltas; only fall back to final message
			// content if we never printed any delta.
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
				hasContent = true
			} else if evt.Done && choice.Message.Content != "" && !hasContent {
				fmt.Print(choice.Message.Content)
				hasContent = true
			}
		}

		// Don't break on evt.Done - we need to process all events including tool calls
		// The channel will be closed when the workflow is complete
	}

	if !hasContent {
		fmt.Print("(No response)")
	}
	fmt.Printf("\n[Processed %d events]\n", eventCount)

	return nil
}

// calculate performs basic mathematical operations
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

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
