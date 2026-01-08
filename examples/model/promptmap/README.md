# Model Prompt Mapping Example

This example shows how to make one `LLMAgent` (Large Language Model agent)
use different prompt text depending on which model is used for a request.

## Why this exists

In real systems, you may switch models for cost, latency, or quality.
Different models can behave better with different system prompts.

Instead of creating multiple Agents, you can configure one Agent with:

- A default prompt (fallback).
- A per-model prompt map keyed by `model.Info().Name`.

## What the code does

1. Creates two models (A and B).
2. Creates one `LLMAgent` with:
   - Default `Instruction` and `GlobalInstruction`.
   - `WithModelInstructions` and `WithModelGlobalInstructions`.
3. Runs the same user message twice:
   - Once with the default model.
   - Once with a per-request `agent.WithModelName(...)` override.

## Prerequisites

- Go 1.24 or later (the `examples` module uses Go 1.24).
- A valid OpenAI-compatible Application Programming Interface (API) key.

## Environment variables

The OpenAI Software Development Kit (SDK) reads these automatically:

| Variable          | Description                              |
| ----------------- | ---------------------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) |
| `OPENAI_BASE_URL` | Base URL for the API endpoint (optional) |

## Run

```bash
cd examples/model/promptmap

export OPENAI_API_KEY="your-api-key"
# export OPENAI_BASE_URL="https://api.openai.com/v1"

go run . -a gpt-4o-mini -b gpt-4o
```

If your model follows the prompt, the first response should start with
`MODEL_A:` and the second should start with `MODEL_B:`.
