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
| `-model`                    | Name of the model to use                      | `deepseek-v4-flash` |
| `-variant`                  | Variant passed to the OpenAI provider         | `openai`        |
| `-streaming`                | Enable streaming output                        | `true`          |
| `-timeout`                  | Request timeout                               | `5m`            |
| `-show-inner`               | Show member transcript                        | `true`          |
| `-member-inner-text`        | Member inner text mode: `include` or `exclude` | `include`      |
| `-member-history`           | Member history scope: `parent` or `isolated`  | `parent`        |
| `-member-skip-summarization` | Skip coordinator synthesis after a member tool | `false`      |
| `-parallel-tools`           | Enable parallel tool execution                | `false`         |

## Usage

```bash
cd examples/team/coord
export OPENAI_API_KEY="your-api-key"
go run .
```

The example starts in the most verbose mode:

- member internals are forwarded (`-show-inner=true`)
- member assistant text is visible (`-member-inner-text=include`)
- the coordinator still writes the final answer

That makes it easy to see what each member is doing, but it can also make the
terminal noisy.

## How to Control What Users See

This example is designed to teach the difference between `StreamInner` and
`InnerTextMode`.

### 1. Hide All Member Internals

Use this when you only want the coordinator's final answer:

```bash
go run . -show-inner=false
```

What you will see:

- no member transcript
- no member tool arguments
- only the coordinator's final answer

### 2. Show the Full Member Transcript

Use this when you are debugging prompts, evaluating routing quality, or want to
watch each specialist think out loud:

```bash
go run . -show-inner=true -member-inner-text=include
```

What you will see:

- `[tools]` and `[tool.args]` lines when the coordinator calls a member
- streamed member text such as `[requirements_analyst] ...`
- `[tool.done]` when a member call finishes
- the coordinator's final synthesized answer

This is the most transparent mode, but it can look repetitive because both the
member and the coordinator may describe similar conclusions.

### 3. Show Progress but Keep One Final Answer

Use this when you want a cleaner product experience:

```bash
go run . -show-inner=true -member-inner-text=exclude
```

What you will see:

- `[tools]` and `[tool.args]` when the coordinator dispatches work
- `[tool.done]` when each member finishes
- no streamed member prose
- one final answer from the coordinator

This is the recommended setup when you want users to see progress without
showing duplicate member text.

## How the Flags Map to the API

The example wires the command-line flags to `team.MemberToolConfig`:

```go
memberCfg := team.DefaultMemberToolConfig()
memberCfg.StreamInner = showInner
memberCfg.InnerTextMode = team.InnerTextModeExclude
memberCfg.HistoryScope = team.HistoryScopeParentBranch
memberCfg.SkipSummarization = false
```

Conceptually:

- `-show-inner=false` means `StreamInner=false`
- `-show-inner=true -member-inner-text=include` means forward the full member
  transcript
- `-show-inner=true -member-inner-text=exclude` means forward member progress,
  but suppress member assistant prose

Try a prompt like:

> Design a simple “export data” feature for a web app: define requirements,
> propose an API and a backend design, then list risks and next steps.

Notes:

- If the output shows a line like `[tools] ...`, the coordinator is invoking a
  member.
- By default, member output is shown. Pass `-show-inner=false` to hide all
  member internals.
- Pass `-member-inner-text=exclude` when you want users to see member progress
  without seeing duplicate member prose.
- Use `-member-history=parent` (default) when members should see the
  coordinator's conversation history. Use `-member-history=isolated` when you
  want members to only see the tool input and their own outputs.
- `-member-skip-summarization=true` is an advanced mode: the coordinator will
  end the run right after the member returns, so the user sees the member
  output directly (no coordinator synthesis).
- `-parallel-tools=true` runs multiple member invocations concurrently only
  when the model emits multiple tool calls in one response.

For a Swarm Team example, see `examples/team/swarm`.
