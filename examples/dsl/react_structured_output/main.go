package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/compiler"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin"
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

	fmt.Print("Assistant: ")
	var hasContent bool
	var transformOutput string
	eventCount := 0

	for evt := range eventChan {
		eventCount++

		if evt.Error != nil {
			fmt.Printf("\nError (event %d): %s\n", eventCount, evt.Error.Message)
			return fmt.Errorf("execution error: %s", evt.Error.Message)
		}

		// Check for transform node output in StateDelta
		if evt.StateDelta != nil {
			// Check node_structured for transform outputs
			if raw, ok := evt.StateDelta["node_structured"]; ok {
				var nodeStructured map[string]any
				if err := json.Unmarshal(raw, &nodeStructured); err == nil {
					for _, key := range []string{"large_result", "medium_result", "small_result"} {
						if nodeData, ok := nodeStructured[key].(map[string]any); ok {
							if output, ok := nodeData["output_parsed"].(string); ok && output != "" {
								transformOutput = output
							}
						}
					}
				}
			}
		}

		// Debug mode
		if os.Getenv("DEBUG") == "1" {
			fmt.Printf("\n[DEBUG Event %d] Object: %s, Author: %s, Done: %v\n",
				eventCount, evt.Object, evt.Author, evt.Done)
			if len(evt.Choices) > 0 {
				choice := evt.Choices[0]
				fmt.Printf("  Delta.Content: %q\n", choice.Delta.Content)
				fmt.Printf("  Message.Content: %q\n", choice.Message.Content)
				if len(choice.Message.ToolCalls) > 0 {
					fmt.Printf("  ToolCalls: %d\n", len(choice.Message.ToolCalls))
					for i, tc := range choice.Message.ToolCalls {
						fmt.Printf("    [%d] %s(%s)\n", i, tc.Function.Name, tc.Function.Arguments)
					}
				}
			}
		}

		if len(evt.Choices) > 0 {
			choice := evt.Choices[0]

			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
				hasContent = true
			} else if evt.Done && choice.Message.Content != "" && !hasContent {
				fmt.Print(choice.Message.Content)
				hasContent = true
			}
		}
	}

	if !hasContent {
		fmt.Print("(No response)")
	}
	fmt.Println()
	
	// Print transform output if available
	if transformOutput != "" {
		fmt.Printf("Route Result: %s\n", transformOutput)
	}
	
	fmt.Printf("[Processed %d events]\n", eventCount)

	return nil
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
