# ClickHouse Vector Store Example

端到端验证 ClickHouse 向量存储，**不需要 LLM / embedding 服务**，只需一个 ClickHouse 实例即可跑通 Add/Get/Search（向量/过滤/关键词/混合）/Update/Delete/Count 全部操作。

## 前置条件

启动 ClickHouse：

```bash
docker run -d --name clickhouse \
  -p 9000:9000 \
  clickhouse/clickhouse-server:latest
```

## 运行

```bash
cd examples/knowledge/vectorstores/clickhouse
go run main.go
```

预期输出会逐步打印建表、添加 3 个文档、向量搜索（`[1,0,0]` 应最接近 `doc1`）、过滤搜索、关键词搜索、混合搜索、更新、计数、删除的结果，最后打印 `✅ ClickHouse vector store verification passed.`。

## 连接说明

- 使用 ClickHouse **native 协议（9000 端口）**，不是 HTTP（8123）。
- DSN 格式：`clickhouse://user:password@host:port/database`。
- Docker 默认用户为 `default`，无密码，所以 `clickhouse://default:@localhost:9000/default` 可直接连接。

## 验证的接口

| 操作 | 说明 |
|------|------|
| `New` + 自动建表 | `ReplacingMergeTree` 表，含向量列与过滤字段列 |
| `Add` | 写入文档 + 向量 + 元数据 |
| `Get` | 按 ID 查询文档与向量 |
| `Search`（Vector） | 余弦距离排序的向量搜索 |
| `Search`（Filter） | 元数据过滤 + searchfilter 条件过滤 |
| `Search`（Keyword） | 内容子串关键词搜索 |
| `Search`（Hybrid） | 关键词预过滤 + 向量排序 |
| `Update` | 保留 created_at，更新内容 |
| `Count` | 计数 |
| `Delete` | 删除文档 |
