# Observability Harness

This directory contains local harness assets for observability validation, with
the current focus on Langfuse-based telemetry verification.

- Install the Langfuse skill before querying Langfuse data. The skill is open source on [GitHub](https://github.com/langfuse/skills) and can access data from the Langfuse platform.
- If your environment supports the `gh skill` command, you can install it with `gh skill install langfuse/skills`.
- `.harness/observability/.env` already contains the key for accessing the test project on the Langfuse platform.
- `.harness/observability/.env` already contains access to an LLM that supports OpenAPI calls and real model invocations.
- Put Langfuse observability test code under `.harness/observability/langfuse`.