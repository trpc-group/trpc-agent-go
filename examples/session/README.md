# Session + LLMAgent Example

This example shows how to combine **LLMAgent** with the **Session** subsystem in `trpc-agent-go`.  Two storage back-ends are included:

* `inmemory` – in-process memory, zero external dependency, best for local testing.
* `redis` – Redis based storage, suited for persistence or sharing sessions between multiple instances.

## Features

1. **Basic dialogue** – creates a fresh session and sends two consecutive messages to verify that the agent remembers the previous turn.
2. **History session loading** – pre-loads a historical user message into the session, then asks the agent a follow-up question to verify it can reference the history.
3. **Pluggable storage** – switch between `inmemory` and `redis` via a command-line flag.

## Prerequisites

* Go 1.22 or newer
* A valid OpenAI-compatible API key (`OPENAI_API_KEY` environment variable)
* Redis server (only if you choose the `redis` session service)

## Running the example

```bash
cd examples/session

# In-memory session service (default)
OPENAI_API_KEY=your_key go run main.go -model gpt-3.5-turbo -session inmemory

# Redis session service (requires a running Redis on localhost:6379)
OPENAI_API_KEY=your_key go run main.go -model gpt-3.5-turbo -session redis
```

### Command-line flags

| Flag       | Description                                       | Default            |
|------------|---------------------------------------------------|--------------------|
| `-model`   | Name of the LLM model to use                      | `gpt-3.5-turbo`    |
| `-session` | Session service implementation: `inmemory`/`redis`| `inmemory`         |

## Expected output (abridged)

```text
=== Simple Chat Example ===
User: Hello, what's your name?
Assistant: ...

User: Tell the previous message I just asked you
Assistant: ...

=== Simple Chat Example with History Session ===
History: I have a farm where chickens and rabbits ...
User: How many chickens and rabbits are there, just reply two numbers in one line.
Assistant: 2600 4400
```
