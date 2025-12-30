# Wikipedia 搜索工具

## 概述

trpc-agent-go 的 Wikipedia 搜索工具,提供详细的文章信息和元数据。

## 功能特性

- **综合搜索**: 在 Wikipedia 文章中进行搜索,返回详细结果
- **丰富的元数据**: 获取文章统计信息、时间戳和结构化信息
- **多语言支持**: 支持不同语言版本的 Wikipedia 搜索(中文、英文、西班牙语等)
- **灵活配置**: 自定义搜索参数和行为

## 安装

```go
import "trpc.group/trpc-go/trpc-agent-go/tool/wikipedia"
```

## 快速开始

```go
// 创建 Wikipedia 工具集
wikipediaToolSet, err := wikipedia.NewToolSet(
    wikipedia.WithLanguage("zh"),  // 使用中文 Wikipedia
    wikipedia.WithMaxResults(5),
)
if err != nil {
    // 处理错误
}
```

- 可参考样例： `trpc-agent-go/examples/wiki/main.go`

## 配置选项

### WithLanguage(language string)
设置要搜索的 Wikipedia 语言版本。

```go
// 英文 Wikipedia (默认)
wikipedia.WithLanguage("en")

// 中文 Wikipedia
wikipedia.WithLanguage("zh")

// 西班牙语 Wikipedia
wikipedia.WithLanguage("es")
```

### WithMaxResults(maxResults int)
设置返回的最大搜索结果数量。

```go
// 返回最多 10 条结果
wikipedia.WithMaxResults(10)
```

### WithTimeout(timeout time.Duration)
设置 HTTP 请求超时时间。

```go
// 30 秒超时
wikipedia.WithTimeout(30 * time.Second)
```

### WithUserAgent(userAgent string)
设置自定义的 User-Agent 字符串。

```go
wikipedia.WithUserAgent("MyApp/1.0")
```

## 工具输入参数

Wiki 搜索工具接受以下 JSON 参数:

| 参数 | 类型 | 必填 | 描述 |
|------|------|------|------|
| `query` | string | 是 | Wikipedia 搜索查询关键词 |
| `limit` | int | 否 | 最大结果数量(默认: 5) |
| `include_all` | bool | 否 | 包含所有可用的元数据 |

### 输入示例
```json
{
  "query": "人工智能",
  "limit": 3,
  "include_all": true
}
```

## 工具输出格式

工具返回一个包含以下结构的综合性响应:

```json
{
  "query": "人工智能",
  "results": [
    {
      "title": "人工智能",
      "url": "https://zh.wikipedia.org/wiki/人工智能",
      "description": "人工智能(英语:Artificial Intelligence, AI)亦称机器智能...",
      "page_id": 18985062,
      "word_count": 12543,
      "size_bytes": 156789,
      "last_modified": "2024-11-15T10:30:00Z",
      "namespace": 0
    }
  ],
  "total_hits": 1247,
  "summary": "Found 3 results (total: 1247)",
  "search_time": "45.23ms"
}
```

### 输出字段说明

| 字段 | 描述 |
|------|------|
| `query` | 原始搜索查询 |
| `results` | 搜索结果数组 |
| `total_hits` | 匹配文章的总数 |
| `summary` | 人类可读的搜索摘要 |
| `search_time` | 搜索执行时间 |

#### 结果项字段

| 字段 | 描述 |
|------|------|
| `title` | 文章标题 |
| `url` | Wikipedia 文章直链 |
| `description` | 文章摘要/片段 |
| `page_id` | Wikipedia 页面唯一标识符 |
| `word_count` | 文章字数 |
| `size_bytes` | 文章大小(字节) |
| `last_modified` | 最后修改时间戳 |
| `namespace` | Wikipedia 命名空间(0=主文章) |

## 使用场景

### 1. 基础信息检索
适合快速获取任何主题的基本事实和信息。

**Agent 查询**: "*什么是量子计算?*"  
**工具使用**: 搜索"量子计算"并返回综合信息。

### 2. 研究和分析
适合深度研究,提供详细的元数据。

**Agent 查询**: "*比较不同编程语言文章的长度*"  
**工具使用**: 搜索多个编程语言并比较字数统计。

### 3. 事实核查
验证信息并获取权威来源。

**Agent 查询**: "*相对论是什么时候提出的?*"  
**工具使用**: 搜索"相对论"并提取历史信息。

### 4. 教育内容
为学习目的提供详细的解释。

**Agent 查询**: "*解释机器学习的概念*"  
**工具使用**: 搜索机器学习并返回结构化信息。

## 在 Agent 中的使用示例

```go
package main

import (
    "context"
    "fmt"
    
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/wikipedia"
)

func main() {
    // 创建模型
    model := openai.New("gpt-4", openai.WithAPIKey("your-api-key"))
    
    // 创建 Wikipedia ToolSet
    wikipediaToolSet, err := wikipedia.NewToolSet(
        wikipedia.WithLanguage("zh"),  // 使用中文版本
        wikipedia.WithMaxResults(3),
    )
    if err != nil {
        // 处理错误
    }
    
    // 创建 Agent
    agent := llmagent.New(
        "wikipedia-agent",
        llmagent.WithModel(model),
        llmagent.WithDescription("具有 Wikipedia 访问能力的 AI 助手"),
        llmagent.WithInstruction("使用 Wikipedia 提供准确的信息"),
        llmagent.WithToolSets([]tool.ToolSet{wikipediaToolSet}),
    )
    
    // 使用 agent...
}
```

## 错误处理

- **网络错误**: 返回带描述性消息的错误
- **无效查询**: 返回空结果和错误摘要
- **API 速率限制**: 返回适当的错误响应
- **超时错误**: 可配置超时,提供清晰的错误消息

## 最佳实践

1. **查询质量**: 使用具体、格式良好的查询以获得最佳结果
2. **结果限制**: 设置适当的限制以避免过多的数据传输
3. **语言一致性**: 根据你的使用场景选择合适的语言版本
4. **错误处理**: 在 Agent 逻辑中始终处理潜在错误
5. **速率限制**: 注意 Wikipedia 的使用政策

## 支持的语言

工具支持所有 Wikipedia 语言版本。常见的包括:

- `en` - 英文(默认)
- `zh` - 中文
- `es` - 西班牙语
- `fr` - 法语
- `de` - 德语
- `ja` - 日语
- `ru` - 俄语
- `pt` - 葡萄牙语
- `it` - 意大利语
- `ar` - 阿拉伯语

## 工作原理

### 数据流程

1. **用户查询** → Agent 接收用户消息
2. **工具调用** → LLM 决定调用 wiki_search 工具
3. **API 请求** → 工具向 Wikipedia API 发送搜索请求
4. **数据返回** → 获取结构化的搜索结果(JSON 格式)
5. **LLM 处理** → 模型基于工具返回的数据生成自然语言回答
6. **流式输出** → 实时显示 LLM 生成的内容

### 关键点

- **工具返回的是原始数据**: Wikipedia API 返回的 JSON 结构化数据
- **LLM 进行智能加工**: 模型将结构化数据转换为流畅的自然语言
- **RAG 架构**: 工具提供准确的事实,LLM 提供语言能力和推理

示例:
```
工具返回: {"title": "人工智能", "word_count": 12543, ...}
LLM 生成: "人工智能是一门复杂的学科,Wikipedia 上的相关文章包含 12,543 个单词..."
```

## 技术架构

```
用户输入
  ↓
LLM Agent (决策层)
  ↓
Wiki Search Tool (工具层)
  ↓
Wikipedia API Client (HTTP 层)
  ↓
Wikipedia MediaWiki API
  ↓
返回结构化数据
  ↓
LLM 生成自然语言回答
  ↓
流式输出给用户
```

## 常见使用场景示例

### 场景 1: 知识问答
```
用户: "什么是深度学习?"
工具调用: wiki_search(query="深度学习")
返回: 深度学习的定义、应用、历史等信息
输出: 基于 Wikipedia 数据的详细解释
```

### 场景 2: 对比分析
```
用户: "比较 Python 和 Java 的文章长度"
工具调用: 
  - wiki_search(query="Python")
  - wiki_search(query="Java")
返回: 两篇文章的 word_count 等元数据
输出: 对比分析结果
```

### 场景 3: 历史查询
```
用户: "牛顿的万有引力定律是什么时候发现的?"
工具调用: wiki_search(query="万有引力")
返回: 包含历史信息的文章摘要
输出: 提取并总结历史信息
```

## 许可证

此工具是 trpc-agent-go 框架的一部分,采用 Apache License Version 2.0 许可。

## 贡献

有关贡献指南,请参阅主 trpc-agent-go 仓库。

## 相关资源

- [trpc-agent-go 文档](https://trpc.group/trpc-go/trpc-agent-go)
- [Wikipedia API 文档](https://www.mediawiki.org/wiki/API:Main_page)
- [示例代码](../examples/wiki/)

## 版本历史

- v1.0 - 初始版本,提供综合搜索和丰富元数据
