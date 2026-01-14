# 报告

本报告要回答的问题：

1. 使用 Tool Search 能节约多少 token？
2. LLM Search vs Knowledge Search: 哪种策略更优？在什么场景？
3. 影响 Tool Search 的因素有哪些？

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


| Case Name | Case Description | Tool Search (Chat Model) | Tool Search (Embedding Model) | Other Chat Model Token | Total Token |
| --- | --- | ---| --- | --- | --- |
| without tool search |all tools provided to LLM directly | 0 | 0 | 0 | 0 |
| knowledge search |首先使用LLM重写查询,然后使用向量嵌入进行语义匹配| 0 | 0 | 0 | 0 |
| llm search | 直接使用LLM从工具列表中选择相关工具,使用结构化输出 | 0 | 0 | 0 | 0 |

Total Token =  Tool Search (Chat Model) + Tool Search (Embedding Model) + Other Chat Model Token
