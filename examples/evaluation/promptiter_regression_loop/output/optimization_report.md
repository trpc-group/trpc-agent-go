# PromptIter 回归优化报告

- 应用: promptiter-nba-commentary-app
- 模型: deepseek-v4-flash
- 优化轮次: 4
- 耗时: 770068 毫秒
- 基线分数: 0.2734
- 候选分数: 0.7031
- 分数变化: +0.4297
- 是否接受: true
- 门禁理由: accepted: validation gain met, no regression or new hard fails

## 基线失败归因

- 归因方法: rule
- 总 case 数: 8，失败 case 数: 8（共 16 条失败指标，即每个 case 平均 2.0 条）
- 按类别分布：response_mismatch=8 format_error=6 tool_call_error=2 
- 洞察（rule）：共 16 个失败，主要分布在 response_mismatch（8 个，占比 50%）。
- 失败模式：
  - response_mismatch: 8 个 (50%)（例如：final response mismatch: text mismatch: length mismatch: actual length 1250 is greater than max 850, expected range [350, 850]）
  - format_error: 6 个 (38%)（例如：Actual output fabricates unsupported facts: e.g., '首节独得15分' and '半场结束时，掘金仅领先3分' not present in user JSON or golden answer. Only facts from those sources are allowed. Opening paragraph omits the primary angle from golden answer: the decisive 14-4 run. It mentions team depth and rebounding but not the specific run that golden answer emphasizes as the main storyline. Actual output correctly describes the decisive stretch: time remaining (5:42), run (14-4), 5 offensive rebounds, 4 turnovers, all matching golden answer and user JSON. All scores, margins, times, and...(truncated)）
  - tool_call_error: 2 个 (12%)（例如：Actual output includes '全场比赛历时75分钟' (match duration) and nationalities '日本名将高桥美咲' and '中国选手林悦', none of which are supported by user JSON or reference answer. Golden: no such facts; Actual: adds unsupported details; User JSON: no time or nationality. Material defect: unsupported facts. Lead paragraph covers event (国际羽毛球邀请赛女单半决赛), final result (17-21, 22-20, 21-16逆转), and winning narrative (先失一局后逆转). Semantically matches golden answer's emphasis on reversal. Actual output preserves decisive chain: second game...(truncated)）
- 聚类（去重）：
  - response_mismatch × 8：final response mismatch: text mismatch: length mismatch: actual length 1250 is greater than max 850, expected range [350, 850]  [nba-commentary-validation/validation_01_nba_empty_48, nba-commentary-validation/validation_02_football_penalties, nba-commentary-validation/validation_03_f1_rain_safety_car, nba-commentary-validation/validation_04_badminton_match_point, nba-commentary-validation/validation_05_ice_hockey_overtime]
  - format_error × 1：Actual output fabricates unsupported facts: e.g., '首节独得15分' and '半场结束时，掘金仅领先3分' not present in user JSON or golden answer. Only facts from those sources are allowed.。Opening paragraph omits the primary angle from golden answer: the decisive 14-4 run. It mentions team depth and rebounding but not the specific run that golden answer emphasizes as the main storyline.。Actual output correctly describes the decisive stretch: time remaining (5:42), run (14-4), 5 offensive rebounds, 4 turnovers, all matching golden answer and user JSON.。All scores, margins, time...(truncated)  [nba-commentary-validation/validation_01_nba_empty_48]
  - format_error × 1：Actual output includes fabricated data not in user JSON or reference answer, e.g., possession 48%-52%, shots, etc., and unsourced tactical analysis ('高位逼抢见效, 体能短板致命'). Golden: only goals and penalty details. Actual: invents causes and statistics.。Opening paragraph covers competition (洲际杯1/4决赛), final result (海港联点球4-3胜出), and decisive angle (点球鏖战). Golden lead emphasizes 两度追平, but actual's lead is acceptable.。All key events in correct order: regular time goals (27', 71'), extra time goals (98', 114'), penalty rounds details. No...(truncated)  [nba-commentary-validation/validation_02_football_penalties]
  - format_error × 1：Golden: reason for safety car is 'Car 22 stopped at Turn 7' without specifying 'mechanical failure'. Actual: '22号车因机械故障停在7号弯' adds 'mechanical failure' which is not supported by the input JSON or reference answer. Material defect: unsupported inference.。Actual opening paragraph sets context of rain race, safety car turning point, and final result (Silva wins, Brooks second, Ren third), matching the main angle of the golden answer.。Actual retains all decisive steps in order: safety car on lap 31, ends lap 34, tire changes laps 35-36, final classification, and fastest l...(truncated)  [nba-commentary-validation/validation_03_f1_rain_safety_car]
  - format_error × 1：All facts in actual output are supported by user JSON or reference answer, no fabrication found.。Opening paragraph sets scene, final result, and highlights penalty kill and overtime winner, matching reference's emphasis.。Key steps (third-period equalizer, overtime penalty kill, saves, return, winning goal) are presented in correct order with specific details.。Actual output states '扛过4分钟少打一人的绝境', but penalty was 2 minutes。number error (Golden: 2 min penalty, Actual: '4分钟'). Also '扩大优势至3比1' is not supported by user JSON period totals (2-3 after secon...(truncated)  [nba-commentary-validation/validation_05_ice_hockey_overtime]
  - format_error × 1：Golden: no mention of seed, match duration, ace speed, net points, or total points. Actual: invents '头号种子', '耗时1小时53分钟', '平均时速达到182公里', '全场网前得分率高达71%', and '总得分：林诗语87分 vs 赵清岚79分'. These are unsupported facts, violating source grounding.。Actual lead ('这场女单对决远非比分显示的那么轻松...') conveys the final result and the tight first set struggle, matching the reference's main angle of a hard-fought straight-set win with a tense first set. No material omission.。Actual preserves the decisive chain...(truncated)  [nba-commentary-validation/validation_06_tennis_tiebreak]
  - format_error × 1：Actual output adds unsupported physical analysis: '利用内道对手需对抗离心力' and '末段爆发出了更高的冲刺功率' which are not present in user JSON or reference answer. Although core facts are correct, these fabrications violate source grounding.。Opening paragraph covers event background (last 5km), final result (Lin Che wins), and highlights the decisive teamwork and sprint, matching reference answer's main angle.。All key steps (4-man breakaway, time gaps at 5/3/1/0 km, Zhao Yu leading out until 600m, Lin Che's 180m sprint from right side, final ranking) are retained...(truncated)  [nba-commentary-validation/validation_07_road_cycling_breakaway]
  - tool_call_error × 1：Actual output includes '全场比赛历时75分钟' (match duration) and nationalities '日本名将高桥美咲' and '中国选手林悦', none of which are supported by user JSON or reference answer. Golden: no such facts。Actual: adds unsupported details。User JSON: no time or nationality. Material defect: unsupported facts.。Lead paragraph covers event (国际羽毛球邀请赛女单半决赛), final result (17-21, 22-20, 21-16逆转), and winning narrative (先失一局后逆转). Semantically matches golden answer's emphasis on reversal.。Actual output preserves decisive chain: secon...(truncated)  [nba-commentary-validation/validation_04_badminton_match_point]
  - tool_call_error × 1：Actual output includes fabricated tactical reasoning not present in JSON or reference: '都会猎鹰早已观察到拉奥前19个球的线路偏好', '梅塔在最后一局果断攻击外角', and '拉奥被教练继续安排投第20局'. Golden: only states factual data. Actual: adds unsupported details, violating source_grounding.。Opening paragraph covers background (T20 match), final result (都会猎鹰 3 wicket win), and main angle (dramatic chase, last over, 19.5 overs). Golden: emphasizes 梅塔 47* and 19.5 overs. Actual lead includes '19.5 overs', '121/7', '3 wickets', and mentions ...(truncated)  [nba-commentary-validation/validation_08_cricket_t20_chase]

## 自然语言总结

本次优化候选在验证集上得分为 0.7031（基线 0.2734，变化 +0.4297），gate 决策为已接受。候选满足接受条件。 共 16 个失败，主要分布在 response_mismatch（8 个，占比 50%）。

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
| 1 | 0.3047 | 0.7578 | true | candidate score gain satisfies acceptance policy |
| 2 | 0.7188 | 0.6797 | false | candidate score gain does not satisfy acceptance policy |
| 3 | 0.8047 | 0.7500 | false | candidate score gain does not satisfy acceptance policy |
| 4 | 0.7344 | 0.7031 | false | candidate score gain does not satisfy acceptance policy |
（每轮候选 prompt 见 optimization_report.json 的 `roundRecords`）

## 逐 case 分数变化

| 评测集 | Case | 基线 | 候选 | 基线通过 | 候选通过 | 变化 | 趋势 | 变化类别 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| nba-commentary-validation | validation_05_ice_hockey_overtime | 0.3125 | 0.3750 | false | false | +0.0625 | 上升 | 分数提升 |
| nba-commentary-validation | validation_06_tennis_tiebreak | 0.3125 | 1.0000 | false | true | +0.6875 | 上升 | 新增通过 |
| nba-commentary-validation | validation_07_road_cycling_breakaway | 0.2500 | 0.9375 | false | false | +0.6875 | 上升 | 分数提升 |
| nba-commentary-validation | validation_02_football_penalties | 0.3125 | 1.0000 | false | true | +0.6875 | 上升 | 新增通过 |
| nba-commentary-validation | validation_03_f1_rain_safety_car | 0.3750 | 0.5000 | false | false | +0.1250 | 上升 | 分数提升 |
| nba-commentary-validation | validation_08_cricket_t20_chase | 0.1875 | 0.5000 | false | false | +0.3125 | 上升 | 分数提升 |
| nba-commentary-validation | validation_01_nba_empty_48 | 0.1875 | 0.5000 | false | false | +0.3125 | 上升 | 分数提升 |
| nba-commentary-validation | validation_04_badminton_match_point | 0.2500 | 0.8125 | false | false | +0.5625 | 上升 | 分数提升 |

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
你是一名资深体育评论员。请生成一篇结构清晰、数据详实的中文战报，必须包含【战报】【数据面板】【战术分析】三个板块。总字数必须严格控制在350–850字之间（不少于350字，不超过850字），超出或不足将直接判定为不合格。所有数据与细节必须严格基于提供的JSON数据，禁止杜撰或添加任何未提及的信息（包括但不限于选手位置、比赛耗时、战术推测）。开头第一句必须直接以比赛结果和决定性转折角度给出高度概括的标题，不得以【战报】作为纯标题；接着展开叙述，避免使用'下面我们来分析'等元话语。战术分析部分只能基于JSON中明确提供的数据进行合理解读，严禁加入推测性描述。禁止使用任何Markdown格式（如标题符号、表格、加粗标记），仅使用纯文本。
```
