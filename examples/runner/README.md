# Runner Example

This example demonstrates how to use the `Runner` orchestration component with an `LLMAgent` and OpenAI-like model with environment variable configuration.

## What is Runner?

The `Runner` is an orchestration component that provides a higher-level interface for running agents. It manages agent execution and can integrate with session services for conversation management.

### Key Features

- **Agent Orchestration**: Provides a clean interface for running agents with user messages
- **Session Management**: Can integrate with session services for conversation state
- **Event-driven**: Returns streaming events from agent execution
- **Configurable**: Supports different agent types and session services

## Prerequisites

- Go 1.23 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

The example supports the following environment variables:

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `MODEL_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |
| `MODEL_NAME` | Name of the model to use | `gpt-4o-mini` |

## Usage

### Basic Usage

```bash
cd examples/runner
export OPENAI_API_KEY="your-api-key-here"
go run main.go
```

### With Custom Configuration

```bash
export OPENAI_API_KEY="your-api-key"
export MODEL_BASE_URL="https://api.openai.com/v1"
export MODEL_NAME="gpt-4"
go run main.go
```

### Using environment variables inline

```bash
cd examples/runner
OPENAI_API_KEY="your-api-key" MODEL_BASE_URL="https://api.openai.com/v1" MODEL_NAME="gpt-4o-mini" go run main.go
```

## Architecture

The Runner example follows this architecture:

```
Runner
├── Manages Agent execution
├── Provides streaming interface
├── Handles user messages and sessions
└── Returns events via channel

Components:
1. OpenAI-like Model: Handles LLM API calls
2. LLMAgent: Implements agent logic with request/response processors
3. Runner: Orchestrates agent execution
4. Streaming: Real-time response processing
```

## Example Output

When you run the example, you will see output like:

```
Creating Runner with configuration:
- Base URL: https://api.openai.com/v1
- Model Name: gpt-4o-mini
- API Key: sk-***

Created Runner: runner-demo-app with agent: assistant-agent

=== Runner Streaming Execution ===
User: Hello! Can you tell me an interesting fact about Go programming language concurrency features?
Starting streaming response... 