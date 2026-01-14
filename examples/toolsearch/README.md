# 报告

本报告要回答的问题：

1. 使用 Tool Search 能节约多少 token？
2. LLM Search vs Knowledge Search: 哪种策略更优？在什么场景？
3. 影响 Tool Search 的因素有哪些？

### 结论（基于本次 Token 测试数据）

#### 1) 使用 Tool Search 能节约多少 token？

本次对比了三种模式的 **Total Tokens**（10 轮消息，小规模 10 个工具库）：

- **without tool search**：37840
- **llm search**：30511（相对 baseline **节约 7329 tokens，约 19.4%**）
- **knowledge search**：24565（相对 baseline **节约 13275 tokens，约 35.1%**）

结论：在本次实验设置下，**Tool Search 能显著降低总 Token**；其中 **Knowledge Search 的节约最明显**。

#### 2) LLM Search vs Knowledge Search：哪种策略更优？在什么场景？

在本次实验数据里（10 个工具、小规模），按 **Total Tokens 越低越优**：

- **Knowledge Search 更优**：24565（最低）
- **LLM Search 次之**：30511

场景建议（结合策略本身的成本结构）：

- **Knowledge Search 更适合**：工具库更大、工具描述更长、或需要稳定检索（向量检索开销相对稳定，不需要每次把全部工具描述塞进 LLM）。
- **LLM Search 更适合**：工具库较小、但需要用 LLM 对“工具意图/适配性”做更强的语义判断，或向量库不可用/不方便维护时（代价是 prompt 往往与工具列表规模线性相关）。

#### 3) 影响 Tool Search 的因素有哪些？

对 Token 消耗最关键的因素通常包括：

- **工具库规模与工具描述长度**：越大/越长，越容易推高（尤其是 LLM Search 的）prompt tokens。
- **MaxTools / TopK**：召回工具越多，主 Agent 的 prompt 越大；召回越少，可能影响正确率（本报告未评测正确率）。
- **搜索阶段的提示词（SystemPrompt）与输出格式**：越啰嗦、越结构化（例如要求列出排序/解释），搜索阶段 completion tokens 越高。
- **多轮对话上下文长度**：上下文越长，搜索阶段与主 Agent 阶段都可能增长。
- **工具描述相似度/歧义**：同质工具越多，搜索更难，往往需要更多 token 才能区分与选择。

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

指标包括 Token 消耗，耗时，最终结果的准确性。
本报告仅测试 Token 消耗量。

#### Token 消耗量


| Case Name | Case Description | Tool Search (Chat Model) + Tool Search (Embedding Model) | Other Chat Model Token | Total Token |
| --- | --- | ---| --- | --- |
| without tool search |all tools provided to LLM directly | 0 | 37840 | 37840 |
| knowledge search |首先使用LLM重写查询,然后使用向量嵌入进行语义匹配| 3103 | 21462 | 24565 |
| llm search | 直接使用LLM从工具列表中选择相关工具,使用结构化输出 | 11375 | 19136 | 30511 |

Total Token =  Tool Search (Chat Model) + Tool Search (Embedding Model) + Other Chat Model Token
