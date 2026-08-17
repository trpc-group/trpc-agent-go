# Dynamic Workflow Model Routing Example

This example shows host-authorized child-Agent model profiles for Dynamic
Workflow. The application registers one neutral `general_agent` template and
two profile aliases. Workflow code may select an alias per `agent(...)` call,
or omit `model` to keep the template default.

```text
workflow_assistant
└── run_workflow
    └── registered base agent: general_agent
        ├── template default model: -model
        ├── model profile "fast": -fast-model (or -model when empty)
        └── model profile "deep": -deep-model (or -model when empty)
```

## Host-owned profile aliases

A profile name such as `fast` or `deep` is a host-owned workflow alias, not a
provider model identifier. `WithAgentModelProfile` is an allowlist: the host
maps each alias to a concrete `model.Model` instance, and unknown aliases fail
before the child Agent runs. Workflow code cannot invent endpoints, API keys,
or provider model strings; it may only pass registered aliases.

This example builds every model through the same OpenAI-compatible environment
(`OPENAI_API_KEY`, optional `OPENAI_BASE_URL`). That is an example convenience,
not a framework limit: other applications may register profile models from
different providers or endpoints.

## Per-call override

Selecting `model="fast"` or `model="deep"` overrides the model for that child
call only. It does not mutate the registered template. Omitting `model`
preserves the template model exactly, which this example demonstrates with a
final summary role that leaves the field unset.

The root prompt stays lean: answer simple tasks directly, call `run_workflow`
for temporary collaboration, and apply a compact host routing policy
(`fast` / `deep` / omit). The tool declaration still carries the profile
catalog. Primary sample prompts state semantic needs only; they do not name
aliases. This example focuses on per-child model selection with a short
sequential workflow, not on concurrency primitives. Structured review with
`schema` depends on provider JSON Schema support; when that is unavailable,
use a plain-text review child as the portable fallback.

## Event stream and observability

Child Agent events remain in the same Runner stream as the parent. Event
authors are template Agent names such as `general_agent`, optionally annotated
with `via dynamic_workflow`. They are not profile aliases.

Profile selection is visible in the generated workflow source and in the
`run_workflow` tool-call arguments (`model="fast"`, `model="deep"`, or
omitted). When the provider returns a response model field, the event printer
emits one `provider model: <name>` notice per invocation
(`InvocationID`, with a safe fallback key). That field is the provider-reported
response model value, which may be generic; it is not the workflow alias. The
startup mapping remains the host-side source of truth for alias configuration.

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

From the `examples` module:

```bash
cd examples
go run ./dynamicworkflow/modelrouting -model gpt-5
```

With no `-prompt`, the example starts an interactive chat loop. Commands:

- `/new`: start a fresh session
- `/exit`: quit the demo

Startup output prints the root model, the template default model, and the
alias-to-concrete-model mapping.

### One-model fallback

When `-fast-model` and `-deep-model` are empty, both profiles resolve to
`-model`. The alias allowlist and per-call selection still apply; only the
concrete model names coincide:

```bash
go run ./dynamicworkflow/modelrouting -model gpt-5 \
  -prompt 'Compare sharded LRU and W-TinyLFU for a Go local cache. Use run_workflow once: run two independent quick analyses in sequence, then a rigorous structured review, then a balanced concise summary.'
```

### Multi-model use

Pass distinct provider model names for the two profiles:

```bash
go run ./dynamicworkflow/modelrouting \
  -model gpt-5 \
  -fast-model gpt-5-mini \
  -deep-model gpt-5 \
  -prompt 'Compare sharded LRU and W-TinyLFU for a Go local cache. Use run_workflow once: run two independent quick analyses in sequence, then a rigorous structured review, then a balanced concise summary.'
```

### Other sample prompts

Simple direct answer (no workflow required):

```bash
go run ./dynamicworkflow/modelrouting -model gpt-5 \
  -prompt 'In one sentence, what is the difference between a mutex and a channel for mutual exclusion in Go?'
```

Bounded draft, review, and revision loop:

```bash
go run ./dynamicworkflow/modelrouting \
  -model gpt-5 \
  -fast-model gpt-5-mini \
  -deep-model gpt-5 \
  -prompt 'Draft a short Go local-cache API proposal with an efficient drafter, have a rigorous reviewer return structured approval and issues, revise for at most two review rounds, and finish with a balanced concise summary.'
```

Optional explicit routing check (deterministic troubleshooting, not the main
experience):

```bash
go run ./dynamicworkflow/modelrouting \
  -model gpt-5 \
  -fast-model gpt-5-mini \
  -deep-model gpt-5 \
  -prompt 'Use run_workflow once: analyze sharded LRU with model="fast", analyze W-TinyLFU with model="fast", then model="deep" for a structured review, and omit model for a concise summary.'
```

`-show-workflow-code` defaults to `true` so the generated Python, including
profile selections, is easy to inspect. Pass `-show-workflow-code=false` to
suppress that separate print; tool-call arguments still include the code.

## Expected workflow shape

Exact roles and schemas are model-generated. After the root model chooses
profiles from the host catalog, a comparison workflow may look like this:

```python
lru = await agent(
    "Analyze sharded LRU for a Go local cache.",
    instruction="Act as an independent analyst. Cover hit-rate behavior, concurrency, and operational tradeoffs.",
    model="fast",
    tools=[],
)

tinylfu = await agent(
    "Analyze W-TinyLFU for a Go local cache.",
    instruction="Act as an independent analyst. Cover hit-rate behavior, concurrency, and operational tradeoffs.",
    model="fast",
    tools=[],
)

comparison = await agent(
    {"lru": lru["text"], "tinylfu": tinylfu["text"]},
    instruction="Synthesize the analyses into a structured comparison and recommendation.",
    model="deep",
    schema={
        "type": "object",
        "properties": {
            "recommendation": {"type": "string"},
            "tradeoffs": {"type": "array", "items": {"type": "string"}},
        },
        "required": ["recommendation", "tradeoffs"],
    },
    tools=[],
)

summary = await agent(
    {"comparison": comparison["structured"]},
    instruction="Write a concise executive summary for engineering leads.",
    tools=[],
)

return {
    "lru": lru["text"],
    "tinylfu": tinylfu["text"],
    "comparison": comparison["structured"],
    "summary": summary["text"],
}
```

The final summary omits `model`, so it uses the template default. Profile
selection never changes the template registration itself.

## Runtime notes

`LocalRunner` starts a local Python process and is not a security sandbox. This
example registers no child tools and no code-callable tools; the focus is model
profile selection only. In production, provide a `dynamicworkflow.Runtime`
suited to your trust boundary and register only the model profiles your host is
willing to expose.
