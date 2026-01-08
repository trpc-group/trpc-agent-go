# 过滤器功能

> **示例代码**: [examples/knowledge/features/metadata-filter](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/metadata-filter) 以及 [examples/knowledge/features/agentic-filter](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/agentic-filter)

Knowledge 系统提供了强大的过滤器功能，允许基于文档元数据进行精准搜索。这包括静态过滤器和智能过滤器（由 LLM 根据文档元数据信息自动生成过滤条件）两种模式。

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

## 过滤器字段设置要求

在使用过滤器时，**元数据字段需要使用 `metadata.` 前缀**：

*   `metadata.` 前缀用于区分元数据字段和系统字段（如 `id`、`name`、`content` 等）
*   无论是基础过滤器的条件设置，还是智能过滤器生成的条件，元数据字段名都必须包含此前缀
*   **基础过滤器**：例如 `searchfilter.Equal("metadata.category", "docs")`
*   **智能过滤器**：LLM 生成的 JSON 字段名也需包含前缀，例如 `{"metadata.topic": "api"}`（系统会自动处理）
*   **例外**：通过 `WithDocBuilder` 自定义的表字段（如 `status`、`priority` 等额外列，需向量数据库支持）直接使用字段名，无需前缀

## 过滤器层级

Knowledge 系统支持多层过滤器，所有过滤器统一使用 FilterCondition 实现，通过 **AND 逻辑**组合。系统不区分优先级，所有层级的过滤器平等合并。

**过滤器层级**：

1. **Tool 级过滤器 / Agent 级过滤器**：
   - **手动创建工具**（通过 `llmagent.WithTools(自定义searchTool)` 注入）：通过 `knowledgetool.WithConditionedFilter()` 设置。
   - **自动创建工具**（通过 `llmagent.WithKnowledge` 注入）：通过 `llmagent.WithKnowledgeConditionedFilter()` 设置。
   - 注：两者本质相同，都是作用于 Tool 实例的静态过滤器。

2. **Runner 级过滤器**：

   - 通过 `agent.WithKnowledgeConditionedFilter()` 在 `runner.Run()` 时传递条件过滤器

3. **LLM 智能过滤器**：

   - LLM 根据用户查询动态生成的过滤条件

> **重要说明**：
>
> - 所有过滤器通过 **AND 逻辑**组合，即必须同时满足所有层级的过滤条件

## 基础过滤器

基础过滤器支持两种设置方式：Tool/Agent 级别的固定过滤器和 Runner 级别的运行时过滤器。

### Tool/Agent 级过滤器

**方式一：自动创建 Tool 时的 Agent 级过滤器**

在创建 Agent 并通过 `WithKnowledge` 自动注入 Knowledge Tool 时，可以使用 `WithKnowledgeConditionedFilter` 预设固定的搜索过滤条件：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

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

**方式二：手动创建 Tool 时的 Tool 级过滤器**

手动创建 Knowledge Search Tool 时，可以使用 `WithConditionedFilter` 注入过滤器：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
    knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
)

// 手动创建 Tool 并设置过滤器
searchTool := knowledgetool.NewKnowledgeSearchTool(
    kb,
    knowledgetool.WithConditionedFilter(
        searchfilter.Equal("metadata.language", "en"),
    ),
)

// 将 Tool 注入到 Agent
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools(searchTool),
)
```

### Runner 级过滤器

在调用 `runner.Run()` 时动态传递过滤器，适用于需要根据不同请求上下文进行过滤的场景：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// 创建 Runner
appRunner := runner.NewRunner("knowledge-chat", llmAgent)

// 在运行时传递过滤器
eventCh, err := appRunner.Run(
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

Tool/Agent 级过滤器和 Runner 级过滤器通过 **AND 逻辑**组合：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// Tool/Agent 级过滤器 (metadata.category = "documentation")
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

// 创建 Runner
appRunner := runner.NewRunner("knowledge-chat", llmAgent)

// Runner 级过滤器 (metadata.topic = "api")
eventCh, err := appRunner.Run(
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

智能过滤器是 Knowledge 系统的高级功能，允许 LLM Agent 根据用户查询动态选择合适的过滤条件。

### 启用智能过滤器

启用智能过滤器时，需要通过 `WithKnowledgeAgenticFilterInfo` 提供可用于过滤的元数据字段信息。这些信息将作为提示词的一部分，引导 LLM 生成正确的过滤条件。

支持以下三种配置方式：

**方式一：自动提取（推荐）**

从配置的文档源中自动提取元数据信息：

1.  **提取字段和值**：使用 `source.GetAllMetadata(sources)`，LLM 将在提取的枚举值范围内进行选择（适合有限枚举值）。
2.  **仅提取字段**：使用 `source.GetAllMetadataWithoutValues(sources)`，LLM 将根据用户查询自由推断值（适合开放域值）。

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// 1. 提取所有元数据信息（包括字段名和所有出现过的值）
// 例如：{"metadata.category": ["doc", "tutorial"], "metadata.topic": ["api", "rpc"]}
sourcesMetadata := source.GetAllMetadata(sources)

// 2. 或者仅提取字段名（不限制取值范围，由 LLM 推断）
// 例如：{"metadata.category": [], "metadata.topic": []}
// sourcesMetadata := source.GetAllMetadataWithoutValues(sources)

llmAgent := llmagent.New(
    // ...
    llmagent.WithEnableKnowledgeAgenticFilter(true),
    llmagent.WithKnowledgeAgenticFilterInfo(sourcesMetadata),
)
```

**方式二：手动配置（指定字段和值）**

手动指定允许过滤的字段及其允许值枚举，适合枚举值有限的场景（如状态、分类）：

```go
// 手动指定字段和可选值
customMetadata := map[string][]string{
    "category": {"documentation", "tutorial", "blog"},
    "language": {"en", "zh"},
}

llmAgent := llmagent.New(
    // ...
    llmagent.WithEnableKnowledgeAgenticFilter(true),
    llmagent.WithKnowledgeAgenticFilterInfo(customMetadata),
)
```

**方式三：手动配置（指定字段，由 LLM 推断值）**

仅指定允许过滤的字段，值列表留空（`nil` 或空切片），让 LLM 根据用户查询自由推断值（适合 ID、名称等枚举值过多的开放域字段）：

```go
// 仅指定字段，不限制取值范围
customMetadata := map[string][]string{
    "author_id": nil,       // LLM 自动提取 author_id
    "publish_year": nil,    // LLM 自动提取年份
    "category": {"news"},   // 混合使用：category 限制为 "news"
}

llmAgent := llmagent.New(
    // ...
    llmagent.WithEnableKnowledgeAgenticFilter(true),
    llmagent.WithKnowledgeAgenticFilterInfo(customMetadata),
)
```

### 过滤器组合示例

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)


// 创建 Runner
appRunner := runner.NewRunner("knowledge-chat", llmAgent)

// 2. Runner 级过滤器
eventCh, err := appRunner.Run(
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
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
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
