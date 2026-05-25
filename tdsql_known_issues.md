# TDSQL Session 已知潜在问题

> 本文档记录 TDSQL session 实现中已识别但暂未修复的潜在问题，供后续迭代参考。

## 1. upsertAppState / upsertUserState 并发安全性

**影响范围：** `app_states`、`user_states` 表的写入

**问题描述：**

`upsertAppState` 和 `upsertUserState` 使用 SELECT-then-INSERT 两步模式：

```go
// 1. 查询是否存在活跃记录
SELECT id FROM app_states WHERE app_name = ? AND `key` = ? AND deleted_at IS NULL

// 2a. 不存在则插入
INSERT INTO app_states (app_name, `key`, value, ...) VALUES (?, ?, ?, ...)

// 2b. 存在则更新
UPDATE app_states SET value = ? WHERE id = ?
```

UNIQUE KEY 定义为 `(app_name, key, deleted_at)` / `(app_name, user_id, key, deleted_at)`，包含 `deleted_at` 列。
由于 MySQL 中 `NULL != NULL`，当 `deleted_at` 为 NULL 时，唯一索引**无法阻止重复插入**。

两个并发请求可能同时 SELECT 到 `ErrNoRows`，然后都 INSERT 成功，产生重复的活跃记录。

**影响程度：** 低。`app_state` 和 `user_state` 的写入频率通常很低，实际触发的概率极小。

**可选修复方案：**

- **方案 A：** 使用 `INSERT ... ON DUPLICATE KEY UPDATE`，去掉 `deleted_at` 列（参考 `session_summaries` 的做法）
- **方案 B：** 使用 `SELECT ... FOR UPDATE` 加事务锁
- **方案 C：** 维持现状，记录为已知限制

---

## 2. session_summaries 的 VARCHAR(128) 长度限制

**影响范围：** TDSQL 模式下的 `session_summaries` 表

**问题描述：**

为满足 TDSQL 的 UNIQUE KEY 约束（shardkey 不能使用前缀索引）和 InnoDB 3072 字节索引长度限制，
TDSQL 模式下 `session_summaries` 的 `app_name`、`session_id`、`filter_key` 列被限制为 `VARCHAR(128)`，
而其他 5 张表仍为 `VARCHAR(255)`。

如果用户的 `app_name` 或 `session_id` 超过 128 字符，在 TDSQL 模式下插入 `session_summaries` 时会报错。

**影响程度：** 低。实际使用中 128 字符通常足够。

**建议：** 在用户文档中明确标注此限制。

---

## 3. getEventsList / getSummariesList 的 user_id 假设

**影响范围：** `service_helper.go` 中的批量查询函数

**问题描述：**

`getEventsList` 和 `getSummariesList` 在构造 TDSQL 路由 hint 时，使用 `sessionKeys[0].UserID` 作为 shardkey：

```go
query := fmt.Sprintf(`SELECT ... FROM %s
    WHERE (app_name, user_id, session_id) IN (%s)
    AND user_id = ?
    AND deleted_at IS NULL`, ...)
args = append(args, sessionKeys[0].UserID)
```

这隐含假设：**所有传入的 sessionKeys 属于同一个 user_id**。

当前调用路径下是安全的：
- `getSession` — 单个 key
- `listSessions` — 同一 `UserKey` 下的所有 session

但如果将来新增跨用户的批量查询场景，此处会导致非 `keys[0]` 用户的数据被过滤掉。

**影响程度：** 当前无功能问题，属于脆弱设计。

**建议：** 在函数注释中明确标注"all sessionKeys must belong to the same user_id"的前置条件。
