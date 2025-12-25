# Tool Interrupt (External Tool Execution) Example

This example demonstrates how to **interrupt tool execution** in an agent run,
execute the tool externally (for example, in a client or an upstream service),
and then feed the tool result back to the agent so the **Large Language Model
(LLM)** can continue.

## Why this example exists

By default, when an LLM returns `tool_calls`, the framework executes the tools
automatically and then sends the tool results back to the LLM.

In some systems, you want a different control flow:

- The agent should **only proxy** tool calls (do not auto-execute).
- The caller decides how to execute the tool (for example, an external tool,
  a Model Context Protocol (MCP) tool, or a business-specific tool runtime).
- The caller sends a `role=tool` message back to the agent so the LLM can use
  the tool result and produce the final answer.

This matches the common protocol order:

`system → user → assistant(tool_calls) → tool(tool_result) → assistant(answer)`

## Key API

- `agent.WithToolExecutionFilter(...)`
  - Controls **which tool calls are auto-executed** by the framework.
  - Different from `agent.WithToolFilter(...)` which controls **which tools are
    visible/callable** by the model.

## Prerequisites

- Go 1.23 or later (this `examples/` module uses its own `go.mod`)
- Valid OpenAI-compatible API key (for example, `OPENAI_API_KEY`)

## Usage

```bash
cd examples/toolinterrupt
export OPENAI_API_KEY="your-api-key-here"
go run . -model deepseek-chat
```

Try asking anything. The agent is instructed to always call the external tool
first, then answer based on the tool result.

## What you will see

1. The model emits a tool call (but it is **not executed** by the agent).
2. The demo "client" executes the tool externally.
3. The demo sends a `role=tool` message back.
4. The model continues and produces the final answer using the tool output.

## See also

- `examples/humaninloop/` demonstrates a Human-in-the-Loop (HIL) pattern using a
  long-running tool.
- This example focuses on **manual tool execution** (interrupt + resume).

