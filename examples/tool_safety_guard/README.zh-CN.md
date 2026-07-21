# 工具安全护栏（仅扫描示例）

此示例只评估工具请求，不执行任何命令或代码。它展示严格策略加载、结构化风险发现、仅含元数据的 JSONL 审计，以及 `allow` / `ask` / `deny` 三种决策。

## 运行

在仓库的 `examples` 目录执行：

```bash
go run ./tool_safety_guard -policy ./tool_safety_guard/tool_safety_policy.yaml -output-dir ./tool_safety_guard/output
```

程序扫描 16 个公开样本并生成：

- `output/tool_safety_report.json`：每个样本一份结构化报告；
- `output/tool_safety_audit.jsonl`：每次扫描一行、只含元数据的审计事件。

所有样本都不会被执行。如果任一样本的实际决策与预期不一致，程序会以非零状态退出。
仓库中的 [`sample/`](sample/) 保存了一份可直接审查的参考输出。

自动测试：

```bash
go test ./tool_safety_guard
```

## 决策语义

- `allow`：没有启用的规则要求干预；
- `ask`：必须暂停执行并取得明确的人工批准；
- `deny`：禁止执行。

同一请求命中多条规则时，按 `deny > ask > allow` 选择最强决策；审计写入失败也会关闭执行通道。

示例策略只允许精确域名和显式 `*.suffix` 通配符，通配符不匹配根域。YAML/JSON 策略会拒绝未知字段、重复键、尾随文档或值、不支持的版本和非法动作。

框架与直接工具接入、威胁模型、遥测和运维说明见[工具安全护栏设计](../../docs/tool-safety-guard.zh-CN.md)。
