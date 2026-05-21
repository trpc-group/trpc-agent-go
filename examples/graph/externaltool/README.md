# External Tools (Client‑Executed)

This example shows how a graph agent can orchestrate an external tool
followed by internal tools with the model in control. The assistant first
returns a tool call (external_fetch), the client executes the tool outside
the graph process, submits the result back, and the assistant immediately
continues with internal tools (summarize_text, optionally format_bullets).

The source code is in [`main.go`](main.go).

## Core Building Blocks

- Runner: streams Large Language Model (LLM) output to the CLI.
- GraphAgent: wraps the compiled graph with checkpoint persistence.
- Graph: defines nodes, state schema, and conditional tool edges.
- LLM node (`assistant_plan`): decides when to call tools.
- Tool node (`external_tools`): intercepts tool calls, pauses via
  `graph.Interrupt` only for external_fetch, and resumes when the client
  provides the tool result.

### Graph Structure

1. `prepare_input` – trims user input and stores it in state.
2. `assistant_plan` – streams the assistant response and makes tool calls.
3. `external_tools` – pauses for external_fetch and waits for results.
4. `finalize` – ensures there is a final assistant answer.

Edges:

- `prepare_input → assistant_plan`
- `assistant_plan → external_tools` (when tool calls are present)
- `assistant_plan → finalize` (when no tools are needed)
- `external_tools → assistant_plan` (continue after each tool result)

## Running the Demo

```bash
cd examples/graph/externaltool
go run .
```

CLI commands:

- Type a question to start a run
- When external_fetch appears, execute it on your side and respond with:
  `/resume <content>`
- `/help` prints a short command summary
- `/exit` ends the program

### Example: Extract Then Summarize

```
🔌 External Tools (Client‑Executed)
Model: deepseek-v4-flash
==================================================
Type a question to start the workflow.
Commands:
  /resume <content> Resume with extract result
  /help            Show this help message
  /exit            Quit the program

You> First fetch the content at https://example.com/doc, then summarize it.
🔧 Tool call requested:
   • external_fetch (ID: call_0)
     args: {"source":"https://example.com/doc"}
⏸️  Waiting for external tool result.
🛑 External tool requested:
   Run external_fetch and return the content.
   Reply: /resume <content>
You> /resume <document text here>
🧰 Tool result: {"content":"<document text here>"}
🤖 Assistant: Here is the summary:
  • ...
  (Optionally, the assistant may also call format_bullets internally.)
You>
```

### Real Execution Example

```
🔌 External Tools (Client‑Executed)
Model: deepseek-v4-flash
==================================================
Type a question to start the workflow.
Commands:
  /resume <content> Resume with extract result
  /help            Show this help message
  /exit            Quit the program

You> fetch and summarize content from www.qq.com
🤖 Assistant: I'll fetch content from www.qq.com and then summarize it for you. Let me start by retrieving the content.
🔧 Tool call requested:
   • external_fetch (ID: call_00_550LIAwSjCvBxRRzHNHQYV6D)
     args: {"source": "www.qq.com"}


🛑 External tool requested:
   Run extract externally and return content.
   Reply: /resume <content>

⏸️  Waiting for external tool result.
You> /resume "qq.com is a website that provides rich content for qq"

🧰 Tool result: {"content":"\"qq.com is a website that provides rich content for qq\""}
🤖 Assistant: {"content":"\"qq.com is a website that provides rich content for qq\""}

Now let me summarize this content for you:
🔧 Tool call requested:
   • summarize_text (ID: call_00_CDYXfRZV6ihEJbTIIQQ4n9gg)
     args: {"text": "qq.com is a website that provides rich content for qq"}

🧰 Tool result: {"summary":"qq.com is a website that provides rich content for qq"}

Here's the summary of the content from www.qq.com:

- qq.com is a website that provides rich content for qq

The content appears to be quite brief and describes qq.com as a platform offering various content related to QQ services.
```

## How It Works

1. The tool node installs a `BeforeToolCallback` that intercepts
   external_fetch before execution.
2. The callback emits `graph.Interrupt`, so the runner saves a
   checkpoint and the CLI prompts for a resume value.
3. The client submits the result via `/resume <content>`.
4. The callback wraps the plain string into `{content: ...}` and
   returns it as the tool result, letting the graph continue.

Tip: You can provide a different sequence. The assistant follows your
plan and only pauses on external_fetch.
