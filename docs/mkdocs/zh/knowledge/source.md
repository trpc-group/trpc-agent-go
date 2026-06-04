# 文档源配置

> **示例代码**: [examples/knowledge/sources](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources)

源模块提供了多种文档源类型，每种类型都支持丰富的配置选项。

## 支持的文档源类型

| 源类型 | 说明 | 示例 |
|-------|------|------|
| **文件源 (file)** | 单个文件处理 | [示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/file-source) |
| **目录源 (dir)** | 批量处理目录 | [示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/directory-source) |
| **仓库源 (repo)** | Git 仓库 / 本地仓库目录 | [AST 示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/ast) |
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

## 仓库源 (Repo Source)

仓库源面向代码仓库场景，适合：

- 直接加载 **Git URL**
- 加载本地 checkout 后的 **仓库目录**
- 对单个仓库统一处理 Go / Python / Proto / Markdown 等内容

> **当前开源状态说明**：目前 AST-aware 代码解析能力已开源支持 **Go**、**Python** 和 **Proto / PB**。`C++`、`JavaScript` 等语言能力正在逐步开源中。对于这些尚未开源的语言，仓库源仍可通过普通文档 reader 处理对应文本类文件，但不会产出同等级别的 AST 语义实体。

### 典型场景

- 加载远程 Git 仓库进行代码知识库构建
- 加载本地仓库并限制到某个子目录
- 对单个仓库做 Go + Python + Markdown（以及已支持类型）统一 ingest

### 基本用法

```go
import (
    _ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/golang"
    _ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/python"
    reposource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/repo"
)

repoSrc := reposource.New(
    reposource.WithRepository(
        reposource.Repository{
            URL:    "https://github.com/trpc-group/trpc-go",
            Branch: "main",
        },
    ),
    reposource.WithName("Code Repository"),
    reposource.WithFileExtensions([]string{".go", ".py", ".md"}),
)
```

Go AST reader 和 Python AST reader 是可选模块，需要手动 blank import 完成注册：

- 扫描 `.go` 文件 → `knowledge/document/reader/golang`
- 扫描 `.py` 文件 → `knowledge/document/reader/python`

Proto reader 默认注册，无需额外导入。

> **注意**：Python reader 通过内嵌的 Python 脚本进行 AST 解析，运行时需要系统安装 Python 3.8+（仅使用标准库 `ast` 模块，无第三方依赖）。

### Repository 结构说明

`Repository` 用于描述单个仓库输入，可分别配置版本与范围：

| 字段 | 说明 |
|------|------|
| `URL` | 远程 Git 仓库地址 |
| `Dir` | 本地仓库目录 |
| `Branch` | 指定分支 |
| `Tag` | 指定 tag |
| `Commit` | 指定 commit |
| `Subdir` | 仅扫描仓库中的某个子目录 |
| `RepoName` | 自定义仓库名称 |
| `RepoURL` | 自定义仓库 URL（覆盖默认推导） |

> `URL` 与 `Dir` 通常二选一。当前一个 `repo.Source` 仅处理一个仓库输入。

### 版本选择优先级

当同时配置多个版本字段时，优先级为：

1. `Commit`
2. `Tag`
3. `Branch`

也就是说，如果同时给出 `Commit` 和 `Branch`，最终会 checkout `Commit`。

### 扫描范围控制

仓库源当前推荐暴露的是“仓库语义”相关配置：

- [`WithFileExtensions`](https://github.com/trpc-group/trpc-agent-go/blob/main/knowledge/source/repo/options.go) 控制扫描哪些文件后缀
- [`WithSkipDirs`](https://github.com/trpc-group/trpc-agent-go/blob/main/knowledge/source/repo/options.go) 控制跳过哪些目录名
- [`WithSkipSuffixes`](https://github.com/trpc-group/trpc-agent-go/blob/main/knowledge/source/repo/options.go) 控制跳过哪些文件后缀
- `Repository.Subdir` 控制某个仓库只扫描部分目录

例如只扫描仓库内 `server/` 目录下的 Go 与 Markdown：

```go
repoSrc := reposource.New(
    reposource.WithRepository(
        reposource.Repository{
            URL:    "https://github.com/trpc-group/trpc-go",
            Branch: "main",
            Subdir: "server",
        },
    ),
    reposource.WithFileExtensions([]string{".go", ".md"}),
    reposource.WithSkipSuffixes([]string{".pb.go", ".trpc.go", "_mock.go"}),
)
```

### Metadata 行为

仓库源会在 reader 产出的文档基础上补充仓库级 metadata，例如：

| Metadata | 说明 |
|----------|------|
| `trpc_agent_go_source=repo` | 文档来自仓库源 |
| `trpc_agent_go_repo_path` | 本地克隆后的仓库根目录 |
| `trpc_ast_repo_name` | 仓库名称 |
| `trpc_ast_repo_url` | 仓库 URL |
| `trpc_ast_branch` | 当前解析的版本标识（branch/tag/commit） |
| `trpc_ast_file_path` | 仓库内相对路径 |

注意：

- `trpc_ast_file_path` 语义上表示 **仓库内逻辑路径**，不是远程 Git URL
- 如果输入来自 Git URL，仓库源会先 clone 到临时目录，再以相对路径形式写入 `trpc_ast_file_path`

### 与 AST Reader 的关系

仓库源不会自己解析代码，而是根据文件类型分发到底层 reader：

- `.go` → Go AST reader
- `.py` → Python AST reader
- `.proto` → Proto AST reader
- `.md` → Markdown reader
- 其他已注册扩展 → 对应 reader

因此，仓库源非常适合演示和构建“**同一个仓库内多语言 / 多类型内容统一 ingest**”的知识库。

### 解析效果示例

仓库源对 AST 感知文件（`.go` / `.py` / `.proto`）会按语义实体切块，每个切块包含三层信息：

- **content**：语义完整的代码片段（如一个完整的结构体/类/函数定义），非随机字符截断
- **embedding text**：结构化摘要（name / signature / comment 等），用于向量化检索
- **metadata**：`trpc_ast_*` 系列字段（type / full_name / language / file_path 等），用于精确过滤和定位

下面是对一个 Go 结构体的切块示意：

```text
content:
// Server is a tRPC server.
// One process, one server. A server may offer one or more services.
type Server struct {
    MaxCloseWaitTime time.Duration
    services         map[string]Service
    ...
}

embedding text:
{"name": "Server", "signature": "type Server struct", "type": "Struct",
 "full_name": "trpc.group/trpc-go/trpc-go/server.Server",
 "comment": "Server is a tRPC server. ..."}

metadata:
trpc_agent_go_source: repo
trpc_agent_go_file_path: server/server.go
trpc_ast_repo_name: trpc-go
trpc_ast_repo_url: https://github.com/trpc-group/trpc-go
trpc_ast_file_path: server/server.go
trpc_ast_full_name: trpc.group/trpc-go/trpc-go/server.Server
trpc_ast_type: Struct
trpc_ast_signature: type Server struct
trpc_ast_language: go
```

对于 Python 文件，同样按 Class / Function / Method 粒度切块；对于 `.proto` 文件，则按 service / rpc / message / enum 粒度切块。

### 代码图谱（GraphRAG）

> **示例代码**: [examples/knowledge/features/graphrag](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/graphrag)

除了向量化检索（embedding + vector store），仓库源还支持以图谱的方式存储代码结构关系。AST reader 在解析时会提取实体之间的边关系，结合图数据库（Apache AGE）实现结构化的代码导航。

![AGE Graph Viewer - 代码图谱可视化](../../assets/img/knowledge/viewer.jpg)

#### 边类型

| 边类型 | 含义 | 示例 |
|--------|------|------|
| `CALLS` | 函数/方法调用 | `main` → `server.Start` |
| `METHOD` | 类/结构体的方法 | `Server` → `Server.Start` |
| `FIELD` | 结构体字段 | `Server` → `services` |
| `PARAM` | 函数参数 | `NewServer` → `opts` |
| `RETURNS` | 函数返回类型 | `NewServer` → `Server` |
| `INHERITS` | 继承/实现 | `MyRunner` → `BaseRunner` |
| `CONTAINS` | 包含关系 | `package server` → `Server` |

#### 用法

```go
import (
    _ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/golang"
    _ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/python"
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    agegraphstore "trpc.group/trpc-go/trpc-agent-go/knowledge/graphstore/age"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source/repo"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
    knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
)

// 1. 配置仓库源
repoSrc := repo.New(
    repo.WithRepository(repo.Repository{URL: "https://github.com/example/repo"}),
    repo.WithFileExtensions([]string{".go", ".py"}),
)

// 2. 创建 GraphKnowledge（图 + 向量混合检索）
gk := knowledge.NewGraphKnowledge(
    knowledge.WithGraphStore(agegraphstore.New(db, agegraphstore.WithGraphName("my_graph"))),
    knowledge.WithGraphVectorStore(pgvector.New(db)),
    knowledge.WithGraphEmbedder(embedder),
)

// 3. 加载图数据
gk.LoadGraphSource(ctx, repoSrc)

// 4. 将图工具注入 Agent
toolSet := knowledgetool.NewCodeGraphSearchTool(gk)
// 暴露 code_graph_search / code_graph_traverse / code_graph_find_paths 三个工具
```

#### Agent 可用的图工具

| 工具 | 功能 |
|------|------|
| `code_graph_search` | 向量检索 AST 节点，返回匹配的代码实体 |
| `code_graph_traverse` | 从指定节点出发，沿边遍历关联节点（如查找某函数的所有调用者） |
| `code_graph_find_paths` | 查找两个代码实体之间的路径（如追踪调用链） |

通过向量检索定位入口节点，再结合图遍历探索结构关系，Agent 可以在无需阅读大量源码的情况下理解代码架构。

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

## 分块策略 (Chunking Strategy)

> **示例代码**: [fixed-chunking](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/fixed-chunking) | [recursive-chunking](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/recursive-chunking)

分块（Chunking）是将长文档拆分为较小片段的过程，这对于向量检索至关重要。框架提供了多种内置分块策略，同时支持自定义分块策略。

### 内置分块策略

| 策略 | 说明 | 适用场景 |
|-----|------|---------|
| **FixedSizeChunking** | 固定大小分块 | 通用文本，简单快速 |
| **RecursiveChunking** | 递归分块，按分隔符层级拆分 | 保持语义完整性 |
| **MarkdownChunking** | 按 Markdown 结构分块 | Markdown 文档（默认） |
| **JSONChunking** | 按 JSON 结构分块 | JSON 文件（默认） |

### 默认行为

每种文件类型都有相关的分块策略：

- `.md` 文件 → MarkdownChunking（按标题层级 H1→H6→段落→固定大小 递归分块）
- `.json` 文件 → JSONChunking（按 JSON 结构分块）
- `.txt/.csv/.docx` 等 → FixedSizeChunking

**默认参数**：

| 参数 | 默认值 | 说明 |
|-----|-------|------|
| ChunkSize | 1024 | 每个分块的最大字符数 |
| Overlap | 128 | 相邻分块之间的重叠字符数 |

> 默认的分块策略都受 `chunkSize` 参数影响。`overlap` 参数仅对 FixedSizeChunking、RecursiveChunking、MarkdownChunking 生效，JSONChunking 不支持 overlap。

可通过 `WithChunkSize` 和 `WithChunkOverlap` 调整默认策略的参数：

```go
fileSrc := filesource.New(
    []string{"./data/document.txt"},
    filesource.WithChunkSize(512),     // 分块大小（字符数）
    filesource.WithChunkOverlap(64),   // 分块重叠（字符数）
)
```

### 自定义分块策略

使用 `WithCustomChunkingStrategy` 可覆盖默认分块策略。

> **注意**：自定义分块策略会完全覆盖 `WithChunkSize` 和 `WithChunkOverlap` 的配置，分块参数需在自定义策略内部设置。

#### FixedSizeChunking - 固定大小分块

将文本按固定字符数分割，支持重叠：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
)

// 创建固定大小分块策略
fixedChunking := chunking.NewFixedSizeChunking(
    chunking.WithChunkSize(512),   // 每块最大 512 字符
    chunking.WithOverlap(64),      // 块间重叠 64 字符
)

fileSrc := filesource.New(
    []string{"./data/document.md"},
    filesource.WithCustomChunkingStrategy(fixedChunking),
)
```

#### RecursiveChunking - 递归分块

按分隔符层级递归拆分，尽量在自然边界处分割：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
)

// 创建递归分块策略
recursiveChunking := chunking.NewRecursiveChunking(
    chunking.WithRecursiveChunkSize(512),   // 最大块大小
    chunking.WithRecursiveOverlap(64),      // 块间重叠
    // 自定义分隔符优先级（可选）
    chunking.WithRecursiveSeparators([]string{"\n\n", "\n", ". ", " "}),
)

fileSrc := filesource.New(
    []string{"./data/article.txt"},
    filesource.WithCustomChunkingStrategy(recursiveChunking),
)
```

**分隔符优先级说明**：

1. `\n\n` - 优先按段落分割
2. `\n` - 其次按行分割
3. `. ` - 再按句子分割
4. ` ` - 按空格分割

递归分块会尝试使用更高优先级的分隔符，仅当分块仍超过最大大小时才使用下一级分隔符。若所有分隔符都无法将文本切分到 chunkSize 以内，则按 chunkSize 强制切分。




## 内容转换器 (Transformer)

> **示例代码**: [examples/knowledge/features/transform](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/transform)

Transformer 用于在文档分块（Chunking）前后对内容进行预处理和后处理。这对于清理从 PDF、网页等来源提取的文本特别有用，可以去除多余的空白字符、重复字符等噪声。

### 处理流程

```
文档 → Preprocess（预处理） → 处理后的文档 → Chunking（分块） → 分块 → Postprocess（后处理） → 最终分块
```

### 内置转换器

#### CharFilter - 字符过滤器

移除指定的字符或字符串：

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/transform"

// 移除换行符和制表符
filter := transform.NewCharFilter("\n", "\t", "\r")
```

#### CharDedup - 字符去重器

将连续重复的字符或字符串合并为单个：

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/transform"

// 将多个连续空格合并为单个空格，多个换行合并为单个换行
dedup := transform.NewCharDedup(" ", "\n")

// 示例：
// 输入:  "hello     world\n\n\nfoo"
// 输出:  "hello world\nfoo"
```

### 使用方式

Transformer 通过 `WithTransformers` 选项传递给各类文档源：

```go
import (
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
    autosource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

// 创建转换器
filter := transform.NewCharFilter("\t")           // 移除制表符
dedup := transform.NewCharDedup(" ", "\n")        // 合并连续空格和换行

// 文件源使用转换器
fileSrc := filesource.New(
    []string{"./data/document.pdf"},
    filesource.WithTransformers(filter, dedup),
)

// 目录源使用转换器
dirSrc := dirsource.New(
    []string{"./docs"},
    dirsource.WithTransformers(filter, dedup),
)

// URL 源使用转换器
urlSrc := urlsource.New(
    []string{"https://example.com/article"},
    urlsource.WithTransformers(filter, dedup),
)

// 自动源使用转换器
autoSrc := autosource.New(
    []string{"./mixed-content"},
    autosource.WithTransformers(filter, dedup),
)
```

### 组合多个转换器

多个转换器按顺序依次执行：

```go
// 先移除制表符，再合并连续空格
filter := transform.NewCharFilter("\t")
dedup := transform.NewCharDedup(" ")

src := filesource.New(
    []string{"./data/messy.txt"},
    filesource.WithTransformers(filter, dedup),  // 按顺序执行
)
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
