# WeKnora streaming metadata

Each streaming run uses a generated `model.Response.ID` shared by its text
chunks and final completion. The upstream WeKnora ID never replaces this ID.

When WeKnora supplies a non-empty `AgentStreamResponse.ID`, subsequent emitted
text events and the final completion or stream error include a snapshot in
`Event.Extensions`:

```json
{"weknora":{"version":1,"response_id":"upstream-id"}}
```

`response_id` is the most recently observed non-empty upstream ID at the time
of emission. Empty IDs do not clear it. IDs on chunks without text are also
recorded. If upstream IDs change, later events reflect the new ID while earlier
events retain their original metadata. The extension is omitted until an ID
is available, and its state is scoped to one run.

Event consumers can read `evt.Extensions["weknora"]` and log the upstream
`response_id` alongside `evt.Response.ID` on text events to correlate downstream
messages with WeKnora logs. This is framework event metadata; it does not add
metadata to the AG-UI wire protocol. No upstream ID is invented when WeKnora
omits it. Error events retain their existing response identity behavior.
