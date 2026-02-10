# Skill-Driven Dynamic Structured Output (JSON Schema)

This example demonstrates a **Skill-driven** pattern for selecting a **JSON Schema at runtime**, applying it to the **current invocation**, and returning a final JSON response that is extracted into `event.StructuredOutput`.

Key ideas:

- The agent starts with **no static structured output schema**.
- The model uses Skills (`skill_load` / `skill_run`) like normal tools.
- The model extracts a JSON schema from the chosen Skill and calls a custom tool: `set_output_schema`.
- `set_output_schema` updates `invocation.StructuredOutput`, which affects **subsequent** model calls in the same invocation.
- The example sets `WithOutputKey(...)` so `OutputResponseProcessor` is installed and `event.StructuredOutput` is produced (without framework changes).

How it works (in this demo):

1. The model chooses a skill (`plan_route` or `recommend_poi`) based on your input.
2. Call `skill_load` to mark that skill as loaded for this invocation.
3. On the next model request, the framework materializes the loaded `SKILL.md` into the prompt (by default as a system-message `[Loaded] <skill>` block).
4. The model finds the schema under **"Output JSON Schema"** in `SKILL.md` and calls `set_output_schema`.
5. The model calls `skill_run` (`cat result.json`) to produce a JSON result.
6. The model returns **only** that JSON as the final assistant message; the framework extracts it into `event.StructuredOutput` (untyped `map[string]any`, etc.).

## Run

From this directory:

```bash
go run . -model gpt-5
```

Then type requests to switch output formats dynamically. For example (same as the startup hints):

- "Plan a route from A to B and return distance and ETA. (Use plan_route for the output format.)"
- "Recommend a coffee shop POI in Shenzhen. (Use recommend_poi for the output format.)"

You can also force a specific skill by mentioning `plan_route` or `recommend_poi` in your prompt.

Type `exit` to quit.

Optional flags:

```bash
go run . -model gpt-5 -streaming=true
```

## Example session (trimmed)

```text
$ go run . -model gpt-5
...
> Plan a route from A to B and return distance and ETA. (Use plan_route for the output format.)
tool_call: 📥 skill_load (id=call_...)
  args: {"skill":"plan_route"}
tool_result: 📥 skill_load (id=call_...): "loaded: plan_route"
tool_call: 🧩 set_output_schema (id=call_...)
  args: {"schema":{...}}
tool_result: 🧩 set_output_schema (id=call_...): {"name":"output","ok":true}
tool_call: ▶️ skill_run (id=call_...)
  args: {"command":"cat result.json","skill":"plan_route"}
tool_result: ▶️ skill_run (id=call_...): {"stdout":"{...}","exit_code":0,...}
{"distance_km":12.3,"eta_min":25,"route":"A->B"}

event.StructuredOutput:
{
  "distance_km": 12.3,
  "eta_min": 25,
  "route": "A->B"
}
```

## Notes

- This approach works best when your model/provider supports native structured outputs (`structured_output` / `response_format=json_schema`).
- The schema set by `set_output_schema` only affects **subsequent** LLM calls (not the one that requested the tool call).
- Skill folders must include `SKILL.md` with YAML front matter (`--- ... ---`) so the repository can discover them.
- This demo embeds the JSON schema in `SKILL.md` under "Output JSON Schema" to avoid escaping issues from tool stdout.
- `set_output_schema` expects `schema` to be a JSON object.
- The demo prints tool call/response trace by default; disable with `-trace_tools=false`.
