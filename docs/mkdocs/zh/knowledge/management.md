# 知识库管理

> **示例代码**: [examples/knowledge/features/management](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/management)

Knowledge 系统提供了强大的知识库管理功能，支持动态源管理和智能同步机制。

## 源同步模式对比

Knowledge 提供两种加载模式：**默认模式（不启用同步）** 和 **同步模式（启用 WithEnableSourceSync）**。

### 默认模式（不启用同步）

默认情况下，`WithEnableSourceSync` 为 `false`，Knowledge 采用**追加式加载**：

```go
import (
    "log"

    "trpc.group/trpc-go/trpc-agent-go/knowledge"
)

kb := knowledge.New(
    knowledge.WithEmbedder(embedder),
    knowledge.WithVectorStore(vectorStore),
    knowledge.WithSources(sources),
    // WithEnableSourceSync 默认为 false
)

if err := kb.Load(ctx); err != nil {
    log.Fatalf("Failed to load: %v", err)
}
```

**默认模式的行为**：

- **仅追加**：每次 `Load()` 只会将新文档添加到向量存储
- **不检测变更**：已存在的文档不会被更新，即使源文件内容已变更
- **不清理孤儿**：已删除的源文件对应的向量数据不会被自动清理
- **适用场景**：一次性导入、数据只增不减的场景、业务自己管理数据源的场景

### 同步模式（启用 WithEnableSourceSync）

* 启用 `WithEnableSourceSync(true)` 后，Knowledge 会**保持向量存储与配置的 Source 完全一致**：

```go
import (
    "log"

    "trpc.group/trpc-go/trpc-agent-go/knowledge"
)

kb := knowledge.New(
    knowledge.WithEmbedder(embedder),
    knowledge.WithVectorStore(vectorStore),
    knowledge.WithSources(sources),
    knowledge.WithEnableSourceSync(true), // 启用同步模式
)

if err := kb.Load(ctx); err != nil {
    log.Fatalf("Failed to load: %v", err)
}
```

**触发同步的操作**

启用同步模式后，以下操作都会触发同步校验：


| 操作             | 同步行为                                                                   |
| ------------------ | ---------------------------------------------------------------------------- |
| `Load()`         | 全量同步：检测所有 Source 的变更，清理孤儿文档                             |
| `ReloadSource()` | 单源同步：检测指定 Source 的变更，加载变更，清理 Source 的孤儿文档         |
| `RemoveSource()` | 删除同步：精确删除指定 Source 的所有文档，更新缓存状态                     |
| `AddSource()`    | 增量同步：检测新 Source 中文档的变更并加载，更新缓存状态（不触发孤儿清理） |

**同步模式的行为**：

1. **加载前准备**：刷新文档信息缓存，建立同步状态跟踪
2. **智能增量处理**：检测文档变更，只处理新增或修改的文档
3. **加载后清理**：自动删除不再属于任何配置 Source 的孤儿文档

### 模式对比


| 特性       | 默认模式      | 同步模式              |
| ------------ | --------------- | ----------------------- |
| 新文档处理 | ✅ 追加       | ✅ 追加               |
| 变更检测   | ❌ 不检测     | ✅ 自动检测并更新     |
| 孤儿清理   | ❌ 不清理     | ✅ 自动清理           |
| 数据一致性 | ❌ 可能不一致 | ✅ 与 Source 保持一致 |
| 性能开销   | 较低          | 略高（需要对比状态）  |

> ⚠️ **重要警告**：启用同步模式时，**必须确保所有需要保留的 Source 都被正确配置**。同步机制会对比配置的 Source 与向量存储中的数据，**任何不属于配置 Source 的文档都会被视为孤儿并删除**。
>
> ```go
> import (
>     "trpc.group/trpc-go/trpc-agent-go/knowledge"
>     "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
> )
>
> // ❌ 危险：空 Source 配置会导致所有现有文档被删除！
> kb := knowledge.New(
>     knowledge.WithEmbedder(embedder),
>     knowledge.WithVectorStore(vectorStore),
>     knowledge.WithSources([]source.Source{}), // 空配置
>     knowledge.WithEnableSourceSync(true),
> )
> kb.Load(ctx) // 所有文档将被清理！
>
> // ✅ 正确：确保所有需要的 Source 都已配置
> kb := knowledge.New(
>     knowledge.WithEmbedder(embedder),
>     knowledge.WithVectorStore(vectorStore),
>     knowledge.WithSources([]source.Source{source1, source2, source3}), // 完整配置
>     knowledge.WithEnableSourceSync(true),
> )
> kb.Load(ctx) // 安全：只清理不属于这些 Source 的文档
> ```

## 动态源管理

Knowledge 支持运行时动态管理知识源，确保向量存储中的数据始终与用户配置的 source 保持一致：

```go
import (
    "log"

    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
)

// 添加新的知识源 - 数据将与配置的源保持同步
newSource := filesource.New([]string{"./new-docs/api.md"})
if err := kb.AddSource(ctx, newSource); err != nil {
    log.Printf("Failed to add source: %v", err)
}

// 重新加载指定的知识源 - 自动检测变更并同步
if err := kb.ReloadSource(ctx, newSource); err != nil {
    log.Printf("Failed to reload source: %v", err)
}

// 移除指定的知识源 - 精确删除相关文档
if err := kb.RemoveSource(ctx, "API Documentation"); err != nil {
    log.Printf("Failed to remove source: %v", err)
}
```

## 知识库状态监控

Knowledge 提供了丰富的状态监控功能，帮助用户了解当前配置源的同步状态：

```go
import (
    "fmt"
    "log"

    "trpc.group/trpc-go/trpc-agent-go/knowledge"
)

// 显示所有文档信息
docInfos, err := kb.ShowDocumentInfo(ctx)
if err != nil {
    log.Printf("Failed to show document info: %v", err)
    return
}

// 按源名称过滤显示
docInfos, err = kb.ShowDocumentInfo(ctx,
    knowledge.WithShowDocumentInfoSourceName("APIDocumentation"))
if err != nil {
    log.Printf("Failed to show source documents: %v", err)
    return
}

// 按文档ID过滤显示
docInfos, err = kb.ShowDocumentInfo(ctx,
    knowledge.WithShowDocumentInfoIDs([]string{"doc1", "doc2"}))
if err != nil {
    log.Printf("Failed to show specific documents: %v", err)
    return
}

// 遍历显示文档信息
for _, docInfo := range docInfos {
    fmt.Printf("Document ID: %s\n", docInfo.DocumentID)
    fmt.Printf("Source: %s\n", docInfo.SourceName)
    fmt.Printf("URI: %s\n", docInfo.URI)
    fmt.Printf("Chunk Index: %d\n", docInfo.ChunkIndex)
}
```

**状态监控输出示例**

```
Document ID: a1b2c3d4e5f6...
Source: Technical Documentation
URI: /docs/api/authentication.md
Chunk Index: 0

Document ID: f6e5d4c3b2a1...
Source: Technical Documentation
URI: /docs/api/authentication.md
Chunk Index: 1
```

