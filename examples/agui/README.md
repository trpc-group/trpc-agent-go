# AG-UI Examples

This folder collects runnable demos that showcase how to integrate the `tRPC-Agent-Go` AG-UI server and various clients.

- [`client/`](client/) – Client-side samples.
- [`server/`](server/) – Server-side samples.

## Quick Start

1. Start the default AG-UI server:

```bash
go run ./server/default
```

2. In another terminal start the Bubble Tea client:

```bash
go run ./client/bubbletea/main.go
```

3. Ask a question such as `calculate 1.2+3.5` and watch the live event stream in the terminal. A full transcript example is documented in [`client/bubbletea/README.md`](client/bubbletea/README.md).

See the individual README files under `client/` and `server/` for more background and configuration options.
