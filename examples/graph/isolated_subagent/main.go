//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates WithSubgraphIsolatedMessages(true) working correctly
// when the subagent uses tools with the builtin planner (default LLMAgent behavior).
//
// This example shows:
//   - A parent graph that delegates to a child LLMAgent via AddAgentNode
//   - The child LLMAgent has tools and uses the default builtin planner
//   - WithSubgraphIsolatedMessages(true) isolates the child from parent's history
//     while preserving the child's own tool call history within the current invocation
//
// Run with:
//
//	go run . -question "What is 12 + 7?"
//
// Expected behavior: The agent should call the calculator tool once and return the result.
// The child agent correctly sees its own tool calls while being isolated from parent history.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String("model", getEnvOrDefault("MODEL_NAME", "deepseek-chat"), "LLM model name")
	question  = flag.String("question", "", "User question; leave empty for interactive mode")
	isolate   = flag.Bool("isolate", true, "Enable WithSubgraphIsolatedMessages for session isolation")
	maxIter   = flag.Int("max-iter", 3, "Max tool iterations to prevent infinite loop")
	verbose   = flag.Bool("v", false, "Verbose output")
	useReact  = flag.Bool("react", false, "Use ReActPlanner instead of builtin planner")
)

const (
	appName        = "isolated-subagent-demo"
	parentName     = "parent"
	childAgentName = "calculator_agent"
	nodePreprocess = "preprocess"
	nodeAgent      = childAgentName
	nodeCollect    = "collect"
)

func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func main() {
	flag.Parse()

	fmt.Println("=" + strings.Repeat("=", 63))
	fmt.Println("Isolated Subagent Demo - WithSubgraphIsolatedMessages Example")
	fmt.Println("=" + strings.Repeat("=", 63))
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Isolate messages: %v (WithSubgraphIsolatedMessages)\n", *isolate)
	fmt.Printf("Use ReActPlanner: %v\n", *useReact)
	fmt.Printf("Max tool iterations: %d\n", *maxIter)
	fmt.Println()

	prompt := strings.TrimSpace(*question)
	if prompt == "" {
		prompt = readQuestionFromStdin()
	}

	if err := run(prompt); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func readQuestionFromStdin() string {
	fmt.Println("Enter a math question (e.g., 'What is 12 + 7?'):")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			fmt.Println("No input provided, using default question")
			return "What is 12 + 7?"
		}
		text := strings.TrimSpace(scanner.Text())
		if text != "" {
			return text
		}
	}
}

func run(prompt string) error {
	ctx := context.Background()

	// Build the child LLMAgent with calculator tool
	childAgent := buildChildAgent()

	// Build the parent graph that delegates to the child agent
	parentGraph, err := buildParentGraph()
	if err != nil {
		return fmt.Errorf("failed to build parent graph: %w", err)
	}

	// Create the parent GraphAgent with the child as a sub-agent
	parentGA, err := graphagent.New(
		parentName,
		parentGraph,
		graphagent.WithDescription("Parent graph that delegates to calculator agent"),
		graphagent.WithSubAgents([]agent.Agent{childAgent}),
	)
	if err != nil {
		return fmt.Errorf("failed to create parent graph agent: %w", err)
	}

	// Create runner with in-memory session
	sessSvc := inmemory.NewSessionService()
	r := runner.NewRunner(appName, parentGA, runner.WithSessionService(sessSvc))
	defer r.Close()

	userID := "demo-user"
	sessionID := fmt.Sprintf("session-%d", time.Now().Unix())

	fmt.Printf("Session: %s\n", sessionID)
	fmt.Printf("Question: %s\n", prompt)
	fmt.Println(strings.Repeat("-", 64))

	message := model.NewUserMessage(prompt)
	eventChan, err := r.Run(ctx, userID, sessionID, message)
	if err != nil {
		return fmt.Errorf("run failed: %w", err)
	}

	return streamEvents(eventChan)
}

func buildChildAgent() agent.Agent {
	// Create model using environment variables for configuration
	mdl := openai.New(*modelName)

	// Create calculator tool
	calculatorTool := function.NewFunctionTool(
		calculator,
		function.WithName("calculator"),
		function.WithDescription("Perform basic arithmetic operations. "+
			"Parameters: operation (add/subtract/multiply/divide), a (first number), b (second number)."),
	)

	instruction := `You are a calculator assistant. When the user asks a math question:
1. Use the calculator tool to compute the result
2. Return the answer in a clear format

IMPORTANT: Only call the calculator tool ONCE per calculation. After getting the result, 
provide the final answer to the user.`

	opts := []llmagent.Option{
		llmagent.WithModel(mdl),
		llmagent.WithInstruction(instruction),
		llmagent.WithTools([]tool.Tool{calculatorTool}),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
		llmagent.WithMaxToolIterations(*maxIter), // Prevent infinite loop
	}

	// Optionally use ReActPlanner
	if *useReact {
		// Import and use react planner if needed
		// opts = append(opts, llmagent.WithPlanner(react.New()))
	}

	return llmagent.New(childAgentName, opts...)
}

func buildParentGraph() (*graph.Graph, error) {
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)

	// Preprocess node - just passes through
	sg.AddNode(nodePreprocess, preprocess)

	// Agent node with optional isolation
	agentOpts := []graph.Option{}
	if *isolate {
		// WithSubgraphIsolatedMessages(true) isolates the child agent from parent's
		// session history while preserving the child's own tool call history within
		// the current invocation. This allows proper ReAct loop execution.
		agentOpts = append(agentOpts, graph.WithSubgraphIsolatedMessages(true))
	}
	sg.AddAgentNode(nodeAgent, agentOpts...)

	// Collect node - gathers the result
	sg.AddNode(nodeCollect, collect)

	// Wire up the graph
	sg.SetEntryPoint(nodePreprocess)
	sg.AddEdge(nodePreprocess, nodeAgent)
	sg.AddEdge(nodeAgent, nodeCollect)
	sg.SetFinishPoint(nodeCollect)

	return sg.Compile()
}

func preprocess(ctx context.Context, state graph.State) (any, error) {
	if *verbose {
		fmt.Println("[preprocess] Passing input to calculator agent")
	}
	// Just pass through - the user input is already in the state
	return nil, nil
}

func collect(ctx context.Context, state graph.State) (any, error) {
	if *verbose {
		fmt.Println("[collect] Gathering result from calculator agent")
	}
	// Extract the last response
	lastResp, _ := state[graph.StateKeyLastResponse].(string)
	return graph.State{
		"final_answer": lastResp,
	}, nil
}

func streamEvents(eventChan <-chan *event.Event) error {
	toolCallCount := 0
	var lastContent string

	for evt := range eventChan {
		if evt == nil {
			continue
		}

		// Track tool calls
		if evt.IsToolCallResponse() && evt.Response != nil {
			for _, choice := range evt.Response.Choices {
				for _, tc := range choice.Message.ToolCalls {
					toolCallCount++
					fmt.Printf("🔧 Tool call #%d: %s(%s)\n",
						toolCallCount, tc.Function.Name, string(tc.Function.Arguments))
				}
			}
			continue
		}

		// Track tool results
		if evt.IsToolResultResponse() && evt.Response != nil {
			for _, choice := range evt.Response.Choices {
				if choice.Message.Role == model.RoleTool {
					fmt.Printf("✅ Tool result: %s\n", choice.Message.Content)
				}
			}
			continue
		}

		// Stream assistant content
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			choice := evt.Response.Choices[0]

			// Delta content (streaming)
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
				continue
			}

			// Full message content
			if choice.Message.Role == model.RoleAssistant && choice.Message.Content != "" {
				if choice.Message.Content != lastContent {
					fmt.Println(choice.Message.Content)
					lastContent = choice.Message.Content
				}
			}
		}

		// Handle errors
		if evt.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
		}
	}

	fmt.Println()
	fmt.Println(strings.Repeat("-", 64))
	fmt.Printf("Total tool calls: %d\n", toolCallCount)

	if toolCallCount == 1 {
		fmt.Println("✅ Success! The agent correctly called the tool only once.")
		if *isolate {
			fmt.Println("   WithSubgraphIsolatedMessages(true) properly isolates parent history")
			fmt.Println("   while preserving the current invocation's tool call history.")
		}
	} else if toolCallCount > 1 {
		fmt.Println("⚠️  Multiple tool calls detected - this may indicate an issue.")
	}

	return nil
}

// calculator implements the calculator tool
func calculator(_ context.Context, args calculatorArgs) (calculatorResult, error) {
	var result float64
	switch strings.ToLower(args.Operation) {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		if args.B == 0 {
			return calculatorResult{}, fmt.Errorf("cannot divide by zero")
		}
		result = args.A / args.B
	default:
		return calculatorResult{}, fmt.Errorf("unsupported operation: %s", args.Operation)
	}
	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}, nil
}

type calculatorArgs struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
}

type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}
