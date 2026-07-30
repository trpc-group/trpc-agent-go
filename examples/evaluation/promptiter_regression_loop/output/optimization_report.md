# PromptIter 回归优化报告

- 应用: promptiter-nba-commentary-app
- 模型: deepseek-v4-flash
- 优化轮次: 4
- 耗时: 957473 毫秒
- 分阶段耗时:
  - 引擎运行: 957467 毫秒
  - 失败归因: 5 毫秒
  - 门禁判断: 0 毫秒
  - 报告生成: 0 毫秒
- 基线分数: 0.3984
- 候选分数: 0.8125
- 分数变化: +0.4141
- 是否接受: true
- 门禁理由: accepted: validation gain met, no regression or new hard fails

## 基线失败归因

- 归因方法: rule
- 总 case 数: 8，失败 case 数: 8（共 15 条失败指标，即每个 case 平均 1.9 条）
- 按类别分布：format_error=7 knowledge_gap=1 response_mismatch=7 
- 洞察（rule）：共 15 个失败，主要分布在 format_error（7 个，占比 47%）。
- 失败模式：
  - format_error: 7 个 (47%)（例如：Actual output invents facts not supported by User JSON or Reference Answer: '百事中心' (User: Ball Arena), '杜兰特（本场未列出具体数据但据推测）' (no mention in sources), '尤班克斯+阿伦', '克雷格、奥科吉', '二次进攻得分领先太阳18分' (not in JSON or Reference). These constitute fabrication. Actual opening paragraph sets context (date, venue, teams), states final score, and captures the main angle: despite Booker's 48 points, Nuggets won through rebounding (52-39) and low turnovers (8-16), with a decisive 14-4 run. Matches Reference's primary narrati...(truncated)）
  - response_mismatch: 7 个 (47%)（例如：final response mismatch: text mismatch: length mismatch: actual length 1401 is greater than max 850, expected range [350, 850]）
  - knowledge_gap: 1 个 (7%)（例如：编造了控球率、射门次数等数据面板及战术分析中的战术意图（如“放弃控球”），这些信息在用户JSON和参考战报中无依据。 导语仅泛写结果，未突出参考战报首要强调的“两度追平”这一决定性角度。 正确按顺序描述了点球大战各轮次，包括河城FC第3、第5轮罚失，与参考战报一致。 总比分写为“6-5”，正确应为“5-6”，方向错误。 标题未包含参考标题核心的“两度追平”和罚失轮次，角度不足。 使用了编造的数据（控球率等），且未与胜负结�...(truncated)）
- 聚类（去重）：
  - response_mismatch × 7：final response mismatch: text mismatch: length mismatch: actual length 1401 is greater than max 850, expected range [350, 850]  [nba-commentary-validation/validation_01_nba_empty_48, nba-commentary-validation/validation_02_football_penalties, nba-commentary-validation/validation_03_f1_rain_safety_car, nba-commentary-validation/validation_05_ice_hockey_overtime, nba-commentary-validation/validation_06_tennis_tiebreak]
  - format_error × 1：Actual output invents facts not supported by User JSON or Reference Answer: '百事中心' (User: Ball Arena), '杜兰特（本场未列出具体数据但据推测）' (no mention in sources), '尤班克斯+阿伦', '克雷格、奥科吉', '二次进攻得分领先太阳18分' (not in JSON or Reference). These constitute fabrication.。Actual opening paragraph sets context (date, venue, teams), states final score, and captures the main angle: despite Booker's 48 points, Nuggets won through rebounding (52-39) and low turnovers (8-16), with a decisive 14-4 run. Matches Reference's primary narra...(truncated)  [nba-commentary-validation/validation_01_nba_empty_48]
  - format_error × 1：Actual output includes unverified details such as 'Aurora Racing本赛季第二场胜利，港湾赛道首胜', '差距缩小至2秒内', and '维修区换胎仅用时2.4秒', which are not supported by the user JSON or reference answer. Golden: only reports given facts. Actual: adds fabricated specifics, violating the requirement to use only supported facts.。The opening paragraph correctly sets the context (rain race, 58 laps), states the final result (Silva wins, Brooks second, Ren third), and highlights the decisive angle (safety car and pit strategy), matching the reference answer's main...(truncated)  [nba-commentary-validation/validation_03_f1_rain_safety_car]
  - format_error × 1：Actual output uses only facts from JSON and reference: scores, net front data, turning points. No fabricated information detected. The 75-minute duration is not in JSON or reference, but it is a minor addition that does not contradict any given data。however, for source_grounding, it is not a material defect because it is a harmless extrapolation and does not invent key facts. Score 1.。Opening paragraph sets context (international badminton invitational, women's singles semifinal), final result (2-1 win), and highlights reversal storyline from reference: '19-20落后时网前扑球挽救赛...(truncated)  [nba-commentary-validation/validation_04_badminton_match_point]
  - format_error × 1：Actual output introduces unsupported facts: venue "北港竞技场" (golden uses "North Harbor Arena") and player "隋远" for a second-period goal (not in golden or JSON). Fabricated details violate source grounding requirement.。Opening paragraph correctly sets context (venue, result), highlights the decisive angle of surviving penalty kill and scoring overtime goal, consistent with golden's main story.。Actual output preserves the correct sequence of key moments: tying goal (第三节17:42), penalty (68s), saves on Han Wei and Ye Ming, return from penalty, then assist and goal. No omissio...(truncated)  [nba-commentary-validation/validation_05_ice_hockey_overtime]
  - format_error × 1：All facts (score, tiebreak, aces, first serve%, break points, set point saves) are from user JSON and reference answer. No fabricated details.。Lead paragraph covers result (7-6(10),6-3), tiebreak 12-10, three set points saved, and emphasizes the match was not easy, matching golden answer's main angle.。Correctly retains decisive chain: saved 2 set points at 5-6, then saved third at 8-9 in tiebreak。second set: two breaks for winner, one break back for loser. Order preserved.。All numbers match user JSON: sets, tiebreak 12-10, aces 9-4, first serve% 78%-66%, break points 3/7 and 2/6, secon...(truncated)  [nba-commentary-validation/validation_06_tennis_tiebreak]
  - format_error × 1：Golden: all facts from JSON and reference. Actual: uses only provided data, no invented facts. Materially matches.。Golden: opens with context, result, main angle (breakaway, leadout, half-bike win). Actual: first paragraph sets race context, winner, and key tactical elements. Materially matches.。Golden: breakaway at 5km, gap changes (9s→6s→4s→7s), Zhao Yu leadout until 600m, sprint at 180m. Actual: same sequence in correct order. No omissions or misordering.。Golden and JSON: distance 5km, gaps exactly as provided, sprint distance 180m, margin half-bike, result order. Actual: all nu...(truncated)  [nba-commentary-validation/validation_07_road_cycling_breakaway]
  - format_error × 1：Actual includes unsupported tactical details (e.g., '连续三个短球被梅塔识破', '近身外场站位未能收缩保护边线') not present in JSON or reference. Fails source grounding.。First paragraph sets context: teams, result, key player Mehta, final over chase. Matches main angle of reference.。Critical sequence preserved: 109/7 needing 11 off 6, Mehta scores 12 in final over, target reached on 19.5. No omission or reordering.。All numbers (119/8, 121/7, 3 wickets, 47* off 39, 4-0-18-3, 19.5 overs) match JSON and retain proper cricket semantics.。Headline '猎鹰绝地反击�...(truncated)  [nba-commentary-validation/validation_08_cricket_t20_chase]
  - knowledge_gap × 1：编造了控球率、射门次数等数据面板及战术分析中的战术意图（如“放弃控球”），这些信息在用户JSON和参考战报中无依据。导语仅泛写结果，未突出参考战报首要强调的“两度追平”这一决定性角度。正确按顺序描述了点球大战各轮次，包括河城FC第3、第5轮罚失，与参考战报一致。总比分写为“6-5”，正确应为“5-6”，方向错误。标题未包含参考标题核心的“两度追平”和罚失轮次，角度不足。使用了编造的数据（控球率等），且未与胜负结果关�...(truncated)  [nba-commentary-validation/validation_02_football_penalties]

## 自然语言总结

本次优化候选在验证集上得分为 0.8125（基线 0.3984，变化 +0.4141），gate 决策为已接受。候选满足接受条件。 共 15 个失败，主要分布在 format_error（7 个，占比 47%）。

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
| 1 | 0.2812 | 0.6094 | true | candidate score gain satisfies acceptance policy |
| 2 | 0.6016 | 0.7969 | true | candidate score gain satisfies acceptance policy |
| 3 | 0.8281 | 0.7500 | false | candidate score gain does not satisfy acceptance policy |
| 4 | 0.7109 | 0.8125 | true | candidate score gain satisfies acceptance policy |

（每轮候选 prompt 见 optimization_report.json 的 `roundRecords`）

## 逐 case 分数变化

| 评测集 | Case | 基线 | 候选 | 基线通过 | 候选通过 | 变化 | 趋势 | 变化类别 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| nba-commentary-validation | validation_01_nba_empty_48 | 0.3125 | 1.0000 | false | true | +0.6875 | 上升 | 新增通过 |
| nba-commentary-validation | validation_02_football_penalties | 0.1250 | 0.9375 | false | false | +0.8125 | 上升 | 分数提升 |
| nba-commentary-validation | validation_03_f1_rain_safety_car | 0.3750 | 0.4375 | false | false | +0.0625 | 上升 | 分数提升 |
| nba-commentary-validation | validation_04_badminton_match_point | 0.8750 | 1.0000 | false | true | +0.1250 | 上升 | 新增通过 |
| nba-commentary-validation | validation_05_ice_hockey_overtime | 0.3125 | 0.3125 | false | false | +0.0000 | 持平 | 持平 |
| nba-commentary-validation | validation_06_tennis_tiebreak | 0.4375 | 0.9375 | false | false | +0.5000 | 上升 | 分数提升 |
| nba-commentary-validation | validation_07_road_cycling_breakaway | 0.3750 | 0.8750 | false | false | +0.5000 | 上升 | 分数提升 |
| nba-commentary-validation | validation_08_cricket_t20_chase | 0.3750 | 1.0000 | false | true | +0.6250 | 上升 | 新增通过 |

## 成本

- 评测单元：72（单元成本 0.0100）
- 工作流单元：40（单元成本 0.0500）
- 估算总成本：2.7200
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
- Fake 模式：false（场景=happy）

## 候选 Prompt

```text
你是一名资深体育评论员。根据提供的比赛数据撰写，严格禁止使用任何数据之外的信息。开头需包含一个标题，标题中总结比赛结果和关键转折点，并使用连字符表示比分（例如"湖人102-98火箭"）。用纯文字（不使用任何Markdown语法）将内容划分为三个部分：战报、数据面板、战术分析。总字数严格控制在350至850字之间，不得少于350字，不得多于850字。如果超出850字，输出无效。确保所有事实（比分、球员姓名、赛况等）完全来自输入数据，无任何编造；所有数值准确，无数字错误。特别注意：禁止添加输入数据中未明确给出的信息（如球员位置、晋级状态、战术意图、心理描述等）。所有陈述必须与输入数据完全一致，不得反转或改变任何事实。
```
