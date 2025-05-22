# trpc-agent-go

A powerful Go framework for building intelligent agent systems using large language models (LLMs) and tools.

## Overview

trpc-agent-go is a comprehensive framework for creating, configuring, and deploying AI agents built with large language models and tools. It enables the development of sophisticated agents that can reason, use tools, maintain memory, and follow complex workflows through a flexible graph-based orchestration system.

## Key Features

- **Graph-Based Orchestration**: Design complex agent workflows using a composable graph architecture
- **Multiple Node Types**:
  - **Model Nodes**: Process messages using language models
  - **Tool Nodes**: Execute actions through integrated tools
  - **Agent Nodes**: Implement complete agent patterns
  - **Prompt Nodes**: Add predefined prompts to conversations
- **Streaming Support**: First-class support for streaming responses
- **Event-Driven Architecture**: Fully event-driven system for maximum flexibility
- **A2A Protocol Support**: Implements the Agent-to-Agent protocol for standardized communication
- **Session Management**: Built-in session handling for multi-turn conversations
- **Tool Integration**: Easily integrate custom tools with a simple interface
- **Schema-Based Parameter Handling**: Robust parameter extraction and type conversion

## Getting Started

### Prerequisites

- Go 1.19 or later
- API key for your chosen LLM provider (e.g., OpenAI, Google)

### Installation

```bash
go get trpc.group/trpc-go/trpc-agent-go
```

### Basic Usage

Here's a simple example of creating an agent with the graph-based orchestration system:

```go
package main

import (
	"context"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/graph"
)

func main() {
	// Create a calculator tool
	calculatorTool := NewCalculatorTool()
	
	// Create a tool set
	toolSet := tool.NewToolSet()
	toolSet.Add(calculatorTool)
	
	// Create an OpenAI model
	openAIModel := model.NewOpenAIModel(
		"gpt-3.5-turbo",
		model.WithOpenAIAPIKey(os.Getenv("OPENAI_API_KEY")),
	)
	
	// Create a graph
	g := graph.NewGraph("Calculator Agent", "An agent that can perform calculations")
	
	// Add nodes to the graph
	g.AddNode("model", graph.NewModelNode(
		openAIModel,
		"calculator_model",
		"Processes user requests and decides what calculations to perform",
		graph.WithSystem("You are a helpful calculator assistant."),
	))
	
	g.AddNode("tool", graph.NewToolNode(
		calculatorTool,
		"calculator_tool",
		"Performs arithmetic operations",
		func(ctx context.Context, msg *message.Message) (map[string]interface{}, error) {
			// Extract tool arguments from the message
			// This is a simplified example
			return map[string]interface{}{
				"operation": "add",
				"a": 5,
				"b": 3,
			}, nil
		},
	))
	
	// Connect the nodes
	g.AddEdge("model", "tool")
	g.AddEdge("tool", "model")
	
	// Set the start node
	g.SetStartNode("model")
	
	// Create a graph runner
	runner, err := graph.NewGraphRunner(g)
	if err != nil {
		panic(err)
	}
	
	// Run the graph with a user message
	userMsg := message.NewUserMessage("What is 5 + 3?")
	
	// For streaming output
	eventCh, err := runner.ExecuteStream(context.Background(), userMsg)
	if err != nil {
		panic(err)
	}
	
	// Process events
	for event := range eventCh {
		switch event.Type {
		case event.TypeStreamChunk:
			// Handle streaming chunks
			fmt.Print(event.Data["content"])
		case event.TypeMessage:
			// Handle complete messages
			msg := event.Message
			fmt.Println(msg.Content)
		}
	}
}
```

## Architecture

The framework is organized into several key packages:

- **core**: Core components and interfaces
  - **model**: LLM integrations (OpenAI, etc.)
  - **tool**: Tools that agents can use to perform actions
  - **message**: Message types and utilities
  - **event**: Event handling and propagation
  - **agent**: Basic agent interfaces
- **orchestration**: Advanced orchestration capabilities
  - **graph**: Graph-based execution system
  - **session**: Session management
  - **runner**: Execution runners
  - **planner**: Planning capabilities
  - **flow**: Workflow management
  - **prompt**: Prompt management

## Graph-Based Orchestration

The framework's graph system allows you to create complex agent workflows:

1. **Nodes**: Represent processing units (models, tools, agents, prompts)
2. **Edges**: Define the flow between nodes
3. **Conditional Edges**: Enable dynamic routing based on conditions
4. **Streaming**: First-class support for streaming responses

Example graph nodes:

- **ModelNode**: Processes messages using language models
- **ToolNode**: Executes tools with extracted arguments
- **AgentNode**: Implements a complete agent pattern
- **PromptNode**: Adds predefined prompts to conversations

## Tool System

Create custom tools easily:

```go
// Create a custom calculator tool
type CalculatorTool struct {
	tool.BaseTool
}

func NewCalculatorTool() *CalculatorTool {
	parameters := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"operation": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"add", "subtract", "multiply", "divide"},
				"description": "The arithmetic operation to perform",
			},
			"a": map[string]interface{}{
				"type":        "number",
				"description": "The first operand",
			},
			"b": map[string]interface{}{
				"type":        "number",
				"description": "The second operand",
			},
		},
		"required": []string{"operation", "a", "b"},
	}

	return &CalculatorTool{
		BaseTool: *tool.NewBaseTool(
			"calculator",
			"Performs basic arithmetic operations",
			parameters,
		),
	}
}

func (t *CalculatorTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	operation, _ := args["operation"].(string)
	a, _ := args["a"].(float64)
	b, _ := args["b"].(float64)

	var result float64
	switch operation {
	case "add":
		result = a + b
	case "subtract":
		result = a - b
	case "multiply":
		result = a * b
	case "divide":
		if b == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		result = a / b
	default:
		return nil, fmt.Errorf("unsupported operation: %s", operation)
	}

	return tool.NewResult(result), nil
}
```

## Examples

The repository includes several examples:

- **Basic**: Simple agents demonstrating core functionality
- **A2A**: Agent-to-Agent protocol implementation
- **Session Server**: Multi-turn conversations with session management
- **Streaming Graph**: Demonstrates streaming responses with graph orchestration

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the [Apache License 2.0](LICENSE).
