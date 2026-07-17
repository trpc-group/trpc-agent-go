# Dynamic Workflow

Dynamic Workflow lets a regular `LLMAgent` temporarily run workflow code for a
complex request and use that workflow to orchestrate child Agents. The built-in
`LocalRunner` currently executes Python workflow code.

Application developers usually do not write this workflow code ahead of time.
Your application only needs to:

1. Prepare one or more base Agents that the workflow may call.
2. Create the `run_workflow` tool.
3. Attach `run_workflow` to the root Agent.

If you only want to get started, read "Minimal setup" and "A complete example"
first. The later sections explain tool calls, concurrency, event streams, and
safety boundaries.

At runtime, the flow looks like this:

```text
user request
  ↓
root Agent
  ├─ simple task: answer directly
  └─ complex task: call run_workflow
        ↓
      model generates temporary workflow code
        ↓
      workflow sends agent(...) calls through the bridge/RPC
        ↓
      registered base Agents run inside the Go process
        ↓
      child Agent events remain in the same event stream / Session Service
        ↓
      workflow returns the combined result to the root Agent
```

Dynamic Workflow is useful when a task needs temporary roles, for example:

```text
analyze a plan → ask a reviewer to check it → revise with feedback → review again
```

Stable, deterministic, strongly constrained business processes should still be
application Go code. For loops, branches, or JSON conversion across ordinary
tools, prefer the lighter `execute_tool_code` capability.

The workflow language is a Runtime choice rather than a constraint of Dynamic
Workflow. The current built-in Runtime uses Python. Calls to registered Agents
and tools cross an explicit bridge/RPC back into the Go host instead of running
through a separate Agent SDK inside the script.

## Minimal setup

The minimal setup registers one neutral base Agent and attaches `run_workflow`
to the root Agent.

Registering one base Agent is common. Many temporary roles only need different
instructions, while the model, tools, and permission boundary can stay the
same. Register multiple base Agents only when those boundaries really need to
differ.

Place this fragment in your application's Agent setup code:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/dynamicworkflow"
)

// The root Agent and workflow-local child Agents may share one model instance.
modelInstance := openai.New("gpt-5")

// Register one base Agent. Workflow code will call it through agent(...).
// This base Agent fixes the model, tools, and permission boundary; each call's
// instruction defines the temporary role.
general := llmagent.New(
    "general_agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("Base agent for workflow-local roles."),
    llmagent.WithInstruction(
        "Follow the dynamic instruction supplied for this workflow-local role.",
    ),
)

// Create the run_workflow tool.
// LocalRunner starts a local Python process and is only for development or
// already-isolated environments.
workflow, err := dynamicworkflow.NewTool(
    dynamicworkflow.LocalRunner{},
    []agent.Agent{general},
)
if err != nil {
    panic(err) // handle the error in production code
}

// Attach run_workflow to the root Agent.
root := llmagent.New(
    "assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithInstruction(
        "Answer simple requests directly. Use run_workflow for tasks that " +
            "need temporary child-agent collaboration.",
    ),
    llmagent.WithTools([]tool.Tool{workflow}),
)
```

This exposes only `run_workflow` to the root Agent. The root Agent's other
tools are not automatically available inside the workflow. This keeps the
workflow boundary explicit and avoids accidental access to writes,
credentials, shell execution, or control-plane tools.

## `agent(...)` in the current Python workflow

Think of `agent(...)` as: run one Go-registered base Agent once.

If `NewTool` registered exactly one base Agent, the workflow can call it
directly:

```python
result = await agent(
    "Review this production change.",
    instruction="You are a strict production reviewer.",
)
return result["text"]
```

If multiple base Agents are registered, the workflow must select one by name:

```python
result = await agent(
    "Review this production change.",
    template="reviewer",
)
```

The `template` field is only the selector for "which base Agent to call". It
is not a separate template system.

One `agent(...)` call can define a temporary role:

```python
review = await agent(
    {"draft": draft},
    instruction="Review the draft and return approval plus feedback.",
    tools=[],
    structured_output={
        "type": "object",
        "properties": {
            "approved": {"type": "boolean"},
            "feedback": {"type": "string"},
        },
    },
)
```

The common options are:

- `instruction`: the temporary role instruction for this child Agent call.
- `tools` / `skills`: omitted means inherit from the base Agent; `[]` disables
  that capability for this call; a non-empty list narrows the base Agent's
  existing capabilities.
- `structured_output` / `schema`: asks this child Agent to return structured
  JSON.
- `instance_id`: reuses the same child Agent history within one workflow.

When `instance_id` is omitted, each `agent(...)` call creates an independent
child Agent history, which is the right default for parallel branches. Passing
the same `instance_id` explicitly means the calls share one child history; if
those calls happen concurrently, they are serialized to avoid concurrent reads
and writes to the same conversation branch.

These options affect only the current child Agent call. A workflow cannot use
them to change the model, permission policy, or add host capabilities that the
base Agent did not already have.

## A complete example

Assume the user asks:

> Review the production change "Enable a new cache for the product catalog":
> first analyze risk and rationale, then make an approval decision.

The root Agent may call `run_workflow`. The model may generate and execute this
workflow code:

```python
analysis = await agent(
    "Analyze the production change: Enable a new cache for the product catalog.",
    instruction="You are a technical analyst reviewing a production change.",
    structured_output={
        "type": "object",
        "properties": {
            "risks": {"type": "array", "items": {"type": "string"}},
            "rationale": {"type": "string"},
        },
    },
)

review = await agent(
    {
        "change": "Enable a new cache for the product catalog",
        "analysis": analysis["structured"],
    },
    instruction="You are a senior engineering reviewer for production changes.",
    structured_output={
        "type": "object",
        "properties": {
            "approved": {"type": "boolean"},
            "next_steps": {"type": "array", "items": {"type": "string"}},
        },
    },
)

return {
    "analysis": analysis["structured"],
    "decision": review["structured"],
}
```

This workflow code is usually generated temporarily by the model; this example
uses Python. It is not business logic that the application pre-writes in Go.

The first `agent(...)` call makes the base Agent temporarily act as a technical
analyst and return structured risk data. The second `agent(...)` call passes
that structured result into the same base Agent acting as a senior reviewer.
The final result may look like this:

```json
{
  "analysis": {
    "risks": [
      "Cache invalidation can expose stale product information.",
      "Concurrent updates can introduce data-consistency issues."
    ],
    "rationale": "Caching reduces database load for a read-heavy catalog."
  },
  "decision": {
    "approved": true,
    "next_steps": [
      "Define cache invalidation and TTL policies.",
      "Add cache metrics and run a phased rollout."
    ]
  }
}
```

If later code needs stable fields, prefer reading `result["structured"]`. The
framework does not infer field names, units, or business meaning from natural
language. If the model service does not support JSON Schema response formats,
this structured call may fail; if stable fields are unnecessary, omit
`structured_output`.

## Concurrency and batch work

Use `parallel` when independent branches can run at the same time. Results are
returned in input order:

```python
reviews = await parallel([
    lambda: agent({"plan": plan}, instruction="Review security risk."),
    lambda: agent({"plan": plan}, instruction="Review operational risk."),
])
```

`parallel` results are ordered like the input list, but the event stream is
real-time. Partial outputs, tool calls, and final events from concurrent child
Agents may be interleaved. Consumers should group events by fields such as
`InvocationID`, `ParentMetadata`, and `FilterKey` instead of relying on the
global event order.

Use `pipeline(items, stage1, stage2, ...)` for repeated multi-stage work over a
batch of items. Each item moves through the stages in order. Once one item's
previous stage finishes, it can enter the next stage without waiting for the
whole batch.

```python
async def analyze(previous, original, index):
    return await agent({"file": original}, instruction="Analyze this file.")

async def verify(analysis, original, index):
    return await agent(
        {"file": original, "analysis": analysis["structured"]},
        instruction="Verify the analysis.",
    )

results = await pipeline(files, analyze, verify)
```

## Calling tools from workflow code: `WithCodeCallableTools` and `call_tool`

The minimal setup does not need `dynamicworkflow.WithCodeCallableTools`. In
that setup, workflow code mainly orchestrates child Agents through
`agent(...)`.

If workflow code really needs to call ordinary business tools directly,
explicitly pass those tools when creating `run_workflow`:

```go
workflow, err := dynamicworkflow.NewTool(
    dynamicworkflow.LocalRunner{},
    []agent.Agent{general},
    dynamicworkflow.WithCodeCallableTools(searchCatalog, createQuote),
)
```

Then workflow code can call:

```python
facts = await call_tool("search_catalog", query="trail backpack")
```

`call_tool` can only call tools explicitly passed through
`WithCodeCallableTools`. It does not automatically see the root Agent's tools.

Do not put execution tools, `run_workflow` itself, `execute_tool_code`,
`transfer_to_agent`, `await_user_reply`, workspace tools, or AgentTools into
`WithCodeCallableTools`. They create recursive or mixed control-flow
boundaries. Workflows should call child Agents through `agent(...)`.

## Events, Session, and execution boundary

Dynamic Workflow is foreground and one-shot. Workflow code expresses the
orchestration logic, while registered Agents and tools continue to run in the
Go host. Each child Agent call has an isolated conversation context and remains
part of the current run. Therefore:

- Frontends can observe child Agent output and tool-call progress from the same
  event stream.
- The configured Session Service persists those events.
- `parallel` branch events may appear interleaved; this is real-time stream
  semantics and does not change that `parallel(...)` results are returned in
  input order.

The event stream follows the framework's normal streaming contract: consume it
until the run finishes, or cancel the run context when stopping early.

This is the key difference from asking the model to write and run an ordinary
standalone script: the temporary workflow gets code-level flexibility, while
Agent execution, tool boundaries, event streaming, and Session persistence
remain controlled by the Go framework.

`dynamicworkflow.LocalRunner` starts a local Python process. It is not a
security sandbox. In production, provide your own `dynamicworkflow.Runtime`,
such as a container, microVM, or remote sandbox, and enforce filesystem,
network, process, dependency, and resource limits there.

Generated workflow code should call host tools rather than direct HTTP APIs.
Authentication, authorization, retries, idempotency, audit, rate limiting, and
API-version policy should remain controlled by business tools on the Go side.

## Choosing the right capability

| Need | Recommended approach |
| --- | --- |
| Stable, known, strongly constrained business process | application Go code |
| Loops, branches, or JSON conversion across ordinary tools | `execute_tool_code` |
| Temporary child-Agent roles, review, parallel analysis, iterative revision | `run_workflow` |

Do not expose both `execute_tool_code` and `run_workflow` to the same root
Agent by default. Both are Python orchestration paths, and exposing both makes
the model's choice harder.

See the runnable
[Dynamic Workflow Agent example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/dynamicworkflow).

## Roadmap note: file-backed workflows

A future source-selection extension may let `run_workflow` choose either inline
code or a workspace-relative script with optional JSON arguments. It should use
the configured workspace abstraction and remain separate from script authoring,
execution-state persistence, resume, checkpoint, and distribution concerns.
