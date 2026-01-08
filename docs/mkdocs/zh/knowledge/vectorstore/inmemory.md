# Memory（内存向量存储）

> **示例代码**: [examples/knowledge/vectorstores/inmemory](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/inmemory)

内存向量存储是最简单的实现，适用于开发测试和小规模数据场景。

## 基础配置

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
)

memVS := vectorinmemory.New()

kb := knowledge.New(
    knowledge.WithVectorStore(memVS),
    knowledge.WithEmbedder(embedder),
)
```

## 配置选项

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithMaxResults(n)` | 默认搜索结果数量 | `10` |

## 特点

- ✅ 零配置，开箱即用
- ✅ 支持所有过滤器功能（包括 FilterCondition）
- ⚠️ 数据不持久化，重启后丢失
- ⚠️ 仅适用于开发和测试环境

## 搜索模式

| 模式 | 支持情况 | 说明 |
|------|---------|------|
| Vector | ✅ | 向量相似度搜索（余弦相似度） |
| Filter | ✅ | 仅过滤搜索，按创建时间排序 |
| Hybrid | ⚠️ | 回退到向量搜索 |
| Keyword | ⚠️ | 回退到过滤搜索 |
