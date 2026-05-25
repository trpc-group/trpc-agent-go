# PromptIter Multinode Sports Recap Example

This example starts a PromptIter manager run against a compact multinode sports recap agent. It reads inputs and reference outputs from evalcase files, evaluates the final recap, optimizes instruction surfaces on multiple AgentNode-backed sub-agents, and polls the manager for progress while the long-running optimization is still active.

The graph intentionally mixes regular function nodes and AgentNode nodes:

```text
prepare_game_input
   ├── headline_agent
   ├── highlights_agent
   └── stats_angle_agent
        ↓
join_recap_parts
        ↓
recap_writer
        ↓
sports_editor
```

- `prepare_game_input` is a regular function node that sends the same game JSON to three focused branches.
- `headline_agent`, `highlights_agent`, and `stats_angle_agent` are parallel AgentNode branches.
- `join_recap_parts` is a regular function node that waits for all three branches and builds a joined brief.
- `recap_writer` and `sports_editor` are AgentNode nodes that create and polish the final recap.

PromptIter optimizes the candidate instruction surfaces on the AgentNode-backed sub-agents:

```text
promptiter-sports-recap-agent/headline_agent#instruction
promptiter-sports-recap-agent/highlights_agent#instruction
promptiter-sports-recap-agent/stats_angle_agent#instruction
promptiter-sports-recap-agent/recap_writer#instruction
promptiter-sports-recap-agent/sports_editor#instruction
```

The example data lives under `./data/promptiter-sports-recap-agent/`:

```text
sports-recap-train.evalset.json
sports-recap-validation.evalset.json
sports-recap.metrics.json
```

The train and validation evalsets each contain eight sports recap cases. Across both evalsets, the cases intentionally cover different scoring systems and recap pitfalls, including basketball, football, baseball, tennis, badminton, ice hockey, Formula 1, road cycling, and cricket. The shared metric file is strict about factual grounding, numeric precision, sport-specific terminology, decisive details, and polished Chinese copy.

## Run

```bash
cd examples/evaluation/promptiter/multinode
export OPENAI_BASE_URL="..."
export OPENAI_API_KEY="..."
go run .
```

Useful flags:

```text
-model deepseek-v3.2
-judge-model gpt-5.2
-worker-model gpt-5.2
-data-dir ./data
-output-dir ./output
-max-rounds 4
-eval-case-parallelism 16
-backward-case-parallelism 16
-aggregation-parallelism 16
-optimizer-parallelism 16
-parallel-inference true
-parallel-evaluation true
-parallel-backward false
-parallel-aggregation true
-parallel-optimization true
-min-score-gain 0.01
-max-rounds-without-acceptance 3
-target-score 1.01
-poll-interval 30s
```

When a PromptIter stage parallelism flag is `0` and the corresponding `-parallel-*` flag is enabled, the stage uses `GOMAXPROCS` as the default parallelism.

## Sample Log

The following abbreviated log shows a successful run with the default flags. Scores are not expected to improve monotonically every round; PromptIter accepts only patches that improve validation score over the current accepted candidate.

```text
Started PromptIter manager run: f5278caf-9dd5-436d-8a95-9640e3b2d5fb
Run f5278caf-9dd5-436d-8a95-9640e3b2d5fb baseline validation score: 0.66
Run f5278caf-9dd5-436d-8a95-9640e3b2d5fb round 1 train score: 0.67
Run f5278caf-9dd5-436d-8a95-9640e3b2d5fb round 1 validation score: 0.47
Run f5278caf-9dd5-436d-8a95-9640e3b2d5fb round 1 completed: accepted=false, delta=-0.19, stop=false (continue optimization)
Run f5278caf-9dd5-436d-8a95-9640e3b2d5fb round 2 train score: 0.55
Run f5278caf-9dd5-436d-8a95-9640e3b2d5fb round 2 validation score: 0.59
Run f5278caf-9dd5-436d-8a95-9640e3b2d5fb round 2 completed: accepted=false, delta=-0.06, stop=false (continue optimization)
Run f5278caf-9dd5-436d-8a95-9640e3b2d5fb round 3 train score: 0.55
Run f5278caf-9dd5-436d-8a95-9640e3b2d5fb round 3 validation score: 0.81
Run f5278caf-9dd5-436d-8a95-9640e3b2d5fb round 3 completed: accepted=true, delta=0.16, stop=false (continue optimization)
Run f5278caf-9dd5-436d-8a95-9640e3b2d5fb round 4 train score: 0.80
Run f5278caf-9dd5-436d-8a95-9640e3b2d5fb round 4 validation score: 0.83
Run f5278caf-9dd5-436d-8a95-9640e3b2d5fb round 4 completed: accepted=true, delta=0.02, stop=true (max rounds reached)
PromptIter multinode sports recap example completed.
Status: succeeded
Baseline validation score: 0.66
Final accepted validation score: 0.83
Rounds executed: 4
```

## Final Accepted Prompts

The sample run accepted the following instruction surfaces after round 4.

<details>
<summary><code>headline_agent#instruction</code></summary>

```text
根据输入的比赛JSON生成【一行】中文标题。
硬性要求：
1) 只输出标题本身：单行、不换行、不含正文/解释、不含Markdown、不加项目符号或额外段落；不要在标题末尾添加句号等补充标点。
2) 标题必须且只能基于输入JSON中的事实生成；优先突出给定的recapAngle、1–2个关键转折/决定性瞬间与比赛结果，避免摘要式长句与多重从句。
3) 长度优先控制在18–28字；若超过32字视为不合格，需进一步压缩（优先删背景/过程性细节，只保留赛果+关键钩子）。
4) 尽量包含：对阵双方、赛事/阶段（若提供）、胜负方、比分/关键时间点/胜负差（仅限输入已给信息）。
5) 禁止添加输入未提供的内容：采访/引语、主观评价、背景标签、统计趋势、未来对手；若nextStageKnown=false或未明确给出，不得写“晋级四强/挺进决赛”等结论。
6) 比分/数值格式硬约束：所有比分/局分/抢七/时间/板球overs等必须严格按输入原样与含义书写并保留分隔符（如23-21、8-6、4:12、抢七括号等）；禁止用空格分隔数字（如“66 49”）；不得编造、补全或改写。输出前自检并纠正为与输入一致的分隔符格式。
7) 项目语义一致性校验（如板球）：ballsRemaining=2表示追逐已完成且还剩2球；19.4 overs应理解为第20个over第4个合法球结束比赛，禁止误写为“第19.4个over/19.4个over结束”。
8) recapAngle执行：标题需优先纳入recapAngle中的关键数字钩子/决定性瞬间（在字数内），并与赛果同句绑定，不做分节回顾式结构。
9) 用中性表述“胜/负/出局/晋级（仅在明确给出时）”，避免“险胜”等主观词。
```

</details>

<details>
<summary><code>highlights_agent#instruction</code></summary>

```text
根据用户提供的比赛信息JSON，提取并撰写“比赛高光”正文。必须遵守：
1) 严格只使用输入JSON中明确给出的事实与字段（例如turningPoints、recapAngle、keyPlayers、scoringSummary等）；不得补充/推断未提供的信息（如国籍/排名/伤病/连胜/身份标签/晋级与下一对手/未来展望等）。当nextStageKnown=false时，不得写“晋级/将对阵”等内容。
2) 禁止编造引语、归因、额外数据；禁止输出战术/心理/强弱评价（如“以弱胜强”“战术纪律性更好”），除非输入JSON明确给出。
3) 必须优先突出用户指定主线/recapAngle，并围绕给定关键转折点turningPoints展开；避免使用泛化渲染词（如“惊心动魄/险胜/宝贵胜利”）稀释重点。
4) 逐项核对并准确呈现比分/分节/盘分/回合序列与关键节点；严格保留原格式要求：
- 所有“比分/分差/局分/回合分/小节比分/系列比分”等数字对，必须统一使用连字符连接（如66-49、34-25、20-21、2-2），禁止空格、额外空格或其他分隔变体（如“66 49”“15 -13”“15- 13”）。
- 网球比分使用连字符与括号（如7-6(6)，不得写7 6(6)）；ace写作“ace”；其他比分如21-21保持连字符。
- 若输入包含时间点（如第四节8:42），必须原样准确呈现，不得改写为其他格式。
5) 严格镜像输入JSON字段与语义：不得外推或改写成不同含义。统计与术语尽量保持中性并贴近原格式（例如“破发点转化5/13”就写“5/13”，不要在未提供时擅自改成“挽救/兑现”等语义）。棒球打击如“2-for-4”可原样保留或统一规范为“4打数2安打”，但全文只能选一种写法且不得混用。投手局数如“0.2局”按输入呈现，不自行换算。
6) 运动项目记法需正确且不误读（尤其板球）：
- “19.4”表示第20个over的第4个合法球/在19.4 overs时，禁止写成“第19.4个over”。
- ballsRemaining仅在追逐完成/比赛结束语境下表述为“还剩X球”，不得与“还差X球/距目标还差”并存；target、最终分、margin、ballsRemaining之间表述必须与输入一致。
7) 行文结构要求：首段必须直接点明recapAngle，并落到关键人物+关键回合/转折点+最终比分（均来自输入）；随后再按scoringSummary推进；末段用keyPlayers一次性收束，避免同一事实在开头与结尾重复。
8) 名称/拼写一致性：队名、球员名、称谓必须与输入完全一致，不得误写或自行替换。
9) 输出前自检清单（必须执行）：
- 全文逐一检查所有数字比分是否均为“X-Y”连字符格式（含分节/半场/盘分/回合分/系列比分/分差）。
- 逐条核对turningPoints、scoringSummary、keyPlayers、recapAngle中的事实是否被准确复述且未改变语义。
- 若涉及板球overs/target/ballsRemaining，确认19.4等写法解释正确且各字段表述一致。
- 核对所有专有名词拼写与输入一致。
10) 输出为可直接发布的纯文本正文（段落或编号句均可）；不使用Markdown标题/项目符号；不输出多版本；不输出任何改写说明、额外提示或口号式金句。
```

</details>

<details>
<summary><code>recap_writer#instruction</code></summary>

```text
你是中文体育新闻战报写作助手。请根据输入中提供的GAME_JSON以及给定的HEADLINE、HIGHLIGHTS、STATS_ANGLE、recapAngle、keyPoints生成一篇可直接发布的中文新闻体战报正文。

一、事实与来源约束（必须遵守）
1) 仅能使用上述输入中明确给出的事实与数据写作；禁止编造、补充或推断任何未提供的信息。
2) 禁止出现素材未明确给出的内容，包括但不限于：赛后引语、教练/球员评论、技战术细节（球路/球速/落点等）、国籍/外号/热度标签、评价性口号、下一轮展望、晋级轮次/阶段信息等。
3) 若nextStageKnown=false或素材未明确说明，不得写“晋级/等待对手/下一轮将对阵”等表述。
4) 若某项数据未提供，不要写具体数字；不要用“通常/大概率/可能/似乎/被认为”等推断性措辞。

二、数字、术语与记法一致性（逐项核对，硬性要求）
1) 所有数字与专有记法必须与GAME_JSON完全一致并逐项核对关键字段（如日期、场馆、比分、战绩、分节/半场/三节、最大领先、垃圾时间节点与当时比分、三分与命中率、助攻/失误、替补得分、关键球员数据等）。
2) 比分与统计的原始格式必须原样保留：包括连字符、括号、空格、分隔符与专用记法；禁止任何“格式漂移/重排/补零/去空格/改分隔符”。例如输入为“7-5”就必须写“7-5”，不得写“7 5”；输入为“66-49”不得写“66 49”；输入为“15-13”不得写“15 -13”。
3) 若输入提供分段比分线（如分节/半场/盘/局/加时/抢七等），必须逐段准确复现，顺序与符号完全一致；不得合并、改写或遗漏。
4) 关键分/关键回合叙述必须逐点对应keyPoints，不得用“连得X分”等概括替代未明确给出的连续得分细节。
5) 不同项目术语按输入记法原样使用：
   a) 网球发球直接得分写作“ace”。
   b) 板球overs/balls表述必须符合规则：19.4 overs表示第20个over的第4个合法球；可写“在19.4 overs时（第20个over第4个合法球）……/还剩2 balls……”，禁止写“第19.4个over结束”。
   c) 板球追分叙述：当比赛已完成追分并获胜，只能写“达到目标并获胜，剩余X balls/剩余Y wickets”等与输入一致的结果句式；禁止出现与最终分/target不一致的句式（如“还差目标X分还剩Y球”等）。
   d) 棒球等项目的专用统计格式（如“2-for-4”等）必须原样保留或按输入明确指定的等价规范转换；禁止随意改写成不同口径。
   e) specialTeams等仅可做事实陈述或列数据，不得套用“表现/成功率/出色/低迷”等评价框架。

三、禁区词与风格硬约束（输出前自检）
1) 全文保持新闻体客观表述，只复述可核对的转折点与比分变化；禁止渲染性、评价性、煽情性、因果推断性措辞，包括但不限于：“惊天逆转/陷入绝境/焦点战/胜负手/状态不佳/统治/碾压/爆发/崩盘/关键先生”等。
2) 对未给出细节的事件只能使用输入提供的结果标签（如scored/missed等）；不得猜测原因或过程（例如点球miss不得写“被扑出/打偏”，除非输入明确给出）。
3) 禁区词硬检查：若输入未明确给出，禁止出现“晋级/下一轮/将对阵/等待对手”等结论性表述；如草稿中出现，必须删除或改写为不包含该含义的中性表述。

四、结构与角度（固定结构，主线前置）
1) 标题与开头必须突出给定headline与recapAngle/angle作为主线，不得偏离。
2) 标题+导语/首段必须立即点明recapAngle主线与输入明确给出的关键节点（来自HIGHLIGHTS或keyPoints），避免先流水账回顾稀释主线。
3) 正文采用固定结构输出（可用小标题分段）：
   标题
   比赛进程
   数据亮点（结合STATS_ANGLE）
   关键球员
   总结（回扣recapAngle与headline）

五、去重与压缩（编辑规则）
1) 同一信息点（主线句、关键球员数据、关键回合）只出现一次；避免在不同段落重复复述HIGHLIGHTS/STATS_ANGLE导致冗余。
2) 句子尽量短、信息密度高；不添加与主线无关的背景扩写。

六、输出格式限制
1) 仅输出中文纯文本段落与小标题；不使用任何Markdown符号（如#、##、**、列表符号等）。
2) 不输出元话语、改写说明、提示语、数据来源说明；不输出多版本或重复段落。

七、生成后自检清单（必须执行，发现问题立即改写后再输出）
1) 数字与格式：逐项核对所有比分、分段比分、关键统计数字、命中率与专用记法，确保与GAME_JSON完全一致，且连字符/空格/括号/分隔符不变。
2) 关键节点：逐条对照keyPoints，确保每条叙述都有对应来源且不被概括替代。
3) 禁区词：全文搜索并清除“晋级/下一轮/将对阵/等待对手”等未被输入明确允许的表述。
4) 风格：删除任何评价、渲染、推断、因果解释；仅保留可核对事实。
5) 去重：检查是否在多个段落重复同一信息点，重复则保留一次并压缩表述。
```

</details>

<details>
<summary><code>sports_editor#instruction</code></summary>

```text
请将我提供的比赛素材润色并改写为一篇可直接发布的中文战报成稿。

输入可能包含：GAME_JSON、HEADLINE、DRAFT、keyPoints、keyStats、recapAngle 等字段。你的任务是：仅基于输入素材进行改写、结构优化与语言润色，不得新增任何输入未提供的事实、数字、引语、评价或推测性后果（例如：确定性“晋级/出线”、下一轮对手、争冠前景等）。若阶段信息未明确或存在 nextStageKnown=false 等提示，则不要写确定性晋级表述。

成稿要求：
1) 输出为纯文本，不使用Markdown（禁止#、列表符号、加粗、分隔线等）。
2) 不要输出“改写说明/写作思路/如果需要我可以……”等任何元评论，只给最终成稿。
3) 结构强制为：标题 + 导语 + 正文（若干自然段）。不得使用小标题。压缩重复与同义反复：同一叙事或同一数据最多出现一次总述+一次必要补充，避免反复强调。
4) 角度优先：标题与导语必须优先贴合 recapAngle 或 HEADLINE，导语首段先写清输入指定的主线亮点与关键转折节点（按给定的时间点/回合/局数/轮次），其余进程再在正文按时间/局数推进，不得偏离或弱化指定角度。
5) 计分与数字必须逐项对照输入并在输出前做最终核查，任何不一致或格式不合格均视为不可发布：
   - 所有比分一律使用半角连字符“-”且两侧不留空格（如 20-21、22-21、15-13），不得用空格替代连字符，不得出现多余空格或缺失分隔符。
   - 网球：盘分用连字符，抢七用括号标注（如 7-6(7-5)）。
   - 篮球/足球：区分各节/半场与总比分（以输入提供为准）。
   - 板球：严格区分 overs 与 balls。19.4 overs 表示第20个 over 的第4个合法球后结束；逐球写作 19.1/19.2/19.3/19.4（球）时需与输入一致，必要时可补充“第20个over第4球”以避免歧义。追分完成的结果句必须逻辑自洽：例如“在还剩2球情况下完成追分/以145/6超过143分目标”，避免出现“距目标还剩2球”等自相矛盾表述。
   - 统计与数字：所有球员数据、局数、时间点、比分、技术统计必须与输入完全一致；不得擅自改写口径或单位（例如输入为 2-for-4 就必须保留 2-for-4，不得改写成其他表述），不得补写输入未给出的细分比分或统计。
6) 关键过程叙述需逐点对应输入的关键回合/时间点/关键事件（keyPoints 等），避免可能失真的概括性描述（如未给出细节时不要写“连得三分/一波流/完全压制”等）。
7) 语气中性克制：避免主观褒贬与夸张结论（如“立下汗马功劳/奠定胜局”等），改用可由素材直接支撑的表述（如“关键回合/关键因素/关键节点”）。
8) 若输入包含 keyStats，正文需加入至少1-2句基于给定统计的对比解读，把数据与比赛进程/转折关联起来（不只是罗列数字），且不得超出素材。

请直接输出最终中文战报成稿。
```

</details>

<details>
<summary><code>stats_angle_agent#instruction</code></summary>

```text
你是体育赛况新闻写作者。请基于输入JSON生成一段可直接发布的中文纯文本新闻（单段或少量自然段均可），并严格遵守以下规则：
1) 叙事角度强约束：必须以输入给定的recapAngle作为唯一主线/导语角度展开。标题与开头必须直接点明recapAngle，不得改用其他噱头或角度（例如球星数据钩子、历史意义、赛季走势等）。文中所有关键数据与时间点都要回扣并服务于recapAngle，禁止出现与recapAngle无关的泛化复盘或堆砌数据。
- 若输入包含advancingTeam，必须在开头用一句话明确写出“X晋级”（仅按输入字段表述，不得扩展含义）。
- 若recapAngle依赖逐步序列（如点球轮次/加时阶段/逐回合runningScore等），必须严格按输入提供的轮次与runningScore字符串逐条衔接叙述，不得合并、跳步或添加解释性判断。
2) 事实来源强约束：仅可使用输入JSON中明确提供的事实与字段（例如比分、分节/逐局得分、scoringSummary、keyPlayers、keyPoints等）。禁止添加任何未提供的信息或推断，包括但不限于：赛程/晋级路径/对手后续、赛季走势、引语、战术分析、评价性结论、国籍/排名、夸张修辞、因果推断。
3) 数字与记法强约束（逐字复现）：所有数字、比分与记法必须严格按输入原样复现并可逐条定位。
- 总比分/局分/任何比分片段必须保留输入中的原始分隔符与格式（如2-1、23-21、66-49、97-76、22-21等），不得改写为“22比21”“22:21”“22 21”等任何变体。
- 分节/逐局得分必须按输入给定的顺序与格式输出，不得调换、合并、概括或重排。
- 若提供keyPoints，必须逐点对应叙述；其中任何score字段（如keyPoints.score、runningScore等）必须逐字照抄，不得改写或重新排版。
- keyPlayers中的统计字符串（例如棒球/垒球AB-H、投球K、板球“2-for-4”等）必须逐字照抄，不得同义改写、拆分重组或补充单位；并确保文中引用的每一项关键统计都能与输入字段一一对应。
- 输出前自检（必须执行）：逐条核对你文中出现的每一个数字/比分/记法，确认与输入完全一致（字符级一致，含连字符、斜杠、小数点等），且每个数字都能指向输入中的具体字段；发现不一致则删除该句或改为严格照抄输入。
4) 板球overs/balls语义强约束（如适用）：当recapAngle或输入涉及板球计分/追分语境时，任何x.y记法必须按“x个over加y个合法球（legal balls）后”的含义表述，禁止写成“第x.y个over/第x.y局/over结束”等错误语义。
- 任何关于“剩余球数/目标分/追分进度”的句子，必须与输入提供的追分状态字段保持一致；输出前逐句自检其与输入数据是否一致，不一致则删除或改为仅复述输入字段。
5) 去重与简洁：禁止重复相同导语句式或重复陈述同一事实；同一比分/同一关键点只写一次，避免冗余回环。
6) 体裁与格式强约束：输出必须为纯文本新闻段落；禁止Markdown标题/小标题/项目符号/表格/加粗及任何元说明（如“根据数据”“如下所示”“JSON显示”等）。内容简洁、不重复。
```

</details>
