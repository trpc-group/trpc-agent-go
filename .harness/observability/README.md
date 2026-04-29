# Observability Harness

This directory contains local harness assets for observability validation, with
the current focus on Langfuse-based telemetry verification.

- Copy `.harness/observability/.env.example` to `.harness/observability/.env` and fill in local credentials before running harness commands.
- `LANGFUSE_BASE_URL`, `LANGFUSE_PUBLIC_KEY`, and `LANGFUSE_SECRET_KEY` are required for exporting traces and querying Langfuse data.
- `OPENAI_API_KEY` is required when the harness performs real model calls through an OpenAI-compatible endpoint. Set `OPENAI_BASE_URL` and `OPENAI_MODEL` when using a non-default compatible provider.
- Install the Langfuse skill before querying Langfuse data. If your environment supports the `gh skill` command, install it with `gh skill install langfuse/skills`.
- Put Langfuse observability test code under `.harness/observability/langfuse`.

## Verification Checklist

Use the Langfuse harness under `.harness/observability/langfuse` to validate message telemetry with a real model call.

- Confirm the raw span contains both deprecated compatibility attributes (`gen_ai.input.messages`, `gen_ai.output.messages`) and the recommended OTel attributes (`gen_ai.input.messages.otel`, `gen_ai.output.messages.otel`).
- Confirm Langfuse displays only one final input/output payload.
- Confirm the displayed payload comes from `gen_ai.input.messages.otel` and `gen_ai.output.messages.otel`.
- Confirm Langfuse does not parse deprecated compatibility attributes (`gen_ai.input.messages`, `gen_ai.output.messages`) for display.
- Confirm multimodal `parts`, `tool_call`, `tool_call_response`, and `reasoning` survive in the displayed payload.