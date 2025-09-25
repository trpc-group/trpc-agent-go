# Graph Placeholder Demo

This example demonstrates using placeholders in Graph-based workflows (GraphAgent + StateGraph) with session state integration. It mirrors the capabilities of the non-graph placeholder example, but implemented using a single LLM node inside a graph.

## Features

- Placeholder replacement in LLM node instruction using session state
- Supports unprefixed (readonly) and prefixed (mutable) placeholders:
  - `{research_topics}` (readonly, set at session creation)
  - `{user:topics}` (user-level state, mutable)
  - `{app:banner}` (app-level state, mutable)
- Interactive commands to update user/app state during runtime
- Streaming responses and simple event printing

## How It Works

- The graph defines a single LLM node with an instruction that contains placeholders.
- At runtime, the graph LLM node injects session state into the instruction before calling the model (now supported in graph/state_graph.go).
- Session state comes from three scopes:
  - Session-local keys (e.g., `research_topics`)
  - App-level merged keys (`app:*`)
  - User-level merged keys (`user:*`)

## Build

```
go build -o graph-placeholder main.go
```

## Run

```
./graph-placeholder
# or select a model
./graph-placeholder -model deepseek-chat
```

## Commands

- Set user topics (user scope):
  ```bash
  /set-user-topics quantum computing, cryptography
  ```
- Set app banner (app scope):
  ```bash
  /set-app-banner Research Mode
  ```
- Show current merged state:
  ```bash
  /show-state
  ```
- Ask questions (normal input):
  ```bash
  What are the latest developments?
  ```
- Exit:
  ```bash
  exit
  ```

## Example Session

```
🔗 Graph Placeholder Demo
Model: deepseek-chat
Type 'exit' to end the session
Commands: /set-user-topics <topics>, /set-app-banner <text>, /show-state
============================================================

👤 You: /show-state
📋 Current Session State:
   - research_topics: artificial intelligence, machine learning, deep learning, neural networks
   - user:topics: quantum computing, cryptography
   - app:banner: Research Mode

👤 You: What are the latest developments?
🤖 Research Node: Based on the current research focus and user interests...
```

## Notes

- `{name?}` optional suffix is supported: if the key is missing, it renders as empty string.
- Keys must exist in session state to be injected; for `{user:*}` and `{app:*}`, use the commands above or the session service APIs to set them.
- The example uses the in-memory session service.

