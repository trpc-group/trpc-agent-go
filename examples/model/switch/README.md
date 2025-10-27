# Model Switching (Runner-less) Example

This example demonstrates dynamic model switching using LLMAgent without the Runner. It showcases the recommended approach: pre-registering multiple models with `WithModels` and switching between them by name using `SetModelByName`.

## Prerequisites

- Go 1.21 or later
- Valid OpenAI API key (or compatible API endpoint).

## Overview

The example shows how to:

1. Pre-register multiple models using `WithModels` when creating the agent.
2. Specify an initial model with `WithModel`.
3. Switch the active model at runtime by name using `SetModelByName(modelName)`.
4. Send user messages and print assistant responses.

This approach simplifies model management by eliminating the need to maintain model instances externally—you only need to remember model names.

## Key Features

1. **Pre-registered Models**: Models are registered once with `WithModels` at agent creation.
2. **Name-based Switching**: Switch models by name using `SetModelByName(modelName)`.
3. **Error Handling**: `SetModelByName` returns an error if the model name is not found.
4. **Minimal Setup**: No Runner, no tools, only model switching logic.
5. **Interactive Switching**: Use `/switch <model>` to change the active model.
6. **Session Management**: Simple session ID for telemetry is handled internally.
7. **Streaming-Friendly**: Accumulates content from streaming or non-streaming responses.

## Environment Variables

The example supports the following environment variables (automatically read by the OpenAI SDK):

| Variable          | Description                              | Default Value               |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

Note: You do not need to read these variables in your own code; the SDK does it automatically when creating the client.

## Command Line Arguments

| Argument | Description          | Default Value |
| -------- | -------------------- | ------------- |
| `-model` | Default model to use | `gpt-4o-mini` |

## Running the Example

### Using default values

```bash
cd examples/model/switch
go run main.go
```

### Using a custom default model

```bash
cd examples/model/switch
go run main.go -model gpt-4o
```

### With environment variables

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.openai.com/v1"

cd examples/model/switch
go run main.go -model gpt-4o-mini
```

## Commands

- `/switch <model>`: Switches the active model to the specified name.
- `/new`: Starts a new session (resets session id used for telemetry).
- `/exit`: Exits the program.

## Example Output

```text
🚀 Model Switching (no runner)
Default model: gpt-4o-mini
Commands: /switch X, /new, /exit

✅ Ready. Session: session-1700000000

👤 You: What can you do?
🤖 I can help answer questions, assist with writing, summarize content, and more.

👤 You: /switch gpt-4o
✅ Switched model to: gpt-4o

👤 You: Write a haiku about code.
🤖 Silent lines compile
   Logic flows like mountain streams
   Bugs fade into dusk
```

## Package Usage

Below is the core idea used in this example.

```go
import (
    "context"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Prepare models map.
models := map[string]model.Model{
    "gpt-4o-mini":   openai.New("gpt-4o-mini"),
    "gpt-4o":        openai.New("gpt-4o"),
    "gpt-3.5-turbo": openai.New("gpt-3.5-turbo"),
}

// Create agent with pre-registered models.
// Use WithModels to register all models, and WithModel to set the initial model.
agt := llmagent.New("switching-agent",
    llmagent.WithModels(models),
    llmagent.WithModel(models["gpt-4o-mini"]),
)

// Switch active model by name.
err := agt.SetModelByName("gpt-4o") // e.g., after parsing "/switch gpt-4o"
if err != nil {
    // Handle error: model not found.
}

// Send a message.
ctx := context.Background()
// See the example for how invocation/session are handled internally.
```

## Important Notes

- **Pre-registration Required**: Models must be registered with `WithModels` before they can be switched by name.
- **Error Handling**: `SetModelByName` returns an error if the model name is not found.
- **No Runner**: This example intentionally does not use the Runner.
- **No Tools**: The flow focuses purely on model switching and message handling.
- **Model Names**: Switching is based on exact model names (case-sensitive).
- **Telemetry**: A minimal session id is generated internally for tracing.

## Security Notes

- Never commit API keys to version control.
- Use environment variables or a secure configuration system.

## Benefits

1. **Simplicity**: Minimal code focused on switching models by name.
2. **Flexibility**: Easily switch between models based on needs without managing instances.
3. **Type Safety**: Error handling ensures you only switch to registered models.
4. **Maintainability**: No need to maintain model instances externally—just remember names.
5. **Separation of Concerns**: Agent handles LLM logic; example handles I/O and switching.
