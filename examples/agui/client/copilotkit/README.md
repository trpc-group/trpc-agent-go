# CopilotKit Front-End for the AG-UI Server

This example shows how to pair the Go-based AG-UI server with a React front-end
built on [CopilotKit](https://docs.copilotkit.ai/). The UI streams Server-Sent
Events from the AG-UI endpoint using the `@ag-ui/client` HTTP agent and renders
an assistant sidebar provided by CopilotKit.

> The example lives in `ui/`, while a full copy of the upstream CopilotKit
> repository is available under `CopilotKit/` for reference and advanced usage.

## Prerequisites

- Node.js 18+ and pnpm (or npm/yarn)
- Go 1.21+
- Access to an LLM model supported by the AG-UI server setup (see
  `../server/default`)

## 1. Launch the AG-UI server

```bash
cd support-agui/trpc-agent-go/examples/agui/server/default
GOOGLE_API_KEY=... go run .
```

By default the server listens on `http://127.0.0.1:8080/agui`. Adjust
`--address`, `--path`, or model flags if required.

## 2. Start the CopilotKit client

```bash
cd support-agui/trpc-agent-go/examples/agui/client/copilotkit/ui
pnpm install   # or npm install
pnpm dev       # or npm run dev
```

If you need to pin dependencies manually, make sure to use a published
`@ag-ui/client` version (the example uses `^0.0.38`).

Available environment variables before `pnpm dev`:

- `AG_UI_ENDPOINT`: override the AG-UI endpoint URL (defaults to
  `http://127.0.0.1:8080/agui`).
- `AG_UI_TOKEN`: optional bearer token forwarded as an `Authorization` header.

Open `http://localhost:3000` and start chatting with the full-screen assistant
UI. The input shows the placeholder `Calculate 2*(10+11), first explain the
idea, then calculate, and finally give the conclusion.`—press Enter to run that
scenario or type your own request. Tool calls and their results appear inline
inside the chat transcript.

## How it works

- `ui/app/api/copilotkit/route.ts` instantiates a `CopilotRuntime` and registers
  a single AG-UI agent via `new HttpAgent({ url: ... })`.
- The front-end wraps the App Router layout in `<CopilotKit>` and renders a
  `CopilotChat`, filling the page with a streaming AG-UI conversation.
- `ui/app/page.tsx` overrides `RenderMessage` so AG-UI tool invocations and
  outputs show up inline with the assistant replies.

Use this scaffold as a starting point for richer CopilotKit integrations, or
explore the full upstream examples in `CopilotKit/examples/` for more advanced
patterns such as shared state or tool-driven UI.
