# TDesign Chat Client for AG-UI

This example provides a lightweight web chat UI built with `tdesign-react`. It connects to any AG-UI server that exposes an SSE endpoint (for example the servers under `examples/agui/server/*`) and renders:

- Streaming assistant text (`TEXT_MESSAGE_*`)
- Tool calls / results (`TOOL_CALL_*`)
- Custom events (`CUSTOM`) including:
  - Think stream (`think_*`) as a dedicated “Thinking” block
  - React planner tags (`react.*`)
  - Progress updates (`node.progress`)
- Graph activity patches (`ACTIVITY_DELTA`) including Human-in-the-loop interrupts
- Report mode document sidebar (`open_report_document` / `close_report_document`)

## Start

```bash
pnpm install
pnpm dev
```

By default, the app connects directly to `http://127.0.0.1:8080/agui` (no proxy required).

In the header you can configure:

- `IP:Port` + `实时对话` (or a full URL) for the AG-UI SSE endpoint.
- `Thread` (defaults to a short random id) and `User` for session routing.
- `消息快照` (defaults to `/history`) to load a message snapshot when applying the connection.

Then open the printed local URL (typically `http://localhost:5173`).
