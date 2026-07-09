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
      child Agent events still enter Runner event stream / Session Service
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

Python is the engineering choice of the current built-in runtime, not the core
constraint of Dynamic Workflow. Workflow code is not an ordinary script that
breaks away from the framework, and it does not directly call an Agent SDK from
inside the script. It always crosses an explicit bridge/RPC back into the Go
process, where registered Agents and tools continue to run. Using Go as the
workflow language would therefore not provide direct access to in-process host
objects.

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

The first version of Dynamic Workflow is foreground and one-shot. Workflow code
only expresses the orchestration logic; the actual child Agent execution still
happens inside the Go process. In other words, `agent(...)` is not an ordinary
SDK call from a disconnected script. It is a bridge/RPC call back into the
host-side Agent runtime.

Implementation-wise, each child Agent is derived from the parent invocation as
a new invocation: it reuses the parent execution's Session, SessionService,
Plugins, and event-forwarding path, but receives a new InvocationID, input
Message, ParentMetadata, and independent event filter key. Therefore the child
Agent's LLM context and event branch remain isolated instead of being simply
merged into the root Agent's current prompt context.

Therefore, child Agent output events are still sent through the current Runner
event stream:

- Frontends can observe child Agent output and tool-call progress from the same
  event stream.
- The configured Session Service persists those events.
- `parallel` branch events may appear interleaved; this is real-time stream
  semantics and does not change that `parallel(...)` results are returned in
  input order.

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

This section is a future-design note. Most users can skip it.

The current version is one-shot: the model passes inline workflow code to
`run_workflow` and runs it immediately.

A future extension may support file-backed execution, where workflow scripts
can be written, edited, reviewed, and re-run as normal workspace files. This
should be a source-selection extension of `run_workflow`, not a full workflow
management system in this package.

Key constraints:

- Future inputs should choose exactly one of inline `code` or workspace `file`,
  with optional JSON `args`.
- File paths should be workspace-relative, not arbitrary host paths.
- The file resolver should reuse the parent invocation's `codeexecutor`
  workspace.
- The workflow tool should run existing scripts, not provide a script-authoring
  API.
- File-backed workflows persist script source, not execution state; resume,
  checkpoints, publishing, and cross-node storage need separate designs.
- The file-backed version should preserve the current Runtime boundary: it may
  orchestrate registered Agents and explicitly granted host tools, but it
  should not gain unrestricted filesystem or shell access.
