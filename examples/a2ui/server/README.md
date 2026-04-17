# A2UI Server Examples

This directory contains the server-side A2UI examples.

## Layout

- `default/`: basic A2UI server bootstrap with one planner-driven agent.
- `sbti/`: graph-based SBTI server example built from a director agent and an A2UI renderer agent.

## Run examples

Default server example:

```bash
cd examples/a2ui/server/default
go run .
```

SBTI server example:

```bash
cd examples/a2ui/server/sbti
go run .
```

## Related docs

- Top-level example: [examples/a2ui/README.md](../README.md)
- Frontend client: [examples/a2ui/client/README.md](../client/README.md)
