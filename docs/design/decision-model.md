# tRPC-Agent-Go 决策模型集成设计文档（v2，社区对齐版）

> **v2 修订说明（2026-11-01）**：本版全面重写。v1 假设"决策模型集成层"是空白，
> 自建了 `decision` 抽象层 + `decision/jev` + `decision/mock` + `graph.AddDecisionNode`。
> 重写前的事实核查（附录 B）发现社区路线已与此冲突：
>
> - 提案 issue #2632 明确 Non-Goal："Adding a new generic decision-model
>   abstraction"（不做通用决策模型抽象层），并确定用既有槽位集成；
> - `internal/systemone` 协议客户端已由 PR #2652 合入（wangxuw，2026-10-10）；
> - 工具审批 reviewer（v1 三期的风险门控场景）正由 PR #2664 在审；
> - 评估器由 PR #2638 占位（liuzengh）。
>
> 结论：v1 的一期（抽象层 + jev 客户端 + mock + AddDecisionNode）与社区路线
> 重叠或被否决，本地实现已撤销。v2 重新分期：
>
> ```text
> 第一期：社区基础（已有实现 + 在途 PR + 规则约束）—— 不写代码，只锚定地基
> 第二期：toolfilter / skillfilter / modelrouter —— 我们的核心贡献（无人认领的空白）
> 第三期：出口质检 + 提案剩余示例 + 观测收尾
> ```
>
> v1 中仍然有效的资产：附录 A 的 Octop 生产调研（二期 toolfilter 的全部依据）、
> 执行流程与召回层设计（骨架换为 systemone 客户端后逻辑不变）、失败语义纪律
> （筛选 fail-open / 安全 fail-closed）。均已并入 v2。

---

## 一、第一期：社区基础（现状 + 规则，不写代码）

第一期在 v2 中不再是交付物，而是**必须锚定的地基**：了解社区已有什么、
正在交付什么、遵守哪些约束。本期的行动项只有两个：在 issue #2632 下提出
二期方向并获得认可（见 2.0），以及可选地认领提案内无人认领的两个示例
（见 3.2）。

### 1.1 提案 #2632 与社区路线图

[issue #2632](https://github.com/trpc-group/trpc-agent-go/issues/2632)
（*proposal: support TypeSafe Jev for structured decisions*，作者 wangxuw，
2026-09-21 开启，状态 open）是社区 Jev 集成的总路线图。其 scope：

- 带重试的 HTTP 客户端（已在 PR #2652 落地，注：落地版无重试，重试归上层）；
- 三种问题类型，保留概率、置信度、用量与解析出的模型版本；
- "把 Jev 决策包装成 callable tool"的示例（**无人认领**）；
- "Graph 按决策 + 可配置置信度阈值路由"的示例（**无人认领**）；
- Jev 版 `review.Reviewer` 工具审批实现（PR #2664 在审）；
- 协议与集成测试 + 双语文档。

提案原文的核心句（集成哲学，v2 全文遵守）：

> Existing `function.NewFunctionTool`, `StateGraph.AddNode`, conditional
> edges, and the approval plugin provide integration points **without
> requiring a new Agent execution loop**.

维护者互动（详见附录 B）：IRONICBo 给了审批数据集建议（绑定参数 + 状态
指纹、fail-closed、mutation 一律拦截）；liuzengh 认领评估方向。**没有任何
人讨论或否决过抽象层问题——Non-Goal 是提案作者自划范围，但两个已落地 PR
均按"具体实现填既有槽位"路线执行，形成既成方向。**

### 1.2 `internal/systemone` 客户端（PR #2652，已合入）

位置：`internal/systemone/`（16 个 Go 文件 + 测试 + `README.md` + `DESIGN.md`，
引用官方 OpenAPI v0.2.0 与 Laya serve.py 契约）。**全仓零使用方（等集成层
接入），仅根 module 内的包可导入**（Go internal 可见性规则；examples 与
兄弟 module 被 CI 明确禁止）。

消费视角的契约速览（以包内文档为准，此处仅摘二期会用到的部分）：

```go
// 构造（唯一入口；默认 30s 超时）
client, err := systemone.NewClient(baseURL,
    systemone.WithAPIKey(key),            // 必须显式传入
    systemone.WithDefaultModel("jev-2025-10-10"), // 可选，钉版本
    systemone.WithHTTPClient(hc),         // 测试注入点
    systemone.WithTimeout(30*time.Second),
)

// 一次决策请求
resp, err := client.SystemOne(ctx, &systemone.Request{
    State:     state,     // string | map[string]any | []any（会做 JSON 形态校验）
    Questions: questions, // map[string]systemone.Question，非空
})

// 三种问题（Go 命名刻意用 Binary，协议字段仍是 "noul"）
systemone.BinaryQuestion{Instructions: "text", Criteria: &systemone.BinaryCriteria{True: "...", False: "..."}}
systemone.ChoiceQuestion{Instructions: "text", Options: []systemone.ChoiceOption{{Name, Description}}} // 有序、唯一
systemone.ScoreQuestion{Instructions: "text", Criteria: []string{"level0", "level1"}}                    // >= 2 级

// 强类型答案访问器（id 不存在 / 类型不符 / 校验失败 → error）
ans, err := resp.Binary(questionID)  // ans.Value float64
ans, err := resp.Choice(questionID)  // ans.Selected / ans.Probabilities / ans.Confidence
ans, err := resp.Score(questionID)   // ans.Score / ans.Levels []ScoreLevel{Description, Probability} / ans.Confidence
resp.Usage()                          // InputTokens / OutputTokens / Truncated（截断证据）
```

**严格校验语义**（对二期有直接影响）：答案必须匹配全部请求问题（数量、
类型、choice 选项集、score 层级数）；概率必须 ∈[0,1] 且求和≈1（容 4 位小数
舍入）；score 必须在 rubric 范围内；文本必须非空白 UTF-8。**响应不完整 =
整个调用返回 error**——v1 设计里"缺分（部分回复）→ 保持原目录"的分支在
此客户端下不存在：要么全部答案齐、要么整调用失败走 fail-open。指令与
criteria 是纯文本（string），即使 provider 支持富 JSON 输入也不使用。

### 1.3 PR #2664（在审）：System One 审批 reviewer

`plugin/guardrail/approval: add system one reviewer and final permission checks`
（wangxuw，2026-10-12 开启，27+ 文件）。它确立了**集成层构造模式**（v2 二期
直接沿用）：

- 新增公开包 `plugin/guardrail/approval/review/systemone`，内部 import
  `internal/systemone`，对外实现既有 `review.Reviewer` 接口
  （`Request{Action, Transcript} → Decision{Approved, RiskScore, RiskLevel, Reason}`）；
- **公开构造器形态**：`New(baseURL string, opts ...Option)`——baseURL 显式
  传入，`WithAPIKey/WithModel/WithTimeout/WithHTTPClient` 透传给内部客户端，
  外加领域选项（`WithRiskThreshold` 默认 50、`WithMinConfidence` 默认 0.8）；
  **不读环境变量**；
- 决策策略在本地代码：一次请求问 authorization（Choice：supported /
  contradicted / insufficient）+ risk（Score 五级 rubric）两问，批准 =
  supported 且 risk < 阈值 且置信度 ≥ 下限；`Reason` 是本地策略拼接文本，
  不是生成文本；
- **fail-closed**（安全门控语义）：无用户意图直接拒（不调服务器）、截断
  证据拒、任何错误拒；
- 附带 final permission checks（审批后参数定稿时的终点复查）、跨 agent
  委托/steering/taskrun 的权限继承、示例与文档。

它同时覆盖了 v1 三期的"风险门控"场景，**v2 不再做任何 guardrail 方向的工作**。

### 1.4 PR #2638（占位）：Jev 原子 rubric 评估器

liuzengh 在 #2632 下的评估方向跟进：Jev 实现既有
`evaluation/evaluator.Evaluator` 接口并注册，原子 rubric 评估为首个里程碑，
与 LLM judge 对照报告。目前是空占位 PR。**v2 不做评估方向的工作**，但二期
工具预筛与其"合批一次请求、精确检查在代码、API 错误与负面判断分开"的
设计约束精神一致。

### 1.5 LangChain typesafe 官方生态参照

`langchain-typesafe` 0.0.1a3（本地包核实，附录 B.4）的官方集成形态：

| 官方组件 | 对应物 |
| --- | --- |
| `TypeSafeClassifier`（Runnable） | `internal/systemone` 客户端（功能对应） |
| `ModelRouterMiddleware`（模型请求钩子选模型） | **二期 modelrouter 的直接先例** |
| `AutoModeMiddleware`（工具调用钩子拦风险） | PR #2664 的对应物 |
| graph 场景 | 官方示例让用户**自己写普通 node** 调 classifier + conditional edges——无框架 API |

关键情报：**LangChain 官方没有工具/技能预筛**（toolfilter 在其生态也是
空白）——二期是 LangChain 官方都没做的差异化方向（Octop 有生产实践，
附录 A）。

### 1.6 集成规则与红线（v2 全程遵守的约束清单）

1. **不引入通用决策抽象**（Non-Goal）：不新增 `Decider` 类接口、不建
   `decision/` 包；每个集成是"具体实现填既有槽位"。
2. **internal 可见性**：`internal/systemone` 的类型**不得出现在任何公开
   API 签名里**；集成包一律在根 module 内，构造器形态 = `New(baseURL,
   opts ...Option)`（PR #2664 模式），内部自建客户端。
3. **不读环境变量**：显式配置，用户自己 `os.Getenv` 后传入。
4. **测试注入走 HTTP 层**：`WithHTTPClient` + `httptest` 脚本化响应（仓库
   惯例，systemone 自身与 PR #2664 测试均如此）；不建 mock 接口层。
5. **失败方向纪律**：筛选/优化类（toolfilter、skillfilter、modelrouter）
   **fail-open**；安全类（审批、门控）fail-closed（PR #2664 已做）。
   两个默认值方向绝不写反。
6. **响应严格校验语义**：接受"部分回复 = 整调用 error"，由 fail-open 兜底；
   不做多字段名容错（`noul/probability/p_yes/yes` 容错是杂牌方言，上游已
   拒绝；协议收敛的供应商走 OpenRouter/Laya 兼容端点，客户端天然支持）。
7. **核心包零感知**：`graph`/`agent`/`model`/`plugin` 框架层不 import
   `internal/systemone`；只有 `plugin/<name>` 集成包与示例碰它。
8. **既有槽位优先**：BeforeModel 回调、`llmagent.WithSkillFilter`、
   `agent.ModelSelector`、`review.Reviewer`、`evaluator.Evaluator`——全部
   已存在，只做实现，不改槽位。
9. **流程规则**：新能力（二期三个集成）动工前先在 issue #2632 评论提出，
   获得提案 owner / 维护者认可；一期一个 commit，PR 总量小步提交。
10. **命名**：Go 侧用 `Binary` 而非 provider 术语 `Noul`（上游命名约定，
    见 `internal/systemone` DESIGN.md）。

### 1.7 剩余可认领的空位

提案 scope 内无人认领的交付物（低成本、纯示例，受 1.6 规则 2 约束需要
一个公开集成包作桥——正是 PR #2664 为审批场景建 `review/systemone` 的
同款做法）：

1. "Jev 决策包装成 callable tool" 示例（`function.NewFunctionTool` 槽位）；
2. "Graph 按决策 + 置信度阈值路由" 示例（普通 `AddNode` + NodeFunc +
   conditional edges，无新 API）。

v2 将其归入三期（3.2）作为融入社区的低成本贡献。

---

## 二、第二期：toolfilter / skillfilter / modelrouter（核心贡献）

### 2.0 定位与前置流程

二期是 v2 的主体，也是社区真正的空白（提案无此步骤、LangChain 官方无
对应物、Octop 有生产实践佐证价值）。三个集成全部是"既有槽位上的具体
实现"，零框架改动、零新抽象，符合 1.6 全部约束：

| 集成 | 槽位（已存在，不改） | 形态 |
| --- | --- | --- |
| toolfilter | `model.BeforeModelCallbackStructured`（改写 `Request.Tools`，官方支持 "can be modified"） | `plugin/toolfilter`（根 module 包） |
| skillfilter | `llmagent.WithSkillFilter`（`skill.VisibilityFilter` 槽位） | 同上包内第二个构造器 |
| modelrouter | `agent.ModelSelector`（`func(ctx, *Invocation) (model.Model, error)`，纯函数槽位） | 同上包内第三个构造器（或独立 `plugin/modelrouter`） |

**前置流程（硬性）**：动工前在 issue #2632 发英文评论，提出"Tool / skill
pre-filtering and model routing as concrete integrations on existing slots"：
复用三个既有槽位、零新抽象、fail-open 语义；引 LangChain
`ModelRouterMiddleware` 为 modelrouter 先例、引 Octop 生产实践为 toolfilter
依据；询问纳入本提案路线还是另开 issue。**获得认可前不写代码**。

与 v1 二期的差异：骨架从 `插件 → decision.Decider → jev/mock` 换为
`插件 → internal/systemone`（插件内部直连），测试从 mock Decider 换为
httptest。**全部核心设计（预筛算法、缓存策略、fail-open 语义、逃生通道、
三态事件、token 遥测）原样保留。**

### 2.1 构造形态总规则（PR #2664 模式）

```go
// 三个集成的构造器同构：baseURL 显式 + 选项透传 + 领域选项
func NewToolCallback(baseURL string, opts ...Option) (*ToolCallback, error)
func NewSkillFilter(baseURL string, repo skill.Repository, opts ...Option) (*SkillFilter, error)
func NewModelRouter(baseURL string, opts ...Option) (*ModelRouter, error)

// 共通选项（每个包自持一份，与 reviewer 一致；不抽公共 Option 包——那会变相造抽象）
WithAPIKey(key string)
WithDefaultModel(model string)   // 钉版本，如 "jev-2025-10-10"
WithTimeout(d time.Duration)     // 默认 30s（跟随 systemone 默认）
WithHTTPClient(c *http.Client)   // 测试/策略注入

// 领域选项见 2.2-2.4
```

内部统一：

```go
client, err := systemone.NewClient(baseURL,
    systemone.WithAPIKey(o.apiKey),
    systemone.WithDefaultModel(o.model),
    systemone.WithTimeout(o.timeout),
    systemone.WithHTTPClient(o.httpClient),
)
```

### 2.2 工具侧：`plugin/toolfilter`（BeforeModel 回调改写工具列表）

**用户侧**：

```go
cb, err := toolfilter.NewToolCallback("https://api.typesafe.ai",
    toolfilter.WithAPIKey(os.Getenv("TYPESAFE_API_KEY")), // env 由用户读（规则 1.6-3）
    toolfilter.WithDefaultModel("jev-2025-10-10"),
    toolfilter.WithThreshold(0.6),     // P(需要) >= 0.6 保留（Octop 默认值，附录 A.2）
    toolfilter.WithSlack(0.15),        // 近线补录（Octop 默认值）
    toolfilter.WithMinToolCount(8),    // 工具少于 8 个不筛（Octop tool_min_count）
    toolfilter.WithShortlistLimit(16), // 词法召回入围上限
    toolfilter.WithMaxQuestions(20),   // 单请求问题数上限（Octop 同值）
    toolfilter.WithKeepAlways("my_core_tool"), // 在默认保留集之外追加用户自留工具
    // toolfilter.WithShortlistEmbedder(myEmbedder),        // 可选 embedding 召回路
)

agt := llmagent.New("my-agent",
    llmagent.WithModel(myModel),
    llmagent.WithModelCallbacks(&model.Callbacks{
        BeforeModel: []model.BeforeModelCallbackStructured{cb.Callback()},
    }),
)
```

**默认保留集**：Octop 生产实现把 28 个内置工具写死在
`_TOOLS_ALWAYS_KEPT`（完整清单见附录 A.2）——**永不参与打分**（代码注释：
给内置工具花一个问题只会挤占决策模型真正能删的长尾，且删掉核心集会让模型
打出裸 tool call 然后停摆），且**无条件进保留集**（即使全部被问工具低于
阈值、收窄到最小集时也在）。trpc 是框架而非产品，默认保留框架自身的关键
流程工具；业务注册的文件、记忆和搜索工具由业务方按模型侧名称显式保留：

| 默认保留 | 来源（已核实注册名） | 理由（Octop 语义的移植） |
| --- | --- | --- |
| `skill_load`、`skill_select_docs`、`skill_list_docs` | skill 渐进披露 | 保留技能加载及文档发现流程 |
| `skill_run`、`skill_exec`、`skill_write_stdin`、`skill_poll_session`、`skill_kill_session` | skill 执行 | 保留执行入口及会话操作 |
| `workspace_exec`、`workspace_write_stdin`、`workspace_kill_session`、`workspace_save_artifact` | workspace 执行 | 保留执行入口及会话、产物操作 |
| `session_search`、`session_load` | 会话回溯 | 保留按需搜索和加载流程 |
| `transfer_to_agent`、`await_user_reply`、`environment_context_current_time` | Agent 转交、用户回复和时间上下文 | 保留框架控制与上下文流程 |

语义三条（对齐 Octop 决策中间件，源码已核）：

1. 默认保留集**不进候选、不生成问题、不打分**——它们直接从打分名单里排除，
   省下的问题名额全部留给长尾；
2. 保留集 = Jev 判定保留 ∪ 入围但问题预算溢出的（unasked）∪ 默认保留集 ∪
   `WithKeepAlways` 用户追加——四者取并集；
3. 只有实际出现在请求中的名称才受影响。业务方通过 `WithKeepAlways`
   追加核心工具，名称须与模型请求一致，包含可能的工具集前缀；默认集不可裁剪。
   `tool_search` 不保证找回被筛掉的工具，必要时由业务方显式保留。

**内部执行流程**（参数默认值与边界处理按 Octop 生产实现校准，证据见
附录 A；骨架为 systemone 客户端）：

1. **每个 Invocation 打分一次**。key 取 Invocation ID；决策上下文取 Session
   中按默认事件前缀过滤的近期用户与助手对话，并补入当前 Invocation 消息。
   三个决策组件使用相同提取规则，工具预筛使用近期用户消息。工具结果不进入
   上下文，2400 字符预算优先保留最新消息。Session 事件不保证等同最终模型
   请求（如临时注入消息或自定义过滤）。ReAct 后续调用重放首次目录筛选结果，
   之后新增的工具直接保留；没有 Invocation 或有效文本时保持工具目录不变；
2. **两道串联判定**：词法预筛（免费）→ Jev 打分（花钱）。默认保留集
   （技能、会话和执行等框架流程工具，见上文表格）**先从
   候选中排除**——不参与打分、不占问题名额。目录超过
   shortlist 上限时，先按"用户消息 token 与名字+描述的重叠"取前 N 个入围；
   小目录直接送 Jev；大目录按近期用户消息词项对子串的命中次数排序，仅正分
   候选入围（启用 embedding 时融合排序）；技能同样使用此规则。只有入围的才生成 Binary 问题（"本轮需要工具 X 吗"），**单请求合批并行
   评估**。词法落选的候选视为预判不相关——收窄发生时一并删除（硬切语义，
   理由与更保守选项的取舍见 2.6）。大目录没有召回候选时筛选中止，目录原样；
3. **保留判定**：得分 ≥ 阈值入选；没有打分候选过线时，slack 近线补录。最终保留集 =
   Jev 判定保留的 ∪ 入围但问题预算溢出的（unasked）∪ 默认保留集 ∪
   WithKeepAlways 用户追加。全部被问且全部低于阈值减 slack 时收窄到
   "溢出的 + 默认保留集 + 用户追加"。
   （v1 的"缺分保持原目录"分支删除：systemone 严格校验下缺分 = 整调用
   error，由第 4 条吞掉，语义等价且实现更简单；）
4. **fail-open**：`SystemOne` 返回任何错误（网络、协议、截断证据）→ 全量
   放行 + 记事件。筛选是优化，失败要放行；与 PR #2664 的 fail-closed 方向
   相反，两处默认值不能写反（规则 1.6-5）；
5. **发出三态结果事件**（未动 / 收窄 / 保留子集，含每项得分）并统计节省的
   token 数进遥测（附录 A.4 的可观测性做法）。

**打分调用的写法**（问题构造与答案读取的形态）：

```go
questions := make(map[string]systemone.Question, len(shortlist))
for _, t := range shortlist {
    questions["tool:"+t.Name] = systemone.BinaryQuestion{
        Instructions: fmt.Sprintf(
            "Does the current user request need the tool %q? Tool description: %s",
            t.Name, truncate(t.Description, 240)), // Octop 描述截断 240 字符
    }
}
resp, err := f.client.SystemOne(ctx, &systemone.Request{
    State: map[string]any{
        "user_message": userMsg,
        "tool_catalog": toolSummaries(shortlist),
    },
    Questions: questions,
})
if err != nil {
    return nil, nil // fail-open：全量放行，发事件记录
}
// 先遍历全部答案确定 anyAboveThreshold，再逐项判定。
// 严格校验保证被问的问题全部有答案。
ans, _ := resp.Binary("tool:" + t.Name)
keep := ans.Value >= f.threshold || (!anyAboveThreshold && ans.Value >= f.threshold-f.slack)
```

state 裁剪：只含 user/assistant 对话行（剔除 ToolMessage），裁到 2400 字符，
最新消息优先保留——决策 API 吃短情境串，不吃完整上下文窗口（附录 A.2）。

**召回层算法（词法预筛的实现设计）**

硬切会丢弃召回层遗漏的候选，决策模型无法评估它们，也不保证能够恢复；
业务可用 `WithKeepAlways` 保护关键工具。两路召回，RRF 融合：

```text
关键词路（默认，零外部依赖）：
    token 切分（CJK 单字 + 拉丁词，参考 Octop select.py 的处理）
    + 名字和描述合并后做子串匹配，每个用户词项最多计一次分
embedding 路（可选，复用 knowledge/embedder 接口）：
    用户文本和候选描述按需嵌入，分别保存在每个 filter 的 128 条和 1024 条 LRU 缓存中；淘汰后按需重新计算。
    候选描述并发嵌入，每个 filter 的 embedding 调用总并发最多为 8。
    每 turn 只嵌入一条用户消息，余弦相似度排序。
      同义词、跨语言、间接相关在此路解决。
融合：RRF（Reciprocal Rank Fusion）——两个榜单按排名倒数求和。
    关键词抓精确命中（工具名、专有名词），embedding 抓语义相似，
    两路失败模式互补；RRF 是纯算术，零模型调用。
```

边界行为：

1. 无 embedder 或 embedder 调用失败 → 退回纯关键词单路（fail-open 降级，
   与 toolsearch 的 `WithEmbeddingFailOpen` 语义一致）；
2. 工具目录的嵌入不需要向量库——几十上百个描述在内存里算余弦即可，
   不引入 `vectorstore` 依赖。

**边界与演进路径（与 plugin/toolsearch 的关系）**：

1. **不改动 `plugin/toolsearch`**（已核实：仓库内无任何代码 import 它，
   打分内核全是私有函数）。toolfilter 的召回层自带实现，不复制它的导出
   API，注释标明语义参考；
2. 两套打分暂不共享的代价是排序可能漂移（推模式删掉的工具，拉模式未必排
   在搜索前列）。将来的共享内核下沉（`internal/toolmatch`，toolsearch 留
   类型别名保持兼容、跑全部既有测试防回归）**不在二期范围内**，重构理由
   需要实际召回效果的数据支撑；
3. 二期验收标准包含召回测试：建 golden set（用户消息、工具目录、已知相关
   工具），断言相关工具全部进入入围名单，必须覆盖跨语言和同义词用例。
   召回不达标，硬切不上线。

### 2.3 skill 侧：`VisibilityFilter` 实现（`WithSkillFilter` 槽位）

skill 的渲染时序和工具不同（已核实）：skill 摘要和已加载正文由
`SkillsRequestProcessor` 在 preprocess 阶段注入 system message，发生在
BeforeModel 回调**之前**。回调触发时 skill 目录已是 prompt 里的纯文本，
`Request` 上没有结构化字段可改。但 trpc 已有现成机制，不需要像 Octop 那样
用正则修剪 prompt：

- `skill.VisibilityFilter`（`skill/context_repository.go`）：函数槽位
  `func(ctx, skill.Summary) bool`，决定每个 skill 对本轮是否可见；
- `llmagent.WithSkillFilter(filter)`：agent 构造时自动把 filter 包进 skill
  仓库（`skill.NewFilteredRepository`）。包装后的仓库在所有入口生效——
  overview 注入（`SummariesForContext`）、正文加载（`GetForContext`）、
  路径访问（`PathForContext`）。不可见的 skill 不出现在 prompt 里，
  `skill_load` 点名加载也会得到 "not found"。

定位：trpc 现有的 skill 可见性机制是静态规则——scope 隔离（仓库按
app/user 划分）、`WithMaxOverviewSkills`（overview 条数截断）、
`WithMaxLoadedSkills`（加载驱逐）。机制是流水线关系，decision filter 补的是
第一道"按内容动态判断"的工序，后面的截断照常工作：

```text
scope 隔离（决定全量集合，如 40 个）
    ↓
VisibilityFilter（本设计：决策模型筛选，如剩 3 个）
    ↓
MaxOverviewSkills（可见条数过多时截断显示）
    ↓
MaxLoadedSkills（加载全文后的驱逐）
```

与渐进式披露正交：filter 收窄的是披露的候选池（第一级 overview），LLM 在
可见集合里继续做第二级判断（要不要 `skill_load` 全文）。

**实现要点：逐条签名，批量藏在函数内部**

`VisibilityFilter` 的签名是逐条询问，和决策模型"一次请求批量问"不匹配。
但 filter 是空槽位，签名只约定"问一次答一个 bool"，不约束实现内部怎么做。
批量藏在 filter 的第一次调用里：

```go
func (f *SkillFilter) visible(ctx context.Context, s skill.Summary) bool {
    run := runKeyFromContext(ctx)        // 本轮用户消息的标识
    kept, ok := f.cache.get(run)
    if !ok {
        all := f.repo.Summaries()        // 构造时捕获的仓库，拿全量摘要
        kept = f.decideBatch(ctx, all)   // 首次调用：N 个 skill:N 问题合成一次 SystemOne 请求
        f.cache.set(run, kept)
    }
    return kept[s.Name]                  // 本轮后续 N-1 次调用全查缓存
}
```

框架的 for 循环照常逐条问，只是第一次问的时候触发批量决策，之后全部命中
内存缓存。不改 `SummariesForContext` 本身（已发布的公共契约，逐条语义对
白名单、scope 这类便宜实现是合理的），也不写并行版的新函数。

缓存和并发的三个细节：

1. 缓存 key 是 run（用户消息），不是 agent。filter 实例在 agent 构造时挂上，
   全生命周期共享，多个会话并发进出同一个 agent——缓存做成
   `map[runKey]kept` 加锁（或 singleflight，同 run 并发首次调用只发一次
   请求）。新用户消息进来 key 变化，缓存自然失效；
2. 每个 LLM step 都会重新渲染 overview，即重新过一遍全部 filter 调用。缓存
   让重复调用变成内存查询，一个 turn 只花一次决策请求；
3. fail-open 藏在 filter 里：批量决策失败时 kept 退化为"全部可见"，行为
   等价于没配 filter。

**用户视角的完整示例**：

```go
repo, _ := skill.NewFSRepository("./skills")

flt, _ := toolfilter.NewSkillFilter("https://api.typesafe.ai", repo,
    toolfilter.WithAPIKey(key),
    toolfilter.WithDefaultModel("jev-2025-10-10"),
    toolfilter.WithThreshold(0.6),
    toolfilter.WithKeepSkills("core-skill"),
)

agt := llmagent.New("my-agent",
    llmagent.WithModel(myModel),
    llmagent.WithSkills(repo),
    llmagent.WithSkillFilter(flt.VisibilityFilter()), // skill 收窄
    llmagent.WithModelCallbacks(&model.Callbacks{
        BeforeModel: []model.BeforeModelCallbackStructured{cb.Callback()}, // tool 收窄
    }),
)
```

效果：仓库 40 个 skill，本轮决策模型判断 3 个相关——overview 只列 3 条；
模型 `skill_load` 一个不可见的 skill，`GetForContext` 返回 "not found"；
`skill_load` 工具本身在默认保留集里，不会被筛掉。

### 2.4 模型分流：`ModelSelector` 实现

对标 LangChain `ModelRouterMiddleware`（官方先例，附录 B.4），实现既有
`agent.ModelSelector` 槽位（`func(ctx, inv *Invocation) (model.Model, error)`，
已核实签名；模型以实例形式注册，用户消息在 `inv.Message`）：

```go
router, err := toolfilter.NewModelRouter("https://api.typesafe.ai",
    toolfilter.WithAPIKey(key),
    toolfilter.WithDefaultModel("jev-2025-10-10"),
    toolfilter.WithModelCandidate("gpt5mini", myGpt5Mini,
        "fast, cheap tasks, simple lookups, summarization"),
    toolfilter.WithModelCandidate("claude", myClaude,
        "complex reasoning, long-context analysis, code review"),
)

// 每次 Run 传入选择器（RunOption；模型实例在候选表里，不是名字字符串）
out, err := runner.Run(ctx, userID, sessionID, msg,
    agent.WithModelSelector(router.Select),
)
```

**内部**——一个 ChoiceQuestion，每 invocation 缓存一次（ReAct 循环内后续
LLM 调用直接重放，与 LangChain 的 per-run 缓存语义一致）：

```go
func (r *ModelRouter) Select(ctx context.Context, inv *agent.Invocation) (model.Model, error) {
    if m, ok := r.cache.get(inv.InvocationID); ok {
        return m, nil
    }
    options := make([]systemone.ChoiceOption, 0, len(r.candidates))
    for _, c := range r.candidates {
        options = append(options, systemone.ChoiceOption{
            Name: c.name, Description: c.criteria, // 有序（保序是协议要求）
        })
    }
    resp, err := r.client.SystemOne(ctx, &systemone.Request{
        State: inv.Message.Content, // string 是合法 State
        Questions: map[string]systemone.Question{
            "model_route": systemone.ChoiceQuestion{
                Instructions: "Select the model best suited for this user request.",
                Options:      options,
            },
        },
    })
    if err != nil {
        return r.defaultModel, nil // fail-open：出错用默认模型
    }
    ans, err := resp.Choice("model_route")
    if err != nil || r.byName[ans.Selected] == nil {
        return r.defaultModel, nil
    }
    m := r.byName[ans.Selected]
    r.cache.set(inv.InvocationID, m)
    return m, nil
}
```

**设计决定**：循环终止（ReAct 循环是否继续）不引入决策模型。该判断属于
开放式推理（是否继续思考、下一步做什么），由 LLM 在生成 tool call 或最终
回答时自行决定，不属于有界判断。（此条 v1 已定，保留。）

### 2.5 测试策略（httptest 脚本化响应）

替代 v1 的 mock Decider（规则 1.6-4），并额外覆盖真实 wire 解码层：

```go
func newTestToolCallback(t *testing.T, answers map[string]float64) *toolfilter.ToolCallback {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        io.WriteString(w, decisionResponseJSON(answers)) // {"answers":{"tool:X":{"type":"noul","noul":0.9}},...}
    }))
    t.Cleanup(server.Close)
    cb, _ := toolfilter.NewToolCallback(server.URL,
        toolfilter.WithAPIKey("test-key"),
        toolfilter.WithHTTPClient(server.Client()),
        toolfilter.WithThreshold(0.6),
    )
    return cb
}
```

fail-open 覆盖：handler 返回 500 / 429 / 非法响应体 / `server.Close()` 断连
→ 断言工具列表全量保留。原 v1 用 `mock.WithError` 注入的全部场景等价覆盖。

### 2.6 与 Octop 实现的校准点汇总

| 设计点 | Octop 生产值（附录 A） | v2 采用 |
| --- | --- | --- |
| 阈值 / slack | 0.6 / 0.15 | 同 |
| minToolCount | 8 | 同 |
| 单请求问题上限 | 20 | 同（WithMaxQuestions） |
| 词法零重叠 | 整体中止 | 同 |
| 词法落选 | 删除（硬切） | 同，召回不达标不上线 |
| turn 缓存 | key=最后一条 human 消息，LRU 128 | 同语义，run 级缓存 |
| 描述截断 / state 裁剪 | 240 字符 / 2400 字符、剔除 ToolMessage | 同 |
| 内置工具保留集 | `_TOOLS_ALWAYS_KEPT`：28 个产品内置工具，永不打分、无条件保留 | 保留框架流程工具；业务按实际模型侧名称经 WithKeepAlways 追加 |
| 显式指名优先 | @成员直接锁定 | v2 暂不引入（无成员目录场景），留作演进 |
| 被过滤工具的恢复 | ALWAYS_KEPT 保核心，但被筛掉的长尾工具无法找回 | 不保证恢复；业务可通过 WithKeepAlways 防止关键工具被过滤 |

---

## 三、第三期：收尾与增值

### 3.1 出口质检（AfterAgentCallback + Score）

挂 `agent.AfterAgentCallback`：对最终回答问 Score（回答质量 rubric：充分/
部分/不足），低分注入 `CustomResponse` 触发重答（上限 N 次）。为文档示例 +
可选包，不是核心行为。参照附录 A.3 第 3 条：更温和的"提示 + 拦截"组合
（给 system message 追加总结提示，同时拦下后续工具调用）也可选，两案
文档并列。

### 3.2 提案剩余示例认领（融入社区的低成本贡献）

认领 issue #2632 中无人认领的两个示例（1.7）。Graph 路由示例严格用普通
`AddNode` + NodeFunc + conditional edges（提案哲学，无新 API）；Jev-as-tool
示例用 `function.NewFunctionTool` 包装一次决策调用。两示例需要经一个公开
集成包桥接 internal 可见性（PR #2664 同款做法，见 3.3 的候选位置）。
**先在 issue 评论认领再动工。**

### 3.3 观测与运维收尾

1. 决策调用遥测：三态事件（未动 / 收窄 / 保留子集）+ 节省 token 统计在
   二期已做；三期补决策调用的 trace 对接（复用 invocation 既有 trace 机制，
   不新建事件类型）；
2. `model/failover` 将来识别决策模型错误：`internal/systemone` 已暴露稳定
   的 `*HTTPError`（含 status 与 `Retry-After`、`X-Request-ID` 头）；三期
   评估 failover 是否需要消费它，**只读不改 systemone**；
3. 共享集成桥的沉淀：若 3.2 两个示例与二期三个集成的构造样板重复度
   过高，可评估一个小型公开桥包（仅构造便利，不含抽象——形态仍是
   `New(baseURL, opts...)`）；是否需要以实测重复度为依据，不预设。

### 3.4 开放问题（不立项，记录待社区演化）

1. **无决策模型凭据用户的 LLM 后备**：v1 的 `decision/llm`（用现有
   `model.Model` 实现 Decider）随抽象层取消。可能路径：自建网关把 LLM 包装
   成 systemone 兼容端点（协议收敛使然，OpenRouter/Laya 均为兼容先例）；
   或接受无后备。倾向后者，等真实需求出现再议；
2. **供应商预设**（lava / msdecision / qwen）：systemone 客户端本身即协议
   兼容客户端，预设 = baseURL + model 组合，属用户配置而非代码交付；
3. **共享打分内核下沉**（`internal/toolmatch`）：见 2.2 演进路径，靠
   实际召回效果的数据触发。

---

## 四、目录结构

```text
trpc-agent-go/                        （根 module：trpc.group/trpc-go/trpc-agent-go）
├── internal/systemone/               ← 社区已有（PR #2652），只读
├── plugin/guardrail/approval/review/systemone/   ← 社区在审（PR #2664），不做
├── plugin/toolfilter/                ← 第二期（新增，根 module 包，import internal/systemone）
│   ├── toolcallback.go               // NewToolCallback + BeforeModel 回调
│   ├── skillfilter.go                // NewSkillFilter + VisibilityFilter 实现
│   ├── modelrouter.go                // NewModelRouter + ModelSelector 实现
│   ├── recall.go                     // 词法 + 可选 embedding RRF 召回层
│   ├── cache.go                      // run 级缓存 / singleflight
│   ├── events.go                     // 三态事件 + token 节省遥测
│   └── *_test.go                     // httptest 脚本化响应
├── examples/llmagent/toolfilter/     ← 第二期示例：toolfilter + skillfilter + modelrouter
└── examples/graph/decisionroute/     ← 第三期认领示例（普通 AddNode + conditional edges）
```

依赖方向（唯一的箭头方向）：

```text
plugin/toolfilter ──→ internal/systemone（根 module 内，合法）
examples/* ─────────→ plugin/*（公开包，不碰 internal）
core packages ──────→ ✗ 不依赖 internal/systemone（红线，规则 1.6-7）
```

（v1 的 `decision/`、`decision/jev/`、`decision/mock/`、
`graph.AddDecisionNode` 全部移除——已被社区路线替代或否决。）

---

## 五、里程碑

| 里程碑 | 交付物 | 验收标准 |
| --- | --- | --- |
| **M1（第一期：锚定地基）** | 不写代码。issue #2632 英文评论提出二期三集成方向；完成 `internal/systemone` 契约与 PR #2664 构造模式的通读校准（本文档 1.2/1.3 即校准结果） | 提案 owner（wangxuw）或维护者对二期方向表态（纳入 / 另开 issue / 拒绝）；若拒绝则仅保留 3.2 示例认领路径并重估 |
| **M2（第二期：核心贡献）** | `plugin/toolfilter`（tool 回调 + skill filter + model router 三构造器，内置关键词 + 可选 embedding RRF 召回层）；`examples/llmagent/toolfilter` 示例 | fail-open 有测试覆盖（500/断连/非法响应体→全量保留）；turn 内只打分一次、循环内重放有缓存验证；skill 筛选经 `WithSkillFilter` 生效，`GetForContext` 拒绝不可见加载；默认保留的框架流程工具不进打分名单、无条件保留有测试；召回 golden set 测试达标（含跨语言、同义词用例），不达标则硬切不上线；三态事件与节省 token 统计可用；httptest 全离线运行 |
| **M3（第三期：收尾增值）** | 出口质检示例（可选包）；提案剩余两个示例认领交付；决策调用 trace 对接 | 出口质检低分重答路径有测试；两个示例被提案 owner 接收；trace 不新建事件类型；`model/failover` 评估结论成文（做或不做，附依据） |

每个里程碑完成后按 AGENTS.md 执行强制二轮设计评审（导出必要性、包归属、
API 重叠、命名、可扩展性、兼容性、契约完整性、文档、验证九项）。提交纪律：
一期一个 commit，三期合计三个 commit，向 fork 提 PR 前先开 issue（CONTRIBUTING
要求）。

---

## 六、兼容性承诺（不变式清单）

1. 不安装任何 `plugin/toolfilter` 时，`graph`/`agent`/`model`/`plugin` 的
   默认、零值、错误、事件顺序、序列化行为与现状逐字节一致。
2. `plugin/toolsearch` 与 `skill` 包的公共行为不变；toolfilter 与其无代码
   依赖，仅设计互补。
3. 所有新能力 opt-in：不挂回调、不配 filter、不传 selector 即不存在。
4. 核心包不 import `internal/systemone`；`internal` 类型不出现在任何公开
   API 签名中（构造器一律 `New(baseURL, opts ...Option)`）。
5. 不引入通用决策模型抽象（遵守提案 #2632 Non-Goal）；不读环境变量。
6. 失败方向纪律：toolfilter/skillfilter/modelrouter 一律 fail-open；不触碰
   fail-closed 语义的安全域（那是 PR #2664 的职责边界）。
7. 集成锚定的是 System One **线上 API 契约**（state + questions → typed
   answers + probabilities，OpenAPI v0.2.0），而非任何 alpha 阶段包装层。
8. 每个新 Go 文件携带 CONTRIBUTING.md 的 Tencent Apache 2.0 许可头。

---

## 附录 A：参考实现调研（Octop v1.0.2 / octop-harness 1.0.5）

腾讯 Octop 在 v1.0.2 引入"技能、工具与团队的决策模型"（CHANGELOG 明确列出）。
实现分两层：Octop 应用仓库（`github.com/TencentCloud/Octop`，tag v1.0.2）持有
配置、REST API 和前端展示；过滤引擎在外部依赖 octop-harness 中，`decision`
包自 1.0.4 加入（本附录基于 PyPI 1.0.5 wheel 的源码，共 352 行 + 一个 1153 行
的中间件）。以下事实直接取自代码。

### A.1 协议收敛

octop-harness 的 decision 客户端注释写明请求体匹配 OpenRouter decisions API，
Jev 也用该格式：`{state, questions: {id: {type: "noul", instructions,
criteria}}}`，回答取 `noul` 字段即 P(yes)。本地模型 Laya 的端点同样是
`/v1/systemone`。TypeSafe systemone、OpenRouter decisions、Laya 三者线格式
同构，"state + questions → 概率"正在成为决策模型的事实协议。

供应商差异靠一个宽松解析器吸收：响应兼容 `noul` / `probability` / `p_yes` /
`yes` 四种字段名、dict 和 list 两种行格式，而不是每家一个客户端。超时按供应
商区分：远程 API 15 秒，本地 Laya 60 秒（本地 CPU 推理约每问 1 秒，这是性能
预算的实证数据）。问题陈述的 criteria 双语自适应（中文给"适用/不适用"，英文
给 "Yes, this applies."）。

> v2 注：A.1 的"宽松解析器"策略与上游 `internal/systemone` 的严格单契约
> 选择相反，属社区已裁决项（严格校验 + 协议兼容端点覆盖供应商差异）。
> 本附录保留 A.1 作为协议收敛的证据，**方言容错不再采纳**（规则 1.6-6）。

### A.2 筛选流程

每次模型调用前，中间件按以下步骤收窄目录：

1. 取本轮用户消息和三个目录：技能（state 的 `skills_metadata`）、工具
   （`request.tools`）、团队成员（team roster）；
2. **词法预筛**：目录 ≤12 项（middleware 实际传 `_CATALOG_LIMIT = 12`；
   select.py 函数签名的默认值 16 未被使用）全部送决策模型；超过 12 项按
   token 重叠打分取前 12；词法完全无命中则返回 None——整个目录保持不动。
   词法落选的候选不送决策模型，收窄发生时一并删除（词法零重叠视为预判
   不相关）；
3. 每个候选项一条 Noul 陈述，问题 ID 带类型前缀（`tool:X` / `skill:X` /
   `member:X` / `result:sufficient`），文案形如「这轮需要工具「X」。{描述截断
   240 字符}」；
4. 单次请求最多 **20 个问题**，按 result > skill > tool > member 的优先级在
   四类目录间分配名额；member 放不下时整组放弃——部分评估会错杀没打分的成员；
5. **state 只含 user/assistant 对话行**（剔除 ToolMessage），裁到 2400 字符，
   最新消息优先保留。决策 API 吃短情境串，不吃完整上下文窗口。

保留判定：得分 ≥ 阈值（默认 **0.6**）入选；**slack 0.15**——差一点到线的项在
名额未满时保留；一个都没过线返回 None，保持原目录。另有两个独立门槛：工具
少于 **8 个**（`tool_min_count`）不筛；核心工具集 `_TOOLS_ALWAYS_KEPT`
（**28 个**，v2 重数修正 v1 的"约 27"）**永不参与打分**——代码注释原话：
给它们花一个问题只会挤占决策模型真正能删的长尾，且删掉核心集会让模型打出
裸 tool call 然后停摆。完整清单（frozenset，按功能分组）：

```text
文件系统/执行（8）  ls, read_file, write_file, edit_file, delete,
                    glob, grep, execute
任务/时间（3）      write_todos, task, current_time
检索逃生（1）       tool_search
记忆（2）           memory_search, memory_get
派工（2）           agent_list, ask_agent
网络获取（1）       web_fetch
搜索类（8）         tavily_search, brave_search, google_search,
                    kimi_search, deepseek_search, yuanbao_search,
                    searchfree_search, you_search
用户/桌面（3）      send_file_to_user, browser_use, desktop_screenshot
```

另设 `_HOST_DISPATCH_TOOLS = ("ask_agent", "agent_list")`：在决策卡片上
展示但不打分——注释原话 "a low score must not take away the host's
ability to dispatch"（低分不得夺走宿主的派工能力）。

### A.3 三个边界处理

**失败与部分回复**：HTTP 客户端层面任何异常记 warning 并返回空 map，调用方
拿到空结果即目录不变——fail-open 做在客户端里，只有连通性探针用 `strict=True`
抛错。被问的工具全部拿到分数且全部低于阈值，才删长尾；有缺分则保持原目录，
代码注释："a partial reply is not a verdict"（部分回复不是判决）。

> v2 注："部分回复不是判决"的语义在严格校验客户端下由 fail-open 整体吞掉
> （任何响应不完整 = 调用 error = 全量放行），语义等价、实现更简。

**turn 缓存与重放**：每个用户 turn 只打分一次，key = thread + 最后一条 human
消息的位置和内容；该 turn 内 ReAct 工具循环的所有后续模型调用直接重放缓存的
收窄结果（LRU 上限 128）。这回答了"每 run 一次还是每轮一次"：每 turn 一次，
turn 内重放。

**显式指名优先**：用户消息里 @某成员时直接锁定该成员，决策模型照常打分展示但
无权改派；@了不存在的成员则本轮跳过决策。代码注释："Explicit user routing
always wins"（用户的显式选择永远赢）。

### A.4 与本设计的差异

1. **Octop 没有逃生通道**：被决策中间件筛掉的工具无法由模型自行找回。
   硬证据（v2 核实，agent.py 装配顺序）：
   `ToolsFilterMiddleware → DecisionMiddleware → tool_search`，且注释明写
   *"Search only the final model-visible tool set. In particular, this must
   run after MCP and user filtering so hidden tools cannot leak into either
   the client or provider search inventory"*——搜索目录就是收窄后的可见集，
   被删工具不在其中（v1 在 A.2 写过"判漏由 tool_search 兜底"，据本证据
   修正：tool_search 在 ALWAYS_KEPT 里只保住"能搜"这个能力本身，搜不回
   被决策删掉的工具）。trpc 的 `plugin/toolsearch` 结构不同：检索目录是
   MCP 工具注册表（deferred/lazy 加载全集），不是当前 `Request.Tools`——
   toolfilter 删掉的 MCP 工具仍在可搜可载范围内，模型能自己找回。推拉
   互补在 trpc 成立、在 Octop 不成立，这是与 Octop 的关键差异点。
2. **技能注入位置不同**：Octop 的技能目录先被上游 SkillsMiddleware 写进
   system prompt，收窄时除了改 state，还要用正则修剪 prompt 里的
   `**Available Skills:**` 段落。trpc 的渲染时序相同（skill 在 preprocess
   阶段注入 system message，早于 BeforeModel 回调），但已有
   `skill.VisibilityFilter` + `skill.NewFilteredRepository` + llmagent
   `WithSkillFilter` 的现成机制：过滤发生在渲染之前，overview、正文加载、
   路径访问共用同一道闸，不需要字符串手术。已核实，见 2.3。
3. **结果充分性不篡改响应**：团队收尾时问"结果是否足以总结"（≥ 阈值即充分），
   不注入 CustomResponse，而是两步：给 system message 追加"请直接总结"提示，
   同时在工具拦截层拦下后续 `ask_agent` 调用返回 error ToolMessage。提示 +
   拦截的组合比改写响应温和，trpc 的出口质检可参照（3.1）。
4. **可观测性**：节省量 = 模型调用前后的目录 token 差，经 ContextVar 传给
   响应，钉在 AI 消息的 `additional_kwargs` 上，同时经 stream writer 推
   `decision` 事件（含模型名、三态 parts、每项得分）。前端能显示每轮保留
   清单和省下的 token 数。

---

## 附录 B：社区生态情报与核实记录（v2 重写依据）

### B.1 时间线（2026-11-01 经 GitHub API 直连核实）

| 时间 | 事件 | 内容 |
| --- | --- | --- |
| 2026-09-21 | issue #2632 开启（wangxuw） | 提案：scope + Goals + Non-Goals + Open Questions；作者要求 ownership、增量 PR |
| 2026-09-22 | IRONICBo 评论 | 审批数据集四案例；绑定参数 + 状态指纹；fail-closed for mutation |
| 2026-09-24 | liuzengh 评论 | 评估方向跟进，占位 PR #2638 |
| 2026-10-10 | **PR #2652 合入** | `internal/systemone` 共享客户端（16 文件 + 880 行测试 + DESIGN.md/README.md） |
| 2026-10-12 | PR #2664 开启（在审） | system one reviewer + final permission checks（27+ 文件） |
| 至今 | 25 个活跃分支 | 无 decision / jev / systemone / toolfilter 相关 WIP 分支（wangxuw 的工作分支 codex/systemone-reviewer 除外） |

### B.2 "否决"的准确定性

Non-Goal "Adding a new generic decision-model abstraction" 是**提案作者
wangxuw 自划的范围**，不是维护者裁决——评论区无人讨论过抽象层。但两个已
落地 PR（#2652、#2664）均按"具体实现填既有槽位"执行，形成既成方向；提案
Open Questions 留有口子（"existing interfaces sufficient, or concrete use
case requiring..."），说明具体用例驱动的扩展可以谈。**结论：对抗既成方向
性价比低，v2 全面转向槽位集成。**

### B.3 internal 可见性与构造模式的硬证据

- PR #2664 新增公开包 `plugin/guardrail/approval/review/systemone`：内部
  `import systemoneclient "trpc.group/trpc-go/trpc-agent-go/internal/systemone"`，
  对外 `New(baseURL string, opts ...Option)`，**不读 env**；
- 其示例 `examples/guardrail/approval/systemone/app.go` 导入公开插件包，
  **不直接 import internal**（CI 禁止 examples 引 internal）；
- 结论：集成包必须落在根 module 内；公开签名不得出现 internal 类型；
  示例经公开包桥接。v2 规则 1.6-2 与 2.1 构造形态由此而来。

### B.4 LangChain typesafe 0.0.1a3 核实（本地包逐文件验证）

包结构：`classifier.py`（TypeSafeClassifier，Runnable）、`types.py`（三种
问题/答案类型）、`experimental/middleware/{model_router,auto_mode}.py`。

**核实结论：官方包没有 decision node API**；Graph 场景的官方形态是用户
自己写普通 node 调 classifier + conditional edges；框架级集成是两个
AgentMiddleware（`wrap_model_request` / `wrap_tool_call` 钩子包装）。
TypeSafe 官方 cookbook 的 function-calling 页面亦无 decision node API
（已抓取核实）。

> 更正记录：v1 前期调研曾得出"LangChain 官方做法是加 addDecisionNode"的
> 结论，**该结论有误**（可能把"官方示例中用户写 node 调 classifier"误记为
> "官方提供 decision node API"）。记录在案防止复用。

**战略情报**：`ModelRouterMiddleware`（= 二期 modelrouter 的官方先例）与
`AutoModeMiddleware`（= 风险门控，已被 PR #2664 对应）官方均做了；**工具/
技能预筛官方未做**——二期 toolfilter 是 LangChain 生态也没有的空白，
差异化最大。

### B.5 v1 撤销记录

v1 一期曾在本地 `feature/jev-integration` 分支实现（decision 抽象层 +
jev/mock 独立 module + AddDecisionNode + 示例，全部验证通过），因与社区
路线冲突于 2026-11-01 撤销：`git checkout --` 恢复跟踪文件，删除全部新增
文件，构建与测试回到 main 基线。撤销前实现满足原设计规格，其架构结论
（checkpoint JSON 往返的类型恢复问题、问题 map 共享的数据竞争、指针形式
类型断言等）已在 v2 设计时规避（v2 不再有跨 state 传递 Answer 类型的场景）。
