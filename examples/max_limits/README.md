# Max Limits Example

This example demonstrates how to use **per-agent limits** for LLM calls and
tool iterations in `LLMAgent`, including opt-in graceful finalization. It
intentionally configures very small limits so you can see how the agent
produces a tool-free final response when a budget is exhausted.

## What it does

- Creates an `LLMAgent` with:
  - A simple `calculator` tool (`add` / `multiply`)
  - `WithMaxLLMCalls(5)`
  - `WithMaxToolIterations(2)`
  - Default finalization instructions for both limits
- Sends a fixed user message asking the agent to compute an exponent
  step‑by‑step, **always using the calculator tool**, and never doing math in
  its head.
- Streams all events and prints:
  - Tool calls
  - Tool responses
  - Assistant deltas / messages
  - A final assistant response once the tool-iteration limit is reached.

You do **not** need to type any input during the run; the example constructs a
single fixed user message in code.

## How to run

From the repo root:

```bash
cd examples/max_limits

# Configure your model endpoint; for example:
export OPENAI_BASE_URL="http://v2.open.venus.oa.com/llmproxy/"
export OPENAI_API_KEY="YOUR_API_KEY"

go run .
```

The example currently uses:

```go
modelInstance := openai.New(
    "deepseek-v4-flash",
    openai.WithVariant(openai.VariantOpenAI),
)
```

If your environment uses a different model name or provider, adjust this
configuration accordingly.

## Input examples

By default, `main.go` sends a fixed user message that asks the agent to:

- Automatically complete the computation of 2^8 in a single conversational run.
- Call the `calculator` tool at most once per assistant turn.
- Never do any arithmetic mentally; every multiplication must go through the tool.
- Provide a final summary and the numeric result at the end.

The tool-iteration budget is intentionally too small to complete all eight
multiplications. After the second tool result, the framework removes the tools
from the next model request and asks the model to give the best possible final
response from the available context.

You can experiment by editing `main.go` and changing:

```go
message := model.NewUserMessage("...")
```

For example:

- Compute 3^10 while still requiring every step to use the `calculator` tool.
- Ask the model to call `calculator` multiple times in one conversation but only give the final result in the last reply.
- Have the model print the `current` value at each step and compare different base/exponent combinations at the end.

After each change, re-run:

```bash
go run .
```
