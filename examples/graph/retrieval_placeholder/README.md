# Graph Retrieval → LLM (Placeholder Injection)

This example shows how to pass the output of an upstream retrieval node and the current user input into an LLM (Large Language Model) node by using instruction placeholders. We do this without adding a separate "replace" node or modifying `AddLLMNode`.

Key idea: write transient values into the session's `temp:` namespace inside the retrieval node. LLM nodes automatically expand placeholders from the session state before calling the model.

## Highlights

- Retrieval node writes context to `session.State["temp:retrieved_context"]`
- User input is mirrored to `session.State["temp:user_input"]` for use in the instruction
- LLM node instruction uses `{temp:retrieved_context}` and `{temp:user_input}` placeholders
- No custom plumbing in `AddLLMNode`; uses built‑in placeholder expansion

## Build

```
go build -o graph-retrieval-placeholder main.go
```

## Run

```
./graph-retrieval-placeholder
# or choose a model
./graph-retrieval-placeholder -model deepseek-chat
```

Type your question, for example:

```
What is the impact of quantum error correction?
```

You should see the retrieval context injected into the LLM instruction via placeholders.

## How It Works

1. The retrieval node simulates fetching documents, then writes two transient keys on the current session object:
   - `temp:retrieved_context`: concatenated retrieval snippets
   - `temp:user_input`: current `user_input` from graph state, mirrored for instruction use
2. The LLM node has an instruction string that references these keys:
   - `Context: {temp:retrieved_context}`
   - `Question: {temp:user_input}`
3. Before executing the LLM, the framework expands placeholders from the session state.

This pattern is ideal when retrieval results are per‑turn ephemeral and should not persist across sessions or users.

## Files

- `main.go`: the complete runnable example

## Why Write `temp:` Keys Directly on `session.State`

- Placeholder expansion for LLM (Large Language Model) nodes reads from the session’s state, see [graph/state_graph.go](graph/state_graph.go).
- GraphAgent injects the current `*session.Session` into graph state, see [agent/graphagent/graph_agent.go](agent/graphagent/graph_agent.go).
- Templates can use both `{key}` and `{{key}}` styles (Mustache is normalized automatically).
- For per‑turn data used only to build this round’s prompt, writing `temp:*` directly on `session.State` is appropriate and simple. It won’t be persisted.

## When to Use `SessionService` Instead

- If you need persistence across rounds or sessions, or you want user/app scoped configuration, use `SessionService` (Session Service) to update `user:*` or `app:*` keys.
- `temp:*` and `app:*` are intentionally blocked from `UpdateUserState` (User State update) in the in‑memory implementation, see [session/inmemory/service.go](session/inmemory/service.go).

## Concurrency Tips

- A single straight‑line flow (retrieve → llm) is safe to update `session.State` in the retrieval node.
- If you have parallel branches that might write the same keys, fan‑in to a single node to compose the values and then write once.

## Exposing Data to Observability

- Direct `session.State` writes are for prompt assembly. If you want observers (User Interface dashboards, logs) to see derived data at completion, place a compact summary into graph state (e.g., `metadata`). The final graph execution event serializes final state keys (excluding internal ones), see [graph/events.go](graph/events.go).
