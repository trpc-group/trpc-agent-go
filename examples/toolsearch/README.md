# 报告

## 本报告要回答的问题

在多轮对话场景下：

1. 使用 Tool Search 能节约多少 token？
2. LLM Search vs Knowledge Search: 哪种策略更优？在什么场景？
3. 随着对话轮数增加 Token 消耗量如何变化？
4. 影响 Tool Search 的因素有哪些？

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

| paramter name | paramter value |
| --- | --- |
| SystemPrompt | Your goal is to select the most relevant tools for answering the user's query.<br><br>IMPORTANT: List the tool names in order of relevance, with the most relevant first.<br>If you exceed the maximum number of tools, only the first {MaxTools} will be used.<br><br>Available tools:<br>- {ToolName-1}: {ToolDescription-1}<br>- {ToolName-2}: {ToolDescription-2}<br>......<br>- {ToolName-n}: {ToolDescription-n} |
| Chat Model | deepseek v3.2, 使用[对话补全（chat/completions） Request](https://api-docs.deepseek.com/zh-cn/api/create-chat-completion) 里面的默认参数 |
| MaxTools | 3 |

#### Knowledge Search

| paramter name | paramter value |
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

本报告仅测试 Token 消耗量；但在真实业务中，耗时与 Prompt Caching 的影响也很重要：

- Tool Search 会引入额外步骤（LLM 选工具 / query rewrite + embedding + vector search），因此端到端耗时可能增加。
- 由于每轮传给主模型的工具列表会变化，可能降低 Prompt Caching 的命中率（例如 cached_tokens），进而影响整体成本评估。

Total Token =  Tool Search (Chat Model) + Tool Search (Embedding Model) + Other Chat Model Token

#### 不同模块的 Total Tokens 消耗量


| Case Name | Case Description | Tool Search Token (Chat Model + Embedding Model) | Other Chat Model Token (Total) | Total Token |
| --- | --- | ---| --- | --- |
| without tool search |all tools provided to LLM directly | 0 | 37840 | 37840 |
| knowledge search |首先使用LLM重写查询,然后使用向量嵌入进行语义匹配| 3103 | 21462 | 24565 |
| llm search | 直接使用LLM从工具列表中选择相关工具,使用结构化输出 | 11375 | 19136 | 30511 |


##### 不同 case 的  Prompt Tokens，Completion Tokens 和 Total Tokens 消耗量

| Case Name | Prompt Tokens | Completion Tokens | Total Tokens |
| --- | ---:| ---:| ---:|
| without tool search | 37309 | 531 | 37840 |
| llm search | 29571 | 940 | 30511 |
| knowledge search | 23403 | 1162 | 24565 |

##### Other Chat Model 明细汇总（Prompt/Completion/Total）

| Case Name | Prompt Tokens | Completion Tokens | Total Tokens |
| --- | ---:| ---:| ---:|
| without tool search | 37309 | 531 | 37840 |
| llm search | 18588 | 548 | 19136 |
| knowledge search | 20876 | 586 | 21462 |

##### Tool Search 明细汇总（Prompt/Completion/Total）

| Case Name | Prompt Tokens | Completion Tokens | Total Tokens |
| --- | ---:| ---:| ---:|
| without tool search | 0 | 0 | 0 |
| llm search | 10983 | 392 | 11375 |
| knowledge search | 2527 | 576 | 3103 |





##### 每轮 Token 消耗量（P: Prompt Tokens; C: Completion Tokens; T: Total Tokens）

| Turn | without P | without C | without T | llm P | llm C | llm T | knowledge P | knowledge C | knowledge T |
| --- | ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:|
| 1 | 2827 | 74 | 2901 | 657 | 72 | 729 | 1307 | 87 | 1394 |
| 2 | 3001 | 6 | 3007 | 767 | 6 | 773 | 1371 | 6 | 1377 |
| 3 | 3134 | 17 | 3151 | 940 | 15 | 955 | 1362 | 15 | 1377 |
| 4 | 3316 | 41 | 3357 | 1792 | 51 | 1843 | 1745 | 50 | 1795 |
| 5 | 3535 | 42 | 3577 | 1705 | 38 | 1743 | 2035 | 38 | 2073 |
| 6 | 3854 | 85 | 3939 | 2109 | 108 | 2217 | 2168 | 90 | 2258 |
| 7 | 4086 | 97 | 4183 | 2323 | 86 | 2409 | 2410 | 123 | 2533 |
| 8 | 4321 | 66 | 4387 | 2547 | 63 | 2610 | 2579 | 68 | 2647 |
| 9 | 4505 | 71 | 4576 | 2695 | 78 | 2773 | 2774 | 71 | 2845 |
| 10 | 4730 | 32 | 4762 | 3053 | 31 | 3084 | 3125 | 38 | 3163 |


## 结论（基于本次 Token 测试数据）

### 1) 使用 Tool Search 能节约多少 token？

结合上面 4 张表（`不同模块的 Total Tokens 消耗量`、`不同 case 的 Prompt/Completion/Total`、`Other Chat Model 明细汇总`、`Tool Search 明细汇总`），可以按 **3 个维度**来分析（Total / Prompt / Completion）：

- **维度 1：Total Tokens（最终总量）**：
  - without tool search：**37840**
  - llm search：**30511**（相对 baseline **节约 7329，约 19.4%**）
  - knowledge search：**24565**（相对 baseline **节约 13275，约 35.1%**）

- **维度 2：Prompt Tokens（主要节省来源）**：
  - 聚合（Other + Tool Search）PromptTokens：without **37309** → llm **29571**（**-7738，约 -20.7%**）/ knowledge **23403**（**-13906，约 -37.3%**）
  - 从模块角度看，节省来自 **Other Chat Model prompt 大幅下降**，但会被 **Tool Search prompt** 抵消一部分：
    - llm search：Other prompt **18588**（相对 baseline **-18721**） + ToolSearch prompt **10983** ⇒ 净 **-7738**
    - knowledge search：Other prompt **20876**（相对 baseline **-16433**） + ToolSearch prompt **2527** ⇒ 净 **-13906**

- **维度 3：Completion Tokens（引入额外开销，但总体可控）**：
  - 聚合（Other + Tool Search）CompletionTokens：without **531** → llm **940**（**+409，约 +77.0%**）/ knowledge **1162**（**+631，约 +118.8%**）
  - 增量主要来自 **Tool Search 阶段自身的输出**（如 LLM 选工具的结构化输出、knowledge search 的 query rewrite 等），而 Other Chat Model 的 completion 本身变化不大：
    - llm search：Other completion **548**（相对 baseline **+17**） + ToolSearch completion **392** ⇒ 净 **+409**
    - knowledge search：Other completion **586**（相对 baseline **+55**） + ToolSearch completion **576** ⇒ 净 **+631**

结论：在本次 10 轮对话、小规模工具库设置下，Tool Search 的收益主要体现在 **显著降低 PromptTokens**；虽然会增加一定的 CompletionTokens（搜索阶段输出），但 **Prompt 的下降幅度更大**，因此 Total Tokens 仍显著下降，其中 **Knowledge Search 节省最多**。

### 2) LLM Search vs Knowledge Search：哪种策略更优？在什么场景？原因是什么？

在本次实验数据里（10 个工具、小规模），按 **Total Tokens 越低越优**：

- **Knowledge Search 更优**：24565（最低）
- **LLM Search 次之**：30511

原因分析（结合实现与成本结构）：

- **Knowledge Search 为什么更省**：
  - **tool embedding 会缓存**：首次会把候选工具（name/description/params 等）做 embedding 并写入向量库；同一进程/同一 `ToolKnowledge` 实例下后续轮次不重复 embedding 同一 tool，因此后续轮的 tool upsert 成本接近 0（日志里也能看到首轮 Tool Search token 明显更高、后续显著变小）。
  - **不需要每轮把全部工具描述喂给选择模型**：选择模型主要做 query rewrite（system prompt 很短），真正的匹配由向量检索完成。
  - **注意**：它每轮仍需要对 **query 做 embedding**（每轮固定成本之一），但通常远小于“把全部工具描述塞进 LLM prompt”。

- **LLM Search 为什么省得没那么多**：
  - **每轮都要把候选工具列表（tool name + description）拼进 prompt** 让 LLM 直接选工具；当前实现没有类似的本地缓存复用机制，因此这部分开销会随工具数量/描述长度近似线性增长，并在多轮里反复发生。

场景建议：

- **Knowledge Search 更适合**：工具库更大、工具描述更长、或需要稳定检索（向量检索开销相对稳定，不需要每次把全部工具描述塞进 LLM）。
- **LLM Search 更适合**：工具库较小、但需要用 LLM 对“工具意图/适配性”做更强的语义判断，或向量库不可用/不方便维护时（代价是 prompt 往往与工具列表规模线性相关）。

### 3) 随着对话轮数增加 Token 消耗量如何变化？

从三份日志的逐轮统计可以看到：**对话轮数增加时，主 Agent 的 PromptTokens 会逐步上升**（因为需要携带更多历史上下文）。差异主要体现在「每轮 prompt 的基线大小」：baseline 每轮都携带全部工具描述，导致 prompt 起点更大。

下面表格对比了三种策略在 **主 Agent（Other Chat Model）** 的逐轮 PromptTokens（Turn 1~10）：

| Turn | without tool search | llm search | knowledge search |
| --- | ---:| ---:| ---:|
| 1 | 2827 | 657 | 1307 |
| 2 | 3001 | 767 | 1371 |
| 3 | 3134 | 940 | 1362 |
| 4 | 3316 | 1792 | 1745 |
| 5 | 3535 | 1705 | 2035 |
| 6 | 3854 | 2109 | 2168 |
| 7 | 4086 | 2323 | 2410 |
| 8 | 4321 | 2547 | 2579 |
| 9 | 4505 | 2695 | 2774 |
| 10 | 4730 | 3053 | 3125 |

趋势总结：

- **总体趋势**：三种策略的主 Agent PromptTokens 都随轮数增加而上升，整体接近线性增长（本次 10 轮里每轮大约增加 200~270 左右的 prompt tokens，取决于每轮对话内容长度）。
- **baseline 上升更快/更高**：因为每轮都要携带全部工具描述，prompt 的“基线”很高（Turn1 就是 2827）。
- **Tool Search 的搜索阶段更像固定开销**：从 Session 统计看，搜索阶段平均每轮 token 约为：
  - llm search：约 541.7 tokens/次（`11375 / 21`，日志中每次波动很小）
  - knowledge search：约 147.8 tokens/次（`3103 / 21`，除首轮外也较稳定）
  
  因此，总体 Total Tokens 随轮数增加时，**主要的增长来源仍然是主 Agent 的上下文累积**；Tool Search 更多是在“每轮开始前”增加一个相对稳定的检索开销，用来显著降低主 Agent 每轮的 prompt 基线。


### 4) 影响 Tool Search 的因素有哪些？

对 Token 消耗最关键的因素通常包括：

- **工具库规模与工具描述长度**：越大/越长，越容易推高（尤其是 LLM Search 的）prompt tokens。
- **MaxTools / TopK**：召回工具越多，主 Agent 的 prompt 越大；召回越少，可能影响正确率（本报告未评测正确率）。
- **搜索阶段的提示词（SystemPrompt）与输出格式**：越啰嗦、越结构化（例如要求列出排序/解释），搜索阶段 completion tokens 越高。
- **多轮对话上下文长度**：上下文越长，搜索阶段与主 Agent 阶段都可能增长。
- **工具描述相似度/歧义**：同质工具越多，搜索更难，往往需要更多 token 才能区分与选择。
