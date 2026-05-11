# Sandbox Backend Support

The sandbox runtime provides a managed OS sandbox only where the package has a
native backend implementation. Other profiles may still be selected by callers,
but they do not provide local managed sandbox enforcement on every platform.

| Platform | Managed OS sandbox support | Backend | Notes |
| --- | --- | --- | --- |
| Linux | Supported | `linux-bubblewrap` | Uses `bubblewrap` with user, PID, mount, and optional network namespaces. |
| macOS | Not implemented | N/A | Managed profiles return an unsupported-backend error. Disabled profiles run without sandboxing. |
| Windows | Not implemented | N/A | Managed profiles return an unsupported-backend error. Disabled profiles run without sandboxing. |

## Prerequisites

On Linux, Sandbox Code Executor uses the `bwrap` executable found on `PATH`. If `bwrap` is unavailable, setup fails before the command starts.
