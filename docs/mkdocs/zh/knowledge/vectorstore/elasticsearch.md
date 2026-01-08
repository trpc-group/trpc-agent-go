# Elasticsearch

> **示例代码**: [examples/knowledge/vectorstores/elasticsearch](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/elasticsearch)

Elasticsearch 向量存储支持 v7、v8、v9 多个版本。

## 基础配置

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    vectorelasticsearch "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/elasticsearch"
)

// 创建支持多版本 (v7, v8, v9) 的 Elasticsearch 向量存储
esVS, err := vectorelasticsearch.New(
    vectorelasticsearch.WithAddresses([]string{"http://localhost:9200"}),
    vectorelasticsearch.WithUsername("your-username"),
    vectorelasticsearch.WithPassword("your-password"),
    vectorelasticsearch.WithIndexName("trpc_agent_documents"),
    // 版本可选："v7"、"v8"、"v9"（默认 "v9"）
    vectorelasticsearch.WithVersion("v9"),
)
if err != nil {
    // 处理 error
}

kb := knowledge.New(
    knowledge.WithVectorStore(esVS),
    knowledge.WithEmbedder(embedder),
)
```

## 配置选项

### 连接配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithAddresses(addresses)` | Elasticsearch 服务地址列表 | `["http://localhost:9200"]` |
| `WithUsername(username)` | 用户名 | - |
| `WithPassword(password)` | 密码 | - |
| `WithAPIKey(apiKey)` | API Key 认证 | - |
| `WithCertificateFingerprint(fp)` | 证书指纹认证 | - |
| `WithVersion(version)` | ES 版本（v7/v8/v9） | `"v9"` |

### 索引配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithIndexName(name)` | 索引名称 | `"trpc_agent_documents"` |
| `WithVectorDimension(dim)` | 向量维度 | `1536` |
| `WithEnableTSVector(enabled)` | 启用文本搜索向量 | `true` |

### 搜索配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithMaxResults(n)` | 默认搜索结果数量 | `10` |
| `WithScoreThreshold(threshold)` | 最小相似度分数阈值 | `0.7` |

### 高级配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithMaxRetries(n)` | 最大重试次数 | `3` |
| `WithCompressRequestBody(enabled)` | 启用请求压缩 | `true` |
| `WithEnableMetrics(enabled)` | 启用指标收集 | `false` |
| `WithEnableDebugLogger(enabled)` | 启用调试日志 | `false` |
| `WithRetryOnStatus(codes)` | 重试的 HTTP 状态码 | `[500, 502, 503, 429]` |
| `WithDocBuilder(builder)` | 自定义文档构建方法 | 默认构建器 |
| `WithExtraOptions(opts...)` | 注入自定义 ES ClientBuilder 配置，默认无需关心 | - |

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

## 版本选择

根据你的 Elasticsearch 服务版本选择对应的配置：

```go
// Elasticsearch 7.x
vectorelasticsearch.WithVersion("v7")

// Elasticsearch 8.x
vectorelasticsearch.WithVersion("v8")

// Elasticsearch 9.x（默认）
vectorelasticsearch.WithVersion("v9")
```
