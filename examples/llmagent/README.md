# LLMAgent Example

This example demonstrates how to use the `LLMAgent` implementation that was created to satisfy the `agent.Agent` interface.

## What is LLMAgent?

The `LLMAgent` is a concrete implementation of the `agent.Agent` interface that uses language models (LLMs) to generate responses. It leverages the `llmflow` package internally to handle the execution flow.

### Key Features

- **Implements `agent.Agent` interface**: Provides a `Run` method that accepts a context and invocation, returning a channel of events
- **Configurable**: Supports custom channel buffer sizes, request processors, and response processors
- **Flow-based execution**: Uses the `llmflow` package for handling LLM interactions
- **Event-driven**: Communicates through events that can include LLM responses, errors, and metadata

## Prerequisites

- Go 1.23 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required, automatically read by OpenAI SDK) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint (automatically read by OpenAI SDK) | `https://api.openai.com/v1` |

**Note**: `OPENAI_API_KEY` and `OPENAI_BASE_URL` are automatically read by the OpenAI SDK. You don't need to manually read these environment variables in your code. The SDK handles this automatically when creating the client.

## Command Line Arguments

| Argument | Description | Default Value |
|----------|-------------|---------------|
| `-model` | Name of the model to use | `gpt-4o-mini` |

## Usage

### Basic Usage

```bash
cd examples/llmagent
export OPENAI_API_KEY="your-api-key-here"
go run main.go
```

### With Custom Configuration

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"
go run main.go -model gpt-4
```

## Architecture

The LLMAgent follows this architecture:

```
LLMAgent
├── Implements agent.Agent interface
├── Uses llmflow.Flow internally
├── Configurable via Options struct
└── Returns events via channel

Components:
- Model: OpenAI-compatible model for LLM calls
- RequestProcessors: Process requests before sending to LLM
- ResponseProcessors: Process responses after receiving from LLM
- Flow: Handles the execution logic and event generation
```

## Example Output

When you run the example, you might see output like:

```
Creating LLMAgent with configuration:
- Base URL: https://api.openai.com/v1
- Model Name: gpt-4o-mini
- API Key: sk-***
Created LLMAgent: demo-llm-agent

=== LLMAgent Execution ===
Processing events from LLMAgent:

--- Event 1 ---
ID: 550e8400-e29b-41d4-a716-446655440000
Author: demo-llm-agent
InvocationID: demo-invocation-001
Error: no model available for LLM call (Type: flow_error)
Done: true

=== Execution Complete ===
Total events processed: 1
```

**Note**: Since this basic example doesn't provide request processors, the agent has no instructions on what to request from the LLM, resulting in an error. In a real implementation, you would add request processors that prepare the LLM request with system messages, user input, etc.

## Advanced Usage

To create a more functional LLMAgent, you would typically:

1. **Add Request Processors**: These prepare the LLM request with appropriate messages
2. **Add Response Processors**: These handle the LLM responses and can trigger additional actions
3. **Configure Buffer Sizes**: Optimize for your specific throughput requirements

Example with function options:

```go
agent := llmagent.New(
    "advanced-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("An advanced AI assistant"),
    llmagent.WithInstruction("Your agent instruction"),
    llmagent.WithSystemPrompt("Your system prompt"),
    llmagent.WithTools(tools),
    llmagent.WithPlanner(planner),
)
```
