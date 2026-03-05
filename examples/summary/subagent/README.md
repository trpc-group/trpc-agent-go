# Sub-Agent Summarization Example

This example demonstrates session summarization when the primary agent delegates
work to a sub-agent via `agenttool`. It uses intentionally low token/event
thresholds so that summarisation triggers quickly, making it easy to observe how
summaries are generated for both the primary agent branch and the sub-agent
branch.

## Key Concepts

- **AgentTool delegation**: The parent agent wraps a child agent as a tool and
  delegates math questions to it.
- **Branch summaries**: Each agent branch (primary and child) gets its own
  summary keyed by `FilterKey`.
- **Full-session summary**: A separate summary covering all events across all
  branches is also generated.
- **Threshold isolation**: Sub-agent tokens/events do not inflate the primary
  agent's threshold checks, preventing premature summarisation of the parent
  branch.

## Architecture

```
Parent Agent (summary-subagent-demo)
└── Math Specialist Agent Tool (math-specialist-<uuid>)
    └── Calculator Function Tool
```

When the parent receives a math question it delegates to the math-specialist
agent tool. The framework records events under separate `FilterKey` values:

- `summary-subagent-demo` — parent agent events.
- `math-specialist-<uuid>` — child agent events (isolated branch).

The summariser produces up to three summaries:

| Summary key                  | Contents                            |
|------------------------------|-------------------------------------|
| `summary-subagent-demo`      | Parent agent branch only.           |
| `math-specialist-<uuid>`     | Child agent branch only.            |
| `(full-session)`             | All events merged.                  |

## Prerequisites

- Go 1.21 or later.
- Model configuration via environment variables.

Environment variables:

- `OPENAI_API_KEY`: API key for the model service.
- `OPENAI_BASE_URL` (optional): Base URL for the model API endpoint.
- `MODEL_NAME` (optional): Model name. Default: `deepseek-v3.2`.

## Run

```bash
cd examples/summary/subagent
export OPENAI_API_KEY="your-api-key"
go run .
```

With a specific model:

```bash
MODEL_NAME=gpt-4o-mini go run .
```

## Interactive Commands

| Command   | Description                                 |
|-----------|---------------------------------------------|
| `/show`   | Display all summaries generated so far.     |
| `/events` | Dump session events with FilterKey info.    |
| `/exit`   | Quit the demo.                              |

## Example Session

```
Session Summarization + Sub-Agent Demo
Model:          deepseek-v3.2
TokenThreshold: 200
EventThreshold: 2
Session:        sess-1741234567
============================================================
You: what is 123 * 456?
Assistant: ...delegates to math-specialist...
The result of 123 × 456 is 56,088.

You: /events
Total events: 7
  [0] author=user          filterKey=summary-subagent-demo    content=what is 123 * 456?
  [1] author=parent-agent  filterKey=summary-subagent-demo    content=<tool call to math-specialist>
  [2] author=math-spec...  filterKey=math-specialist-abc123   content=<tool call to calculator>
  [3] author=math-spec...  filterKey=math-specialist-abc123   content=<tool response>
  [4] author=math-spec...  filterKey=math-specialist-abc123   content=The result is 56088.
  [5] author=parent-agent  filterKey=summary-subagent-demo    content=<tool result>
  [6] author=parent-agent  filterKey=summary-subagent-demo    content=The result of 123 × 456 is 56,088.

You: /show
--- Summary [math-specialist-abc123] ---
User asked to calculate 123 * 456 using the calculator tool. Result: 56088.

--- Summary [(full-session)] ---
The user asked to compute 123 * 456. The parent agent delegated to a math
specialist which used a calculator tool and returned 56088.

--- Summary [summary-subagent-demo] ---
User requested 123 * 456. Delegated to math-specialist agent tool. Result: 56088.

You: /exit
Bye.
```

## What to Observe

1. **Three distinct summaries** — the parent branch, child branch, and
   full-session summary are all generated independently.
2. **Threshold isolation** — the child agent's events/tokens do not count
   toward the parent's threshold. The parent summary triggers only when the
   parent's own events exceed the threshold.
3. **Summary injection** — on subsequent turns, `[summary injected into prompt]`
   is printed when the framework prepends the latest summary to the LLM request.

## Implementation Highlights

```go
// Low thresholds for quick triggering.
sum := summary.NewSummarizer(
    llm,
    summary.WithMaxSummaryWords(100),
    summary.WithChecksAny(
        summary.CheckTokenThreshold(200),
        summary.CheckEventThreshold(2),
    ),
)

// Child agent wrapped as a tool.
childTool := agenttool.NewTool(
    childAgent,
    agenttool.WithStreamInner(true),
)

// Parent agent with summary injection enabled.
parentLLM := llmagent.New(
    parentAgent,
    llmagent.WithTools([]tool.Tool{childTool}),
    llmagent.WithAddSessionSummary(true),
)
```
