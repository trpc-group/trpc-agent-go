# 工具代码编排

`codeexecutor/codeact` 实现的是 CodeAct-style pattern：模型生成的代码只能经由受限工具网关编排 trpc tools。作为框架能力，对外更建议叫 **工具代码编排**。未知工具会被拒绝，工具输入与输出都在 Go 侧按声明的 JSON Schema 校验。它不替代稳定业务流程中的 Go 代码。

```text
LLM -> execute_tool_code -> Runtime -> guest call_tool(name, JSON args)
                                  -> Go Gateway -> trpc Tool
```

`Gateway` 是能力边界，负责 allowlist、schema 校验和真实 Go tool 调用。`Runtime` 是传输/执行边界：内置 `LocalRunner` 用 Python stdio guest 实现；远端 sandbox、microVM、容器服务或平台原生 callback 后端应直接实现 `codeact.Runtime`，并把 sandbox 产生的 `ToolCall` 路由给传入的 `ToolCallHandler`。

## 安全边界

`LocalRunner` 仅用于开发或已经隔离的容器/VM；它不是安全 sandbox。生产应用应基于已有远端 sandbox、microVM 或容器服务实现 `codeact.Runtime`，并由应用侧配置隔离、资源和依赖策略。

## 为什么不让生成代码直接调用 HTTP API？

工具代码编排把动态控制流和系统能力访问分开：生成代码适合循环、分支和数据转换，业务 API 访问仍应是宿主侧工具。这样认证、授权、重试与幂等策略、API 版本适配、审计和限流仍由应用代码掌控，而不是落在模型生成的代码中。

确实需要 HTTP 能力时，应把具体业务操作做成宿主 adapter tool；如果需要一组较宽但明确受限的 HTTP 能力，可暴露受约束的 `http_request` 类工具，由宿主控制允许的域名和方法、凭证以及响应大小限制。不能把 `LocalRunner` 的通用 Python 环境当作安全控制；生产 runtime 必须自行落实网络和进程策略。

| 需求 | 推荐实现 |
| --- | --- |
| 稳定、确定性的业务流程 | Go 应用代码 |
| 在已批准工具间做动态分支或循环 | `execute_tool_code` |
| 受限的外部 HTTP 集成 | 宿主 adapter 或受约束 HTTP tool |
| 不可信代码或无边界外部访问 | 隔离 runtime 加明确的应用策略 |

工具 A/B 的小型结构化数据经 JSON 传递；中间结果留在 guest 代码中，只有最终 `value` 和捕获的 `stdout` 会作为外层工具结果返回。应返回紧凑的聚合结果、标识符或 artifact/workspace 引用，而不是把大块原始数据交回模型。大数据应返回 artifact/workspace 引用，由 guest 在挂载 workspace 中处理。业务语义不匹配必须用显式转换代码或宿主 adapter tool 处理，CodeAct 不会猜测字段或单位含义。

要运行 Agent 的多工具编排示例（产品搜索 → 库存筛选 → 创建报价），设置
`OPENAI_API_KEY`（以及可选的 `OPENAI_BASE_URL`）后执行：

```bash
cd examples && go run ./codeact -model gpt-5
```

该示例只把 `execute_tool_code` 暴露给模型；`search_catalog`、`get_inventory` 和
`create_quote` 仅能由 guest Python 经 allowlist 调用。它不设置 `GenerationConfig`，
模型配置完全由 `openai.New` 和环境变量处理。

需要给 Agent 暴露工具时，调用 `tool/toolcode.NewTool(runtime, managedTools)`，将返回的 `execute_tool_code` 注册给 Agent。只有 `managedTools` 能被 guest 中的 `await call_tool(...)` 调用。

第一版中，managed tool 是同步的直接宿主能力调用：内置 Python guest 任一时刻只有一个调用在执行，`await` 是 guest API 的调用形式，但 `asyncio.gather(...)` 不会产生并行的宿主工具调用。宿主工具失败会在 Python 中表现为 `RuntimeError`，生成代码可用 `try`/`except` 处理。它不会重放 Agent 的 callback、retry 或内层 tracing 生命周期，也不能在执行中暂停以等待交互式审批。不要把需要“审批后恢复”流程的工具加入 `managedTools`；授权应在业务工具自身或应用定义的 adapter tool 中实现。

## 面向模型的工具说明

默认的 `execute_tool_code` 声明会告诉模型：能在一次调用完成流程时优先一次完成；只能使用 `await call_tool(name, **json_arguments)`；按顺序调用工具；并只返回完成任务所需的紧凑 JSON 兼容值。它还会动态列出每个 managed tool 的名称、描述、输入 JSON Schema 和输出 JSON Schema。中间的 managed tool 结果留在 guest 代码中，只有 `execute_tool_code` 最终的 `value` 和捕获的 `stdout` 会作为外层工具结果返回；不要打印或返回模型不需要的大型原始结果。这里的说明只是模型引导，不能替代前述 runtime 安全边界。

managed tool 的描述应写成业务契约：说明操作含义、前置条件、单位、枚举含义和结果语义。除非模型必须依此选择操作，否则不要塞入 header、SDK 等传输细节。两个工具之间若需要领域特定的语义映射，应使用宿主 adapter tool，而不要让通用字段转换去猜测含义。

`toolcode.WithDescription` 会替换默认描述。使用自定义描述时，应保留“只能经由 `call_tool` 访问能力”和 JSON 入参与结果约束。

## 与其他执行工具的边界

`execute_tool_code` 不是 `workspace_exec`、`workspace_write_stdin` 或
`workspace_kill_session` 的替代品：

- `workspace_exec` 系列用于在共享 workspace 里运行 shell command、脚本、
  CLI 或长任务，输入输出主要是 terminal 文本、文件、artifact、exit code。
- `execute_tool_code` 用于让生成的 Python 胶水代码通过 `call_tool(...)`
  编排一组显式 allowlisted 的普通 Go tools，适合分支、循环、JSON 转换和
  多工具结果聚合。
- `tool/codeexec` 的 `execute_code` 用于普通代码执行、计算、数据分析或逻辑
  验证；它不会把 Python 调用桥接回 Go tools。

因此默认工具名刻意不叫 `execute_code`：`tool/codeexec` 已经把
`execute_code` 用于普通代码执行。如果一个应用只暴露工具编排版本，可以用
`toolcode.WithName("execute_code")` 覆盖；但不要在同一个 Agent 上注册两个
同名工具。

## managed tools 选择

`managedTools` 是应用显式配置的独立 capability registry，不会从 Agent 已注册的
tools 自动推导。应优先放业务工具、数据工具和宿主侧 adapter tool。通常应排除：

- `workspace_exec` / `workspace_write_stdin` / `workspace_kill_session`
- `workspace_save_artifact`
- `execute_code` / `execute_tool_code`
- `skill_run` / `skill_exec` / `skill_write_stdin` / `skill_poll_session` /
  `skill_kill_session`
- `transfer_to_agent` / `await_user_reply`

对于同一个业务操作，通常应只选择一种模型侧入口：要么作为 Agent 的直接工具暴露，要么仅加入 `execute_tool_code` 的 managed registry。应用当然可以有意同时注册两者，但这会让模型对同一个操作看到两条执行路径，弱化何时应该使用代码编排的引导。无论如何，managed registry 仍是 guest 代码的实际能力边界。

这些工具通常不应加入 registry：执行类工具会形成递归或异质执行链；
`transfer_to_agent` 和 `await_user_reply` 会修改外层 Invocation 的控制状态；
`AgentTool` / `DynamicAgentTool` 会启动子 Agent 并带来独立的历史、流式事件和
成本边界。框架不按名称或类型替应用判断；应用应只把明确需要编排的普通能力工具
传给 `tool/toolcode.NewTool`。
