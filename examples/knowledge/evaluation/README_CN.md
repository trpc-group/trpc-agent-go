# RAG 评测：tRPC-Agent-Go vs LangChain vs Agno vs CrewAI vs AutoGen

本目录包含一个全面的评测框架，使用 [RAGAS](https://docs.ragas.io/) 指标对不同的 RAG（检索增强生成）系统进行对比分析。

## 概述

为了确保公平对比，我们使用**完全相同的配置**对五个 RAG 实现进行了评测：

- **tRPC-Agent-Go**: 我们基于 Go 的 RAG 实现
- **LangChain**: 基于 Python 的参考实现
- **Agno**: 具有内置知识库支持的 Python AI Agent 框架
- **CrewAI**: 基于 Python 的多智能体框架，使用 ChromaDB 向量存储
- **AutoGen**: 微软开发的基于 Python 的多智能体框架

## 快速开始

### 环境准备

```bash
# 安装 Python 依赖
pip install -r requirements.txt

# 设置环境变量
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="your-base-url"  # 可选
export MODEL_NAME="deepseek-v3.2"        # 可选，用于 RAG 的模型
export EVAL_MODEL_NAME="gemini-3-flash"   # 可选，用于评测的模型
export EMBEDDING_MODEL="server:274214"  # 可选

# PostgreSQL (PGVector) 配置
export PGVECTOR_HOST="127.0.0.1"
export PGVECTOR_PORT="5432"
export PGVECTOR_USER="root"
export PGVECTOR_PASSWORD="123"           # 默认密码
export PGVECTOR_DATABASE="vector"
```

### 运行评测

```bash
# 评测 LangChain
python3 main.py --kb=langchain

# 评测 tRPC-Agent-Go
python3 main.py --kb=trpc-agent-go

# 评测 Agno
python3 main.py --kb=agno

# 评测 AutoGen
python3 main.py --kb=autogen

# 查看完整日志（包含答案和上下文）
python3 main.py --kb=trpc-agent-go --max-qa=1 --full-log
```

## 配置对齐

五个系统均使用**相同参数**以确保对比的公正性：


| 参数                     | LangChain               | tRPC-Agent-Go              | Agno                    | CrewAI                  | AutoGen                 |
| -------------------------- | ------------------------- | ---------------------------- | ------------------------- | ------------------------- | ------------------------- |
| **Temperature**          | 0                       | 0                          | 0                       | 0                       | 0                       |
| **Chunk Size**           | 500                     | 500                        | 500                     | 500                     | 500                     |
| **Chunk Overlap**        | 50                      | 50                         | 50                      | 50                      | 50                      |
| **Embedding Dimensions** | 1024                    | 1024                       | 1024                    | 1024                    | 1024                    |
| **Vector Store**         | PGVector                | PGVector                   | PgVector                | ChromaDB                | PGVector                |
| **检索模式**             | Vector                  | Vector (已关闭默认 Hybrid) | Vector                  | Vector                  | Vector                  |
| **Knowledge Base 构建**  | 框架原生方式            | 框架原生方式               | 框架原生方式            | 框架原生方式            | 框架原生方式            |
| **Agent 类型**           | Agent + KB (ReAct 关闭) | Agent + KB (ReAct 关闭)    | Agent + KB (ReAct 关闭) | Agent + KB (ReAct 关闭) | Agent + KB (ReAct 关闭) |
| **单次检索数量 (k)**     | 4                       | 4                          | 4                       | 4                       | 4                       |

> 📝 **tRPC-Agent-Go 说明**：
>
> - **检索模式**：tRPC-Agent-Go 默认使用 Hybrid Search（混合检索：向量相似度 + 全文检索），但为了保证与其他框架的公平对比，评测中**关闭了混合检索**，统一使用纯 Vector Search（向量相似度检索）。

> 📝 **CrewAI 说明**：
>
> - **Vector Store**：由于 CrewAI 目前不支持 PGVector 构建知识库，这里使用 ChromaDB 作为向量存储。
> - **Bug 修复**：CrewAI (v1.9.0) 存在一个 Bug，当 LLM（如 DeepSeek-V3.2）同时返回 `content` 和 `tool_calls` 时，框架会优先返回 `content` 而忽略 `tool_calls`，导致 Agent 无法正常调用工具。我们通过 Monkey Patch 修复了 `LLM._handle_non_streaming_response` 方法，使其优先处理 `tool_calls`，确保评测的公平性。详见 `knowledge_system/crewai/knowledge_base.py`。

## 系统提示词 (System Prompt)

为了确保评测的公平性，我们为所有五个系统配置了**完全相同**的核心提示词。

**LangChain, Agno, tRPC-Agent-Go, CrewAI & AutoGen 使用的提示词：**

```text
You are a helpful assistant that answers questions using a knowledge base search tool.

CRITICAL RULES(IMPORTANT !!!):
1. You MUST call the search tool AT LEAST ONCE before answering. NEVER answer without searching first.
2. Answer ONLY using information retrieved from the search tool.
3. Do NOT add external knowledge, explanations, or context not found in the retrieved documents.
4. Do NOT provide additional details, synonyms, or interpretations beyond what is explicitly stated in the search results.
5. Use the search tool at most 3 times. If you haven't found the answer after 3 searches, provide the best answer from what you found.
6. Be concise and stick strictly to the facts from the retrieved information.
7. Give only the direct answer.
```

## 数据集

我们使用 [HuggingFace Documentation](https://huggingface.co/datasets/m-ric/huggingface_doc) 数据集。

**重要过滤说明**：为了确保数据质量和格式统一，我们对原始数据进行了严格过滤，**仅保留 Markdown (`.md`) 文件**用于文档检索和 QA 评测对。

- **Documents**: `m-ric/huggingface_doc` - 仅限 .md 文档
- **QA Pairs**: `m-ric/huggingface_doc_qa_eval` - 仅限来源为 .md 文件的问答对

## 评测指标说明

### 回答质量 (Answer Quality)


| 指标                            | 含义                                     | 越高说明                 |
| --------------------------------- | ------------------------------------------ | -------------------------- |
| **Faithfulness (忠实度)**       | 回答是否**仅基于检索到的上下文**，无幻觉 | 答案更可信，没有编造内容 |
| **Answer Relevancy (相关性)**   | 回答与问题的**相关程度**                 | 答案更切题、更完整       |
| **Answer Correctness (正确性)** | 回答与标准答案的**语义一致性**           | 答案越接近正确答案       |
| **Answer Similarity (相似度)**  | 回答与标准答案的**语义相似程度**         | 答案文本表达越相似       |

### 上下文质量 (Context Quality)


| 指标                                 | 含义                                             | 越高说明                     |
| -------------------------------------- | -------------------------------------------------- | ------------------------------ |
| **Context Precision (精确率)**       | 检索到的文档中**相关内容的密集程度**             | 检索更精准，噪音更少         |
| **Context Recall (召回率)**          | 检索出的内容是否**包含了得出答案所需的全部信息** | 检索更全面，没有遗漏关键信息 |
| **Context Entity Recall (实体召回)** | 检索到的内容对标准答案中**关键实体的覆盖程度**   | 关键信息检索更完整           |

### 指标的简单理解

- **Faithfulness**: "你说的都是根据检索到的内容吗？"（检查有没有瞎编）
- **Answer Relevancy**: "你回答的是我问的问题吗？"（检查是否答非所问）
- **Answer Correctness**: "你答对了吗？"（和标准答案对比）
- **Answer Similarity**: "你的答案和正确答案像不像？"（语义相似度）
- **Context Precision**: "检索到的内容有用吗？"（检查检索质量）
- **Context Recall**: "检索到的内容够不够？"（检查是否漏掉关键信息）
- **Context Entity Recall**: "关键信息都检索到了吗？"（检查关键实体覆盖）

## 评测结果

### 全量数据评测 (54 个问答对)

**测试环境参数：**

- **数据集**: 全量 HuggingFace Markdown 文档集 (54 QA)
- **Embedding 模型**: `BGE-M3` (1024 维)
- **Agent 模型**: `DeepSeek-V3.2`
- **评测模型**: `Gemini 3 Flash`

#### 回答质量指标 (Answer Quality)


| 指标                            | LangChain | tRPC-Agent-Go | Agno   | CrewAI | AutoGen    | 胜者             |
| --------------------------------- | ----------- | --------------- | -------- | -------- | ------------ | ------------------ |
| **Faithfulness (忠实度)**       | 0.8614    | **0.9853**    | 0.7213 | 0.9655 | 0.9113     | ✅ tRPC-Agent-Go |
| **Answer Relevancy (相关性)**   | 0.8529    | 0.8890        | 0.9013 | 0.8383 | **0.9040** | ✅ AutoGen       |
| **Answer Correctness (正确性)** | 0.6912    | **0.8299**    | 0.6916 | 0.8101 | 0.7725     | ✅ tRPC-Agent-Go |
| **Answer Similarity (相似度)**  | 0.6740    | **0.7251**    | 0.6772 | 0.6948 | 0.6830     | ✅ tRPC-Agent-Go |

#### 上下文质量指标 (Context Quality)


| 指标                                 | LangChain | tRPC-Agent-Go | Agno   | CrewAI     | AutoGen    | 胜者                |
| -------------------------------------- | ----------- | --------------- | -------- | ------------ | ------------ | --------------------- |
| **Context Precision (精确率)**       | 0.6314    | **0.7278**    | 0.7046 | 0.6673     | 0.6142     | ✅ tRPC-Agent-Go    |
| **Context Recall (召回率)**          | 0.8333    | 0.9259        | 0.9259 | **0.9444** | **0.9444** | ✅ CrewAI / AutoGen |
| **Context Entity Recall (实体召回)** | 0.4138    | **0.5034**    | 0.4331 | 0.3922     | 0.2902     | ✅ tRPC-Agent-Go    |

### 核心结论

1. **tRPC-Agent-Go 综合表现最优**：在 7 项指标中拿下 5 项第一——**Faithfulness (0.9853)**、**Answer Correctness (0.8299)**、**Answer Similarity (0.7251)**、**Context Precision (0.7278)** 和 **Context Entity Recall (0.5034)**，回答质量和检索精度全面领先。
2. **AutoGen 相关性领先**：**Answer Relevancy (0.9040)** 排名第一（与 Agno 的 0.9013 接近），回答切题性最优。同时 **Context Recall (0.9444)** 并列第一。
3. **CrewAI 召回率最高**：**Context Recall (0.9444)** 并列第一，表明其检索召回最全面。
4. **Agno 相关性突出**：**Answer Relevancy (0.9013)** 排名第二，回答切题性优秀。
5. **五个框架各有所长**：LangChain 表现均衡稳定，各框架在不同维度各具优势。

### 评测观察

在评测过程中，我们通过抓包分析发现，各框架在使用相同 LLM 模型的情况下，**框架发起请求的流程比较相似**——本质上都是 Agent 调用搜索工具、获取上下文、生成回答的标准 RAG 流程。

需要注意的是：

- **数据集规模偏小**：当前评测集仅有1900+文档 以及 54 个QA对，不算大规模数据
- **Prompt 对分数影响**： 不可否认，在当前数据集下系统提示词对Agent的执行影响比较大，同样也会对最终的分数产生很大的影响，我们保证了统一的系统提示词。
- **切块策略可能有影响**：排除系统提示词的影响后，不同框架的切块实现（chunk size、overlap、边界识别等）可能会对检索和回答质量产生影响，进而影响 Context Precision、Context Recall 等检索指标。
