# Database Lifecycle Rules

## DB-001: sql.Open Without Deferred Close

- **type**: token
- **severity**: high
- **pattern**: `=\s*sql\.Open\(`
- **message**: "sql.Open 返回的 *sql.DB 应在程序退出前 Close()。当前未发现对应的 defer db.Close()"
- **fix**: "在 main() 或初始化函数中 defer db.Close()"

## DB-002: Query Without Deferred Rows Close

- **type**: token
- **severity**: medium
- **pattern**: `\.Query\(|\.QueryContext\(|\.QueryRow\(`
- **message**: "数据库查询返回的 *sql.Rows 需要 defer rows.Close()。确认当前作用域有对应的 defer"
- **fix**: "在 err 检查后立即 defer rows.Close()"

## DB-003: Transaction Begin Without Deferred Rollback

- **type**: token
- **severity**: high
- **pattern**: `\.Begin\(\)|\.BeginTx\(`
- **message**: "开启了数据库事务，确认有 defer tx.Rollback() 和显式 tx.Commit()"
- **fix**: "使用模式: tx, err := db.Begin(); if err != nil { return err }; defer tx.Rollback(); ...; return tx.Commit()"
