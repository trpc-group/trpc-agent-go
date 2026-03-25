# Model Switching Example

This example demonstrates two ways to switch models when using `LLMAgent` with the runner:

1. **Agent-level switching** via `/switch`: updates the agent's default model for all subsequent requests.
2. **Per-request switching** via `/model`: overrides the model for one request only with `agent.WithModelName()`.

## Prerequisites

- Go 1.21 or later
- A valid API key for your OpenAI-compatible endpoint

## Environment variables

The OpenAI SDK reads these automatically:

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the model service | none |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

If you want to talk to the official DeepSeek API, point `OPENAI_BASE_URL` at `https://api.deepseek.com/v1`.

## Command-line arguments

| Argument | Description | Default |
| --- | --- | --- |
| `-model` | Default model to use | `deepseek-chat` |

## Run the example

```bash
cd examples/model/switch
go run main.go
```

With a different default model:

```bash
cd examples/model/switch
go run main.go -model deepseek-reasoner
```

With environment variables:

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"

cd examples/model/switch
go run main.go -model deepseek-chat
```

## Commands

- `/switch <model>`: change the agent's default model for all later requests
- `/model <model>`: use a different model for the next request only
- `/new`: start a new session
- `/exit`: exit the program

## Package usage

### Agent-level switching

```go
import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

func main() {
    ctx := context.Background()

    models := map[string]model.Model{
        "deepseek-chat": openai.New(
            "deepseek-chat",
            openai.WithVariant(openai.VariantDeepSeek),
        ),
        "deepseek-reasoner": openai.New(
            "deepseek-reasoner",
            openai.WithVariant(openai.VariantDeepSeek),
        ),
    }

    agt := llmagent.New(
        "switching-agent",
        llmagent.WithModels(models),
        llmagent.WithModel(models["deepseek-chat"]),
    )

    r := runner.NewRunner("app-name", agt)
    defer r.Close()

    if err := agt.SetModelByName("deepseek-reasoner"); err != nil {
        panic(err)
    }

    _, _ = r.Run(ctx, "user-1", "session-1", model.NewUserMessage("Hello"))
}
```

### Per-request switching

```go
events, err := r.Run(
    ctx,
    userID,
    sessionID,
    model.NewUserMessage("Hello"),
    agent.WithModelName("deepseek-chat"),
)
if err != nil {
    // Handle error.
}

// The next request without override will use the agent's default model again.
_ = events
```
