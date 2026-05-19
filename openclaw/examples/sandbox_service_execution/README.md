# OpenClaw Sandbox Service Execution Example

This example validates `tools.code_executor.type: sandbox` through a real
OpenClaw service process. It starts `cmd/openclaw`, waits for the HTTP gateway,
then sends requests to `/v1/gateway/messages:stream` so the model and sandbox
code executor run across the same boundary as a deployed local service.

From the repository root, source the OpenAI-compatible GLM credentials without
printing them:

```bash
source ./glm.sh
```

Then run from the `openclaw` module:

```bash
cd openclaw
go run ./examples/sandbox_service_execution \
  -config ./examples/sandbox_service_execution/openclaw.yaml \
  -scenario all
```

The harness uses a temporary state directory and random loopback port for each
scenario. It does not write API keys into the generated config or print secret
values. The OpenClaw service inherits the model credentials, while sandboxed
programs use `shell_env.inherit: core` with default secret-like excludes.

Assertions read the sandbox execution result from the service debug trace runner
events, not from the Gateway final reply. This keeps the example scoped to
service-level validation without changing Gateway aggregation behavior.

Scenarios include Python execution, session workspace persistence, secret
environment redaction, restricted networking, timeout handling, and output
truncation.

Linux managed sandbox execution requires `bwrap` and user namespace support.
Use `-require-os-sandbox=false` only when you want unavailable sandbox setup to
skip scenarios instead of failing.
