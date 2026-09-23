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

Keep generated workflow code as short orchestration glue. It should express
delegation, data flow, branches, concurrency, and bounded loops while child
Agents perform the substantive research, writing, coding, or tool use. Do not
embed the task's report, source files, or large shell scripts in workflow code.

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
    // Keep each temporary role focused on its own branch. Reusing an
    // instance_id still shares history inside the current workflow request.
    llmagent.WithMessageFilterMode(llmagent.IsolatedRequest),
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

## Use Skills as workflow recipes

An Agent Skill can hold reusable process knowledge while Dynamic Workflow
makes the current request's control flow explicit. For example, one Skill can
describe a bounded draft/review/revise loop and another can describe parallel
analysis followed by synthesis. After loading the matching text, the root
Agent compiles it into one request-specific workflow with real loops, branches,
or parallel stages rather than relying on a long Agent loop to remember every
step.

Keep both capabilities available on the root Agent:

```go
root := llmagent.New(
    "assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithSkills(repo),
    // This example loads process knowledge, not Skill commands or scripts.
    llmagent.WithAllowedSkillTools(llmagent.SkillToolLoad),
    // run_workflow is visible from the first request.
    llmagent.WithTools([]tool.Tool{workflow}),
)
```

The first model request can see the compact Skill summaries, `skill_load`, and
the standard `run_workflow` tool. When a recipe matches, the model normally
loads it first, waits for the `skill_load` result in the next model request,
and then calls `run_workflow` once; it should not issue both calls in one
response. Loading a Skill adds its body to subsequent requests in the current
turn; it does not turn Markdown into an executable script or gate the workflow
tool. If a recipe has optional Markdown or text references, `skill_load` can
request them with `docs` or `include_all_docs`; keep summaries short so
unrelated requests do not pay for large references.

The complete [Dynamic Workflow with Skills example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/dynamicworkflow/skills)
includes a code-shaped bounded loop and a prose-only fan-out recipe. The
workflow remains request-specific: the Skill contributes reusable decisions
and guardrails, while the model adapts roles, inputs, criteria, and output.

This root workflow recipe is distinct from child Agent Skills. A base Agent
registered with Dynamic Workflow may have its own domain Skills; an
`agent(...)` call can inherit them, disable them with `skills=[]`, or narrow
them with `skills=["name"]`. Narrowing selects available capabilities but does
not implicitly load a Skill. If the child template exposes `skill_load` and the
role needs a Skill body, the child can load it; its Agent-scoped Skill state
remains separate from the root.

Child Agent model and tool events continue through the parent Runner event
stream; the linked example also prints these events while the workflow runs.

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
- `model`: optional host-authorized model profile alias when the host registered
  profiles with `WithAgentModelProfile`. Omit it to inherit the template model.
- `tools` / `skills`: omitted means inherit from the base Agent; `[]` disables
  that capability for this call; a non-empty list narrows the base Agent's
  existing capabilities.
- `structured_output` / `schema`: asks this child Agent to return structured
  JSON.
- `instance_id`: reuses the same child Agent history within one workflow.

### Host-authorized model profiles

By default, each child call uses the template Agent's registered model. To let
workflow code choose among a few host-owned models, register profile aliases:

```go
fast := openai.New("gpt-5-mini")
deep := openai.New("gpt-5")

workflow, err := dynamicworkflow.NewTool(
    dynamicworkflow.LocalRunner{},
    []agent.Agent{general},
    dynamicworkflow.WithAgentModelProfile(
        "fast",
        "Low-latency drafting and simple extraction.",
        fast,
    ),
    dynamicworkflow.WithAgentModelProfile(
        "deep",
        "Careful review and multi-step reasoning.",
        deep,
    ),
)
```

A workflow may then select a profile for one child call:

```python
draft = await agent(
    "Write a short draft.",
    instruction="Draft quickly.",
    model="fast",
)
review = await agent(
    {"draft": draft["text"]},
    instruction="Review carefully.",
    model="deep",
)
```

Profiles are an allowlist. The host owns each `model.Model` instance; workflow
code cannot pass a provider model identifier or construct a model. Omitting
`model` preserves the template model exactly. Choose an override only for a
clear task-specific reason. Selected profiles apply to `LLMAgent` templates and
to custom Agents that honor invocation surface patches; other Agents keep their
configured model.

When `instance_id` is omitted, each `agent(...)` call creates an independent
child Agent history, which is the right default for parallel branches. For
`LLMAgent` templates, use `llmagent.IsolatedRequest` as shown above when a
temporary role should not inherit the root branch's conversation. Passing the
same `instance_id` explicitly means the calls share one child history; if those
calls happen concurrently, they are serialized to avoid concurrent reads and
writes to the same conversation branch.

The shared history contains child inputs and emitted events. A dynamic
`instruction` is configuration for that call only and is not persisted as a
conversation message. Put facts that a later call must remember in `input`.

These options affect only the current child Agent call. A workflow cannot use
them to change permission policy, invent model endpoints, or add host
capabilities that the base Agent did not already have. Model selection is
limited to aliases the host registered with `WithAgentModelProfile`.

`agent(...)` returns an envelope containing `text`, optional `structured`
output, and execution metadata. Pass `result["text"]` downstream for plain text
and `result["structured"]` for typed data. Avoid forwarding the complete
envelope unless the next step actually needs its metadata. When a later branch
or loop needs stable fields, request `schema` and use `structured`; do not ask
for JSON text and parse it inside the workflow.

When a role must visibly call a tool and later code needs a typed decision,
split it into two child calls: first collect unstructured, tool-grounded text;
then pass that evidence to a `tools=[]` child with `schema`. This avoids relying
on a model provider to combine tool calling and structured response mode in one
turn.

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
returned in input order; a failed independent branch produces `None`:

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

A stage can accept one, two, or three positional arguments:

- `stage(previous)`
- `stage(previous, original)`
- `stage(previous, original, index)`

For the first stage, `previous` is the original item. This keeps simple first
stages concise while preserving access to the original item and index when a
later stage needs them. Stage signatures are checked before any item starts.
If a stage fails or returns `None`, that item's final result is `None` and its
later stages are skipped.

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

Selecting a tool through `agent(..., tools=[...])` authorizes the child Agent
to use it; it does not guarantee that the model will call it. Structured output
constrains the child's final response shape, not the provenance of its fields.
When Python control flow must consume an exact host-tool result, use
`call_tool` first and pass those facts to an Agent for interpretation.

Do not put execution tools, `run_workflow` itself, `execute_tool_code`,
`transfer_to_agent`, `await_user_reply`, workspace tools, or AgentTools into
`WithCodeCallableTools`. They create recursive or mixed control-flow
boundaries. Workflows should call child Agents through `agent(...)`.

## Events, Session, and execution boundary

Dynamic Workflow is foreground and one-shot. Workflow code expresses the
orchestration logic, while registered Agents and tools continue to run in the
Go host. When `instance_id` is omitted, each child Agent call gets a distinct
conversation branch; calls that explicitly reuse an `instance_id` share that
child history. Every child call remains part of the current run. With
`IsolatedRequest` configured as above, a child sees only its branch; an Agent
configured for broader history may also see ancestor context. Therefore:

- Frontends can observe child Agent output and tool-call progress from the same
  event stream.
- The configured Session Service persists those events.
- `parallel` branch events may appear interleaved; this is real-time stream
  semantics and does not change that `parallel(...)` results are returned in
  input order.

The event stream follows the framework's normal streaming contract: consume it
until the run finishes, or cancel the run context when stopping early.

Workflow execution is not transactional. If a child Agent or code-callable
tool changes external state and a later step fails, that side effect is not
rolled back. Keep mutating operations sequential and make them idempotent when
the root Agent or application may retry the workflow.

This is the key difference from asking the model to write and run an ordinary
standalone script: the temporary workflow gets code-level flexibility, while
Agent execution, tool boundaries, event streaming, and Session persistence
remain controlled by the Go framework.

`dynamicworkflow.LocalRunner` starts a local Python process through the shared
local Python runtime. It is not a security sandbox. It applies
defense-in-depth checks for local use, including
restricted Python syntax, restricted builtins, source-size limits, captured
output limits, a minimal process environment, an empty temporary working
directory by default, a private bootstrap script, best-effort guest process
termination with process-group cleanup on Unix-like systems, and an optional
full-execution timeout configured with
`dynamicworkflow.NewLocalRunner(dynamicworkflow.LocalRunnerConfig{Timeout: ...})`.
The default timeout is intentionally unset; LocalRunner inherits the caller's
context so long Agent workflows are not cut off unexpectedly.

The direct-body form ending in `return` is preferred. For compatibility with a
common model-generated shape, LocalRunner also invokes a workflow consisting
only of an optional docstring and one zero-argument `async def run()` or
`async def main()` definition. Other uncalled helper definitions still fail
validation.

Compared with the earlier LocalRunner behavior, the hardened runner no longer
inherits the host environment, uses an empty temporary working directory by
default, rejects generated source larger than 64 KiB unless configured
otherwise, and enforces the documented restricted Python subset. These are
intentional behavior changes rather than a security sandbox boundary.

For local OS isolation, use the built-in sandbox runner:

```go
workflow, err := dynamicworkflow.NewTool(
    dynamicworkflow.NewSandboxRunner(),
    childAgents,
)
```

With the no-option constructor shown above, every workflow gets a one-shot
workspace and clean process environment. The runner uses
`codeexecutor/sandbox`, restricts networking by default, and fails closed
instead of falling back to local execution. Linux requires `bubblewrap`; macOS
uses `/usr/bin/sandbox-exec`; Windows has no managed backend.

`SandboxRunner.Timeout` sets a deadline for the complete workflow and
propagates cancellation to the guest and host Tool or child-Agent callbacks. Go
context cancellation is cooperative: call handlers must return promptly when
their context is done. The zero value adds no deadline and relies on the
caller's context. A production caller must provide a context deadline or
configure this timeout; if both are present, the earlier deadline wins. CPU,
memory, and process-count quotas remain the responsibility of the surrounding
container, microVM, or remote runtime.

When `SandboxRunner.Python` is empty, the sandbox resolves `python3` from its
clean PATH. Any non-empty value, including an explicit `"python3"`, is resolved
through the host PATH and converted to an absolute path. An interpreter outside
the backend's default runtime paths may also need a managed permission profile
extended with `sandbox.WorkspaceWriteProfile().WithReadPaths(...)` and passed
through `sandbox.WithPermissionProfile(...)`.

The OS sandbox remains host-local and grants the platform/runtime paths needed
to launch Python; exact read visibility differs by backend. It is not a tenant
boundary equivalent to a microVM. Use a container, microVM, or remote `Runtime`
when the guest must have no host filesystem visibility or needs stronger
resource and tenant isolation.

See the runnable
[Sandbox Dynamic Workflow example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/dynamicworkflow/sandbox)
for an Agent whose generated workflow runs in the managed sandbox while child
Agent tools and events remain in Go.

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
[basic Dynamic Workflow Agent example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/dynamicworkflow/basic).

## Roadmap note: file-backed workflows

A future source-selection extension may let `run_workflow` choose either inline
code or a workspace-relative script with optional JSON arguments. It should use
the configured workspace abstraction and remain separate from script authoring,
execution-state persistence, resume, checkpoint, and distribution concerns.
