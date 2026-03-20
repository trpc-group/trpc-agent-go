# A2UI Example

This example demonstrates how to wire the trpc-agent-go AG-UI server with the A2UI translator and render A2UI messages in a browser in real time.

## Example Structure

```text
a2ui/
├── server/
│   ├── main.go          # A2UI server bootstrap
│   └── agent.go         # Agent example configured with A2UI planner
├── client/
│   ├── index.html       # Demo frontend page
│   ├── client.js        # SSE stream consumer, logs, and A2UI renderer
│   └── README.md        # Frontend README
├── README.md            # This file
└── go.mod
```

## Highlights

- Runs an AG-UI server with A2UI translator enabled.
- In default mode, AG-UI control events (for example `RUN_*`) are forwarded while non-text events are dropped by default.
- Provides an interactive browser client that:
  - Shows AG-UI raw event stream and A2UI parsed event stream.
  - Renders A2UI `surfaceUpdate` payloads into an interactive UI.
  - Sends `userAction` events when a rendered button is clicked.
- The client generates a 7-character alphabetic `threadId` by default; refreshing the page creates a new conversation id.

## Demo Screenshot

![A2UI Example Screenshot](../../.resource/images/examples/a2ui.png)

## Prerequisites

1. Configure model credentials (at least one valid LLM provider).

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"   # or your compatible endpoint
```

2. Ensure `go run` works in your environment.

## Run the server

```bash
cd examples/a2ui/server
go run .
```

Optional flags:

- `-model` (default: `gpt-5.4`): LLM model name.
- `-stream` (default: `true`): whether streaming is enabled.
- `-address` (default: `127.0.0.1:8080`): listen address.
- `-path` (default: `/a2ui`): AG-UI/A2UI HTTP path.

Default endpoint:

- `http://127.0.0.1:8080/a2ui`

## Run the client (SSE visualizer)

In a second terminal:

```bash
cd examples/a2ui/client
python3 -m http.server 4173
```

Open:

```text
http://127.0.0.1:4173
```

## Usage

1. Set `A2UI Endpoint` in the top panel (pre-filled with `http://127.0.0.1:8080/a2ui`).
2. Enter a `Thread ID` (or use the auto-generated one).
3. Type a prompt and click `Send`.
4. On the right side, observe:
  - AG-UI events (`run_started`, `run_finished`, `run_error`, `raw`, etc.)
  - A2UI parsed events
  - Rendered A2UI UI output
5. Click buttons on the rendered UI to send `userAction` events back to the server.

## Log behavior

The client exposes both AG-UI and A2UI event streams and allows switching between them.
The server only logs error-level messages in this example and does not print every request or response payload.

## Troubleshooting

- No frontend response:
  - Verify endpoint URL and port match the server.
  - Make sure the `go run .` process in `examples/a2ui/server` is still running.
- Missing or incomplete rendering:
  - Verify the server emits valid A2UI events such as `surfaceUpdate` and `beginRendering`.
  - Check that AG-UI raw event payloads contain A2UI-compatible data and source.
- Unexpected cross-session behavior:
  - Keep using the same `Thread ID` for one conversation, or intentionally start a new one.

## Key files

- `server/main.go`: server startup, path, and runner configuration.
- `server/agent.go`: A2UI planner and LLM agent setup.
- `client/index.html`: frontend layout.
- `client/client.js`: SSE parser, event rendering, and action submission.

## Expected verification

- AG-UI stream and rendered UI update appears in sync.
- Initial surfaces (such as a menu or form) are rendered on the right panel.
- Button interactions trigger new requests and drive the next UI state.
