# TcVector（腾讯云向量数据库）

> **示例代码**: [examples/knowledge/vectorstores/tcvector](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/tcvector)

TcVector 是腾讯云向量数据库的实现，支持本地和远程两种 embedding 模式。

## Embedding 模式

TcVector 支持两种 embedding 模式：

### 1. 本地 Embedding 模式（默认）

使用本地 embedder 计算向量，然后存储到 TcVector：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    vectortcvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
)

// 本地 embedding 模式
tcVS, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-tcvector-endpoint"),
    vectortcvector.WithUsername("your-username"),
    vectortcvector.WithPassword("your-password"),
    vectortcvector.WithFilterAll(true), // 推荐开启：自动索引所有元数据字段
)
if err != nil {
    // 处理 error
}

kb := knowledge.New(
    knowledge.WithVectorStore(tcVS),
    knowledge.WithEmbedder(embedder), // 需要配置本地 embedder
)
```

### 2. 远程 Embedding 模式

使用 TcVector 云端 embedding 计算，无需本地 embedder，节省资源：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    vectortcvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
)

// 远程 embedding 模式
tcVS, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-tcvector-endpoint"),
    vectortcvector.WithUsername("your-username"),
    vectortcvector.WithPassword("your-password"),
    vectortcvector.WithFilterAll(true), // 推荐开启：自动索引所有元数据字段
    // 启用远程 embedding 计算
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

## 配置选项

### 连接配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithURL(url)` | TcVector 服务端点 | - |
| `WithUsername(username)` | 用户名 | - |
| `WithPassword(password)` | 密码 | - |
| `WithDatabase(database)` | 数据库名称 | `"trpc-agent-go"` |
| `WithCollection(collection)` | 集合名称 | `"documents"` |
| `WithTCVectorInstance(name)` | 使用已注册的 TcVector 实例（优先级低于直接连接配置） | - |

### 向量配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithIndexDimension(dim)` | 向量维度（需与 embedding 模型匹配） | `1536` |
| `WithRemoteEmbeddingModel(model)` | 远程 embedding 模型名称（如 bge-base-zh） | - |
| `WithEnableTSVector(enabled)` | 启用混合检索 | `true` |
| `WithHybridSearchWeights(vector, text)` | 混合检索权重（向量/文本） | `0.7, 0.3` |
| `WithLanguage(lang)` | 文本分词语言（zh/en） | `"en"` |

### 索引配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithReplicas(n)` | 副本数量 | `0` |
| `WithSharding(n)` | 分片数量 | `1` |
| `WithFilterIndexFields(fields)` | 为指定字段构建过滤索引 | - |
| `WithFilterAll(enabled)` | 启用全字段过滤（跳过索引创建） | `false` |

### 搜索配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithMaxResults(n)` | 默认搜索结果数量 | `10` |
| `WithDocBuilder(builder)` | 自定义文档构建方法 | 默认构建器 |

### 字段映射（高级）

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithIDField(field)` | ID 字段名 | `"id"` |
| `WithNameField(field)` | 名称字段名 | `"name"` |
| `WithContentField(field)` | 内容字段名 | `"content"` |
| `WithEmbeddingField(field)` | 向量字段名 | `"vector"` |
| `WithMetadataField(field)` | 元数据字段名 | `"metadata"` |
| `WithCreatedAtField(field)` | 创建时间字段名 | `"created_at"` |
| `WithUpdatedAtField(field)` | 更新时间字段名 | `"updated_at"` |
| `WithSparseVectorField(field)` | 稀疏向量字段名 | `"sparse_vector"` |

## 过滤器支持

TcVector 对过滤器的支持：

- ✅ 支持所有元数据过滤
- ✅ v0.4.0+ 新建集合自动支持 JSON 索引（需 TCVector 服务支持）
- ⚡ 可选：使用 `WithFilterIndexFields` 为高频字段构建额外索引

```go
// 推荐配置（适用于大多数场景）
vectorStore, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-endpoint"),
    vectortcvector.WithFilterAll(true), // 推荐开启：自动索引所有元数据字段，无需手动管理索引
    // ... 其他配置
)

// 可选：为高频字段构建额外索引以优化性能（配合 WithFilterAll(true) 使用）
metadataKeys := source.GetAllMetadataKeys(sources)
vectorStore, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-endpoint"),
    vectortcvector.WithFilterAll(true),
    vectortcvector.WithFilterIndexFields(metadataKeys), // 可选：构建额外倒排索引加速查询
    // ... 其他配置
)
```

**说明：**
- **WithFilterAll(true)**（推荐）：自动为 `metadata` 字段创建 JSON 索引，使所有元数据字段均可被过滤查询，无需预先定义 schema。
- **WithFilterIndexFields**（可选）：为特定的高频查询字段创建额外的倒排索引，在大数据量下进一步提升过滤性能。
