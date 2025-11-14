//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a debug server with GraphAgent for mathematical processing.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/server/debug"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const defaultListenAddr = ":8080"

// State keys for math workflow
const (
	stateKeyOriginalExpression = "original_expression"
	stateKeyParsedNumbers      = "parsed_numbers"
	stateKeyOperation          = "operation"
	stateKeyProcessingStage    = "processing_stage"
	stateKeyCalculationResult  = "calculation_result"
)

// calculatorArgs holds the input for the calculator tool.
type calculatorArgs struct {
	Operation string  `json:"operation" description:"The operation to perform: add, subtract, multiply, divide"`
	A         float64 `json:"a" description:"First number operand"`
	B         float64 `json:"b" description:"Second number operand"`
}

// calculatorResult holds the output for the calculator tool.
type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

func main() {
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	addr := flag.String("addr", defaultListenAddr, "Listen address")
	flag.Parse()

	// Create agents map with both LLM and Graph agents
	agents := map[string]agent.Agent{
		"simple-agent":   createSimpleAgent(*modelName),
		"workflow-agent": createMathWorkflowAgent(*modelName),
	}

	// Create debug server
	server := debug.New(agents)

	fmt.Printf("🚀 Graph Debug Server starting on %s\n", *addr)
	fmt.Printf("📊 Available agents:\n")
	for name, ag := range agents {
		fmt.Printf("  - %s (%T)\n", name, ag)
	}
	fmt.Printf("🌐 ADK Web UI: http://localhost:4200\n")
	fmt.Printf("📡 API Endpoint: http://localhost%s\n", *addr)

	http.Handle("/", server.Handler())
	log.Fatal(http.ListenAndServe(*addr, nil))
}

// createSimpleAgent creates a simple LLM agent for comparison with the Graph Agent.
// This agent directly uses tool calling for mathematical calculations without workflow steps.
func createSimpleAgent(modelName string) agent.Agent {
	modelInstance := openai.New(modelName)

	calculatorTool := function.NewFunctionTool(
		calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform basic mathematical calculations"),
	)

	return llmagent.New(
		"simple-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A simple calculator agent"),
		llmagent.WithInstruction("Help users with mathematical calculations using the calculator tool when needed"),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(1000),
			Temperature: floatPtr(0.7),
			Stream:      true,
		}),
		llmagent.WithTools([]tool.Tool{calculatorTool}),
	)
}

// createMathWorkflowAgent creates a GraphAgent with a multi-stage mathematical processing workflow.
// The workflow consists of: parse_input → analyze → tools → format_result
// This demonstrates how Graph Agents can implement complex, stateful processing pipelines.
func createMathWorkflowAgent(modelName string) agent.Agent {
	// Create extended state schema for messages and metadata
	schema := graph.MessagesStateSchema()

	// Create model instance
	modelInstance := openai.New(modelName)

	// Create calculator tool
	calculatorTool := function.NewFunctionTool(
		calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform basic mathematical calculations"),
	)

	// Create node callbacks for monitoring
	callbacks := createNodeCallbacks()

	// Create stateGraph with schema and callbacks
	stateGraph := graph.NewStateGraph(schema).WithNodeCallbacks(callbacks)
	tools := map[string]tool.Tool{
		"calculator": calculatorTool,
	}

	// Build the workflow graph - following the official pattern
	stateGraph.
		// Add preprocessing node to parse input
		AddNode("parse_input", parseInputNode).

		// Add LLM analyzer node that determines what calculation to do
		AddLLMNode("analyze", modelInstance,
			`You are a math expression analyzer. Look at the input and determine what mathematical operation should be performed.

If you see a mathematical expression like "3*4", "5+2", "10/2", etc., use the calculator tool to compute it.

IMPORTANT: You MUST use the calculator tool with the appropriate operation and numbers.

Available operations: add, subtract, multiply, divide

Example: For "3*4", call calculator with operation="multiply", a=3, b=4`,
			tools).
		AddToolsNode("tools", tools).

		// Add result formatting node
		AddNode("format_result", formatResultNode).

		// Set up the workflow routing
		SetEntryPoint("parse_input").
		SetFinishPoint("format_result")

	// Add workflow edges - let tools return to analyze for result processing
	stateGraph.AddEdge("parse_input", "analyze")
	stateGraph.AddToolsConditionalEdges("analyze", "tools", "format_result")
	stateGraph.AddEdge("tools", "analyze")         // Tools return to analyze to process results
	stateGraph.AddEdge("analyze", "format_result") // Direct path when no tools needed or after tool processing

	// Build and compile the graph
	workflowGraph, err := stateGraph.Compile()
	if err != nil {
		log.Fatalf("Failed to create math workflow graph: %v", err)
	}

	// Create GraphAgent
	graphAgent, err := graphagent.New("workflow-agent", workflowGraph,
		graphagent.WithDescription("Mathematical processing workflow with calculator"),
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		log.Fatalf("Failed to create graph agent: %v", err)
	}

	return graphAgent
}

// createNodeCallbacks creates callbacks for monitoring like the official example
func createNodeCallbacks() *graph.NodeCallbacks {
	callbacks := graph.NewNodeCallbacks()

	// Before node callback
	callbacks.RegisterBeforeNode(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State) (any, error) {
		fmt.Printf("🚀 Starting node: %s (%s) at step %d\n",
			callbackCtx.NodeID, callbackCtx.NodeType, callbackCtx.StepNumber)
		return nil, nil
	})

	// After node callback
	callbacks.RegisterAfterNode(func(
		ctx context.Context,
		callbackCtx *graph.NodeCallbackContext,
		state graph.State,
		result any,
		nodeErr error,
	) (any, error) {
		if nodeErr == nil {
			fmt.Printf("✅ Completed node: %s (%s)\n", callbackCtx.NodeID, callbackCtx.NodeType)
		}
		return result, nil
	})

	// Error callback
	callbacks.RegisterOnNodeError(func(
		ctx context.Context,
		callbackCtx *graph.NodeCallbackContext,
		state graph.State,
		err error,
	) {
		fmt.Printf("❌ [CALLBACK] Error in node: %s (%s) at step %d\n",
			callbackCtx.NodeName, callbackCtx.NodeType, callbackCtx.StepNumber)
		fmt.Printf("   Error details: %v\n", err)

		switch callbackCtx.NodeType {
		case graph.NodeTypeLLM:
			fmt.Printf("   🤖 LLM node error - this might be a model API issue\n")
		case graph.NodeTypeTool:
			fmt.Printf("   🔧 Tool execution error - check tool implementation\n")
		case graph.NodeTypeFunction:
			fmt.Printf("   ⚙️  Function node error - check business logic\n")
		}
	})

	return callbacks
}

// Node function implementations

// parseInputNode preprocesses the user input and sets up initial state for the workflow.
// This is the entry point of the Graph Agent's mathematical processing pipeline.
func parseInputNode(ctx context.Context, state graph.State) (any, error) {
	// Get input from GraphAgent's state fields
	var input string
	if userInput, ok := state[graph.StateKeyUserInput].(string); ok {
		input = userInput
	}
	if input == "" {
		return nil, errors.New("no input expression found")
	}

	// Basic preprocessing
	input = strings.TrimSpace(input)
	if len(input) < 1 {
		return nil, errors.New("expression too short for processing")
	}

	fmt.Printf("📝 Parsing input: %s\n", input)

	// Return state with preprocessing results
	return graph.State{
		stateKeyOriginalExpression: input,
		stateKeyProcessingStage:    "input_parsed",
	}, nil
}

func formatResultNode(ctx context.Context, state graph.State) (any, error) {
	// Try to get the last response from LLM or tool results
	var content string

	// Look for tool results first
	if msgs, ok := state[graph.StateKeyMessages].([]model.Message); ok {
		for i := len(msgs) - 1; i >= 0; i-- {
			msg := msgs[i]
			if msg.Role == model.RoleTool {
				// Try to parse calculator result
				var result calculatorResult
				if err := json.Unmarshal([]byte(msg.Content), &result); err == nil {
					content = fmt.Sprintf("The result of %s %.2f %s %.2f is %.2f",
						result.Operation, result.A, getOperatorSymbol(result.Operation), result.B, result.Result)
					break
				}
			}
		}
	}

	// Fallback to last response
	if content == "" {
		if lastResponse, ok := state[graph.StateKeyLastResponse].(string); ok && strings.TrimSpace(lastResponse) != "" {
			content = lastResponse
		} else {
			content = "Calculation completed but no result found"
		}
	}

	originalExpr, _ := state[stateKeyOriginalExpression].(string)

	finalOutput := fmt.Sprintf("Calculation result: %s = %s",
		originalExpr,
		content)

	return graph.State{
		graph.StateKeyLastResponse: finalOutput,
	}, nil
}

// getOperatorSymbol converts operation names to mathematical symbols.
// Used by the calculate function to display the operation in a user-friendly format.
func getOperatorSymbol(operation string) string {
	switch strings.ToLower(operation) {
	case "add":
		return "+"
	case "subtract":
		return "-"
	case "multiply":
		return "*"
	case "divide":
		return "/"
	default:
		return operation
	}
}

// calculate performs mathematical operations (add, subtract, multiply, divide) on two numbers.
// This function is exposed as a tool to both the simple-agent and the Graph Agent's workflow.
// It validates inputs, performs the calculation, and returns a formatted result.
func calculate(ctx context.Context, args calculatorArgs) (calculatorResult, error) {
	fmt.Printf("🔧 CALCULATOR CALLED: %s %.2f %.2f\n", args.Operation, args.A, args.B)
	var result float64
	switch strings.ToLower(args.Operation) {
	case "add":
		result = args.A + args.B
	case "subtract":
		result = args.A - args.B
	case "multiply":
		result = args.A * args.B
	case "divide":
		if args.B != 0 {
			result = args.A / args.B
		} else {
			fmt.Printf("❌ CALCULATOR ERROR: division by zero\n")
			return calculatorResult{}, fmt.Errorf("division by zero")
		}
	default:
		fmt.Printf("❌ CALCULATOR ERROR: unsupported operation %s\n", args.Operation)
		return calculatorResult{}, fmt.Errorf("unsupported operation: %s", args.Operation)
	}
	fmt.Printf("✅ CALCULATOR RESULT: %.2f\n", result)
	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}, nil
}

// intPtr returns a pointer to the given int value.
func intPtr(i int) *int {
	return &i
}

// floatPtr returns a pointer to the given float64 value.
func floatPtr(f float64) *float64 {
	return &f
}
