# Go 代码审查清单

快速参考清单，用于代码审查。

## 预审查（2 分钟）

- [ ] 阅读 PR 描述和关联的 issue
- [ ] 检查 PR 大小（<400 行理想）
- [ ] 验证 CI/CD 状态（测试通过？）
- [ ] 理解业务需求

## 架构与设计（5 分钟）

- [ ] 解决方案适合问题
- [ ] 与现有模式一致
- [ ] 没有更简单的方案
- [ ] 是否可扩展？
- [ ] 变更在正确的位置

## 逻辑与正确性（10 分钟）

- [ ] 边界情况已处理
- [ ] nil 检查完整
- [ ] 越界错误已检查
- [ ] 竞态条件已考虑
- [ ] 错误处理完整
- [ ] 使用正确的数据类型

## 安全（5 分钟）

- [ ] 没有硬编码的密钥
- [ ] 输入已验证/清理
- [ ] SQL 注入已防护
- [ ] XSS 已防护
- [ ] 授权检查存在
- [ ] 敏感数据已保护

## 性能（3 分钟）

- [ ] 没有 N+1 查询
- [ ] 昂贵的操作已优化
- [ ] 大列表已分页
- [ ] 没有内存泄漏
- [ ] 适当的地方已考虑缓存

## 测试（5 分钟）

- [ ] 新代码有测试
- [ ] 边界情况已测试
- [ ] 错误情况已测试
- [ ] 测试可读
- [ ] 测试是确定性的

## 代码质量（3 分钟）

- [ ] 清晰的变量/函数名
- [ ] 没有代码重复
- [ ] 函数只做一件事
- [ ] 复杂代码有注释
- [ ] 没有魔法数字

## 文档（2 分钟）

- [ ] 公共 API 已文档化
- [ ] README 已更新（如需要）
- [ ] 破坏性变更已说明
- [ ] 复杂逻辑已解释

---

## Go 特定检查

### 错误处理
- [ ] 错误不被忽略（`_` 丢弃）
- [ ] 错误有上下文（`fmt.Errorf("...: %w", err)`）
- [ ] 使用 `errors.Is` 和 `errors.As`（不是直接比较）
- [ ] 哨兵错误已定义

### 并发
- [ ] goroutine 有退出机制
- [ ] channel 有关闭逻辑
- [ ] 循环变量正确捕获（Go < 1.22）
- [ ] 使用 `sync.WaitGroup` 等待 goroutine

### Context
- [ ] context 作为第一个参数
- [ ] 传播而非创建新的根 context
- [ ] 始终调用 cancel 函数
- [ ] 响应 context 取消

### 资源管理
- [ ] 文件使用 `defer f.Close()`
- [ ] 数据库连接使用 `defer rows.Close()`
- [ ] HTTP 响应体使用 `defer resp.Body.Close()`
- [ ] 没有资源泄漏

### 数据库
- [ ] 使用参数化查询（不是字符串拼接）
- [ ] 事务有 commit/rollback
- [ ] 连接池已配置
- [ ] 超时已设置

---

## 严重级别

| 标签 | 含义 | 行动 |
|------|------|------|
| 🔴 `[blocking]` | 必须修复 | 阻止合并 |
| 🟡 `[important]` | 应该修复 | 讨论是否同意 |
| 🟢 `[nit]` | 锦上添花 | 非阻塞 |
| 💡 `[suggestion]` | 替代方案 | 考虑采用 |
| 📚 `[learning]` | 教育性评论 | 无需行动 |
| 🎉 `[praise]` | 好的工作 | 表扬！ |

---

## 反馈示例

### 好的反馈

```markdown
🔴 [blocking] SQL 注入风险

这里使用 `fmt.Sprintf` 拼接 SQL 查询，存在 SQL 注入风险。

```go
// 当前代码
query := fmt.Sprintf("SELECT * FROM users WHERE name = '%s'", username)
```

**建议修复：**
```go
// 使用参数化查询
rows, err := db.Query("SELECT * FROM users WHERE name = ?", username)
```

**为什么重要：** 攻击者可以通过构造恶意 username 执行任意 SQL。
```

```markdown
🟡 [important] 资源泄漏

文件打开后没有关闭，可能导致资源泄漏。

```go
f, err := os.Open("file.txt")
if err != nil {
    return err
}
// 缺少 defer f.Close()
```

**建议修复：**
```go
f, err := os.Open("file.txt")
if err != nil {
    return err
}
defer f.Close()
```
```

```markdown
🟢 [nit] 命名改进

变量名 `uc` 不够清晰，建议改为 `userCount`。

**非阻塞** - 如果你更喜欢保持原样也可以。
```

```markdown
🎉 [praise] 好的错误处理

这里使用 `fmt.Errorf` 包装错误并保留错误链，非常好！

```go
if err != nil {
    return fmt.Errorf("failed to process user %d: %w", userID, err)
}
```
```
