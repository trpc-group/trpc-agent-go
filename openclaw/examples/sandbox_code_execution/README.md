# OpenClaw Sandbox Code Execution Example

This example validates OpenClaw-style `tools.code_executor.type: sandbox`
configuration without enabling the generic `workspace_exec` tool surface.

From the repository root, export OpenAI-compatible model credentials:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="your-base-url"
export MODEL_NAME="your-model-name"
```

Then run the scenarios from the `openclaw` module:

```bash
cd openclaw
go run ./examples/sandbox_code_execution \
  -config ./examples/sandbox_code_execution/openclaw.yaml \
  -scenario all
```

Scenarios include deterministic Python execution, session persistence, secret
environment redaction, restricted networking, timeout handling, output
truncation, and verifying that `workspace_exec` remains hidden.
