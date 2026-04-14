# Tool Call ID Plugin Example

This example shows how to attach the official `toolcallid` plugin to one real `llmagent` with a calculator tool and observe the same tool call ID in tool execution and runner events without any mocks.

## What this example demonstrates

- Install the plugin once with `runner.WithPlugins(toolcallid.New())`
- Use one real model-backed `llmagent`
- Use one real local calculator function tool
- Observe the same tool call ID in two places:
  - tool execution context via `tool.ToolCallIDFromContext`
  - runner event stream tool call and tool result messages

## Prerequisites

- Go 1.21 or later
- A valid OpenAI-compatible API key

## Environment Variables

| Variable | Description | Default Value |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the model service. | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint. | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument | Description | Default Value |
| --- | --- | --- |
| `-model` | Name of the model to use. | `gpt-5.4` |
| `-variant` | OpenAI provider variant. | `openai` |
| `-streaming` | Enable streaming responses. | `false` |
| `-prompt` | The user prompt. | Built-in prompt that forces one calculator tool call |

## Usage

```bash
cd examples/toolcallid
export OPENAI_API_KEY="your-api-key"
go run .
```

## What to look for

The run should print lines similar to the following:

```text
[event] tool_call tool=calculator call_id=... args=...
[tool] tool=calculator call_id=... operation=multiply a=17 b=23
[event] tool_result call_id=... result=...
```

The key point is that the same tool call ID appears consistently across all stages.

## Notes

- The plugin rewrites the final tool call ID into a framework-managed ID.
- The example uses one agent, one tool, and one `runner.Run(...)`, so the data flow stays easy to inspect.
