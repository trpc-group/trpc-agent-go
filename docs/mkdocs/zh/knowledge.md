# Knowledge 使用文档

## 概述

Knowledge 是 tRPC-Agent-Go 框架中的知识管理系统，为 Agent 提供检索增强生成（Retrieval-Augmented Generation, RAG）能力。通过集成向量数据、embedding 模型和文档处理组件，Knowledge 系统能够帮助 Agent 访问和检索相关的知识信息，从而提供更准确、更有依据的响应。

### 使用模式

Knowledge 系统的使用遵循以下模式：

1. **创建 Knowledge**：配置向量存储、Embedder 和知识源
2. **加载文档**：从各种来源加载和索引文档
3. **集成到 Agent**：使用 `WithKnowledge()` 将 Knowledge 集成到 LLM Agent 中
4. **Agent 自动检索**：Agent 通过内置的 `knowledge_search` 工具自动进行知识检索
5. **知识库管理**：通过 `enableSourceSync` 启用智能同步机制，确保向量存储中的数据始终与用户配置的 source 保持一致

这种模式提供了：

- **智能检索**：基于向量相似度的语义搜索
- **多源支持**：支持文件、目录、URL 等多种知识来源
- **灵活存储**：支持内存、PostgreSQL、TcVector 等多种存储后端
- **高性能处理**：并发处理和批量文档加载
- **知识过滤**：通过元数据，支持知识的静态过滤和 Agent 智能过滤
- **可扩展架构**：支持自定义 Embedder、Retriever 和 Reranker
- **动态管理**：支持运行时添加、移除和更新知识源
- **数据一致性保证**：通过 `enableSourceSync` 开启智能同步机制，确保向量存储数据始终与用户配置的 source 保持一致，支持增量处理、变更检测和孤儿文档自动清理

### Agent 集成

Knowledge 系统与 Agent 的集成方式：

- **自动工具注册**：使用 `WithKnowledge()` 选项自动添加 `knowledge_search` 工具
- **智能过滤工具**：使用 `WithEnableKnowledgeAgenticFilter(true)` 启用 `knowledge_search_with_agentic_filter` 工具
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
        knowledge.WithEnableSourceSync(true), // 启用增量同步，保持向量存储与源一致
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
}
```

### 手动调用示例

```go

package main

import (
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    vectorelasticsearch "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/elasticsearch"
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

// 创建支持多版本 (v7, v8, v9) 的 Elasticsearch 向量存储
esVS, err := vectorelasticsearch.New(
    vectorelasticsearch.WithAddresses([]string{"http://localhost:9200"}),
    vectorelasticsearch.WithUsername(os.Getenv("ELASTICSEARCH_USERNAME")),
    vectorelasticsearch.WithPassword(os.Getenv("ELASTICSEARCH_PASSWORD")),
    vectorelasticsearch.WithAPIKey(os.Getenv("ELASTICSEARCH_API_KEY")),
    vectorelasticsearch.WithIndexName(getEnvOrDefault("ELASTICSEARCH_INDEX_NAME", "trpc_agent_documents")),
    vectorelasticsearch.WithMaxRetries(3),
    // 版本可选："v7"、"v8"、"v9"（默认 "v9"）
    vectorelasticsearch.WithVersion("v9"),
    // 用于文档检索时的自定义文档构建方法。若不提供，则使用默认构建方法。
    vectorelasticsearch.WithDocBuilder(docBuilder),
)
if err != nil {
    // 处理 error
}

embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"), // embedding 模型，也可通过 OPENAI_EMBEDDING_MODEL 环境变量设置
)

kb := knowledge.New(
    knowledge.WithVectorStore(esVS),
    knowledge.WithEmbedder(embedder),
)

// 注意：元数据字段需要使用 metadata. 前缀
filterCondition := &searchfilter.UniversalFilterCondition{
    Operator: searchfilter.OperatorAnd,
    Value: []*searchfilter.UniversalFilterCondition{
        {
            Field: "metadata.tag",  // 元数据字段使用 metadata. 前缀
            Operator: searchfilter.OperatorEqual,
            Value: "tag",
        },
        {
            Field: "metadata.age",
            Operator: searchfilter.OperatorGreaterThanOrEqual,
            Value: 18,
        },
        {
            Field: "metadata.create_time",
            Operator: searchfilter.OperatorBetween,
            Value: []string{"2024-10-11 12:11:00", "2025-10-11 12:11:00"},
        },
        {
            Operator: searchfilter.OperatorOr,
            Value: []*searchfilter.UniversalFilterCondition{
                {
                    Field: "metadata.login_time",
                    Operator: searchfilter.OperatorLessThanOrEqual,
                    Value: "2025-01-11 12:11:00",
                },
                {
                    Field: "metadata.status",
                    Operator: searchfilter.OperatorEqual,
                    Value: "logout",
                },
            },
        },
    },
}

req := &knowledge.SearchRequest{
    Query: "any text"
    MaxResults: 5,
    MinScore: 0.7,
    SearchFilter: &knowledge.SearchFilter{
        DocumentIDs: []string{"id1","id2"},
        Metadata: map[string]any{
            "title": "title test",
        },
        FilterCondition: filterCondition,
    }
}
searchResult, err := kb.Search(ctx, req)
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
├── reranker/             # 结果重排
│   ├── reranker.go      # Reranker 接口定义
│   ├── topk.go          # 返回topK的检索结果
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

Knowledge 系统提供了两种与 Agent 集成的方式：自动集成和手动构建工具。

#### 方式一：自动集成（推荐）

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

#### 方式二：手动构建工具

使用手动构建SearchTool的方法来配置知识库，通过这个方法可以构建多个知识库

**使用 NewKnowledgeSearchTool 创建基础搜索工具：**

```go
import (
    knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
)

// 创建 Knowledge
// kb := ...

// 创建基础搜索工具
searchTool := knowledgetool.NewKnowledgeSearchTool(
    kb,                    // Knowledge 实例
    knowledgetool.WithToolName("knowledge_search"),
    knowledgetool.WithToolDescription("Search for relevant information in the knowledge base."),
)

// 创建 Agent 并手动添加工具
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools([]tool.Tool{searchTool}),
)
```

**使用 NewAgenticFilterSearchTool 创建智能过滤搜索工具：**

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
)

// 获取源的元数据信息（用于智能过滤）
sourcesMetadata := source.GetAllMetadata(sources)

// 创建智能过滤搜索工具
filterSearchTool := knowledgetool.NewAgenticFilterSearchTool(
    kb,                    // Knowledge 实例
    sourcesMetadata,       // 元数据信息
    knowledgetool.WithToolName("knowledge_search_with_filter"),
    knowledgetool.WithToolDescription("Search the knowledge base with intelligent metadata filtering."),
)

llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools([]tool.Tool{filterSearchTool}),
)
```

### 向量存储 (VectorStore)

向量存储可在代码中通过选项配置，配置来源可以是配置文件、命令行参数或环境变量，用户可以自行实现。

trpc-agent-go 支持多种向量存储实现：

- **Memory**：内存向量存储，适用于测试和小规模数据
- **PgVector**：基于 PostgreSQL + pgvector 扩展的向量存储，支持混合检索
- **TcVector**：腾讯云向量数据库，支持远程 embedding 计算和混合检索
- **Elasticsearch**：支持 v7/v8/v9 多版本的 Elasticsearch 向量存储

#### 向量存储配置示例

##### Memory（内存向量存储）

```go
import (
    vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
)

// 内存实现，适用于测试和小规模数据
memVS := vectorinmemory.New()

kb := knowledge.New(
    knowledge.WithVectorStore(memVS),
    knowledge.WithEmbedder(embedder), // 需要配置本地 embedder
)
```

##### PgVector（PostgreSQL + pgvector）

```go
import (
    vectorpgvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
)

// PostgreSQL + pgvector
pgVS, err := vectorpgvector.New(
    vectorpgvector.WithHost("127.0.0.1"),
    vectorpgvector.WithPort(5432),
    vectorpgvector.WithUser("postgres"),
    vectorpgvector.WithPassword("your-password"),
    vectorpgvector.WithDatabase("your-database"),
    // 根据 embedding 模型设置索引维度（text-embedding-3-small 为 1536）
    vectorpgvector.WithIndexDimension(1536),
    // 启用/关闭文本检索向量，配合混合检索权重使用
    vectorpgvector.WithEnableTSVector(true),
    // 调整混合检索权重（向量相似度权重与文本相关性权重）
    vectorpgvector.WithHybridSearchWeights(0.7, 0.3),
    // 如安装了中文分词扩展（如 zhparser/jieba），可设置语言以提升文本召回
    vectorpgvector.WithLanguageExtension("english"),
)
if err != nil {
    // 处理 error
}

kb := knowledge.New(
    knowledge.WithVectorStore(pgVS),
    knowledge.WithEmbedder(embedder), // 需要配置本地 embedder
)
```

##### TcVector（腾讯云向量数据库）

TcVector 支持两种 embedding 模式：

**1. 本地 Embedding 模式（默认）**

使用本地 embedder 计算向量，然后存储到 TcVector：

```go
import (
    vectortcvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
)

docBuilder := func(tcDoc tcvectordb.Document) (*document.Document, []float64, error) {
    return &document.Document{ID: tcDoc.Id}, nil, nil
}

// 本地 embedding 模式
tcVS, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-tcvector-endpoint"),
    vectortcvector.WithUsername("your-username"),
    vectortcvector.WithPassword("your-password"),
    // 用于文档检索时的自定义文档构建方法。若不提供，则使用默认构建方法
    vectortcvector.WithDocBuilder(docBuilder),
)
if err != nil {
    // 处理 error
}

kb := knowledge.New(
    knowledge.WithVectorStore(tcVS),
    knowledge.WithEmbedder(embedder), // 需要配置本地 embedder
)
```

**2. 远程 Embedding 模式**

使用 TcVector 云端 embedding 计算，无需本地 embedder，节省资源：

```go
import (
    vectortcvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
)

// 远程 embedding 模式
tcVS, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-tcvector-endpoint"),
    vectortcvector.WithUsername("your-username"),
    vectortcvector.WithPassword("your-password"),
    // 启用远程 embedding 计算
    vectortcvector.WithEnableRemoteEmbedding(true),
    // 指定 TcVector 的 embedding 模型（如 bge-base-zh）
    vectortcvector.WithRemoteEmbeddingModel("bge-base-zh"),
    // 如需混合检索，需启用 TSVector
    vectortcvector.WithEnableTSVector(true),
)
if err != nil {
    // 处理 error
}

kb := knowledge.New(
    knowledge.WithVectorStore(tcVS),
    // 注意：使用远程 embedding 时，不需要配置 embedder
    // knowledge.WithEmbedder(embedder), // 不需要
)
```

#### Elasticsearch

```go

docBuilder := func(hitSource json.RawMessage) (*document.Document, []float64, error) {
    var source struct {
        ID        string    `json:"id"`
        Title     string    `json:"title"`
        Content   string    `json:"content"`
        Page      int       `json:"page"`
        Author    string    `json:"author"`
        CreatedAt time.Time `json:"created_at"`
        UpdatedAt time.Time `json:"updated_at"`
        Embedding []float64 `json:"embedding"`
    }
    if err := json.Unmarshal(hitSource, &source); err != nil {
        return nil, nil, err
    }
    // Create document.
    doc := &document.Document{
        ID:        source.ID,
        Name:      source.Title,
        Content:   source.Content,
        CreatedAt: source.CreatedAt,
        UpdatedAt: source.UpdatedAt,
        Metadata: map[string]any{
            "page":   source.Page,
            "author": source.Author,
        },
    }
    return doc, source.Embedding, nil
}

// 创建支持多版本 (v7, v8, v9) 的 Elasticsearch 向量存储
esVS, err := vectorelasticsearch.New(
    vectorelasticsearch.WithAddresses([]string{"http://localhost:9200"}),
    vectorelasticsearch.WithUsername(os.Getenv("ELASTICSEARCH_USERNAME")),
    vectorelasticsearch.WithPassword(os.Getenv("ELASTICSEARCH_PASSWORD")),
    vectorelasticsearch.WithAPIKey(os.Getenv("ELASTICSEARCH_API_KEY")),
    vectorelasticsearch.WithIndexName(getEnvOrDefault("ELASTICSEARCH_INDEX_NAME", "trpc_agent_documents")),
    vectorelasticsearch.WithMaxRetries(3),
    // 版本可选："v7"、"v8"、"v9"（默认 "v9"）
    vectorelasticsearch.WithVersion("v9"),
    // 用于文档检索时的自定义文档构建方法。若不提供，则使用默认构建方法。
    vectorelasticsearch.WithDocBuilder(docBuilder),
)
if err != nil {
    // 处理 error
}

kb := knowledge.New(
    knowledge.WithVectorStore(esVS),
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

### Reranker

Reranker 负责对检索结果的精排：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
)

rerank := reranker.NewTopKReranker(
    reranker.WithK(1), // 指定精排后的返回结果数，不设置的情况下默认返回所有结果
)

// 传递给 Knowledge
kb := knowledge.New(
    knowledge.WithReranker(rerank),
)
```

**支持的 embedding 模型**：

- OpenAI embedding 模型（text-embedding-3-small 等）
- 其他兼容 OpenAI API 的 embedding 服务
- Gemini embedding 模型（通过 `knowledge/embedder/gemini`）
- Ollama embedding 模型 (通过 `knowledge/embedder/ollama`）
- hugging_face text_embedding_interface 模型 (通过 `knowledge/embedder/hugging_face`）

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

// URL 源高级配置：分离内容获取和文档标识
urlSrcAlias := urlsource.New(
    []string{"https://trpc-go.com/docs/api.md"},     // 标识符 URL（用于文档 ID 和元数据）
    urlsource.WithContentFetchingURL([]string{"https://github.com/trpc-group/trpc-go/raw/main/docs/api.md"}), // 实际内容获取 URL
    urlsource.WithName("TRPC API Docs"),
    urlsource.WithMetadataValue("source", "github"),
)
// 注意：使用 WithContentFetchingURL 时，标识符 URL 应保留获取内容的URL的文件信息，比如
// 正确：标识符 URL 为 https://trpc-go.com/docs/api.md，获取 URL 为 https://github.com/.../docs/api.md
// 错误：标识符 URL 为 https://trpc-go.com，会丢失文档路径信息

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

> 关于性能与限流：
>
> - 提高并发会增加对 Embedder 服务（OpenAI/Gemini）的调用频率，可能触发限流；
> - 请根据吞吐、成本与限流情况调节 `WithSourceConcurrency()`、`WithDocConcurrency()`；
> - 默认值在多数场景下较为均衡；需要更快速度可适当上调，遇到限流则下调。

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

**重要**：Agent 级过滤器的优先级高于 Runner 级过滤器，相同键的值会被 Agent 级覆盖：

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

// Runner 级过滤器的同名键会被 Agent 级覆盖
eventCh, err := runner.Run(
    ctx, userID, sessionID, message,
    agent.WithKnowledgeFilter(map[string]interface{}{
        "source": "external",  // 会被 Agent 级的 "internal" 覆盖
        "topic":  "api",       // 新增过滤条件（Agent 级没有此键）
    }),
)

// 最终生效的过滤器：
// {
//     "category": "general",   // 来自 Agent 级
//     "source":   "internal",  // 来自 Agent 级（覆盖了 Runner 级的 "external"）
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

#### 过滤器层级

Knowledge 系统支持多层过滤器，所有过滤器统一使用 FilterCondition 实现，通过 **AND 逻辑**组合。系统不区分优先级，所有层级的过滤器平等合并。

**过滤器层级**：

1. **Agent 级过滤器**：
   - 通过 `llmagent.WithKnowledgeFilter()` 设置元数据过滤器
   - 通过 `llmagent.WithKnowledgeConditionedFilter()` 设置复杂条件过滤器

2. **Tool 级过滤器**：
   - 通过 `tool.WithFilter()` 设置元数据过滤器
   - 通过 `tool.WithConditionedFilter()` 设置复杂条件过滤器
   - 注：Agent 级过滤器实际上是通过 Tool 级过滤器实现的

3. **Runner 级过滤器**：
   - 通过 `agent.WithKnowledgeFilter()` 在 `runner.Run()` 时传递元数据过滤器
   - 通过 `agent.WithKnowledgeConditionedFilter()` 在 `runner.Run()` 时传递复杂条件过滤器

4. **LLM 智能过滤器**：
   - LLM 根据用户查询动态生成的过滤条件（仅支持复杂条件过滤器）

> **重要说明**：
> - 所有过滤器通过 **AND 逻辑**组合，即必须同时满足所有层级的过滤条件
> - 不存在优先级覆盖关系，所有过滤器都是平等的约束条件
> - 每个层级都支持元数据过滤器和复杂条件过滤器（LLM 除外，仅支持复杂条件）

##### 示例：过滤器组合

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"

// 1. Agent 级过滤器
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithKnowledge(kb),
    // Agent 级元数据过滤器
    llmagent.WithKnowledgeFilter(map[string]any{
        "source":   "official",      // 官方来源
        "category": "documentation", // 文档类别
    }),
    // Agent 级复杂条件过滤器（元数据字段使用 metadata. 前缀）
    llmagent.WithKnowledgeConditionedFilter(
        searchfilter.Equal("metadata.status", "published"), // 已发布状态
    ),
)

// 2. Runner 级过滤器
eventCh, err := runner.Run(
    ctx, userID, sessionID, message,
    // Runner 级元数据过滤器
    agent.WithKnowledgeFilter(map[string]any{
        "region":   "china",  // 中国区域
        "language": "zh",     // 中文
    }),
    // Runner 级复杂条件过滤器
    agent.WithKnowledgeConditionedFilter(
        searchfilter.GreaterThan("metadata.priority", 5), // 优先级大于 5
    ),
)

// 3. LLM 智能过滤器（由 LLM 动态生成）
// 例如：用户问 "查找 API 相关文档"，LLM 可能生成 {"field": "metadata.topic", "value": "api"}

// 最终生效的过滤条件（所有条件通过 AND 组合）：
// metadata.source = "official" AND
// metadata.category = "documentation" AND
// metadata.status = "published" AND
// metadata.region = "china" AND
// metadata.language = "zh" AND
// metadata.priority > 5 AND
// metadata.topic = "api"
//
// 即：必须同时满足所有层级的所有条件
```

##### 复杂条件过滤器示例

```go
// 手动创建带有复杂条件过滤器的 Tool
searchTool := tool.NewKnowledgeSearchTool(
    kb,
    // Agent 级元数据过滤器
    tool.WithFilter(map[string]any{
        "source": "official",
    }),
    // Agent 级复杂条件过滤器（元数据字段使用 metadata. 前缀）
    tool.WithConditionedFilter(
        searchfilter.Or(
            searchfilter.Equal("metadata.topic", "programming"),
            searchfilter.Equal("metadata.topic", "llm"),
        ),
    ),
)

llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools(searchTool),  // 手动传递 Tool
)

// 最终过滤条件：
// metadata.source = "official" AND (metadata.topic = "programming" OR metadata.topic = "llm")
// 即：必须是官方来源，且主题是编程或 LLM
```

##### 过滤器字段命名规范

使用 `FilterCondition` 时，**元数据字段必须使用 `metadata.` 前缀**：

```go
// ✅ 正确：使用 metadata. 前缀
searchfilter.Equal("metadata.topic", "programming")
searchfilter.Equal("metadata.category", "documentation")

// ❌ 错误：缺少 metadata. 前缀
searchfilter.Equal("topic", "programming")
```

> **说明**：
> - `metadata.` 前缀用于区分元数据字段和系统字段（如 `id`、`name`、`content` 等）
> - 如果通过 `WithMetadataField()` 自定义了元数据字段名，仍然使用 `metadata.` 前缀，框架会自动转换为实际的字段名
> - 系统字段（`id`、`name`、`content`、`created_at`、`updated_at`）直接使用字段名，无需前缀

##### 常用过滤器辅助函数

```go
// 比较操作符（注意：元数据字段需要 metadata. 前缀）
searchfilter.Equal("metadata.topic", value)              // metadata.topic = value
searchfilter.NotEqual("metadata.status", value)          // metadata.status != value
searchfilter.GreaterThan("metadata.priority", value)     // metadata.priority > value
searchfilter.GreaterThanOrEqual("metadata.score", value) // metadata.score >= value
searchfilter.LessThan("metadata.age", value)             // metadata.age < value
searchfilter.LessThanOrEqual("metadata.level", value)    // metadata.level <= value
searchfilter.In("metadata.category", values...)          // metadata.category IN (...)
searchfilter.NotIn("metadata.type", values...)           // metadata.type NOT IN (...)
searchfilter.Like("metadata.title", pattern)             // metadata.title LIKE pattern
searchfilter.Between("metadata.date", min, max)          // metadata.date BETWEEN min AND max

// 系统字段不需要前缀
searchfilter.Equal("id", "doc-123")                      // id = "doc-123"
searchfilter.In("name", "doc1", "doc2")                  // name IN ("doc1", "doc2")

// 逻辑操作符
searchfilter.And(conditions...)               // AND 组合
searchfilter.Or(conditions...)                // OR 组合

// 嵌套示例：(metadata.status = 'published') AND (metadata.category = 'doc' OR metadata.category = 'tutorial')
searchfilter.And(
    searchfilter.Equal("metadata.status", "published"),
    searchfilter.Or(
        searchfilter.Equal("metadata.category", "documentation"),
        searchfilter.Equal("metadata.category", "tutorial"),
    ),
)
```

#### 多文档返回

Knowledge Search Tool 支持返回多个相关文档，可通过 `WithMaxResults(n)` 选项限制返回的最大文档数量：

```go
// 创建搜索工具，限制最多返回 5 个文档
searchTool := tool.NewKnowledgeSearchTool(
    kb,
    tool.WithMaxResults(5),
)

// 或使用智能过滤搜索工具
agenticSearchTool := tool.NewAgenticFilterSearchTool(
    kb,
    sourcesMetadata,
    tool.WithMaxResults(10),
)
```

每个返回的文档包含文本内容、元数据和相关性分数，按分数降序排列

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

- ✅ 支持所有元数据过滤
- ✅ v0.4.0+ 新建集合自动支持 JSON 索引（需 TCVector 服务支持）
- ⚡ 可选：使用 `WithFilterIndexFields` 为高频字段构建额外索引

```go
// v0.4.0+ 新建集合（TCVector 服务支持 JSON 索引）
vectorStore, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-endpoint"),
    // ... 其他配置
)
// 所有元数据字段可通过 JSON 索引查询，无需预定义

// 可选：为高频字段构建额外索引以优化性能
metadataKeys := source.GetAllMetadataKeys(sources)
vectorStore, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-endpoint"),
    vectortcvector.WithFilterIndexFields(metadataKeys), // 可选：构建额外索引
    // ... 其他配置
)

// v0.4.0 之前的集合或 TCVector 服务不支持 JSON 索引
vectorStore, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-endpoint"),
    vectortcvector.WithFilterIndexFields(metadataKeys), // 必需：预定义过滤字段
    // ... 其他配置
)
```

**说明：**
- **v0.4.0+ 新建集合**：自动创建 metadata JSON 索引，所有字段可查询
- **旧版本集合**：仅支持 `WithFilterIndexFields` 中预定义的字段

#### 内存存储

- ✅ 支持所有过滤器功能
- ⚠️ 仅适用于开发和测试

### 知识库管理功能

Knowledge 系统提供了强大的知识库管理功能，支持动态源管理和智能同步机制。

#### 启用源同步 (enableSourceSync)

通过启用 `enableSourceSync`，知识库会始终保持向量存储数据和配置的数据源一致，这里如果没有使用自定义的办法来管理知识库，建议开启此选项：

```go
kb := knowledge.New(
    knowledge.WithEmbedder(embedder),
    knowledge.WithVectorStore(vectorStore),
    knowledge.WithSources(sources),
    knowledge.WithEnableSourceSync(true), // 启用增量同步
)
```

**同步机制的工作原理**：

1. **加载前准备**：刷新文档信息缓存，建立同步状态跟踪
2. **处理过程跟踪**：记录已处理的文档，避免重复处理
3. **加载后清理**：自动清理不再存在的孤儿文档

**启用同步的优势**：

- **数据一致性**：确保向量存储与源配置完全同步
- **增量更新**：只处理变更的文档，提升性能
- **孤儿清理**：自动删除已移除源的相关文档
- **状态跟踪**：实时监控同步状态和处理进度

#### 动态源管理

Knowledge 支持运行时动态管理知识源，确保向量存储中的数据始终与用户配置的 source 保持一致：

```go
// 添加新的知识源 - 数据将与配置的源保持同步
newSource := filesource.New([]string{"./new-docs/api.md"})
if err := kb.AddSource(ctx, newSource); err != nil {
    log.Printf("Failed to add source: %v", err)
}

// 重新加载指定的知识源 - 自动检测变更并同步
if err := kb.ReloadSource(ctx, newSource); err != nil {
    log.Printf("Failed to reload source: %v", err)
}

// 移除指定的知识源 - 精确删除相关文档
if err := kb.RemoveSource(ctx, "API Documentation"); err != nil {
    log.Printf("Failed to remove source: %v", err)
}
```

**动态管理的核心特点**：

- **数据一致性保证**：向量存储数据始终与用户配置的 source 保持一致
- **智能增量同步**：只处理变更的文档，避免重复处理
- **精确源控制**：支持按源名称精确添加/移除/重载
- **孤儿文档清理**：自动清理不再属于任何配置源的文档
- **热更新支持**：无需重启应用即可更新知识库

#### 知识库状态监控

Knowledge 提供了丰富的状态监控功能，帮助用户了解当前配置源的同步状态：

```go
// 显示所有文档信息
docInfos, err := kb.ShowDocumentInfo(ctx)
if err != nil {
    log.Printf("Failed to show document info: %v", err)
    return
}

// 按源名称过滤显示
docInfos, err = kb.ShowDocumentInfo(ctx,
    knowledge.WithShowDocumentInfoSourceName("APIDocumentation"))
if err != nil {
    log.Printf("Failed to show source documents: %v", err)
    return
}

// 按文档ID过滤显示
docInfos, err = kb.ShowDocumentInfo(ctx,
    knowledge.WithShowDocumentInfoIDs([]string{"doc1", "doc2"}))
if err != nil {
    log.Printf("Failed to show specific documents: %v", err)
    return
}

// 遍历显示文档信息
for _, docInfo := range docInfos {
    fmt.Printf("Document ID: %s\n", docInfo.DocumentID)
    fmt.Printf("Source: %s\n", docInfo.SourceName)
    fmt.Printf("URI: %s\n", docInfo.URI)
    fmt.Printf("Chunk Index: %d\n", docInfo.ChunkIndex)
}
```

**状态监控输出示例**：

```
Document ID: a1b2c3d4e5f6...
Source: Technical Documentation
URI: /docs/api/authentication.md
Chunk Index: 0

Document ID: f6e5d4c3b2a1...
Source: Technical Documentation
URI: /docs/api/authentication.md
Chunk Index: 1
```

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
	ollamaembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/ollama"
	huggingfaceembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/huggingface"

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
        embedderType    = flag.String("embedder", "openai", "ollama", "embedder type (openai, gemini, ollama,huggingface)")
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
	case "ollama":
		embedder, err = ollamaembedder.New()
		if err != nil {
			log.Fatalf("Failed to create ollama embedder: %v", err)
        }
	case "huggingface":
		embedder = huggingfaceembedder.New()
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

    // 11. 演示知识库管理功能 - 查看文档元数据
    log.Println("📊 显示当前知识库状态...")

    // 查询所有文档的元数据信息，也支持查询指定 source 或者 metadata 的数据信息
    docInfos, err := kb.ShowDocumentInfo(ctx)
    if err != nil {
        log.Printf("Failed to show document info: %v", err)
    } else {
        log.Printf("知识库中总共有 %d 个文档块", len(docInfos))
    }


    // 12. 演示动态添加源 - 新数据将自动与配置保持同步
    log.Println("演示动态添加 source ...")
    newSource := filesource.New(
        []string{"./new-docs/changelog.md"},
        filesource.WithName("Changelog"),
        filesource.WithMetadataValue("category", "changelog"),
        filesource.WithMetadataValue("type", "updates"),
    )

    if err := kb.AddSource(ctx, newSource); err != nil {
        log.Printf("Failed to add new source: %v", err)
    }

    // 13. 演示移除source（可选，取消注释以测试）
    // if err := kb.RemoveSource(ctx, "Changelog"); err != nil {
    //     log.Printf("Failed to remove source: %v", err)
    // }
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

# PostgreSQL + pgvector 配置（当使用 -vectorstore=pgvector 时必填）
export PGVECTOR_HOST="127.0.0.1"
export PGVECTOR_PORT="5432"
export PGVECTOR_USER="postgres"
export PGVECTOR_PASSWORD="your-password"
export PGVECTOR_DATABASE="vectordb"

# TcVector 配置（当使用 -vectorstore=tcvector 时必填）
export TCVECTOR_URL="https://your-tcvector-endpoint"
export TCVECTOR_USERNAME="your-username"
export TCVECTOR_PASSWORD="your-password"

# Elasticsearch 配置（当使用 -vectorstore=elasticsearch 时必填）
export ELASTICSEARCH_HOSTS="http://localhost:9200"
export ELASTICSEARCH_USERNAME=""
export ELASTICSEARCH_PASSWORD=""
export ELASTICSEARCH_API_KEY=""
export ELASTICSEARCH_INDEX_NAME="trpc_agent_documents"
```

### 命令行参数

```bash
# 运行示例时可以通过命令行参数选择组件类型
go run main.go -embedder openai -vectorstore inmemory
go run main.go -embedder gemini -vectorstore pgvector
go run main.go -embedder openai -vectorstore tcvector
go run main.go -embedder openai -vectorstore elasticsearch -es-version v9

# 参数说明：
# -embedder: 选择 embedder 类型 (openai, gemini, ollama,huggingface)， 默认为 openai
# -vectorstore: 选择向量存储类型 (inmemory, pgvector, tcvector, elasticsearch)，默认为 inmemory
# -es-version: 指定 Elasticsearch 版本 (v7, v8, v9)，仅当 vectorstore=elasticsearch 时有效
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
