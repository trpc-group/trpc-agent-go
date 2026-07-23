# PromptIter Evaluation Regression Loop

本示例实现 [Issue #2003](https://github.com/trpc-group/trpc-agent-go/issues/2003) 要求的 Evaluation + PromptIter 回归闭环：

```text
Train/Validation Baseline
→ 证据快照与失败归因
→ 真实 PromptIter backward/aggregate/optimize
→ Candidate Train/Validation 回归
→ 逐 Case/Metric Delta
→ 独立 Release Gate
→ JSON/Markdown 审计报告
```

示例完全使用确定性 Fake Model，不读取网络或真实 API Key。Fake 仅替换 `model.Model`，Evaluation Service、runner、llmagent、PromptIter Engine、backwarder、aggregator、optimizer 和 Gate 均执行真实代码路径。

## 运行

```bash
cd examples/evaluation/promptiter_regression_loop
env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u GOOGLE_API_KEY \
  go run . -config ./data/promptiter.json
```

默认输出位于 `example_output/`：

- `optimization_report.json`
- `optimization_report.md`
- `candidate_prompt.txt`
- `round-001-candidate_prompt.txt`（每轮均单独留存）
- `accepted_prompt.txt`（仅 Gate 接受时存在）

## 输入

`data/promptiter.json` 定义随机种子、优化轮次、输入路径、数据集污染检查、接受门禁和资源预算。默认输入包括：

- `data/promptiter-regression-loop-app/city-service-train.evalset.json`；
- `data/promptiter-regression-loop-app/city-service-validation.evalset.json`；
- `data/promptiter-regression-loop-app/city-service.metrics.json`；
- `prompts/baseline.txt`。

所有相对路径均相对于配置文件所在目录解析，可以复制默认配置后替换为自己的 Prompt 和评测集。

## 样例数据

公开 fixture 包含 3 条 Train 和 3 条 Validation Case，每条 Case 同时记录最终回复和工具轨迹指标。默认 Baseline 按 6 个 Case/Metric 单元聚合，Train 得分为 `0.500`，Validation 为 `0.667`。

配置中的 `scenario` 支持：

| 场景 | Candidate Train | Candidate Validation | 预期 Gate |
|---|---:|---:|---|
| `improvement` | 3/3 | 3/3 | 接受 |
| `noop` | 0.500 | 0.667 | 因无提升拒绝 |
| `overfit` | 1.000 | 0.167 | 因 Train 升、Validation 降拒绝 |
| `multi_round` | 首轮无效、次轮泛化 | 首轮无效、次轮泛化 | 拒绝候选不成为下一轮 parent |

各类候选都由 deterministic worker 响应真实 PromptIter 请求生成；Pipeline 不直接注入候选分数或 Gate 结果。

## 接受门禁

独立 Gate 检查：

- Dataset/Metrics/Profile provenance；
- seed、evaluationRunID、evalsetID 及四组运行身份唯一性；
- Case/Metric 矩阵完整性；
- 快照总分、逐项 Delta 与输入结果一致性；
- Validation 最小提升；
- 新增 hard fail；
- critical case 和单指标退化；
- Train/Validation 过拟合及泛化差距；
- 模型调用、Token、延迟和可选金额预算；
- 精确/近重复数据污染检测、NaN/Inf 和未评测项 fail-closed。

PromptIter 内部 acceptance 会进入报告，但不会替代独立 Gate。只有 Gate 通过后才原子写入 `accepted_prompt.txt`；拒绝运行会删除同一输出目录中的旧 accepted prompt。

## 测试

```bash
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
```

测试覆盖 41 条 Gate 真值（41/41）、128 次固定种子安全性质变异、38 条归因真值（38/38）、严格 Delta、快照/Delta 防伪、语义确定性、报告 Schema 实校验、报告脱敏、数据污染，以及 improvement/noop/overfit/multi_round 四条无 Key E2E。

## 实现与安全约束

- 公开 fixture 使用确定性 final-response exact metric 与顺序/参数/结果敏感的 tool trajectory metric；candidate Fake Model 会真实返回 ToolCall，经工具结果后再生成最终回复。
- 金额成本在没有价格表时保存为 `null`/`not_configured`，不会伪造为 0；配置金额 Gate 而成本未知时 fail-closed。
- 示例以可运行参考实现的形式位于 `examples/evaluation/`，不新增公共包 API。
- Pipeline 只发布 Prompt 产物，不自动改写源码、提交 Git 或创建 PR。
- 报告默认保存 Trace 的步骤身份与输入/输出 Hash，不落盘原始敏感正文。
