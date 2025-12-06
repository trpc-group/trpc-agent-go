# DSL 编辑器与后端交互流程说明

本文档说明可视化 DSL 编辑器与 trpc-agent-go DSL 后端之间的交互流程：  
有哪些接口、在什么时机调用、请求和返回大致是什么结构。

目标：

- 前端不需要理解引擎内部实现，只维护 Graph JSON 草稿。
- 所有“变量、类型、可用字段”的推导都交给后端（introspection）。

## 核心数据模型

引擎层的统一 Schema 定义在：

- `dsl/schema/engine_dsl.schema.json`

其中最重要的几个类型：

- **`Graph`**
  - 前端编辑、提交给后端的根对象。
  - 字段：`version/name/description/nodes/edges/conditional_edges/state_variables/start_node_id/metadata`。
  - 各种 `node_type` 以及对应 `config` 的结构，已经在 `$defs.Node` 的 `oneOf` 分支里**静态枚举**好了，前后端都可以把这份 Schema 当成「有哪些节点类型、每个节点怎么配置」的唯一事实来源。

## 整体交互流程（概览）

从编辑器角度，一个典型的交互流程是：

1. **在前端内存中维护 Graph 草稿**
2. **需要变量/类型/连接信息时调用 introspection 接口**
3. **保存前做一次整体校验**
4. **保存 / 发布 Graph**
5. **执行 Graph（测试或线上）**

下面按步骤展开说明。

### 1. 前端维护 Graph 草稿

编辑器在内存中维护一份 **Graph JSON 草稿**：

- 尽量遵守 `engine_dsl.schema.json` 中 `$defs.Graph` 的结构；
- 允许是“不完全合法”的中间状态（比如某个节点临时没有 `node_type`，某条边的 `target` 还没选好）。

后续所有 introspection / validate / save 接口，都直接以这份草稿 Graph 作为请求体。

### 3. Introspection：变量 / 类型推导

#### 3.1 推导全局 State 字段 & 读写关系

- 接口：`POST /api/v1/graphs/schema`
- 请求体：当前 Graph 草稿 JSON
- 返回：`GraphSchemaResponse`

示意结构：

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

后端内部会做的事情（`SchemaInference`）：

- 把框架内置字段加入 Schema：`messages/user_input/last_response/node_structured/...`；
- 根据 `state_variables` 声明增加字段；
- 按组件的 `ComponentMetadata.Outputs` / builtin 节点（例如 `builtin.end` / `builtin.transform` / `builtin.set_state`）推导 Writer；
- 得到一个统一的 State 视图和读写关系。

前端用途：

- 在某些高级界面（例如“全局变量列表”）展示当前 Graph 里有哪些字段；
- 作为变量搜索/自动补全的候选集合。

#### 3.2 每个节点可用变量视图

- 接口（全部节点）：`POST /api/v1/graphs/vars`  
  - 请求体：当前 Graph 草稿 JSON  
  - 返回：`GraphVarsResponse`

- 接口（单个节点，可选）：`POST /api/v1/graphs/vars/node`  
  - 请求体示例：

    ```jsonc
    {
      "graph": { /* Graph 草稿 JSON */ },
      "node_id": "classification_agent"
    }
    ```

  - 返回：`GraphVarsNode`

示意结构：

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

含义：

- 对于每个节点，后端给出“在这个节点的表达式里可以引用哪些变量”，以及它们的类型信息。

前端用途：

- 节点右侧面板中的变量选择器：
  - Transform/End 的表达式；
  - 条件判断；
  - Set state 等节点的赋值表达式。

##### 变量命名约定（variable 字段）

`GraphVar.variable` 字段就是“在表达式/模板里要写的变量名/路径字符串”，前端可以把它当成**不透明的片段**，直接插入即可，不需要自己去推导它背后是如何从 State 计算出来的——后端保证它在当前 Graph 上是合法的。

推荐的命名前缀（前缀层面会尽量保持稳定）：

- `state.*` —— Graph 级 State 字段
  - 例：`state.user_input`、`state.greeting`、`state.counter`、`state.end_structured_output`。
- `nodes.<node_id>.*` —— 某个节点的结构化输出
  - 例：
    - `nodes.classification_agent.output_parsed.classification`
    - `nodes.transform_1.result.original_text`
-- `graph.*` —— Graph 级输入或元信息（如果暴露）
  - 例：`graph.input_as_text`（聊天类 Graph 的输入文本）。

前端使用建议：

- 把 `variable` 当成表达式片段：
- 在变量列表中展示时，可以按首段分组（例如 state/nodes/graph），方便用户浏览；
  - 用户选择某个变量时，直接把这串字符串插入到表达式/模板即可。
- UI 层面的类型/结构判断，优先使用 `kind` 和 `json_schema`：
  - `kind` 用来决定控件类型（文本、数字、布尔、对象、数组等）；
  - `json_schema` 用在需要显示/编辑结构化对象时（例如结构化输出编辑器、变量树）。

当使用单节点接口（`/graphs/vars/node`）时，返回的只有当前正在编辑节点的
`vars` 数组，适合在节点侧边栏做就地变量选择，而不需要前端自己在
`nodes[]` 里查找目标节点。

#### 3.3 检查两个节点之间的连接

当用户在画布上把一个节点拖线连到另一个节点（比如 Agent → MCP）时，编辑器通常希望：

- 知道这条边在「类型上」是否兼容；
- 如果不兼容，明确知道问题出在哪里（例如 MCP 需要 `repoName`，但上游没有提供）。

- 接口：`POST /api/v1/graphs/edges/inspect`
- 请求体：

  ```jsonc
  {
    "graph": { /* Graph 草稿 JSON */ },
    "edge": {
      "source_node_id": "Agent1",
      "target_node_id": "MCP1"
    }
  }
  ```

- 返回：`EdgeInspectionResult`

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
          "properties": { /* 来自 AgentConfig.output_format.schema 的用户自定义结构 */ }
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

语义示例：

- 对于 `builtin.llmagent`：
  - `source_output_schema` 至少包含：
    - `output_text: string`
    - `output_parsed: object`，内部结构来自 `AgentConfig.output_format.schema`（当 `type = "json"` 时）。
- 对于 `builtin.mcp`：
  - `target_input_schema` 来自所选 MCP tool 的输入 schema。

前端用途：

- 在「检查连接」的面板中展示 Source/Target 的 schema（类似你截图里的 UI）；
- 当连接非法时，用 `errors` 信息在边/节点上展示红色错误标记。

### 4. 校验 Graph 草稿

- 接口：`POST /api/v1/graphs/validate`
- 请求体：当前 Graph 草稿 JSON
- 返回：`ValidationResult`

示意结构：

```jsonc
{
  "valid": false,
  "errors": [
    { "field": "nodes[2].node_type", "message": "component builtin.foo not found in registry" },
    { "field": "start_node_id", "message": "start_node_id start does not exist" }
  ]
}
```

后端校验内容（`Validator`）包括但不限于：

- Graph 自身结构是否完整：version/name/nodes/edges/start_node_id 等；
- 所有 `node_type` 在组件注册表中是否存在；
- builtin 节点的特有约束：
  - `builtin.start` 是否至多一个；
  - 如果存在 `builtin.start`，`start_node_id` 是否指向它，且它是否只有一条出边；
- `state_variables` 是否命名唯一，`builtin.set_state` 的 assignments 是否只写入这些字段；
- 图的拓扑结构是否存在 unreachable 节点等问题。

前端用途：

- 在“保存 / 发布”之前给出统一的错误提示；
- 根据 `errors[field]` 对应到具体节点/字段，做高亮或错误图标。

### 5. 保存 / 发布 Graph

- 创建：
  - 接口：`POST /api/v1/graphs`
  - 请求体：最终确认后的 `Graph` JSON
  - 返回：`GraphResponse`，包含 `id/created_at/updated_at` 等。

- 更新：
  - 接口：`PUT /api/v1/graphs/{id}`
  - 请求体：更新后的 `Graph` JSON。

前端用途：

- 把当前画布对应的 Graph 保存为一个版本化对象（类似 OpenAI Agent Builder 的“Publish”）。

### 6. 执行 Graph

- 接口：`POST /api/v1/graphs/{id}/execute`  
  或：`POST /api/v1/graphs/{id}/execute/stream`（SSE）

- 请求体：`ExecutionRequest`

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

- 返回：`ExecutionResult`

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

前端用途：

- 在编辑器内点击“Evaluate / Run”时，发起执行；
- 根据返回的事件流展示执行轨迹、每个节点的输入输出等。

## 小结

- 前端只需要维护一份 Graph 草稿 JSON，并在需要时把它发送给后端：
  - `/components`：初始化组件目录；
  - `/graphs/schema`：全局 State 视图；
  - `/graphs/vars`：节点级变量视图；
  - `/graphs/validate`：整体校验；
  - `/graphs`：保存 / 发布；
  - `/graphs/{id}/execute`：执行。
- 所有 State / 变量 / 类型推导逻辑都由后端负责，前端不需要自己“跟着引擎实现重算一遍”。这样可以最大程度保持协议简单、实现解耦。 
