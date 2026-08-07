# Tool Execution Safety Guard (Issue #2002)

本示例实现了面向 Tool、MCP Tool、Skill 和 CodeExecutor 命令调用的安全检查、Permission 拦截与可观测审计机制。

## 核心设计与架构说明

### 1. `tool.PermissionPolicy` 接口接入与框架治理
`SafetyPermissionPolicy` 原生实现了 Go 框架导出的 `tool.PermissionPolicy` 接口：
```go
func (p *SafetyPermissionPolicy) CheckToolPermission(
    ctx context.Context,
    req *tool.PermissionRequest,
) (tool.PermissionDecision, error)
```
可以直接作为 `agent.WithToolPermissionPolicy(policy)` 选项传入 Agent 运行选项中，在工具被真正执行前完成三态决策（`allow` / `deny` / `ask`）拦截与脱敏审计日志记录。

### 2. 风险类型覆盖
- **危险删除/覆盖**：`rm -rf` 等破坏性命令拦截。
- **Shell 参数展开逃逸**：拦截 `${IFS}` 等混淆绕过手段。
- **敏感路径/凭据保护**：拦截对 `/.ssh/`、`/.env`、`id_rsa` 等密钥文件的读取。
- **网络外连控制**：基于 `allowed_domains` 域名白名单拦截非法 egress 数据外派。
- **Shell 绕过拦截**：对 `sh -c`、`eval`、反引号等动态子壳包装予以拒绝。
- **宿主机高危提升**：对 `hostexec` 模式下的 `sudo` / `chmod` 等权限变更转入 `ask` 人工复核。

### 3. 为什么安全扫描不能替代沙箱隔离？
静态安全扫描（Static Command Scanning）可以在命令真正执行前做快速防呆与高危过滤，但它无法防护未知的逻辑逃逸与二进制漏洞。**必须结合容器/E2B 沙箱（CodeExecutor Sandbox）** 提供操作系统层面的资源隔离与网络命名空间隔离。

## 运行方式

```bash
# 运行单测
go test -v -count=1 ./...

# 格式校验与代码诊查
gofmt -l .
go vet ./...

# 运行主流程示例
go run . -config tool_safety_policy.yaml -output output
```
