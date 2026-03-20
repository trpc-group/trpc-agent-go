# A2A Structured Error Handling

This example shows how to expose structured business errors across the A2A
boundary and reconstruct them on the client as `Response.Error`.

It runs one local process that contains:

- an A2A server
- a backend agent that always returns a structured error
- a unary A2A client
- a streaming A2A client

## Why this example matters

Without a structured convention, an A2A caller often receives only a failed
task or a plain text error message.

That is usually not enough for business code that needs:

- stable error codes
- machine-readable error types
- a single client-side branching path based on `evt.Response.Error`

This example enables the framework path that solves that problem.

## Server-side setup

The server enables structured task errors:

```go
server, err := a2aserver.New(
    a2aserver.WithHost(host),
    a2aserver.WithAgent(&structuredErrorAgent{}, true),
    a2aserver.WithStructuredTaskErrors(true),
)
```

With this option enabled:

- unary requests return a failed `Task`
- streaming requests emit a failed `TaskStatusUpdateEvent`
- the task metadata keeps `error_type`, `error_code`, and `error_message`

## Client-side setup

The client does not need a custom converter for the default framework
convention.

It can use a normal `A2AAgent`:

```go
remoteAgent, err := a2aagent.New(
    a2aagent.WithAgentCardURL(serverURL),
    a2aagent.WithEnableStreaming(true),
)
```

When the remote task reaches a terminal failed state, `A2AAgent` reconstructs
the error into `evt.Response.Error`.

That means business code can keep one standard rule:

- consume events
- branch on `evt.Response.Error`
- read `Type`, `Code`, and `Message`

## Run the example

```bash
go run ./examples/a2aagent/error_handling
```

To use a different port:

```bash
go run ./examples/a2aagent/error_handling \
  -host 127.0.0.1:19999
```

## What to look for in the output

The example runs two scenarios:

- `unary`
- `streaming`

Both should print a structured error like:

```text
structured error: type=flow_error code=REMOTE_VALIDATION_FAILED ...
```

The streaming case should also report:

```text
assistant content events: 0
```

That confirms the client did not emit a synthetic normal assistant message after
the terminal task error.

## What business code may still extend

- If a third-party A2A provider uses different metadata keys, add a custom
  `a2aagent.A2AEventConverter`
- If your domain needs extra fields, keep using your own metadata keys in
  addition to the standard error metadata
- If some remote task states should map differently, implement a custom
  converter and register it with `a2aagent.WithCustomEventConverter(...)`

## Recommended production pattern

- Keep your domain error codes in a business package and emit them through
  `Response.Error.Code`.
- Enable `a2aserver.WithStructuredTaskErrors(true)` on servers that should
  expose those codes across the A2A boundary.
- Keep client branching logic on `evt.Response.Error` instead of inventing a
  second task-error abstraction in application code.

For a fuller design guide with complete code snippets, see
[`docs/mkdocs/en/error-handling.md`](../../../docs/mkdocs/en/error-handling.md)
and [`docs/mkdocs/en/runner.md`](../../../docs/mkdocs/en/runner.md).
