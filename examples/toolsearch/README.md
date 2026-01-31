# 报告

## 本报告要回答的问题

在多轮对话场景下：

1. 使用 Tool Search 能节约多少 token？
2. LLM Search vs Knowledge Search: 哪种策略更优？在什么场景？
3. 随着对话轮数增加 Token 消耗量如何变化？
4. 影响 Tool Search 的因素有哪些？
5. Tool Search 会带来多少端到端耗时增量？
6. Search Tool 的调用次数如何分布？它和耗时/Token 的关系是什么？

## 实验设定

### 工具库

#### 规模

| 规模类型 | 工具数量 |
| --- | --- |
| 小规模 | 10-20 个工具 |
| 中规模 | 50-100 个工具 |
| 大规模 | 500+ 个工具 |

本报告仅测试包含10 个函数类型的小规模工具库，具体如下。

```
examples/toolsearch/toollibrary/small/
├── base64.go           - Base64转换器
├── calculator.go       - 计算器
├── currency.go         - 货币转换器
├── email.go            - 邮箱验证器
├── hash.go             - 哈希生成器
├── password.go         - 密码生成器
├── random.go           - 随机数生成器
├── small_test.go       - 测试文件
├── text.go             - 文本处理工具
├── time.go             - 时间工具
├── tools.go            - 工具集合
└── unit.go             - 单位转换器
```

###  tool search 设置

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

### 多轮对话 user message 的设置

### 评价指标
	
指标包括 Token 消耗、耗时、最终结果的准确性。

本报告基于三份测试结果同时统计 **Token 消耗**、**端到端耗时**，并补充 **search tool 调用次数**（用于估算额外检索步骤的开销来源）。在真实业务中还需要关注 Prompt Caching 的影响：

- Tool Search 会引入额外步骤（LLM 选工具 / query rewrite + embedding + vector search），因此端到端耗时往往会增加。
- 由于每轮传给主模型的工具列表会变化，可能降低 Prompt Caching 的命中率（例如 cached_tokens），进而影响整体成本评估。

Total Token =  Tool Search (Chat Model) + Tool Search (Embedding Model) + Other Chat Model Token

#### 不同模块的 Total Tokens 消耗量


| Case Name | Case Description | Tool Search Token (Chat Model + Embedding Model) | Other Chat Model Token (Total) | Total Token |
| --- | --- | ---| --- | --- |
| without tool search |all tools provided to LLM directly | 0 | 38187 | 38187 |
| knowledge search |首先使用LLM重写查询,然后使用向量嵌入进行语义匹配| 3367 | 21447 | 24814 |
| llm search | 直接使用LLM从工具列表中选择相关工具,使用结构化输出 | 11425 | 20654 | 32079 |


##### 不同 case 的 Prompt Tokens、Completion Tokens 和 Total Tokens 消耗量

| Case Name | Prompt Tokens | Completion Tokens | Total Tokens |
| --- | ---:| ---:| ---:|
| without tool search | 37626 | 561 | 38187 |
| llm search | 31059 | 1020 | 32079 |
| knowledge search | 23510 | 1304 | 24814 |

##### Other Chat Model 明细汇总（Prompt/Completion/Total）

| Case Name | Prompt Tokens | Completion Tokens | Total Tokens |
| --- | ---:| ---:| ---:|
| without tool search | 37626 | 561 | 38187 |
| llm search | 20076 | 578 | 20654 |
| knowledge search | 20850 | 597 | 21447 |

##### Tool Search 明细汇总（Prompt/Completion/Total）

| Case Name | Prompt Tokens | Completion Tokens | Total Tokens |
| --- | ---:| ---:| ---:|
| without tool search | 0 | 0 | 0 |
| llm search | 10983 | 442 | 11425 |
| knowledge search | 2660 | 707 | 3367 |





##### 每轮 Token 消耗量（P: Prompt Tokens; C: Completion Tokens; T: Total Tokens）

| Turn | without P | without C | without T | llm P | llm C | llm T | knowledge P | knowledge C | knowledge T |
| --- | ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:|
| 1 | 2856 | 62 | 2918 | 1241 | 72 | 1313 | 1197 | 62 | 1259 |
| 2 | 3018 | 6 | 3024 | 1269 | 6 | 1275 | 1367 | 6 | 1373 |
| 3 | 3151 | 15 | 3166 | 1346 | 16 | 1362 | 1358 | 16 | 1374 |
| 4 | 3331 | 44 | 3375 | 1791 | 46 | 1837 | 1741 | 50 | 1791 |
| 5 | 3553 | 36 | 3589 | 1699 | 38 | 1737 | 2030 | 38 | 2068 |
| 6 | 3869 | 134 | 4003 | 2101 | 129 | 2230 | 2167 | 113 | 2280 |
| 7 | 4149 | 88 | 4237 | 2331 | 86 | 2417 | 2430 | 133 | 2563 |
| 8 | 4375 | 59 | 4434 | 2554 | 50 | 2604 | 2609 | 68 | 2677 |
| 9 | 4550 | 72 | 4622 | 2690 | 86 | 2776 | 2797 | 78 | 2875 |
| 10 | 4774 | 45 | 4819 | 3054 | 49 | 3103 | 3154 | 33 | 3187 |

## 结论（基于本次 Token/耗时/调用次数测试数据）

### 总览

| Case Name | Total Tokens | vs baseline（Total） | Prompt Tokens | vs baseline（Prompt） | Completion Tokens | vs baseline（Completion） | Total Session Duration | vs baseline（Duration） | Avg Duration/Turn | Tool Search Calls | Calls/Turn |
| --- | ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:|
| without tool search | 38187 | +0 (0%) | 37626 | +0 (0%) | 561 | +0 (0%) | 55.593s | +0s (0%) | 5.559s | 0 | 0 |
| llm search | 32079 | -6108 (-16.0%) | 31059 | -6567 (-17.5%) | 1020 | +459 (+81.8%) | 1m30.277s | +34.684s (+62.4%) | 9.028s | 21 | 2.1 |
| knowledge search | 24814 | -13373 (-35.0%) | 23510 | -14116 (-37.5%) | 1304 | +743 (+132.5%) | 1m43.028s | +47.435s (+85.3%) | 10.303s | 21 | 2.1 |

### 1) 使用 Tool Search 能节约多少 token？

结合上面 4 张表（`不同模块的 Total Tokens 消耗量`、`不同 case 的 Prompt/Completion/Total`、`Other Chat Model 明细汇总`、`Tool Search 明细汇总`），可以按 **3 个维度**来分析（Total / Prompt / Completion）：

- **维度 1：Total Tokens（最终总量）**：
  - without tool search：**38187**
  - llm search：**32079**（相对 baseline **节约 6108，约 16.0%**）
  - knowledge search：**24814**（相对 baseline **节约 13373，约 35.0%**）

- **维度 2：Prompt Tokens（主要节省来源）**：
  - 聚合（Other + Tool Search）PromptTokens：without **37626** → llm **31059**（**-6567，约 -17.5%**）/ knowledge **23510**（**-14116，约 -37.5%**）
  - 从模块角度看，节省来自 **Other Chat Model prompt 大幅下降**，但会被 **Tool Search prompt** 抵消一部分：
    - llm search：Other prompt **20076**（相对 baseline **-17550**） + ToolSearch prompt **10983** ⇒ 净 **-6567**
    - knowledge search：Other prompt **20850**（相对 baseline **-16776**） + ToolSearch prompt **2660** ⇒ 净 **-14116**

- **维度 3：Completion Tokens（引入额外开销，但总体可控）**：
  - 聚合（Other + Tool Search）CompletionTokens：without **561** → llm **1020**（**+459，约 +81.8%**）/ knowledge **1304**（**+743，约 +132.5%**）
  - 增量主要来自 **Tool Search 阶段自身的输出**（如 LLM 选工具的结构化输出、knowledge search 的 query rewrite 等），而 Other Chat Model 的 completion 本身变化不大：
    - llm search：Other completion **578**（相对 baseline **+17**） + ToolSearch completion **442** ⇒ 净 **+459**
    - knowledge search：Other completion **597**（相对 baseline **+36**） + ToolSearch completion **707** ⇒ 净 **+743**

结论：在本次 10 轮对话、小规模工具库设置下，Tool Search 的收益主要体现在 **显著降低 PromptTokens**；虽然会增加一定的 CompletionTokens（搜索阶段输出），但 **Prompt 的下降幅度更大**，因此 Total Tokens 仍显著下降，其中 **Knowledge Search 节省最多**。

### 2) LLM Search vs Knowledge Search：哪种策略更优？在什么场景？原因是什么？

在本次实验数据里（10 个工具、小规模），按 **Total Tokens 越低越优**：

- **Knowledge Search 更优**：24814（最低）
- **LLM Search 次之**：32079

原因分析（结合实现与成本结构）：

- **Knowledge Search 为什么更省**：
  - **tool embedding 会缓存**：首次会把候选工具（name/description/params 等）做 embedding 并写入向量库；同一进程/同一 `ToolKnowledge` 实例下后续轮次不重复 embedding 同一 tool，因此后续轮的 tool upsert 成本接近 0（测试结果里也能看到首轮 Tool Search token 明显更高、后续显著变小）。
  - **不需要每轮把全部工具描述喂给选择模型**：选择模型主要做 query rewrite（system prompt 很短），真正的匹配由向量检索完成。
  - **注意**：它每轮仍需要对 **query 做 embedding**（每轮固定成本之一），但通常远小于“把全部工具描述塞进 LLM prompt”。

- **LLM Search 为什么省得没那么多**：
  - **每轮都要把候选工具列表（tool name + description）拼进 prompt** 让 LLM 直接选工具；当前实现没有类似的本地缓存复用机制，因此这部分开销会随工具数量/描述长度近似线性增长，并在多轮里反复发生。

场景建议：

- **Knowledge Search 更适合**：工具库更大、工具描述更长、或需要稳定检索（向量检索开销相对稳定，不需要每次把全部工具描述塞进 LLM）。
- **LLM Search 更适合**：工具库较小、但需要用 LLM 对“工具意图/适配性”做更强的语义判断，或向量库不可用/不方便维护时（代价是 prompt 往往与工具列表规模线性相关）。

### 3) 随着对话轮数增加 Token 消耗量如何变化？

从三份测试结果的逐轮统计可以看到：**对话轮数增加时，主 Agent 的 PromptTokens 会逐步上升**（因为需要携带更多历史上下文）。差异主要体现在「每轮 prompt 的基线大小」：baseline 每轮都携带全部工具描述，导致 prompt 起点更大。

下面表格对比了三种策略在 **主 Agent（Other Chat Model）** 的逐轮 PromptTokens（Turn 1~10）：

| Turn | without tool search | llm search | knowledge search |
| --- | ---:| ---:| ---:|
| 1 | 2856 | 1241 | 1197 |
| 2 | 3018 | 1269 | 1367 |
| 3 | 3151 | 1346 | 1358 |
| 4 | 3331 | 1791 | 1741 |
| 5 | 3553 | 1699 | 2030 |
| 6 | 3869 | 2101 | 2167 |
| 7 | 4149 | 2331 | 2430 |
| 8 | 4375 | 2554 | 2609 |
| 9 | 4550 | 2690 | 2797 |
| 10 | 4774 | 3054 | 3154 |

趋势总结：

- **总体趋势**：三种策略的主 Agent PromptTokens 都随轮数增加而上升，整体接近线性增长（本次 10 轮里每轮大约增加 200~270 左右的 prompt tokens，取决于每轮对话内容长度）。
- **baseline 上升更快/更高**：因为每轮都要携带全部工具描述，prompt 的“基线”很高（Turn1 就是 2856）。
- **Tool Search 的搜索阶段更像固定开销**：从 Session 统计看，搜索阶段平均每次调用 token 约为：
  - llm search：约 544.0 tokens/次（`11425 / 21`，除个别轮次外波动较小）
  - knowledge search：约 160.3 tokens/次（`3367 / 21`，除首轮外也较稳定）
  
  因此，总体 Total Tokens 随轮数增加时，**主要的增长来源仍然是主 Agent 的上下文累积**；Tool Search 更多是在“每轮开始前”增加一个相对稳定的检索开销，用来显著降低主 Agent 每轮的 prompt 基线。


### 4) 影响 Tool Search 的因素有哪些？

对 Token 消耗最关键的因素通常包括：

- **工具库规模与工具描述长度**：越大/越长，越容易推高（尤其是 LLM Search 的）prompt tokens。
- **MaxTools / TopK**：召回工具越多，主 Agent 的 prompt 越大；召回越少，可能影响正确率（本报告未评测正确率）。
- **搜索阶段的提示词（SystemPrompt）与输出格式**：越啰嗦、越结构化（例如要求列出排序/解释），搜索阶段 completion tokens 越高。
- **多轮对话上下文长度**：上下文越长，搜索阶段与主 Agent 阶段都可能增长。
- **工具描述相似度/歧义**：同质工具越多，搜索更难，往往需要更多 token 才能区分与选择。

### 5) 耗时分析（端到端）

从 `耗时汇总（端到端）` 表可以看到（同为 10 轮对话）：

| Case Name | Total Session Duration | Average Duration per Turn | vs baseline（Total） | vs baseline（Avg/Turn） |
| --- | ---:| ---:| ---:| ---:|
| without tool search | 55.593s | 5.559s | +0s (0%) | +0s (0%) |
| llm search | 1m30.277s | 9.028s | +34.684s (+62.4%) | +3.469s (+62.4%) |
| knowledge search | 1m43.028s | 10.303s | +47.435s (+85.3%) | +4.744s (+85.3%) |

- **without tool search 最快**：55.593s（5.559s/turn）。
- **llm search 明显变慢**：1m30.277s，相对 baseline 增加 34.684s（约 +62.4%）。
- **knowledge search 最慢**：1m43.028s，相对 baseline 增加 47.435s（约 +85.3%），相对 llm search 增加 12.751s（约 +14.1%）。

解释：Tool Search 会把“选工具/准备工具”的步骤前置为额外调用（见下节调用次数），因此端到端耗时通常会上升；这也是评估 Tool Search 策略时需要与 Token 节省一起权衡的点。

### 6) Search Tool 调用次数分析

从 `Search Tool 调用次数汇总` 表可以看到：

##### Search Tool 调用次数汇总

说明：这里的“调用次数”指测试结果中 `Tool Search Turn-by-Turn Usage History` 里的 call 数量（即每轮为主模型选择/准备工具的内部调用次数），不包含业务 tool 本身的执行次数。

| Case Name | Tool Search 调用次数 | 平均每轮调用次数 |
| --- | ---:| ---:|
| without tool search | 0 | 0 |
| llm search | 21 | 2.1 |
| knowledge search | 21 | 2.1 |

- **两种 Tool Search 都是 21 次调用 / 10 轮**：平均约 2.1 次/轮（baseline 为 0）。
- **分布上大多是每轮 2 次，少数轮次 3 次**：通常与当轮是否发生 tool 执行失败/重试、或一轮内多次 tool 调用有关；每多一次重试，往往就会多一次“选工具”内部调用。

含义：调用次数与 Token/耗时的额外开销高度相关；当业务场景里工具不稳定或需要多次尝试时，Tool Search 的额外成本会被放大。
