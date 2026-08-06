# Tool 执行安全

`tool/safety` 提供可选的执行前参数检查。应用通过 `tool.PermissionPolicy`
接入，得到 `allow` / `deny` / `ask` 决策，并产出 JSONL 审计事件；在已有
recording span 时可写入 `tool.safety.*` 属性。

该机制不能替代 workspace 隔离、宿主机进程控制、CodeExecutor 沙箱或运行时资源限制。

## 范围

在框架真正执行工具之前，检查已定稿的工具 JSON 参数，包括：

- 命令类字段：`command`、`args` / `argv`、`stdin`、`code_blocks`
- 路径类字段：`path`、`file`、`file_path` 等
- 位置类字段：`uri`、`url`、`href`、`location`、`file_uri`
- 环境变量覆盖与疑似密钥字段

默认策略覆盖破坏性命令与敏感路径、非白名单外连、向解释器投喂的管道与
shell 包装、依赖变更提示、粗粒度资源滥用信号、hostexec 会话提示，以及参数中的凭据材料。

扫描是静态启发式的。混淆、多步工具链、依赖远程数据才确定的行为可能绕过检查。
扫描通过仅表示当前规则未命中。

## 接入

```go
guard := safety.NewGuard(
    safety.WithPolicyFile("tool_safety_policy.yaml"),
    safety.WithAuditor(auditor),
)
defer guard.Close()

runner.Run(ctx, userID, sessionID, msg,
    agent.WithToolPermissionPolicy(guard),
)
```

`Guard` 直接实现 `tool.PermissionPolicy`。需要串联多条策略时使用
`safety.Compose`，以第一个非 allow 决策为准。

`Policy.CommandLists()` 返回可供 `workspaceexec` 使用的 allow/deny 列表，
便于与 PermissionPolicy 保持一致。详见 `tool/safety/DUAL_POLICY.md`。

PermissionPolicy 看不到工具输出。若需落盘结果，请使用 `AfterToolRedact` 或
`RedactText` / `RedactMap`。

## 策略

`LoadPolicyFile` / `LoadPolicy` 以严格模式解析 YAML/JSON，拒绝未知字段。
省略 deny 列表时保留 `DefaultPolicy` 的拒绝项（fail-closed）；显式空列表会清空默认拒绝项。

可配置项包括允许/拒绝命令、禁止路径、允许主机、环境变量名白名单、
ask 命令，以及扫描期大小/超时提示。修改策略文件无需改代码。

## 与其他机制的关系

| 机制 | 作用 |
|---|---|
| `tool/safety` | 执行前参数扫描与权限决策 |
| `agent.WithToolFilter` | 控制模型可见的工具集合 |
| `workspaceexec` / `hostexec` / CodeExecutor | 进程隔离、环境清理、超时 |
| OpenTelemetry / 审计 JSONL | 决策后的可观测性 |

对不信任工作区仍应使用沙箱或等价隔离。本组件是策略边界，不是执行监狱。

## 示例与测试

- 示例：`examples/tool_safety_guard`
- 验收语料：`tool/safety/testdata/acceptance_corpus.json`
- 执行链路集成：`internal/flow/processor/safety_guard_integration_test.go`

包级细节与残差限制见
[`tool/safety/README.md`](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/safety/README.md)。
