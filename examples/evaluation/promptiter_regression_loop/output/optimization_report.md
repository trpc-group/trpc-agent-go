# Evaluation + Optimization 回归闭环报告

| 项 | 值 |
|---|---|
| Run ID | `run-1785910298180` |
| 开始时间 | 2026-08-05T14:11:38+08:00 |
| 耗时 | 24 ms |
| 随机种子 | 42 |
| 模型 | scripted-headline-card (fake) |
| 目标 surface | `candidate#instruction` |

## 1. 基线评测

| 数据集 | 总分 |
|---|---|
| 训练集 | 0.000 |
| 验证集 | 0.000 |

### 验证集逐 case 基线

| Case | 通过 | 分数 | 失败归因 |
|---|---|---|---|
| `validation_01_baseball` | false | 0.000 | format_error |
| `validation_02_ice_hockey` | false | 0.000 | format_error |
| `validation_03_badminton` | false | 0.000 | format_error |

## 2. 优化轮次

| 轮次 | 训练分 | 验证分 | Engine 接受 | Gate 接受 | 接受原因 |
|---|---|---|---|---|---|
| 1 | 0.000 | 1.000 | true | true | all gate checks passed |
| 2 | 0.667 | 0.667 | false | false | gate rejected candidate: validation_score_gain, no_new_hard_fail |
| 3 | 0.667 | 0.000 | false | false | gate rejected candidate: validation_score_gain, no_new_hard_fail, key_cases_no_regression |

### 逐 case delta(相对基线验证集)

**Round 1**(训练 0.000 → 验证 1.000)

| Case | 基线分 | 候选分 | Δ | 结果 |
|---|---|---|---|---|
| `validation_01_baseball` | 0.000 | 1.000 | +1.000 | newly_passed |
| `validation_02_ice_hockey` | 0.000 | 1.000 | +1.000 | newly_passed |
| `validation_03_badminton` | 0.000 | 1.000 | +1.000 | newly_passed |

**Round 2**(训练 0.667 → 验证 0.667)

| Case | 基线分 | 候选分 | Δ | 结果 |
|---|---|---|---|---|
| `validation_01_baseball` | 1.000 | 0.500 | -0.500 | newly_failed |
| `validation_02_ice_hockey` | 1.000 | 1.000 | +0.000 | unchanged |
| `validation_03_badminton` | 1.000 | 0.500 | -0.500 | newly_failed |

**Round 3**(训练 0.667 → 验证 0.000)

| Case | 基线分 | 候选分 | Δ | 结果 |
|---|---|---|---|---|
| `validation_01_baseball` | 1.000 | 0.000 | -1.000 | newly_failed |
| `validation_02_ice_hockey` | 1.000 | 0.000 | -1.000 | newly_failed |
| `validation_03_badminton` | 1.000 | 0.000 | -1.000 | newly_failed |


## 3. 接受门禁(Gate)

**最终决策:接受候选**

all gate checks passed

| 检查项 | 结果 | 详情 |
|---|---|---|
| validation_score_gain | 通过 | validation 0.000 -> 1.000 (delta +1.000), threshold +0.050 |
| no_new_hard_fail | 通过 | 0 new hard fails |
| key_cases_no_regression | 通过 | 1 key case(s) checked, no regression |
| budget_within_limit | 通过 | 40 model calls / 24 ms |

## 4. 失败归因

共 3 个失败 case,覆盖 2 个类别:

| 类别 | 数量 |
|---|---|
| final_response_mismatch | 2 |
| format_error | 1 |

## 5. 成本与预算

| 项 | 值 |
|---|---|
| 模型调用次数 | 40 / 200 |
| 总耗时 | 24 ms / 180000 ms |
| 预算内 | true |

## 6. 优化建议

接受并回写第 1 轮候选 prompt(验证集 0.000 → 1.000),已通过全部门禁检查。建议将 "[STAGE_GOOD] 严格输出包含 headline 与 source 字段的 JSON 对象,headline 必须原样使用输入中的 headline 字段" 更新到源 prompt 并归档本报告。
