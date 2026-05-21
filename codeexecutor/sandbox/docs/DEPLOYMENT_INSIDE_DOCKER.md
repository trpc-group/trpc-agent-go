# Deployment inside Docker

The Linux managed sandbox backend uses `bubblewrap` (`bwrap`) to create user,
PID, mount, and optional network namespaces for each sandboxed command. This
works directly on Linux hosts that provide namespace support, but it often
fails inside a default Docker or Kubernetes container because the outer
container runtime filters namespace and mount operations through seccomp and
capability restrictions.

This document describes the permissions required by the outer container. The
sandbox executor's profile still controls the child process file system,
network, timeout, and environment policy inside that container.

## Why default Docker may fail

The Linux backend probes `bwrap` before running a command with a minimal setup
similar to:

```bash
bwrap \
  --die-with-parent \
  --unshare-user \
  --unshare-pid \
  --new-session \
  --ro-bind / / \
  --dev /dev \
  --proc /proc \
  -- /bin/true
```

If that probe fails only because the container runtime rejects mounting a fresh
`/proc` (for example `Can't mount proc on /newroot/proc: Operation not
permitted`), the backend automatically retries the probe without
`--proc /proc`. When the retry succeeds, managed sandbox runs keep PID isolation
but skip the fresh `/proc` mount.

If the probe fails with `Can't access /newroot/proc/sysrq-trigger: Read-only
file system`, upgrade the image to `bubblewrap` 0.5.0 or newer. That message
matches an older Docker `/proc` handling issue fixed by bubblewrap 0.5.0, so the
backend reports it as a setup error instead of using the no-proc fallback.

Managed sandbox runs then add PID isolation, optional `/proc` mounting, bind
mounts for the workspace and granted paths, masks for protected paths, and
`--unshare-net` when networking is restricted.

Default Docker settings commonly block one of these operations. Typical failure
messages are surfaced as setup/backend errors such as `bubblewrap preflight
failed`, `Operation not permitted`, or a namespace/mount related error.

## Recommended Docker permissions

Prefer the smallest outer-container permission set that lets `bwrap` complete
the preflight:

```bash
docker run --rm -it \
  --security-opt seccomp=unconfined \
  --security-opt systempaths=unconfined \
  --cap-add SYS_ADMIN \
  your-image
```

These flags do not disable the sandbox executor. They allow the OpenClaw or
trpc-agent-go process inside the outer container to create the nested sandbox
for executed code.

If your platform cannot set seccomp and capability options separately, use a
privileged container only as a deployment-platform fallback or debugging path:

```bash
docker run --rm -it \
  --privileged \
  your-image
```

`--privileged` grants much broader access to the outer container than the
minimal option above. Treat it as a larger trust boundary: `bwrap` still isolates
the code executed by the sandbox backend, but the service process that launches
`bwrap` is running with elevated container privileges.

## Impact scope

The Docker or Kubernetes permissions in this document change the security
boundary of the outer service container. They do not change the sandbox profile
selected by `codeexecutor/sandbox`, and they do not grant model-generated code
direct host access by themselves.

The main impact is that every process already running in the service container
can use the added container privileges, not only `bwrap`. With the minimal
permission set, the service container can perform mount and namespace operations
needed to create nested sandboxes. With `--privileged`, the service container
gets a much broader set of device, capability, and kernel-interface access.

The `bwrap` child sandbox still applies the configured executor controls:

- file system access is limited by the selected permission profile and explicit
  path grants;
- restricted networking still uses a network namespace for executed code;
- shell environment filtering still controls which variables are visible to
  sandboxed commands;
- timeouts and output caps still apply to each sandboxed run.

Therefore, enabling these outer-container permissions should be treated as
expanding trust in the OpenClaw or trpc-agent-go service process and its
dependencies. It is not equivalent to granting the same permissions to each
model-generated command, but a compromise of the service process would have a
larger container-level impact than it would under default Docker settings.

Keep the permission grant scoped to the deployment that needs managed sandbox
code execution. Avoid sharing the same elevated container with unrelated
workloads, and prefer the minimal permission set over `--privileged` whenever
the platform supports it.

## Kubernetes and managed platforms

For Kubernetes-style deployments, map the minimal Docker option to the pod or
container security context:

```yaml
securityContext:
  capabilities:
    add:
      - SYS_ADMIN
  seccompProfile:
    type: Unconfined
  procMount: Unmasked
```

If the platform only exposes a privileged-container switch, the equivalent
fallback is:

```yaml
securityContext:
  privileged: true
```

On managed platforms such as 123, prefer a whitelist that grants `SYS_ADMIN`
and unconfined seccomp to the specific service that needs managed sandbox code
execution. Use full privileged mode only when the platform cannot express the
smaller permission set.

## Validation commands

First verify that `bwrap` exists in the image:

```bash
command -v bwrap
```

Then run the preflight probe:

```bash
bwrap \
  --die-with-parent \
  --unshare-user \
  --unshare-pid \
  --new-session \
  --ro-bind / / \
  --dev /dev \
  --proc /proc \
  -- /bin/true
```

If the only failure is `Can't mount proc on /newroot/proc` with
`Operation not permitted`, `Permission denied`, or `Invalid argument`, validate
the automatic fallback path:

```bash
bwrap \
  --die-with-parent \
  --unshare-user \
  --unshare-pid \
  --new-session \
  --ro-bind / / \
  --dev /dev \
  -- /bin/true
```

The fallback is intentionally narrow. Namespace, mount, executable lookup, or
other setup failures still make managed sandbox startup fail rather than falling
back to unsandboxed execution.

If the failure is `Can't access /newroot/proc/sysrq-trigger: Read-only file
system`, upgrade `bubblewrap` to 0.5.0 or newer before retrying the validation.

For a full service-level check, run the OpenClaw sandbox service execution
example from the repository root:

```bash
docker build \
  -f openclaw/examples/sandbox_service_execution/Dockerfile \
  -t openclaw-sandbox-service-execution .

docker run --rm \
  --security-opt seccomp=unconfined \
  --security-opt systempaths=unconfined \
  --cap-add SYS_ADMIN \
  -e OPENAI_BASE_URL \
  -e OPENAI_API_KEY \
  -e MODEL_NAME \
  openclaw-sandbox-service-execution
```

The example also documents a full test matrix for default Docker, minimal
permissions, and privileged fallback.

## Operational notes

- Keep model and service credentials in environment variables or a secret
  manager. Do not bake them into Dockerfiles, images, or example configs.
- Use `shell_env.inherit: core` plus default secret-like excludes when sandboxed
  child processes do not need model credentials.
- The sandbox backend may skip mounting a fresh `/proc` for known
  restricted-container proc mount failures, but it reports the older
  `sysrq-trigger` read-only failure as an upgrade-required setup error and does
  not silently fall back to local execution when a managed OS sandbox is
  requested and `bwrap` setup fails.
- Docker, containerd, and managed Kubernetes runtimes can differ in kernel,
  seccomp, and namespace behavior. Validate on the same runtime class used in
  production.
