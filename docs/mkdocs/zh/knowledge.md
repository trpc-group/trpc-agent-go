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

> **完整示例**: [examples/knowledge/basic](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/basic)

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

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
    knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
    vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/tool"

    // 如需支持 PDF 文件，需手动引入 PDF reader（独立 go.mod，避免引入不必要的第三方依赖）
    // _ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
)

func main() {
    ctx := context.Background()

    // 1. 创建 embedder
    embedder := openaiembedder.New(
        openaiembedder.WithModel("text-embedding-3-small"),
    )

    // 2. 创建向量存储
    vectorStore := vectorinmemory.New()

    // 3. 创建知识源
    sources := []source.Source{
        filesource.New([]string{"./data/llm.md"}),
        dirsource.New([]string{"./dir"}),
    }

    // 4. 创建 Knowledge
    kb := knowledge.New(
        knowledge.WithEmbedder(embedder),
        knowledge.WithVectorStore(vectorStore),
        knowledge.WithSources(sources),
        knowledge.WithEnableSourceSync(true),
    )

    // 5. 加载文档
    if err := kb.Load(ctx); err != nil {
        log.Fatalf("Failed to load knowledge base: %v", err)
    }

    // 6. 创建搜索工具
    searchTool := knowledgetool.NewKnowledgeSearchTool(
        kb,
        knowledgetool.WithToolName("knowledge_search"),
        knowledgetool.WithToolDescription("Search for relevant information in the knowledge base."),
    )

    // 7. 创建 Agent 并添加工具
    modelInstance := openai.New("claude-4-sonnet-20250514")
    llmAgent := llmagent.New(
        "knowledge-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithTools([]tool.Tool{searchTool}),
    )

    // 8. 创建 Runner 并执行
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner("knowledge-chat", llmAgent, runner.WithSessionService(sessionService))

    message := model.NewUserMessage("请告诉我关于 LLM 的信息")
    _, err := appRunner.Run(ctx, "user123", "session456", message)
    if err != nil {
        log.Fatalf("Failed to run agent: %v", err)
    }
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

Knowledge 系统提供了两种与 Agent 集成的方式：手动构建工具和自动集成。

#### 方式一：手动构建工具（推荐）

使用 `NewKnowledgeSearchTool` 手动创建搜索工具，可以灵活配置工具名称、描述，并支持构建多个知识库。

```go
import (
    knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

// 创建搜索工具
searchTool := knowledgetool.NewKnowledgeSearchTool(
    kb,
    knowledgetool.WithToolName("knowledge_search"),
    knowledgetool.WithToolDescription("Search for relevant information in the knowledge base."),
)

// 创建 Agent 并添加工具
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools([]tool.Tool{searchTool}),
)
```

#### 方式二：自动集成

使用 `llmagent.WithKnowledge(kb)` 将 Knowledge 集成到 Agent，框架会自动注册 `knowledge_search` 工具。

```go
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithKnowledge(kb), // 自动添加 knowledge_search 工具
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

> **示例代码**: [examples/knowledge/vectorstores](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores)

向量存储可在代码中通过选项配置，配置来源可以是配置文件、命令行参数或环境变量，用户可以自行实现。

trpc-agent-go 支持多种向量存储实现：

- **Memory**：内存向量存储，适用于测试和小规模数据
- **PGVector**：基于 PostgreSQL + pgvector 扩展的向量存储，支持混合检索 - [示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/postgres)
- **TcVector**：腾讯云向量数据库，支持远程 embedding 计算和混合检索 - [示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/tcvector)
- **Elasticsearch**：支持 v7/v8/v9 多版本的 Elasticsearch 向量存储 - [示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/elasticsearch)
- **Milvus**：高性能向量数据库，支持十亿级向量搜索 - [示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/milvus)
- **Qdrant**：高性能向量数据库，支持高级过滤功能，支持云端和本地部署 - [示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/qdrant)

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

##### PGVector（PostgreSQL + pgvector）

```go
import (
    vectorpgvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
)

// PostgreSQL + pgvector
pgVS, err := vectorpgvector.New(
    vectorpgvector.WithPGVectorClientDSN("postgres://postgres:your-password@127.0.0.1:5432/your-database?sslmode=disable"),
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

##### Qdrant

[Qdrant](https://qdrant.tech/) 是一个高性能向量数据库，具有高级过滤功能，支持云端和本地部署。

**架构**

Qdrant 集成分为两个模块，以实现更好的职责分离：

- **`storage/qdrant`**: 底层客户端管理（连接、注册表、客户端构建器）
- **`knowledge/vectorstore/qdrant`**: 用于 Knowledge 的高级向量存储实现

**基础配置**

```go
import (
    vectorqdrant "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/qdrant"
)

// 本地 Qdrant 实例（默认：localhost:6334）
qdrantVS, err := vectorqdrant.New(ctx)
if err != nil {
    // 处理 error
}

// 自定义配置
qdrantVS, err := vectorqdrant.New(ctx,
    vectorqdrant.WithHost("qdrant.example.com"),
    vectorqdrant.WithPort(6334),
    vectorqdrant.WithCollectionName("my_documents"),
    vectorqdrant.WithDimension(1536),  // 必须与 embedding 模型匹配
)

kb := knowledge.New(
    knowledge.WithVectorStore(qdrantVS),
    knowledge.WithEmbedder(embedder),
)
```

**Qdrant Cloud 配置**

```go
qdrantVS, err := vectorqdrant.New(ctx,
    vectorqdrant.WithHost("xyz-abc.cloud.qdrant.io"),
    vectorqdrant.WithPort(6334),
    vectorqdrant.WithAPIKey(os.Getenv("QDRANT_API_KEY")),
    vectorqdrant.WithTLS(true),  // Qdrant Cloud 必需
    vectorqdrant.WithCollectionName("my_documents"),
    vectorqdrant.WithDimension(1536),
)
```

**使用 Storage 模块（高级用法）**

`storage/qdrant` 模块（`trpc.group/trpc-go/trpc-agent-go/storage/qdrant`）提供底层客户端管理，与向量存储实现分离。有两种使用方式：

1. **直接使用向量存储选项**：在向量存储上配置连接

```go
vs, err := vectorqdrant.New(ctx,
    vectorqdrant.WithHost("localhost"),
    vectorqdrant.WithPort(6334),
)
```

2. **使用 storage 模块**：创建客户端，实现多个向量存储共享

```go
client, err := qdrantstorage.NewClient(ctx,
    qdrantstorage.WithHost("localhost"),
    qdrantstorage.WithPort(6334),
)
vs, err := vectorqdrant.New(ctx, vectorqdrant.WithClient(client))
```

storage 模块还提供**注册表模式**，可在启动时注册命名实例（如 "test"、"prod"），在应用中通过名称获取。

**BM25 混合检索**

Qdrant 支持混合检索，结合稠密向量相似度和 BM25 关键词匹配，使用 Reciprocal Rank Fusion (RRF) 进行结果融合：

```go
qdrantVS, err := vectorqdrant.New(ctx,
    vectorqdrant.WithHost("localhost"),
    vectorqdrant.WithPort(6334),
    vectorqdrant.WithCollectionName("my_documents"),
    vectorqdrant.WithDimension(1536),
    vectorqdrant.WithBM25(true),  // 启用 BM25 混合检索
)
```

启用 BM25 后，向量存储会创建同时包含稠密向量和稀疏向量的集合。支持以下搜索模式：

- **向量检索**（默认）：稠密向量相似度搜索
- **关键词检索**：BM25 稀疏向量搜索（需要 `WithBM25(true)`）
- **混合检索**：使用 RRF 融合稠密和稀疏结果（需要 `WithBM25(true)`）
- **过滤检索**：仅基于元数据过滤，不使用向量相似度

> **BM25 集合重要说明：**
>
> - **集合兼容性**：启用 BM25 和未启用 BM25 的集合具有不同的向量配置。您不能在已有的非 BM25 集合上创建 `WithBM25(true)` 的向量存储，反之亦然。向量存储在启动时会验证集合配置，如果不匹配将返回错误。
> - **降级行为**：如果在未启用 BM25 的情况下尝试关键词或混合检索，关键词检索将返回错误，混合检索将降级为仅向量检索（如果配置了日志记录器，会输出警告日志）。
> - **配置一致性**：连接到现有集合时，请始终使用相同的 BM25 设置。如果您使用 `WithBM25(true)` 索引了文档，则在该集合上创建新的向量存储实例时也必须使用 `WithBM25(true)`。

**配置选项**

| 选项 | 默认值 | 说明 |
|------|--------|------|
| `WithClient(client)` | `nil` | 使用预创建的客户端（来自 storage 模块） |
| `WithHost(host)` | `"localhost"` | Qdrant 服务器主机名 |
| `WithPort(port)` | `6334` | Qdrant gRPC 端口（1-65535） |
| `WithAPIKey(key)` | `""` | Qdrant Cloud 认证 API 密钥 |
| `WithTLS(enabled)` | `false` | 启用 TLS（Qdrant Cloud 必需） |
| `WithCollectionName(name)` | `"trpc_agent_documents"` | 集合名称 |
| `WithDimension(dim)` | `1536` | 向量维度（必须与 embedding 模型匹配） |
| `WithDistance(d)` | `DistanceCosine` | 距离度量（Cosine、Euclid、Dot、Manhattan） |
| `WithMaxResults(max)` | `10` | 默认搜索结果数量 |
| `WithBM25(enabled)` | `false` | 启用 BM25 稀疏向量用于混合/关键词检索 |
| `WithPrefetchMultiplier(n)` | `3` | 混合检索融合的预取倍数 |
| `WithOnDiskVectors(enabled)` | `false` | 将向量存储在磁盘上（适用于大数据集） |
| `WithOnDiskPayload(enabled)` | `false` | 将负载存储在磁盘上 |
| `WithHNSWConfig(m, efConstruct)` | `16, 128` | HNSW 索引参数（越高 = 召回率越好，内存越多） |
| `WithMaxRetries(n)` | `3` | 瞬态 gRPC 错误最大重试次数 |
| `WithBaseRetryDelay(d)` | `100ms` | 初始重试延迟 |
| `WithMaxRetryDelay(d)` | `5s` | 最大重试延迟 |

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
- Gemini embedding 模型（通过 `knowledge/embedder/gemini`）
- Ollama embedding 模型 (通过 `knowledge/embedder/ollama`）
- hugging_face text_embedding_interface 模型 (通过 `knowledge/embedder/hugging_face`）

> **注意**:
>
> - Retriever 和 Reranker 目前由 Knowledge 内部实现，用户无需单独配置。Knowledge 会自动处理文档检索和结果排序。
> - `OPENAI_EMBEDDING_MODEL` 环境变量需要在代码中手动读取，框架不会自动读取。参考示例代码中的 `getEnvOrDefault("OPENAI_EMBEDDING_MODEL", "")` 实现。


### Reranker

> 📁 **示例代码**: [examples/knowledge/reranker](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/reranker)

Reranker 负责对检索结果的精排，trpc-agent-go 支持多种 Reranker 实现：

#### TopK (简单截断)

最基础的 Reranker，仅根据检索分数截取 Top K 结果：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/topk"
)

rerank := topk.New(
    topk.WithK(3), // 指定精排后的返回结果数
)
```

#### Cohere (SaaS Rerank)

使用 Cohere 官方 API 进行重排序，效果通常优于简单的向量检索：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/cohere"
)

// API key 通过 WithAPIKey 选项提供
rerank := cohere.New(
    cohere.WithAPIKey("your-api-key"),       // 必填：API key
    cohere.WithModel("rerank-english-v3.0"), // 指定模型
    cohere.WithTopN(5),                      // 最终返回数
)
```

#### Infinity / TEI

**术语说明**

- **Infinity**: 开源高性能推理引擎，支持多种 Reranker 模型
- **TEI (Text Embeddings Inference)**: Hugging Face 官方推理引擎，专为 Embedding 和 Rerank 优化

trpc-agent-go 的 Infinity Reranker 实现可以连接任何兼容标准 Rerank API 的服务，包括使用 Infinity/TEI 自建的服务、Hugging Face Inference Endpoints 托管服务等。

**使用方式**

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/infinity"
)

// 连接自建或托管的 Rerank 服务
reranker, err := infinity.New(
    infinity.WithEndpoint("http://localhost:7997/rerank"), // 必填：服务地址
    infinity.WithModel("BAAI/bge-reranker-v2-m3"),         // 可选：模型名称
    infinity.WithTopN(5),                                   // 可选：返回数量
)
if err != nil {
    log.Fatalf("Failed to create reranker: %v", err)
}
```

详细的服务部署方法和示例请参考 `examples/knowledge/reranker/infinity/` 目录。


#### Reranker 配置到 Knowledge

```go
kb := knowledge.New(
    knowledge.WithReranker(rerank),
    // ... 其他配置
)
```

### 文档源配置

> 📁 **示例代码**: [examples/knowledge/sources](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources)

源模块提供了多种文档源类型，每种类型都支持丰富的配置选项：

- **文件源 (file)**: 单个文件处理 - [示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/file-source)
- **目录源 (dir)**: 批量处理目录 - [示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/directory-source)
- **URL 源 (url)**: 从网页获取内容 - [示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/url-source)
- **自动源 (auto)**: 智能识别类型 - [示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/auto-source)

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
    autosource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
    vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
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

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))
vectorStore := vectorinmemory.New()

// 传递给 Knowledge
kb := knowledge.New(
    knowledge.WithEmbedder(embedder),
    knowledge.WithVectorStore(vectorStore),
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

> 📁 **示例代码**: [examples/knowledge/features/metadata-filter](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/metadata-filter)

Knowledge 系统提供了强大的过滤器功能，允许基于文档元数据进行精准搜索。这包括静态过滤器和智能过滤器两种模式。

> **重要：过滤器字段命名规范**
>
> 在使用过滤器时，**元数据字段建议使用 `metadata.` 前缀**：
> - `metadata.` 前缀用于区分元数据字段和系统字段（如 `id`、`name`、`content` 等）
> - 无论是 `WithKnowledgeFilter()`、`tool.WithFilter()` 还是 `searchfilter.Equal()` 等，元数据字段都建议加 `metadata.` 前缀
> - 如果通过 `WithMetadataField()` 自定义了元数据字段名，仍然使用 `metadata.` 前缀，框架会自动转换为实际的字段名
> - 通过 `WithDocBuilder` 自定义的表字段（如 `status`、`priority` 等额外列）直接使用字段名，无需前缀

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
        "metadata.category": "documentation",
        "metadata.topic":    "programming",
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
        "metadata.user_level": "premium",     // 根据用户级别过滤
        "metadata.region":     "china",       // 根据地区过滤
        "metadata.language":   "zh",          // 根据语言过滤
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
        "metadata.category": "general",
        "metadata.source":   "internal",
    }),
)

// Runner 级过滤器的同名键会被 Agent 级覆盖
eventCh, err := runner.Run(
    ctx, userID, sessionID, message,
    agent.WithKnowledgeFilter(map[string]interface{}{
        "metadata.source": "external",  // 会被 Agent 级的 "internal" 覆盖
        "metadata.topic":  "api",       // 新增过滤条件（Agent 级没有此键）
    }),
)

// 最终生效的过滤器：
// {
//     "metadata.category": "general",   // 来自 Agent 级
//     "metadata.source":   "internal",  // 来自 Agent 级（覆盖了 Runner 级的 "external"）
//     "metadata.topic":    "api",       // 来自 Runner 级（新增）
// }
```

### 智能过滤器 (Agentic Filter)

> 📁 **示例代码**: [examples/knowledge/features/agentic-filter](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/agentic-filter)

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
        "metadata.source":   "official",      // 官方来源
        "metadata.category": "documentation", // 文档类别
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
        "metadata.region":   "china",  // 中国区域
        "metadata.language": "zh",     // 中文
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
        "metadata.source": "official",
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

// 自定义表字段（通过 WithDocBuilder 添加的额外列）不需要前缀
searchfilter.NotEqual("status", "deleted")               // status != "deleted"
searchfilter.GreaterThanOrEqual("priority", 3)           // priority >= 3

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

#### Qdrant

- ✅ 支持所有元数据字段过滤
- ✅ 支持复杂查询条件（AND、OR、比较运算符）
- ✅ 支持 IN、NOT IN、LIKE、BETWEEN 运算符
- ✅ 自动重试瞬态错误

```go
vectorStore, err := vectorqdrant.New(ctx,
    vectorqdrant.WithHost("localhost"),
    vectorqdrant.WithPort(6334),
    // ... 其他配置
)
```

#### 内存存储

- ✅ 支持所有过滤器功能
- ⚠️ 仅适用于开发和测试

### 知识库管理功能

> 📁 **示例代码**: [examples/knowledge/features/management](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/management)

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

> 📁 **所有示例**: [examples/knowledge](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge)

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
    vectorqdrant "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/qdrant"
    vectortcvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"

    // 如需支持 PDF 文件，需手动引入 PDF reader（独立 go.mod，避免引入不必要的第三方依赖）
    // _ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
)

func main() {
    var (
        embedderType    = flag.String("embedder", "openai", "ollama", "embedder type (openai, gemini, ollama,huggingface)")
        vectorStoreType = flag.String("vectorstore", "inmemory", "vector store type (inmemory, pgvector, tcvector, qdrant)")
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
    case "qdrant":
        port, err := strconv.Atoi(getEnvOrDefault("QDRANT_PORT", "6334"))
        if err != nil {
            log.Fatalf("Failed to convert QDRANT_PORT to int: %v", err)
        }
        vectorStore, err = vectorqdrant.New(ctx,
            vectorqdrant.WithHost(getEnvOrDefault("QDRANT_HOST", "localhost")),
            vectorqdrant.WithPort(port),
            vectorqdrant.WithAPIKey(getEnvOrDefault("QDRANT_API_KEY", "")),
            vectorqdrant.WithTLS(getEnvOrDefault("QDRANT_TLS", "") == "true"),
            vectorqdrant.WithDimension(1536),
        )
        if err != nil {
            log.Fatalf("Failed to create qdrant store: %v", err)
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

# Qdrant 配置（当使用 -vectorstore=qdrant 时必填）
export QDRANT_HOST="localhost"          # 或 "xyz-abc.cloud.qdrant.io"（Qdrant Cloud）
export QDRANT_PORT="6334"
export QDRANT_API_KEY=""                # Qdrant Cloud 必需
export QDRANT_TLS="false"               # Qdrant Cloud 设置为 "true"
```

### 命令行参数

```bash
# 运行示例时可以通过命令行参数选择组件类型
go run main.go -embedder openai -vectorstore inmemory
go run main.go -embedder gemini -vectorstore pgvector
go run main.go -embedder openai -vectorstore tcvector
go run main.go -embedder openai -vectorstore elasticsearch -es-version v9
go run main.go -embedder openai -vectorstore qdrant

# 参数说明：
# -embedder: 选择 embedder 类型 (openai, gemini, ollama,huggingface)， 默认为 openai
# -vectorstore: 选择向量存储类型 (inmemory, pgvector, tcvector, elasticsearch, qdrant)，默认为 inmemory
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

7. **PDF 文件读取支持**

   - 说明：由于 PDF reader 依赖第三方库，为避免主模块引入不必要的依赖，PDF reader 采用独立 `go.mod` 管理。
   - 使用方式：如需支持 PDF 文件读取，需在代码中手动引入 PDF reader 包进行注册：
     ```go
     import (
         // 引入 PDF reader 以支持 .pdf 文件解析
         _ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
     )
     ```
   - 注意：其他格式（.txt/.md/.csv/.json 等）的 reader 已自动注册，无需手动引入。
