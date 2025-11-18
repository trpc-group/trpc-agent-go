# DSL Schema Overview

This directory contains JSON Schemas that describe the **engine‑level data model** used by the DSL APIs. These schemas are intended for:

- Frontend editors (visual graph builders)
- HTTP / OpenAPI clients
- Code generators and tooling

They are **not** tied to any specific UI; they describe only execution semantics.

## Engine DSL core schema

The authoritative engine‑level schema lives in:

- `dsl/schema/engine_dsl.schema.json`

Key top‑level definitions inside `$defs`:

- **`Graph`**
  - The root type for a graph DSL document.
  - Fields:
    - `version` – DSL version string (e.g., `"1.0"`).
    - `name` / `description` – graph metadata.
    - `nodes` – array of `Node` objects.
    - `edges` – array of `Edge` objects (direct connections).
    - `conditional_edges` – array of `ConditionalEdge` objects (if/else, tool routing, etc.).
    - `state_variables` – graph‑level state declarations (global variables).
- `start_node_id` – ID of the visual start node (usually `builtin.start`); the executable entry point is derived from its outgoing edge.
    - `metadata` – arbitrary graph‑level metadata (engine‑agnostic).

- **`Node`**
  - An executable node in the graph.
  - Fields:
    - `id` – unique node identifier within the graph.
    - `label` – human‑readable label.
    - `node_type` – component name in the registry (e.g., `"builtin.llmagent"`).
    - `node_version` – optional component version.
    - `config` – component‑specific configuration for this node instance.
    - `description` – optional description.
  - Note: `inputs` / `outputs` are *not* part of the graph schema. Node I/O comes from component metadata (see below).

- **`Edge` / `ConditionalEdge` / `Condition`**
  - `Edge` – direct connection (`source` → `target`).
  - `ConditionalEdge` – routing based on a `Condition` evaluated at `from` node.
  - `Condition` – union of:
    - builtin (`ConditionBuiltin`)
    - function (`ConditionFunction`)
    - tool routing (`ConditionToolRouting`)

- **`StateVariable`**
  - Graph‑level global variable declaration.
  - Fields:
    - `name` – state key (e.g., `"greeting"`, `"counter"`).
    - `kind` – coarse type: `"string" | "number" | "boolean" | "object" | "array" | "opaque"`.
    - `json_schema` – optional JSON Schema for structured objects.
    - `description` – human description.
    - `default` – default value when absent.
    - `reducer` – reducer name used when multiple writers update this field.

## Component metadata and I/O schema

The I/O schema for each component is not encoded inside the graph; it is described by **component metadata** and exposed via the `/api/v1/components` API. The corresponding schema types are:

- **`ComponentMetadata`** (see `server/dsl/openapi.json` `#/components/schemas/ComponentMetadata`)
  - Describes a reusable component in the registry.
  - Fields:
    - `name` – unique component name (e.g., `"builtin.llmagent"`).
    - `display_name` – human‑readable label for editors.
    - `description` – what this component does.
    - `category` – logical grouping (LLM, Tools, Control, Data, etc.).
    - `version` – component version string.
    - `inputs` – array of `ParameterSchema` describing input parameters.
    - `outputs` – array of `ParameterSchema` describing state fields produced by this component.
    - `config_schema` – array of `ParameterSchema` describing `node.config` fields.
    - `meta` – engine‑agnostic metadata for higher layers (UI hints, tags, etc.).

- **`ParameterSchema`**
  - Shared shape for inputs, outputs, and config parameters.
  - Important fields:
    - `name` – parameter name (e.g., `"messages"`, `"result"`, `"model_name"`).
    - `display_name` – label used in UIs.
    - `description` – description for users.
    - `type_id` – DSL‑level type identifier for editors (e.g., `"string"`, `"number"`, `"graph.messages"`, `"llmagent.output_parsed"`).
    - `kind` – coarse kind: `"string" | "number" | "boolean" | "object" | "array" | "opaque"`.
    - `required` – whether the parameter must be provided.
    - `default` – default value when omitted.
    - `enum` – optional finite set of allowed values.
    - `placeholder` – optional placeholder text for editor inputs.
    - `json_schema` – optional JSON Schema when this parameter is a structured object.

### How editors should use these schemas

A typical frontend flow:

1. **List components**
   - Call `GET /api/v1/components`.
   - Response is an array of `ComponentMetadata` objects.
   - Use `inputs` / `outputs` / `config_schema` to:
     - Build the palette of available node types.
     - Render config forms for `node.config`.
     - Show variable suggestions based on component outputs.

2. **Edit and save graphs**
   - When saving, construct a JSON object that matches the `Graph` definition in `engine_dsl.schema.json`.
   - `node_type` is set to one of the component `name` values (e.g., `"builtin.transform"`).
   - `config` is filled according to the component’s `config_schema`.
   - `state_variables` is used to declare global state fields (schema + reducer).

3. **Introspection / variable suggestions**
   - The `/api/v1/graphs/schema` and `/api/v1/graphs/vars` endpoints provide:
     - Inferred state fields and usages (`FieldUsage`).
     - Per‑node variables (`GraphVars*`).
   - These types are defined in `server/dsl/openapi.json` and align with `ParameterSchema`/`ComponentMetadata`.

## Built‑in components (overview)

Built‑in components are implemented under `dsl/registry/builtin`. Each component registers its own `ComponentMetadata` via `Metadata()`. Below is a **schema‑oriented** overview: for each `node_type` we only describe the high‑level meaning of its `inputs` / `outputs` / `config_schema`; the full field list is defined by the `ComponentMetadata` objects returned from `/api/v1/components`.

- **`builtin.start`**
  - `inputs` (`ParameterSchema[]`): empty.
  - `outputs` (`ParameterSchema[]`): empty; does not write to state.
  - `config_schema` (`ParameterSchema[]`): currently empty; may be extended in the future (for example to declare graph‑level variables).

- **`builtin.llm`**
  - `inputs`:
    - Primarily `messages` (`type_id: graph.messages, kind: array`) plus model configuration parameters (`model_name`, `instruction`, `temperature`, `max_tokens`, ...).
  - `outputs`:
    - `messages` (appended to the conversation history).
    - `last_response` (the most recent model output).
  - `config_schema`:
    - Model name, system instruction, sampling parameters, etc. (aligned with OpenAI model settings).

- **`builtin.llmagent`**
  - `inputs`:
    - Similar to `builtin.llm`: `messages` plus model configuration.
  - `outputs`:
    - `last_response` / `messages` as above.
  - `config_schema`:
    - `model_name`, `instruction`, `tools`, `tool_sets`, `mcp_tools`, `structured_output` (JSON Schema), sampling parameters, etc.

- **`builtin.agent`**
  - `inputs`:
    - Typically `messages`, and it may read additional fields from global state.
  - `outputs`:
    - Focuses on `last_response` / `node_responses` for higher‑level agent compositions.
  - `config_schema`:
    - Higher‑level agent behavior settings, such as agent name, message isolation, event scope, etc.

- **`builtin.tools`**
  - `inputs`:
    - `messages` (containing tool calls).
    - `tools` (references/configuration for the tool set).
  - `outputs`:
    - Updated `messages` including tool results.
  - `config_schema`:
    - How to select and bind concrete tools.

- **`builtin.end`**
  - `inputs`:
    - Reads from global state (especially `node_structured` and previous node outputs) to construct the final result.
  - `outputs`:
    - `end_structured_output` (`kind: object`) – the final structured result object.
  - `config_schema`:
    - `output_schema` (JSON Schema describing the final output shape).
    - `expr` (structured output expression; currently JSON+template, future versions may use a dedicated expression engine).

- **`builtin.transform`**
  - `inputs`:
    - Reads from state and previous node outputs; the exact fields are determined by the expression/template.
  - `outputs`:
    - `result` (`kind: object` or `array`, depending on `output_schema`).
  - `config_schema`:
    - `output_schema` (JSON Schema for the target object).
    - `expr` (expression that produces the transformed object).

- **`builtin.set_state`**
  - `inputs`:
    - Reads existing state and upstream outputs to evaluate assignment expressions.
  - `outputs`:
    - No explicit `outputs` entry; updates graph‑level `state_variables` via assignments.
  - `config_schema`:
    - `assignments`: array of objects with `field` (target state key) and `expr` (expression object).

- **`builtin.user_approval`**
  - `inputs`:
    - Text or structured results from upstream nodes (for example, an LLM output).
  - `outputs`:
    - `approval_result` (boolean or enum indicating approve/reject).
    - `last_response` (message shown to the user).
  - `config_schema`:
    - Approval message, auto‑approve options, timeout behavior, etc.

- **`builtin.http_request`**
  - `inputs`:
    - Templated `url` / `body` / `headers`, typically rendered from state variables.
  - `outputs`:
    - `status_code` (number).
    - `response_body` (string or object, depending on implementation).
    - `response_headers` (object).
  - `config_schema`:
    - `method`, `url_template`, `body_template`, `headers`, and other HTTP request settings.

- **`builtin.code`**
  - `inputs`:
    - Code string and related context.
  - `outputs`:
    - `output` (standard output).
    - `output_files` (list of generated files or artifacts).
  - `config_schema`:
    - `code`, `language`, `executor_type`, `timeout`, `work_dir`, `clean_temp_files`, and other execution environment settings.

- **`builtin.passthrough`**
  - `inputs`:
    - Wildcard I/O (often a `*` parameter), represented in `ParameterSchema` as a loose type.
  - `outputs`:
    - The same wildcard output (directly passing through the input).
  - `config_schema`:
    - Minimal or empty; primarily used for debugging and pipeline wiring.

For the authoritative and always up‑to‑date view of the **execution DSL** shape, the single source of truth is:

- `dsl/schema/engine_dsl.schema.json` – engine‑level Graph / Node / Edge / ConditionalEdge / StateVariable, plus builtin node configs such as `AgentConfig`, `WhileConfig`, `UserApprovalConfig`, `TransformConfig`, `SetStateConfig`, and `MCPConfig`.
