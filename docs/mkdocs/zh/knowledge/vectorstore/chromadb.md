# ChromaDB

> **示例代码**: [examples/knowledge/vectorstores/chroma](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/chroma)

[Chroma](https://docs.trychroma.com/) 是一个开源向量数据库。`knowledge/vectorstore/chroma` 通过 Chroma v2 REST API 实现 `vectorstore.VectorStore`，要求 Chroma 1.5.3 或更高版本。

关键词和混合检索依赖 [Chroma Cloud](https://docs.trychroma.com/cloud/getting-started) 的 `/search` 接口；自托管服务只支持向量和过滤检索。

## 基础配置

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    chroma "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/chroma"
)

chromaVS, err := chroma.New(ctx,
    chroma.WithBaseURL("http://localhost:8000"),
    chroma.WithCollection("my_documents"),
    chroma.WithIndexDimension(1536), // 必须与 embedding 模型匹配
)
if err != nil {
    // 处理 error
}

kb := knowledge.New(
    knowledge.WithVectorStore(chromaVS),
    knowledge.WithEmbedder(embedder),
)
```

## Chroma Cloud 配置

```go
chromaVS, err := chroma.New(ctx,
    chroma.WithBaseURL("https://api.trychroma.com"),
    chroma.WithAPIKey("your-api-key"),
    chroma.WithCollection("my_documents"),
    chroma.WithIndexDimension(1536),
)
```

`WithAPIKey` 发送 `X-Chroma-Token`。未设置 tenant / database 时从 Cloud identity 推断。

## 配置选项

### 连接配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithBaseURL(url)` | Chroma HTTP 地址。未使用命名实例时必填。 | - |
| `WithInstanceName(name)` | 使用 `storage.RegisterChromaInstance` 注册的客户端 | - |
| `WithTenant(tenant)` / `WithDatabase(database)` | 租户和数据库。空值使用服务端默认值；配置 `WithAPIKey` 时从 Cloud identity 推断。 | 服务端默认值 |
| `WithAPIKey(key)` | 发送 `X-Chroma-Token` | - |
| `WithBearerToken(token)` | 发送 `Authorization: Bearer` | - |
| `WithHeaders(headers)` | 额外请求头。自定义鉴权头需要同时设置 tenant 和 database。 | - |

> 鉴权选项只添加请求头；自托管 Chroma 1.0+ 不再内置鉴权。

### 集合配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithCollection(name)` | Collection 名 | 必填 |
| `WithIndexDimension(dim)` | 向量维度（必须与 embedding 模型匹配） | `1536` |
| `WithAutoCreateCollection(enable)` | Collection 不存在时自动创建 | `true` |
| `WithMaxResults(n)` | 默认搜索结果数量 | `10` |
| `WithMaxRequestRecords(n)` | 单次 Chroma 请求的最大记录数。Vector Query 和 `/search` 结果按该上限封顶；Get 操作超出后翻页。 | `300` |

Collection 必须使用 cosine 度量：新建的 collection 使用 cosine HNSW，已有的非 cosine collection 会在启动时报错。

### 搜索配置

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithSparseSearch(embedder...)` | 启用关键词和混合检索。编码器可省略，省略时使用内置 Cloud SPLADE 编码器（需要 `WithAPIKey`）。 | 未启用 |
| `WithSparseSearchKey(key)` | 存储稀疏向量的 metadata 字段 | `"sparse_embedding"` |
| `WithHybridWeights(dense, sparse)` | 稠密/稀疏 RRF 权重，内部归一化为总和 1 | `0.5, 0.5` |

### 内置 Cloud SPLADE 编码器

`WithSparseSearch()` 不传编码器时使用 `NewCloudSpladeEmbedder`——通过 Chroma Cloud 托管的 SPLADE 嵌入 API（`prithivida/Splade_PP_en_v1`）编码文档与查询，使用 `WithAPIKey` 提供的密钥：

```go
vs, err := chroma.New(ctx,
    chroma.WithBaseURL("https://api.trychroma.com"),
    chroma.WithAPIKey(apiKey),
    chroma.WithCollection("docs"),
    chroma.WithIndexDimension(1536),
    chroma.WithSparseSearch(), // 内置 Cloud SPLADE 编码器
)
```

- 每次写入非空文档、每次关键词或混合检索，都会调用一次托管嵌入 API，受账号速率限制约束。
- 自动创建的 collection 会在 schema 中声明 `chroma-cloud-splade` 嵌入函数（与官方 Python / TypeScript / Rust 客户端一致），读取 schema 的客户端可重建兼容的编码函数，实现跨 SDK 互操作。
- 该模型面向英文。其他语言（包括中文）请实现 `SparseEmbedder` 接口后传给 `WithSparseSearch`。
- 显式构造 `NewCloudSpladeEmbedder` 时，可用 `WithSpladeBaseURL` 和 `WithSpladeModel` 自定义嵌入服务地址与模型；可用 `WithSpladeHTTPClient` 自定义嵌入请求使用的 HTTP 客户端（transport、代理、TLS、超时、链路追踪）。

### 自定义稀疏编码器

其他语言或自管稀疏模型可实现 `SparseEmbedder` 接口（词典分词、BGE-M3 稀疏输出等）后传给 `WithSparseSearch`。实现必须并发安全；文档与查询两次编码必须处于同一向量空间；稀疏索引必须严格递增且在 int32 范围内。集合一经某个编码器写入，就必须一直使用同一编码器检索；更换编码器需要重写整个集合。

## 搜索模式

| 模式 | 支持情况 | 说明 |
|------|---------|------|
| Vector | ✅ | 通过 Chroma `Query` 做稠密 cosine 检索 |
| Filter | ✅ | 通过 `Get` 按 ID、metadata 或正文过滤 |
| Keyword | ⚠️ | 通过 Cloud `/search` 做稀疏 KNN。需要 `WithSparseSearch`。 |
| Hybrid | ⚠️ | 稠密+稀疏加权 RRF。未配置稀疏检索时退化为 Vector。 |

说明：

- `WithSparseSearch` 只会在**新建**的 collection 上写入稀疏索引；已有 collection 必须预先具备该索引，Chroma 无法事后补充。
- Hybrid 的融合公式为 `score = wd·k/(k+dense_rank) + ws·k/(k+sparse_rank)`，默认 `k = 60`。未进入某一路候选窗口的记录使用窗口末位之后的 rank。
- 配置稀疏检索后，`/search` 失败会直接返回错误，不会静默退化。

## Metadata 与过滤

`name`、`created_at`、`updated_at` 和 `_json` 是保留键，Add / Update 遇到会报错。嵌套 metadata 写入 `_json` 以便往返，但无法过滤。配置的稀疏字段由适配器管理，不能直接写入。

| 通用算子 | Chroma 算子 |
|----------|-------------|
| eq / ne / gt / gte / lt / lte | `$eq` / `$ne` / `$gt` / `$gte` / `$lt` / `$lte` |
| in / not in | `$in` / `$nin` |
| and / or | `$and` / `$or` |
| like / not like（仅 `content` 字段） | `$contains` / `$not_contains` |

不支持 `between`，也不支持跨 ID / metadata / 正文的 OR。

## 行为说明

- Add 是 upsert。Update 只覆盖已存在的文档；新 embedding 为空时保留原向量。
- Add 和 Update 采用整条 metadata 覆盖（先读后写，非原子）。
- `UpdateByFilter` 会在写入前固定全部匹配 ID，默认超过 100,000 条时报错；可用 `WithMaxUpdateRecords` 调整。
- 过滤检索没有任何条件时返回错误，不会扫描整个 collection。
- DeleteAll 必须使用 `vectorstore.WithDeleteAll(true)`，且不能与其他选择器同时使用：

```go
store.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true))
```
