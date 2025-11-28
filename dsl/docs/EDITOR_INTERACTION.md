# DSL Editor ↔ Backend Interaction Overview

This document describes how a visual DSL editor should interact with the
trpc-agent-go DSL backend: which APIs to call, when to call them, and what
request/response shapes to expect.

The goal is to:

- Keep the editor **stateless** with respect to engine internals.
- Let the backend own all schema / variable inference (introspection).

## Core data models

The engine-level schemas live in `dsl/schema/engine_dsl.schema.json`.

- **Graph**
  - Root object edited and sent by the frontend.
  - Fields: `version`, `name`, `description`, `nodes`, `edges`,
    `conditional_edges`, `state_variables`, `start_node_id`, `metadata`.
  - Valid values for `node.node_type` and the shape of each `node.config`
    are statically enumerated in `$defs.Node` via `oneOf` branches. The
    editor can treat this schema as the single source of truth for which
    node types exist and how they are configured.

## High-level flow

From an editor's point of view, a typical session looks like:

1. **User edits a draft graph (in memory)**
2. **Ask backend for schema/variable/connection introspection as needed**
3. **Validate the draft graph**
4. **Save / publish the graph**
5. **Execute the graph**

### 1. Maintain a draft Graph on the frontend

The editor maintains a **draft graph JSON** in memory. It should conform to
`$defs.Graph` from `dsl/schema/engine_dsl.schema.json` as closely as
possible, but it does not have to be valid at all times (e.g., while the user
is in the middle of wiring edges).

All introspection/validation APIs below accept this draft graph as input.

### 3. Introspection APIs

#### 3.1 Infer global state fields and usage

- **API**: `POST /api/v1/graphs/schema`
- **Request body**: draft `Graph` JSON
- **Response**: `GraphSchemaResponse`

```jsonc
{
  "fields": [
    {
      "name": "messages",
      "type": "[]model.Message",
      "kind": "array",
      "json_schema": { "...": "..." },
      "writers": ["llm_agent"],
      "readers": ["guardrail_node"]
    }
  ]
}
```

Semantics:

- Backend inspects the graph and:
  - Adds built-in fields (messages, user_input, last_response, node_structured, etc.).
  - Applies `state_variables` declarations.
  - Incorporates component outputs and well-known nodes (e.g., `builtin.end` /
    `builtin.transform` / `builtin.set_state`).

Editor usage:

- Show a global list of state fields and their types.
- Offer suggestions when configuring expressions (e.g., Transform/End).

#### 3.2 Per-node variable view

- **API (all nodes)**: `POST /api/v1/graphs/vars`  
  **Body**: draft `Graph` JSON  
  **Response**: `GraphVarsResponse`

- **API (single node, optional)**: `POST /api/v1/graphs/vars/node`  
  **Body**:

  ```jsonc
  {
    "graph": { /* draft Graph JSON */ },
    "node_id": "classification_agent"
  }
  ```

  **Response**: `GraphVarsNode`

```jsonc
{
  "nodes": [
    {
      "id": "classification_agent",
      "title": "Classification agent",
      "vars": [
        { "variable": "state.user_input", "kind": "string" },
        { "variable": "input.output_parsed.classification", "kind": "string" }
      ]
    }
  ]
}
```

Semantics:

- Backend returns, for each node, the variables that are meaningful in that
  node's expressions/config.

Editor usage:

- Right-hand variable pickers for:
  - Transform expressions
  - End expressions
  - Conditions
  - Set state expressions

##### Variable naming conventions

The `variable` field in `GraphVar` is intended to be copied **verbatim**
into expressions/templates. Editors do not need to reverse‑engineer how it is
computed; the backend guarantees that it is a valid path in the current
graph context.

Recommended naming scheme (subject to evolution, but stable at the prefix
level):

- `state.*` – graph‑level state fields
  - Examples: `state.user_input`, `state.greeting`, `state.counter`,
    `state.end_structured_output`.
- `nodes.<node_id>.*` – per‑node structured outputs
  - Examples:
    - `nodes.classification_agent.output_parsed.classification`
    - `nodes.transform_1.result.original_text`
- `graph.*` – graph‑level inputs or metadata (if exposed)
  - Example: `graph.input_as_text` (for chat-style graphs).

When using the single-node API (`/graphs/vars/node`), the editor only
receives the `vars` array for the node currently being edited, which is often
enough for inline variable pickers without having to search through the full
`nodes[]` list.

Frontend guidance:

- Treat `variable` as an opaque expression snippet:
  - Display it (optionally grouping by the first segment: `state`/`nodes`/`graph`).
  - Insert it into the expression when the user selects it.
- Use `kind` / `json_schema` for UI decisions (icons, editors, tree views),
  not for reconstructing engine internals.

#### 3.3 Inspect a connection between two nodes

When the user draws an edge between nodes (for example, Agent → MCP), the
editor may want to know whether the connection is type-compatible, and why it
is invalid if not.

- **API**: `POST /api/v1/graphs/edges/inspect`
- **Request body**:

  ```jsonc
  {
    "graph": { /* draft Graph JSON */ },
    "edge": {
      "source_node_id": "Agent1",
      "target_node_id": "MCP1"
    }
  }
  ```

- **Response**: `EdgeInspectionResult`

  ```jsonc
  {
    "valid": false,
    "errors": [
      {
        "code": "missing_field",
        "message": "MCP requires input.repoName, but Agent doesn't provide repoName",
        "path": "input.repoName"
      }
    ],
    "source_output_schema": {
      "type": "object",
      "properties": {
        "output_text": { "type": "string" },
        "output_parsed": {
          "type": "object",
          "properties": { /* user-defined schema from Agent output_format.schema */ }
        }
      }
    },
    "target_input_schema": {
      "type": "object",
      "properties": {
        "repoName": { "type": "string" }
      },
      "required": ["repoName"]
    }
  }
  ```

Semantics (example):

- For `builtin.llmagent`:
  - `source_output_schema` always has at least:
    - `output_text: string`
    - `output_parsed: object` whose inner schema is taken from
      `AgentConfig.output_format.schema` when `type = "json"`.
- For `builtin.mcp`:
  - `target_input_schema` is derived from the selected MCP tool's input schema.

Editor usage:

- Display the source/target schemas in an “Inspect connection” panel.
- Show inline errors when the connection is invalid (e.g., missing fields).

### 4. Validate draft graph

- **API**: `POST /api/v1/graphs/validate`
- **Request body**: draft `Graph` JSON
- **Response**: `ValidationResult`

```jsonc
{
  "valid": false,
  "errors": [
    { "field": "nodes[2].node_type", "message": "component builtin.foo not found in registry" },
    { "field": "start_node_id", "message": "start_node_id start does not exist" }
  ]
}
```

Backend checks:

- Graph structure (version/name/nodes/edges/start_node_id).
- Node/component references (node_type exists in registry).
- Special rules for builtin nodes (e.g., `builtin.start` uniqueness and edges).
- State variable rules (e.g., `builtin.set_state` assignments must target
  declared `state_variables`).
- Topology (e.g., unreachable nodes).

Editor usage:

- Show validation results before allowing publish/save.

### 5. Save / publish graph

- **Create**: `POST /api/v1/graphs`
  - Body: final `Graph` JSON
  - Response: `GraphResponse` (includes `id`, timestamps).

- **Update**: `PUT /api/v1/graphs/{id}`
  - Body: updated `Graph` JSON

Editor usage:

-- Save current graph as a versioned asset (similar to OpenAI Agent Builder
  publishing a workflow).

### 6. Execute graph

- **API**: `POST /api/v1/graphs/{id}/execute` (or `/execute/stream` for SSE)
- **Request body**: `ExecutionRequest`

```jsonc
{
  "input": {
    "user_input": "Hello, world"
  },
  "config": {
    "max_iterations": 100,
    "timeout_seconds": 300
  }
}
```

- **Response**: `ExecutionResult`

```jsonc
{
  "execution_id": "exec_123",
  "status": "success",
  "final_state": {
    "messages": [ /* ... */ ],
    "end_structured_output": { /* ... */ }
  },
  "events": [
    { "type": "node_start", "node_id": "start", "timestamp": "..." },
    { "type": "node_end", "node_id": "end", "timestamp": "..." }
  ]
}
```

Editor usage:

- Run graph test executions from the UI.
- Show per-node traces based on `events`.
