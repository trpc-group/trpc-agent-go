# PromptIter 回归优化报告

- 应用: promptiter-nba-commentary-app
- 模型: deepseek-v4-flash
- 优化轮次: 4
- 耗时: 833974 毫秒
- 基线分数: 0.3125
- 候选分数: 0.8750
- 分数变化: +0.5625
- 是否接受: true
- 门禁理由: accepted: validation gain met, no regression or new hard fails

## 基线失败归因

- 归因方法: rule
- 总 case 数: 8，失败 case 数: 8
- 按类别分布：response_mismatch=7 format_error=8 
- 洞察（rule）：共 15 个失败，主要分布在 format_error（8 个，占比 53%）。
- 失败模式：
  - format_error: 8 个 (53%)（例如：Actual output invents unsubstantiated mention of Durant: '杜兰特（本场未列出但应有贡献）', which is not in user JSON or reference answer. Also uses '丹佛高原' not in sources. Material defect. Opening paragraph includes result (118-111), Booker's 48 points, and the decisive 14-4 run, matching reference answer's main angle. Correctly retains decisive sequence: 5:42 remaining, 14-4 run, 4 Suns turnovers, 5 Nuggets offensive rebounds, matching reference. Data panel misinterprets playersInDoubleFigures as double-doubles ('两双人数'), showing 1 for Denver instead of 6. Golden ...(truncated)）
  - response_mismatch: 7 个 (47%)（例如：final response mismatch: text mismatch: length mismatch: actual length 1021 is greater than max 850, expected range [350, 850]）
- 聚类（去重）：
  - response_mismatch × 7：final response mismatch: text mismatch: length mismatch: actual length 1021 is greater than max 850, expected range [350, 850]  [nba-commentary-validation/validation_01_nba_empty_48, nba-commentary-validation/validation_02_football_penalties, nba-commentary-validation/validation_03_f1_rain_safety_car, nba-commentary-validation/validation_05_ice_hockey_overtime, nba-commentary-validation/validation_06_tennis_tiebreak]
  - format_error × 1：Actual output invents unsubstantiated mention of Durant: '杜兰特（本场未列出但应有贡献）', which is not in user JSON or reference answer. Also uses '丹佛高原' not in sources. Material defect.。Opening paragraph includes result (118-111), Booker's 48 points, and the decisive 14-4 run, matching reference answer's main angle.。Correctly retains decisive sequence: 5:42 remaining, 14-4 run, 4 Suns turnovers, 5 Nuggets offensive rebounds, matching reference.。Data panel misinterprets playersInDoubleFigures as double-doubles ('两双人数'), showing 1 for Denver instead of 6. G...(truncated)  [nba-commentary-validation/validation_01_nba_empty_48]
  - format_error × 1：实际战报的战术分析部分编造了未在JSON或参考战报中出现的战术意图（如'高位逼抢'、'支点作用'），违反source_grounding要求。导语交代了赛事背景（洲际杯1/4决赛）、最终结果（海港联点球晋级），并突出了'两度追平后点球晋级'的主角度，与参考战报一致。实际战报按正确顺序描述了常规时间、加时赛进球和点球大战每个轮次，完整保留了参考战报中的决定性链条（两次追平、点球第3/5轮罚失）。实际战报中'总比分6-5'与JSON中常规时间1-1、加时2-2（�...(truncated)  [nba-commentary-validation/validation_02_football_penalties]
  - format_error × 1：Golden: Silva wins after lap35 tire change。no mention of '第三胜' or '技术故障'. Actual: states '个人赛季第三胜' and '22号赛车因技术故障停在7号弯' which are not supported by user JSON or reference. Material defect: unsupported facts.。Reference lead emphasizes 'Silva第35圈换胎后夺冠' as decisive angle. Actual lead focuses on rain and general strategy, missing explicit mention of lap35 tire change as the main decisive moment. Also includes unsupported '第三胜'.。Actual correctly preserves the decisive chain: safety car laps31-34, pit window laps34-36, tir...(truncated)  [nba-commentary-validation/validation_03_f1_rain_safety_car]
  - format_error × 1：Actual output adds '中国选手' and '日本名将' (nationalities) and '耗时81分钟' (match duration) which are not present in user JSON or reference answer, thus inventing facts.。Lead paragraph covers event, final result, and reversal narrative, matching reference's main storyline.。Actual output correctly retains the decisive sequence: 19-20 deficit, net front save, consecutive 3 points to win second set, as in reference.。Data panel misrepresents net front margin: '失误优势-5' is incorrect (should be +5), and '合计+9' contradicts the negative sign. This is a material numeric ...(truncated)  [nba-commentary-validation/validation_04_badminton_match_point]
  - format_error × 1：实际战报中少防多成功率写为2/2（含加时），但用户JSON中keyPlays仅有一次加时犯规，且参考未提及其他少防多，导致数据自相矛盾且缺乏依据，属于编造事实。正文开头段（第一段）未交代参考重点突出的“少防多”主线，仅概括逆转绝杀，遗漏关键要素。Golden开头先提守住少防多再绝杀，Actual第一段未包含。实际战报按正确顺序包含了决定性链条：第三节扳平、加时犯规、少防多扑救、回场、绝杀，与参考一致，无遗漏或重排。犯规次数写1但少防�...(truncated)  [nba-commentary-validation/validation_05_ice_hockey_overtime]
  - format_error × 1：实际输出包含用户JSON和参考战报中未提供的数据，如“总得分92-81”、“制胜分28-21”、“非受迫性失误18-22”、“二发得分率52%和45%”。Golden和User均未涉及这些数字，属于编造。开头段交代了WTA女单焦点战背景、最终结果7-6(10)/6-3，并突出首盘抢七12-10和三救盘点的主线，与参考战报重点一致。按正确顺序保留了决定性链条：首盘5-6发球局挽救2盘点→抢七8-9挽救第3盘点→12-10拿下→第二盘两次破发、赵清岚一次回破→6-3。无遗漏或顺序错误。所有核�...(truncated)  [nba-commentary-validation/validation_06_tennis_tiebreak]
  - format_error × 1：实际输出所有事实均来自用户 JSON 和参考战报，未编造信息。开头段交代了赛事背景、最终结果和赵屿带冲的主线，与参考战报一致。正确保留了突围、时间差变化、赵屿退领骑、林澈冲刺的决定性顺序。所有数字（距离、时间差、排名、差距）均与用户 JSON 和参考战报一致。标题融合了赛果和战术关键角度（带冲），效果接近参考战报。数据面板结合战术分析，解释了赵屿领骑对胜利的作用，达到解读层次。内容密度、叙述顺序和专业风格与参考战...(truncated)  [nba-commentary-validation/validation_07_road_cycling_breakaway]
  - format_error × 1：Actual output invents a highest score of 28 runs for the first batting team, which is not present in user JSON or reference answer. Also adds unsupported tactical analysis (e.g., '窒息式投球' and '守底反击') and details about wide balls not in reference. Golden: no such data。User JSON: no highest scorer for first team. Actual: '全场最高得分仅为28分' and extensive tactical commentary.。First paragraph does not state the winner or final result explicitly。it only says '直到最后一刻才分出胜负'. Golden: first line states '都会猎鹰3 wickets险胜海港国王'. A...(truncated)  [nba-commentary-validation/validation_08_cricket_t20_chase]

## 自然语言总结

本次优化候选在验证集上得分为 0.8750（基线 0.3125，变化 +0.5625），gate 决策为已接受。候选满足接受条件。 共 15 个失败，主要分布在 format_error（8 个，占比 53%）。

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
| 1 | 0.3359 | 0.7266 | true | candidate score gain satisfies acceptance policy |
| 2 | 0.8828 | 0.8516 | true | candidate score gain satisfies acceptance policy |
| 3 | 0.8984 | 0.7578 | false | candidate score gain does not satisfy acceptance policy |
| 4 | 0.8672 | 0.8750 | true | candidate score gain satisfies acceptance policy |
（每轮候选 prompt 见 optimization_report.json 的 `roundRecords`）

## 逐 case 分数变化

| 评测集 | Case | 基线 | 候选 | 变化 | 趋势 |
| --- | --- | --- | --- | --- | --- |
| nba-commentary-validation | validation_03_f1_rain_safety_car | 0.2500 | 1.0000 | +0.7500 | 上升 |
| nba-commentary-validation | validation_08_cricket_t20_chase | 0.1250 | 0.8750 | +0.7500 | 上升 |
| nba-commentary-validation | validation_01_nba_empty_48 | 0.1250 | 1.0000 | +0.8750 | 上升 |
| nba-commentary-validation | validation_02_football_penalties | 0.2500 | 0.3750 | +0.1250 | 上升 |
| nba-commentary-validation | validation_04_badminton_match_point | 0.7500 | 0.8750 | +0.1250 | 上升 |
| nba-commentary-validation | validation_05_ice_hockey_overtime | 0.1875 | 1.0000 | +0.8125 | 上升 |
| nba-commentary-validation | validation_06_tennis_tiebreak | 0.3750 | 0.8750 | +0.5000 | 上升 |
| nba-commentary-validation | validation_07_road_cycling_breakaway | 0.4375 | 1.0000 | +0.5625 | 上升 |

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
- Fake 模式：false（场景=happy）

## 候选 Prompt

```text
你是一名资深体育评论员。根据提供的用户JSON和参考战报，生成一篇纯文本（禁止任何Markdown格式）的中文战报。必须以一个独立的新闻标题开头（非“【战报】”标题），标题需概括赛果和决定性角度，必须包含关键数字（如比分、具体回合/时间等）。正文必须包含【战报】、【数据面板】、【战术分析】三个部分，各部分以纯文字区分，不要使用任何格式化标记。所有事实必须严格来源于用户JSON和参考战报中的数据，包括球员姓名、数据统计、事件描述。禁止添加任何未明确出现的数据、事件、地点、时间或球员动作。注意数字和表述的准确性，例如球员数据统计的次序（命中数/出手数）必须与输入一致，不得颠倒。术语必须规范，如使用“over”而非“球”，使用“回合”等标准体育术语。正文必须完整包含所有关键事件步骤，不得遗漏（如决胜局中的每一球详情）。总字数必须严格控制在350-850字之间（含标题和所有部分）。若超过850字，必须精简至850字以内，优先删除非关键细节。确保战报内容准确、简洁，符合专业体育新闻报道风格。【战报】部分开头需直接点明赛果和决定性角度（如点球大战、最后时刻绝杀等），避免空泛描述。
```
