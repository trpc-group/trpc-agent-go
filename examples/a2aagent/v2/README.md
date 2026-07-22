# A2A v2 Example

This example starts a deterministic trpc-agent-go agent behind the A2A v2
server adapter, discovers it through the v2 client adapter, and opens an
interactive chat. Both adapters use A2A protocol v1.0, and the example does not
require a model or API key.

The server uses the stateless task manager by default. A2A Tasks exist for the
request lifecycle, while trpc-agent-go's session service owns the conversation
context.

## Run

Run the example from the `examples` module:

```bash
cd examples
go run ./a2aagent/v2
```

Use `-streaming=false` to exercise the blocking `message/send` path:

```bash
go run ./a2aagent/v2 -streaming=false
```
