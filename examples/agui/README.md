# AG-UI Examples

This folder collects runnable demos that showcase how to integrate the `tRPC-Agent-Go` AG-UI server and various clients.

- [`client/`](client/) – General client-side samples.
- [`server/`](server/) – General server-side samples.
- [`messagessnapshot/`](messagessnapshot/) – Example showing how to enable and consume message snapshots.

## Quick Start

1. Start the default AG-UI server:

```bash
go run ./server/default
```

2. In another terminal start the CopilotKit client:

```bash
cd ./client/copilotkit
pnpm install
pnpm dev
```

3. Ask a question such as `Calculate 2*(10+11)` and watch the live event stream in the terminal. A full transcript example is documented in [`client/copilotkit/README.md`](client/copilotkit/README.md).

See the individual README files under `client/` and `server/` for more background and configuration options.
