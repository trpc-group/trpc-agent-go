## Sub‑Agent Runtime State (GraphAgent)

This example demonstrates:

- GraphAgent with a pre‑processing node → LLMAgent sub‑agent flow
- Passing graph state to the sub‑agent via `Invocation.RunOptions.RuntimeState`
- Injecting scene knowledge in a system message (tool‑friendly)
- Parsing time up front and letting tools use `parsed_time` instead of LLM‑guessed values
- Interactive streaming from the single graph event channel

What happens:

- Pre node loads scene context and parses time from the latest user input, then writes
  `scene_id`, `scene_info`, and `parsed_time` into graph state.
- The sub‑agent’s model/tool callbacks read that state via
  `agent.InvocationFromContext(ctx).RunOptions.RuntimeState`:
  - Model Before: prepends an English system message with guidance.
  - Tool Before: logs that it sees `parsed_time`/`scene_info` (for demo).
- The `schedule_meeting` tool uses `parsed_time` when present; otherwise the agent asks for clarification.
- All events stream through one channel. This example prints LLM deltas (💬) and tool completions.

Run:

1) Provide credentials via flags or env:

- Flags (recommended for quick testing):

  go run ./examples/graph/subagent_runtime_state \
    -api-key "$OPENAI_API_KEY" \
    -base-url "$OPENAI_BASE_URL" \
    -model deepseek-chat

- Or env vars (flags optional):

  export OPENAI_API_KEY=sk-...
  export OPENAI_BASE_URL=https://api.deepseek.com
  go run ./examples/graph/subagent_runtime_state

2) Interactive examples:

- “Schedule a sync with Alex tomorrow at 10am”
- “Standup today at 3 pm”

Notes

- State mutability: graph state is immutable inside node/sub‑agent code. Return a state delta; the graph merges via reducers.
- Streaming: sub‑agent events are forwarded to the graph channel. Because the sub‑agent isn’t a graph LLM/Tool node, you won’t see graph‑style [MODEL]/[TOOL] metadata rows by default. This example prints a fallback line for tool completions.
