# Graph Session Demo

This example demonstrates a graph agent running inside `Runner` with session
persistence enabled.

The graph includes normal graph nodes and an agent node. It is useful for
checking that graph completion snapshot keys are available on runner completion
events, while session state keeps only compact business state and response
identity.

## Prerequisites

- Go 1.21 or later
- An OpenAI-compatible model endpoint

## Required Environment Variables

Set both variables before running the demo:

```bash
export OPENAI_API_KEY=
export OPENAI_BASE_URL=
```

For example:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"
```

## Quick Start

```bash
cd examples/session/graph
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"

go run .
```

You can also choose a model explicitly:

```bash
go run . -model deepseek-chat
```

## Flags

| Flag | Description | Default |
| ---- | ----------- | ------- |
| `-model` | Model name to use | `MODEL_NAME` or `deepseek-chat` |
| `-streaming` | Enable streaming mode for the LLM agent node | `true` |
| `-debug` | Print session events and state after each turn | `true` |

## Commands

| Command | Description |
| ------- | ----------- |
| `/help` | Show commands |
| `/debug` | Toggle debug output |
| `/state` | Print persisted `session.State` |
| `/new [id]` | Start a new session |
| `/sessions` | List sessions |
| `/exit` | End the conversation |

## What To Look For

With debug enabled, the demo prints:

- persisted session events;
- persisted `session.State`;
- whether graph snapshot keys such as `messages`, `user_input`,
  `last_response`, `last_tool_response`, `node_responses`, and
  `_completion_metadata` are present in `session.State`.

Those snapshot keys should not be persisted into `session.State`. The response
identity key `last_response_id` is intentionally retained for runner resume and
completion de-duplication logic.

Example output:

```text
You: 1
Assistant: I understand you've entered "1" but I need more context...
│
│  [DEBUG] Session Events: 2
│    1. user     : 1
│    2. assistant: I understand you've entered "1" but I need more context...
│
│  [DEBUG] Persisted session.State:
│    - agent_reply = I understand you've entered "1" but I need more context...
│    - business_result = last turn at 2026-04-27T11:21:28+08:00
│    - checkpoint_ns = session-graph-agent
│    - draft_answer = I routed your message through a graph with an agent node. Normalized input: "1".
│    - last_response_id =
│    - normalized_input = 1
│
│  [DEBUG] Graph snapshot keys in session.State:
│    - messages             false
│    - user_input           false
│    - last_response        false
│    - last_tool_response   false
│    - node_responses       false
│    - _completion_metadata false
```
