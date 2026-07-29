# Security Rules

## SEC-001: SQL Injection via fmt.Sprintf

- **type**: token
- **severity**: critical
- **pattern**: `fmt\.Sprintf\s*\(\s*["'][^"]*?(SELECT|INSERT|UPDATE|DELETE|DROP)\s+[^"]*?["'].*?\)`
- **message**: "SQL 语句通过 fmt.Sprintf 拼接，存在注入风险。应使用参数化查询 (database/sql 的 ? 占位符)"
- **fix**: "使用参数化查询。例如: db.Query(\"SELECT * FROM users WHERE name = ?\", name)"

## SEC-002: Command Injection via os/exec

- **type**: token
- **severity**: critical
- **pattern**: `exec\.Command\s*\(\s*["'][^"']*\$\{[^}]*\}[^"']*["']`
- **message**: "命令字符串包含 shell 变量注入，可能被注入恶意命令"
- **fix**: "避免直接拼接变量到命令字符串。使用 exec.Command(cmd, arg1, arg2) 参数形式"

## SEC-003: Hardcoded Path Traversal Risk

- **type**: token
- **severity**: high
- **pattern**: `(os\.Open|ioutil\.ReadFile|os\.ReadFile)\s*\(\s*["'][^"']*\.\.\/[^"']*["']`
- **message**: "文件路径包含 ../ 路径穿越，应验证和清理输入"
- **fix**: "使用 filepath.Clean() 清理路径，或用 filepath.SecureJoin() 验证"
