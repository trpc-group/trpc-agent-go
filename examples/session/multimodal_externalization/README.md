# Session Multimodal Externalization

This targeted example demonstrates the opt-in session multimodal externalization flow.

It uses an in-memory session backend, an in-memory artifact service, and a recording fake model, so it runs without external API keys.

## What It Shows

- Enabling session multimodal externalization with `session/multimodal.Wrap`.
- Why an `artifact.Service` is required when the feature is enabled.
- The model request still sees normal inline multimodal content during the current turn.
- The persisted session event stores `ContentRef` instead of inline image/file payloads.
- A later turn hydrates persisted `ContentRef` back into normal `ContentParts` before building the model request.

## Run

```bash
cd examples/session
go run ./multimodal_externalization
```

## Expected Output

The exact artifact names are generated at runtime, so the artifact refs below are shown as placeholders:

```text
After first turn:
Runtime request sent to model:
- image bytes len: 15
- file data URL present: true
- contains ContentRef: false
Persisted session event:
- image bytes len: 0
- image artifact ref: artifact://sessionpart_<generated>.png@0
- file URL len: 0
- file artifact ref: artifact://sessionpart_<generated>.<ext>@0
After second turn:
Runtime request sent to model:
- hydrated historical image bytes len: 15
- contains ContentRef: false
```

## Notes

- `artifact/inmemory` is used only for the demo. Production code should use an artifact backend that matches the application's persistence and lifecycle needs.
- The feature is disabled by default. Existing applications keep inline session payloads unless they opt in.
- Pass the wrapped session service to `runner.WithSessionService`, and reuse the wrapped service for any direct session writes that should be governed.
- Standard `ContentParts` image/audio/file inline bytes and data URLs are governed. Ordinary remote URLs and provider file IDs are not re-hosted by this feature.
