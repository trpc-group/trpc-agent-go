# PGVector（PostgreSQL + pgvector）

> **示例代码**: [examples/knowledge/vectorstores/postgres](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/postgres)

PGVector 是基于 PostgreSQL + pgvector 扩展的向量存储实现，支持混合检索（向量相似度 + 文本相关性）。

## 基础配置

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    vectorpgvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
)

// PostgreSQL + pgvector
pgVS, err := vectorpgvector.New(
    vectorpgvector.WithPGVectorClientDSN("postgres://postgres:your-password@127.0.0.1:5432/your-database?sslmode=disable"),
    // 根据 embedding 模型设置索引维度（text-embedding-3-small 为 1536）
    vectorpgvector.WithIndexDimension(1536),
    // vectorpgvector.WithEnableTSVector(true), // 启用全文检索，支持 Keyword/Hybrid 搜索
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

PGVector 支持两种连接配置方式（按优先级从高到低）：

#### 1. 直接连接配置

```go
// Option 1: Use DSN connection string (recommended)
pgVS, err := vectorpgvector.New(
    vectorpgvector.WithPGVectorClientDSN("postgres://user:password@localhost:5432/mydb?sslmode=disable"),
    vectorpgvector.WithIndexDimension(1536),
)

// Option 2: Use individual connection parameters
// pgVS, err := vectorpgvector.New(
//     vectorpgvector.WithHost("localhost"),
//     vectorpgvector.WithPort(5432),
//     vectorpgvector.WithUser("postgres"),
//     vectorpgvector.WithPassword("your-password"),
//     vectorpgvector.WithDatabase("mydb"),
//     vectorpgvector.WithSSLMode("disable"),
//     vectorpgvector.WithTable("documents"),
//     vectorpgvector.WithIndexDimension(1536),
// )
```

#### 2. 使用已注册实例

复用已在 `storage/postgres` 中注册的 PostgreSQL 实例，适合多个组件共享同一数据库连接的场景。

```go
import "trpc.group/trpc-go/trpc-agent-go/storage/postgres"

// Step 1: Register instance
postgres.RegisterPostgresInstance("my-postgres",
    postgres.WithClientConnString("postgres://user:password@localhost:5432/mydb?sslmode=disable"),
)

// Step 2: Use registered instance
pgVS, err := vectorpgvector.New(
    vectorpgvector.WithPostgresInstance("my-postgres"),
    vectorpgvector.WithIndexDimension(1536),
)
```

**优先级规则**：
- `WithPGVectorClientDSN()` / `WithHost()...` > `WithPostgresInstance()`
- 如果同时指定多个方式，优先级高的生效

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithPGVectorClientDSN(dsn)` | PostgreSQL 连接字符串 | - |
| `WithHost(host)` | 数据库主机地址 | `"localhost"` |
| `WithPort(port)` | 数据库端口 | `5432` |
| `WithUser(user)` | 数据库用户名 | - |
| `WithPassword(password)` | 数据库密码 | - |
| `WithDatabase(database)` | 数据库名称 | `"trpc_agent_go"` |
| `WithTable(table)` | 表名称 | `"documents"` |
| `WithSSLMode(mode)` | SSL 模式 | `"disable"` |
| `WithPostgresInstance(name)` | 使用已注册的 PostgreSQL 实例 | - |

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
| `WithHybridSearchWeights(vector, text)` | 混合检索权重（向量/文本），仅 Weighted 模式生效 | `0.7, 0.3` |
| `WithHybridFusionMode(mode)` | 混合检索融合模式（`HybridFusionWeighted` / `HybridFusionRRF`） | `HybridFusionWeighted` |
| `WithRRFParams(params)` | RRF 参数（K 和 CandidateRatio），仅 RRF 模式生效 | `K=60, CandidateRatio=3` |
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

## 全文检索

PGVector 支持全文检索（TSVector），可用于 Keyword Search 和 Hybrid Search（混合检索）。

> **⚠️ 重要提示**: Keyword Search 和 Hybrid Search 需要通过 `WithEnableTSVector(true)` 启用 PostgreSQL 全文检索功能。

```go
// Option 1: Weighted fusion (default)
pgVS, err := vectorpgvector.New(
    vectorpgvector.WithPGVectorClientDSN(dsn),
    vectorpgvector.WithIndexDimension(1536),
    vectorpgvector.WithEnableTSVector(true),           // ✅ 开启全文检索支持
    vectorpgvector.WithHybridSearchWeights(0.7, 0.3),  // 设置混合检索权重 (70% 向量 + 30% 文本)
    vectorpgvector.WithLanguageExtension("english"),   // 设置分词语言（支持中文需安装 zhparser/jieba）
)

// Option 2: Reciprocal Rank Fusion (RRF)
pgVS, err := vectorpgvector.New(
    vectorpgvector.WithPGVectorClientDSN(dsn),
    vectorpgvector.WithIndexDimension(1536),
    vectorpgvector.WithEnableTSVector(true),
    vectorpgvector.WithHybridFusionMode(vectorpgvector.HybridFusionRRF),
    vectorpgvector.WithRRFParams(&vectorpgvector.RRFParams{
        K:              60, // RRF constant, smaller = more weight to top results
        CandidateRatio: 3,  // fetch 3x candidates from each sub-search
    }),
)
```

### 搜索模式支持

| 搜索模式 | 是否需要 TSVector | 说明 |
|---------|------------------|------|
| **Vector Search** | ❌ | 仅使用向量索引 |
| **Keyword Search** | ✅ 必需 | 依赖 PostgreSQL `tsvector` 全文索引 |
| **Hybrid Search** | ✅ 必需 | 同时使用向量索引和 `tsvector` 索引 |
| **Filter Search** | ❌ | 仅过滤元数据 |

**如果未启用 `WithEnableTSVector(true)`**:

系统会自动降级搜索模式，不会报错：
- 尝试使用 Keyword/Hybrid 搜索时 → 自动降级为 **Vector Search**（有向量）或 **Filter Search**（无向量）
- 会输出 INFO 日志提示降级原因

**注意**: 从 SearchTool 到 VectorStore 的默认调用链路不会主动指定 `SearchModeKeyword`，因此通常不会触发 Keyword Search，而是使用默认的 Vector 或 Hybrid 搜索。

## 搜索模式与 Score 归一化

> **💡 提示**: 本节内容为具体计算细节，默认用户无需关心。PGVector 会自动处理所有搜索模式的评分归一化。

PGVector 支持多种搜索模式，所有模式的 score 都归一化到 `[0, 1]` 范围，分数越高表示相关性越强。

### 1. Vector Search (向量搜索)

**SQL 模板** (使用子查询避免重复计算):
```sql
SELECT *, vector_score as score
FROM (
  SELECT *, 
         (1.0 - (embedding <=> $1) / 2.0) as vector_score,
         0.0 as text_score
  FROM table_name
  WHERE ...
  ORDER BY embedding <=> $1
  LIMIT 10
) subq
```

**归一化公式**:
```
vector_score = 1.0 - (cosine_distance / 2.0)
```

**数学原理**:
- PGVector `<=>` 操作符返回 **Cosine Distance**: `d ∈ [0, 2]`
  - `d = 0`: 向量完全相同
  - `d = 1`: 向量正交
  - `d = 2`: 向量完全相反
- Cosine Similarity: `s = 1 - d ∈ [-1, 1]`
- 归一化到 `[0, 1]`: `score = (s + 1) / 2 = (2 - d) / 2 = 1 - d/2`

**示例**:
- `distance = 0.2` → `score = 1 - 0.2/2 = 0.90` (高度相似)
- `distance = 1.0` → `score = 1 - 1.0/2 = 0.50` (正交)
- `distance = 1.8` → `score = 1 - 1.8/2 = 0.10` (几乎相反)

---

### 2. Keyword Search (关键词搜索)

**SQL 模板** (使用子查询避免重复计算):
```sql
SELECT *, text_score as score
FROM (
  SELECT *, 
         0.0 as vector_score,
         (ts_rank(to_tsvector('english', content), websearch_to_tsquery('english', $1)) 
          / (ts_rank(to_tsvector('english', content), websearch_to_tsquery('english', $1)) + 0.1)) as text_score
  FROM table_name
  WHERE to_tsvector('english', content) @@ websearch_to_tsquery('english', $1)
  ORDER BY (ts_rank(...) / (ts_rank(...) + 0.1)) DESC, created_at DESC
  LIMIT 10
) subq
```

**归一化公式**:
```
text_score = rank / (rank + c)
```
其中 `c = 0.1` (sparseNormConstant)

**数学原理**:
- PostgreSQL `ts_rank()` 返回原始文本相关性分数，范围不固定（通常 `[0, ∞)`）
- 使用双曲函数归一化: `f(x) = x / (x + c)` 将其映射到 `[0, 1)`
- 参数 `c` 控制敏感度：
  - `c` 越小：对小 rank 值更敏感，区分度更高
  - `c` 越大：对大 rank 值更宽容，趋于饱和

**示例** (c = 0.1):
- `rank = 1.0` → `score = 1.0 / 1.1 = 0.909`
- `rank = 0.5` → `score = 0.5 / 0.6 = 0.833`
- `rank = 0.1` → `score = 0.1 / 0.2 = 0.500`
- `rank = 0.01` → `score = 0.01 / 0.11 = 0.091`

**计算流程示例**:

假设用户查询 `"machine learning"`，数据库中有一篇文档内容为 `"Machine learning enables intelligent systems..."`

```sql
-- Step 1: 将查询转换为 tsquery
websearch_to_tsquery('english', 'machine learning')
→ 'machin' & 'learn'  -- (automatic stemming: machine → machin, learning → learn)

-- Step 2: 将文档内容转换为 tsvector
to_tsvector('english', 'Machine learning enables intelligent systems...')
→ 'enabl':3 'intellig':4 'learn':2 'machin':1 'system':5

-- Step 3: 检查是否匹配 (@@)
to_tsvector(...) @@ websearch_to_tsquery(...)
→ true  -- (包含 'machin' 和 'learn' 两个词干)

-- Step 4: 计算相关性分数
ts_rank(to_tsvector(...), websearch_to_tsquery(...))
→ 2.5  -- (原始 rank 值)

-- Step 5: 归一化到 [0, 1)
text_score = 2.5 / (2.5 + 0.1) = 0.961
```

**核心函数**:
- `to_tsvector()`: 文本 → 可搜索向量（分词、词干化）
- `websearch_to_tsquery()`: 用户查询 → 搜索表达式（支持 `"引号"`, `OR`, `-排除`）
- `@@`: 匹配检查（true/false）
- `ts_rank()`: 计算相关性分数


---

### 3. Hybrid Search (混合搜索)

Hybrid Search 支持两种融合模式，通过 `WithHybridFusionMode()` 切换。

#### 3a. Weighted Fusion（加权融合，默认）

**SQL 模板** (使用子查询避免重复计算):
```sql
SELECT *, (vector_score * 0.700 + text_score * 0.300) as score
FROM (
  SELECT *,
         (1.0 - (embedding <=> $1) / 2.0) as vector_score,
         (COALESCE(ts_rank(to_tsvector('english', content),
                           websearch_to_tsquery('english', $2)), 0)
          / (COALESCE(ts_rank(...), 0) + 0.1000)) as text_score
  FROM documents
  WHERE ...
  ORDER BY ((1.0 - (embedding <=> $1) / 2.0) * 0.700
            + (ts_rank_expr / (ts_rank_expr + 0.1000)) * 0.300) DESC
  LIMIT 10
) subq
```

**归一化公式**:
```
hybrid_score = vector_score × w_v + text_score × w_t
```
其中默认权重: `w_v = 0.7`, `w_t = 0.3`

**数学原理**:
- 分别计算 `vector_score` 和 `text_score`（如上述两个模式）
- 使用线性加权组合，权重可通过 `WithHybridSearchWeights()` 配置
- 由于两个 score 都在 `[0, 1]` 范围且 `w_v + w_t = 1`，最终 `hybrid_score ∈ [0, 1]`
- **重要**: 不会强制过滤文本不匹配的文档，因为向量相似度权重更高(0.7)，即使文本不匹配也可能返回高质量结果

**示例**:
```
Case 1: Both vector and text match well
  vector_score = 0.85, text_score = 0.90
  hybrid_score = 0.85 × 0.7 + 0.90 × 0.3 = 0.865

Case 2: High vector similarity but no text match
  vector_score = 0.95, text_score = 0.0
  hybrid_score = 0.95 × 0.7 + 0.0 × 0.3 = 0.665  -- Still returns high-quality result
```

#### 3b. Reciprocal Rank Fusion (RRF)

RRF 是一种基于排名的融合策略，不依赖分数归一化，而是根据文档在各子搜索中的排名来计算最终得分。适用于向量分数和文本分数量纲差异大、难以直接加权的场景。

**执行流程** (并行子查询 + Go 代码融合):

RRF 模式将向量搜索和文本搜索拆分为两个独立的 SQL 查询并行执行，然后在 Go 代码层完成 RRF 分数融合：

```sql
-- Query 1: Vector rank sub-search (parallel)
SELECT id, ROW_NUMBER() OVER (ORDER BY embedding <=> $1) as rank
FROM documents WHERE ... LIMIT 30  -- limit * CandidateRatio

-- Query 2: Text rank sub-search (parallel)
SELECT id, ROW_NUMBER() OVER (
  ORDER BY ts_rank(to_tsvector('english', content),
                   websearch_to_tsquery('english', $1)) DESC
) as rank
FROM documents
WHERE ... AND to_tsvector('english', content) @@ websearch_to_tsquery('english', $1)
LIMIT 30

-- Query 3: Fetch full documents by fused top-N IDs
SELECT * FROM documents WHERE id IN ($1, $2, ...)
```

```go
// Go-level RRF fusion:
// 1. Merge (id, rank) pairs from both sub-searches
// 2. Compute RRF score: score(d) = 1/(k + rank_v) + 1/(k + rank_t)
// 3. Sort by score DESC, take top N
// 4. Fetch full documents by IDs
```

**RRF 公式**:
```
score(d) = sum(1 / (k + rank_i))  for each ranking list i
```

**参数说明**:

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `K` | RRF 常数，必须 > 0。越小则排名靠前的结果权重越大 | `60` |
| `CandidateRatio` | 候选倍率，必须 > 0。每个子搜索取 `limit × CandidateRatio` 条候选 | `3` |

**K 值的影响**:
- `K=1`: 排名第 1 得分 `1/2 = 0.500`，排名第 10 得分 `1/11 ≈ 0.091`，差距 ~5.5x
- `K=60`（默认）: 排名第 1 得分 `1/61 ≈ 0.016`，排名第 10 得分 `1/70 ≈ 0.014`，差距 ~1.15x

K=60 是学术界广泛使用的默认值，让融合更"民主"，不会过度偏向某个单一排序列表中排名特别靠前的结果。

**注意事项**:
- RRF 模式下 `WithHybridSearchWeights()` 不生效，两个排序列表的 RRF 分数直接相加
- RRF 模式下 `MinScore` 不生效。RRF 分数是基于排名的（K=60 时最大约 0.033），与 [0,1] 范围的相似度分数语义不兼容
- 两个子查询并行执行，延迟 = max(vector_latency, text_latency)，比单条复杂 CTE 更高效

---

### 4. Filter Search (过滤搜索)

**SQL 模板**:
```sql
SELECT *, 
       0.0 as vector_score,
       0.0 as text_score,
       1.0 as score
FROM table_name
WHERE [metadata filters]
ORDER BY created_at DESC
LIMIT 10
```

**说明**:
- 纯元数据过滤，不涉及向量或文本相似度
- 所有结果 `score = 1.0`（因为都满足过滤条件）
- 按创建时间降序排序

---
