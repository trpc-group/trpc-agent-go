# Requesty Example

This example demonstrates how to use the OpenAI-compatible model implementation
with the [Requesty](https://requesty.ai) router. Requesty exposes an
OpenAI-compatible Chat Completions API, so it works with the existing
`model/openai` client by pointing the base URL at the Requesty router and
supplying a Requesty API key.

## Overview

Requesty is an OpenAI-compatible LLM router. Because the framework already
drives any OpenAI-compatible endpoint, no new provider code is required: you
create an `openai.Model` and override the base URL and API key.

- Base URL: `https://router.requesty.ai/v1`
- Auth: bearer key from the `REQUESTY_API_KEY` environment variable
- Model naming: `provider/model`, e.g. `openai/gpt-4o-mini`

## Prerequisites

- Go installed and the project dependencies available.
- A Requesty API key. Create one at <https://app.requesty.ai/api-keys>.
- Browse available models at <https://app.requesty.ai/router/list>.

## Environment Variables

| Variable           | Description                                      | Default |
| ------------------ | ------------------------------------------------ | ------- |
| `REQUESTY_API_KEY` | API key for the Requesty router (required)       | ``      |

## Command Line Arguments

| Argument     | Description                          | Default Value             |
| ------------ | ------------------------------------ | ------------------------- |
| `-model`     | Name of the model to use             | `openai/gpt-4o-mini`      |
| `-base-url`  | Requesty router base URL             | `https://router.requesty.ai/v1` |

## How It Works

The example creates an OpenAI-compatible model instance and explicitly sets the
base URL and API key:

```go
import (
    "os"

    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

llm := openai.New("openai/gpt-4o-mini",
    openai.WithBaseURL("https://router.requesty.ai/v1"),
    openai.WithAPIKey(os.Getenv("REQUESTY_API_KEY")),
)
```

Alternatively, you can rely on the OpenAI SDK environment variables instead of
the explicit options:

```bash
export OPENAI_API_KEY="$REQUESTY_API_KEY"
export OPENAI_BASE_URL="https://router.requesty.ai/v1"
```

## Running the Example

```bash
export REQUESTY_API_KEY="your-requesty-key"

cd examples/model/requesty
go run main.go
```

### Using a custom model:

```bash
cd examples/model/requesty
go run main.go -model openai/gpt-4o
```

## Example Output

The example runs two demonstrations:

1. **🔄 Non-streaming Example**: basic request/response with token usage.
2. **🌊 Streaming Example**: streaming response handling.

## Security Notes

- Never commit API keys to version control.
- Use environment variables or secure configuration management.
