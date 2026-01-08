# 文档源配置

> **示例代码**: [examples/knowledge/sources](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources)

源模块提供了多种文档源类型，每种类型都支持丰富的配置选项。

## 支持的文档源类型

| 源类型 | 说明 | 示例 |
|-------|------|------|
| **文件源 (file)** | 单个文件处理 | [示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/file-source) |
| **目录源 (dir)** | 批量处理目录 | [示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/directory-source) |
| **URL 源 (url)** | 从网页获取内容 | [示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/url-source) |
| **自动源 (auto)** | 智能识别类型 | [示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/auto-source) |

## 文件源 (File Source)

单个文件处理，支持 .txt, .md, .json, .doc, .csv 等等格式：

```go
import (
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
)

fileSrc := filesource.New(
    []string{"./data/llm.md"},
    filesource.WithChunkSize(1000),      // 分块大小
    filesource.WithChunkOverlap(200),    // 分块重叠
    filesource.WithName("LLM Doc"),
    filesource.WithMetadataValue("type", "documentation"),
)
```

## 目录源 (Directory Source)

批量处理目录，支持递归和过滤：

```go
import (
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
)

dirSrc := dirsource.New(
    []string{"./docs"},
    dirsource.WithRecursive(true),                           // 递归处理子目录
    dirsource.WithFileExtensions([]string{".md", ".txt"}),   // 文件扩展名过滤
    dirsource.WithChunkSize(800),
    dirsource.WithName("Documentation"),
)
```

## URL 源 (URL Source)

从网页和 API 获取内容：

```go
import (
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
)

urlSrc := urlsource.New(
    []string{"https://en.wikipedia.org/wiki/Artificial_intelligence"},
    urlsource.WithChunkSize(1000),
    urlsource.WithChunkOverlap(200),
    urlsource.WithName("Web Content"),
)
```

### URL 源高级配置

分离内容获取和文档标识：

```go
import (
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
)

urlSrcAlias := urlsource.New(
    []string{"https://trpc-go.com/docs/api.md"},     // 标识符 URL（用于文档 ID 和元数据）
    urlsource.WithContentFetchingURL([]string{"https://github.com/trpc-group/trpc-go/raw/main/docs/api.md"}), // 实际内容获取 URL
    urlsource.WithName("TRPC API Docs"),
    urlsource.WithMetadataValue("source", "github"),
)
```

> **注意**：使用 `WithContentFetchingURL` 时，标识符 URL 应保留获取内容的URL的文件信息，比如：
> - 正确：标识符 URL 为 `https://trpc-go.com/docs/api.md`，获取 URL 为 `https://github.com/.../docs/api.md`
> - 错误：标识符 URL 为 `https://trpc-go.com`，会丢失文档路径信息

## 自动源 (Auto Source)

智能识别类型，自动选择处理器：

```go
import (
    autosource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
)

autoSrc := autosource.New(
    []string{
        "Cloud computing provides on-demand access to computing resources.",
        "https://docs.example.com/api",
        "./config.yaml",
    },
    autosource.WithName("Mixed Sources"),
    autosource.WithChunkSize(1000),
)
```

## 组合使用

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
    autosource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
    vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
)

// 组合多种源
sources := []source.Source{fileSrc, dirSrc, urlSrc, autoSrc}

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))
vectorStore := vectorinmemory.New()

// 传递给 Knowledge
kb := knowledge.New(
    knowledge.WithEmbedder(embedder),
    knowledge.WithVectorStore(vectorStore),
    knowledge.WithSources(sources),
)

// 加载所有源
if err := kb.Load(ctx); err != nil {
    log.Fatalf("Failed to load knowledge base: %v", err)
}
```


## 配置元数据

为了使过滤器功能正常工作，建议在创建文档源时添加丰富的元数据。

> 详细的过滤器使用指南，请参考 [过滤器文档](filter.md)。

```go
sources := []source.Source{
    // 文件源配置元数据
    filesource.New(
        []string{"./docs/api.md"},
        filesource.WithName("API Documentation"),
        filesource.WithMetadataValue("category", "documentation"),
        filesource.WithMetadataValue("topic", "api"),
        filesource.WithMetadataValue("service_type", "gateway"),
        filesource.WithMetadataValue("protocol", "trpc-go"),
        filesource.WithMetadataValue("version", "v1.0"),
    ),

    // 目录源配置元数据
    dirsource.New(
        []string{"./tutorials"},
        dirsource.WithName("Tutorials"),
        dirsource.WithMetadataValue("category", "tutorial"),
        dirsource.WithMetadataValue("difficulty", "beginner"),
        dirsource.WithMetadataValue("topic", "programming"),
    ),

    // URL 源配置元数据
    urlsource.New(
        []string{"https://example.com/wiki/rpc"},
        urlsource.WithName("RPC Wiki"),
        urlsource.WithMetadataValue("category", "encyclopedia"),
        urlsource.WithMetadataValue("source_type", "web"),
        urlsource.WithMetadataValue("topic", "rpc"),
        urlsource.WithMetadataValue("language", "zh"),
    ),
}
```

## PDF 文件支持

由于 PDF reader 依赖第三方库，为避免主模块引入不必要的依赖，PDF reader 采用独立 `go.mod` 管理。

如需支持 PDF 文件读取，需在代码中手动引入 PDF reader 包进行注册：

```go
import (
    // 引入 PDF reader 以支持 .pdf 文件解析
    _ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
)
```

> **注意**：其他格式（.txt/.md/.csv/.json 等）的 reader 已自动注册，无需手动引入。