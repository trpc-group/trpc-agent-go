# Dynamic Workflow Research Example

This example uses Dynamic Workflow for open-ended research rather than one
fixed business process. The root `research_assistant` sees only
`run_workflow`. Workflow code creates temporary roles from one neutral
`research_agent` template and explicitly selects the tools available to each
role.

```text
research_assistant
└── run_workflow
    └── research_agent template
        ├── web researcher: duckduckgo_search
        ├── local experimenter: hostexec_exec_command
        ├── long command controller: hostexec_write_stdin / hostexec_kill_session
        └── reviewer or synthesizer: no tools
```

The template fixes the model, Runner, callbacks, and maximum capability set.
Each `agent(...)` call supplies a temporary instruction and an explicit
`tools=[...]` list. A role cannot select tools that are not registered on the
template.

## What It Demonstrates

- parallel web research with independent temporary Agent instances;
- passing research evidence into later reviewers and synthesizers;
- running a bounded local command to verify a claim;
- iterating through implementation, testing, and review until accepted or a
  fixed attempt limit is reached;
- keeping search, execution, review, and synthesis permissions separate;
- streaming child Agent tool calls through the parent Runner event stream.

Web search uses the existing DuckDuckGo HTML search tool. Host execution uses
the existing `hostexec` ToolSet and starts commands in the directory selected
by `-base-dir`, but it still executes real commands on the local machine and is
not path-isolated.

## Run

From the `examples` module:

```bash
export OPENAI_API_KEY="your-api-key"
# Optional for a compatible endpoint:
export OPENAI_BASE_URL="https://your-endpoint/v1"

go run ./dynamicworkflow/research -model gpt-5 -base-dir ..
```

With no `-prompt`, the example starts an interactive session. `/new` creates a
new session and `/exit` quits. Pass `-show-workflow-code=true` to print the
generated Python separately before execution.

Example single-turn requests:

```bash
go run ./dynamicworkflow/research -model gpt-5 -base-dir .. \
  -prompt 'Research two current Go HTTP routers in parallel, compare their documented tradeoffs, and have a reviewer identify unsupported claims.'

go run ./dynamicworkflow/research -model gpt-5 -base-dir .. \
  -prompt 'Inspect this repository for its Go version, research the relevant Go release notes, ask a local experimenter to verify one claim, and synthesize the evidence.'

workspace=$(mktemp -d)
go run ./dynamicworkflow/research -model gpt-5 -base-dir "$workspace" \
  -prompt 'Build a temporary software team to implement a small local in-memory cache in Go. Have an architect define the design, then use a bounded loop of at most three iterations: implement or revise the cache, run focused tests with the race detector, and have an independent reviewer return structured approval and issues. Exit early when approved; otherwise pass the issues into the next iteration. Finish with the iteration count, files, test evidence, and remaining limitations. Do not use web search.'
```

A typical generated workflow can use different tool scopes for each role:

```python
research = await parallel([
    lambda: agent(
        "Research option A and preserve useful source URLs.",
        instruction="Act as an independent web researcher.",
        tools=["duckduckgo_search"],
    ),
    lambda: agent(
        "Research option B and preserve useful source URLs.",
        instruction="Act as an independent web researcher.",
        tools=["duckduckgo_search"],
    ),
])

local_evidence = await agent(
    {"research": research},
    instruction="Run one small, read-only local check that verifies a concrete claim.",
    tools=["hostexec_exec_command"],
)

review = await agent(
    {"research": research, "local_evidence": local_evidence},
    instruction="Identify unsupported, conflicting, or stale claims.",
    tools=[],
)

return {"research": research, "local_evidence": local_evidence, "review": review}
```

For an implementation task, the generated workflow can instead use a bounded
quality loop. The workflow remains dynamic: the model decides the temporary
roles, their instructions, and the concrete checks for the request.

```python
design = await agent(
    "Define the cache API and acceptance criteria.",
    instruction="Act as the software architect.",
    tools=[],
)

issues = []
approved = False
for iteration in range(1, 4):
    implementation = await agent(
        {"design": design, "review_issues": issues},
        instruction="Implement the design and address every review issue.",
        tools=["hostexec_exec_command"],
    )
    tests = await agent(
        {"design": design, "implementation": implementation},
        instruction="Write focused tests and run them with the race detector.",
        tools=["hostexec_exec_command"],
    )
    review = await agent(
        {"design": design, "implementation": implementation, "tests": tests},
        instruction="Independently review the code and test evidence.",
        schema={
            "type": "object",
            "properties": {
                "approved": {"type": "boolean"},
                "issues": {"type": "array", "items": {"type": "string"},
            },
            "required": ["approved", "issues"],
            "additionalProperties": False,
        },
        tools=[],
    )
    approved = review["structured"]["approved"]
    issues = review["structured"]["issues"]
    if approved:
        break

return {
    "approved": approved,
    "iterations": iteration,
    "remaining_issues": issues,
}
```

The attempt limit prevents an unresolvable review from consuming resources
indefinitely. Passing on the first iteration is still a successful execution:
the loop exits early instead of manufacturing extra work.

## Security Boundary

This is a local development example, not a production sandbox. In particular,
`hostexec_exec_command` runs real host commands. The base directory only sets
the default working directory; it does not constrain filesystem access, form
an OS security boundary, or make untrusted shell commands safe.

Do not expose this configuration directly to untrusted users or a multi-tenant
service. Use a sandboxed or remote Agent/runtime, command policy, approval, and
resource limits appropriate to the deployment. Research roles should never
execute commands copied from web content.
