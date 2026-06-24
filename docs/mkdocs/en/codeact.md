# Tool Code Orchestration

`codeexecutor/codeact` implements the CodeAct-style pattern: generated code can orchestrate a narrow, explicitly allowlisted set of trpc tools. The public capability is better described as **tool code orchestration**. It is not a replacement for writing stable business workflows in Go.

```text
LLM -> execute_tool_code -> Runtime -> guest call_tool(name, JSON args)
                                  -> Go Gateway -> trpc Tool
```

The Go gateway is the trust boundary: it rejects unknown tools, validates input JSON against the tool declaration, calls the tool, then validates/serializes its result before returning it to guest code. Guest code never gets Go references or host credentials.

`Runtime` is the transport boundary. The built-in `LocalRunner` implements it with a Python stdio guest. Remote or sandbox-native backends should implement `Runtime` directly and route sandbox-originated `ToolCall` values to the provided `ToolCallHandler`.

## Security

`LocalRunner` starts a local Python process and is only for development or an already isolated container/VM. It is **not** a security sandbox. Applications should implement `codeact.Runtime` for their existing remote sandbox, microVM, or container service, with the isolation, resource, and dependency policies appropriate to their environment.

## Why not call raw HTTP APIs from generated code?

Tool code orchestration separates dynamic control flow from access to system
capabilities. Generated code is useful for loops, branching, and transformation;
business API access remains a host-owned tool. That keeps authentication,
authorization, retry and idempotency policy, API-version adaptation, audit
logging, and rate limits in application code instead of model-generated code.

When an application genuinely needs HTTP access, expose a host adapter tool for
the business operation. For a deliberately broad but bounded HTTP surface,
expose a constrained `http_request`-style tool whose host controls allowed
domains and methods, credentials, and response-size limits. Do not treat
`LocalRunner`'s general Python environment as this security control: a
production runtime must enforce its own network and process policy.

| Need | Preferred implementation |
| --- | --- |
| Stable, deterministic business workflow | Go application code |
| Dynamic branching or loops across approved tools | `execute_tool_code` |
| Bounded external HTTP integration | Host adapter or constrained HTTP tool |
| Untrusted code or unrestricted external access | Isolated runtime plus explicit application policy |

## Example

For an agent-facing example that orchestrates catalog search, inventory
filtering, and quote creation, set `OPENAI_API_KEY` (and optionally
`OPENAI_BASE_URL`) and run:

```bash
cd examples && go run ./codeact -model gpt-5
```

Only `execute_tool_code` is exposed to the model; `search_catalog`,
`get_inventory`, and `create_quote` are callable only from guest Python through
the allowlist. The example deliberately does not set a `GenerationConfig`:
`openai.New` and environment variables provide the model configuration.

For an agent-facing tool, create `tool/toolcode.NewTool(runtime, managedTools)`. Register that returned tool on the agent as `execute_tool_code`; only `managedTools` become callable by `await call_tool(...)` in guest code.

Managed calls are direct, synchronous host-capability calls in this first version. The built-in Python guest permits only one outstanding call: `await` is required by the guest API, but `asyncio.gather(...)` does not create parallel host calls. A failed host call becomes a Python `RuntimeError`, which generated code may handle with `try`/`except`. Managed calls do not replay the agent's callback, retry, or inner tracing lifecycle, and they cannot pause an execution for interactive approval. Do not add tools that require an approval/resume flow to `managedTools`; enforce authorization in the business tool itself or use an application-defined adapter tool.

## Model-facing tool guidance

The default `execute_tool_code` declaration tells the model to prefer a single
call when it can finish the workflow, use only `await call_tool(name,
**json_arguments)`, make sequential calls, and return only a compact
JSON-compatible value. It also includes the name, description, input JSON
Schema, and output JSON Schema for every managed tool. Intermediate managed-tool
values remain in guest code; only the final `value` and captured `stdout` of
`execute_tool_code` become its outer tool result. Do not print or return raw
large tool values unless they are needed by the model. The instruction is
guidance, not a replacement for the runtime security boundary described above.

Write managed-tool descriptions as business contracts: explain the operation,
preconditions, units, enum meanings, and result semantics. Avoid transport
details such as headers or SDK mechanics unless the model needs them to choose
the operation. Use a host adapter when two tools need a domain-specific mapping
that cannot be described safely by a generic field transformation.

`toolcode.WithDescription` replaces the default description. A custom
description should preserve the `call_tool`-only capability boundary and the
JSON argument/result constraints.

## Boundaries with other execution tools

`execute_tool_code` does not replace `workspace_exec`,
`workspace_write_stdin`, or `workspace_kill_session`:

- The `workspace_exec` family runs shell commands, scripts, CLIs, and long
  tasks in the shared workspace. Its natural data model is terminal text,
  files, artifacts, and exit codes.
- `execute_tool_code` runs generated Python glue code that calls explicitly
  allowlisted ordinary Go tools through `call_tool(...)`. It is for branching,
  loops, JSON transformation, and multi-tool result aggregation.
- `tool/codeexec`'s `execute_code` is ordinary code execution for computation,
  data analysis, or logic verification. It does not bridge Python calls back
  into Go tools.

The default tool name is intentionally **not** `execute_code`: `tool/codeexec`
already uses `execute_code` for ordinary code execution. If an application
exposes only the tool-orchestration variant, it may override the name with
`toolcode.WithName("execute_code")`, but do not register both tools with the
same name on one agent.

## Choosing managed tools

`managedTools` is an application-owned, independent capability registry; it
is not inferred from the tools already registered on the agent. Prefer ordinary
business tools, data tools, and host-defined adapter tools. Normally exclude:

- `workspace_exec` / `workspace_write_stdin` / `workspace_kill_session`
- `workspace_save_artifact`
- `execute_code` / `execute_tool_code`
- `skill_run` / `skill_exec` / `skill_write_stdin` / `skill_poll_session` /
  `skill_kill_session`
- `transfer_to_agent` / `await_user_reply`

For each business operation, normally choose one model-facing path: expose it
as a direct agent tool, or make it callable only through `execute_tool_code`.
An application can deliberately register the same operation in both places,
but that gives the model two execution paths for the same action and weakens
the guidance about when code orchestration is useful. The allowlist remains the
actual capability boundary for guest code regardless of that choice.

These tools should normally stay out of the registry: execution tools create
recursive or heterogeneous execution chains; `transfer_to_agent` and
`await_user_reply` alter outer-invocation control flow; and `AgentTool` /
`DynamicAgentTool` start a sub-agent run with separate history, streaming-event,
and cost boundaries. The framework does not infer that policy from names or
types; applications should pass only the ordinary capabilities they explicitly
intend to orchestrate to `tool/toolcode.NewTool`.

## Data flow

Small tool values use JSON. Intermediate values stay in guest code; only the
returned `value` and captured `stdout` are sent back as the outer tool result.
Return a compact aggregate, identifier, or artifact/workspace reference rather
than a raw large payload. For large datasets, tools should return an
artifact/workspace reference and code should process the mounted artifact
instead of serializing the full payload through the model context. Semantic
conversions between tool A and B are explicit code or a host-defined adapter
tool; tool code orchestration never guesses domain mappings.
