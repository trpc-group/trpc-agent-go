# 过滤器功能

> **示例代码**: [examples/knowledge/features/metadata-filter](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/metadata-filter) 以及 [examples/knowledge/features/agentic-filter](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/agentic-filter)


Knowledge 系统提供了强大的过滤器功能，允许基于文档元数据进行精准搜索。这包括静态过滤器和智能过滤器两种模式。

## 配置元数据源

为了使过滤器功能正常工作，需要在创建文档源时添加元数据：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
)

sources := []source.Source{
    // 文件源配置元数据
    filesource.New(
        []string{"./docs/api.md"},
        filesource.WithName("API Documentation"),
        filesource.WithMetadataValue("category", "documentation"),
        filesource.WithMetadataValue("topic", "api"),
        filesource.WithMetadataValue("service_type", "gateway"),
        filesource.WithMetadataValue("protocol", "trpc-go"),
        filesource.WithMetadataValue("version", "v1.0"),
    ),

    // 目录源配置元数据
    dirsource.New(
        []string{"./tutorials"},
        dirsource.WithName("Tutorials"),
        dirsource.WithMetadataValue("category", "tutorial"),
        ...
    ),

    // URL 源配置元数据
    urlsource.New(
        []string{"https://example.com/wiki/rpc"},
        urlsource.WithName("RPC Wiki"),
        urlsource.WithMetadataValue("category", "encyclopedia"),
        ...
    ),
}
```

> **提示**：更多文档源配置详见 [文档源配置](source.md)。


## 基础过滤器

> **重要：过滤器字段命名规范**
>
> 在使用过滤器时，**元数据字段需要使用 `metadata.` 前缀**：
> - `metadata.` 前缀用于区分元数据字段和系统字段（如 `id`、`name`、`content` 等）
> - 无论是 `llmagent.WithKnowledgeConditionedFilter()`、`knowledgetool.WithConditionedFilter()` 还是 `searchfilter.Equal()` 等，元数据字段都需要加 `metadata.` 前缀
> - 如果通过 `WithMetadataField()` 自定义了元数据字段名，仍然使用 `metadata.` 前缀，框架会自动转换为实际的字段名
> - 通过 `WithDocBuilder` 自定义的表字段（如 `status`、`priority` 等额外列）直接使用字段名，无需前缀

基础过滤器支持两种设置方式：Agent 级别的固定过滤器和 Runner 级别的运行时过滤器。

### Agent 级过滤器

在创建 Agent 时预设固定的搜索过滤条件：

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"

// 创建带有固定过滤器的 Agent
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithKnowledge(kb),
    llmagent.WithKnowledgeConditionedFilter(
        searchfilter.And(
            searchfilter.Equal("metadata.category", "documentation"),
            searchfilter.Equal("metadata.topic", "api"),
        ),
    ),
)
```

### Runner 级过滤器

在调用 `runner.Run()` 时动态传递过滤器，适用于需要根据不同请求上下文进行过滤的场景：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

// 在运行时传递过滤器
eventCh, err := runner.Run(
    ctx,
    userID,
    sessionID,
    message,
    agent.WithKnowledgeConditionedFilter(
        searchfilter.And(
            searchfilter.Equal("metadata.category", "tutorial"),
            searchfilter.Equal("metadata.difficulty", "beginner"),
            searchfilter.Equal("metadata.language", "zh"),
        ),
    ),
)
```

### 过滤器合并规则

Agent 级过滤器和 Runner 级过滤器通过 **AND 逻辑**组合：

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"

// Agent 级过滤器
llmAgent := llmagent.New(
    "assistant",
    llmagent.WithKnowledge(kb),
    llmagent.WithKnowledgeConditionedFilter(
        searchfilter.And(
            searchfilter.Equal("metadata.category", "documentation"),
            searchfilter.Equal("metadata.source_type", "web"),
        ),
    ),
)

// Runner 级过滤器
eventCh, err := runner.Run(
    ctx, userID, sessionID, message,
    agent.WithKnowledgeConditionedFilter(
        searchfilter.Equal("metadata.topic", "api"),
    ),
)

// 最终生效的过滤器（AND 组合）：
// metadata.category = "documentation" AND
// metadata.source_type = "web" AND
// metadata.topic = "api"
```

## 智能过滤器 (Agentic Filter)


> **重要：过滤器字段命名规范**
>
> 在使用过滤器时，**元数据字段需要使用 `metadata.` 前缀**：
> - `metadata.` 前缀用于区分元数据字段和系统字段（如 `id`、`name`、`content` 等）
> - 无论是 `llmagent.WithKnowledgeConditionedFilter()`、`knowledgetool.WithConditionedFilter()` 还是 `searchfilter.Equal()` 等，元数据字段都需要加 `metadata.` 前缀
> - 如果通过 `WithMetadataField()` 自定义了元数据字段名，仍然使用 `metadata.` 前缀，框架会自动转换为实际的字段名
> - 通过 `WithDocBuilder` 自定义的表字段（如 `status`、`priority` 等额外列）直接使用字段名，无需前缀


智能过滤器是 Knowledge 系统的高级功能，允许 LLM Agent 根据用户查询动态选择合适的过滤条件。

### 启用智能过滤器

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// 获取所有源的元数据信息
sourcesMetadata := source.GetAllMetadata(sources)

// 创建支持智能过滤的 Agent
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithKnowledge(kb),
    llmagent.WithEnableKnowledgeAgenticFilter(true),           // 启用智能过滤器
    llmagent.WithKnowledgeAgenticFilterInfo(sourcesMetadata), // 提供可用的过滤器信息
)
```

## 过滤器层级

Knowledge 系统支持多层过滤器，所有过滤器统一使用 FilterCondition 实现，通过 **AND 逻辑**组合。系统不区分优先级，所有层级的过滤器平等合并。

**过滤器层级**：

1. **Agent 级过滤器**：
   - 通过 `llmagent.WithKnowledgeConditionedFilter()` 设置条件过滤器

2. **Tool 级过滤器**：
   - 通过 `knowledgetool.WithConditionedFilter()` 设置条件过滤器
   - 注：Agent 级过滤器实际上是通过 Tool 级过滤器实现的

3. **Runner 级过滤器**：
   - 通过 `agent.WithKnowledgeConditionedFilter()` 在 `runner.Run()` 时传递条件过滤器

4. **LLM 智能过滤器**：
   - LLM 根据用户查询动态生成的过滤条件

> **重要说明**：
> - 所有过滤器通过 **AND 逻辑**组合，即必须同时满足所有层级的过滤条件

### 过滤器组合示例

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"

// 1. Agent 级过滤器
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithKnowledge(kb),
    llmagent.WithKnowledgeConditionedFilter(
        searchfilter.And(
            searchfilter.Equal("metadata.source_type", "web"),
            searchfilter.Equal("metadata.category", "documentation"),
            searchfilter.Equal("metadata.protocol", "trpc-go"),
        ),
    ),
)

// 2. Runner 级过滤器
eventCh, err := runner.Run(
    ctx, userID, sessionID, message,
    agent.WithKnowledgeConditionedFilter(
        searchfilter.And(
            searchfilter.Equal("metadata.language", "zh"),
            searchfilter.Equal("metadata.version", "v1.0"),
        ),
    ),
)

// 3. LLM 智能过滤器（由 LLM 动态生成）
// 例如：用户问 "查找 API 相关文档"，LLM 可能生成 {"field": "metadata.topic", "value": "api"}

// 最终生效的过滤条件（所有条件通过 AND 组合）：
// metadata.source_type = "web" AND
// metadata.category = "documentation" AND
// metadata.protocol = "trpc-go" AND
// metadata.language = "zh" AND
// metadata.version = "v1.0" AND
// metadata.topic = "api"
//
// 即：必须同时满足所有层级的所有条件
```

## 复杂条件过滤器

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
    knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
)

// 手动创建带有条件过滤器的 Tool
searchTool := knowledgetool.NewKnowledgeSearchTool(
    kb,
    knowledgetool.WithConditionedFilter(
        searchfilter.And(
            searchfilter.Equal("metadata.source_type", "web"),
            searchfilter.Or(
                searchfilter.Equal("metadata.topic", "programming"),
                searchfilter.Equal("metadata.topic", "api"),
            ),
        ),
    ),
)

llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools(searchTool),  // 手动传递 Tool
)

// 最终过滤条件：
// metadata.source_type = "web" AND (metadata.topic = "programming" OR metadata.topic = "api")
// 即：必须是 web 来源，且主题是编程或 API
```

## 常用过滤器辅助函数

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"

// 比较操作符（注意：元数据字段需要 metadata. 前缀）
searchfilter.Equal("metadata.topic", value)                  // metadata.topic = value
searchfilter.NotEqual("metadata.category", value)            // metadata.category != value
searchfilter.GreaterThan("metadata.version", value)          // metadata.version > value
searchfilter.GreaterThanOrEqual("metadata.version", value)   // metadata.version >= value
searchfilter.LessThan("metadata.version", value)             // metadata.version < value
searchfilter.LessThanOrEqual("metadata.version", value)      // metadata.version <= value
searchfilter.In("metadata.category", values...)              // metadata.category IN (...)
searchfilter.NotIn("metadata.topic", values...)              // metadata.topic NOT IN (...)
searchfilter.Like("metadata.protocol", pattern)              // metadata.protocol LIKE pattern
searchfilter.Between("metadata.version", min, max)           // metadata.version BETWEEN min AND max

// 自定义表字段（通过 WithDocBuilder 添加的额外列）不需要前缀
searchfilter.NotEqual("status", "deleted")                   // status != "deleted"
searchfilter.GreaterThanOrEqual("priority", 3)               // priority >= 3

// 逻辑操作符
searchfilter.And(conditions...)               // AND 组合
searchfilter.Or(conditions...)                // OR 组合

// 嵌套示例：(metadata.category = 'documentation') AND (metadata.topic = 'api' OR metadata.topic = 'rpc')
searchfilter.And(
    searchfilter.Equal("metadata.category", "documentation"),
    searchfilter.Or(
        searchfilter.Equal("metadata.topic", "api"),
        searchfilter.Equal("metadata.topic", "rpc"),
    ),
)
```

## 多文档返回

Knowledge Search Tool 支持返回多个相关文档，可通过 `WithMaxResults(n)` 选项限制返回的最大文档数量：

```go
import knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"

// 创建搜索工具，限制最多返回 5 个文档
searchTool := knowledgetool.NewKnowledgeSearchTool(
    kb,
    knowledgetool.WithMaxResults(5),
)

// 或使用智能过滤搜索工具
agenticSearchTool := knowledgetool.NewAgenticFilterSearchTool(
    kb,
    sourcesMetadata,
    knowledgetool.WithMaxResults(10),
)
```

每个返回的文档包含文本内容、元数据和相关性分数，按分数降序排列。
