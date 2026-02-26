# Team Example: Swarm

This example demonstrates a Swarm Team built with the `team` package.

In a Swarm Team, Agents can hand off control to each other using the
`transfer_to_agent` tool. The last Agent reply is the final answer.

## When to use this mode

Use a Swarm Team when you want peer-to-peer handoffs, where each Agent decides
who should act next.

This mode works well for “group discussion” tasks, for example:

- brainstorming options
- debating tradeoffs
- converging on a decision and next steps

## Member roles in this example

This example uses three discussion roles plus one summary role:

- `optimist`: proposes options and highlights upsides
- `skeptic`: challenges assumptions and raises risks
- `pragmatist`: focuses on constraints and execution details
- `summarizer`: produces the final recommendation and next steps

## Prerequisites

- Go 1.21 or later
- A valid OpenAI-compatible Application Programming Interface (API) key

## Terminology

- Application Programming Interface (API): the interface your code calls to
  talk to a model service.
- Uniform Resource Locator (URL): a web address, like `https://...`.

## Environment Variables

| Variable          | Description                              | Default Value               |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument | Description | Default Value |
| --- | --- | --- |
| `-model` | Name of the model to use | `deepseek-chat` |
| `-variant` | Variant passed to the OpenAI provider | `openai` |
| `-streaming` | Enable streaming output | `true` |
| `-timeout` | Request timeout | `5m` |
| `-cross-request-transfer` | Enable cross-request transfer (see below) | `false` |

## Usage

```bash
cd examples/team/swarm
export OPENAI_API_KEY="your-api-key"
go run .
```

## Cross-request transfer (optional)

By default, each new user message starts from the entry Agent (`optimist` in
this example), even if the previous message ended after a handoff.

If you want the "current owner" of the conversation to persist across user
messages, enable cross-request transfer:

- In code: pass `team.WithCrossRequestTransfer(true)` to `team.NewSwarm(...)`.
- In this example:

```bash
go run . -cross-request-transfer=true
```

With this enabled, after a handoff, the next user message will start from the
Agent that produced the last reply (until another `transfer_to_agent` happens).

Try a prompt like:

> We need to choose between REST and gRPC for an internal service. Discuss the
> tradeoffs, then give a recommendation with next steps.

Notes:

- You do not need to tell the system which Agent to transfer to. Agents can
  decide the next handoff based on the task.
- The console output includes an Agent name prefix (for example, `[optimist]`)
  so you can see who is responding.

For a coordinator Team example, see `examples/team/coord`.
