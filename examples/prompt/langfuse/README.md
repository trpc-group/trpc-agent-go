# Langfuse Prompt + LLMAgent Example

This example shows how to use `prompt/provider/langfuse` as a dynamic prompt source while keeping `llmagent` on its existing string-based API.

The flow is:

1. Read Langfuse config from the current shell environment.
2. Create a cached `prompt.Source` via `prompt/provider/langfuse`.
3. Before each run, fetch the latest prompt from Langfuse.
4. Render prompt variables locally with `prompt.Text.Render(...)`.
5. Apply the rendered text to `llmagent` via `SetInstruction(...)`.
6. Run the agent through `runner.Run(...)`.

This matches the current framework direction: Langfuse prompt fetching stays outside `llmagent`, and users wire it in through existing APIs.

## Expected Langfuse Prompt

The example defaults match the sample prompt metadata in `examples/prompt/langfuse/prompt.txt`:

```text
name: movie-critic
label: production
text prompt: As a {{criticlevel}} movie critic, do you like {{movie}}?
```

The example renders these variables at runtime:

- `criticlevel`
- `movie`

## Environment

Set the Langfuse credentials in your current shell before running the example:

```bash
export LANGFUSE_SECRET_KEY="..."
export LANGFUSE_PUBLIC_KEY="..."
export LANGFUSE_HOST="cloud.langfuse.com"
```

It also works with:

```bash
export LANGFUSE_BASE_URL="https://cloud.langfuse.com"
```

For the LLM model itself, you still need the normal model credentials, for example:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.deepseek.com/v1" # optional
```

## Running

From the `examples` module:

```bash
cd examples
go run ./prompt/langfuse
```

Or from the repository root:

```bash
go run ./examples/prompt/langfuse
```

## Useful Flags

- `-model`: model name to use. Default: `deepseek-v4-flash`
- `-stream`: enable streaming output. Default: `true`
- `-prompt-name`: Langfuse prompt name. Default: `movie-critic`
- `-prompt-label`: Langfuse prompt label. Default: `production`
- `-movie`: run once with the given movie title, then exit
- `-critic-level`: value for `{{criticlevel}}`. Default: `expert`
- `-cache-ttl`: source cache TTL. Default: `30s`
- `-follow-up`: user message sent after the instruction is refreshed

Example:

```bash
go run ./examples/prompt/langfuse \
  -model gpt-4o-mini \
  -movie "Dune 2" \
  -critic-level casual \
  -cache-ttl 5s
```

## What Makes It Dynamic

Each time you enter a movie title, the example does this:

1. `source.FetchPrompt(ctx)` gets the latest Langfuse prompt, subject to the cache TTL.
2. `text.Render(...)` fills `{{criticlevel}}` and `{{movie}}`.
3. `llmAgent.SetInstruction(rendered)` updates the agent instruction for that run.
4. The example prints both the fetched raw prompt and the rendered instruction before running the agent.

If you change the prompt in Langfuse and wait for the cache TTL to expire, the next movie title will use the updated prompt without recreating the agent.
