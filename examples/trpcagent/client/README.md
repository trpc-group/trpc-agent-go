# tRPC-Agent API Client

This example calls a `server/trpcagent` HTTP service through the `runner/trpcagent` remote runner.

It demonstrates:

- Fetching the remote app structure with `Describe`.
- Running the remote app through the standard runner interface.

## Run

Start the server example first:

```bash
go run ./trpcagent/server
```

Then run the client from the `examples` module:

```bash
go run ./trpcagent/client
```

The server example still owns the model provider configuration. The client only creates a remote runner and sends one request to the default local service.
