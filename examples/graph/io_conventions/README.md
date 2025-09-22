## Graph I/O Conventions (LLM Node + Agent Node)

This example demonstrates, end‑to‑end, how node input/output is handled via graph state and how downstream nodes consume upstream results. It focuses on two special node types: LLM node and Agent node.

What it shows

- Function node writes a state delta (custom keys) for downstream nodes.
- LLM node consumes one‑shot messages and user_input; it appends assistant output to messages and updates last_response/node_responses.
- Agent node (sub‑agent) receives the full graph state via Invocation.RunOptions.RuntimeState and, when finished, updates last_response/node_responses.
- A collector node shows how to read:
  - Built‑in outputs: state["last_response"], state["node_responses"][nodeID]
  - Custom outputs from previous nodes (e.g., parsed_time)

Run

1) With flags (recommended when testing custom providers):

   go run ./examples/graph/io_conventions \
     -api-key "$OPENAI_API_KEY" \
     -base-url "$OPENAI_BASE_URL" \
     -model deepseek-chat

2) Or via env (flags optional):

   export OPENAI_API_KEY=sk-...
   export OPENAI_BASE_URL=https://api.deepseek.com
   go run ./examples/graph/io_conventions

Interactive usage

- Built-in commands:
  - help     — show help
  - samples  — show sample inputs
  - demo     — run a short scripted demo
  - exit     — quit

- Sample inputs to try:
  - schedule a sync tomorrow at 10am
  - today at 3 pm standup
  - summarize what you understood

Expected output

- 💬 streaming assistant text
- 📦 Final payload printed by the collector node, e.g.:

  {
    "parsed_time": "2025-09-23T10:00:00+08:00",
    "llm_summary": "User wants to schedule a sync tomorrow at 10am.",
    "agent_last":  "Sure, I can help schedule that...",
    "node_responses": { "llm_summary": "...", "assistant": "..." }
  }

Key I/O conventions

- Input via state
  - User input: state["user_input"] (one‑shot). Cleared after LLM/Agent node consumes it.
  - One‑shot messages override: state["one_shot_messages"]
  - Custom keys: add them in a function node (e.g., state["parsed_time"]) for downstream.
- LLM node output
  - Appends assistant messages to state["messages"]
  - Sets state["last_response"] to the last assistant text
  - Sets state["node_responses"][<llm_node_id>] = last assistant text
- Agent node (sub‑agent) I/O
  - Receives full graph state through inv.RunOptions.RuntimeState (readable in model/tool callbacks).
  - On finish, sets state["last_response"] and state["node_responses"][<agent_node_id>].

See docs: docs/mkdocs/en/graph.md → “Node I/O Conventions”.
