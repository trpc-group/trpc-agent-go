# PGVector（PostgreSQL + pgvector）

> **示例代码**: [examples/knowledge/vectorstores/postgres](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/postgres)

PGVector 是基于 PostgreSQL + pgvector 扩展的向量存储实现，支持混合检索（向量相似度 + 文本相关性）。

## 基础配置

```go
import (
    vectorpgvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
)

// PostgreSQL + pgvector
pgVS, err := vectorpgvector.New(
    vectorpgvector.WithPGVectorClientDSN("postgres://postgres:your-password@127.0.0.1:5432/your-database?sslmode=disable"),
    // 根据 embedding 模型设置索引维度（text-embedding-3-small 为 1536）
    vectorpgvector.WithIndexDimension(1536),
)
if err != nil {
    // 处理 error
}

kb := knowledge.New(
    knowledge.WithVectorStore(pgVS),
    knowledge.WithEmbedder(embedder), // 需要配置本地 embedder
)
```

## 配置选项

### 连接配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithPGVectorClientDSN(dsn)` | PostgreSQL 连接字符串（优先级最高） | - |
| `WithHost(host)` | 数据库主机地址 | `"localhost"` |
| `WithPort(port)` | 数据库端口 | `5432` |
| `WithUser(user)` | 数据库用户名 | - |
| `WithPassword(password)` | 数据库密码 | - |
| `WithDatabase(database)` | 数据库名称 | `"trpc_agent_go"` |
| `WithTable(table)` | 表名称 | `"documents"` |
| `WithSSLMode(mode)` | SSL 模式 | `"disable"` |
| `WithPostgresInstance(name)` | 使用已注册的 PostgreSQL 实例（优先级低于直接连接配置） | - |

### 向量配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithIndexDimension(dim)` | 向量维度（需与 embedding 模型匹配） | `1536` |
| `WithVectorIndexType(type)` | 向量索引类型（`VectorIndexHNSW` / `VectorIndexIVFFlat`） | `VectorIndexHNSW` |
| `WithHNSWIndexParams(params)` | HNSW 索引参数（M, EfConstruction） | `M=16, EfConstruction=64` |
| `WithIVFFlatIndexParams(params)` | IVFFlat 索引参数（Lists） | `Lists=100` |

### 混合检索配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithEnableTSVector(enabled)` | 启用文本检索向量 | `true` |
| `WithHybridSearchWeights(vector, text)` | 混合检索权重（向量/文本） | `0.7, 0.3` |
| `WithLanguageExtension(lang)` | 文本分词语言扩展（如 zhparser/jieba） | `"english"` |

### 搜索配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithMaxResults(n)` | 默认搜索结果数量 | `10` |
| `WithDocBuilder(builder)` | 自定义文档构建方法 | 默认构建器 |
| `WithExtraOptions(opts...)` | 注入自定义 PostgreSQL ClientBuilder 配置，默认无需关心 | - |

### 字段映射（高级）

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithIDField(field)` | ID 字段名 | `"id"` |
| `WithNameField(field)` | 名称字段名 | `"name"` |
| `WithContentField(field)` | 内容字段名 | `"content"` |
| `WithEmbeddingField(field)` | 向量字段名 | `"embedding"` |
| `WithMetadataField(field)` | 元数据字段名 | `"metadata"` |
| `WithCreatedAtField(field)` | 创建时间字段名 | `"created_at"` |
| `WithUpdatedAtField(field)` | 更新时间字段名 | `"updated_at"` |

## 混合检索

PGVector 支持混合检索，结合向量相似度搜索和全文检索：

```go
pgVS, err := vectorpgvector.New(
    vectorpgvector.WithPGVectorClientDSN(dsn),
    vectorpgvector.WithIndexDimension(1536),
    vectorpgvector.WithEnableTSVector(true),           // 启用全文检索
    vectorpgvector.WithHybridSearchWeights(0.7, 0.3),  // 70% 向量 + 30% 文本
)
```
