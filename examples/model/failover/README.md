# Model Failover with LLMAgent and Runner

This example demonstrates how to use `model/failover` behind `llmagent` and `runner`.

The primary provider uses the official OpenAI endpoint, and the backup provider uses DeepSeek. The example keeps the failover logic at the model layer, then plugs that wrapped model into a normal multi-turn chat stack built with `LLMAgent` and `Runner`.

## What This Example Shows

- A primary/backup model chain created with `failover.New(...)`.
- A normal `llmagent.New(...)` setup using the failover model as its single LLM.
- A `runner.NewRunner(...)` chat loop with in-memory session storage.
- Interactive multi-turn chat without any mock provider or fake transport.

## Failover Rule

The wrapper switches from the primary model to the backup model only when all of the following are true:

1. The primary fails before the first non-error chunk.
2. The primary returns a function-level error, or a `Response.Error` with a non-empty `Message` or `Type`.
3. The backup model starts successfully.

Once any non-error chunk has already been delivered to the caller, the example keeps the current stream and surfaces later failures directly instead of replaying the request on the backup.

## Environment Variables

The example expects separate credentials for the two providers:

| Variable | Description |
| --- | --- |
| `OPENAI_API_KEY` | API key for the primary OpenAI endpoint. |
| `DEEPSEEK_API_KEY` | API key for the backup DeepSeek endpoint. |

The example passes explicit base URLs in code:

- Primary: `https://api.openai.com/v1`
- Backup: `https://api.deepseek.com/v1`

## Running The Example

```bash
export OPENAI_API_KEY="your-openai-key"
export DEEPSEEK_API_KEY="your-deepseek-key"

cd examples/model/failover
go run . \
  -primary-model gpt-4o-mini \
  -backup-model deepseek-chat \
  -streaming=true
```

## Available Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-primary-model` | Primary model name. | `gpt-4o-mini` |
| `-backup-model` | Backup model name. | `deepseek-chat` |
| `-primary-base-url` | Primary model base URL. | `https://api.openai.com/v1` |
| `-backup-base-url` | Backup model base URL. | `https://api.deepseek.com/v1` |
| `-streaming` | Enables streaming responses. | `true` |

## Chat Commands

- `/new` starts a fresh session.
- `/exit` ends the conversation.

## Core Setup

```go
primary := openai.New(
    "gpt-4o-mini",
    openai.WithBaseURL("https://api.openai.com/v1"),
)
backup := openai.New(
    "deepseek-chat",
    openai.WithBaseURL("https://api.deepseek.com/v1"),
)

llm, err := failover.New(
    failover.WithCandidates(primary, backup),
)
if err != nil {
    return err
}

agentInstance := llmagent.New(
    "failover-chat-agent",
    llmagent.WithModel(llm),
    llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
)

r := runner.NewRunner(
    "failover-chat-demo",
    agentInstance,
    runner.WithSessionService(sessioninmemory.NewSessionService()),
)
```

## Design Notes

- The request is cloned before each attempt so provider-side request mutation on the primary cannot leak into the backup attempt.
- The example uses the normal `LLMAgent + Runner` path, so the failover model can be adopted without changing higher-level agent orchestration code.

## Verification

Build the example directly:

```bash
cd examples
go build ./model/failover
```
