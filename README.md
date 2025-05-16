# trpc-agent-go

A powerful Go framework for building intelligent agents with large language models and tools.

## Overview

trpc-agent-go is a framework for creating, configuring, and deploying AI agents built with large language models (LLMs) and tools. It enables the development of sophisticated agents that can reason, use tools, maintain memory, and follow complex workflows.

## Features

- **Multiple Agent Types**: Includes ReAct agents (reasoning and acting), sequential agents, parallel agents, and more
- **Tool Integration**: Easily integrate custom tools with a simple interface
- **Flexible Memory**: Built-in memory systems for short and long-term information retention
- **Workflow Management**: Organize complex workflows with flow and planning components
- **Event-Driven Architecture**: Fully event-driven system for maximum flexibility
- **A2A Protocol Support**: Implements the Agent-to-Agent protocol for standardized communication
- **Schema-Based Parameter Handling**: Robust parameter extraction and type conversion
- **Evaluation Framework**: Built-in evaluation capabilities for agent performance assessment

## Getting Started

### Prerequisites

- Go 1.19 or later
- API key for your chosen LLM provider (e.g., Google, OpenAI)

### Installation

```bash
go get trpc.group/trpc-go/trpc-agent-go
```

### Basic Usage

Here's a simple example of creating a ReAct agent:

```go
package main

import (
	"context"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/agent/agents/react"
	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model/models"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func main() {
	// Create a simple calculator tool
	calculatorTool := NewCalculatorTool()

	// Create a tool registry with the calculator tool
	registry := tool.NewRegistry()
	registry.Register(calculatorTool)

	// Create a Gemini model
	model := models.NewGeminiModel(&models.GeminiConfig{
		APIKey: os.Getenv("GOOGLE_API_KEY"),
	})

	// Create a ReAct agent with the model and tools
	agent := react.NewReActAgent(&react.ReActAgentConfig{
		Model:        model,
		ToolRegistry: registry,
	})

	// Run the agent with a message
	msg := message.NewUserMessage("Calculate the square root of 225")
	resp, err := agent.Run(context.Background(), msg)
	if err != nil {
		log.Fatalf("Error running agent: %v", err)
	}

	// Print the response
	log.Infof("Agent response: %s", resp.Content)
}
```

## Architecture

The framework is organized into several key packages:

- **agent**: Core agent interfaces and implementations
  - **agents**: Specific agent implementations (ReAct, sequential, parallel)
- **tool**: Tools that agents can use to perform actions
- **model**: LLM integrations (Gemini, OpenAI, etc.)
- **memory**: Memory systems for retaining information
- **message**: Message types and utilities
- **event**: Event handling and propagation
- **flow**: Workflow management
- **planner**: Planning capabilities

## Examples

The repository includes several examples:

- **A2A Example**: Implements an A2A (Agent-to-Agent) server with both local tools and MCP integration
- **Simple Agent Server**: A basic agent server implementation
- **Calculator Agent**: Demonstrates a specialized calculation agent

See the [examples](./examples) directory for more details.

## Tool System

The framework includes a flexible tool system that allows agents to interact with external systems:

```go
// Create a custom tool
type MyTool struct {
    tool.BaseTool
}

func NewMyTool() *MyTool {
    parameters := map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "input": map[string]interface{}{
                "type": "string",
                "description": "The input to process",
            },
        },
        "required": []string{"input"},
    }

    return &MyTool{
        BaseTool: *tool.NewBaseTool(
            "my_tool",
            "Processes input in a custom way",
            parameters,
        ),
    }
}

func (t *MyTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
    input, _ := args["input"].(string)
    // Process the input
    output := "Processed: " + input
    return tool.NewResult(output), nil
}
```

## ReAct Agent

The ReAct agent uses a reasoning and acting approach:

1. **Thinking**: Formulates a plan to solve the problem
2. **Acting**: Selects appropriate tools and executes them
3. **Observing**: Processes the results from tools
4. **Repeating**: Continues the process until the task is complete

## Schema-Based Parameter Handling

The framework includes robust parameter processing:

1. **Schema Extraction**: Parameters are defined with JSON Schema
2. **Parameter Inference**: Identifies primary parameters in ambiguous formats
3. **Structured Parsing**: Handles JSON, key-value pairs, and more
4. **Type Conversion**: Automatically converts values to expected types

## Using the Library

To create and use agents:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

// Create tools for your agent
calculator := tool.NewCalculatorTool()

// Create the agent
agent := agent.NewReActAgent(
    agent.WithTools([]tool.Tool{calculator}),
    agent.WithModel(openaiModel),
)

// Run the agent
response, err := agent.Run(ctx, "Calculate 1 + 2")
```

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the [Apache License 2.0](LICENSE).
