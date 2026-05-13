# Model Selector Example

This example demonstrates `agent.WithModelSelector` with `runner.Run`.
It uses a real OpenAI-compatible model provider and a real local function
tool. It does not use mock models.

The run is built to produce two framework-managed LLM calls:

1. The first LLM call uses `-tool-call-model` and decides to call
   `calculator`.
2. After the tool result is available, the second LLM call uses
   `-final-model` and writes the final answer.

The example reads `OPENAI_BASE_URL` from the environment when present, and the OpenAI adapter reads the API key from environment variables. The tool marks the invocation state after it runs, and the selector reads that state on the next LLM call. This keeps the routing logic explicit and application-owned.

## Prerequisites

- Go 1.24 or later for the `examples` module.
- A valid API key for an OpenAI-compatible endpoint.
- A model endpoint that supports tool calling.

## Environment Variables

```bash
export OPENAI_BASE_URL="your-openai-compatible-base-url"
export OPENAI_API_KEY="your-api-key"
```

## Run

```bash
cd examples/model/selector
go run .
```

With explicit model names:

```bash
cd examples/model/selector
go run . \
  -tool-call-model deepseek-v4-flash \
  -final-model deepseek-v4-flash
```

With explicit shared endpoint configuration:

```bash
cd examples/model/selector
OPENAI_BASE_URL="https://provider.example/v1" \
OPENAI_API_KEY="your-api-key" \
go run . -tool-call-model model-a -final-model model-b
```

## Key API

```go
events, err := r.Run(
    ctx,
    userID,
    sessionID,
    model.NewUserMessage(text),
    agent.WithModelSelector(func(ctx context.Context, inv *agent.Invocation) (model.Model, error) {
        checked, ok := agent.GetStateValue[bool](inv, "example:model_selector:calculator_called")
        if ok && checked {
            return finalModel, nil
        }
        return toolCallModel, nil
    }),
)
```

`ModelSelector` returns a `model.Model`, not just a model name. In this example, both candidates share the same OpenAI-compatible endpoint and credentials, so the returned models only differ in the model identifier.
