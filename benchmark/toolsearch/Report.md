# 报告

## 本报告要回答的问题

在多轮对话场景下（本次 evalset 为 3 个 case、共 27 轮 invocation）：

1. 使用 Tool Search 能节约多少 token？
2. LLM Search vs Knowledge Search：哪种策略更优？在什么场景？
3. 随着对话轮数增加 Token 消耗量如何变化？
4. 影响 Tool Search 的因素有哪些？
5. Tool Search 会带来多少端到端耗时增量？

## 实验设定

### 工具库

#### 规模

| 规模类型 | 工具数量 |
| --- | --- |
| 小规模 | 10-20 个工具 |
| 中规模 | 50-100 个工具 |
| 大规模 | 500+ 个工具 |

本报告测试的是 **中规模工具库**：基于 Go 标准库 `math` 生成的 `mathtools`，工具声明来自 `trpc-agent-go-impl/mathtools/schemas.json`，共 **67** 个函数工具。

### Tool Search 设置

本次对比三种模式（与 `benchmark/toolsearch/trpc-agent-go-impl/README.md` 一致）：

- `none`：不启用 Tool Search，所有工具直接提供给主模型（baseline）
- `llm`：使用 LLM Search 从工具列表中选择 Top-K 工具
- `knowledge`：使用 Knowledge Search（LLM 进行 query rewrite + embedding + vector search）选择 Top-K 工具

共同参数（来自三份 `*.summary.json` 的 `config`）：

| parameter name | parameter value |
| --- | --- |
| AppName | `toolsearch-benchmark` |
| EvalSetId | `toolsearch-mathtools-multiturn` |
| Chat Model | `deepseek-v3.2` |
| Embedding Model | `text-embedding-3-small`（仅 `knowledge` 会实际用到） |
| MaxTools | 3 |
| NumRuns | 1 |

#### LLM Search

| parameter name | parameter value |
| --- | --- |
| SystemPrompt | Your goal is to select the most relevant tools for answering the user's query.<br><br>IMPORTANT: List the tool names in order of relevance, with the most relevant first.<br>If you exceed the maximum number of tools, only the first {MaxTools} will be used.<br><br>Available tools:<br>- {ToolName-1}: {ToolDescription-1}<br>- {ToolName-2}: {ToolDescription-2}<br>......<br>- {ToolName-n}: {ToolDescription-n} |
| Chat Model | deepseek v3.2, 使用[对话补全（chat/completions） Request](https://api-docs.deepseek.com/zh-cn/api/create-chat-completion) 里面的默认参数 |
| MaxTools | 3 |

#### Knowledge Search

| parameter name | parameter value |
| --- | --- |
| SystemPrompt | Your goal is to identify the most relevant tools for answering the user's query.<br>Provide a natural-language description of the kind of tool needed (e.g., 'weather information', 'currency conversion', 'stock prices'). |
| Chat Model | deepseek v3.2, 使用[对话补全（chat/completions） Request](https://api-docs.deepseek.com/zh-cn/api/create-chat-completion) 里面的默认参数 |
| Embedding Model |  "text-embedding-3-small", Dimensions = 1536, EncodingFormat = "float"|
| MaxTools | 3 |

### 主 Agent Chat Model 的设置

| Chat Model | deepseek v3.2, 使用[对话补全（chat/completions） Request](https://api-docs.deepseek.com/zh-cn/api/create-chat-completion) 里面的默认参数 |

> 注意：运行脚本中需要配置 `OPENAI_API_KEY` / `OPENAI_BASE_URL` 等环境变量，但报告中不记录任何密钥信息。

### 多轮对话 user message 的设置

评估集位于 `data/toolsearch-benchmark/toolsearch-mathtools-multiturn.evalset.json`：

- 共 **3 个 eval case**：`log-exp` / `trig-hyperbolic` / `pow-root`
- 每个 case **9 轮对话**（总计 **27 轮**）
- 每轮的期望工具由 evalset 内 `tools[].name` 给出（部分轮允许多个期望工具，如 `math_Log1p|math_Log`）

### 评价指标

指标包括：

- **正确性/达标**：`tool_trajectory_avg_score`（阈值为 1）。本次三种模式均为 `overallStatus: passed`。
- **Token 消耗**：区分 `chat`（主 Agent 对话）与 `toolsearch`（检索阶段），并汇总为 `total`。
- **耗时**：`wallTimeMs`（整体端到端）以及按轮统计的 `durationMs`（在 `*.summary.json` 的 turns 中）。

Token 汇总关系：

- Total Token = Tool Search Tokens + Other(Chat) Tokens

同时需要注意真实业务中 Prompt Caching 的影响：

- Tool Search 会引入额外步骤（LLM 选工具 / query rewrite + embedding + vector search），因此端到端耗时往往会增加。
- Tool Search 会让每轮传给主模型的工具列表变化，从而可能降低 Prompt Caching 命中率（例如 cached tokens），影响整体成本评估。

## 结论（基于本次 Token/耗时/调用次数测试数据）

### 总览

> 统计口径：27 轮（3 case × 9 turns），`wallTimeMs` 为整次运行总 wall time。

| Case Name | Total Tokens | vs baseline（Total） | Prompt Tokens | vs baseline（Prompt） | Completion Tokens | vs baseline（Completion） | Total Wall Time | vs baseline（Time） | Avg Time/Turn | Tool Search Tokens | Tool Search 占比 |
| --- | ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:|
| without tool search (`none`) | 620780 | +0 (0.0%) | 618772 | +0 (0.0%) | 2008 | +0 (0.0%) | 186.640s | +0.000s (0.0%) | 6.913s | 0 | 0.0% |
| llm search (`llm`) | 197902 | -422878 (-68.1%) | 194558 | -424214 (-68.6%) | 3344 | +1336 (+66.5%) | 241.855s | +55.215s (+29.6%) | 8.958s | 120719 | 61.0% |
| knowledge search (`knowledge`) | 96252 | -524528 (-84.5%) | 91609 | -527163 (-85.2%) | 4643 | +2635 (+131.2%) | 453.656s | +267.016s (+143.0%) | 16.802s | 11215 | 11.7% |

### 1) 使用 Tool Search 能节约多少 token？

#### 不同模块的 Total Tokens 消耗量

| Case Name | Tool Search Token（Chat+Embedding） | Other(Chat) Token（Total） | Total Token |
| --- | ---:| ---:| ---:|
| without tool search | 0 | 620780 | 620780 |
| knowledge search | 11215 | 85037 | 96252 |
| llm search | 120719 | 77183 | 197902 |

#### 不同 case 的 Prompt/Completion/Total 消耗量

| Case Name | Prompt Tokens | Completion Tokens | Total Tokens |
| --- | ---:| ---:| ---:|
| without tool search | 618772 | 2008 | 620780 |
| llm search | 194558 | 3344 | 197902 |
| knowledge search | 91609 | 4643 | 96252 |

#### Other(Chat) 明细汇总（Prompt/Completion/Total）

| Case Name | Prompt Tokens | Completion Tokens | Total Tokens |
| --- | ---:| ---:| ---:|
| without tool search | 618772 | 2008 | 620780 |
| llm search | 75068 | 2115 | 77183 |
| knowledge search | 82648 | 2389 | 85037 |

#### Tool Search 明细汇总（Prompt/Completion/Total）

| Case Name | Prompt Tokens | Completion Tokens | Total Tokens |
| --- | ---:| ---:| ---:|
| without tool search | 0 | 0 | 0 |
| llm search | 119490 | 1229 | 120719 |
| knowledge search | 8961 | 2254 | 11215 |

结论（本次 67 工具、27 轮对话）：

- **Knowledge Search 的总 token 最低**：96252，相对 baseline **节约 524528（约 84.5%）**。
- **LLM Search 次之**：197902，相对 baseline **节约 422878（约 68.1%）**。
- Tool Search 的收益主要来自 **显著降低主 Agent 的 Prompt Tokens**（不需要把全部工具声明反复塞入每轮 prompt）；代价是 **Tool Search 阶段自身 token** 与 **Completion Tokens** 会增加。

### 2) LLM Search vs Knowledge Search：哪种策略更优？在什么场景？原因是什么？

在本次实验数据里（67 个工具、中规模），按 **Total Tokens 越低越优**：

- **Knowledge Search 更优**：96252
- **LLM Search 次之**：197902

原因分析（结合成本结构）：

- **Knowledge Search 更省**：Tool Search 阶段 token 仅 11215（占总量 11.7%），整体成本主要由主对话贡献；其检索靠向量召回，避免每轮把工具列表完整喂给 LLM。
- **LLM Search 更“贵”**：Tool Search 阶段 token 高达 120719（占总量 61.0%），说明“用 LLM 在工具声明上做选择”本身就消耗大量 prompt/completion；当工具描述较长或工具数较多时，这部分开销会很显著。

场景建议：

- **更推荐 Knowledge Search**：工具数量达到中/大规模、工具描述较长、对 token 成本敏感时。
- **LLM Search 适合**：工具库较小、或需要 LLM 对工具语义做更强判断、或向量检索链路不可用/不便维护时（但要注意 Tool Search 阶段本身的 token 开销）。

### 3) 随着对话轮数增加 Token 消耗量如何变化？

总体规律：随着轮数增长，主对话需要携带的历史上下文增多，因此每轮 token 往往会上升；不同策略主要差在“每轮工具列表带来的基线成本”。

下面展示三个 case 的 **每轮总 token（chat + toolsearch）** 变化（单位：tokens）。

#### Case: log-exp（9 turns）

| Turn | without total | knowledge total | llm total |
| --- | ---:| ---:| ---:|
| 1 | 21911 | 5137 | 6263 |
| 2 | 22248 | 2350 | 6636 |
| 3 | 22458 | 2702 | 6818 |
| 4 | 22729 | 3337 | 7177 |
| 5 | 23018 | 3465 | 7498 |
| 6 | 23246 | 3709 | 7728 |
| 7 | 23484 | 3747 | 7970 |
| 8 | 23708 | 3978 | 8167 |
| 9 | 23942 | 4545 | 8406 |

#### Case: trig-hyperbolic（9 turns）

| Turn | without total | knowledge total | llm total |
| --- | ---:| ---:| ---:|
| 1 | 21948 | 2056 | 5995 |
| 2 | 22291 | 2309 | 6637 |
| 3 | 22555 | 2519 | 6902 |
| 4 | 22890 | 2931 | 7254 |
| 5 | 23183 | 3087 | 7517 |
| 6 | 23427 | 3253 | 7659 |
| 7 | 23671 | 3485 | 7889 |
| 8 | 23914 | 3790 | 7882 |
| 9 | 24172 | 3998 | 8474 |

#### Case: pow-root（9 turns）

| Turn | without total | knowledge total | llm total |
| --- | ---:| ---:| ---:|
| 1 | 21911 | 1963 | 6319 |
| 2 | 22216 | 2340 | 6597 |
| 3 | 22436 | 2614 | 6862 |
| 4 | 22698 | 6640 | 7101 |
| 5 | 22914 | 3879 | 7053 |
| 6 | 23136 | 4235 | 7604 |
| 7 | 23342 | 4637 | 7497 |
| 8 | 23564 | 4643 | 8041 |
| 9 | 23768 | 4903 | 7956 |

趋势总结：

- **baseline（without）每轮都很高**：该模式每轮会携带完整工具列表，导致单轮 token 在 2.2 万左右并随轮数增长。
- **Knowledge Search 每轮最低（但首轮可能更高）**：大多数轮在 2k~5k 区间；`log-exp` 第 1 轮达到 5137，体现了初始化/预热成本（例如工具向量入库、首次 embedding 等）可能集中发生在首轮。
- **LLM Search 介于两者之间**：主对话 token 降得很低，但 Tool Search 阶段 token 很高，因此总量仍显著高于 Knowledge Search。

### 4) 影响 Tool Search 的因素有哪些？

对 Token/耗时最关键的因素通常包括：

- **工具库规模与工具描述长度**：规模越大、描述越长，baseline 的 prompt 越容易爆炸；LLM Search 的选工具 prompt 成本也会更敏感。
- **MaxTools / TopK**：召回工具越多，主对话 prompt 越大；召回越少可能影响正确率（本次三种模式均通过阈值）。
- **搜索阶段的提示词与输出格式**：越啰嗦、越结构化（例如要求排序+解释），Tool Search completion 越高。
- **多轮上下文长度**：对话越长，主对话与搜索阶段都可能增长（尤其在需要 query rewrite 的策略里）。
- **工具歧义/同质化**：同类工具越多，检索越难，可能需要更多 token 或更高 TopK 才能稳定命中。

### 5) 耗时分析（端到端）

| Case Name | Total Wall Time | Avg Time per Turn | vs baseline（Total） |
| --- | ---:| ---:| ---:|
| without tool search | 186.640s | 6.913s | +0.000s (0.0%) |
| llm search | 241.855s | 8.958s | +55.215s (+29.6%) |
| knowledge search | 453.656s | 16.802s | +267.016s (+143.0%) |

结论：Tool Search 在本次测试里带来了明显的端到端耗时增量。

- `llm` 的增量约 **+29.6%**，符合“每轮增加一次选工具调用”的预期。
- `knowledge` 的增量约 **+143.0%**，从逐轮数据看首轮（例如 `log-exp-01`）出现了明显的耗时峰值（45s 级别），通常与首次 embedding/向量写入/缓存未命中等初始化步骤相关；因此在评估 knowledge 策略时，需要区分“首轮冷启动”与“稳定态”的开销。


## 局限与备注

- 本报告只基于当前产物进行统计，未纳入 Prompt Caching / 并发 / 网络波动等线上因素；若用于成本评估，建议多次运行取置信区间。
