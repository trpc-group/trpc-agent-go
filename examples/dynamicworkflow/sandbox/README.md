# Sandbox Dynamic Workflow Example

This example runs model-generated Dynamic Workflow Python in the built-in OS
sandbox:

```text
sandbox_workflow_assistant
└── run_workflow
    ├── Python workflow: one-shot sandbox workspace
    └── Go callback bridge
        └── operations_agent
            └── tool: get_service_health
```

Only the temporary Python glue code runs inside the sandbox. Child Agents,
their tools, Session persistence, and event streaming remain in the Go
framework. A child Agent can therefore call `get_service_health`, and the
frontend still receives its output and tool-call events through the root
Runner's event stream.

This example focuses on the execution boundary. The `basic` and `research`
examples cover broader Dynamic Workflow patterns.

## What it demonstrates

- `dynamicworkflow.NewSandboxRunner()` as the `run_workflow` Runtime.
- A one-shot workspace and clean process environment for every workflow.
- Network-restricted managed sandbox execution.
- A configured workflow timeout that also propagates cancellation to Go
  callbacks.
- Shared Session persistence with workflow-local model context isolated from
  ancestor tool transcripts through
  `llmagent.WithMessageFilterMode(llmagent.IsolatedRequest)`.
- A deterministic demo health tool whose invocation remains in Go rather than
  moving into Python.
- Generated workflow source and nested tool events in the terminal.
- Fail-closed behavior: sandbox setup errors never fall back to `LocalRunner`.

## Prerequisites

- Go 1.24.4 or later.
- Python 3 available to the managed sandbox.
- An OpenAI-compatible model endpoint.
- A supported managed sandbox backend:
  - Linux: install `bubblewrap` (`bwrap`).
  - macOS: `/usr/bin/sandbox-exec` must be available.
  - Windows: the managed backend is not implemented.

```bash
export OPENAI_API_KEY="<your-api-key>"
# Optional for an OpenAI-compatible endpoint:
export OPENAI_BASE_URL="https://your-endpoint/v1"
```

## Run

From the repository's `examples` directory:

```bash
go run ./dynamicworkflow/sandbox -model gpt-5
```

With no `-prompt`, the example starts an interactive session:

- `/new` starts a new conversation session.
- `/exit` exits.

For a single turn:

```bash
go run ./dynamicworkflow/sandbox \
  -model gpt-5 \
  -show-workflow-code \
  -prompt 'Build a temporary team to assess rolling out an in-memory cache for the catalog service. Have a planner propose the rollout, a verifier call get_service_health, and a reviewer allow at most two revisions before making a final decision.'
```

Useful flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `-model` | `gpt-5` | Model on the configured OpenAI-compatible endpoint. |
| `-prompt` | empty | Single-turn request; empty starts interactive mode. |
| `-show-workflow-code` | `false` | Print generated Python before execution. |
| `-workflow-timeout` | `10m` | Full workflow deadline; `0` relies on caller context. |
| `-python` | empty | Explicit interpreter; empty uses sandbox-resolved `python3`; custom paths may need read grants. |

## Expected workflow

For the rollout prompt above, the model should generate a workflow with this
general shape:

```python
plan = await agent(
    {"change": "Add an in-memory cache", "service": "catalog"},
    instruction="Act as a rollout planner and propose a staged plan.",
    tools=[],
)

health = await agent(
    {"service": "catalog", "plan": plan["text"]},
    instruction=(
        "Act as an operational verifier. Call get_service_health for catalog "
        "and assess whether the current state permits this rollout."
    ),
    tools=["get_service_health"],
)

for revision in range(3):
    review = await agent(
        {"plan": plan["text"], "health": health["text"]},
        instruction=(
            "Act as a strict reviewer. Approve the plan or return concrete "
            "changes required before rollout."
        ),
        tools=[],
        structured_output={
            "type": "object",
            "properties": {
                "approved": {"type": "boolean"},
                "feedback": {"type": "string"},
            },
            "required": ["approved", "feedback"],
            "additionalProperties": False,
        },
    )
    if review["structured"]["approved"] or revision == 2:
        break
    plan = await agent(
        {
            "plan": plan["text"],
            "feedback": review["structured"]["feedback"],
        },
        instruction="Revise the rollout plan against every item of review feedback.",
        tools=[],
    )

return {
    "plan": plan["text"],
    "health": health["text"],
    "review": review["structured"],
}
```

The precise code and prose are model-generated. The stable boundary is:

- Python may use the Dynamic Workflow DSL.
- `agent(...)` requests cross the stdio bridge back into Go.
- When selected by the verifier, `get_service_health` is called by the Go child
  Agent.
- Child Agent output and tool events remain visible on the original event
  stream.

The verifier Agent decides when to call `get_service_health`, so its nested tool
events are visible. Tool selection is authorization, not a guarantee that a
model will invoke the tool. If Python must branch on the exact health payload,
also register the business tool with
`dynamicworkflow.WithCodeCallableTools` and obtain the raw value through
`call_tool` before asking an Agent to interpret it.

When `-show-workflow-code` is enabled, the terminal prints the generated source
before the sandbox starts. A successful run should also include nested events
similar to:

```text
[operations_agent via dynamic_workflow] tool call: get_service_health ...
[operations_agent via dynamic_workflow] tool result: get_service_health ...
```

## Python selection

Leaving `-python` empty lets the sandbox resolve `python3` from its clean PATH.
Any non-empty value, including `-python python3`, is first resolved through the
host PATH and converted to an absolute path.

An explicitly selected interpreter outside the backend's default readable
runtime paths needs a corresponding sandbox read grant in application code.
The example uses the default managed permission profile and does not broaden
host filesystem visibility.

## Security boundary

`SandboxRunner` provides stronger local isolation than `LocalRunner`, but it is
not a microVM tenant boundary:

- The managed profile restricts networking by default.
- The guest receives a clean environment and a one-shot workspace.
- Sandbox setup fails closed.
- `-workflow-timeout` sets a deadline for the guest and Go callbacks.
- Go context cancellation is cooperative; Tool and Agent handlers must return
  promptly when their context is done.
- CPU, memory, and process-count quotas remain the responsibility of an outer
  container, microVM, or remote Runtime.

Only give child Agents the tools required for their role. The OS sandbox
controls the Python process; Go-side Tool authorization remains an application
capability boundary.
