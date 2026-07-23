# PromptIter Regression Loop Report

结论：**接受候选 Prompt**。

- 场景：`improvement`
- Baseline Train：`0.500`
- Baseline Validation：`0.667`
- 模型调用：`35`
- 总 Token：`18390`
- 耗时：`26ms`
- 成本：`null`（`not_configured`）
- Gate 理由：`ALL_CHECKS_PASSED`

## Round 1

- Candidate Train：`1.000`，Delta `0.500`
- Candidate Validation：`1.000`，Delta `0.333`
- 独立 Gate：`true`
- PromptIter 内部 acceptance（仅审计）：`true`

- 候选 Prompt 产物：`round-001-candidate_prompt.txt`

### Validation 逐 Case/Metric Delta

| Case | Metric | Baseline | Candidate | Delta | Transition |
|---|---|---:|---:|---:|---|
| validation-distance-01 | final_response_avg_score | 0.000 | 1.000 | 1.000 | newly_passed |
| validation-distance-01 | tool_trajectory_avg_score | 0.000 | 1.000 | 1.000 | newly_passed |
| validation-scope-01 | final_response_avg_score | 1.000 | 1.000 | 0.000 | unchanged |
| validation-scope-01 | tool_trajectory_avg_score | 1.000 | 1.000 | 0.000 | unchanged |
| validation-weather-01 | final_response_avg_score | 1.000 | 1.000 | 0.000 | unchanged |
| validation-weather-01 | tool_trajectory_avg_score | 1.000 | 1.000 | 0.000 | unchanged |

## 失败归因统计

- `final_response_mismatch`: 1
- `tool_selection_error`: 4

## 发布产物

已生成 `accepted_prompt.txt`。
