# Graph Tool-Call Retry Example

This example demonstrates framework-level single tool-call retry on a
graph workflow that includes an `LLMNode` and a `ToolsNode`.

The graph path is:

- `assistant` (`LLMNode`)
- `tools` (`ToolsNode`)
- back to `assistant`

The tool is a real `function.NewFunctionTool(...)` implementation that
intentionally fails with `io.ErrUnexpectedEOF` for the first few
attempts, then succeeds. The program runs the same graph twice:

- once without `graph.WithToolCallRetryPolicy(...)`
- once with a `tool.RetryPolicy`

That makes it easy to see what changes when tool-call retry is enabled.

## What the example shows

- how an `LLMNode` emits a tool call for `get_weather`
- how a `ToolsNode` executes that call and retries only the tool
  invocation
- retry is scoped to a single tool invocation, not the whole node
- the default retry classifier retries common transient I/O errors
- the graph tools node keeps its original behavior when no retry policy is configured

## Run

Set model credentials first, for example:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="your-base-url"
```

```bash
cd examples
go run ./graph/tool_call_retry -model deepseek-chat
```

## Flags

- `-model string` model name to use (default: `deepseek-chat`)
- `-base-url string` OpenAI-compatible base URL
- `-api-key string` API key for the model service
- `-location string` location passed to the tool (default: `Shenzhen`)
- `-fail int` number of transient failures before the tool succeeds (default: `1`)
- `-backoff duration` initial retry backoff (default: `200ms`)

Example:

```bash
go run ./graph/tool_call_retry -model deepseek-chat -location Shanghai -fail 2 -backoff 300ms
```

## Expected output

You should see something like:

```text
== without_retry ==
llm tool call: get_weather args={"location":"Shenzhen"}
tool attempt 1 for Shenzhen
result: failed after 1 attempt(s): ...

== with_retry ==
llm tool call: get_weather args={"location":"Shenzhen"}
tool attempt 1 for Shenzhen
tool attempt 2 for Shenzhen
result: succeeded after 2 attempt(s)
answer: The weather in Shenzhen is sunny.
```

## Code highlights

- `AddLLMNode(...)` plus `AddToolsConditionalEdges(...)`
- `graph.WithToolCallRetryPolicy(...)` on `AddToolsNode(...)`
- `tool.RetryPolicy` with `MaxAttempts`, `InitialInterval`,
  `BackoffFactor`, and `MaxInterval`
- a regular function tool created with `function.NewFunctionTool(...)`

## Next step

This example focuses on retrying raw tool errors. If you also need to
retry result-level failures such as MCP `isError=true`, provide a custom
`RetryOn` function in `tool.RetryPolicy`.
