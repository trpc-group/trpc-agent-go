# Knowledge 使用文档

## 概述

Knowledge 是 tRPC-Agent-Go 框架中的知识管理系统，为 Agent 提供检索增强生成（Retrieval-Augmented Generation, RAG）能力。通过集成向量数据、embedding 模型和文档处理组件，Knowledge 系统能够帮助 Agent 访问和检索相关的知识信息，从而提供更准确、更有依据的响应。

### 使用模式

Knowledge 系统的使用遵循以下模式：

1. **创建 Knowledge**：配置向量存储、Embedder 和知识源
2. **加载文档**：从各种来源加载和索引文档
3. **创建搜索工具**：使用 `NewKnowledgeSearchTool` 创建知识搜索工具
4. **集成到 Agent**：将搜索工具添加到 Agent 的工具列表中
5. **知识库管理**：通过 `knowledge.WithEnableSourceSync(true)` 启用智能同步机制，确保向量存储中的数据始终与用户配置的 source 保持一致（详见 [知识库管理](management.md)）

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
    modelInstance := openai.New("gpt-4o")
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
│   ├── topk/            # TopK 简单截断实现
│   ├── cohere/          # Cohere SaaS Rerank 实现
│   └── infinity/        # Infinity/TEI 标准 Rerank API 实现
├── transform/            # 内容转换器
│   ├── transform.go     # Transformer 接口定义
│   ├── charfilter.go    # 字符过滤器（移除指定字符）
│   └── chardedup.go     # 字符去重器（合并连续重复字符）
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

Knowledge 系统提供了搜索工具，可以将知识库能力集成到 Agent 中。

### 构建 Knowledge 实例

在创建搜索工具之前，首先需要构建并加载 Knowledge 实例：

```go
simport (
    "context"
    "log"
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
)

// 创建 Knowledge 实例
kb := knowledge.New(
    knowledge.WithEmbedder(embedder),       // 必选，配置 Embedder
    knowledge.WithVectorStore(vectorStore), // 必选，配置向量存储
    knowledge.WithSources(sources),         // 必选，配置数据源
)

// 加载数据（重要：创建 Tool 前需确保数据已加载）
if err := kb.Load(context.Background()); err != nil {
    log.Fatalf("Knowledge load failed: %v", err)
}
```

### 搜索工具

#### KnowledgeSearchTool

基础搜索工具，支持语义搜索和静态过滤：

```go
import (
    knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
)

searchTool := knowledgetool.NewKnowledgeSearchTool(
    kb,
    knowledgetool.WithToolName("knowledge_search"),
    knowledgetool.WithToolDescription("Search for relevant information in the knowledge base."),
    knowledgetool.WithMaxResults(10),
    knowledgetool.WithMinScore(0.5),
)
```

#### AgenticFilterSearchTool

智能过滤搜索工具，Agent 可以根据用户查询自动构建过滤条件。

支持自动提取、手动指定枚举值、手动指定字段等多种配置方式：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
)

// 方式一（推荐）：自动获取所有源的元数据信息（用于智能过滤）
sourcesMetadata := source.GetAllMetadata(sources)

// 方式二：手动配置允许过滤的字段和值（适合枚举值有限）
// sourcesMetadata := map[string][]string{
//     "category": {"doc", "blog"},
//     "status":   {"published", "draft"},
// }

// 方式三：手动配置字段，值由 LLM 推断（适合枚举值过多）
// sourcesMetadata := map[string][]string{
//     "author_id": nil,
//     "year":      nil,
// }

filterSearchTool := knowledgetool.NewAgenticFilterSearchTool(
    kb,                    // Knowledge 实例
    sourcesMetadata,       // 元数据信息
    knowledgetool.WithToolName("knowledge_search_with_filter"),
    knowledgetool.WithToolDescription("Search the knowledge base with intelligent metadata filtering."),
    knowledgetool.WithMaxResults(10), // 返回最多 10 个结果
)
```

> 详细配置说明请参考 [过滤器文档](filter.md#启用智能过滤器)。

#### 搜索工具配置选项

`NewKnowledgeSearchTool` 和 `NewAgenticFilterSearchTool` 都支持以下配置选项：


| 选项                          | 说明                                                        | 默认值                                                          |
| ------------------------------- | ------------------------------------------------------------- | ----------------------------------------------------------------- |
| `WithToolName(name)`          | 设置工具名称                                                | `"knowledge_search"` / `"knowledge_search_with_agentic_filter"` |
| `WithToolDescription(desc)`   | 设置工具描述                                                | 默认描述                                                        |
| `WithMaxResults(n)`           | 设置返回的最大文档数量                                      | `10`                                                            |
| `WithMinScore(score)`         | 设置最小相关性分数阈值（0.0-1.0），低于此分数的文档将被过滤 | `0.0`                                                           |
| `WithFilter(map)`             | 设置静态元数据过滤（简单 AND 逻辑）                         | `nil`                                                           |
| `WithConditionedFilter(cond)` | 设置复杂过滤条件（支持 AND/OR/嵌套逻辑）                    | `nil`                                                           |

> **提示**: 每个返回的文档包含文本内容、元数据和相关性分数，按分数降序排列。

### 集成方式

#### 方式一：手动添加工具（推荐）

使用 `llmagent.WithTools` 手动添加搜索工具，可以灵活配置工具参数，支持同时集成多个知识库：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools([]tool.Tool{searchTool, filterSearchTool}),
)
```

#### 方式二：自动集成

使用 `llmagent.WithKnowledge(kb)` 将 Knowledge 集成到 Agent，框架会自动注册 `knowledge_search` 工具。

> **注意**：自动集成方式简单快捷，但灵活性较低，无法自定义工具名称、描述、过滤条件等参数，也不支持同时集成多个知识库。如需更精细的控制，建议使用手动添加工具的方式。

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
)

llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithKnowledge(kb), // 自动添加 knowledge_search 工具
)
```

## 加载时性能选项

Knowledge 支持批量文档处理和并发加载，可以显著提升大量文档的处理性能：

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge"

err := kb.Load(ctx,
    knowledge.WithShowProgress(true),      // 打印进度日志
    knowledge.WithProgressStepSize(10),    // 进度步长
    knowledge.WithShowStats(true),         // 打印统计信息
    knowledge.WithSourceConcurrency(4),    // 源级并发
    knowledge.WithDocConcurrency(64),      // 文档级并发
)
```

> **关于性能与限流**：
>
> - 提高并发会增加对 Embedder 服务（OpenAI/Gemini）的调用频率，可能触发限流
> - 请根据吞吐、成本与限流情况调节 `WithSourceConcurrency()`、`WithDocConcurrency()`
> - 默认值在多数场景下较为均衡；需要更快速度可适当上调，遇到限流则下调

## 评测对比

我们使用 [RAGAS](https://docs.ragas.io/) 框架对 tRPC-Agent-Go、LangChain、Agno 和 CrewAI 进行了全面的 RAG 质量评测。


> **详细文档**: 完整的评测方案、参数配置和结果分析请参考 [examples/knowledge/evaluation/README_CN.md](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/evaluation/README_CN.md)


### 评测方案

- **数据集**: HuggingFace 文档数据集（[m-ric/huggingface_doc](https://huggingface.co/datasets/m-ric/huggingface_doc)）
- **评估指标**: 7 项 RAGAS 标准指标（Faithfulness、Answer Relevancy、Context Precision 等）
- **对比对象**: tRPC-Agent-Go vs LangChain vs Agno vs CrewAI，使用相同的配置参数

### 配置对齐

为确保公平对比，四个系统使用完全相同的配置：

| 参数 | 配置值 |
|------|--------|
| **System Prompt** | 统一的 5 条核心准则提示词 |
| **Temperature** | 0 |
| **Chunk Size** | 500 |
| **Chunk Overlap** | 50 |
| **Embedding Model** | server:274214 (1024 维) |
| **Vector Store** | PGVector (CrewAI 使用 ChromaDB) |
| **Agent 模型** | DeepSeek-V3.2 |


## 更多内容

- [向量存储](vectorstore/index.md) - 配置各种向量数据库后端
- [Embedder](embedder.md) - 文本向量化模型配置
- [Reranker](reranker.md) - 检索结果精排
- [文档源](source.md) - 文件、目录、URL 等知识来源配置
- [OCR 图片文字识别](ocr.md) - 配置 Tesseract OCR 提取文本
- [过滤器](filter.md) - 基础过滤器和智能过滤器
- [知识库管理](management.md) - 动态源管理和状态监控
- [常见问题](troubleshooting.md) - 常见问题说明
