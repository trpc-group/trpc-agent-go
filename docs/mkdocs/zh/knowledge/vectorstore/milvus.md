# Milvus

> **示例代码**: [examples/knowledge/vectorstores/milvus](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/milvus)

[Milvus](https://milvus.io/) 是一个高性能向量数据库，专为十亿级向量搜索场景设计。

## 基础配置

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    vectormilvus "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/milvus"
)

milvusVS, err := vectormilvus.New(ctx,
    vectormilvus.WithAddress("localhost:19530"),
    vectormilvus.WithCollectionName("my_documents"),
    vectormilvus.WithDimension(1536),
)
if err != nil {
    // 处理 error
}

kb := knowledge.New(
    knowledge.WithVectorStore(milvusVS),
    knowledge.WithEmbedder(embedder),
)
```

## 配置选项

### 连接配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithAddress(address)` | Milvus 服务器地址 | - |
| `WithUsername(username)` | 用户名 | - |
| `WithPassword(password)` | 密码 | - |
| `WithDBName(dbName)` | 数据库名称 | - |
| `WithAPIKey(apiKey)` | API Key 认证 | - |
| `WithDialOptions(opts...)` | gRPC 连接选项 | `Timeout=5s` |

### 集合配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithCollectionName(name)` | 集合名称 | `"trpc_agent_documents"` |
| `WithDimension(dim)` | 向量维度 | `1536` |
| `WithMetricType(type)` | 相似度度量类型（IP/L2/COSINE） | `entity.IP` |

### 索引配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithHNSWParams(m, efConstruction)` | HNSW 索引参数 | `M=16, EfConstruction=128` |

### 搜索配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithMaxResults(max)` | 默认搜索结果数量 | `10` |
| `WithReranker(reranker)` | 设置重排器 | - |
| `WithDocBuilder(builder)` | 自定义文档构建方法 | 默认构建器 |

### 字段映射（高级）

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithIDField(field)` | ID 字段名 | `"id"` |
| `WithNameField(field)` | 名称字段名 | `"name"` |
| `WithContentField(field)` | 内容字段名 | `"content"` |
| `WithVectorField(field)` | 向量字段名 | `"vector"` |
| `WithMetadataField(field)` | 元数据字段名 | `"metadata"` |
| `WithCreatedAtField(field)` | 创建时间字段名 | `"created_at"` |
| `WithUpdatedAtField(field)` | 更新时间字段名 | `"updated_at"` |

## 相似度度量类型

Milvus 支持多种相似度度量类型：

```go
import "github.com/milvus-io/milvus/client/v2/entity"

// 内积（Inner Product）- 默认，分数越高越相似
vectormilvus.WithMetricType(entity.IP)

// 欧氏距离（L2）- 分数越低越相似
vectormilvus.WithMetricType(entity.L2)

// 余弦相似度
vectormilvus.WithMetricType(entity.COSINE)
```

## 使用示例

```go
import (
    vectormilvus "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/milvus"
    "github.com/milvus-io/milvus/client/v2/entity"
)

milvusVS, err := vectormilvus.New(ctx,
    vectormilvus.WithAddress("localhost:19530"),
    vectormilvus.WithUsername("root"),
    vectormilvus.WithPassword("Milvus"),
    vectormilvus.WithCollectionName("knowledge_base"),
    vectormilvus.WithDimension(1536),
    vectormilvus.WithMetricType(entity.COSINE),
    vectormilvus.WithHNSWParams(32, 256),  // 更高的召回率
    vectormilvus.WithMaxResults(20),
)
```
