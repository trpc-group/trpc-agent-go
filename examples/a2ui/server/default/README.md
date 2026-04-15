# Default A2UI Server Example

This folder provides the basic server-side A2UI example.

## Contents

- `main.go`: AG-UI server bootstrap and translator wiring.
- `agent.go`: LLM agent registration, tool configuration, and A2UI planner setup.

## What this server does

- Creates an in-memory session service for AG-UI run state.
- Builds an AG-UI runner with A2UI translator enabled.
- Exposes HTTP endpoint:
  - default path: `/a2ui`
  - default address: `127.0.0.1:8080`
- Forwards control-type AG-UI events (`RUN_*`) by default while dropping unsupported non-text events unless custom pass-through options are introduced by callers.
- Keeps error logs only in this example to avoid noisy request-level output.

## Run the server directly

From repository root:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1" # Optional for OpenAI-compatible gateways.

cd examples/a2ui/server/default
go run .
```

The default endpoint is:

```text
http://127.0.0.1:8080/a2ui
```

## Runtime flags

- `-model`: model name, defaults to `gpt-5.4`
- `-stream`: stream mode, defaults to `true`
- `-address`: listen address, defaults to `127.0.0.1:8080`
- `-path`: HTTP path, defaults to `/a2ui`

## Related docs

- Server example index: [examples/a2ui/server/README.md](../README.md)
- Top-level example: [examples/a2ui/README.md](../../README.md)
- Frontend client: [examples/a2ui/client/README.md](../../client/README.md)
