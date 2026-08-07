# PromptIter 回归闭环报告

- Run ID：`promptiter-regression-loop-app-20260717`
- App：`promptiter-regression-loop-app`
- 模式：`deterministic`
- 数据来源：`fake model with deterministic evalset responses`
- 决策：**REJECT**
- 目标 surface：`travel-support#instruction`
- 引擎：`deterministic-promptiter`（`fake-model-v1`）

## 分数汇总

| 数据集 | Baseline | Candidate | Delta |
|---|---:|---:|---:|
| Train | 0.4444 | 0.8889 | 0.4444 |
| Validation | 0.7778 | 0.8889 | 0.1111 |

## Gate 决策

- 新增 hard fail `1` 超过上限 `0`。
- `1` 个关键验证 case 发生退化。

## 验证集逐 Case Delta

| Case | 关键 | Baseline | Candidate | Delta | 转换 |
|---|---:|---:|---:|---:|---|
| `val_json_refund` | false | 0.3333 | 1.0000 | 0.6667 | fixed |
| `val_weather_berlin` | false | 1.0000 | 1.0000 | 0.0000 | stayed_pass |
| `val_critical_direct_status` | true | 1.0000 | 0.6667 | -0.3333 | regressed |

## 验证集输出证据

| Case | Baseline 实际输出 | Baseline 工具数 | Candidate 实际输出 | Candidate 工具数 |
|---|---|---:|---|---:|
| `val_json_refund` | Refund request r-204 is approved for 35 USD. | 0 | {"refund_id":"r-204","status":"approved","amount_usd":35} | 0 |
| `val_weather_berlin` | Berlin is cloudy today at 8 C. | 1 | Berlin is cloudy today at 8 C. | 1 |
| `val_critical_direct_status` | TR900 is boarding at gate K12. | 0 | {"flight":"TR900","status":"boarding","gate":"K12"} | 0 |

## 失败归因

### Baseline

#### Train

- `final_response_mismatch`：1
- `format_error`：2
- `knowledge_recall_gap`：1
- `tool_argument_error`：1

#### Validation

- `format_error`：2

### Candidate

#### Train

- `knowledge_recall_gap`：1

#### Validation

- `final_response_mismatch`：1

## 审计摘要

- Candidate：`round-1-json-tool-overfit`
- 调用次数：`12`
- 预估成本：`$0.000114`
- 耗时：`0 ms`
- Seed：`20260717`

除非 gate 决策为 ACCEPT，否则候选不能自动发布。
