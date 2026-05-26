# ToolPipe Extension

ToolPipe 是一个 agent-scoped extension，为选定的工具注入 shell-like 结果过滤能力。

## 核心理念

**精确投喂，提升模型注意力质量。**

当工具返回大量数据时，全部塞进 context 不仅浪费 token，更关键的是会**分散模型注意力**。ToolPipe 的价值不在于"省 token"本身，而在于让模型在有限的注意力窗口里看到的**全是有用信息**。

大输出处理的哲学：模型永远不应该被大输出直接淹没。ToolPipe 的做法是自动窗口化（保留头尾轮廓）+ 按需精确过滤（shell-like pipe），让模型始终工作在精简、聚焦的上下文中。

ToolPipe 的独特定位：**给没有本地 shell 环境的 Agent 一个受控的"结果投影"层**。特别适合第三方 MCP 工具、网络搜索、API 返回等接入方无法修改工具定义、也无法预知返回大小的场景。

## 甜蜜点与反面场景

### ✅ 适合的场景（大数据 + 小目标 + 可结构化过滤）

- **日志查错**：10000 行日志中 grep 5 行 ERROR
- **API/MCP 返回提取字段**：从搜索结果 JSON 中只取 title + url
- **网页定位特定段落**：80KB 页面中 grep 关于 authentication 的 2KB 内容
- **配置/Schema 提取**：大 OpenAPI spec 中只看路由列表
- **结构提取**：大文档中只取标题列表

共同特征：数据大、目标小、目标可用 grep/jq 描述、不需要全文理解。

### ❌ 不适合的场景

- **总结整篇文章**：需要全文理解
- **对比两个文档差异**：需要全量
- **小数据源**：结果本来就不大（< maxOutput），filter 反而增加无谓轮次
- **目标模糊/需要全文扫描**：模型不确定要 grep 什么，会反复探测

### Benchmark 实测数据

Token 消耗可能减少也可能增加，取决于场景和模型策略。**Token 不是核心指标**——核心指标是单轮上下文的信噪比和模型回答的精准度。

| 场景 | Token 变化 | Peak Context 变化 | 说明 |
|------|-----------|-----------------|------|
| JSON 字段提取（Algolia API） | -88% | -96% | 最佳场景 |
| 结构提取（文档标题列表） | -99% | -99% | 最佳场景 |
| 大页面定位段落（defer 章节） | -65% | -93% | 良好 |
| 大海捞针（2 个事实问答） | -34% | -86% | 良好 |
| 小数据模糊搜索 ❌ | +235% | +86% | 不适合此场景 |

关键观察：即使在"大海捞针"场景中 toolpipe 模式的答案**更完整更准确**——因为 context 精简后模型注意力更集中。这比 token 数字更有意义。

## 工作原理

ToolPipe 使用三个 callback（BeforeModel + BeforeTool + AfterTool）实现，不修改框架主链路：

1. **BeforeModel**：给选定工具的 InputSchema 追加一个 `result_filter` 字段（optional string）；注入 system prompt 引导模型使用。
2. **BeforeTool**：从工具参数中剥离 `result_filter`，解析为 pipeline AST，存入 context。
3. **AfterTool**：
   - 有 filter → 对结果执行 pipeline，返回过滤后内容
   - 无 filter 但结果超过 maxOutput → 自动 head+tail 窗口化，标记 `truncated: true` + `total_bytes`
   - 无 filter 且结果不大 → 原样返回

### 窗口化策略（无 filter 时）

采用 head+tail 截断策略：保留输出的开头和结尾，中间标记省略字节数。这让模型获得整体轮廓，能直接工作或写出精准的 filter，**不会诱导多轮探测拼全文**。

## 使用方式

```go
import "trpc.group/trpc-go/trpc-agent-go/agent/extension/toolpipe"

agent := llmagent.New("researcher",
    llmagent.WithTools([]tool.Tool{webFetchTool, mcpSearchTool}),
    llmagent.WithExtensions(
        toolpipe.New(
            toolpipe.WithToolNames("web_fetch", "mcp_search"),
            toolpipe.WithAllowedOps(toolpipe.OpGrep, toolpipe.OpHead, toolpipe.OpTail, toolpipe.OpJQ),
            toolpipe.WithMaxOutputBytes(32<<10), // 32KB 窗口
        ),
    ),
)
```

### 配置选项

| Option | 说明 | 默认值 |
|--------|------|--------|
| `WithToolNames(names...)` | 白名单：哪些工具启用 filter | 空（必须显式指定） |
| `WithToolScope(fn)` | 动态选择器（如按前缀匹配 MCP 工具） | nil |
| `WithAllowedOps(ops...)` | 允许的 filter 操作 | grep, head, tail |
| `WithMaxOutputBytes(n)` | 窗口大小（无 filter 时截断阈值） | 64KB |
| `WithMaxInputBytes(n)` | filter 前最大输入 | 2MB |
| `WithFilterField(name)` | 注入的参数字段名 | `"result_filter"` |
| `WithPrompt(text)` | 自定义引导 prompt（空字符串=关闭） | 内置默认 |

### 支持的 Filter 操作

模型可以写 shell-like pipeline 语法：

```sh
grep ERROR                     — 匹配行
grep -i timeout                — 忽略大小写
grep -v DEBUG                  — 排除行
head -20                       — 前 N 行
tail -10                       — 后 N 行
jq '.results[] | .title'       — JSON 查询
jq -r '.content'               — JSON 提取为纯文本
grep ERROR | head 5            — 组合管道
jq -r '.items[].name' | grep Go | head 10
```

使用 `mvdan.cc/sh/v3` 做 shell 语法解析（只解析不执行），白名单验证命令，拒绝重定向、变量赋值、命令替换等不安全构造。

## 设计原则与代价

### 设计原则

1. **Allowlist only**：只有显式选定的工具才会被增强。不该对写操作、审批、状态修改类工具启用。
2. **同名增强**：不改工具名，不新增工具，只追加一个 optional 字段。对 dispatch、tracing、tool filter 零影响。
3. **Fail safe**：filter 解析失败仍剥离字段（防止泄漏到原工具）；工具执行失败直接透传错误不 filter。
4. **框架不教策略**：prompt 只描述能力和格式，不教模型"怎么用"。策略留给用户在 Instruction 中定义。
5. **框架工具自动跳过**：以下类型的工具会被自动排除，即使出现在 allowlist 中：
   - 实现了 `StreamInner()` 或 `InnerTextMode()` 的工具（如 AgentTool）
   - 已知框架内置工具（`transfer_to_agent`、`await_user_reply`）
   - `LongRunning()` 返回 true 的长生命周期工具
   - 实现了 `StateDelta` / `StateDeltaForInvocation` 的状态修改工具（如 todo、artifact、skill 类工具）
   
   这类工具的输出是框架编排语义或被框架状态机消费，不是可 grep/jq 投影的用户数据。

### 代价与权衡

- **多轮次**：模型可能为了精确提取信息做多次 filter 调用，增加总 token 和延迟。
- **不适合全文理解任务**：如果任务需要通读全文，toolpipe 的窗口化反而会迫使模型多轮拼凑。
- **模型行为不可控**：模型看到 `result_filter` 参数可能过度使用它，即使数据不大。
- **非幂等工具风险**：result_filter 的"重新调用回查"模式假设工具是幂等的。对有副作用或结果不稳定的工具，多次调用可能有问题。

### 什么时候不该用

- 工具返回结果通常很小（< maxOutput）
- 任务是"总结/翻译/对比"整份内容
- 工具有副作用或非幂等
- 目标不明确，需要全文扫描才能确定要什么

## 独立模块

ToolPipe 是独立 Go module（`agent/extension/toolpipe/go.mod`），依赖 `mvdan.cc/sh/v3` 和 `github.com/itchyny/gojq`。不使用 toolpipe 的用户不会被拉入这些依赖。
