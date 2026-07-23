# 回放一致性设计

框架按 JSONL 执行 Event、State、Memory、Summary、Track 动作，经公开 API 读回快照。规范化只处理 UTC、JSON、集合顺序和 nil/empty，保留事件与 Track 顺序。Summary 比较 filter-key、正文、Session 归属、版本、更新时间和 Boundary；Track 比较名称、时间及 payload。交错 fixture 模拟工具与子 Agent 并发流。差异须用 `allowed_diff` 声明路径、后端和原因。默认比较 InMemory、SQLite、miniredis，可接入真实 Redis。
