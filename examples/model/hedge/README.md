# Model Hedge with LLMAgent and Runner

This example demonstrates how to use `model/hedge` behind `llmagent` and `runner`.

The primary provider uses the official OpenAI endpoint, and the backup provider uses DeepSeek. The example keeps the hedge logic at the model layer, then plugs that wrapped model into a normal multi-turn chat stack built with `LLMAgent` and `Runner`.

## What This Example Shows

- A hedged model chain created with `hedge.New(...)`.
- A normal `llmagent.New(...)` setup using the hedge model as its single LLM.
- A `runner.NewRunner(...)` chat loop with in-memory session storage.
- Interactive multi-turn chat without any mock provider or fake transport.

## Hedge Rule

The wrapper behaves as follows:

1. It launches the primary candidate immediately.
2. It launches the next candidate after the configured hedge delay, or earlier when all active candidates have already failed.
3. It commits the first candidate that produces a meaningful non-error response.
4. Once a winner is committed, it cancels the other candidates and keeps forwarding only the winner's stream.

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

cd examples/model/hedge
go run . \
  -primary-model gpt-4o-mini \
  -backup-model deepseek-v4-flash \
  -hedge-delay=100ms \
  -streaming=true
```

## Available Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-primary-model` | Primary model name. | `gpt-4o-mini` |
| `-backup-model` | Backup model name. | `deepseek-v4-flash` |
| `-primary-base-url` | Primary model base URL. | `https://api.openai.com/v1` |
| `-backup-base-url` | Backup model base URL. | `https://api.deepseek.com/v1` |
| `-hedge-delay` | Delay before launching the next hedge candidate. | `100ms` |
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
    "deepseek-v4-flash",
    openai.WithBaseURL("https://api.deepseek.com/v1"),
)

llm, err := hedge.New(
    hedge.WithName("hedge-chat-model"),
    hedge.WithCandidates(primary, backup),
    hedge.WithDelay(100*time.Millisecond),
)
if err != nil {
    return err
}

agentInstance := llmagent.New(
    "hedge-chat-agent",
    llmagent.WithModel(llm),
    llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
)

r := runner.NewRunner(
    "hedge-chat-demo",
    agentInstance,
    runner.WithSessionService(sessioninmemory.NewSessionService()),
)
```

## Design Notes

- The request is cloned per candidate, so provider-side request mutation on one candidate does not leak into another candidate.
- Setting `-hedge-delay=0` makes the example launch both candidates immediately, which matches an all-at-once race.
- The example uses the normal `LLMAgent + Runner` path, so the hedge model can be adopted without changing higher-level agent orchestration code.

## Verification

Build the example directly:

```bash
cd examples
go build ./model/hedge
```
