# ClickHouse Vector Store

基于 ClickHouse 的向量存储实现，实现 `knowledge/vectorstore.VectorStore` 接口，支持向量搜索、过滤搜索、关键词搜索和混合搜索。

## 架构

```text
knowledge/vectorstore/clickhouse   (VectorStore 实现)
        │
        ▼
storage/clickhouse                 (ClickHouse 客户端抽象)
        │
        ▼
ClickHouse (ReplacingMergeTree 表)
```

- **`storage/clickhouse`**：提供 `Client` 接口，支持连接池（原生 `clickhouse-go/v2` 驱动）与命名实例注册（`RegisterClickHouseInstance` / `GetClickHouseInstance`）。
- **`knowledge/vectorstore/clickhouse`**：实现 `vectorstore.VectorStore` 的全部方法，将文档映射为 ClickHouse 表的一行。

## 表结构

每张向量表包含以下固定列，以及由 `WithFilterFields` 声明的动态过滤列：

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | `String` | 文档 ID（排序键） |
| `name` | `String` | 文档名 |
| `content` | `String` | 文档内容 |
| `embedding` | `Array(Float64)` | 向量 |
| `metadata` | `String` | JSON 编码的元数据 |
| `created_at` | `DateTime64(6)` | 创建时间 |
| `updated_at` | `DateTime64(6)` | 更新时间（版本列） |

表引擎为 `ReplacingMergeTree(updated_at) ORDER BY id`，重新插入相同 ID 会以更高的 `updated_at` 覆盖旧行。

## 快速开始

### 通过 DSN 连接

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/clickhouse"
)

vs, err := clickhouse.New(
    clickhouse.WithDSN("clickhouse://user:password@localhost:9000/default"),
    clickhouse.WithTableName("documents"),
    clickhouse.WithVectorDimension(1536),
    clickhouse.WithMetric(clickhouse.MetricCosine),
)
if err != nil {
    // handle error
}
defer vs.Close()
```

### 通过命名实例连接

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/clickhouse"
    storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

// 在应用启动时注册实例。
storage.RegisterClickHouseInstance(
    "my-clickhouse",
    storage.WithClientBuilderDSN("clickhouse://user:password@localhost:9000/default"),
)

vs, err := clickhouse.New(
    clickhouse.WithInstanceName("my-clickhouse"),
    clickhouse.WithTableName("documents"),
    clickhouse.WithVectorDimension(1536),
)
```

## 配置选项

| 选项 | 说明 |
|---|---|
| `WithTableName` | 表名（必填） |
| `WithVectorDimension` | 向量维度，必须与 embedding 输出一致 |
| `WithMetric` | 距离度量：`MetricCosine` / `MetricL2` / `MetricInnerProduct` |
| `WithFilterFields` | 声明可过滤字段，会物化为专用类型列 |
| `WithAutoCreateTable` | 是否自动建表（默认 true） |
| `WithAllowDestructiveDeleteAll` | 是否允许 DeleteAll（默认 false） |
| `WithInstanceName` / `WithDSN` | 连接方式（DSN 优先） |
| `WithExtraOptions` | 传递给自定义 ClientBuilder 的额外选项 |
| `WithMaxResults` | 默认搜索返回条数 |
| `WithIDFieldName` 等 | 覆盖内置列名 |

## 搜索模式

| 模式 | 说明 |
|---|---|
| `SearchModeVector` | 纯向量搜索，按距离排序 |
| `SearchModeFilter` | 仅过滤，不做向量计算 |
| `SearchModeKeyword` | 基于内容子串匹配的关键词搜索 |
| `SearchModeHybrid` | 关键词预过滤 + 向量排序 |

距离度量与评分：

- **Cosine**：`cosineDistance`，距离 ∈ [0, 2]，评分为 `1 - distance`。
- **L2**：`L2Distance`，距离 ∈ [0, +∞)，评分为 `1/(1+distance)`。
- **InnerProduct**：`dotProduct`，评分即内积（无界）。

## 过滤

通过 `WithFilterFields` 声明的字段可用于过滤。过滤条件通过 `searchfilter` 包构造：

```go
query := &vectorstore.SearchQuery{
    SearchMode: vectorstore.SearchModeVector,
    Vector:     []float64{...},
    Filter: &vectorstore.SearchFilter{
        FilterCondition: searchfilter.And(
            searchfilter.Equal("category", "news"),
            searchfilter.GreaterThan("year", 2020),
        ),
    },
}
```

支持的运算符：`eq` / `ne` / `gt` / `gte` / `lt` / `lte` / `in` / `not in` / `like` / `not like` / `between` / `and` / `or`。

## 端到端验证

完整的可运行示例（Add/Get/Search/Update/Delete/Count，不需要 LLM 或 embedding 服务）见 [examples/knowledge/vectorstores/clickhouse](../../../examples/knowledge/vectorstores/clickhouse)。

仅验证单元测试（无需 ClickHouse）：

```bash
go test ./...
```

## 注意事项

- `Delete` / `DeleteByFilter` / `deleteAll` 使用 ClickHouse 的 `ALTER TABLE ... DELETE` 变更（异步生效）。
- `Update` / `UpdateByFilter` 采用"读取现有行 → 重新插入新版本"的方式，借助 `ReplacingMergeTree` 折叠旧行。
- ClickHouse 原生支持 `cosineDistance` / `L2Distance` / `dotProduct` 向量函数（需 ClickHouse 22.8+）。
