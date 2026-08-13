# 常见问题

## Memory 与 Session 的区别

这是最常见的疑问。Memory 和 Session 解决不同的问题：

| 维度         | Memory（记忆）       | Session（会话）                |
| ------------ | -------------------- | ------------------------------ |
| **定位**     | 长期用户档案         | 临时对话上下文                 |
| **隔离维度** | `<appName, userID>`  | `<appName, userID, sessionID>` |
| **生命周期** | 跨会话持久化         | 单次会话内有效                 |
| **存储内容** | 用户画像、偏好、事实 | 对话历史、消息记录             |
| **数据量**   | 小（几十到几百条）   | 大（几十到几千条消息）         |
| **使用场景** | “记住用户是谁”       | “记住说了什么”                 |

**示例**：

```go
// Memory：跨会话保留
memory.AddMemory(ctx, userKey, "用户是后端工程师", []string{"职业"})

// Session：单次会话有效
session.AddMessage(ctx, sessionKey, userMessage("今天天气怎么样？"))
session.AddMessage(ctx, sessionKey, agentMessage("今天晴天"))

// 新会话：Memory 保留，Session 重置
```

## Memory ID 的幂等性

Memory ID 基于「内容 + appName + userID + 规范化事件元数据」的 SHA256 哈希生成；主题不参与 ID，因此同一用户下相同内容即使 topics 改变也会产生相同 ID：

```go
// 第一次添加
memory.AddMemory(ctx, userKey, "用户喜欢编程", []string{"爱好"})
// 生成 ID：abc123...

// 第二次添加相同内容，但使用不同 topics
memory.AddMemory(ctx, userKey, "用户喜欢编程", []string{"兴趣"})
// 生成相同 ID：abc123...，覆盖更新，刷新 topics 和 updated_at
```

**影响**：

- ✅ **天然去重**：避免冗余存储
- ✅ **幂等操作**：重复添加不会创建多条记录
- ⚠️ **覆盖更新**：无法追加相同内容（如需追加，可在内容中加时间戳或序号）

## 搜索行为说明

搜索行为取决于后端：

- 对 `inmemory` / `redis` / `mysql` / `postgres`：`SearchMemories` 使用 **BM25 风格 lexical 关键词匹配**（不是语义搜索）。
- 对 `pgvector` / `mysqlvec` / `sqlitevec`：`SearchMemories` 使用**向量相似度检索**，并且需要配置 Embedder。
- 对 `chromadb`：`SearchMemories` 使用 ChromaDB 向量检索，并支持 kind 回退和混合检索。

**Lexical 匹配细节**（非向量后端）：

**英文分词**：转小写 → 过滤停用词（a、the、is 等）→ 空格分割

```go
// 可以找到
记忆："User likes programming"
搜索："programming" ✅ 匹配

// 找不到
记忆："User likes programming"
搜索："coding" ❌ 不匹配（语义相近但词不同）
```

**中文分词**：优先使用 `gse` 词级分词，并补充低权重 CJK
字符 trigram 召回

```go
记忆："用户喜欢编程"
搜索："编程" ✅ 匹配（词级命中）
搜索："写代码" ❌ 不匹配（词不同）
```

**限制**（非向量后端）：

- 这些后端均在**应用层**过滤和排序（\[O(n)\] 复杂度）
- 数据量大时性能受影响
- 不支持语义相似度搜索
- 排序是 **BM25 风格关键词打分 + query coverage + 有序短语加分**，
  仍然属于 lexical search，不是向量语义检索

**建议**：

- 使用明确关键词和主题标签提高命中率
- 如需语义相似度检索，使用 pgvector、mysqlvec、sqlitevec 或 ChromaDB 后端

## 软删除的注意事项

**支持情况**：

- ✅ MySQL、MySQLVec、PostgreSQL、pgvector、SQLite、SQLiteVec、ChromaDB：支持软删除
- ❌ InMemory、Redis：不支持（只有硬删除）

**软删除配置**：

```go
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN("..."),
    memorymysql.WithSoftDelete(true), // 启用软删除
)
```

**行为差异**：

| 操作 | 硬删除   | 软删除                               |
| ---- | -------- | ------------------------------------ |
| 删除 | 立即移除 | 设置 `deleted_at` 字段               |
| 查询 | 不可见   | 自动过滤（WHERE deleted_at IS NULL） |
| 恢复 | 无法恢复 | 重新 Add，或将更新轮转到相同 ID      |
| 存储 | 节省空间 | 占用空间                             |

**迁移陷阱**：

```go
// ⚠️ 从支持软删除的后端迁移到不支持的后端
// 软删除的记录会丢失！

// 从 MySQL（软删除）迁移到 Redis（硬删除）
// 需要手动处理软删除记录
```
