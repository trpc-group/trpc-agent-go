# Dynamic Workflow Example

This example shows the flexible form of Dynamic Workflow: the application
registers one neutral `general_agent` base agent, while workflow code calls it
one or more times with different temporary roles.

```text
workflow_assistant
└── run_workflow
    └── registered base agent: general_agent
        ├── tool: lookup_policy
        ├── dynamic instance: producer / researcher / writer / planner
        └── dynamic instance: reviewer / critic / verifier
```

The registered base agent fixes the model, executor, callbacks, and permission
boundary. Each `agent(...)` call can supply a role-specific `instruction`,
`instance_id`, optional narrowed tools/skills, and `structured_output`. It does
not grant new host capabilities.

This example deliberately keeps the root agent prompt lean. The
`run_workflow` tool declaration teaches the current Python workflow DSL itself (`await agent`,
`parallel`, `pipeline`, `schema`, direct `return`, and the no-wrapper rules),
while the example's root prompt focuses only on when to call
`run_workflow` and how to constrain `lookup_policy`. If you copy this
example into an application, keep that layering: put workflow-language rules in
the tool description, and keep the root prompt focused on task-routing policy
and capability boundaries.

The `general_agent` base agent also has one ordinary child-agent tool,
`lookup_policy`. It is not exposed to the root agent and is not exposed as a
Python `call_tool`; it is available only when a workflow-created child Agent
uses its own tool surface. This lets the example show child Agent tool calls in
the same event stream as the parent run.
Workflow code should narrow this tool explicitly, for example
`tools=["lookup_policy"]`, only for a child role that reviews, approves, or
rejects something against a team collaboration guideline. Ordinary summaries,
generic analysis, operational-risk review, and non-policy pipeline stages
should pass `tools=[]` so the base agent's default tool surface is not inherited.

For `structured_output`, a bare JSON Schema is the concise form, for example
`{"type": "object", "properties": {...}}`. Use the richer
`{"name": ..., "schema": {...}, "strict": ...}` form only when the workflow
needs output metadata.

The shortest calls are `await agent(input, {"instruction": "..."})` and
`await agent(input, instruction="...")`: because this example has one
base agent, `general_agent` is selected automatically.
Omitted `tools` and `skills` inherit the base agent's eligible user tools and
configured skills. Use `[]` only when a role must have none; use a non-empty
list only when a role needs a narrower capability set.

The example deliberately does not configure `dynamicworkflow.WithCodeCallableTools`, so
the workflow code sees only the Agent-oriented DSL. An application that
explicitly configures `dynamicworkflow.WithCodeCallableTools` also exposes direct Python
`call_tool`, but that is a separate tool-orchestration surface.

## Prerequisites

- Go 1.24.4 or later
- Python 3 available as `python3` (the demo uses `dynamicworkflow.LocalRunner`)
- An OpenAI-compatible model endpoint

```bash
export OPENAI_API_KEY="<your-api-key>"
# Optional for a compatible endpoint:
export OPENAI_BASE_URL="https://your-endpoint/v1"
```

## Run

```bash
cd examples
go run ./dynamicworkflow -model gpt-5
```

With no `-prompt`, the example starts an interactive chat loop and keeps one
conversation session open until you type `/new` or `/exit`.

Commands:

- `/new`: start a fresh session
- `/exit`: quit the demo

For a single-turn run, pass `-prompt`:

```bash
go run ./dynamicworkflow -model gpt-5 \
  -prompt 'Build a temporary team: propose a collaboration plan for a remote team, have a reviewer check it against the team collaboration guideline using lookup_policy, and revise the plan with the feedback.'
```

The example prints the model-generated workflow source by default. Disable it
with `-show-workflow-code=false`.

The event printer also shows child Agent tool calls. A typical run includes
lines like:

```text
[general_agent via dynamic_workflow] tool call: lookup_policy (id: call_...) args: {"topic":"remote_collaboration"}
[general_agent via dynamic_workflow] tool result: lookup_policy (id: call_...) {"topic":"remote_collaboration","guidelines":[...]}
```

Those lines come from child Agent events in the same stream as the parent run.
For a child role that must demonstrate tool use, prefer returning text from
that child instead of also requesting `structured_output`; some providers tend
to satisfy a strict structured response directly instead of calling a tool.

## End-to-end walkthrough

For this request:

> Build a temporary two-role workflow to review the production change “Enable
> a new cache for the product catalog.” First analyze risk and rationale, then
> have a reviewer approve or reject it with next steps.

In a real run, the model generated and executed this equivalent, simplified
workflow body:

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
        "required": ["risks", "rationale"],
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
        "required": ["approved", "next_steps"],
    },
)

return {
    "change": "Enable a new cache for the product catalog",
    "analysis": analysis["structured"],
    "decision": review["structured"],
}
```

The two `agent` calls create separate workflow-local instances of the same
`general_agent` base agent. Their model, executor, callbacks, and capability
boundary remain fixed by Go; only their role instruction and response schema
are temporary. The returned value has this shape:

```json
{
  "change": "Enable a new cache for the product catalog",
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

The exact prose and recommendations are model-generated; the stable contract
is the workflow code, its declared schemas, and the returned JSON shape.

## Expected workflow shape

The exact roles and schemas are model-generated. A revision workflow should
look like this:

```python
draft = await agent({"task": task}, {
    "instance_id": "producer",
    "instruction": (
        "Act as the task's producing specialist. Produce a concrete draft. "
        "When feedback is supplied, revise the prior draft against every item."
    ),
    "structured_output": {
        "name": "draft",
        "strict": True,
        "schema": {
            "type": "object",
            "required": ["content"],
            "properties": {"content": {"type": "string"}},
        },
    },
})

for round_index in range(3):
    review = await agent({"draft": draft["structured"]["content"]}, {
        "instance_id": "reviewer",
        "instruction": "Review the draft strictly and return actionable feedback.",
        "structured_output": {
            "name": "review",
            "strict": True,
            "schema": {
                "type": "object",
                "required": ["approved", "feedback"],
                "properties": {
                    "approved": {"type": "boolean"},
                    "feedback": {"type": "string"},
                },
            },
        },
    })

    if review["structured"]["approved"]:
        return {"draft": draft, "review": review}

    draft = await agent({
        "draft": draft["structured"]["content"],
        "feedback": review["structured"]["feedback"],
    }, {
        "instance_id": "producer",
        "instruction": "Revise the draft using every review comment.",
        "structured_output": {
            "name": "draft",
            "strict": True,
            "schema": {
                "type": "object",
                "required": ["content"],
                "properties": {"content": {"type": "string"}},
            },
        },
    })

return {"draft": draft, "review": review}
```

`agent` returns an envelope with `text`, `structured`, session metadata,
and an invocation ID. Prefer `result["structured"]["field"]` for a role's
typed result. For workflow ergonomics, missing `result["field"]` and
`result.get("field")` fall back to `structured`; envelope keys take
precedence. With `strict: True`, object schemas are completed with
`additionalProperties: False` and every declared property is made required for
OpenAI-compatible strict response formats. Model an optional value as nullable
when needed.

## Concurrent and pipeline work

Use `parallel` only when independent roles may run at the same time. It keeps
result order and returns `None` for a failed branch:

```python
perspectives = await parallel([
    lambda: agent({"plan": plan}, {"instruction": "Review security risks."}),
    lambda: agent({"plan": plan}, {"instruction": "Review operational risks."}),
])
```

For a repeated per-item process, use `pipeline`. Each item advances to its next
stage as soon as its own previous stage completes; it does not wait for every
item in the batch. Its returned list contains each item's final-stage result,
so a later stage must explicitly retain earlier data needed by the final
summary:

```python
async def analyze(previous, original, index):
    return await agent({"file": previous}, {"instruction": "Analyze this file."})

async def verify(analysis, original, index):
    return await agent(
        {"file": original, "analysis": analysis["structured"]},
        {"instruction": "Verify the analysis."},
    )

results = await pipeline(files, analyze, verify)
```

## Runtime and session behavior

The workflow is foreground and one-shot. Each child role has an isolated
conversation context. `parallel` branches run concurrently (up to the
framework's per-workflow limit) with distinct child instances by default.
Child Agent output and tool-call progress remain visible through the same event
stream and are persisted by the configured Session Service.

`LocalRunner` starts a local Python process and is not a security sandbox. In
production, provide a `dynamicworkflow.Runtime` backed by a container, microVM,
or remote sandbox, and apply filesystem, network, process, dependency, and
resource policy there.
