# 高级配置

## 自动提取模式配置选项

| 选项                       | 说明                            | 默认值      |
| -------------------------- | ------------------------------- | ----------- |
| `WithExtractor(extractor)` | 使用 LLM 提取器启用自动提取模式 | nil（禁用） |
| `WithAsyncMemoryNum(n)`    | 后台 worker goroutine 数量      | 1           |
| `WithMemoryQueueSize(n)`   | 记忆任务队列大小                | 10          |
| `WithMemoryJobTimeout(d)`  | 每个提取任务的超时时间          | 30s         |

## 提取检查器（Extraction Checkers）

检查器（Checker）用于控制何时触发记忆提取。默认情况下，每轮对话都会触发提取。使用检查器可以优化提取频率，降低 LLM 调用成本。

### 可用的检查器

| 检查器                  | 说明                               | 示例                                           |
| ----------------------- | ---------------------------------- | ---------------------------------------------- |
| `CheckMessageThreshold` | 当累积消息数超过阈值时触发         | `CheckMessageThreshold(5)` - 消息数 > 5 时触发 |
| `CheckTimeInterval`     | 当距上次提取超过指定时间间隔时触发 | `CheckTimeInterval(3*time.Minute)` - 每 3 分钟 |
| `ChecksAll`             | 组合多个检查器，使用 AND 逻辑      | 所有检查器都通过才触发                         |
| `ChecksAny`             | 组合多个检查器，使用 OR 逻辑       | 任一检查器通过即触发                           |

### 检查器配置示例

```go
// 示例 1：消息数 > 5 或每 3 分钟提取一次（OR 逻辑）。
memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithCheckersAny(
        extractor.CheckMessageThreshold(5),
        extractor.CheckTimeInterval(3*time.Minute),
    ),
)

// 示例 2：消息数 > 10 且每 5 分钟提取一次（AND 逻辑）。
memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithChecker(extractor.CheckMessageThreshold(10)),
    extractor.WithChecker(extractor.CheckTimeInterval(5*time.Minute)),
)
```

### 模型回调（Before/After Model）

提取器也支持通过 `model.Callbacks` 注入 before/after 回调（仅支持 structured），用于埋点、改写请求，或在测试中短路模型调用。

```go
callbacks := model.NewCallbacks().RegisterBeforeModel(
    func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
        // You can modify args.Request or return CustomResponse.
        return nil, nil
    },
).RegisterAfterModel(
    func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
        // You can inspect/override args.Response.
        return nil, nil
    },
)

memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithModelCallbacks(callbacks),
)
```

### ExtractionContext

`ExtractionContext` 为检查器提供决策所需的上下文信息：

```go
type ExtractionContext struct {
    UserKey       memory.UserKey  // 用户标识。
    Messages      []model.Message // 自上次提取以来累积的消息。
    LastExtractAt *time.Time      // 上次提取时间戳，首次提取时为 nil。
}
```

**注意**：`Messages` 包含自上次成功提取以来累积的所有消息。当检查器返回 `false` 时，消息会被累积，并在下次提取时一并处理。这确保了使用轮数或时间检查器时不会丢失对话上下文。

## 工具控制

在自动提取模式下，`WithToolEnabled` 控制工具是否可用。`memory_search`
默认会通过 `Tools()` 暴露给 Agent，启用 `memory_load` 后也会暴露；
`WithAutoMemoryExposedTools` 则用于选择性暴露已启用的写工具，支持
Hybrid 用法。

**前端工具**（通过 `Tools()` 暴露给 Agent 调用；默认状态指 Agent 侧暴露状态）：

| 工具            | Agent 侧默认 | 说明                              |
| --------------- | ------------ | --------------------------------- |
| `memory_search` | ✅ 暴露      | 按查询搜索记忆                    |
| `memory_load`   | ❌ 不暴露    | 加载全部或最近 N 条记忆；启用后暴露 |

**后端操作**（提取器可用性与 Agent 侧暴露状态分开控制）：

| 工具            | 操作默认 | Agent 侧默认 | 说明                         |
| --------------- | -------- | ------------ | ---------------------------- |
| `memory_add`    | ✅ 开    | ❌ 隐藏      | 添加新记忆                   |
| `memory_update` | ✅ 开    | ❌ 隐藏      | 更新现有记忆                 |
| `memory_delete` | ✅ 开    | ❌ 隐藏      | 删除记忆                     |
| `memory_clear`  | ❌ 关    | ❌ 隐藏      | 清空用户所有记忆（危险操作） |

**配置示例**：

```go
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithExtractor(memExtractor),
    // 前端：启用 memory_load 供 Agent 调用。
    memoryinmemory.WithToolEnabled(memory.LoadToolName, true),
    // Hybrid：暴露 memory_add，便于 Agent 立即持久化明确提示的长期信息。
    memoryinmemory.WithAutoMemoryExposedTools(memory.AddToolName),
    // 后端：禁用 memory_delete，提取器将无法删除记忆。
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, false),
    // 后端：启用 memory_clear 供提取器使用（谨慎使用）。
    memoryinmemory.WithToolEnabled(memory.ClearToolName, true),
)
```

**注意**：`WithToolEnabled` 和 `WithAutoMemoryExposedTools` 都可以在
`WithExtractor` 之前或之后调用，顺序不影响结果。

## 两种模式对比

| 工具            | 工具驱动模式（无提取器）            | 自动提取模式（有提取器）            |
| --------------- | ----------------------------------- | ----------------------------------- |
| `memory_add`    | ✅ Agent 通过 `Tools()` 调用        | ⚙️ 暴露后 Agent 可通过 `Tools()` 调用；提取器也会在后台使用 |
| `memory_update` | ✅ Agent 通过 `Tools()` 调用        | ⚙️ 暴露后 Agent 可通过 `Tools()` 调用；提取器也会在后台使用 |
| `memory_search` | ✅ Agent 通过 `Tools()` 调用        | ✅ Agent 通过 `Tools()` 调用        |
| `memory_load`   | ✅ Agent 通过 `Tools()` 调用        | ⚙️ 启用后 Agent 通过 `Tools()` 调用 |
| `memory_delete` | ⚙️ 启用后 Agent 通过 `Tools()` 调用 | ⚙️ 暴露后 Agent 可通过 `Tools()` 调用；提取器也会在后台使用 |
| `memory_clear`  | ⚙️ 启用后 Agent 通过 `Tools()` 调用 | ⚙️ 暴露后 Agent 可通过 `Tools()` 调用；启用后提取器也会在后台使用 |

## 记忆预加载

两种模式都支持将记忆预加载到系统提示词中：

```go
llmAgent := llmagent.New(
    "assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(memoryService.Tools()),
    // 预加载选项：
    // llmagent.WithPreloadMemory(0),   // 禁用预加载（默认）。
    // llmagent.WithPreloadMemory(10),  // 自适应预加载预算 10。
    //                                  // 记忆总量 <= 10 时全量注入，
    //                                  // 否则注入当前问题相关的前 10 条检索结果。
    // llmagent.WithPreloadMemory(-1),  // 加载全部。
    //                                  // ⚠️ 警告：全量加载可能显著增加 token 使用量和 API 成本，
    //                                  //     特别是对于存储了大量记忆的用户。生产环境建议使用正数预算。
)
```

启用预加载后，记忆会自动注入到系统提示词中，让 Agent 无需显式工具调用就能获得用户上下文。

当 `WithPreloadMemory(N)` 使用正数时，框架会先探测用户当前的 memory 总量。
如果总量不超过 `N`，则直接全量注入；如果总量超过 `N`，则在框架内部切换为
基于当前用户问题的 `memory_search` 语义，只注入最相关的前 `N` 条结果。
如果当前 `query` 为空、检索报错，或检索结果为空，则会回退为直接加载最多
`N` 条记忆。

**注入机制**：预加载的记忆会**合并**到现有的系统提示词中，而不是作为独立的 system message 插入。这确保了请求中始终只有一个 system message，兼容某些对多个 system message 支持不完善的模型（如 Qwen3.5 系列可能会返回 "System message must be at the beginning" 错误）。

**⚠️ 重要提示**：配置为 `-1` 会加载所有记忆，这可能会显著增加**Token 使用量**和**API 成本**。默认情况下预加载是禁用的（`0`），推荐使用正数预算（如 `10-50`）来平衡性能和成本。

## 混合方案

你可以结合两种方式：

1. 使用自动提取模式进行被动学习（后台提取）
2. 启用搜索工具进行显式记忆查询
3. 预加载记忆获得即时上下文

```go
// 自动提取 + 搜索工具 + 预加载。
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithExtractor(extractor),
)

llmAgent := llmagent.New(
    "assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(memoryService.Tools()),  // 默认只有 search（load 可选）。
    llmagent.WithPreloadMemory(10),             // 自适应预加载预算。
)
```
