# PGVector (PostgreSQL + pgvector)

> **Example Code**: [examples/knowledge/vectorstores/postgres](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/postgres)

PGVector is a vector store implementation based on PostgreSQL + pgvector extension, supporting hybrid retrieval (vector similarity + text relevance).

## Basic Configuration

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    vectorpgvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
)

// PostgreSQL + pgvector
pgVS, err := vectorpgvector.New(
    vectorpgvector.WithPGVectorClientDSN("postgres://postgres:your-password@127.0.0.1:5432/your-database?sslmode=disable"),
    // Set index dimension based on embedding model (text-embedding-3-small is 1536)
    vectorpgvector.WithIndexDimension(1536),
    // vectorpgvector.WithEnableTSVector(true), // Enable full-text search for Keyword/Hybrid search
)
if err != nil {
    // Handle error
}

kb := knowledge.New(
    knowledge.WithVectorStore(pgVS),
    knowledge.WithEmbedder(embedder), // Configure embedder
)
```

## Configuration Options

### Connection Configuration

PGVector supports two connection configuration methods (in priority order from high to low):

#### 1. Direct Connection Configuration

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

#### 2. Use Registered Instance

Reuse a PostgreSQL instance already registered in `storage/postgres`, suitable for scenarios where multiple components share the same database connection.

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

**Priority Rules**:
- `WithPGVectorClientDSN()` / `WithHost()...` > `WithPostgresInstance()`
- If multiple methods are specified simultaneously, the higher priority takes effect

| Option | Description | Default Value |
|--------|-------------|---------------|
| `WithPGVectorClientDSN(dsn)` | PostgreSQL connection string | - |
| `WithHost(host)` | Database host address | `"localhost"` |
| `WithPort(port)` | Database port | `5432` |
| `WithUser(user)` | Database username | - |
| `WithPassword(password)` | Database password | - |
| `WithDatabase(database)` | Database name | `"trpc_agent_go"` |
| `WithTable(table)` | Table name | `"documents"` |
| `WithSSLMode(mode)` | SSL mode | `"disable"` |
| `WithPostgresInstance(name)` | Use registered PostgreSQL instance | - |

### Vector Configuration

| Option | Description | Default Value |
|--------|-------------|---------------|
| `WithIndexDimension(dim)` | Vector dimension (must match embedding model) | `1536` |
| `WithVectorIndexType(type)` | Vector index type (`VectorIndexHNSW` / `VectorIndexIVFFlat`) | `VectorIndexHNSW` |
| `WithHNSWIndexParams(params)` | HNSW index parameters (M, EfConstruction) | `M=16, EfConstruction=64` |
| `WithIVFFlatIndexParams(params)` | IVFFlat index parameters (Lists) | `Lists=100` |

### Hybrid Search Configuration

| Option | Description | Default Value |
|--------|-------------|---------------|
| `WithEnableTSVector(enabled)` | Enable text search vector | `true` |
| `WithHybridSearchWeights(vector, text)` | Hybrid search weights (vector/text) | `0.7, 0.3` |
| `WithLanguageExtension(lang)` | Text tokenization language extension (e.g., zhparser/jieba) | `"english"` |

### Search Configuration

| Option | Description | Default Value |
|--------|-------------|---------------|
| `WithMaxResults(n)` | Default search result count | `10` |
| `WithDocBuilder(builder)` | Custom document builder | Default builder |
| `WithExtraOptions(opts...)` | Inject custom PostgreSQL ClientBuilder config, no need to care by default | - |

### Field Mapping (Advanced)

| Option | Description | Default Value |
|--------|-------------|---------------|
| `WithIDField(field)` | ID field name | `"id"` |
| `WithNameField(field)` | Name field name | `"name"` |
| `WithContentField(field)` | Content field name | `"content"` |
| `WithEmbeddingField(field)` | Embedding field name | `"embedding"` |
| `WithMetadataField(field)` | Metadata field name | `"metadata"` |
| `WithCreatedAtField(field)` | Created at field name | `"created_at"` |
| `WithUpdatedAtField(field)` | Updated at field name | `"updated_at"` |

## Full-Text Search

PGVector supports full-text search (TSVector), which can be used for Keyword Search and Hybrid Search.

> **⚠️ Important**: Keyword Search and Hybrid Search require enabling PostgreSQL full-text search functionality via `WithEnableTSVector(true)`.

```go
pgVS, err := vectorpgvector.New(
    vectorpgvector.WithPGVectorClientDSN(dsn),
    vectorpgvector.WithIndexDimension(1536),
    vectorpgvector.WithEnableTSVector(true),           // ✅ Enable full-text search
    vectorpgvector.WithHybridSearchWeights(0.7, 0.3),  // Set hybrid search weights (70% vector + 30% text)
    vectorpgvector.WithLanguageExtension("english"),   // Set tokenization language (Chinese requires zhparser/jieba)
)
```

### Search Mode Support

| Search Mode | Requires TSVector | Description |
|-------------|-------------------|-------------|
| **Vector Search** | ❌ | Uses vector index only |
| **Keyword Search** | ✅ Required | Depends on PostgreSQL `tsvector` full-text index |
| **Hybrid Search** | ✅ Required | Uses both vector index and `tsvector` index |
| **Filter Search** | ❌ | Metadata filtering only |

**If `WithEnableTSVector(true)` is not enabled**:

The system will automatically downgrade the search mode without error:
- When attempting Keyword/Hybrid search → Automatically downgrades to **Vector Search** (if vector available) or **Filter Search** (if no vector)
- INFO logs will be output indicating the downgrade reason

**Note**: The default call chain from SearchTool to VectorStore does not actively specify `SearchModeKeyword`, so Keyword Search is typically not triggered; instead, the default Vector or Hybrid search is used.

## Search Modes and Score Normalization

> **💡 Note**: This section covers score calculation details. Users typically don't need to worry about this. PGVector automatically handles score normalization for all search modes.

PGVector supports multiple search modes, all scores are normalized to the `[0, 1]` range, with higher scores indicating stronger relevance.

### 1. Vector Search

**SQL Template** (using subquery to avoid duplicate calculations):
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

**Normalization Formula**:
```
vector_score = 1.0 - (cosine_distance / 2.0)
```

**Mathematical Principle**:
- PGVector `<=>` operator returns **Cosine Distance**: `d ∈ [0, 2]`
  - `d = 0`: Vectors are identical
  - `d = 1`: Vectors are orthogonal
  - `d = 2`: Vectors are opposite
- Cosine Similarity: `s = 1 - d ∈ [-1, 1]`
- Normalized to `[0, 1]`: `score = (s + 1) / 2 = (2 - d) / 2 = 1 - d/2`

**Examples**:
- `distance = 0.2` → `score = 1 - 0.2/2 = 0.90` (highly similar)
- `distance = 1.0` → `score = 1 - 1.0/2 = 0.50` (orthogonal)
- `distance = 1.8` → `score = 1 - 1.8/2 = 0.10` (nearly opposite)

---

### 2. Keyword Search

**SQL Template** (using subquery to avoid duplicate calculations):
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

**Normalization Formula**:
```
text_score = rank / (rank + c)
```
Where `c = 0.1` (sparseNormConstant)

**Mathematical Principle**:
- PostgreSQL `ts_rank()` returns raw text relevance score with unbounded range (typically `[0, ∞)`)
- Uses hyperbolic normalization: `f(x) = x / (x + c)` to map to `[0, 1)`
- Parameter `c` controls sensitivity:
  - Smaller `c`: More sensitive to small rank values, higher discrimination
  - Larger `c`: More tolerant to large rank values, tends to saturate

**Examples** (c = 0.1):
- `rank = 1.0` → `score = 1.0 / 1.1 = 0.909`
- `rank = 0.5` → `score = 0.5 / 0.6 = 0.833`
- `rank = 0.1` → `score = 0.1 / 0.2 = 0.500`
- `rank = 0.01` → `score = 0.01 / 0.11 = 0.091`

**Computation Flow Example**:

Assume user query is `"machine learning"`, and the database has a document with content `"Machine learning enables intelligent systems..."`

```sql
-- Step 1: Convert query to tsquery
websearch_to_tsquery('english', 'machine learning')
→ 'machin' & 'learn'  -- (automatic stemming: machine → machin, learning → learn)

-- Step 2: Convert document content to tsvector
to_tsvector('english', 'Machine learning enables intelligent systems...')
→ 'enabl':3 'intellig':4 'learn':2 'machin':1 'system':5

-- Step 3: Check match (@@)
to_tsvector(...) @@ websearch_to_tsquery(...)
→ true  -- (contains both 'machin' and 'learn' stems)

-- Step 4: Calculate relevance score
ts_rank(to_tsvector(...), websearch_to_tsquery(...))
→ 2.5  -- (raw rank value)

-- Step 5: Normalize to [0, 1)
text_score = 2.5 / (2.5 + 0.1) = 0.961
```

**Core Functions**:
- `to_tsvector()`: Text → searchable vector (tokenization, stemming)
- `websearch_to_tsquery()`: User query → search expression (supports `"quotes"`, `OR`, `-exclusion`)
- `@@`: Match check (true/false)
- `ts_rank()`: Calculate relevance score


---

### 3. Hybrid Search

**SQL Template** (using subquery to avoid duplicate calculations):
```sql
SELECT *, (vector_score * 0.7 + text_score * 0.3) as score
FROM (
  SELECT *, 
         (1.0 - (embedding <=> $1) / 2.0) as vector_score,
         (COALESCE(ts_rank(...), 0) / (COALESCE(ts_rank(...), 0) + 0.1)) as text_score
  FROM table_name
  WHERE [metadata_filters]
  ORDER BY ((1.0 - (embedding <=> $1) / 2.0) * 0.7 + (ts_rank_expr) * 0.3) DESC
  LIMIT 10
) subq
```

**Normalization Formula**:
```
hybrid_score = vector_score × w_v + text_score × w_t
```
Where default weights: `w_v = 0.7`, `w_t = 0.3`

**Mathematical Principle**:
- Calculate `vector_score` and `text_score` separately (as in the two modes above)
- Use linear weighted combination, weights configurable via `WithHybridSearchWeights()`
- Since both scores are in `[0, 1]` range and `w_v + w_t = 1`, the final `hybrid_score ∈ [0, 1]`
- **Important**: Documents without text match are not forcibly filtered out, because vector similarity has higher weight (0.7), even without text match, high-quality results may still be returned

**Examples**:
```
Case 1: Both vector and text match well
  vector_score = 0.85, text_score = 0.90
  hybrid_score = 0.85 × 0.7 + 0.90 × 0.3 = 0.865

Case 2: High vector similarity but no text match
  vector_score = 0.95, text_score = 0.0
  hybrid_score = 0.95 × 0.7 + 0.0 × 0.3 = 0.665  -- Still returns high-quality result
```

---

### 4. Filter Search

**SQL Template**:
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

**Description**:
- Pure metadata filtering, no vector or text similarity involved
- All results have `score = 1.0` (as they all satisfy the filter conditions)
- Sorted by creation time in descending order

---
