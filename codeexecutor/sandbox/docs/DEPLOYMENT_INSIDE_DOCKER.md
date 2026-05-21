# Deployment inside Docker

The Linux managed sandbox backend uses `bubblewrap` (`bwrap`) to create user,
PID, mount, and optional network namespaces for each sandboxed command. This
works directly on Linux hosts that provide namespace support, but it often
fails inside a default Docker or Kubernetes container because the outer
container runtime filters namespace and mount operations through seccomp and
capability restrictions.

This document describes Docker launch options from lower to higher privilege.
Use the smallest option that passes on your runtime. The sandbox executor's
profile still controls the child process file system, network, timeout, and
environment policy inside that container.

## Summary

| Docker options | What to validate | Common result | What it means |
| --- | --- | --- | --- |
| Default Docker | `--unshare-user` probe | Often fails with `No permissions to create new namespace` | The container cannot create user namespaces. |
| `--security-opt seccomp=unconfined` | `--unshare-user` probe | Usually passes | User namespaces are available, but this does not prove the full sandbox can mount `/dev` or `/proc`. |
| `--security-opt seccomp=unconfined` | Full no-proc probe | Passes when only fresh `/proc` mounting is blocked | Managed sandbox can run with PID isolation and without `--proc /proc`; this is the lowest-permission working mode on many Docker hosts. |
| `--security-opt seccomp=unconfined --security-opt systempaths=unconfined --cap-add SYS_ADMIN` | Full with-proc probe | Passes when fresh `/proc` mounting is required | The container can create the complete bwrap view including a fresh `/proc`. |
| `--privileged` | Full with-proc probe | Usually passes | Broad fallback when the platform cannot express narrower permissions. |

## Runtime Probe

Before running a managed command, the Linux backend probes `bwrap` with the same
core namespace and mount flags used by real sandbox runs:

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

If that fails with known proc mount errors such as
`Can't mount proc on /newroot/proc: Operation not permitted`, the backend
automatically retries without `--proc /proc`. If the retry succeeds, managed
sandbox runs keep PID isolation but skip mounting a fresh `/proc`.

Managed sandbox runs then add bind mounts for the workspace and granted paths,
masks for protected paths, and `--unshare-net` when networking is restricted.
Other namespace, mount, executable lookup, or policy setup failures still make
managed sandbox startup fail rather than falling back to unsandboxed execution.

## Default Docker

Use default Docker only as a baseline. On many hosts it fails before the managed
sandbox starts because `bwrap --unshare-user` cannot create a user namespace:

```bash
docker run --rm \
  --entrypoint bwrap \
  your-image \
  --die-with-parent --unshare-user --ro-bind / / -- /bin/true
```

If this reports `No permissions to create new namespace`, allow user namespaces
with unconfined seccomp.

## User Namespace Only

The lowest Docker setting that commonly allows `bwrap --unshare-user` is
unconfined seccomp:

```bash
docker run --rm \
  --security-opt seccomp=unconfined \
  --entrypoint bwrap \
  your-image \
  --die-with-parent --unshare-user --ro-bind / / -- /bin/true
```

Passing this check means `--unshare-user` is available. It does not by itself
prove that the full managed sandbox can mount `/dev`, create PID namespaces, or
mount a fresh `/proc`.

## No-Proc Fallback

Some containers allow user and PID namespaces but reject mounting a fresh
`/proc`. In that environment, the with-proc probe fails with text such as
`Can't mount proc on /newroot/proc: Operation not permitted`, while the no-proc
probe succeeds:

```bash
docker run --rm \
  --security-opt seccomp=unconfined \
  --entrypoint bwrap \
  your-image \
  --die-with-parent \
  --unshare-user \
  --unshare-pid \
  --new-session \
  --ro-bind / / \
  --dev /dev \
  -- /bin/true
```

This is the lowest-permission Docker mode that can run the managed sandbox on
hosts where only fresh `/proc` mounting is blocked. In this mode the backend
logs:

```text
managed sandbox notice: bwrap fresh /proc mount unavailable; using no-proc fallback
```

## Fresh Proc Mounting

If your deployment requires a fresh `/proc` inside the sandbox, or if the
no-proc fallback is not acceptable for your environment, grant the outer
container enough permissions for the with-proc probe to pass:

```bash
docker run --rm \
  --security-opt seccomp=unconfined \
  --security-opt systempaths=unconfined \
  --cap-add SYS_ADMIN \
  --entrypoint bwrap \
  your-image \
  --die-with-parent \
  --unshare-user \
  --unshare-pid \
  --new-session \
  --ro-bind / / \
  --dev /dev \
  --proc /proc \
  -- /bin/true
```

These flags do not disable the sandbox executor. They allow the trpc-agent-go
process inside the outer container to create the nested sandbox for executed
code.

## Privileged Fallback

If your platform cannot set seccomp, system path, and capability options
separately, use a privileged container only as a deployment-platform fallback or
debugging path:

```bash
docker run --rm -it \
  --privileged \
  your-image
```

`--privileged` grants much broader access to the outer container than the
options above. Treat it as a larger trust boundary: `bwrap` still isolates the
code executed by the sandbox backend, but the service process that launches
`bwrap` is running with elevated container privileges.

## Kubernetes and Managed Platforms

For Kubernetes-style deployments, map the Docker options above to the pod or
container security context.

User namespace and no-proc fallback modes usually require at least unconfined
seccomp:

```yaml
securityContext:
  seccompProfile:
    type: Unconfined
```

Fresh `/proc` mounting adds the capability and proc visibility needed for the
with-proc path:

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

On managed platforms such as 123, prefer a service-specific whitelist for the
smallest required permissions. Use full privileged mode only when the platform
cannot express the smaller permission set.

## Failure Guide

- `bubblewrap executable not found in PATH`: install `bubblewrap` in the image.
- `No permissions to create new namespace`: use
  `--security-opt seccomp=unconfined` and retry the user namespace probe.
- `Can't mount proc on /newroot/proc: Operation not permitted`: the no-proc
  fallback should apply, or use the fresh-proc permissions if your deployment
  requires `/proc`.
- `Can't access /newroot/proc/sysrq-trigger: Read-only file system`: upgrade
  `bubblewrap` to 0.5.0 or newer. That message matches an older Docker `/proc`
  handling issue fixed by bubblewrap 0.5.0.
- Other namespace, mount, executable lookup, or policy setup failures still make
  managed sandbox startup fail rather than falling back to unsandboxed
  execution.

## Impact Scope

The Docker or Kubernetes permissions in this document change the security
boundary of the outer service container. They do not change the sandbox profile
selected by `codeexecutor/sandbox`, and they do not grant model-generated code
direct host access by themselves.

The `bwrap` child sandbox still applies the configured executor controls:

- file system access is limited by the selected permission profile and explicit
  path grants;
- restricted networking still uses a network namespace for executed code;
- shell environment filtering still controls which variables are visible to
  sandboxed commands;
- timeouts and output caps still apply to each sandboxed run.

Keep the permission grant scoped to the deployment that needs managed sandbox
code execution. Avoid sharing the same elevated container with unrelated
workloads, and prefer the lowest passing option over `--privileged` whenever the
platform supports it.
