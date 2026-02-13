# Reranker 精排

> **示例代码**: [examples/knowledge/reranker](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/reranker)

Reranker 用于对检索出的候选结果进行精排，提升相关性。可与任何向量存储搭配使用，通过 `knowledge.WithReranker` 注入。

## 注入 Knowledge

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge"

kb := knowledge.New(
    knowledge.WithReranker(rerank),
    // ... 其他配置（VectorStore、Embedder、Sources 等）
)
```

## 支持的 Reranker

### TopK（简单截断）
最基础的精排方式，仅按检索分数截取前 K 条。

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/topk"

rerank := topk.New(
    topk.WithK(3), // 返回前 3 条（默认 -1，返回全部）
)
```

| 配置项 | 说明 | 必填 |
|--------|------|------|
| `WithK(int)` | 返回结果数量，默认 `-1`（返回全部） | 否 |

### Cohere（SaaS Rerank）
使用 Cohere 提供的 Rerank 服务

```go
import (
    "log"

    "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/cohere"
)

rerank, err := cohere.New(
    cohere.WithAPIKey("your-api-key"),       // 必填：Cohere API Key
    cohere.WithModel("rerank-english-v3.0"), // 模型名称（默认 rerank-english-v3.0）
    cohere.WithTopN(5),                      // 返回结果数量
    cohere.WithEndpoint("https://..."),      // 可选：自定义端点（默认 https://api.cohere.ai/v1/rerank）
    cohere.WithHTTPClient(customClient),     // 可选：自定义 HTTP 客户端
)
if err != nil {
    log.Fatalf("Failed to create reranker: %v", err)
}
```

| 配置项 | 说明 | 必填 |
|--------|------|------|
| `WithAPIKey(string)` | Cohere API Key | 是 |
| `WithModel(string)` | 模型名称，默认 `rerank-english-v3.0` | 否 |
| `WithTopN(int)` | 返回结果数量 | 否 |
| `WithEndpoint(string)` | 自定义端点 URL | 否 |
| `WithHTTPClient(*http.Client)` | 自定义 HTTP 客户端 | 否 |

### Infinity / TEI（标准 Rerank API）

术语说明

+ Infinity: 开源高性能推理引擎，支持多种 Reranker 模型
+ TEI (Text Embeddings Inference): Hugging Face 官方推理引擎，专为 Embedding 和 Rerank 优化

trpc-agent-go 的 Infinity Reranker 实现可以连接任何兼容标准 Rerank API 的服务，包括使用 Infinity/TEI 自建的服务、Hugging Face Inference Endpoints 托管服务等。

使用方式

```go
import (
    "log"

    "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/infinity"
)

rerank, err := infinity.New(
    infinity.WithEndpoint("http://localhost:7997/rerank"), // 必填：服务端点
    infinity.WithModel("BAAI/bge-reranker-v2-m3"),         // 可选：模型名称
    infinity.WithTopN(5),                                  // 可选：返回结果数量（默认 -1，返回全部）
    infinity.WithAPIKey("your-api-key"),                   // 可选：API Key（用于需认证的服务）
    infinity.WithHTTPClient(customClient),                 // 可选：自定义 HTTP 客户端
)
if err != nil {
    log.Fatalf("Failed to create reranker: %v", err)
}
```

| 配置项 | 说明 | 必填 |
|--------|------|------|
| `WithEndpoint(string)` | 服务端点 URL | 是 |
| `WithModel(string)` | 模型名称 | 否 |
| `WithTopN(int)` | 返回结果数量，默认 `-1`（返回全部） | 否 |
| `WithAPIKey(string)` | API Key（用于需认证的服务） | 否 |
| `WithHTTPClient(*http.Client)` | 自定义 HTTP 客户端 | 否 |

详细的服务部署方法和示例请参考 [examples/knowledge/reranker/infinity/](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/reranker/infinity) 目录。
