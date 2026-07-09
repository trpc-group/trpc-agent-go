# tRPC-Agent API Examples

These examples show the two sides of the tRPC-Agent API.

- `server` exposes a local `tRPC-Agent-Go` agent as an HTTP tRPC-Agent API service.
- `client` calls that HTTP service through `runner/trpcagent`, using the standard runner interface.

## Run

Start the server from the `examples` module:

```bash
go run ./trpcagent/server
```

Then run the client in another terminal:

```bash
go run ./trpcagent/client
```

Both examples default to app `calculator`, base path `/trpc-agent/v1/apps`, and target `http://127.0.0.1:8080`.

The server owns the model provider configuration and needs the same environment as `model/openai`, such as `OPENAI_API_KEY` and compatible base URL variables. The client only creates a remote runner and sends one request to the default local service.

See `server/README.md` and `client/README.md` for the focused usage of each side.
