# External Tool Interrupt Example

This beginner-friendly example demonstrates how to build a tiny graph agent
that:

- Streams Large Language Model (LLM) output through the **Runner**
- Executes a **GraphAgent** built from a **Graph** with conditional tool edges
- Calls `graph.Interrupt` inside a tool node to pause execution
- Saves checkpoints in memory and resumes when the user provides data

The source code lives in [`main.go`](main.go).

## Core Building Blocks

- **Runner** keeps a conversational session and streams events to the CLI.
- **GraphAgent** wraps the compiled graph and attaches the checkpoint saver.
- **Graph** defines nodes, state schema, and conditional edges.
- **LLM node** (`assistant_plan`) writes messages and decides when to use tools.
- **Tool node** (`external_lookup`) wraps `graph.Interrupt` so execution stops
  until a human supplies the expected result.

### Graph Structure

1. `prepare_input` – trims the user question and stores it in state.
2. `assistant_plan` – streams the assistant response and may call the tool.
3. `external_lookup` – tool node that pauses via `graph.Interrupt`.
4. `finalize` – ensures there is a final assistant answer.

Edges:

- `prepare_input → assistant_plan`
- `assistant_plan → external_lookup` (only when the assistant issues tool calls)
- `assistant_plan → finalize` (fallback path when no tool call is issued)
- `external_lookup → assistant_plan` (resume loop after tool completes)

## Running the Demo

```bash
cd examples/graph/externaltool
go run .
```

CLI commands:

- Type a question to start a run (for example: `What happened in the gadget demo?`)
- Respond to the pause with `/resume <value>` (for example: `/resume The demo launched in 2024.`)
- `/help` prints a short command summary
- `/exit` ends the program

### Example Session

```
🔌 External Tool Interrupt Demo
Model: deepseek-chat
==================================================
Type a question to start the workflow.
Commands:
  /resume <value>  Resume the paused run with tool output
  /help            Show this help message
  /exit            Quit the program

You> Summarise the latest launch event.
🔧 Tool call requested:
   • manual_lookup (ID: call_1)
     args: {"topic":"latest launch event"}
⏸️  Workflow paused for manual data.
🛑 Manual data required:
   Manual lookup required for "latest launch event". Provide the missing data.
   Resume with: /resume <value>
You> /resume The launch took place in April 2024 with 500 attendees.
🧰 Tool result: {"data":"The launch took place in April 2024 with 500 attendees."}
🤖 Assistant: The latest launch event occurred in April 2024 with 500 attendees.
You>
```

## How Interrupt and Resume Work

1. The tool node invokes
   `graph.Interrupt(ctx, state, "lookup_result", promptPayload)`.
2. The executor raises a Pregel interrupt event and saves a checkpoint through
   the in-memory saver.
3. The CLI inspects the interrupt event, remembers the checkpoint ID, and asks
   the user for the missing value.
4. `/resume <value>` creates a `*graph.Command` carrying a `ResumeMap`
   entry for `lookup_result` and passes it via `agent.WithRuntimeState`.
5. The executor restores the checkpoint, replays the interrupted node, and
   the tool returns the resume value to the LLM node.

> Note: If you see “Nothing to resume right now.” it means the run hasn’t
> paused yet. First ask a question that leads the assistant to call the
> `manual_lookup` tool and wait for the “🛑 Manual data required:” prompt,
> then use `/resume <value>`.
