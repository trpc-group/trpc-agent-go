# Knowledge 使用文档

## 概述

Knowledge 是 tRPC-Agent-Go 框架中的知识管理系统，为 Agent 提供检索增强生成（Retrieval-Augmented Generation, RAG）能力。通过集成向量数据、embedding 模型和文档处理组件，Knowledge 系统能够帮助 Agent 访问和检索相关的知识信息，从而提供更准确、更有依据的响应。

### 使用模式

Knowledge 系统的使用遵循以下模式：

1. **创建 Knowledge**：配置向量存储、Embedder 和知识源
2. **加载文档**：从各种来源加载和索引文档
3. **集成到 Agent**：使用 `WithKnowledge()` 将 Knowledge 集成到 LLM Agent 中
4. **Agent 自动检索**：Agent 通过内置的 `knowledge_search` 工具自动进行知识检索

这种模式提供了：

- **智能检索**：基于向量相似度的语义搜索
- **多源支持**：支持文件、目录、URL 等多种知识来源
- **灵活存储**：支持内存、PostgreSQL、TcVector 等多种存储后端
- **高性能处理**：并发处理和批量文档加载
- **可扩展架构**：支持自定义 Embedder、Retriever 和 Reranker

### Agent 集成

Knowledge 系统与 Agent 的集成方式：

- **自动工具注册**：使用 `WithKnowledge()` 选项自动添加 `knowledge_search` 工具
- **智能过滤工具**：使用 `WithEnableKnowledgeAgenticFilter(true)` 启用 `knowledge_search_with_filter` 工具
- **工具调用**：Agent 可以调用知识搜索工具获取相关信息
- **上下文增强**：检索到的知识内容自动添加到 Agent 的上下文中
- **元数据过滤**：支持基于文档元数据进行精准搜索

## 快速开始

### 环境要求

- Go 1.24.1 或更高版本
- 有效的 LLM API 密钥（OpenAI 兼容接口）
- 向量数据库（可选，用于生产环境）

### 配置环境变量

```bash
# OpenAI API 配置
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="your-openai-base-url"

# embedding 模型配置（可选，需要手动读取）
export OPENAI_EMBEDDING_MODEL="text-embedding-3-small"
```

### 最简示例

```go
package main

import (
    "context"
    "log"

    // 核心组件
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
    vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    ctx := context.Background()

    // 1. 创建 embedder
    embedder := openaiembedder.New(
        openaiembedder.WithModel("text-embedding-3-small"),
    )

    // 2. 创建向量存储
    vectorStore := vectorinmemory.New()

    // 3. 创建知识源（确保这些路径存在或替换为你自己的路径）
    // 以下文件在 https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge
    sources := []source.Source{
        filesource.New([]string{"./data/llm.md"}),
        dirsource.New([]string{"./dir"}),
    }

    // 4. 创建 Knowledge
    kb := knowledge.New(
        knowledge.WithEmbedder(embedder),
        knowledge.WithVectorStore(vectorStore),
        knowledge.WithSources(sources),
    )

    // 5. 加载文档
    log.Println("🚀 开始加载 Knowledge ...")
    if err := kb.Load(ctx); err != nil {
        log.Fatalf("Failed to load knowledge base: %v", err)
    }
    log.Println("✅ Knowledge 加载完成！")

    // 6. 创建 LLM 模型
    modelInstance := openai.New("claude-4-sonnet-20250514")

    // 7. 创建 Agent 并集成 Knowledge
    llmAgent := llmagent.New(
        "knowledge-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("具有 Knowledge 访问能力的智能助手"),
        llmagent.WithInstruction("使用 knowledge_search 工具从 Knowledge 检索相关信息，并基于检索内容回答问题。"),
        llmagent.WithKnowledge(kb), // 自动添加 knowledge_search 工具
    )

    // 8. 创建 Runner
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "knowledge-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
    )

    // 9. 执行对话（Agent 会自动使用 knowledge_search 工具）
    log.Println("🔍 开始搜索 Knowledge ...")
    message := model.NewUserMessage("请告诉我关于 LLM 的信息")
    eventChan, err := appRunner.Run(ctx, "user123", "session456", message)
    if err != nil {
        log.Fatalf("Failed to run agent: %v", err)
    }

    // 10. 处理响应 ...
}
```

## 核心概念

[knowledge 模块](https://github.com/trpc-group/trpc-agent-go/tree/main/knowledge) 是 tRPC-Agent-Go 框架的知识管理核心，提供了完整的 RAG 能力。该模块采用模块化设计，支持多种文档源、向量存储后端和 embedding 模型。

```
knowledge/
├── knowledge.go          # 核心接口定义和主要实现
├── source/               # 文档源管理
│   ├── source.go        # Source 接口定义
│   ├── file.go          # 文件源实现
│   ├── dir.go           # 目录源实现
│   ├── url.go           # URL 源实现
│   └── auto.go          # 自动源类型检测
├── vectorstore/          # 向量存储后端
│   ├── vectorstore.go   # VectorStore 接口定义
│   ├── inmemory/        # 内存向量存储（开发/测试用）
│   ├── pgvector/        # PostgreSQL + pgvector 实现
│   └── tcvector/        # 腾讯云向量数据库实现
├── embedder/             # 文本 embedding 模型
│   ├── embedder.go      # Embedder 接口定义
│   ├── openai/          # OpenAI embedding 模型
│   └── local/           # 本地 embedding 模型
├── document/             # 文档表示
│   └── document.go      # Document 结构定义
├── query/                # 查询增强器
│   ├── query.go         # QueryEnhancer 接口定义
│   └── passthrough.go   # 默认透传增强器
└── loader/               # 文档加载器
    └── loader.go        # 文档加载逻辑
```

## 使用指南

### 与 Agent 集成

使用 `llmagent.WithKnowledge(kb)` 将 Knowledge 集成到 Agent，框架会自动注册 `knowledge_search` 工具，无需手动创建自定义工具。

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/tool" // 可选：需要附加其他工具时使用
)

// 创建 Knowledge
// kb := ...

// 创建 Agent 并集成 Knowledge
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("具有 Knowledge 访问能力的智能助手"),
    llmagent.WithInstruction("使用 knowledge_search 工具从 Knowledge 检索相关信息，并基于检索内容回答问题。"),
    llmagent.WithKnowledge(kb), // 自动添加 knowledge_search 工具
    // llmagent.WithTools([]tool.Tool{otherTool}), // 可选：附加其他工具
)
```

### 向量存储 (VectorStore)

向量存储可在代码中通过选项配置，配置来源可以是配置文件、命令行参数或环境变量，用户可以自行实现。

#### 向量存储配置示例

```go
import (
    vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
    vectorpgvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
    vectortcvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
)

// 内存实现，可用于测试
memVS := vectorinmemory.New()

// PostgreSQL + pgvector
pgVS, err := vectorpgvector.New(
    vectorpgvector.WithHost("127.0.0.1"),
    vectorpgvector.WithPort(5432),
    vectorpgvector.WithUser("postgres"),
    vectorpgvector.WithPassword("your-password"),
    vectorpgvector.WithDatabase("your-database"),
    // 根据 embedding 模型设置索引维度（text-embedding-3-small 为 1536）。
    pgvector.WithIndexDimension(1536),
    // 启用/关闭文本检索向量，配合混合检索权重使用。
    pgvector.WithEnableTSVector(true),
    // 调整混合检索权重（向量相似度权重与文本相关性权重）。
    pgvector.WithHybridSearchWeights(0.7, 0.3),
    // 如安装了中文分词扩展（如 zhparser/jieba），可设置语言以提升文本召回。
    pgvector.WithLanguageExtension("english"),
)
if err != nil {
    // 处理 error
}

// TcVector
tcVS, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-tcvector-endpoint"),
    vectortcvector.WithUsername("your-username"),
    vectortcvector.WithPassword("your-password"),
)
if err != nil {
    // 处理 error
}

// 传递给 Knowledge
kb := knowledge.New(
    knowledge.WithVectorStore(memVS), // pgVS, tcVS
)
```

### Embedder

Embedder 负责将文本转换为向量表示，是 Knowledge 系统的核心组件。目前框架主要支持 OpenAI embedding 模型：

```go
import (
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
)

// OpenAI Embedder 配置
embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"), // embedding 模型，也可通过 OPENAI_EMBEDDING_MODEL 环境变量设置
)

// 传递给 Knowledge
kb := knowledge.New(
    knowledge.WithEmbedder(embedder),
)
```

**支持的 embedding 模型**：

- OpenAI embedding 模型（text-embedding-3-small 等）
- 其他兼容 OpenAI API 的 embedding 服务

> **注意**:
>
> - Retriever 和 Reranker 目前由 Knowledge 内部实现，用户无需单独配置。Knowledge 会自动处理文档检索和结果排序。
> - `OPENAI_EMBEDDING_MODEL` 环境变量需要在代码中手动读取，框架不会自动读取。参考示例代码中的 `getEnvOrDefault("OPENAI_EMBEDDING_MODEL", "")` 实现。

### 文档源配置

源模块提供了多种文档源类型，每种类型都支持丰富的配置选项：

```go
import (
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
    autosource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
)

// 文件源：单个文件处理，支持 .txt, .md, .go, .json 等格式
fileSrc := filesource.New(
    []string{"./data/llm.md"},
    filesource.WithChunkSize(1000),      // 分块大小
    filesource.WithChunkOverlap(200),    // 分块重叠
    filesource.WithName("LLM Doc"),
    filesource.WithMetadataValue("type", "documentation"),
)

// 目录源：批量处理目录，支持递归和过滤
dirSrc := dirsource.New(
    []string{"./docs"},
    dirsource.WithRecursive(true),                           // 递归处理子目录
    dirsource.WithFileExtensions([]string{".md", ".txt"}),   // 文件扩展名过滤
    dirsource.WithExcludePatterns([]string{"*.tmp", "*.log"}), // 排除模式
    dirsource.WithChunkSize(800),
    dirsource.WithName("Documentation"),
)

// URL 源：从网页和 API 获取内容
urlSrc := urlsource.New(
    []string{"https://en.wikipedia.org/wiki/Artificial_intelligence"},
    urlsource.WithTimeout(30*time.Second),           // 请求超时
    urlsource.WithUserAgent("MyBot/1.0"),           // 自定义 User-Agent
    urlsource.WithMaxContentLength(1024*1024),       // 最大内容长度 (1MB)
    urlsource.WithName("Web Content"),
)

// 自动源：智能识别类型，自动选择处理器
autoSrc := autosource.New(
    []string{
        "Cloud computing provides on-demand access to computing resources.",
        "https://docs.example.com/api",
        "./config.yaml",
    },
    autosource.WithName("Mixed Sources"),
    autosource.WithFallbackChunkSize(1000),
)

// 组合使用
sources := []source.Source{fileSrc, dirSrc, urlSrc, autoSrc}

// 传递给 Knowledge
kb := knowledge.New(
    knowledge.WithSources(sources),
)

// 加载所有源
if err := kb.Load(ctx); err != nil {
    log.Fatalf("Failed to load knowledge base: %v", err)
}
```

### 批量文档处理与并发

Knowledge 支持批量文档处理和并发加载，可以显著提升大量文档的处理性能：

```go
err := kb.Load(ctx,
    knowledge.WithShowProgress(true),      // 打印进度日志
    knowledge.WithProgressStepSize(10),    // 进度步长
    knowledge.WithShowStats(true),         // 打印统计信息
    knowledge.WithSourceConcurrency(4),    // 源级并发
    knowledge.WithDocConcurrency(64),      // 文档级并发
)
```

## 过滤器功能

Knowledge 系统提供了强大的过滤器功能，允许基于文档元数据进行精准搜索。这包括静态过滤器和智能过滤器两种模式。

### 基础过滤器

基础过滤器支持两种设置方式：Agent 级别的固定过滤器和 Runner 级别的运行时过滤器。

#### Agent 级过滤器

在创建 Agent 时预设固定的搜索过滤条件：

```go
// 创建带有固定过滤器的 Agent
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithKnowledge(kb),
    llmagent.WithKnowledgeFilter(map[string]interface{}{
        "category": "documentation",
        "topic":    "programming",
    }),
)
```

#### Runner 级过滤器

在调用 `runner.Run()` 时动态传递过滤器，适用于需要根据不同请求上下文进行过滤的场景：

```go
import "trpc.group/trpc-go/trpc-agent-go/agent"

// 在运行时传递过滤器
eventCh, err := runner.Run(
    ctx,
    userID,
    sessionID,
    message,
    agent.WithKnowledgeFilter(map[string]interface{}{
        "user_level": "premium",     // 根据用户级别过滤
        "region":     "china",       // 根据地区过滤
        "language":   "zh",          // 根据语言过滤
    }),
)
```

Runner 级过滤器的优先级高于 Agent 级过滤器，相同键的值会被覆盖：

```go
// Agent 级过滤器
llmAgent := llmagent.New(
    "assistant",
    llmagent.WithKnowledge(kb),
    llmagent.WithKnowledgeFilter(map[string]interface{}{
        "category": "general",
        "source":   "internal",
    }),
)

// Runner 级过滤器会覆盖相同的键
eventCh, err := runner.Run(
    ctx, userID, sessionID, message,
    agent.WithKnowledgeFilter(map[string]interface{}{
        "source": "external",  // 覆盖 Agent 级的 "internal"
        "topic":  "api",       // 新增过滤条件
    }),
)

// 最终生效的过滤器：
// {
//     "category": "general",   // 来自 Agent 级
//     "source":   "external",  // 来自 Runner 级（覆盖）
//     "topic":    "api",       // 来自 Runner 级（新增）
// }
```

### 智能过滤器 (Agentic Filter)

智能过滤器是 Knowledge 系统的高级功能，允许 LLM Agent 根据用户查询动态选择合适的过滤条件。

#### 启用智能过滤器

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

#### 过滤器优先级

系统支持多层过滤器，按以下优先级合并（后者覆盖前者）：

1. **Agent 级过滤器**：`WithKnowledgeFilter()` 设置的固定过滤器（优先级最低）
2. **Runner 级过滤器**：运行时传递的过滤器（优先级中等）
3. **智能过滤器**：LLM 动态生成的过滤器（优先级最高）

```go
// 过滤器合并逻辑（优先级：Agent < Runner < 智能过滤器）
// 如果多个层级设置了相同的键，高优先级的值会覆盖低优先级的值

// Agent 级过滤器（基础过滤器）
agentFilter := map[string]interface{}{
    "category": "documentation",
    "source":   "internal",
}

// Runner 级过滤器（运行时过滤器）
runnerFilter := map[string]interface{}{
    "source": "official",  // 覆盖 Agent 级的 "internal"
    "topic":  "api",
}

// 智能过滤器（LLM 动态生成）
intelligentFilter := map[string]interface{}{
    "topic": "programming",  // 覆盖 Runner 级的 "api"
    "level": "advanced",
}

// 最终合并结果
finalFilter := {
    "category": "documentation",  // 来自 Agent 级
    "source":   "official",       // 来自 Runner 级（覆盖了 Agent 级）
    "topic":    "programming",     // 来自智能过滤器（覆盖了 Runner 级）
    "level":    "advanced",       // 来自智能过滤器
}
```

### 配置元数据源

为了使智能过滤器正常工作，需要在创建文档源时添加丰富的元数据：

```go
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
        dirsource.WithMetadataValue("difficulty", "beginner"),
        dirsource.WithMetadataValue("topic", "programming"),
    ),
    
    // URL 源配置元数据
    urlsource.New(
        []string{"https://example.com/wiki/rpc"},
        urlsource.WithName("RPC Wiki"),
        urlsource.WithMetadataValue("category", "encyclopedia"),
        urlsource.WithMetadataValue("source_type", "web"),
        urlsource.WithMetadataValue("topic", "rpc"),
        urlsource.WithMetadataValue("language", "zh"),
    ),
}
```

### 向量数据库过滤器支持

不同的向量数据库对过滤器的支持程度不同：

#### PostgreSQL + pgvector
- ✅ 支持所有元数据字段过滤
- ✅ 支持复杂查询条件
- ✅ 支持 JSONB 字段索引

```go
vectorStore, err := vectorpgvector.New(
    vectorpgvector.WithHost("127.0.0.1"),
    vectorpgvector.WithPort(5432),
    // ... 其他配置
)
```

#### TcVector
- ✅ 支持预定义字段过滤
- ⚠️ 需要预先建立过滤字段索引

```go
// 获取所有元数据键用于建立索引
metadataKeys := source.GetAllMetadataKeys(sources)

vectorStore, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-endpoint"),
    vectortcvector.WithFilterIndexFields(metadataKeys), // 建立过滤字段索引
    // ... 其他配置
)
```

#### 内存存储
- ✅ 支持所有过滤器功能
- ⚠️ 仅适用于开发和测试

## 高级功能

### QueryEnhancer

QueryEnhancer 用于在搜索前对用户查询进行预处理和优化。目前框架只提供了一个默认实现：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/query"
)

kb := knowledge.New(
    knowledge.WithQueryEnhancer(query.NewPassthroughEnhancer()), // 默认增强器，按原样返回查询
)
```

> **注意**: QueryEnhancer 不是必须的组件。如果不指定，Knowledge 会直接使用原始查询进行搜索。只有在需要自定义查询预处理逻辑时才需要配置此选项。

### 性能优化

Knowledge 系统提供了多种性能优化策略，包括并发处理、向量存储优化和缓存机制：

```go
// 根据系统资源调整并发数
kb := knowledge.New(
    knowledge.WithSources(sources),
    knowledge.WithSourceConcurrency(runtime.NumCPU()),
    knowledge.WithDocConcurrency(runtime.NumCPU()*2),
)
```

## 完整示例

以下是一个完整的示例，展示了如何创建具有 Knowledge 访问能力的 Agent：

```go
package main

import (
    "context"
    "flag"
    "log"
    "os"
    "strconv"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"

    // Embedder
    "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
    geminiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/gemini"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"

    // Source
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    autosource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"

    // Vector Store
    "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
    vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
    vectorpgvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
    vectortcvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
)

func main() {
    var (
        embedderType    = flag.String("embedder", "openai", "embedder type (openai, gemini)")
        vectorStoreType = flag.String("vectorstore", "inmemory", "vector store type (inmemory, pgvector, tcvector)")
        modelName       = flag.String("model", "claude-4-sonnet-20250514", "Name of the model to use")
    )

    flag.Parse()

    ctx := context.Background()

    // 1. 创建 embedder（根据环境变量选择）
    var embedder embedder.Embedder
    var err error

    switch *embedderType {
    case "gemini":
        embedder, err = geminiembedder.New(context.Background())
        if err != nil {
            log.Fatalf("Failed to create gemini embedder: %v", err)
        }
    default: // openai
        embedder = openaiembedder.New(
            openaiembedder.WithModel(getEnvOrDefault("OPENAI_EMBEDDING_MODEL", "text-embedding-3-small")),
        )
    }

    // 2. 创建向量存储（根据参数选择）
    var vectorStore vectorstore.VectorStore

    switch *vectorStoreType {
    case "pgvector":
        port, err := strconv.Atoi(getEnvOrDefault("PGVECTOR_PORT", "5432"))
        if err != nil {
            log.Fatalf("Failed to convert PGVECTOR_PORT to int: %v", err)
        }

        vectorStore, err = vectorpgvector.New(
            vectorpgvector.WithHost(getEnvOrDefault("PGVECTOR_HOST", "127.0.0.1")),
            vectorpgvector.WithPort(port),
            vectorpgvector.WithUser(getEnvOrDefault("PGVECTOR_USER", "postgres")),
            vectorpgvector.WithPassword(getEnvOrDefault("PGVECTOR_PASSWORD", "")),
            vectorpgvector.WithDatabase(getEnvOrDefault("PGVECTOR_DATABASE", "vectordb")),
            vectorpgvector.WithIndexDimension(1536),
        )
        if err != nil {
            log.Fatalf("Failed to create pgvector store: %v", err)
        }
    case "tcvector":
        vectorStore, err = vectortcvector.New(
            vectortcvector.WithURL(getEnvOrDefault("TCVECTOR_URL", "")),
            vectortcvector.WithUsername(getEnvOrDefault("TCVECTOR_USERNAME", "")),
            vectortcvector.WithPassword(getEnvOrDefault("TCVECTOR_PASSWORD", "")),
        )
        if err != nil {
            log.Fatalf("Failed to create tcvector store: %v", err)
        }
    default: // inmemory
        vectorStore = vectorinmemory.New()
    }

    // 3. 创建知识源
    sources := []source.Source{
        // 文件源：单个文件处理
        filesource.New(
            []string{"./data/llm.md"},
            filesource.WithChunkSize(1000),
            filesource.WithChunkOverlap(200),
            filesource.WithName("LLM Documentation"),
            filesource.WithMetadataValue("type", "documentation"),
            filesource.WithMetadataValue("category", "ai"),
        ),

        // 目录源：批量处理目录
        dirsource.New(
            []string{"./dir"},
            dirsource.WithRecursive(true),
            dirsource.WithFileExtensions([]string{".md", ".txt"}),
            dirsource.WithChunkSize(800),
            dirsource.WithName("Documentation"),
            dirsource.WithMetadataValue("category", "docs"),
        ),

        // URL 源：从网页获取内容
        urlsource.New(
            []string{"https://en.wikipedia.org/wiki/Artificial_intelligence"},
            urlsource.WithName("Web Documentation"),
            urlsource.WithMetadataValue("source", "web"),
            urlsource.WithMetadataValue("category", "wikipedia"),
            urlsource.WithMetadataValue("language", "en"),
        ),

        // 自动源：混合内容类型
        autosource.New(
            []string{
                "Cloud computing is the delivery of computing services over the internet, including servers, storage, databases, networking, software, and analytics. It provides on-demand access to shared computing resources.",
                "Machine learning is a subset of artificial intelligence that enables systems to learn and improve from experience without being explicitly programmed.",
                "./README.md",
            },
            autosource.WithName("Mixed Knowledge Sources"),
            autosource.WithMetadataValue("category", "mixed"),
            autosource.WithMetadataValue("type", "custom"),
            autosource.WithMetadataValue("topics", []string{"cloud", "ml", "ai"}),
        ),
    }

    // 4. 创建 Knowledge
    kb := knowledge.New(
        knowledge.WithEmbedder(embedder),
        knowledge.WithVectorStore(vectorStore),
        knowledge.WithSources(sources),
    )

    // 5. 加载文档（带进度和统计）
    log.Println("🚀 开始加载 Knowledge ...")
    if err := kb.Load(
        ctx,
        knowledge.WithShowProgress(true),
        knowledge.WithProgressStepSize(10),
        knowledge.WithShowStats(true),
        knowledge.WithSourceConcurrency(4),
        knowledge.WithDocConcurrency(64),
    ); err != nil {
        log.Fatalf("❌ Knowledge 加载失败: %v", err)
    }
    log.Println("✅ Knowledge 加载完成！")

    // 6. 创建 LLM 模型
    modelInstance := openai.New(*modelName)

    // 获取所有源的元数据信息（用于智能过滤器）
    sourcesMetadata := source.GetAllMetadata(sources)
    
    // 7. 创建 Agent 并集成 Knowledge
    llmAgent := llmagent.New(
        "knowledge-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("具有 Knowledge 访问能力的智能助手"),
        llmagent.WithInstruction("使用 knowledge_search 或 knowledge_search_with_filter 工具从 Knowledge 检索相关信息，并基于检索内容回答问题。根据用户查询选择合适的过滤条件。"),
        llmagent.WithKnowledge(kb), // 自动添加 knowledge_search 工具
        llmagent.WithEnableKnowledgeAgenticFilter(true),           // 启用智能过滤器
        llmagent.WithKnowledgeAgenticFilterInfo(sourcesMetadata), // 提供可用的过滤器信息
    )

    // 8. 创建 Runner
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "knowledge-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
    )

    // 9. 执行对话（Agent 会自动使用 knowledge_search 工具）
    log.Println("🔍 开始搜索知识库...")
    message := model.NewUserMessage("请告诉我关于 LLM 的信息")
    eventChan, err := appRunner.Run(ctx, "user123", "session456", message)
    if err != nil {
        log.Fatalf("Failed to run agent: %v", err)
    }

    // 10. 处理响应 ...
}

// getEnvOrDefault returns the environment variable value or a default value if not set.
func getEnvOrDefault(key, defaultValue string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return defaultValue
}
```

其中，环境变量配置如下：

```bash
# OpenAI API 配置（当使用 OpenAI embedder 时必选，会被 OpenAI SDK 自动读取）
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="your-openai-base-url"
# OpenAI embedding 模型配置（可选，需要在代码中手动读取）
export OPENAI_EMBEDDING_MODEL="text-embedding-3-small"

# Google Gemini API 配置（当使用 Gemini embedder 时）
export GOOGLE_API_KEY="your-google-api-key"

# PostgreSQL + pgvector 配置
export PGVECTOR_HOST="127.0.0.1"
export PGVECTOR_PORT="5432"
export PGVECTOR_USER="postgres"
export PGVECTOR_PASSWORD="your-password"
export PGVECTOR_DATABASE="vectordb"

# TcVector 配置
export TCVECTOR_URL="https://your-tcvector-endpoint"
export TCVECTOR_USERNAME="your-username"
export TCVECTOR_PASSWORD="your-password"
```

### 命令行参数

```bash
# 运行示例时可以通过命令行参数选择组件类型
go run main.go -embedder openai -vectorstore inmemory
go run main.go -embedder gemini -vectorstore pgvector
go run main.go -embedder openai -vectorstore tcvector

# 参数说明：
# -embedder: 选择 embedder 类型 (openai, gemini)， 默认为 openai
# -vectorstore: 选择向量存储类型 (inmemory, pgvector, tcvector)，默认为 inmemory
```

## 故障排除

### 常见问题与处理建议

1. **Create embedding failed/HTTP 4xx/5xx**

   - 可能原因：API Key 无效或缺失；BaseURL 配置错误；网络访问受限；文本过长；所配置的 BaseURL 不提供 Embeddings 接口或不支持所选的 embedding 模型（例如返回 404 Not Found）。
   - 排查步骤：
     - 确认 `OPENAI_API_KEY` 已设置且可用；
     - 如使用兼容网关，显式设置 `WithBaseURL(os.Getenv("OPENAI_BASE_URL"))`；
     - 确认 `WithModel("text-embedding-3-small")` 或你所用服务实际支持的 embedding 模型名称；
     - 使用最小化样例调用一次 embedding API 验证连通性；
     - 用 curl 验证目标 BaseURL 是否实现 `/v1/embeddings` 且模型存在：
       ```bash
       curl -sS -X POST "$OPENAI_BASE_URL/embeddings" \
         -H "Authorization: Bearer $OPENAI_API_KEY" \
         -H "Content-Type: application/json" \
         -d '{"model":"text-embedding-3-small","input":"ping"}'
       ```
       若返回 404/模型不存在，请更换为支持 Embeddings 的 BaseURL 或切换到该服务提供的有效 embedding 模型名。
     - 逐步缩短文本，确认非超长输入导致。
   - 参考代码：
     ```go
     embedder := openaiembedder.New(
         openaiembedder.WithModel("text-embedding-3-small"),
         openaiembedder.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
         openaiembedder.WithBaseURL(os.Getenv("OPENAI_BASE_URL")),
     )
     if _, err := embedder.GetEmbedding(ctx, "ping"); err != nil {
         log.Fatalf("embed check failed: %v", err)
     }
     ```

2. **加载速度慢或 CPU 占用高**

   - 可能原因：单核顺序加载；并发设置不合适；大文件分块策略不合理。
   - 排查步骤：
     - 设置源级/文档级并发：`WithSourceConcurrency(N)`、`WithDocConcurrency(M)`；
     - 调整分块大小，避免过多小块；
     - 临时关闭统计输出减少日志开销：`WithShowStats(false)`。
   - 参考代码：
     ```go
     err := kb.Load(ctx,
         knowledge.WithSourceConcurrency(runtime.NumCPU()),
         knowledge.WithDocConcurrency(runtime.NumCPU()*2),
         knowledge.WithShowStats(false),
     )
     ```

3. **存储连接失败（pgvector/TcVector）**

   - 可能原因：连接参数错误；网络/鉴权失败；服务未启动或端口不通。
   - 排查步骤：
     - 使用原生客户端先连通一次（psql/curl）；
     - 显式打印当前配置（host/port/user/db/url）；
     - 为最小化示例仅插入/查询一条记录验证。

4. **内存使用过高**

   - 可能原因：一次性加载文档过多；块尺寸/重叠过大；相似度筛选过宽。
   - 排查步骤：
     - 减小并发与分块重叠；
     - 分批加载目录。

5. **维度/向量不匹配**

   - 症状：搜索阶段报错或得分异常为 0。
   - 排查：
     - 确认 embedding 模型维度与存量向量一致（`text-embedding-3-small` 为 1536）；
     - 替换 embedding 模型后需重建（清空并重灌）向量库。

6. **路径/格式读取失败**

   - 症状：加载日志显示 0 文档或特定源报错。
   - 排查：
     - 确认文件存在且后缀受支持（.md/.txt/.pdf/.csv/.json/.docx 等）；
     - 目录源是否需要 `WithRecursive(true)`；
     - 使用 `WithFileExtensions` 做白名单过滤。

