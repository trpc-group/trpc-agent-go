# ChromaDB 存储

通用的 Agent 接入、提取模式和工具配置请参阅[使用与配置](usage.md)。

**适用场景**：自建 ChromaDB 或 Chroma Cloud，使用余弦语义检索和混合检索

```go
import (
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    memorychromadb "trpc.group/trpc-go/trpc-agent-go/memory/chromadb"
)

embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"),
)

chromaService, err := memorychromadb.NewService(
    memorychromadb.WithBaseURL("http://localhost:8000"),
    memorychromadb.WithCollectionName("memories"),
    memorychromadb.WithEmbedder(embedder),
    memorychromadb.WithSoftDelete(true),
)
if err != nil {
    // 处理错误
}
defer chromaService.Close()
```

这是 client-server 模式的 REST 适配器，不是嵌入式 Chroma 运行时。需要单独
启动 Chroma 服务，或让 `WithBaseURL` 指向远程部署或 Chroma Cloud。Embedding
由配置的 tRPC-Agent-Go `embedder.Embedder` 生成；适配器不会安装或调用 Chroma
服务端 embedding function。

使用 Chroma Cloud 时可配置 `WithAPIKey`，该值通过 `X-Chroma-Token` 发送；如果
没有显式设置 tenant 和 database，服务会通过 identity 接口解析唯一作用域。
Bearer 和自定义请求头认证分别使用 `WithBearerToken` 和 `WithHTTPHeaders`，
主要面向代理或自定义网关。使用自定义认证请求头时，必须显式指定 tenant 和
database。非 loopback 地址只要携带认证或任意自定义请求头，就必须使用 HTTPS。

**配置选项**：

- 连接：`WithBaseURL`、`WithAPIKey`、`WithBearerToken`、
  `WithHTTPHeaders`、`WithTenant`、`WithDatabase`、`WithHTTPClient`、
  `WithTimeout`
- Collection：`WithCollectionName`、`WithAutoCreateCollection`、
  `WithIndexDimension`、`WithEmbedder`
- 检索：`WithMaxResults`、`WithSimilarityThreshold`、
  `WithHybridCandidateLimit`
- 保留策略：`WithMemoryLimit`、`WithSoftDelete`
- Auto 模式和工具配置与其他 memory 后端一致。

适配器直接使用 ChromaDB REST API v2，不依赖第三方 SDK。Collection 必须只启用
一个 HNSW 或 SPANN 索引，且距离度量必须为 `cosine`。记录在同一个 collection
内通过 schema、应用和用户 metadata 隔离。每用户容量限制只在单个 Service 实例
内串行保证；多实例同时写同一用户时，应在上层使用分布式锁或 sticky routing。

还需注意以下运行约束：

- 更换 embedding 模型时，即使新旧模型维度相同，也必须使用新 collection，或
  对全部记录重新生成 embedding。
- `EventTime` 和检索时间边界必须能以有符号 64 位 Unix 纳秒表示，即位于 UTC
  1677-09-21 至 2262-04-11 之间；超出范围的值会在请求 ChromaDB 前被拒绝。
- `WithHybridCandidateLimit` 是本地关键词候选扫描的硬上限，与
  `WithMemoryLimit` 无关。
- Chroma 没有为本流程提供跨请求事务或分页 snapshot token，因此多 Service
  实例下的容量检查、ID 轮换和分页读取都是 best-effort。
- Chroma Cloud 当前说明的限制包括：collection 名称最多 128 字节、查询结果最多
  300 条、单次写入最多 300 条、每个 collection 并发读写各 10。适配器只说明
  这些服务限制，不会静默 clamp 用户配置。
