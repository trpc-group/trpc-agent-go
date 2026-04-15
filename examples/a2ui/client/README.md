# A2UI Client Demo

This is a lightweight frontend demo that consumes the A2UI example server SSE stream directly in a browser. It is a reusable demo renderer for the examples in this repo, not a full reference implementation of every A2UI transport and catalog feature.

## Features

- Configure the A2UI endpoint (default: `http://127.0.0.1:8080/a2ui`).
- Configure or reset `Thread ID`.
- Send natural-language prompts to the server.
- Observe AG-UI and A2UI streams, with a switch to view one at a time.
- Render `surfaceUpdate`, `dataModelUpdate`, and `deleteSurface` to visible UI components.
- Send `userAction` events from explicit action components and keep data-bound input state local when the component contract allows it.

## Quick start

Start the server first, then run the static client:

```bash
cd examples/a2ui/client
python3 -m http.server 4173
```

Open:

```text
http://127.0.0.1:4173
```

## UI usage

- `A2UI endpoint`: target AG-UI/A2UI URL.
- `Thread ID`: conversation identifier. If empty, a 7-character alphabetic value is auto-generated.
- `Prompt`: input and submit a normal user message.
- `Send`: starts a run and begins SSE consumption.
- `AG-UI / A2UI`: toggle visible logs.
- Render canvas: shows the current surface and interactive controls.

## Example transport payloads

Normal text prompt:

```json
{
  "messages": [
    {
      "role": "user",
      "content": "Show me a restaurant menu."
    }
  ]
}
```

Action-triggered request in this demo sends:

```json
{
  "messages": [
    {
      "role": "user",
      "content": "{\"userAction\":{...}}"
    }
  ]
}
```

## Notes

- This frontend intentionally keeps dependencies minimal and does not rely on extra UI frameworks.
- The request payload format shown here is the example server contract used in this repo, not a claim about every possible A2UI server transport.
- The renderer focuses on the subset of A2UI components and binding patterns used by the bundled examples.
