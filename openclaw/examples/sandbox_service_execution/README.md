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

## Docker validation

Build the validation image from the repository root so the `openclaw` module can
use its local `replace ../...` dependencies:

```bash
docker build \
  -f openclaw/examples/sandbox_service_execution/Dockerfile \
  -t openclaw-sandbox-service-execution .
```

The image contains Go, `bwrap`, `python3`, Bash, and CA certificates. It does
not contain model credentials. Pass credentials from the host environment:

```bash
source ./glm.sh
```

### Test matrix

| Container mode | Command | Expected result |
| --- | --- | --- |
| Default Docker | `docker run --rm -e OPENAI_API_KEY=dummy -e OPENAI_BASE_URL=http://127.0.0.1 -e MODEL_NAME=dummy openclaw-sandbox-service-execution -config ./examples/sandbox_service_execution/openclaw.yaml -scenario basic-python` | Usually fails during `bwrap` preflight before any model call. If the only blocked operation is mounting a fresh `/proc`, the example follows the runtime no-proc fallback and may continue. |
| Minimal permissions | `docker run --rm --security-opt seccomp=unconfined --security-opt systempaths=unconfined --cap-add SYS_ADMIN -e OPENAI_BASE_URL -e OPENAI_API_KEY -e MODEL_NAME openclaw-sandbox-service-execution` | Runs `-scenario all`; all scenarios should pass when model credentials are valid. |
| Privileged fallback | `docker run --rm --privileged -e OPENAI_BASE_URL -e OPENAI_API_KEY -e MODEL_NAME openclaw-sandbox-service-execution` | Runs `-scenario all`; should pass, but grants broader permissions to the outer container. |

To run one scenario, pass flags after the image name:

```bash
docker run --rm \
  --security-opt seccomp=unconfined \
  --security-opt systempaths=unconfined \
  --cap-add SYS_ADMIN \
  -e OPENAI_BASE_URL \
  -e OPENAI_API_KEY \
  -e MODEL_NAME \
  openclaw-sandbox-service-execution \
  -config ./examples/sandbox_service_execution/openclaw.yaml \
  -scenario network-restricted
```

For Kubernetes-style deployments, the minimal Docker permissions map to:

```yaml
securityContext:
  capabilities:
    add:
      - SYS_ADMIN
  seccompProfile:
    type: Unconfined
  procMount: Unmasked
```

If the platform only exposes a privileged-container switch, use
`securityContext.privileged: true` as a fallback. On managed platforms such as
123, prefer a service-specific whitelist for `SYS_ADMIN` plus unconfined
seccomp before falling back to full privileged mode.

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
When a container denies fresh `/proc` mounting with errors such as
`Can't mount proc on /newroot/proc: Operation not permitted`, the runtime retries
without `--proc /proc` while keeping bwrap isolation. If the error is
`Can't access /newroot/proc/sysrq-trigger: Read-only file system`, upgrade the
image to `bubblewrap` 0.5.0 or newer. Use `-require-os-sandbox=false` only when
you want unavailable sandbox setup to skip scenarios instead of failing.
