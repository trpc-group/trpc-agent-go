# Team Example: Coordinator Team

This example demonstrates a coordinator Team built with the `team` package.

In a coordinator Team, one coordinator Agent consults member Agents and then
produces the final answer.

## When to use this mode

Use a coordinator Team when you want one Agent to combine multiple specialist
outputs into a single final response.

This mode works best for “deliverables” where you want one coherent result,
for example:

- a technical plan
- a decision summary
- a checklist of risks and next steps

## Member roles in this example

This example uses three member roles:

- `requirements_analyst`: clarifies goals, constraints, and acceptance criteria
- `solution_designer`: proposes design options and recommends a plan
- `quality_reviewer`: finds risks, edge cases, and missing details

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

| Argument                     | Description                                   | Default Value   |
| --------------------------- | --------------------------------------------- | --------------- |
| `-model`                    | Name of the model to use                      | `deepseek-chat` |
| `-variant`                  | Variant passed to the OpenAI provider         | `openai`        |
| `-streaming`                | Enable streaming output                        | `true`          |
| `-timeout`                  | Request timeout                               | `5m`            |
| `-show-inner`               | Show member transcript                        | `true`          |
| `-member-history`           | Member history scope: `parent` or `isolated`  | `parent`        |
| `-member-skip-summarization` | Skip coordinator synthesis after a member tool | `false`      |
| `-parallel-tools`           | Enable parallel tool execution                | `false`         |

## Usage

```bash
cd examples/team/coord
export OPENAI_API_KEY="your-api-key"
go run .
```

Try a prompt like:

> Design a simple “export data” feature for a web app: define requirements,
> propose an API and a backend design, then list risks and next steps.

Notes:

- If the output shows a line like `[tools] ...`, the coordinator is invoking a
  member.
- By default, member output is shown. Pass `-show-inner=false` to hide it (you
  will only see `[tool.done] ...`).
- Use `-member-history=parent` (default) when members should see the
  coordinator's conversation history. Use `-member-history=isolated` when you
  want members to only see the tool input and their own outputs.
- `-member-skip-summarization=true` is an advanced mode: the coordinator will
  end the run right after the member returns, so the user sees the member
  output directly (no coordinator synthesis).
- `-parallel-tools=true` runs multiple member invocations concurrently only
  when the model emits multiple tool calls in one response.

For a Swarm Team example, see `examples/team/swarm`.
