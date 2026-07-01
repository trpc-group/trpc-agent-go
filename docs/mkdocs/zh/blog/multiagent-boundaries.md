# 把工作放到合适边界：tRPC-Agent-Go Multi-Agent 设计与选型

> Multi-Agent 的问题，通常不是“要不要多几个角色”，而是“这段工作为什么不适合继续留在当前 Agent 的运行边界里”。上下文、能力、控制权、生命周期、输出观测和运行环境不同，应该选择的 tRPC-Agent-Go 机制也不同。
>
> [tRPC-Agent-Go](https://github.com/trpc-group/trpc-agent-go/) 是面向 Go 语言的自主式多 Agent 框架，提供工具调用、会话与记忆管理、制品管理、多 Agent 协作、图编排、知识库、可观测等能力。tRPC-Agent-Go 的成长离不开社区支持，欢迎 Star 项目。

业务接入 Multi-Agent 能力时，真正卡住的往往不是概念，而是几个具体判断：

- 分析类 Agent 把一段复杂检索或统计交给专家以后，主 Agent 是否还要拿回结果继续协调？
- 分流类 Agent 把用户交给专家以后，下一轮用户补充信息时，应该继续进入专家，还是重新从入口开始？
- 生成类 Agent 需要反复修改同一份产物时，子 Agent 是否应该保留自己的历史轨道？
- 支持、审核、咨询这类接力场景里，handoff 时应该传完整历史，还是只传目标 Agent 需要的输入？
- 一个任务每次都需要不同工具、技能或约束时，是给主 Agent 挂更多工具，还是按次创建更窄的执行单元？
- 外层流程已经用 Graph 编排时，节点内部是否还需要委派给别的 Agent？

这些问题看起来分散，本质上都在问同一件事：这段工作需要换成怎样的运行边界。

本文不把 tRPC-Agent-Go 的 Multi-Agent 能力写成 API 摘要。`AgentTool`、Dynamic AgentTool、`transfer_to_agent`、Team / Swarm、Chain / Parallel / Cycle / Graph、TaskRun、A2A / Remote Agent、Explorer 不是同一种能力的不同名字；它们改变的是不同边界。

## 1. 先从单 Agent 开始

多数 Agent 系统的起点仍然应该是单 Agent：一个主 Agent，配一组工具、一段 instruction、必要的知识源和会话管理。只要这个结构能稳定完成任务，就不需要为了“看起来像团队”而拆成多个 Agent。

Multi-Agent 真正变成工程问题，通常是因为单 Agent 的某条边界开始失效：

- 上下文太脏，搜索、读取、试错、工具结果不断滚进后续轮次。
- 能力面太宽，模型面对过多 tools、skills、knowledge，开始误选、漏选或幻觉能力。
- 控制权需要交给专家，当前轮不应该再由入口 Agent 继续回答。
- 生命周期超出一次调用，任务需要等待、查询、取消、读取过程或恢复。
- 输出和观测需要分层，用户不一定要看到过程，父 Agent 却必须拿到可决策结果。
- 运行环境本来就在另一套模型、executor、服务身份、权限或远程系统里。

因此，选型时不要先问“要几个 Agent”，而要先问“为什么这段工作不能继续放在当前 Agent 里”。

## 2. Agent 边界到底是什么

在 tRPC-Agent-Go 里，Agent 更适合被看成一层运行边界，而不只是一个 prompt 对象。它决定一段工作由谁执行，本轮请求装配哪些 instruction、messages、tools、skills、runtime state、callbacks，以及事件如何写回 session 和 trace。

用模型调用的心智看，模型真正看到的是本轮 `messages` 和 `tools`。`session`、`memory`、runtime state、artifact、trace 都是框架侧事实；除非它们被投影进本轮 `messages`，或者被声明进本轮 `tools`，否则不会直接影响这次模型调用。

所谓“父 Agent”“子 Agent”“当前响应归属谁”“能不能调用某个工具”，不是模型自己记住了身份，而是框架在这一轮运行里重新构造出来的上下文事实。

所以，Multi-Agent 设计不是角色设计，而是边界设计。换 Agent 时真正变化的是：可见信息、可用能力、控制权、状态延续、输出去向和运行责任。

## 3. 六类边界问题

下面这张表可以作为第一轮判断。一个需求经常同时碰到两三项，但先把主问题问准，后面的 API 才不容易选错。

| 边界 | 业务信号 | 先问什么 | 不要误判 |
| --- | --- | --- | --- |
| 上下文 | 检索、读取、试错和工具结果很多，后续对话背着大量低信息密度过程 | 父 Agent 需要完整过程，还是结论、证据和必要引用 | 隔离不等于让父 Agent 什么都看不见 |
| 能力 | tools、skills、knowledge 太多，模型开始误选、漏选或幻觉能力 | 是同一 Agent 内过滤能力，还是建立新的能力边界 | 工具可见性不是权限边界 |
| 控制权 | 当前轮不该只拿回一个结果，而应交给专家继续处理 | 当前轮由谁接管，完成后是否回到主 Agent，下一轮入口是谁 | 调用式委派不等于控制权接管 |
| 生命周期 | 工作离开当前调用后还要等待、查询、取消或读取过程 | 它是否需要 run id、状态机、持久化和结果查询 | 后台工作不是更长的一次 tool call |
| 输出和观测 | 过程要被展示或审计，但不一定进入后续 prompt | 父 Agent 收到什么，用户看到什么，什么进入下一轮 `messages` | UI 隐藏不等于信息流可以断掉 |
| 运行环境 | 任务必须在另一套模型、executor、服务身份或远程系统里运行 | 边界来自能力差异，还是部署、权限、团队和状态所有权 | 不要只因为 prompt 像“专家”就拆服务 |

## 4. 用边界读 tRPC-Agent-Go 机制

下面这张表不是 API 清单，而是默认边界索引：请求发给谁，带什么上下文，结果回谁，当前轮由谁继续处理。默认边界和业务预期对不上时，再看 option 或外部配套。

| 机制 | 默认边界 | 适合 | 何时加 option 或配套 |
| --- | --- | --- | --- |
| 单 LLMAgent | 一个 Agent 用自己的 instruction 和工具面推进 | 工具面可控、链路不复杂 | 工具面过大、上下文污染、权限、模型或环境不同 |
| AgentTool | 固定子 Agent 作为同步 tool；默认隔离历史，结果以 tool result 回父 Agent | 查一下、审一下、分析一段内容 | 继承父历史、稳定子历史、裁剪结果、转发内部 stream、跳过外层总结 |
| Dynamic AgentTool | 一个 `dynamic_agent` 入口；按次创建短生命周期子 Agent；默认隔离，不保留稳定子历史 | 子任务需要临时收窄 instruction、tools 或 skills | 固定能力上限、能力 provider、字段暴露、超时、结果模式 |
| `transfer_to_agent` | `WithSubAgents` 暴露 transfer；目标 Agent 接管当前 invocation；默认原 Agent 结束当前轮 | 当前轮应该由专家继续回答 | 默认交接消息、是否结束、消息投影、跨轮 owner |
| Coordinator Team | 成员被包装成 member tools；结果回 coordinator | 协调者调用多个成员完成当前轮 | 成员历史、内部事件、成员正文展示、coordinator 是否总结 |
| Swarm | entry member 开始；成员间 transfer 接力；默认下一轮仍从 entry 开始 | 当前轮内多专家接力 | 跨轮接管、独立成员历史、自定义 handoff 输入 |
| Chain / Parallel / Cycle | 代码结构承载顺序、并行或循环执行 | 步骤明确、可重复、可评估 | 子 Agent 间输入输出、终止条件、错误处理 |
| Graph | 图结构承载流程、路由、状态和节点执行 | 固定或半固定流程，且需要状态、条件路由、检查点或恢复 | 节点内部是否还需要 AgentTool / transfer，Graph 状态如何传给节点 |
| TaskRun | 控制工具启动 worker；worker 有 run id 和 child session；父 Agent 用 list / get / wait / cancel 管理 | 长任务、可等待、可取消、可查结果 | 外部存储、队列、租约、controller、恢复策略 |
| A2A / Remote Agent | 远程 Agent 以本地代理形态接入；不自动继承本地 session、memory、runtime state | 复用远程服务里的 Agent 或跨团队部署 | 身份、会话映射、权限、memory、结果契约、可观测性 |
| Explorer | 内置探索 Agent；默认继承直接父 invocation 的用户工具、知识、skills 等能力面 | 只读探索、信息收集、定位上下文 | read-only 是 prompt 软约束；硬隔离要收窄工具面或加权限策略 |

Graph 这一行尤其容易误读。Graph 管的是外层流程、状态和路由；节点内部是否能继续委派，要看节点实际运行的 Agent 配了什么能力。`graphagent.WithSubAgents` 让图里的 `AddAgentNode(id)` 能找到对应 Agent，不会自动给每个 LLM 节点注入委派工具。固定专家步骤可以直接建成 Agent node；如果某个 LLM 节点内部还要自主委派，需要给该节点 Agent 配 AgentTool 或 `WithSubAgents`。

## 5. 先分清三种回流路径

让另一个 Agent 参与时，先不要急着看 option。先问请求怎么出去、结果怎么回来、当前轮由谁继续处理。tRPC-Agent-Go 里常见路径可以先压成三类。

| 路径 | 包含机制 | 默认心智 |
| --- | --- | --- |
| 调用式委派 | AgentTool、Dynamic AgentTool、Coordinator member tool、包成 AgentTool 的 Remote Agent | 父 Agent 发出一个 request，目标做完后以 tool result 回到父 Agent |
| 接力式转移 | `transfer_to_agent`、Swarm handoff、作为 sub-agent 的 Remote Agent | 目标 Agent 接管当前 invocation，输出不是先回原 Agent 的 tool result |
| 可管理 run | TaskRun | 父 Agent 启动 worker，拿回 run id，再通过 list / get / wait / cancel 管理状态或结果 |

### 5.1 AgentTool：调用并返回

AgentTool 适合“我需要一个专家结果，然后主 Agent 继续判断”。父 Agent 模型看到的是一个普通 tool；tool 内部运行子 Agent；子 Agent 的最终结果以 tool result 形式回到父 Agent，父 Agent 可以继续下一次模型调用。

这里的关键不是子 Agent 名字，而是结果契约。父 Agent 不应该收到一大段原始过程，也不应该只收到“已完成”。更稳定的返回通常包含结论、证据、关键动作、不确定性、下一步和 artifact 引用。

常见 option 要按出口理解：

- `WithHistoryScope(agenttool.HistoryScopeParentBranch)`：子 Agent 可以继承父 branch 历史；默认 `NewTool` 是 isolated。
- `WithPersistentHistory*`：固定 AgentTool 使用稳定 child history；适合同一个子 Agent 多次修改同一份产物或延续同一条工作轨道。
- `WithResponseMode(agenttool.ResponseModeFinalOnly)`：tool result 只取子 Agent 最后一条完整 assistant message。
- `WithStreamInner(true)`：内部事件向父流程转发，影响 UI / event stream，不等于所有过程进入父 Agent prompt。
- `WithSkipSummarization(true)`：tool 返回后跳过外层总结型 LLM 调用，适合 passthrough 风格，不适合需要 coordinator 综合多个结果的场景。

### 5.2 Dynamic AgentTool：按次装配能力

Dynamic AgentTool 适合“每次子任务都要临时收窄 instruction、tools 或 skills”。它暴露一个默认名为 `dynamic_agent` 的入口，运行时按本次 tool call 创建短生命周期子 Agent。

“动态”不是让模型任意创建 Agent。代码仍然定义能力上限：可以来自父 invocation 的有效能力面，也可以通过 `WithCapabilityTools`、`WithCapabilityProvider`、`WithCapabilitySurfaceProvider`、`WithCapabilitySkills` 等方式指定。模型只能在这个边界内选择子集，不能任意选择模型、executor 或远程目标。

它解决的是能力面问题，不解决长期记忆问题。`NewDynamicTool` 设计上是短生命周期，`WithPersistentHistory*` 会被忽略。如果需求是“同一个子 Agent 下次继续改同一份产物”，优先考虑固定 AgentTool 的 persistent history，或者把产物状态外置成 artifact / 数据库对象。

### 5.3 transfer 与 Swarm：谁接管当前轮

`transfer_to_agent` 回答的是“当前 invocation 应该由谁继续处理”。当 LLMAgent 配置了 `WithSubAgents` 后，框架会暴露 `transfer_to_agent`。目标 Agent 被启动后切换到自己的 instruction 和工具面继续当前轮输出；默认情况下，原 Agent 在 transfer 后结束当前轮。

这和 AgentTool 是不同心智：

- AgentTool 是“专家做完，把结果作为 tool result 还给我”。
- transfer 是“这轮交给专家继续处理”。

Swarm 把这种 handoff 封装成 Team 语义。默认每个新用户消息从 entry member 开始；如果要让上一次 handoff 的目标继续拥有后续用户轮次，需要启用 `team.WithCrossRequestTransfer(true)`，并复用同一个 session。若成员需要私有历史，可以配 `team.WithSwarmIndependentAgents()`；若 handoff 输入不应该只是 `transfer_to_agent` 的 `message` 字段，可以配 `team.WithSwarmHandoffInputBuilder`。

因此，“transfer 后下一轮还在不在子 Agent”不能只看 `sessionID`。`sessionID` 说明事件落在哪个会话容器里；active member 决定下一轮入口；message filter 和 projector 决定后续模型请求实际看到什么。

### 5.4 TaskRun：变成可管理的 run

TaskRun 适合“这段工作不能被当前 invocation 包住”。比如它可能跑得久，父 Agent 不应该一直等；后续还需要查询状态、等待结果、取消任务或读取 transcript。

TaskRun 的核心不是“更久的 AgentTool”，而是 run contract。`start_task_run` 创建带 run id 的工作，通常在 child session 里执行；父 Agent 后续通过 `list_task_runs`、`get_task_run`、`wait_task_run`、`cancel_task_run` 管理它。配置 transcript 工具后，还可以读取子 session 片段。

`sync` 模式只是让控制工具等待子 run 进入终态；它仍然会创建 run id，并走同一套生命周期。生产级 TaskRun 还需要外部存储、队列、租约、取消、重试和恢复策略。框架的 in-process 实现适合本地、测试和单进程适配层；多节点部署应实现自己的 `taskrun.Controller`。

### 5.5 Remote Agent：运行所有权在另一侧

A2AAgent 是远程 Agent 的本地代理，它实现 `agent.Agent`。这意味着它可以像普通 Agent 一样被 Runner 执行，也可以放进其他协作模式里。

但 Remote Agent 只说明运行环境在另一侧，不自动决定回流路径。把 A2AAgent 包成 AgentTool，它就是调用式委派；把它作为 sub-agent，它就可以参与 transfer；把它作为 Graph 节点目标，它就是流程节点。

远程边界需要额外设计：身份、会话映射、权限、memory、runtime state、artifact、错误语义、trace 关联和结果契约都不会因为“它也是 Agent”而自动一致。

## 6. 能力边界：过滤、固定、按次装配

能力面变大时，常见解法有三层。

**同一 Agent 内过滤。** `agent.WithToolFilter(...)` 或 `llmagent.WithToolFilter(...)` 适合用户、会话、租户、模式等低频稳定过滤。它成本低，但没有建立新的 Agent 边界。工具可见性也不是权限边界；真正的执行权限仍要在工具、executor 和 permission policy 里兜住。

**固定能力边界。** AgentTool、`WithSubAgents`、Coordinator member、Swarm member、TaskRun worker、A2AAgent 都可以把一组稳定的 instruction、tools、skills、model 或 executor 封成更小的工作单元。主 Agent 不必直接背所有工具，只面对稳定的能力入口。

**按次装配。** Dynamic AgentTool 适合每次子任务都要临时收窄能力的场景。它应该由代码定义能力池，再让模型在本次 invocation 内选择子集。不要把它当成长期子 Agent，也不要把高频变化的工具面塞进主 Agent 的每次模型请求里；频繁改变 tool schema 和相关 instruction 往往会降低 prompt cache 命中，也会让行为更难复现。

一个实用判断是：低频、稳定的收窄，先用过滤；稳定分工，用固定子 Agent 或固定目标 Agent；按次变化，再考虑 Dynamic AgentTool。

## 7. 输出和观测：四个出口不要混在一起

引入子 Agent 后，最容易混在一起的是四个出口：

| 出口 | 需要回答的问题 |
| --- | --- |
| 父 Agent 收到什么 | 它后续决策需要哪些结论、证据、假设、下一步和 artifact 引用 |
| 用户看到什么 | 是否展示内部 stream、进度、成员输出或最终回答 |
| trace / artifact 留下什么 | 哪些过程要用于调试、审计、复现或大结果存储 |
| 后续 `messages` 投影什么 | 下一次模型请求实际会带哪些历史、tool result 或 foreign-agent context |

UI 不展示内部过程，不代表父 Agent 不需要结果；trace 里有完整过程，也不代表都要塞回 prompt；父 Agent 跳过总结，不代表 tool result 的内容契约可以省略。

一个稳妥的子 Agent 返回契约通常包括：

- 结论：任务完成到什么程度，判断是什么。
- 证据：依据来自哪里，是否可追溯。
- 假设和不确定性：哪些前提没有验证，哪里可能出错。
- 关键动作：做过哪些重要查询、检查或修改，不必重放完整过程。
- 下一步：应该继续调用工具、追问用户，还是可以最终回答。
- artifact 引用：大结果、文件、报告、任务状态不塞进 prompt，只传可定位引用。

## 8. 不要急着拆 Agent 的场景

选型不只要判断什么时候用，也要判断什么时候先不用。

| 场景 | 更稳的选择 |
| --- | --- |
| 只是想调整 instruction 或工具描述 | 先改单 Agent 的 instruction、tool description 或 planner |
| 输入输出确定、单次调用就能完成 | 直接封装 tool，不必只为形式再包一层 Agent |
| 流程固定，比如规划 -> 执行 -> 审查 | Chain、Cycle、Graph 往往比模型自主协作更清楚 |
| 父 Agent 需要关键过程才能判断 | 不要过度隔离，至少返回关键证据、过程摘要或 artifact 引用 |
| 只是工具可见性不同 | 低频变化用 tool filter；执行前兜底用 permission policy |
| 没有 trace、UI 和状态管理支撑 | 不要轻易引入复杂异步或长期子 Agent |

并发和异步当然可能缩短端到端耗时，但效率不应该是默认拆 Agent 的第一叙事。很多时候，用户更需要的是效果稳定、上下文干净、工具选择可靠，而不是看起来并行了多少个角色。

## 结语

调用式委派、控制权接管、跨轮入口、run 生命周期、远程运行所有权、UI / trace 展示和 `messages` 投影，是不同边界。

所以，不要问“哪个 API 更像 Multi-Agent”，而要问：这段工作需要换哪条边界。主上下文太脏，就处理上下文；能力面太宽，就收窄能力；当前轮需要目标 Agent 接管，才讨论 transfer 或 Swarm；任务要离开当前调用，才讨论 TaskRun；如果只是固定流程，就优先用代码、Chain、Cycle 或 Graph 承载骨架。

边界判断清楚以后，API 名字反而不容易选错。

## 参考资料

- [tRPC-Agent-Go GitHub](https://github.com/trpc-group/trpc-agent-go)
- [Multi-Agent 文档](../multiagent.md)
- [Team 文档](../team.md)
- [TaskRun 文档](../taskrun.md)
- [Graph 文档](../graph.md)
- [A2A 文档](../a2a.md)
- [Tool 文档](../tool.md)
