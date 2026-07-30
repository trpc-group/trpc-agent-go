# PromptIter 回归优化报告

- 应用: promptiter-nba-commentary-app
- 模型: deepseek-v4-flash
- 优化轮次: 4
- 耗时: 848390 毫秒
- 基线分数: 0.2656
- 候选分数: 0.9062
- 分数变化: +0.6406
- 是否接受: true
- 门禁理由: accepted: validation gain met, no regression or new hard fails

## 基线失败归因

- 归因方法: rule
- 总 case 数: 8，失败 case 数: 8（共 15 条失败指标，即每个 case 平均 1.9 条）
- 按类别分布：response_mismatch=7 format_error=7 tool_call_error=1 
- 洞察（rule）：共 15 个失败，主要分布在 format_error（7 个，占比 47%）。
- 失败模式：
  - format_error: 7 个 (47%)（例如：All facts (venue, scores, stats, players, decisive stretch) are supported by user JSON and reference answer. No fabrication. Opening paragraph covers background, final result, and main angle (team depth/rebounds vs Booker's 48 points) matching reference. Actual output correctly preserves the decisive chain: time 5:42, 14-4 run, 4 Suns turnovers, 5 Nuggets offensive rebounds. All numbers (scores, stats, player lines, time) match JSON exactly; no errors in values or semantics. Headline '团队篮球制胜：掘金主场 118-111 力克布克48分' combines result with key angle, similar to refere...(truncated)）
  - response_mismatch: 7 个 (47%)（例如：final response mismatch: text mismatch: length mismatch: actual length 1244 is greater than max 850, expected range [350, 850]）
  - tool_call_error: 1 个 (7%)（例如：Actual output fabricates events not supported by JSON or reference: e.g., '开场仅4分钟' goal, second-period scoring details, and '锋线尖刀韩维首开记录'. Golden: no such specifics. Actual: invented details. Material defect. Lead paragraph sets context (venue), result (4-3 OT win), and main storyline (PK and clutch goal) as required. Semantically matches golden answer's emphasis. Retains correct order: 林澈扳平, 周砚犯规, 许知远扑救, 周砚回场, 陆行舟绝杀. All key steps present with accurate timing. All scores, period details, time points, special teams stats,...(truncated)）
- 聚类（去重）：
  - response_mismatch × 7：final response mismatch: text mismatch: length mismatch: actual length 1244 is greater than max 850, expected range [350, 850]  [nba-commentary-validation/validation_01_nba_empty_48, nba-commentary-validation/validation_03_f1_rain_safety_car, nba-commentary-validation/validation_04_badminton_match_point, nba-commentary-validation/validation_05_ice_hockey_overtime, nba-commentary-validation/validation_06_tennis_tiebreak]
  - format_error × 1：All facts (venue, scores, stats, players, decisive stretch) are supported by user JSON and reference answer. No fabrication.。Opening paragraph covers background, final result, and main angle (team depth/rebounds vs Booker's 48 points) matching reference.。Actual output correctly preserves the decisive chain: time 5:42, 14-4 run, 4 Suns turnovers, 5 Nuggets offensive rebounds.。All numbers (scores, stats, player lines, time) match JSON exactly。no errors in values or semantics.。Headline '团队篮球制胜：掘金主场 118-111 力克布克48分' combines result with key angle, similar ...(truncated)  [nba-commentary-validation/validation_01_nba_empty_48]
  - format_error × 1：Final answer contains fabricated data (e.g., shot/possession stats, formation 4-4-2, tactical analysis) not supported by user JSON or reference answer. This violates source grounding.。Lead paragraph only states result and 'epic showdown', omitting the decisive angle of 'twice equalizing then winning on penalties' emphasized in the reference answer.。Final answer omits detailed penalty round progression (e.g., first two rounds all scored, Round 4 miss by Hai Gang Lian) and only summarizes misses, losing key steps present in reference.。All scores, times, and penalty outcome match user JSON ...(truncated)  [nba-commentary-validation/validation_02_football_penalties]
  - format_error × 1：Actual says '安全车的两次介入', but JSON and Reference only mention one safety car deployment (laps 31-34). Also actual fabricates '换胎后5圈内每圈快出2.1-2.8秒' not found in JSON or Reference. Defect: unmatchable facts.。Golden: opening should include context, result, and decisive angle. Actual: '2025赛季F1雨战大奖赛...结束. 最终Marco Silva...夺得冠军...Evan Brooks...Luca Ren...季军' and identifies strategy as key. Satisfies.。Golden: safety car 31-34, pit window 34-36, Silva/Brooks pit lap 35, Ren lap 36. Actual: same order and details preserved. Matches....(truncated)  [nba-commentary-validation/validation_03_f1_rain_safety_car]
  - format_error × 1：Actual adds unsupported facts: '中国名将', '日本选手', '心理素质', '战术执行力', '身高优势'. Golden does not include these。JSON does not specify nationalities or such traits. Material defect: fabricated details.。Opening paragraph ('在刚刚结束...成功挺进决赛') does not highlight the decisive angle '19-20救赛点连下三分' from Golden. Generic reversal without specific turning point. Material omission of main angle.。Actual retains the decisive chain: '第二局19-20时林悦网前扑球挽救赛点', '从19-20连得3分以22-20拿下', and mentions the s...(truncated)  [nba-commentary-validation/validation_04_badminton_match_point]
  - format_error × 1：Actual invents tournament '中国网球公开赛', '本赛季', and '制胜分/非受迫性失误' 25/18, 22/20 not in JSON or reference.。Lead paragraph includes fabricated tournament '中国网球公开赛' and extra details not in reference, undermining grounding.。Actual preserves correct order: save 2 set points at 5-6, save 1 at 8-9 in tiebreak, win tiebreak 12-10, second set with two breaks and one break back.。Actual adds fabricated stats '制胜分/非受迫性失误' 25/18 and 22/20 not in JSON or reference, introducing unsupported numbers.。Headline includes fabricated tournamen...(truncated)  [nba-commentary-validation/validation_06_tennis_tiebreak]
  - format_error × 1：Actual adds unsupported details: '双核领骑战术', '周明远试图变线反压', '利用赛道右侧下坡微幅倾斜获得额外加速空间'. These are not present in JSON or reference answer, constituting fabrication.。Actual opening paragraph: '在刚刚结束的个人公路赛最后5公里决战中...夺得冠军.' It covers context (individual road race, final 5km), final result (Lin Che wins by half a bike length), and matches the reference's emphasis on decisive final phase and teammate leadout.。Actual retains the correct chain: 5km breakaway, gap changes (9s→6s→4s→7s), ...(truncated)  [nba-commentary-validation/validation_07_road_cycling_breakaway]
  - format_error × 1：Golden: factual summary from user JSON and reference. Actual: adds fabricated details like '第八回合后中段更连续丢人', '失1 wicket' in final over, and emotional interpretations ('众望所归', '巨大压力'). These are not supported by user JSON or reference. Defect: unsupported claims.。Golden: opens with '梅塔47*撑到19.5 overs 都会猎鹰3 wickets险胜海港国王', highlighting unbeaten score and overs. Actual: '好的，这是一场典型的T20低分惊魂战...最后一球定胜负的悬念' misstates the timing (won on 5th ball, not last ball) and omits key overs an...(truncated)  [nba-commentary-validation/validation_08_cricket_t20_chase]
  - tool_call_error × 1：Actual output fabricates events not supported by JSON or reference: e.g., '开场仅4分钟' goal, second-period scoring details, and '锋线尖刀韩维首开记录'. Golden: no such specifics. Actual: invented details. Material defect.。Lead paragraph sets context (venue), result (4-3 OT win), and main storyline (PK and clutch goal) as required. Semantically matches golden answer's emphasis.。Retains correct order: 林澈扳平, 周砚犯规, 许知远扑救, 周砚回场, 陆行舟绝杀. All key steps present with accurate timing.。All scores, period details, time points, special teams ...(truncated)  [nba-commentary-validation/validation_05_ice_hockey_overtime]

## 自然语言总结

本次优化候选在验证集上得分为 0.9062（基线 0.2656，变化 +0.6406），gate 决策为已接受。候选满足接受条件。 共 15 个失败，主要分布在 format_error（7 个，占比 47%）。

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
| 1 | 0.4609 | 0.5469 | true | candidate score gain satisfies acceptance policy |
| 2 | 0.7188 | 0.8906 | true | candidate score gain satisfies acceptance policy |
| 3 | 0.7969 | 0.8828 | false | candidate score gain does not satisfy acceptance policy |
| 4 | 0.8359 | 0.9062 | true | candidate score gain satisfies acceptance policy |
（每轮候选 prompt 见 optimization_report.json 的 `roundRecords`）

## 逐 case 分数变化

| 评测集 | Case | 基线 | 候选 | 基线通过 | 候选通过 | 变化 | 趋势 | 变化类别 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| nba-commentary-validation | validation_06_tennis_tiebreak | 0.1250 | 1.0000 | false | true | +0.8750 | 上升 | 新增通过 |
| nba-commentary-validation | validation_07_road_cycling_breakaway | 0.2500 | 1.0000 | false | true | +0.7500 | 上升 | 新增通过 |
| nba-commentary-validation | validation_04_badminton_match_point | 0.1875 | 1.0000 | false | true | +0.8125 | 上升 | 新增通过 |
| nba-commentary-validation | validation_05_ice_hockey_overtime | 0.3125 | 1.0000 | false | true | +0.6875 | 上升 | 新增通过 |
| nba-commentary-validation | validation_08_cricket_t20_chase | 0.0625 | 0.9375 | false | false | +0.8750 | 上升 | 分数提升 |
| nba-commentary-validation | validation_01_nba_empty_48 | 0.4375 | 0.8125 | false | false | +0.3750 | 上升 | 分数提升 |
| nba-commentary-validation | validation_02_football_penalties | 0.5625 | 1.0000 | false | true | +0.4375 | 上升 | 新增通过 |
| nba-commentary-validation | validation_03_f1_rain_safety_car | 0.1875 | 0.5000 | false | false | +0.3125 | 上升 | 分数提升 |

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
你是一名资深体育评论员。请生成一篇结构清晰、数据详实的中文战报，必须包含【战报】【数据面板】【战术分析】三个板块，总字数严格控制在 350–850 字之间，不得超出此范围。开头第一行必须是一个简洁的标题，明确写出最终比赛结果和逆转主题，例如“XX队X-X逆转XX队 关键球员XX绝杀”。正文按时间顺序叙述比赛关键事件。所有内容必须严格基于提供的用户 JSON 和参考答案，不得添加任何未在 JSON 或参考中明确出现的事实、数据、姓名、国籍、战术推断、因果解释（如“因……所以……”）。比分必须使用短横线连接（如3-6），禁止使用中文“比”字。禁止使用任何 Markdown 格式（如标题、表格、列表）。输出纯文本。
```
