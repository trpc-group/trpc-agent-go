# Session Persona Demo

This example demonstrates a simple pattern for **per-session persona**.

Each session stores its own persona in session state. Before every
`runner.Run(...)` call, the demo loads the current session persona and passes a
run-scoped system prompt with `agent.WithGlobalInstruction(...)`.

This keeps the example focused on one idea:

- one shared agent;
- multiple sessions;
- each session has its own persona;
- the persona is applied dynamically for the current run.

## Why this version is simpler

The earlier variant used placeholder injection in the agent's global
instruction. That works, but it adds one more concept.

This version keeps the flow straightforward:

1. store persona text in session state;
2. read persona before calling `runner.Run(...)`;
3. override the system prompt for that run only.

The agent instance itself is not mutated permanently.

## Quick start

```bash
cd examples/session/persona
export OPENAI_API_KEY="your-key"
export MODEL_NAME="gpt-4.1-mini"
export OPENAI_BASE_URL="https://api.openai.com/v1"

go run . -session=inmemory
```

You can also use other backends, consistent with the other session examples:

```bash
go run . -session=sqlite
go run . -session=redis
go run . -session=postgres
go run . -session=mysql
go run . -session=clickhouse
```

## Flags

| Flag | Description | Default |
| ---- | ----------- | ------- |
| `-model` | Model name to use | `MODEL_NAME` or `deepseek-chat` |
| `-session` | Session backend | `inmemory` |
| `-event-limit` | Maximum stored events per session | `1000` |
| `-session-ttl` | Session TTL | `24h` |
| `-streaming` | Enable streaming mode | `true` |

## Commands

| Command | Description |
| ------- | ----------- |
| `/persona <text>` | Set the current session persona |
| `/show-persona` | Show the active session persona |
| `/new [id]` | Start a new session with the default persona |
| `/use <id>` | Switch to an existing session, or return an error if it does not exist |
| `/sessions` | List sessions and persona previews |
| `/exit` | End the demo |

The demo accepts `\n` inside `/persona` and converts it into actual newlines
before storing it in session state.

Example:

```text
/persona You are a strict reviewer.\nGive short feedback.\nHighlight risks first.
```

## Key implementation idea

The dynamic override happens at call time:

```go
eventChan, err := demo.runner.Run(
    ctx,
    userID,
    sessionID,
    model.NewUserMessage(userInput),
    agent.WithGlobalInstruction(buildPersonaInstruction(persona)),
)
```

The persona value itself is still persisted in session state:

```go
sessionService.UpdateSessionState(ctx, key, session.StateMap{
    "assistant_persona": []byte(persona),
})
```

## When to use this pattern

This version is a good fit when you want a lightweight example for:

- session-specific persona;
- session-specific prompt tone;
- a simple session-scoped system prompt that is rebuilt on each run.

If you later need richer prompt templating or state placeholders inside the
prompt, you can move to the placeholder-based approach.
