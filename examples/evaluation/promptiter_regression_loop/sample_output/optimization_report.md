# PromptIter 优化回归报告

- 结论：**接受候选 `candidate-balanced`**
- 运行 ID：`sample-promptiter-regression-20260710`
- 模式 / 随机种子：`fake` / `20260710`
- Baseline prompt SHA-256：`3c789d0631455ed15c4b700f815fe13d7e052825105140b3cf0459f4a7800820`
- Candidate prompt SHA-256：`69fc7e3d04d550956b1af9f92e2b54dba8bdbc5fd43d3496dcd89569defc4580`
- Candidate semantic SHA-256（不含 fake marker）：`48188b412e0364151c49137c894668c234dddfb7e211b490e1057cfe10bfdc80`
- 记录时间：2026-07-21T07:28:06.183Z — 2026-07-21T07:28:06.186Z（墙钟 3 ms）

## 分数摘要

| 数据集 | Baseline | Candidate | Delta | Baseline 通过数 | Candidate 通过数 |
|---|---:|---:|---:|---:|---:|
| train | 0.522041 | 0.797193 | +0.275152 | 0 | 2 |
| validation | 0.720000 | 0.856667 | +0.136667 | 1 | 2 |

## 输出绑定审计

| 数据集 | Prompt 语义哈希绑定 | 已验证 Baseline fallback |
|---|---:|---:|
| train | 3 | 0 |
| validation | 3 | 0 |

## Train 逐 case 回归

| Case | Baseline | Candidate | Delta | 状态 | 新增 hard fail | 关键 case |
|---|---:|---:|---:|---|---|---|
| train_knowledge_no_gain | 0.391579 | 0.391579 | +0.000000 | unchanged_failure | false | false |
| train_order_json_format | 0.584545 | 1.000000 | +0.415455 | new_pass | false | false |
| train_weather_tool_args | 0.590000 | 1.000000 | +0.410000 | new_pass | false | false |

## Validation 逐 case 回归

| Case | Baseline | Candidate | Delta | 状态 | 新增 hard fail | 关键 case |
|---|---:|---:|---:|---|---|---|
| validation_coupon_no_gain | 0.570000 | 0.570000 | +0.000000 | unchanged_failure | false | false |
| validation_critical_refund | 1.000000 | 1.000000 | +0.000000 | unchanged_pass | false | true |
| validation_weather_unseen | 0.590000 | 1.000000 | +0.410000 | new_pass | false | false |

## 接受门禁

| 检查 | 结果 | 实际值 | 条件 | 阈值 | 说明 |
|---|---|---:|---|---:|---|
| evaluation_comparable | PASS | 1.000000 | == | 1.000000 | baseline and candidate cover the same cases and metrics |
| delta_consistent | PASS | 1.000000 | == | 1.000000 | provided delta matches evaluation summaries |
| prompt_changed | PASS | 1.000000 | == | 1.000000 | candidate prompt changed |
| min_validation_score_gain | PASS | 0.136667 | >= | 0.050000 | validation score gain 0.136667 meets threshold |
| max_new_failures | PASS | 0.000000 | <= | 0.000000 | new failures 0 are within limit |
| no_new_hard_failures | PASS | 0.000000 | == | 0.000000 | candidate introduced no hard failures |
| critical_cases_non_regression | PASS | 0.000000 | <= | 0.000000 | critical cases did not regress |
| max_per_case_score_drop | PASS | 0.000000 | <= | 0.050000 | maximum per-case score drop 0.000000 is within limit |
| max_cost_usd | PASS | 0.000000 | <= | 0.000000 | candidate cost 0.000000 is within budget |
| max_model_calls | PASS | 6.000000 | <= | 6.000000 | model calls 6.000000 is within budget |
| max_total_calls | PASS | 8.000000 | <= | 10.000000 | model and tool calls 8.000000 is within budget |
| max_latency_ms | PASS | 58.000000 | <= | 120.000000 | candidate latency 58.000000 is within budget |

决策理由：

- all acceptance checks passed

## Baseline 失败归因统计

| 分类 | 次数 |
|---|---:|
| format_error | 1 |
| knowledge_retrieval_insufficient | 1 |
| route_error | 1 |
| tool_parameter_error | 2 |

## 成本与时延

| Prompt | Model calls | Tool calls | Input tokens | Output tokens | Cost USD | Latency ms |
|---|---:|---:|---:|---:|---:|---:|
| baseline | 6 | 2 | 249 | 67 | 0.000000 | 60 |
| candidate | 6 | 2 | 466 | 73 | 0.000000 | 58 |
| delta | +0 | +0 | +217 | +6 | +0.000000 | -2 |
| total run (baseline + all rounds) | 18 | 6 | 1139 | 211 | 0.000000 | 175 |

## 优化轮次审计

| Round | Candidate | Train delta | Validation delta | 新增通过 | 新增失败 | Gate | Prompt SHA-256 |
|---:|---|---:|---:|---:|---:|---|---|
| 1 | candidate-overfit | +0.275152 | -0.194583 | 0 | 1 | REJECT | `92e9f70b5e9d682194a80363b42144f7a212d1a06182f2fbf5feac2978dc258c` |
| 2 | candidate-balanced | +0.275152 | +0.136667 | 1 | 0 | ACCEPT | `69fc7e3d04d550956b1af9f92e2b54dba8bdbc5fd43d3496dcd89569defc4580` |

轮次决策理由：

- Round 1 `candidate-overfit`：validation score gain -0.194583 is below 0.050000; new failures 1 exceed limit 0; candidate introduced 1 hard failures; critical-case gate failed; regressed=[validation_critical_refund] unknown=[]; maximum per-case score drop 0.583750 exceeds 0.050000 for [validation_critical_refund]
- Round 2 `candidate-balanced`：all acceptance checks passed

每轮完整 prompt、metric、trace、归因、delta、门禁与 usage 均保存在同目录 JSON 报告中。
