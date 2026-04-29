# Observability Harness

This directory contains local harness assets for observability validation, with
the current focus on Langfuse-based telemetry verification.

- Install the Langfuse skill before querying Langfuse data. The skill is open source on [GitHub](https://github.com/langfuse/skills) and can access data from the Langfuse platform.
- If your environment supports the `gh skill` command, you can install it with `gh skill install langfuse/skills`.
- `.harness/observability/.env` already contains the key for accessing the test project on the Langfuse platform.
- `.harness/observability/.env` already contains access to an LLM that supports OpenAPI calls and real model invocations.
- Put Langfuse observability test code under `.harness/observability/langfuse`.

## Verification Checklist

Use the Langfuse harness under `.harness/observability/langfuse` to validate message telemetry with a real model call.

- Confirm the raw span contains both deprecated compatibility attributes (`gen_ai.input.messages`, `gen_ai.output.messages`) and the recommended OTel attributes (`gen_ai.input.messages.otel`, `gen_ai.output.messages.otel`).
- Confirm Langfuse displays only one final input/output payload.
- Confirm the displayed payload comes from `gen_ai.input.messages.otel` and `gen_ai.output.messages.otel`.
- Confirm Langfuse does not parse deprecated compatibility attributes (`gen_ai.input.messages`, `gen_ai.output.messages`) for display.
- Confirm multimodal `parts`, `tool_call`, `tool_call_response`, and `reasoning` survive in the displayed payload.