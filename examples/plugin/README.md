# Runner Plugins Example

This example shows how to use **Runner plugins** to apply cross-cutting
behavior globally (once per Runner), without repeating configuration on every
Agent.

## What are Runner plugins?

A plugin is a reusable module that hooks into the lifecycle of:

- Agent execution
- Model calls (Large Language Model (LLM))
- Tool calls
- Runner events

Typical use cases:

- Logging / tracing
- Global policy enforcement
- Request shaping (for example, add a system instruction everywhere)
- Auditing and event tagging

## What this example demonstrates

- Register plugins once via `runner.WithPlugins(...)`
- Use built-in plugins:
  - `plugin.NewLogging()`
  - `plugin.NewGlobalInstruction(...)`
- Write a custom plugin that:
  - Short-circuits model calls when the user input contains a keyword
  - Adds a tag and a visible prefix to assistant responses

## Prerequisites

- Go 1.21 or later
- A valid OpenAI-compatible API key (Application Programming Interface (API))

## Environment Variables

| Variable          | Description                              | Default Value               |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument      | Description                     | Default Value   |
| ------------- | ------------------------------- | --------------- |
| `-model`      | Name of the model to use        | `deepseek-chat` |
| `-variant`    | Provider variant for OpenAI     | `openai`        |
| `-streaming`  | Enable streaming responses       | `false`         |
| `-debug`      | Print extra plugin/tool details | `false`         |

## Usage

```bash
cd examples/plugin
export OPENAI_API_KEY="your-api-key"
go run .
```

### Try the plugin behavior

1) **Short-circuit the model call**

Send a message that contains `/deny`. The custom plugin intercepts the
BeforeModel hook and returns a custom response, so the real model is not
called.

2) **Trigger a tool call**

Ask a math question like:

> Use the calculator tool to compute 12 * 34.

If the model chooses to call the tool, you will see tool call + tool result
events, and the plugin will also see those lifecycle hooks.

## Files

- `main.go`: interactive chat loop + Runner setup
- `plugins.go`: custom plugin implementation
- `tools.go`: calculator tool implementation

