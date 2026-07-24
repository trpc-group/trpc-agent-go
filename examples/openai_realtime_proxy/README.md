# OpenAI Realtime Proxy Example

This example exposes a local WebSocket endpoint and forwards OpenAI Realtime
events bidirectionally. The API key stays on the server; clients do not receive
it.

This first milestone is transport-only. It does not invoke a tRPC Agent runner,
execute local tools, or persist transcripts.

## Run

From the repository root:

```bash
export OPENAI_API_KEY="..."
cd examples/openai_realtime_proxy
go run . -model gpt-realtime -addr :8080
```

The local endpoint is `ws://localhost:8080/v1/realtime`.

## Send a text-only session

Connect with a WebSocket client such as `websocat`:

```bash
websocat ws://localhost:8080/v1/realtime
```

Then send these events one line at a time:

```json
{"type":"session.update","session":{"modalities":["text"]}}
{"type":"conversation.item.create","item":{"type":"message","role":"user","content":[{"type":"input_text","text":"Say hello in one sentence."}]}}
{"type":"response.create","response":{"modalities":["text"]}}
```

The proxy preserves complete JSON events, including event types that were added
after the installed framework version. Production deployments should add client
authentication, origin validation, TLS, and request limits around the handler.
