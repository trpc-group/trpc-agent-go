# Dynamic Workflow with reusable Skills

This example shows a normal Agent with two reusable workflow recipes. The
Dynamic Workflow tool is available from the first model request. A Skill is
not a tool gate and it is not a stored script: it is human-readable process
knowledge that helps the root Agent decide how to build a request-specific
workflow.

```text
user request
    -> compact summaries of available Skills
    -> root Agent chooses and loads the best recipe
    -> root Agent compiles the recipe for this request
    -> one standard run_workflow call
    -> explicit loop or parallel control flow
    -> child Agent events and final result
```

The repository contains two deliberately different recipes:

- `quality-loop` describes a bounded draft/review/revise loop and includes a
  small illustrative code shape. The model adapts its roles, criteria, inputs,
  and output to the current request.
- `fanout-analysis` describes a parallel analysis and synthesis process in
  prose only. It demonstrates that a recipe does not require the author to
  write workflow code.

The recipe is a reusable starting point, not a fixed business case. A request
about a notice, plan, design, investigation, or another deliverable can use the
same quality loop. A request needing independent perspectives can use the
fan-out recipe. If neither applies, the root Agent may construct a workflow on
its own.

## Key configuration

The root Agent keeps the workflow capability and Skill knowledge separate:

```go
root := llmagent.New(
    "workflow_assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithSkills(repo),
    // This example needs to load knowledge, not execute files from a Skill.
    llmagent.WithAllowedSkillTools(llmagent.SkillToolLoad),
    // run_workflow is visible from the first request.
    llmagent.WithTools([]tool.Tool{workflowTool}),
)
```

With this configuration, the first request can see both `skill_load` and the
standard `run_workflow`. When a recipe matches, the model normally loads it
first, waits for the `skill_load` result in the next model request, and then
calls `run_workflow` once; it should not issue both calls in one response.
Loading a Skill adds its body to subsequent model requests in the current
turn; it does not replace the workflow tool or turn the Markdown into
executable code.

Simple requests can still be answered directly; they do not need to load a
Skill or call `run_workflow`.

`WithAllowedSkillTools(llmagent.SkillToolLoad)` is intentional. It keeps the
root tool surface small and avoids automatically adding Skill execution,
workspace, or session-control tools. If a recipe has a short reference file,
the model can request it through the `docs` or `include_all_docs` arguments of
`skill_load`; this example keeps the second recipe prose-only.

## Typical compiled shapes

After loading `quality-loop`, the generated workflow may have a shape like
this (the concrete role instructions and request data are supplied by the
current task):

```python
review_schema = {
    "type": "object",
    "properties": {
        "approved": {"type": "boolean"},
        "feedback": {"type": "array", "items": {"type": "string"}},
    },
    "required": ["approved", "feedback"],
    "additionalProperties": False,
}

draft = await agent(request, instruction="Write the first draft.", tools=[])
previous_feedback = []
for attempt in range(1, 4):
    review = await agent(
        {
            "request": request,
            "draft": draft["text"],
            "previous_feedback": previous_feedback,
        },
        instruction=(
            "Review against the requested criteria. If the user did not provide "
            "factual values such as dates, URLs, or contacts, clear placeholders "
            "are acceptable: do not reject solely because they are not concrete "
            "and do not ask the writer to invent facts. Check that placeholders "
            "are clear and required fields or steps are complete."
        ),
        schema=review_schema,
        tools=[],
    )
    decision = review["structured"]
    approved = decision["approved"] and not decision["feedback"]
    if approved or attempt == 3 or not decision["feedback"]:
        break
    previous_feedback = decision["feedback"]
    draft = await agent(
        {"draft": draft["text"], "feedback": previous_feedback},
        instruction="Revise the draft using every required change.",
        tools=[],
    )
return {
    "draft": draft["text"],
    "approved": approved,
    "reviews": attempt,
    "remaining_feedback": decision["feedback"],
}
```

After loading `fanout-analysis`, the generated workflow can use
`parallel([...])` for independent branches, then pass the ordered results and
the original constraints to a separate synthesis Agent. The Skill explains
the data handoff and bounded failure handling in prose; it does not prescribe
the number of branches or a particular domain.

The code is only orchestration glue. The child Agents perform the substantive
writing, analysis, review, or synthesis, and structured outputs are used when
the workflow needs a reliable branch or loop decision.

## Add a reusable workflow recipe

An application can add a directory under its Skills root containing a
`SKILL.md` with a short front matter summary and a body that answers:

1. When is this process a good match, and what should the model do when it is
   not a match?
2. Which roles or stages are independent, sequential, or conditional?
3. Which values must be structured for branching, and what are the exit and
   retry limits?
4. Which inputs and previous results must be passed explicitly to each stage?
5. Which capabilities are allowed, and which side effects require serial
   handling or user confirmation?

Prefer process prose when it is enough. Include a short fenced code shape when
it prevents a recurring syntax or data-flow mistake. Longer examples or
reference material can be placed in Markdown or text docs and loaded on
demand; keep the summary compact so unrelated requests do not pay for the
entire recipe.

Do not store secrets, credentials, or unrestricted commands in a Skill. A
recipe guides model-generated orchestration; the registered Agent templates
and Runtime still enforce the capabilities available to the workflow.

## Root workflow Skills and child Agent Skills

The Skill used by the root Agent in this example describes *how to orchestrate
the current task*. A registered child Agent may separately have domain Skills
that describe *how that role performs its work*. Dynamic Workflow code can
omit `skills` to inherit the template's eligible Skills, pass `skills=[]` to
disable them for one child, or pass a non-empty list to narrow the existing
set. Selection is a capability boundary, not an implicit load. If the child
template exposes `skill_load` and the role needs a selected Skill's body, the
child can load it; its loaded state is isolated from the root Agent.

## Events and execution boundary

The root Runner receives the Dynamic Workflow tool event and the child Agent's
model and tool events through the same event stream. Applications can therefore
show child progress without treating the generated workflow as a detached
script. The local Runtime executes one temporary program and routes each
`agent(...)` call through explicitly registered templates; workflow code does
not gain arbitrary access to the host Agent or undeclared tools.

This pattern makes control flow explicit and preserves reusable process
knowledge, but it does not make model output mathematically deterministic. The
model still chooses the recipe, adapts instructions, and produces content.
Keep loops bounded, use structured decisions, inspect generated code when
appropriate, and choose a sandboxed or remote Runtime when the code is not
trusted.

## Prerequisites

- Go 1.24.4 or later
- Python 3 available as `python3` (the example uses
  `dynamicworkflow.LocalRunner`)
- An OpenAI-compatible model endpoint

## Run

From the `examples` module:

```bash
export OPENAI_API_KEY="your-api-key"
# Optional for an OpenAI-compatible endpoint:
export OPENAI_BASE_URL="https://your-endpoint/v1"

go run ./dynamicworkflow/skills -model gpt-5
```

With no `-prompt`, the example starts an interactive chat loop. Commands:

- `/new`: start a fresh session
- `/exit`: quit the example

The example prints the model-visible tools before every root model request.
The first trace includes both `skill_load` and `run_workflow`; later traces
show the corresponding tool calls and results while the workflow runs.
Generated workflow code is printed by default, so you can inspect how the
recipe shaped the one-shot control flow. Disable either diagnostic with
`-trace-tools=false` or `-show-workflow-code=false`.

Try the same recipe with a different request:

```bash
go run ./dynamicworkflow/skills -model gpt-5 \
  -prompt 'Prepare a concise rollout plan for replacing recurring status meetings with async updates and one weekly decision meeting. Have it independently reviewed for clarity, feasibility, ownership, and rollback readiness, and revise it until approved.'
```

The recipe remains unchanged while the generated roles, criteria, data, and
final deliverable adapt to the new request.

To exercise the prose-only recipe, ask for independent perspectives and a
synthesis:

```bash
go run ./dynamicworkflow/skills -model gpt-5 \
  -prompt 'Compare two reasonable approaches to introducing a new service boundary. Analyze feasibility, operational impact, migration risk, and open questions from independent perspectives, then synthesize a recommendation with the evidence and uncertainties.'
```
