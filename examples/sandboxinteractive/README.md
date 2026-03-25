# Sandbox Interactive Example

This example now supports two modes:

- `direct`: call `SandboxCoordinator.StartProgram(...)` directly
- `model`: let an `llmagent` trigger `workspace_exec` and `workspace_write_stdin`, while the same sandbox approval and backend selection logic stays in effect

## Direct Mode

Direct mode is fully local and does not require model credentials.

```bash
go run ./sandboxinteractive -mode direct
```

What it shows:

- a denied interactive request before session startup
- a console approval prompt for an interactive shell session
- backend selection logging for `StartProgram`
- a local interactive session that still runs through the sandbox coordinator

## Model Mode

Model mode requires your OpenAI-compatible environment variables such as
`OPENAI_API_KEY` and optionally `OPENAI_BASE_URL`.

```bash
go run ./sandboxinteractive -mode model -model deepseek-chat
```

After startup, the example enters a chat loop:

- type normal user input to talk to the agent
- type `/demo` to send the built-in interactive `workspace_exec` request
- type `/exit` to quit

When the agent naturally decides to start an interactive session, the console
approval prompt appears in the same terminal during that turn. The example uses
the same stdin reader for both chat input and approval input, so you can approve
or deny the session inline without switching modes.

The model demo also wires an explicit in-memory session service through
`runner.WithSessionService(inmemory.NewSessionService())`, so multi-turn chat
history is stored in the runner session instead of relying on the runner's
implicit default.

Optional flags:

```bash
go run ./sandboxinteractive \
  -mode model \
  -model deepseek-chat \
  -streaming=true \
  -timeout 60s
```

The default model prompt instructs the agent to:

- call `workspace_exec` with `background=true` and `tty=true`
- start a shell command that reads a name and prints sandboxed output
- follow up with `workspace_write_stdin`
- summarize the final result

## What It Demonstrates

- `SandboxCoordinator.StartProgram(...)`
- interactive approval before starting a session
- backend selection logging for both direct and model-driven execution
- `llmagent.WithCodeExecutor(...)` auto-registering `workspace_exec`
- `runner.WithSessionService(inmemory.NewSessionService())` explicitly preserving
  multi-turn conversation state for the model demo
- the same sandboxed interactive path being exercised from model tool calls
