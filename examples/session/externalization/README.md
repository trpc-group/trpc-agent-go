# Session Content Externalization

This targeted example demonstrates the opt-in session content externalization flow.

It uses an in-memory session backend, an in-memory artifact service, and a recording fake model, so it runs without external API keys.

## What It Shows

- Enabling session content externalization with `session/externalization.Wrap`.
- Why an `artifact.Service` is required when the feature is enabled.
- The model request still sees normal inline image/file payloads during the current turn.
- The persisted session event stores `ContentRef` instead of inline image/file payloads.
- A later turn hydrates persisted `ContentRef` back into normal `ContentParts` before building the model request.

## How To Use In Your App

1. Create your normal session backend.
2. Create an artifact service.
3. Wrap the session service with `session/externalization.Wrap`.
4. Pass the wrapped service to `runner.WithSessionService`.
5. Reuse the wrapped service for direct session writes that should be governed.

```go
rawSessionService := sessioninmemory.NewSessionService()
artifactService := artifactinmemory.NewService()

sessionService := externalization.Wrap(
    rawSessionService,
    artifactService,
    externalization.Config{Enabled: true},
)

r := runner.NewRunner(
    appName,
    agent,
    runner.WithSessionService(sessionService),
    runner.WithArtifactService(artifactService),
)
```

## Run

```bash
cd examples/session
go run ./externalization
```

## Expected Output

The exact artifact names are generated at runtime, so the artifact refs below are shown as placeholders:

```text
Step 1: enable session content externalization
Step 2: first user turn stores compact session content
First model request:
- image bytes len: 15
- file data URL present: true
- contains ContentRef: false
Persisted session event:
- image bytes len: 0
- image artifact ref: artifact://sessionpart_<generated>.png@0
- file URL len: 0
- file artifact ref: artifact://sessionpart_<generated>.<ext>@0
Step 3: second user turn hydrates history for model request
Second model request after hydrate:
- image bytes len: 15
- file data URL present: false
- contains ContentRef: false
```

## Notes

- `artifact/inmemory` is used only for the demo. Production code should use an artifact backend that matches the application's persistence and lifecycle needs.
- The feature is disabled by default. Existing applications keep inline session payloads unless they opt in.
- Pass the wrapped session service to `runner.WithSessionService`, and reuse the wrapped service for any direct session writes that should be governed.
- Each `runner.Run` call represents one user turn. This example uses the same session ID for two turns so the second turn reloads session history and hydrates the persisted `ContentRef` before building the model request.
- Standard `ContentParts` image/audio/file inline bytes and data URLs are governed. Ordinary remote URLs and provider file IDs are not re-hosted by this feature.
