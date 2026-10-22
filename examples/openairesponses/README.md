# OpenAI Responses API examples

This nested Go module shows how to use the OpenAI Responses adapter with the
tRPC-Agent-Go framework. It contains a basic chat example and a Runner example
that executes local function tools over multiple model turns.

The adapter sends the full client-managed conversation history on each model
request. Its default is `store=false`; each example also sets this explicitly.
Set `OPENAI_API_KEY` before running an example. `OPENAI_BASE_URL` and
`OPENAI_MODEL` are optional; the defaults are the official OpenAI API URL and
`gpt-5`.

```bash
cd examples/openairesponses
export OPENAI_API_KEY="your-api-key"

go run ./chat
go run ./tools
```

Both programs also accept `-model`, `-api-key`, `-base-url`, and
`-streaming=false` flags.

The chat program accepts repeated messages until `/exit`. The tools program
registers a local calculator function and keeps the same Runner session across
user messages. Try asking it to calculate `123 * 456`, then add `789` to the
result; the model can call the local function more than once before answering.

The supported scope is client-managed full history and local function tools.
This module does not demonstrate or claim support for server-managed
`previous_response_id` or `conversation` continuation, background responses,
hosted tools, tool search, or approval flows. Streaming responses that end
without a valid terminal event, including incomplete responses, are errors;
their partial function calls must not be executed.

The adapter associates native Responses state with a read-only projection of
the framework message that produced it, including function-call arguments.
Treat that association as one history record. Projection edits fail validation;
native tool turns must not be split. A complete older history prefix can be
replaced by a session summary, while tool-result compaction is skipped for
native histories. See [the adapter contract](../../model/openai/responses)
for history, model-switching, and unsupported-feature boundaries.

This is an independent Go module so it can use the Go version required by the
Responses adapter without raising the root module's Go version.
