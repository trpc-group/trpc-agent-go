# Qdrant

> **示例代码**: [examples/knowledge/vectorstores/qdrant](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/qdrant)

[Qdrant](https://qdrant.tech/) 是一个高性能向量数据库，具有高级过滤功能，支持云端和本地部署。

## 基础配置

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
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

## Qdrant Cloud 配置

```go
qdrantVS, err := vectorqdrant.New(ctx,
    vectorqdrant.WithHost("xyz-abc.cloud.qdrant.io"),
    vectorqdrant.WithPort(6334),
    vectorqdrant.WithAPIKey("your-api-key"),
    vectorqdrant.WithTLS(true),  // Qdrant Cloud 必需
    vectorqdrant.WithCollectionName("my_documents"),
    vectorqdrant.WithDimension(1536),
)
```

## 配置选项

### 连接配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithHost(host)` | Qdrant 服务器主机名 | `"localhost"` |
| `WithPort(port)` | Qdrant gRPC 端口（1-65535） | `6334` |
| `WithAPIKey(key)` | Qdrant Cloud 认证 API 密钥 | - |
| `WithTLS(enabled)` | 启用 TLS（Qdrant Cloud 必需） | `false` |
| `WithClient(client)` | 使用预创建的客户端（来自 storage 模块） | - |

### 集合配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithCollectionName(name)` | 集合名称 | `"trpc_agent_documents"` |
| `WithDimension(dim)` | 向量维度（必须与 embedding 模型匹配） | `1536` |
| `WithDistance(d)` | 距离度量（Cosine、Euclid、Dot、Manhattan） | `DistanceCosine` |

### 索引配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithHNSWConfig(m, efConstruct)` | HNSW 索引参数（越高 = 召回率越好，内存越多） | `16, 128` |
| `WithOnDiskVectors(enabled)` | 将向量存储在磁盘上（适用于大数据集） | `false` |
| `WithOnDiskPayload(enabled)` | 将负载存储在磁盘上 | `false` |

### 搜索配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithMaxResults(max)` | 默认搜索结果数量 | `10` |
| `WithBM25(enabled)` | 启用 BM25 稀疏向量用于混合/关键词检索 | `false` |
| `WithPrefetchMultiplier(n)` | 混合检索融合的预取倍数 | `2` |

### 重试配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithMaxRetries(n)` | 瞬态 gRPC 错误最大重试次数 | `3` |
| `WithBaseRetryDelay(d)` | 初始重试延迟 | `100ms` |
| `WithMaxRetryDelay(d)` | 最大重试延迟 | `5s` |

### 其他配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithLogger(logger)` | 设置日志记录器 | - |

## BM25 混合检索

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
