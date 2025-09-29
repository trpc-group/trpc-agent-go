# LLMAgent Example

This example demonstrates how to use the `LLMAgent` implementation with an interactive chat interface.

## What is LLMAgent?

The `LLMAgent` is a concrete implementation of the `agent.Agent` interface that uses language models (LLMs) to generate responses. It leverages the `llmflow` package internally to handle the execution flow.

### Key Features

- **🔄 Interactive Chat**: Multi-turn conversation interface with streaming responses
- **🌊 Streaming Control**: Choose between real-time streaming or batch responses
- **🚀 Simple Interface**: Clean, focused chat experience
- **🔧 Implements `agent.Agent` interface**: Provides a `Run` method that accepts a context and invocation, returning a channel of events
- **⚙️ Configurable**: Supports custom channel buffer sizes, request processors, and response processors
- **🌊 Flow-based execution**: Uses the `llmflow` package for handling LLM interactions
- **⚡ Event-driven**: Communicates through events that can include LLM responses, errors, and metadata

## Prerequisites

- Go 1.21 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

| Variable          | Description                                                                | Default Value               |
| ----------------- | -------------------------------------------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required, automatically read by OpenAI SDK) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint (automatically read by OpenAI SDK)     | `https://api.openai.com/v1` |

**Note**: `OPENAI_API_KEY` and `OPENAI_BASE_URL` are automatically read by the OpenAI SDK. You don't need to manually read these environment variables in your code. The SDK handles this automatically when creating the client.

## Command Line Arguments

| Argument     | Description              | Default Value   |
| ------------ | ------------------------ | --------------- |
| `-model`     | Name of the model to use | `deepseek-chat` |
| `-streaming` | Enable streaming mode    | `true`          |

## Usage

### Basic Chat

```bash
cd examples/llmagent
export OPENAI_API_KEY="your-api-key-here"
go run . # or: go run main.go
```

### Custom Model

```bash
export OPENAI_API_KEY="your-api-key"
go run . -model gpt-4o # or: go run main.go -model gpt-4o
```

### Response Modes

Choose between streaming and non-streaming responses:

```bash
# Default streaming mode (real-time character output)
go run . # or: go run main.go

# Non-streaming mode (complete response at once)
go run . -streaming=false # or: go run main.go -streaming=false
```

## Chat Interface

The interface is simple and intuitive:

```
🚀 Interactive Chat with LLMAgent
Model: deepseek-chat
Streaming: true
==================================================
✅ Chat ready!

💡 Commands:
   /exit     - End the conversation

👤 You: Hello! Can you help me with a question?
🤖 Assistant: Of course! I'd be happy to help. What's your question?

👤 You: What is the capital of France?
🤖 Assistant: The capital of France is **Paris**! 🇫🇷

It's a beautiful city known for its iconic landmarks like the Eiffel Tower, the Louvre Museum, and Notre-Dame Cathedral. Paris is also famous for its art, fashion, and delicious cuisine. Have you ever been, or are you planning a visit?

👤 You: /exit
👋 Goodbye!
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
- **🤖 Model**: OpenAI-compatible model for LLM calls
- **⚙️ RequestProcessors**: Process requests before sending to LLM
- **⚡ ResponseProcessors**: Process responses after receiving from LLM
- **🌊 Flow**: Handles the execution logic and event generation
```

## Advanced Usage

To create a more functional LLMAgent, you would typically:

1. **🔧 Add Request Processors**: These prepare the LLM request with appropriate messages
2. **⚡ Add Response Processors**: These handle the LLM responses and can trigger additional actions
3. **⚙️ Configure Buffer Sizes**: Optimize for your specific throughput requirements

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
