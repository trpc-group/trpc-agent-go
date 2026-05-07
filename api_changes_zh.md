# PR #1731 API 变更文档

> 基准：`origin/main` (1c254efb) → HEAD (codex/graph-processing-interface)

---

## 一、不兼容变更

本 PR 对已有公开 API 的不兼容变更极少，主要集中在 `knowledge/tool` 包的已有导出结构体新增字段和 `internal` 包的常量移除。

### 1.1 `knowledge/internal/codeast`（internal 包，不影响外部消费者）

| 符号 | 变更类型 | 说明 |
|------|----------|------|
| `ScopeDocument Scope = "document"` | **移除常量** | 原用于标记文档类型内容的 scope，现已删除。仅保留 `ScopeCode` 和 `ScopeExample`。因位于 `internal/` 包内，外部模块无法直接引用，不构成公开 API 破坏。 |

### 1.2 `knowledge/tool`（公开包 — 结构体字段变更）

以下变更**不会**破坏已有代码的编译，但改变了序列化输出和工具的 JSON schema：

| 符号 | 变更类型 | 旧定义 | 新定义 |
|------|----------|--------|--------|
| `DocumentResult` | **新增字段** | `Text`, `Metadata`, `Score` | 新增 `ID string \`json:"id,omitempty"\`` — 返回文档 ID，便于后续图遍历引用 |
| `KnowledgeSearchRequestWithFilter` | **新增字段** | `Query`, `Filter` | 新增 `IncludeContent *bool \`json:"include_content,omitempty"\`` — LLM 可控制是否返回全文内容 |

> **影响评估**：Go 中结构体新增字段是向后兼容的（已有代码无需改动即可编译运行）。但如果下游有基于 JSON schema 做严格校验的场景，需要注意 schema 发生了扩展。

### 1.3 `go-apidiff` CI 检查说明

CI 中 `go-apidiff` 报 fail 是因为检测到本 PR 引入了大量新导出符号（见下方第二节）。此 check 属于 informational 性质，用于提醒 reviewer 关注 API 变更，不阻塞合入。

---

## 二、新增导出接口、类型与函数

### 2.1 新增包：`knowledge/graph`

图数据模型与查询定义。

| 符号 | 类型 | 定义 |
|------|------|------|
| `Node` | struct | `ID string`, `Name string`, `Content string`, `Metadata map[string]any` |
| `Edge` | struct | `ID string`, `FromID string`, `ToID string`, `Type string`, `Metadata map[string]any` |
| `Data` | struct | `Nodes []*Node`, `Edges []*Edge` |
| `Direction` | type (string) | 图遍历方向枚举 |
| `DirectionOut` | const | `"out"` — 出边方向 |
| `DirectionIn` | const | `"in"` — 入边方向 |
| `DirectionBoth` | const | `"both"` — 双向 |
| `TraverseQuery` | struct | `StartIDs []string`, `Direction`, `EdgeTypes []string`, `MaxDepth int`, `MaxNodes int` |
| `TraverseResult` | struct | `Nodes []*Node`, `Edges []*Edge`, `Truncated bool`, `Message string` |
| `PathQuery` | struct | `FromID string`, `ToID string`, `Direction`, `EdgeTypes []string`, `MaxDepth int`, `MaxPaths int` |
| `PathResult` | struct | `Paths []*Path`, `Truncated bool`, `Message string` |
| `Path` | struct | `Nodes []*Node`, `Edges []*Edge` |

### 2.2 新增包：`knowledge/graphstore`

图存储抽象接口。

| 符号 | 类型 | 定义 |
|------|------|------|
| `Store` | interface | `AddNodes(ctx, []*graph.Node) error` / `AddEdges(ctx, []*graph.Edge) error` / `Traverse(ctx, *graph.TraverseQuery) (*graph.TraverseResult, error)` / `FindPaths(ctx, *graph.PathQuery) (*graph.PathResult, error)` / `Close() error` |

### 2.3 新增子模块：`knowledge/graphstore/age`

Apache AGE (PostgreSQL 图扩展) 后端实现。

| 符号 | 类型 | 说明 |
|------|------|------|
| `Store` | struct | AGE 图存储后端，实现 `graphstore.Store` |
| `New(opts ...Option) (*Store, error)` | func | 创建 AGE 存储实例，自动初始化图和标签 |
| `(*Store).AddNodes(ctx, []*graph.Node) error` | method | 插入或更新图节点 (MERGE) |
| `(*Store).AddEdges(ctx, []*graph.Edge) error` | method | 插入或更新图边 (MERGE) |
| `(*Store).Traverse(ctx, *graph.TraverseQuery) (*graph.TraverseResult, error)` | method | 图遍历 |
| `(*Store).FindPaths(ctx, *graph.PathQuery) (*graph.PathResult, error)` | method | 路径查找 |
| `(*Store).Close() error` | method | 关闭数据库连接 |
| `Option` | type (func) | 配置函数类型 |
| `WithGraphName(string) Option` | func | 设置 AGE 图名称（默认 `knowledge_graph`） |
| `WithClientDSN(string) Option` | func | 设置 PostgreSQL DSN 连接串 |
| `WithPostgresInstance(string) Option` | func | 使用通过 `storage/postgres` 注册的已有实例 |

### 2.4 新增：`knowledge` 包

#### 接口

| 符号 | 类型 | 定义 |
|------|------|------|
| `GraphKnowledge` | interface | 嵌入 `Knowledge`，扩展 `Traverse(ctx, *graph.TraverseQuery) (*graph.TraverseResult, error)` 和 `FindPaths(ctx, *graph.PathQuery) (*graph.PathResult, error)` |

#### 默认实现与配置

| 符号 | 类型 | 说明 |
|------|------|------|
| `BuiltinGraphKnowledge` | struct | `GraphKnowledge` 的默认实现，组合 `graphstore.Store` + `vectorstore.VectorStore` + `embedder.Embedder` |
| `NewGraphKnowledge(opts ...GraphKnowledgeOption) *BuiltinGraphKnowledge` | func | 创建 `BuiltinGraphKnowledge` 实例 |
| `GraphKnowledgeOption` | type (func) | `BuiltinGraphKnowledge` 配置函数类型 |
| `WithGraphStore(graphstore.Store) GraphKnowledgeOption` | func | 设置图存储后端 |
| `WithGraphVectorStore(vectorstore.VectorStore) GraphKnowledgeOption` | func | 设置向量存储（用于 search 的种子检索） |
| `WithGraphEmbedder(embedder.Embedder) GraphKnowledgeOption` | func | 设置 embedding 模型 |

#### 图数据加载

| 符号 | 类型 | 说明 |
|------|------|------|
| `(*BuiltinGraphKnowledge).LoadGraphSource(ctx, source.GraphSource, ...GraphLoadOption) error` | method | 从 GraphSource 读取图数据，写入图存储 + 向量索引。这是构建 GraphRAG pipeline 的核心入口 |
| `GraphLoadOption` | type (func) | 图数据加载配置函数类型 |
| `GraphLoadConcurrency` | struct | 加载并发配置：`AddNodeRoutines int`、`AddEdgeRoutines int`、`EmbeddingRoutines int` |
| `WithGraphLoadProgress(bool) GraphLoadOption` | func | 启用/禁用加载进度日志 |
| `WithGraphLoadProgressStepSize(int) GraphLoadOption` | func | 设置进度日志更新粒度 |
| `WithGraphLoadConcurrency(GraphLoadConcurrency) GraphLoadOption` | func | 设置加载各阶段的并发度 |
| `WithGraphLoadReadGraphOpts(...source.ReadGraphOption) GraphLoadOption` | func | 传递选项给 `GraphSource.ReadGraph` 调用（如解析并发度） |

### 2.5 新增：`knowledge/source` 包

| 符号 | 类型 | 说明 |
|------|------|------|
| `GraphSource` | interface | 可输出图数据的知识源，方法 `ReadGraph(ctx, ...ReadGraphOption) (*graph.Data, error)` |
| `ReadGraphOption` | type (func) | ReadGraph 配置选项类型 |
| `WithReadGraphParseConcurrency(int) ReadGraphOption` | func | 设置代码解析并发度 |
| `ReadGraphParseConcurrency([]ReadGraphOption) int` | func | 提取配置的并发度值（供 GraphSource 实现者调用） |

### 2.6 新增：`knowledge/source/repo` 包

| 符号 | 类型 | 说明 |
|------|------|------|
| `(*RepoSource).ReadGraph(ctx, ...source.ReadGraphOption) (*graph.Data, error)` | method | 将仓库代码 AST 符号转换为图数据（节点 + 边），`RepoSource` 现同时实现 `source.Source` 和 `source.GraphSource` |
| `WithParseConcurrency(int) Option` | func | 设置代码 AST 解析并发度（用于 `ReadGraph`），零或负值使用解析器默认值 |

### 2.7 新增：`knowledge/tool` 包

#### 工具集构造函数

| 符号 | 签名 | 说明 |
|------|------|------|
| `NewCodeGraphSearchTool` | `(kb GraphKnowledge, opts ...CodeSearchOption) tool.ToolSet` | 创建面向代码的图工具集，包含 search / traverse / find_paths 三个工具。工具名根据 set name 自动生成（默认 `code_graph_search`, `code_graph_traverse`, `code_graph_find_paths`） |
| `NewGraphToolSet` | `(kb GraphKnowledge, agenticFilterInfo map[string][]any, searchOpts ...Option) tool.ToolSet` | 创建通用图工具集（非代码专用），`agenticFilterInfo` 声明暴露给 LLM 的可过滤元数据字段及其枚举值 |
| `NewGraphTraverseTool` | `(kb GraphKnowledge, opts ...GraphToolOption) tool.Tool` | 创建独立的图遍历工具 |
| `NewGraphFindPathsTool` | `(kb GraphKnowledge, opts ...GraphToolOption) tool.Tool` | 创建独立的图路径查找工具 |

#### 配置类型

| 符号 | 类型 | 说明 |
|------|------|------|
| `GraphToolOption` | type (func) | 图工具配置函数类型 |
| `WithGraphToolName(string) GraphToolOption` | func | 设置图工具名称 |
| `WithGraphToolDescription(string) GraphToolOption` | func | 设置图工具描述 |

#### LLM 请求体结构

| 符号 | 字段 | 说明 |
|------|------|------|
| `GraphTraverseRequest` | `StartIDs []string`, `Direction string`, `EdgeTypes []string`, `MaxDepth int`, `MaxNodes int`, `IncludeContent bool` | 图遍历工具的 LLM 输入。要求 `start_ids` 必填，需先通过 search 获取节点 ID |
| `GraphFindPathsRequest` | `FromID string`, `ToID string`, `Direction string`, `EdgeTypes []string`, `MaxDepth int`, `MaxPaths int`, `IncludeContent bool` | 图路径查找工具的 LLM 输入。要求 `from_id` 和 `to_id` 必填 |

---

## 三、架构关系图

```
knowledge.Knowledge  ──嵌入──▶  knowledge.GraphKnowledge
                                    │
                                    ├── Traverse(ctx, *graph.TraverseQuery)
                                    └── FindPaths(ctx, *graph.PathQuery)

knowledge.BuiltinGraphKnowledge (默认实现)
    ├── graphstore.Store         (图存储)
    ├── vectorstore.VectorStore  (向量存储，用于 search 种子检索)
    ├── embedder.Embedder        (向量化)
    └── LoadGraphSource(ctx, src) (从 GraphSource 加载图数据)

graphstore.Store                    knowledge/graphstore/age.Store (实现)
    ├── AddNodes                        └── 基于 PostgreSQL + Apache AGE
    ├── AddEdges
    ├── Traverse
    ├── FindPaths
    └── Close

source.GraphSource                  source/repo.RepoSource (实现)
    └── ReadGraph(ctx)                  └── Go AST → graph.Data (节点+边)

tool.NewCodeGraphSearchTool ──创建──▶  tool.ToolSet
    ├── code_graph_search       (向量搜索 + 元数据过滤)
    ├── code_graph_traverse     (图遍历，需要节点 ID)
    └── code_graph_find_paths   (路径查找，需要两端节点 ID)
```
