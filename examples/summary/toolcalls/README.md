# Same-Turn Tool-Call Summary Example

This example demonstrates summary generation during a single run where the
assistant performs multiple tool calls in the same turn.

It validates two summary strategies:

- **Async summary** (default): A background worker generates summaries after
  tool-result events. The summary appears in later LLM requests once the async
  job completes.
- **Sync intra-run summary** (`-sync-summary-intra-run`): A synchronous
  summary refresh is triggered between each LLM loop iteration. The summary is
  guaranteed to be present from the second LLM request onward.

When sync intra-run summary is enabled, the framework automatically skips
redundant async summary enqueue for intermediate tool-result events during the
same run, while still allowing the final assistant response to trigger an async
job. This ensures the session summary is up-to-date at turn end without
duplicate summarization work.

## Prerequisites

- Go 1.21 or later.
- Model configuration with an OpenAI-compatible endpoint.

Environment variables:

- `OPENAI_API_KEY`.
- `OPENAI_BASE_URL` (optional).

## Run

Async summary (default):

```bash
cd examples/summary/toolcalls
export OPENAI_API_KEY="your-api-key"
go run . -model deepseek-chat -steps 5
```

Sync intra-run summary:

```bash
go run . -model deepseek-chat -steps 5 -sync-summary-intra-run
```

Optional flags:

- `-model`: Model name.
- `-steps`: Number of required sequential tool calls in one run.
- `-query`: User message for the run.
- `-wait-sec`: Max wait time for async summary generation.
- `-sync-summary-intra-run`: Enable synchronous summary refresh between LLM
  iterations (automatically suppresses async summary enqueue for intermediate
  tool-result events).

## What to Observe

Use this checklist to validate behavior:

1. **Same-turn proof.**
   - `Request IDs observed` contains exactly one ID.
   - `Invocation IDs observed` contains exactly one ID.
   - The output includes `✅ Turn check: single RequestID observed; this is
     one turn.`

2. **Mid-turn summary injection.**
   - `BeforeModel request #1` usually has only system + user messages.
   - A later `BeforeModel` request contains a second system message with
     `<summary_of_previous_interactions> ... </summary_of_previous_interactions>`.

3. **Async vs sync intra-run timing.**
   - Without `-sync-summary-intra-run`: the summary system message may not
     appear until request #3 or later, depending on when the async worker
     finishes.
   - With `-sync-summary-intra-run`: the summary system message appears from
     request #2 onward, because it is generated synchronously before each LLM
     call.

4. **User message retention after summary.**
   - In requests that already include the summary system message, the same
     original user message is still present in the message list.

5. **Context compaction after summary.**
   - After summary appears, tool interactions already absorbed into that
     summary are omitted from the raw prompt history, even within the same
     turn.
   - The prompt keeps the original user message plus any events that happened
     after the summary cutoff.

6. **Large tool results are compacted.**
   - A large tool result that triggered the summary should stop appearing as a
     raw `role=tool` message in later `BeforeModel` requests.
   - Its salient information should instead survive through the summary system
     message.

7. **ReAct loop completion.**
   - Tool call count reaches the configured `-steps` value.
   - The final answer is produced after the last required tool call.

8. **Post-run summary.**
   - Without `-sync-summary-intra-run`: the final printed `📝 Summary` is
     fetched asynchronously and may lag behind the latest in-turn state.
   - With `-sync-summary-intra-run`: the summary is already up-to-date at the
     end of the run; the async wait should find it immediately.
