# Go 代码评审规则

## GO-SEC-001 敏感信息

检测硬编码 API key、access token、bearer token、password、AWS access key 和 GitHub token。证据写入存储前必须脱敏。严重级别为 critical。

## GO-SEC-002 SQL 构造

检测在 `Query`、`QueryContext`、`Exec` 或 `ExecContext` 附近使用 `fmt.Sprintf` 或字符串拼接构造 SQL 的代码。建议改用参数化 SQL。严重级别为 high。

## GO-CONC-001 Goroutine 取消路径

新增 goroutine 必须有可见取消路径。对 request-scoped 代码，应传入 `context.Context` 并监听 `ctx.Done()`。严重级别为 medium。

## GO-CONC-002 time.Tick 泄漏

`time.Tick` 无法停止。应使用 `time.NewTicker`、`defer ticker.Stop()` 和 context cancellation。严重级别为 high。

## GO-CONC-003 请求上下文断链

request-scoped handler 不应为下游调用创建 `context.Background()` 或 `context.TODO()`。应传递请求上下文。严重级别为 medium。

## GO-RES-001 资源生命周期

文件、HTTP response body、数据库句柄等资源必须在所有路径上关闭。推荐在 nil-error 检查后立即 `defer Close()`。严重级别为 high。

## GO-DB-001 Rows 生命周期

`Query` 或 `QueryContext` 返回的 rows 必须关闭，并在迭代后检查 `rows.Err()`。严重级别为 high。

## GO-DB-002 Transaction 生命周期

通过 `Begin` 或 `BeginTx` 创建的 transaction 必须有 `Rollback` 和 `Commit` 路径。严重级别为 high。

## GO-ERR-001 错误处理

I/O、DB、JSON、类型转换和网络调用返回的 error 不应赋值给 `_`，除非有明确且局部的理由。严重级别为 medium。

## GO-TEST-001 测试覆盖

生产 Go 行为发生变更且新增函数时，如果 diff 中没有对应 `_test.go` 变更，则进入 human review，作为低置信提醒而不是阻塞 finding。
