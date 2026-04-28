# LLMAgent External Tool AG-UI Server

This example exposes an AG-UI SSE endpoint backed by an `LLMAgent` that mixes:

- internal tools (`calculator`, `internal_lookup`) executed automatically by the framework
- external tools (`external_note`, `external_approval`) executed by the caller through `role=tool`

The goal is to verify that `agui + llmagent + agent.WithToolExecutionFilter(...)`
can handle a mixed tool flow in one conversation:

1. Call 1 (`role=user`) emits tool calls for all four tools.
2. The framework executes `calculator` and `internal_lookup` immediately and
   returns their `TOOL_CALL_RESULT` events.
3. The framework defers `external_note` and `external_approval`, ends the run,
   and waits for the caller.
4. Call 2 (`role=tool`) sends back both external tool results as consecutive
   tail `role=tool` messages.
5. The model reads all four tool results from history and returns the final
   answer.

Call 2 uses the same `threadId` and a new `runId` for the follow-up `role=tool`
request.

## Run

From the `examples/agui` module:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://your-openai-compatible-base-url" # Optional.
go run ./server/externaltool/llmagent \
  -model gpt-4.1-mini \
  -address 127.0.0.1:8080 \
  -path /agui
```

The server also enables `MessagesSnapshot` at `http://127.0.0.1:8080/history`.

## Verify

`run.sh` sends two `curl` requests:

- the first `curl` sends `role=user`
- the script extracts the `external_note` and `external_approval` `toolCallId`
  values from that SSE response
- the second `curl` sends `role=tool` with both aligned `toolCallId` values

From the repo root:

```bash
bash ./examples/agui/server/externaltool/llmagent/run.sh
```

If you also want to inspect persisted history, call `/history` separately after
the two-step flow.
