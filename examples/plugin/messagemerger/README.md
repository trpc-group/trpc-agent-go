# Message Merger Plugin Example

This example shows the standard integration pattern for `plugin/messagemerger` with a real OpenAI-compatible backend. No mock model, stub backend, or A/B comparison flow is used.

## What this example demonstrates

- Create a Runner with `messagemerger.New(...)`
- Pass caller-supplied `[]model.Message` history with adjacent same-role messages
- Run the request against a real model backend through `runner.RunWithMessages(...)`

## Prerequisites

- Go 1.21 or later
- A reachable OpenAI-compatible chat endpoint

## Environment Variables

| Variable | Description | Default Value |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the model service. | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint. | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument | Description | Default Value |
| --- | --- | --- |
| `-model` | Name of the chat model to use. | `gpt-4o-mini` |

## Usage

```bash
cd examples/plugin/messagemerger
export OPENAI_API_KEY="your-api-key"
go run .
```

A different model can be selected:

```bash
go run . -model deepseek-v4-flash
```

## Core integration

The example wires the plugin at Runner construction time:

```go
runnerInstance := runner.NewRunner(
	"message-merger-demo",
	agentInstance,
	runner.WithPlugins(messagemerger.New()),
)
```

The example then calls `runner.RunWithMessages(...)` with a history that contains consecutive `user` and `assistant` messages.

## Files

- `main.go`: Program entry and example request flow.
- `agent.go`: Model, agent, and Runner setup with `messagemerger`.
- `print.go`: Final response collection and simple message printing helpers.
