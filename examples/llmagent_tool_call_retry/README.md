# LLMAgent Tool-Call Retry Example

This example demonstrates framework-level single tool-call retry on an
`LLMAgent`.

It uses a real `function.NewFunctionTool(...)` implementation that
intentionally fails with `io.ErrUnexpectedEOF` for the first few
attempts, then succeeds. The program runs the same agent twice:

- once without `llmagent.WithToolCallRetryPolicy(...)`
- once with a `tool.RetryPolicy`

That makes it easy to compare how `LLMAgent` behaves before and after
tool-call retry is enabled.

To keep the example focused on retry itself, the program stops the run
immediately after it receives the first tool result. It does not wait for
the model to produce a final natural-language answer.

## What the example shows

- how an `LLMAgent` emits a tool call for `get_weather`
- how single tool-call retry works on the `LLMAgent` execution path
- retry is scoped to the tool invocation instead of the whole agent run
- default behavior is unchanged when no retry policy is configured
- the first tool result is enough to observe the retry effect

## Run

Set model credentials first, for example:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="your-base-url"
```

Then run:

```bash
cd examples
go run ./llmagent_tool_call_retry -model deepseek-chat
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
go run ./llmagent_tool_call_retry -model deepseek-chat -location Shanghai -fail 2 -backoff 300ms
```

## Expected output

You should see something like:

```text
== without_retry ==
tool attempt 1 for Shenzhen
llm tool call: get_weather args={"location":"Shenzhen"}
result: failed after 1 attempt(s): Error: callable tool execution failed: unexpected EOF

== with_retry ==
tool attempt 1 for Shenzhen
llm tool call: get_weather args={"location":"Shenzhen"}
tool attempt 2 for Shenzhen
result: succeeded after 2 attempt(s)
tool response: {"attempt":2,"forecast":"sunny","location":"Shenzhen"}
```

Real runs may also print framework log lines such as retry-related warnings
or session wait notices. Those logs are incidental and are omitted above.

## Code highlights

- `llmagent.WithToolCallRetryPolicy(...)`
- `tool.RetryPolicy` with `MaxAttempts`, `InitialInterval`,
  `BackoffFactor`, and `MaxInterval`
- a regular function tool created with `function.NewFunctionTool(...)`

## Next step

This example focuses on retrying raw tool errors. If you also need to
retry result-level failures such as MCP `isError=true`, provide a custom
`RetryOn` function in `tool.RetryPolicy`.
