# Prompt 优化报告

## 结论

**拒绝**：拒绝全部候选；最优候选（第 1 轮）未通过规则: max_regressed_cases, protected_cases。训练集 +0.1250 但验证集 case val_02_protected_format 由 pass 转 fail，判定为过拟合

- 运行 ID：`run-20260709-184441.447944700`（mode=fake，seed=20260705，耗时 49ms）
- 验证集总分：baseline 0.6667 → 候选 0.8333
- 训练集总分：baseline 0.3750 → 候选 0.5000

## 分数对照

| 集合 | baseline 分数 | baseline 通过 | 候选分数 |
|---|---|---|---|
| train | 0.3750 | 1/4 | 0.5000 |
| validation | 0.6667 | 2/3 | 0.8333 |

## 逐 case delta（validation）

汇总：新增通过 1，新增失败 1，提升 0，退化 0，不变 1

| case | 变化 | baseline | 候选 | Δ分数 | 候选侧根因 |
|---|---|---|---|---|---|
| val_01_generalize_tool_and_format | new_pass | fail 0.00 | pass 1.00 | +1.00 | - |
| val_02_protected_format | new_fail | pass 1.00 | fail 0.50 | -0.50 | final_response_mismatch |
| val_03_stable_pass | unchanged | pass 1.00 | pass 1.00 | +0.00 | - |

## 逐 case delta（train）

| case | 变化 | baseline | 候选 | Δ分数 |
|---|---|---|---|---|
| train_01_response_completeness | new_pass | fail 0.50 | pass 1.00 | +0.50 |
| train_02_wrong_tool_choice | unchanged | fail 0.00 | fail 0.00 | +0.00 |
| train_03_stable_tool_pass | unchanged | pass 1.00 | pass 1.00 | +0.00 |
| train_04_wrong_tool_argument | unchanged | fail 0.00 | fail 0.00 | +0.00 |

## 失败归因

baseline 失败 4 例，候选失败 1 例。

| 类别 | baseline | 候选 |
|---|---|---|
| final_response_mismatch | 1 | 1 |
| tool_call_error | 2 | 0 |
| tool_argument_error | 1 | 0 |

因果链明细：

- [baseline] train/train_01_response_completeness：root: final_response_mismatch
  - final_response_mismatch：final response mismatch: text mismatch: source 您的订单 ORD-1001 正在处理中，请耐心等待。 and target 订单查询结果：订单 ORD-1001 当前状态为已发货，订单金额 299.00 元，预计送达时间 2026-07-08。 do not match
- [baseline] train/train_02_wrong_tool_choice：root: tool_call_error, cascaded to final_response_mismatch
  - tool_call_error：expected tool(s) not called: query_order; unexpected tool call(s): query_logistics
  - final_response_mismatch（由 tool_call_error 级联）：final response mismatch: text mismatch: source 根据物流信息，您的包裹正在运输途中，请留意配送通知。 and target 订单查询结果：订单 ORD-1002 当前状态为待发货，订单金额 58.50 元，预计送达时间 2026-07-10。 do not match
- [baseline] train/train_04_wrong_tool_argument：root: tool_argument_error, cascaded to final_response_mismatch
  - tool_argument_error：tool query_order argument mismatch: expected {"order_id":"ORD-1007"}, actual {"order_id":"ORD-1070"}
  - final_response_mismatch（由 tool_argument_error 级联）：final response mismatch: text mismatch: source 订单查询结果：订单 ORD-1070 当前状态为已发货，订单金额 66.00 元，预计送达时间 2026-07-12。 and target 订单查询结果：订单 ORD-1007 当前状态为待发货，订单金额 129.00 元，预计送达时间 2026-07-14。 do not match
- [baseline] validation/val_01_generalize_tool_and_format：root: tool_call_error, cascaded to final_response_mismatch
  - tool_call_error：tool trajectory mismatch: validate tool counts: number of tool calls mismatch: actual(0) != expected(1)
  - final_response_mismatch（由 tool_call_error 级联）：final response mismatch: text mismatch: source 您的订单 ORD-1004 已收到，我们会尽快安排。 and target 订单查询结果：订单 ORD-1004 当前状态为运输中，订单金额 449.00 元，预计送达时间 2026-07-09。 do not match
- [候选] validation/val_02_protected_format：root: final_response_mismatch
  - final_response_mismatch：final response mismatch: text mismatch: source 订单查询结果：**订单 ORD-1005** - 状态：已取消 - 退款金额：199.00 元 and target 订单 ORD-1005 已取消，退款 199.00 元将在 3 个工作日内原路退回。 do not match

## 候选选择过程

| 轮次 | 验证集分数 | 模型调用 | 耗时 | 过安全门 | 选中 |
|---|---|---|---|---|---|
| 1 | 0.8333 | 11 | 14ms | 否 | 否 |
| 2 | 0.8333 | 11 | 11ms | 否 | 否 |

## 安全门规则明细

| 规则 | 实测 | 阈值 | 结果 | 说明 |
|---|---|---|---|---|
| min_validation_score_gain | +0.1667 | >= 0.0200 | 通过 | 验证集总分 0.8333 对比 baseline 0.6667 |
| max_new_hard_fails | 0 | <= 0 | 通过 | 无新增 hard fail |
| max_regressed_cases | 1 | <= 0 | **未通过** | 退化 case: val_02_protected_format |
| protected_cases | 1 | == 0 | **未通过** | 关键 case 退化: val_02_protected_format |
| max_model_calls | 32 | <= 200 | 通过 | 整个 pipeline 运行的模型调用预算 |
| max_wall_clock | 48ms | <= 3m0s | 通过 | 整个 pipeline 运行的墙钟预算 |

## 轮次时间线

| 轮次 | train | validation | 引擎内层判定 | 模型调用 | 耗时 |
|---|---|---|---|---|---|
| 1 | 0.3750 | 0.8333 | 接受 | 11 | 14ms |
| 2 | 0.5000 | 0.8333 | 拒绝（停止：max rounds reached） | 11 | 11ms |

## 成本摘要

| scope | 推理次数 | 模型调用 | prompt tokens | completion tokens |
|---|---|---|---|---|
| candidate | 21 | 32 | 9255 | 611 |
| **合计** | 21 | 32 | 9255 | 611 |

分阶段耗时：s1_baseline_train=11ms s2_attribution=1ms s3_optimization=31ms s4_delta=2ms s5_gate=0s 

## 下一步建议

- 候选被拒绝，baseline prompt 保持不变。
- 参考失败归因与逐 case delta 修订评测集或调整优化目标后重跑。

## 候选 prompt 全文

```
你是电商客服助手。回答用户关于订单的问题。

【优化标记M1】回答订单问题时，必须以"订单查询结果："开头，并包含订单状态、订单金额和预计送达时间。
```
