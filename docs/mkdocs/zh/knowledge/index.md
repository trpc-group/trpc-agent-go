# Knowledge 使用文档

## 概述

Knowledge 是 tRPC-Agent-Go 框架中的知识管理系统，为 Agent 提供检索增强生成（Retrieval-Augmented Generation, RAG）能力。通过集成向量数据、embedding 模型和文档处理组件，Knowledge 系统能够帮助 Agent 访问和检索相关的知识信息，从而提供更准确、更有依据的响应。

### 使用模式

Knowledge 系统的使用遵循以下模式：

1. **创建 Knowledge**：配置向量存储、Embedder 和知识源
2. **加载文档**：从各种来源加载和索引文档
3. **创建搜索工具**：使用 `NewKnowledgeSearchTool` 创建知识搜索工具
4. **集成到 Agent**：将搜索工具添加到 Agent 的工具列表中
5. **知识库管理**：通过 `enableSourceSync` 启用智能同步机制，确保向量存储中的数据始终与用户配置的 source 保持一致

这种模式提供了：

- **智能检索**：基于向量相似度的语义搜索
- **多源支持**：支持文件、目录、URL 等多种知识来源
- **灵活存储**：支持内存、PostgreSQL、TcVector 等多种存储后端
- **高性能处理**：并发处理和批量文档加载
- **知识过滤**：通过元数据，支持知识的静态过滤和 Agent 智能过滤
- **可扩展架构**：支持自定义 Embedder、Retriever 和 Reranker

### Agent 集成

Knowledge 系统与 Agent 的集成方式：

- **手动创建工具（推荐）**：使用 `NewKnowledgeSearchTool` 创建搜索工具，灵活配置工具名称、描述，支持多知识库
- **智能过滤工具**：使用 `NewAgenticFilterSearchTool` 创建支持智能过滤的搜索工具
- **自动集成**：使用 `WithKnowledge()` 选项自动添加 `knowledge_search` 工具（简单场景）
- **上下文增强**：检索到的知识内容自动添加到 Agent 的上下文中
- **元数据过滤**：支持基于文档元数据进行精准搜索

## 快速开始

> **完整示例**: [examples/knowledge/basic](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/basic)

### 环境要求

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

    // 3. 创建知识源，能自动检测文件的格式
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
│   ├── file/            # 文件源实现
│   ├── dir/             # 目录源实现
│   ├── url/             # URL 源实现
│   └── auto/            # 自动源类型检测
├── vectorstore/          # 向量存储后端
│   ├── vectorstore.go   # VectorStore 接口定义
│   ├── inmemory/        # 内存向量存储（开发/测试用）
│   ├── pgvector/        # PostgreSQL + pgvector 实现
│   ├── tcvector/        # 腾讯云向量数据库实现
│   ├── elasticsearch/   # Elasticsearch 实现
│   ├── milvus/          # Milvus 向量数据库实现
│   └── qdrant/          # Qdrant 向量数据库实现
├── embedder/             # 文本 embedding 模型
│   ├── embedder.go      # Embedder 接口定义
│   ├── openai/          # OpenAI embedding 模型
│   ├── gemini/          # Gemini embedding 模型
│   ├── ollama/          # Ollama 本地 embedding 模型
│   └── huggingface/     # HuggingFace embedding 模型
├── reranker/             # 结果重排
│   ├── reranker.go      # Reranker 接口定义
│   └── topk.go          # 返回 TopK 的检索结果
├── document/             # 文档处理
│   ├── document.go      # Document 结构定义
│   └── reader/          # 文档读取器（支持 txt/md/csv/json/docx/pdf 等格式）
├── query/                # 查询增强器
│   ├── query.go         # QueryEnhancer 接口定义
│   └── passthrough.go   # 默认透传增强器
└── ocr/                  # OCR 文字识别
    ├── ocr.go           # Extractor 接口定义
    └── tesseract/       # Tesseract OCR 实现（独立 go.mod）
```

## 与 Agent 集成

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

### 智能过滤搜索工具

使用 `NewAgenticFilterSearchTool` 创建支持 Agent 动态过滤的搜索工具，Agent 可以根据用户查询自动构建过滤条件：

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

### 搜索工具配置选项

`NewKnowledgeSearchTool` 和 `NewAgenticFilterSearchTool` 都支持以下配置选项：

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithToolName(name)` | 设置工具名称 | `"knowledge_search"` / `"knowledge_search_with_agentic_filter"` |
| `WithToolDescription(desc)` | 设置工具描述 | 默认描述 |
| `WithMaxResults(n)` | 设置返回的最大文档数量 | `10` |
| `WithMinScore(score)` | 设置最小相关性分数阈值（0.0-1.0），低于此分数的文档将被过滤 | `0.0` |
| `WithFilter(map)` | 设置静态元数据过滤（简单 AND 逻辑） | `nil` |
| `WithConditionedFilter(cond)` | 设置复杂过滤条件（支持 AND/OR/嵌套逻辑） | `nil` |

```go
// 配置示例：限制返回结果数量和最小分数
searchTool := knowledgetool.NewKnowledgeSearchTool(
    kb,
    knowledgetool.WithToolName("knowledge_search"),
    knowledgetool.WithMaxResults(5),           // 最多返回 5 条结果
    knowledgetool.WithMinScore(0.7),           // 只返回相关性 >= 0.7 的结果
    knowledgetool.WithFilter(map[string]any{   // 静态过滤：只搜索特定类别
        "category": "documentation",
    }),
)
```

## 更多内容

- [向量存储](vectorstore/index.md) - 配置各种向量数据库后端
- [Embedder](embedder.md) - 文本向量化模型配置
- [文档源](source.md) - 文件、目录、URL 等知识来源配置
- [过滤器](filter.md) - 基础过滤器和智能过滤器
- [知识库管理](management.md) - 动态源管理和状态监控
