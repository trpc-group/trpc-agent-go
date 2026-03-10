# Skill Isolation (Sub-Agent Skills) Example

This example shows a common multi-agent pitfall:

- A coordinator agent calls a sub-agent (via AgentTool).
- The sub-agent calls `skill_load`.
- The coordinator and sub-agent share the same Session by default.

In older versions, `skill_load` wrote unscoped session state keys, so a
sub-agent could accidentally make the coordinator inject the loaded skill
body/docs into its own prompt.

trpc-agent-go now scopes skill state keys by agent name, so a sub-agent’s
`skill_load` does **not** automatically inflate the coordinator’s prompt.
This demo also sets `llmagent.WithEnableCodeExecutionResponseProcessor(false)`
on both agents so fenced code blocks in assistant text do not auto-execute
while `skill_run` remains available.

## Prerequisites

- Go 1.24+
- A valid OpenAI-compatible API key

Environment variables:

| Variable          | Description                              | Default Value               |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

## Run

```bash
cd examples/skillisolation
export OPENAI_API_KEY="your-api-key"
go run . -model gpt-5
```

## What to look for

The program prints two coordinator “before model” checkpoints:

1) The model call that decides to invoke the sub-agent tool
2) The model call after the sub-agent tool returns

On the second checkpoint:

- The Session should contain the child’s loaded key:
  `temp:skill:loaded_by_agent:skillisolation-child/demo-skill`
- The coordinator request should **not** contain a `[Loaded] demo-skill`
  block in the system message.
