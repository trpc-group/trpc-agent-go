## Graph I/O With Tools Node

This example extends the I/O conventions demo by adding a Tools node. It shows a full path:

- parse_input → llm_decider (with tool definitions)
- If the LLM requests a tool call → tools → capture_tool → collect
- Otherwise (no tool call) → assistant (sub‑agent) → collect

What it demonstrates

- How an LLM node declares tools so the model can emit tool_calls
- How a Tools node executes those calls and appends tool responses to messages
- How a downstream node parses the latest tool response JSON and saves a structured result into state
- How to read final data in a collector (last_response, node_responses, and custom state keys like meeting)

Run

1) With flags (when using non‑OpenAI providers):

   go run ./examples/graph/io_conventions_tools \
     -api-key "$OPENAI_API_KEY" \
     -base-url "$OPENAI_BASE_URL" \
     -model deepseek-chat

2) Or via env:

   export OPENAI_API_KEY=sk-...
   export OPENAI_BASE_URL=https://api.deepseek.com
   go run ./examples/graph/io_conventions_tools

Interactive usage

- Commands:
  - help     — show help
  - samples  — sample inputs
  - demo     — short scripted demo
  - exit     — quit

- Try inputs:
  - schedule a meeting tomorrow at 10am titled sync with Alex
  - schedule team standup today at 3 pm
  - tell me a fun fact (fallback path, no tool call)

Expected output

- 💬 streaming assistant text
- A final payload that may include a structured meeting result when the tool path is taken, for example:

  {
    "meeting": {
      "meeting_id": "mtg-20250923-1000",
      "title": "sync with Alex",
      "time": "2025-09-23T10:00:00+08:00"
    },
    "agent_last": "…",
    "node_responses": { "llm_decider": "…", "assistant": "…" },
    "parsed_time": "2025-09-23T10:00:00+08:00"
  }

