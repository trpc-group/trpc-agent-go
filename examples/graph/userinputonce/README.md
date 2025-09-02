# One-shot User Input Example

This example demonstrates a minimal workflow where the user's input is
consumed exactly once by an LLM node and then cleared from state.
It uses the same GraphAgent + Runner architecture and CLI style as the
`examples/graph/basic` example.

## What It Shows

- Building a simple graph with a single LLM node.
- Creating a GraphAgent from a compiled graph.
- Running the agent with Runner and streaming responses.
- One-shot semantics: `user_input` is cleared after the LLM node runs.

## Run

```bash
go run . -model deepseek-chat -input "Hello, world!"
```

Or interactively provide the input once:

```bash
go run . -model deepseek-chat
```

Then type your prompt when asked.

## Files

- `main.go`: CLI app that builds the graph, creates the agent, and runs it
  once with a single user input.

## Notes

- The underlying LLM node updates state so that `user_input` is cleared
  after execution, demonstrating a one-shot consumption pattern.
