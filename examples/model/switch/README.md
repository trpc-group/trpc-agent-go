# Model Switching Example

This example demonstrates two types of model switching using LLMAgent without the Runner:

1. **Agent-level switching** (`/switch`): Changes the agent's default model, affecting all subsequent requests.
2. **Per-request switching** (`/model`): Overrides the model for a single request only, without affecting the agent's default.

## Prerequisites

- Go 1.21 or later
- Valid OpenAI API key (or compatible API endpoint).

## Overview

The example shows how to:

1. Pre-register multiple models using `WithModels` when creating the agent.
2. Specify an initial model with `WithModel`.
3. **Agent-level switching**: Use `SetModelByName(modelName)` to change the agent's default model for all subsequent requests.
4. **Per-request switching**: Use `agent.WithModelName()` in RunOptions to override the model for a single request only.
5. Configure streaming mode via command-line flag.
6. Send user messages and print assistant responses.

## Key Features

1. **Pre-registered Models**: Models are registered once with `WithModels` at agent creation.
2. **Agent-level Switching**: Use `SetModelByName(modelName)` to change the agent's default model for all subsequent requests.
3. **Per-request Switching**: Use `agent.WithModelName()` in RunOptions to override the model for a single request only.
4. **Error Handling**: `SetModelByName` returns an error if the model name is not found.
5. **Minimal Setup**: No Runner, no tools, only model switching logic.
6. **Interactive Commands**: Use `/switch` for agent-level and `/model` for per-request switching.
7. **Session Management**: Simple session ID for telemetry is handled internally.
8. **Streaming Support**: Responses are streamed by default (controlled by the model).

## Environment Variables

The example supports the following environment variables (automatically read by the OpenAI SDK):

| Variable          | Description                              | Default Value               |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

Note: You do not need to read these variables in your own code; the SDK does it automatically when creating the client.

## Command Line Arguments

| Argument | Description          | Default Value   |
| -------- | -------------------- | --------------- |
| `-model` | Default model to use | `deepseek-chat` |

## Running the Example

### Using default values

```bash
cd examples/model/switch
go run main.go
```

### Using a custom default model

```bash
cd examples/model/switch
go run main.go -model deepseek-reasoner
```

### With environment variables

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"

cd examples/model/switch
go run main.go -model deepseek-chat
```

## Commands

- `/switch <model>`: **Agent-level switching** - Changes the agent's default model for all subsequent requests.
- `/model <model>`: **Per-request switching** - Uses the specified model for the next request only (agent default unchanged).
- `/new`: Starts a new session (resets session id used for telemetry).
- `/exit`: Exits the program.

## Example Output

```text
🚀 Model Switching (no runner)
Model: deepseek-chat
Commands: /switch X, /model X, /new, /exit
==================================================

✅ Chat ready! Session: session-1700000000

💡 Special commands:
   /switch <model>  - 🔄 Agent-level: change default model for all requests
   /model <model>   - 🎯 Per-request: use model for next request only
   /new             - 🆕 Start a new session
   /exit            - 👋 End the conversation

👤 You: What can you do?
🤖 I can help answer questions, assist with writing, summarize content, and more.

👤 You: /switch deepseek-reasoner
✅ Agent-level switch: all requests will now use deepseek-reasoner

👤 You: Write a haiku about code.
🤖 Silent lines compile
   Logic flows like mountain streams
   Bugs fade into dusk

👤 You: /model deepseek-chat
✅ Per-request mode: next request will use deepseek-chat (agent default unchanged)

👤 You: What is 2+2?
🔧 Per-request override: using model deepseek-chat for this request only
🤖 The answer is 4.

👤 You: What is the capital of France?
🤖 The capital of France is Paris.
(Note: This request uses deepseek-reasoner again, as the per-request override was only for one request)
```

## Package Usage

Below is the core idea used in this example.

### Agent-level Switching

```go
import (
    "context"
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Prepare models map.
models := map[string]model.Model{
    "deepseek-chat":     openai.New("deepseek-chat"),
    "deepseek-reasoner": openai.New("deepseek-reasoner"),
}

// Create agent with pre-registered models.
agt := llmagent.New("switching-agent",
    llmagent.WithModels(models),
    llmagent.WithModel(models["deepseek-chat"]),
)

// Agent-level switching: affects all subsequent requests.
err := agt.SetModelByName("deepseek-reasoner")
if err != nil {
    // Handle error: model not found.
}

// All subsequent requests will use deepseek-reasoner.
invocation := agent.NewInvocation(...)
events, _ := agt.Run(ctx, invocation)
```

### Per-request Switching

```go
// Per-request switching: affects only one request.
invocation := agent.NewInvocation(
    agent.WithInvocationSession(session),
    agent.WithInvocationMessage(model.NewUserMessage("Hello")),
    agent.WithInvocationRunOptions(agent.RunOptions{
        ModelName: "deepseek-chat", // Override for this request only.
    }),
)

// This request uses deepseek-chat, but agent's default remains deepseek-reasoner.
events, _ := agt.Run(ctx, invocation)

// Next request without override will use agent's default (deepseek-reasoner).
invocation2 := agent.NewInvocation(...)
events2, _ := agt.Run(ctx, invocation2)
```

## Comparison: Agent-level vs Per-request Switching

| Feature                  | Agent-level (`/switch`)    | Per-request (`/model`)      |
| ------------------------ | -------------------------- | --------------------------- |
| Scope                    | All subsequent requests    | Single request only         |
| Affects other requests   | Yes                        | No                          |
| Agent state modification | Yes (via `SetModelByName`) | No                          |
| Use case                 | Global model switching     | Dynamic per-request routing |
| Thread-safe              | Yes (with mutex)           | Yes                         |
| Command                  | `/switch <model>`          | `/model <model>`            |

## Important Notes

- **Pre-registration Required**: Models must be registered with `WithModels` before they can be switched by name.
- **Error Handling**: `SetModelByName` returns an error if the model name is not found.
- **Per-request Priority**: If `ModelName` is not found in registered models, falls back to agent's default model.
- **No Runner**: This example intentionally does not use the Runner.
- **No Tools**: The flow focuses purely on model switching and message handling.
- **Model Names**: Switching is based on exact model names (case-sensitive).
- **Streaming**: Responses are streamed by default (handled by the OpenAI model).
- **Telemetry**: A minimal session id is generated internally for tracing.

## Security Notes

- Never commit API keys to version control.
- Use environment variables or a secure configuration system.

## Use Cases

### Agent-level Switching (`/switch`)

- Change the default model for all users/sessions.
- Switch to a different model tier (e.g., from mini to full).
- Global configuration changes.

### Per-request Switching (`/model`)

- **Cost Optimization**: Use cheaper models for simple queries, expensive models for complex tasks.
- **Performance Tuning**: Switch to faster models for latency-sensitive requests.
- **A/B Testing**: Compare different models' responses for the same query.
- **User Preferences**: Allow users to select their preferred model per request.
- **Dynamic Routing**: Route requests to different models based on content complexity.

## Benefits

1. **Simplicity**: Minimal code focused on switching models by name.
2. **Flexibility**: Two switching modes for different use cases.
3. **Type Safety**: Error handling ensures you only switch to registered models.
4. **Maintainability**: No need to maintain model instances externally—just remember names.
5. **Isolation**: Per-request switching doesn't affect other concurrent requests.
6. **Separation of Concerns**: Agent handles LLM logic; example handles I/O and switching.
