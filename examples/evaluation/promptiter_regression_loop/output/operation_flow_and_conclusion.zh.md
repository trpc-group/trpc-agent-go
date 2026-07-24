# PromptIter 回归闭环操作流程与结论

## 运行默认值

- GOPATH/GOCACHE：使用 Go 默认值；如需自定义，请在仓库外按本机环境设置。
- 默认 LLM Base URL：`https://api.deepseek.com`
- 默认 LLM Model：`deepseek-chat`
- API Key 来源：仅从环境变量读取，支持 `LLM_API_KEY`、`DEEPSEEK_API_KEY`、`DEEPSEEK_API_KEY1`、`OPENAI_API_KEY`

API Key 不写入仓库文件。

## Mock / Deterministic 流程

命令：

```powershell
go run ./promptiter_regression_loop `
  -config ./promptiter_regression_loop/data/promptiter.json
```

结果文件：

- `optimization_report.json`
- `optimization_report.md`
- `deterministic_optimization_report.json`
- `deterministic_optimization_report.md`

结论数据：

| 字段 | 值 |
|---|---:|
| 决策 | REJECT |
| Baseline 训练集分数 | 0.4444 |
| Candidate 训练集分数 | 0.8889 |
| Baseline 验证集分数 | 0.7778 |
| Candidate 验证集分数 | 0.8889 |
| 验证集分数增量 | 0.1111 |
| 新增失败验证 case | 1 |
| 关键 case 退化 | 1 |
| 总调用次数 | 12 |
| 预估成本 | 0.000114 |

Gate 原因：

- 新增 hard fail `1` 超过上限 `0`。
- `1` 个关键验证 case 发生退化。

解释：

deterministic 链路是默认运行路径，不需要 API Key。它复现了预期的回归检测闭环：候选 prompt 提升了训练集分数和验证集总分，修复了 `val_json_refund`，但把 `val_critical_direct_status` 的直接回答错误包装成 JSON，导致关键 case 退化。因此即使分数提升，外层 gate 仍会正确拒绝。

## Real LLM 流程

命令：

```powershell
$env:DEEPSEEK_API_KEY="<set locally>"
$env:LLM_BASE_URL="https://api.deepseek.com"
$env:LLM_MODEL="deepseek-chat"
go run ./promptiter_regression_loop `
  -config ./promptiter_regression_loop/data/promptiter.json `
  -mode real_llm
```

结果文件：

- `optimization_report.json`
- `optimization_report.md`
- `real_llm_optimization_report.json`
- `real_llm_optimization_report.md`

最新一次真实 LLM 成功运行的结论数据：

| 字段 | 值 |
|---|---:|
| 决策 | REJECT |
| Baseline 训练集分数 | 0.7778 |
| Candidate 训练集分数 | 0.6667 |
| Baseline 验证集分数 | 0.7778 |
| Candidate 验证集分数 | 0.7778 |
| 验证集分数增量 | 0.0000 |
| 新增失败验证 case | 0 |
| 关键 case 退化 | 0 |
| 总调用次数 | 12 |
| 预估成本 | 0.000125 |
| 耗时 | 57758 ms |

Gate 原因：

- 验证集分数提升 `0.0000` 低于阈值 `0.0500`。

验证集逐 case delta：

| Case | 关键 | Baseline | Candidate | Delta | 转换 |
|---|---:|---:|---:|---:|---|
| `val_json_refund` | false | 0.3333 | 0.3333 | 0.0000 | stayed_fail |
| `val_weather_berlin` | false | 1.0000 | 1.0000 | 0.0000 | stayed_pass |
| `val_critical_direct_status` | true | 1.0000 | 1.0000 | 0.0000 | stayed_pass |

解释：

真实 DeepSeek 链路已通过 Evaluation Service、metric registry、judge runner 和 PromptIter engine 完整跑通。PromptIter 生成的候选 instruction 试图强化 JSON schema 遵循，但在 `val_json_refund` 中仍然输出了 `amount` 而不是 `amount_usd`，因此验证集分数没有提升。候选还让 `train_refund_policy` 的 rubric 失败，训练集分数从 `0.7778` 降到 `0.6667`，说明该 prompt 修改不具备发布条件。

真实 LLM 输出存在随机波动，因此不同轮次可能出现不同措辞和分数。本闭环以最新落盘报告中的验证集 delta、hard fail、关键 case 退化、成本和调用预算为准，而不是直接信任优化器提出的候选 prompt。

## 工程结论

- mock 链路可运行，并能稳定展示目标 regression gate 行为。
- 示例配置默认使用 deterministic 模式，因此没有真实 API Key 也能跑通核心流程。
- 代码显式保留 DeepSeek model/base URL 默认值，同时避免把密钥写入源码。
- real LLM 路径会逐轮复评 PromptIter 候选，并使用外层 regression gate 选择最终候选，而不是固定选择第 1 轮。
- 最新真实 DeepSeek 链路已端到端跑通，但候选因验证集提升 `0.0000` 低于阈值 `0.0500` 被拒绝。
