# Session Multimodal Externalization

This targeted example demonstrates the opt-in session multimodal externalization flow.

It uses an in-memory session backend, an in-memory artifact service, and a recording fake model, so it runs without external API keys.

## What It Shows

- How to enable `runner.WithSessionMultimodalExternalization`.
- Why an `artifact.Service` is required when the feature is enabled.
- How the model request still sees normal inline multimodal content during the current turn.
- How the persisted session event stores `ContentRef` instead of inline image/file payloads.
- How a later turn hydrates persisted `ContentRef` back into normal `ContentParts` before building the model request.

## Run

```bash
cd examples/session
go run ./multimodal_externalization
```

## Expected Output

The exact artifact name is generated at runtime, but the boolean checks should look like this:

```text
After first turn:
- model request has image bytes: true
- persisted image bytes empty: true
- persisted image ContentRef present: true
- persisted data URL removed from file part: true
- persisted file ContentRef present: true
After second turn:
- second request has hydrated historical image bytes: true
- second request leaks artifact refs: false
```

## Notes

- `artifact/inmemory` is used only for the demo. Production code should use an artifact backend that matches the application's persistence and lifecycle needs.
- The feature is disabled by default. Existing applications keep inline session payloads unless they opt in.
- Standard `ContentParts` image/audio/file inline bytes and data URLs are governed. Ordinary remote URLs and provider file IDs are not re-hosted by this feature.
