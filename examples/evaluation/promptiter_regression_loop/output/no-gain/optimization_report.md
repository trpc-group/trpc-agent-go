# PromptIter 回归优化报告

- 应用: promptiter-nba-commentary-app
- 模型: deepseek-v4-flash
- 优化轮次: 3
- 耗时: 194 毫秒
- 分阶段耗时:
  - 引擎运行: 192 毫秒
  - 失败归因: 1 毫秒
  - 门禁判断: 0 毫秒
  - 报告生成: 0 毫秒
- 基线分数: 0.2500
- 候选分数: 0.2500
- 分数变化: +0.0000
- 是否接受: false
- 门禁理由: validation score gain below minimum requirement
- 拒绝原因: insufficient_gain

## 基线失败归因

- 归因方法: rule
- 总 case 数: 8，失败 case 数: 8（共 16 条失败指标，即每个 case 平均 2.0 条）
- 按类别分布：response_mismatch=16 
- 洞察（rule）：全部 16 个失败均集中在 response_mismatch。
- 失败模式：
  - response_mismatch: 16 个 (100%)（例如：final response mismatch: text mismatch: length mismatch: actual length 72 is less than min 350, expected range [350, 850]）
- 聚类（去重）：
  - response_mismatch × 8：final response mismatch: text mismatch: length mismatch: actual length 72 is less than min 350, expected range [350, 850]  [nba-commentary-validation/validation_01_nba_empty_48, nba-commentary-validation/validation_02_football_penalties, nba-commentary-validation/validation_03_f1_rain_safety_car, nba-commentary-validation/validation_04_badminton_match_point, nba-commentary-validation/validation_05_ice_hockey_overtime]
  - response_mismatch × 8：回复基本完整但信息密度不足。  [nba-commentary-validation/validation_01_nba_empty_48, nba-commentary-validation/validation_02_football_penalties, nba-commentary-validation/validation_03_f1_rain_safety_car, nba-commentary-validation/validation_04_badminton_match_point, nba-commentary-validation/validation_05_ice_hockey_overtime]

## 自然语言总结

本次优化候选在验证集上得分为 0.2500（基线 0.2500，变化 +0.0000），gate 决策为已拒绝。拒绝理由：validation score gain below minimum requirement（insufficient_gain）。 全部 16 个失败均集中在 response_mismatch。

## LLM 增强可观测性

- LLM 调用次数：0（批量归因 + 合并报告各计一次）
- LLM 错误次数：0（任何错误均回退确定性规则）
  说明：全流程以确定性规则运行（规则归因），未发起任何 LLM 调用。

## 基线评测（逐 case）

- [validation] nba-commentary-validation：8 个 case，0 通过，8 失败
- [train] nba-commentary-train：8 个 case，0 通过，8 失败
  （完整逐 case 指标分数 / 通过-失败 / 理由 / trace 见 optimization_report.json）

## 优化轮次

| 轮次 | 训练集 | 验证集 | 引擎是否接受 | 理由 |
| --- | --- | --- | --- | --- |
| 1 | 0.2500 | 0.2500 | false | candidate score gain does not satisfy acceptance policy |
| 2 | 0.2500 | 0.2500 | false | candidate score gain does not satisfy acceptance policy |
| 3 | 0.2500 | 0.2500 | false | candidate score gain does not satisfy acceptance policy |
（每轮候选 prompt 见 optimization_report.json 的 `roundRecords`）

## 逐 case 分数变化

| 评测集 | Case | 基线 | 候选 | 基线通过 | 候选通过 | 变化 | 趋势 | 变化类别 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| nba-commentary-validation | validation_06_tennis_tiebreak | 0.2500 | 0.2500 | false | false | +0.0000 | 持平 | 持平 |
| nba-commentary-validation | validation_02_football_penalties | 0.2500 | 0.2500 | false | false | +0.0000 | 持平 | 持平 |
| nba-commentary-validation | validation_03_f1_rain_safety_car | 0.2500 | 0.2500 | false | false | +0.0000 | 持平 | 持平 |
| nba-commentary-validation | validation_05_ice_hockey_overtime | 0.2500 | 0.2500 | false | false | +0.0000 | 持平 | 持平 |
| nba-commentary-validation | validation_07_road_cycling_breakaway | 0.2500 | 0.2500 | false | false | +0.0000 | 持平 | 持平 |
| nba-commentary-validation | validation_08_cricket_t20_chase | 0.2500 | 0.2500 | false | false | +0.0000 | 持平 | 持平 |
| nba-commentary-validation | validation_01_nba_empty_48 | 0.2500 | 0.2500 | false | false | +0.0000 | 持平 | 持平 |
| nba-commentary-validation | validation_04_badminton_match_point | 0.2500 | 0.2500 | false | false | +0.0000 | 持平 | 持平 |

## 成本

- 评测单元：56（单元成本 0.0100）
- 工作流单元：30（单元成本 0.0500）
- 估算总成本：2.0600
- 预算：无限制

## 配置

- 应用：promptiter-nba-commentary-app（候选 agent：candidate）
- Prompt 类型：agent
- 训练评测集：[nba-commentary-train]
- 验证评测集：[nba-commentary-validation]
- 指标文件：sports-commentary
- 模型：candidate=deepseek-v4-flash judge=deepseek-v4-flash worker=deepseek-v4-flash
- minScoreGain=0.0100 targetScore=1.0000 maxRounds=4
- 随机种子：42（确定性 fake runner 不受影响；用于真实模型复现）
- Fake 模式：true（场景=no-gain）

## 候选 Prompt

```text
你是一名体育评论员。请照常撰写比赛评论即可，无需额外结构。
```
