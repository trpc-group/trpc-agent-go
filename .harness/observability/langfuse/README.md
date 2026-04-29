# Langfuse Multimodal Harness

This harness verifies that an `llmagent` real-model invocation can export multimodal telemetry to Langfuse and that the exported payload aligns with the OTel message schemas referenced in:

- [OpenTelemetry GenAI input messages JSON schema](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-input-messages.json)
- [OpenTelemetry GenAI output messages JSON schema](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-output-messages.json)

## What it sends

The integration test builds one user message with:

- a text instruction
- an `image_url` part by default
- an optional file attachment when `HARNESS_INCLUDE_FILE=1`
- a separate tool-call scenario that forces a real `tool_call` / `tool_call_response`
- provider reasoning output in the real-model response

This is intentionally designed so the default path is a portable real-model multimodal request (`text + image`) while still allowing file-mode verification when the provider supports it.

## Run

From the repository root:

```bash
set -a
. ".harness/observability/.env"
set +a

go test "./.harness/observability/langfuse" -run TestRealAgentMultimodalTraceToLangfuse -v
```

Optional file mode:

```bash
set -a
. ".harness/observability/.env"
set +a

export HARNESS_INCLUDE_FILE=1
go test "./.harness/observability/langfuse" -run TestRealAgentMultimodalTraceToLangfuse -v
```

Tool-call + reasoning scenario:

```bash
set -a
. ".harness/observability/.env"
set +a

go test "./.harness/observability/langfuse" -run TestRealAgentToolCallReasoningTraceToLangfuse -v
```

## Expected output

The test logs:

- `trace_name`
- `session_id`
- `run_id`
- `expected_modalities`
- `expected_fields`
- `final_output`

Use `session_id` or `run_id` to query Langfuse.

## Langfuse validation

The intended validation flow is:

1. Query traces by `session_id`.
2. Read the returned `trace_id`.
3. Fetch the full trace by `trace_id`.
4. Confirm there is at least one generation/span observation whose `input` contains OTel `role + parts` JSON from `gen_ai.input.messages.otel`.
5. Confirm the input JSON contains an image part:
   - `"type":"uri"`
   - `"modality":"image"`
6. If file mode is enabled, also confirm the input JSON contains either:
   - `"type":"file"`, or
   - `"type":"blob"` with `"modality":"file"`
7. For the tool-call scenario, confirm a generation observation includes:
   - `"type":"tool_call"`
   - `"type":"tool_call_response"`
   - `"type":"reasoning"`
8. Confirm Langfuse shows a single converted input/output payload from `gen_ai.input.messages.otel` and `gen_ai.output.messages.otel`. Deprecated compatibility attributes may exist on the raw span, but Langfuse should not parse them for display.

The current harness is focused on validating telemetry export, not provider replay.

Example CLI flow:

```bash
set -a
. ".harness/observability/.env"
set +a

export LANGFUSE_HOST="$LANGFUSE_BASE_URL"
export SESSION_ID="<session_id from go test output>"

npx langfuse-cli api traces list \
  --json \
  --session-id "$SESSION_ID" \
  --fields core,io,observations,metrics \
  --server "$LANGFUSE_BASE_URL"

export TRACE_ID="<trace_id from traces list>"

npx langfuse-cli api traces get \
  "$TRACE_ID" \
  --json \
  --server "$LANGFUSE_BASE_URL"
```

When the export is correct, `traces get` should show:

- trace `name = agent-multimodal-otel-harness`
- trace `environment = harness`
- trace metadata keys such as `harness_run_id`
- an `AGENT` observation with `input` serialized as OTel `role + parts` from the `.otel` attributes
- a `GENERATION` observation with the same multimodal user message in `input`
- only one displayed input/output payload per observation
- for `TestRealAgentToolCallReasoningTraceToLangfuse`, the generation chain should also include `tool_call`, `tool_call_response`, and `reasoning` parts
