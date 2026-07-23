# PromptIter 回归闭环报告

- Run ID：`promptiter-regression-loop-app-20260717-real`
- App：`promptiter-regression-loop-app`
- 模式：`real_llm`
- 数据来源：`real LLM via OpenAI-compatible endpoint; evalsets remain local reproducible fixtures`
- 决策：**REJECT**
- 目标 surface：`travel-support#instruction`
- 引擎：`real-llm`（`deepseek-chat`）

## 分数汇总

| 数据集 | Baseline | Candidate | Delta |
|---|---:|---:|---:|
| Train | 0.7778 | 0.6667 | -0.1111 |
| Validation | 0.7778 | 0.7778 | 0.0000 |

## Gate 决策

- 验证集分数提升 `0.0000` 低于阈值 `0.0500`。

## 验证集逐 Case Delta

| Case | 关键 | Baseline | Candidate | Delta | 转换 |
|---|---:|---:|---:|---:|---|
| `val_json_refund` | false | 0.3333 | 0.3333 | 0.0000 | stayed_fail |
| `val_weather_berlin` | false | 1.0000 | 1.0000 | 0.0000 | stayed_pass |
| `val_critical_direct_status` | true | 1.0000 | 1.0000 | 0.0000 | stayed_pass |

## 验证集输出证据

| Case | Baseline 实际输出 | Baseline 工具数 | Candidate 实际输出 | Candidate 工具数 |
|---|---|---:|---|---:|
| `val_json_refund` | {"status": "approved", "amount": 35} | 0 | {"refund_id": "r-204", "status": "approved", "amount": 35} | 0 |
| `val_weather_berlin` | The weather in Berlin today is cloudy with a temperature of 8°C. | 1 | The weather in Berlin today is cloudy with a temperature of 8°C. | 1 |
| `val_critical_direct_status` | TR900 is boarding at gate K12. | 0 | TR900 is boarding at gate K12. | 0 |

## 失败归因

### Train

- `format_error`：1
- `unknown`：2

### Validation

- `format_error`：1
- `unknown`：1

## 审计摘要

- Candidate：`promptiter-real-round-1`
- 调用次数：`12`
- 预估成本：`$0.000125`
- 耗时：`57758 ms`
- Seed：`20260717`

除非 gate 决策为 ACCEPT，否则候选不能自动发布。
