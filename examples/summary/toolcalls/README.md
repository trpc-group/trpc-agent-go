# Same-Turn Tool-Call Summary Example

This example demonstrates summary generation during a single run where the
assistant performs multiple tool calls in the same turn.

It is useful for validating:

- Summary jobs are triggered by tool-result events during ReAct loops.
- The model can continue the same turn after summary processing starts.
- The user message is still present in later in-turn model requests.

## Prerequisites

- Go 1.21 or later.
- Model configuration with an OpenAI-compatible endpoint.

Environment variables:

- `OPENAI_API_KEY`.
- `OPENAI_BASE_URL` (optional).

## Run

```bash
cd examples/summary/toolcalls
export OPENAI_API_KEY="your-api-key"
go run main.go -model deepseek-chat -steps 5
```

Optional flags:

- `-model`: Model name.
- `-steps`: Number of required sequential tool calls in one run.
- `-query`: User message for the run.
- `-wait-sec`: Max wait time for async summary generation.

## What to Observe

Use this checklist to validate behavior:

1. **Same-turn proof.**
   - `Request IDs observed` contains exactly one ID.
   - `Invocation IDs observed` contains exactly one ID.
   - The output includes `✅ Turn check: single RequestID observed; this is one turn.`

2. **Mid-turn summary injection.**
   - `BeforeModel request #1` usually has only system + user messages.
   - A later `BeforeModel` request contains a second system message with
     `<summary_of_previous_interactions> ... </summary_of_previous_interactions>`.

3. **User message retention after summary.**
   - In requests that already include the summary system message, the same
     original user message is still present in the message list.

4. **Context compaction after summary.**
   - After summary appears, older tool interactions are represented by the
     summary text instead of being sent as full raw history.
   - Only recent assistant/tool interactions are kept as raw messages.

5. **No orphan tool context.**
   - Tool messages shown in `BeforeModel` are accompanied by matching recent
     assistant progression text (for example, "Now I'll proceed to step N").
   - There should be no isolated tool result that lacks nearby assistant
     context for the current step.

6. **ReAct loop completion.**
   - Tool call count reaches the configured `-steps` value.
   - The final answer is produced after the last required tool call.

7. **Asynchronous summary note.**
   - The final printed `📝 Summary` is fetched asynchronously and may lag one
     update behind the very latest in-turn state, depending on timing.
