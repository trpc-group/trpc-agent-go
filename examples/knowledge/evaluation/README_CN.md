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

### HuggingFace Documentation

我们使用 [HuggingFace Documentation](https://huggingface.co/datasets/m-ric/huggingface_doc) 数据集。

**重要过滤说明**：为了确保数据质量和格式统一，我们对原始数据进行了严格过滤，**仅保留 Markdown (`.md`) 文件**用于文档检索和 QA 评测对。

- **Documents**: `m-ric/huggingface_doc` - 仅限 .md 文档
- **QA Pairs**: `m-ric/huggingface_doc_qa_eval` - 仅限来源为 .md 文件的问答对

### RGB（检索增强生成基准测试）

我们还使用了 [RGB Benchmark](https://github.com/chen700564/RGB)（[论文](https://arxiv.org/abs/2309.01431)）作为 QA 数据源。RGB 原始设计提供了带有预定义正例（相关）和负例（无关）段落的查询，用于评测 4 种 RAG 能力：噪声鲁棒性、拒答能力、信息整合和反事实鲁棒性。

英文部分包含 3 个子集，具有不同的 QA 特征：


| 子集        | QA 数量 | 原始测试重点 | 说明                                                                                                       |
| ------------- | --------- | -------------- | ------------------------------------------------------------------------------------------------------------ |
| **en**      | 300     | 噪声鲁棒性   | 标准事实性查询，答案来源明确。每个查询有明确的正例段落和独立的负例（无关）段落。                           |
| **en_int**  | 100     | 信息整合能力 | 需要从**多个**正例段落中综合信息才能回答的查询，答案分散在多个文档中。                                     |
| **en_fact** | 100     | 反事实鲁棒性 | 同时包含正确的`positive` 段落和**篡改了关键事实**的 `positive_wrong` 段落（如将"Facebook"替换为"Apple"）。 |

> **重要说明：我们对 RGB 的使用方式与原始论文不同。** RGB 原始评测方式是将预选的 positive + negative 段落直接拼接后作为上下文喂给 LLM。而在我们的评测中，仅将 **positive 段落**作为文档灌入各框架的知识库，由框架自行完成检索 + 生成的端到端流程。这意味着：
>
> - **en**：负例（噪声）段落**未被灌入**知识库，因此"噪声鲁棒性"并未被直接测试，实际作为**标准事实性 QA** 基准。
> - **en_fact**：`positive_wrong`（反事实）段落**未被灌入**，因此"反事实鲁棒性"并未被直接测试，实际作为另一组**事实性 QA** 基准，但问题特征与 en 子集不同。
> - **en_int**：信息整合的挑战**被保留**，因为答案确实需要从多个检索到的文档中综合得出。

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

### 1. HuggingFace 数据集 (54 个问答对)

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

#### 核心结论

1. **tRPC-Agent-Go 综合表现最优**：在 7 项指标中拿下 5 项第一——**Faithfulness (0.9853)**、**Answer Correctness (0.8299)**、**Answer Similarity (0.7251)**、**Context Precision (0.7278)** 和 **Context Entity Recall (0.5034)**，回答质量和检索精度全面领先。
2. **AutoGen 相关性领先**：**Answer Relevancy (0.9040)** 排名第一（与 Agno 的 0.9013 接近），回答切题性最优。同时 **Context Recall (0.9444)** 并列第一。
3. **CrewAI 召回率最高**：**Context Recall (0.9444)** 并列第一，表明其检索召回最全面。
4. **Agno 相关性突出**：**Answer Relevancy (0.9013)** 排名第二，回答切题性优秀。
5. **五个框架各有所长**：LangChain 表现均衡稳定，各框架在不同维度各具优势。

---

### 2. RGB 数据集

**测试环境参数：**

- **数据集**: [RGB Benchmark](https://github.com/chen700564/RGB) (英文子集)
- **Embedding 模型**: `BGE-M3` (1024 维)
- **Agent 模型**: `DeepSeek-V3.2`
- **评测模型**: `Gemini 3 Flash`

#### 2.1 RGB-en：标准事实性 QA (300 个问答对)

标准事实性查询，答案来源明确。（RGB 原始测试重点为噪声鲁棒性，但在我们的端到端评测中，负例段落未被灌入知识库。）

**回答质量：**


| 指标                            | LangChain | tRPC-Agent-Go | Agno       | CrewAI     | AutoGen | 胜者             |
| --------------------------------- | ----------- | --------------- | ------------ | ------------ | --------- | ------------------ |
| **Faithfulness (忠实度)**       | 0.9783    | 0.9872        | 0.7554     | **0.9948** | 0.7816  | ✅ CrewAI        |
| **Answer Relevancy (相关性)**   | 0.9493    | 0.9534        | **0.9612** | 0.9125     | 0.8866  | ✅ Agno          |
| **Answer Correctness (正确性)** | 0.7969    | **0.8462**    | 0.7141     | 0.7680     | 0.6775  | ✅ tRPC-Agent-Go |
| **Answer Similarity (相似度)**  | 0.5308    | **0.5401**    | 0.5040     | 0.5327     | 0.5014  | ✅ tRPC-Agent-Go |

**上下文质量：**


| 指标                                 | LangChain  | tRPC-Agent-Go | Agno       | CrewAI     | AutoGen    | 胜者       |
| -------------------------------------- | ------------ | --------------- | ------------ | ------------ | ------------ | ------------ |
| **Context Precision (精确率)**       | 0.9407     | 0.9539        | 0.9452     | 0.9393     | **0.9715** | ✅ AutoGen |
| **Context Recall (召回率)**          | **1.0000** | **1.0000**    | **1.0000** | **1.0000** | **1.0000** | 并列       |
| **Context Entity Recall (实体召回)** | 0.6378     | 0.6478        | **0.6583** | 0.6467     | 0.6328     | ✅ Agno    |

#### 2.2 RGB-en_int：多文档信息整合 (100 个问答对)

测试模型检索并综合分散在多个文档中的信息的能力。这是我们端到端评测中最能保留 RGB 原始挑战的子集。

**回答质量：**


| 指标                            | LangChain  | tRPC-Agent-Go | Agno   | CrewAI | AutoGen | 胜者             |
| --------------------------------- | ------------ | --------------- | -------- | -------- | --------- | ------------------ |
| **Faithfulness (忠实度)**       | 0.9523     | **0.9743**    | 0.8615 | 0.9623 | 0.8814  | ✅ tRPC-Agent-Go |
| **Answer Relevancy (相关性)**   | **0.9301** | 0.9061        | 0.9146 | 0.9094 | 0.8764  | ✅ LangChain     |
| **Answer Correctness (正确性)** | 0.7258     | **0.8059**    | 0.7203 | 0.7277 | 0.7142  | ✅ tRPC-Agent-Go |
| **Answer Similarity (相似度)**  | 0.5441     | **0.5683**    | 0.5447 | 0.5546 | 0.5403  | ✅ tRPC-Agent-Go |

**上下文质量：**


| 指标                                 | LangChain | tRPC-Agent-Go | Agno       | CrewAI | AutoGen    | 胜者       |
| -------------------------------------- | ----------- | --------------- | ------------ | -------- | ------------ | ------------ |
| **Context Precision (精确率)**       | 0.2868    | 0.3118        | **0.3244** | 0.3069 | 0.3204     | ✅ Agno    |
| **Context Recall (召回率)**          | 0.9133    | 0.9233        | 0.9300     | 0.9250 | **0.9350** | ✅ AutoGen |
| **Context Entity Recall (实体召回)** | 0.6317    | 0.6500        | 0.6350     | 0.6417 | **0.6933** | ✅ AutoGen |

#### 2.3 RGB-en_fact：事实性 QA (100 个问答对)

事实性查询，问题特征与 en 子集不同。（RGB 原始测试重点为反事实鲁棒性，但在我们的端到端评测中，`positive_wrong` 段落未被灌入知识库。）

**回答质量：**


| 指标                            | LangChain | tRPC-Agent-Go | Agno   | CrewAI     | AutoGen | 胜者             |
| --------------------------------- | ----------- | --------------- | -------- | ------------ | --------- | ------------------ |
| **Faithfulness (忠实度)**       | 0.9529    | 0.9533        | 0.6966 | **0.9653** | 0.6714  | ✅ CrewAI        |
| **Answer Relevancy (相关性)**   | 0.9204    | **0.9471**    | 0.9317 | 0.8753     | 0.7334  | ✅ tRPC-Agent-Go |
| **Answer Correctness (正确性)** | 0.7936    | **0.8467**    | 0.6816 | 0.7334     | 0.6106  | ✅ tRPC-Agent-Go |
| **Answer Similarity (相似度)**  | 0.5499    | **0.5672**    | 0.5171 | 0.5500     | 0.5002  | ✅ tRPC-Agent-Go |

**上下文质量：**


| 指标                                 | LangChain  | tRPC-Agent-Go | Agno   | CrewAI     | AutoGen    | 胜者             |
| -------------------------------------- | ------------ | --------------- | -------- | ------------ | ------------ | ------------------ |
| **Context Precision (精确率)**       | 0.8652     | 0.8641        | 0.8495 | **0.8694** | 0.8282     | ✅ CrewAI        |
| **Context Recall (召回率)**          | **0.9900** | **0.9900**    | 0.9700 | **0.9900** | **0.9900** | 并列             |
| **Context Entity Recall (实体召回)** | 0.7300     | **0.7400**    | 0.7100 | 0.7300     | 0.7300     | ✅ tRPC-Agent-Go |

#### RGB 综合分析

**3 个子集平均值对比 (en + en_int + en_fact)：**


| 指标                                 | LangChain  | tRPC-Agent-Go | Agno       | CrewAI     | AutoGen    | 胜者             |
| -------------------------------------- | ------------ | --------------- | ------------ | ------------ | ------------ | ------------------ |
| **Faithfulness (忠实度)**            | 0.9612     | 0.9716        | 0.7712     | **0.9741** | 0.7781     | ✅ CrewAI        |
| **Answer Relevancy (相关性)**        | 0.9333     | 0.9355        | **0.9358** | 0.8991     | 0.8321     | ✅ Agno          |
| **Answer Correctness (正确性)**      | 0.7721     | **0.8329**    | 0.7053     | 0.7430     | 0.6674     | ✅ tRPC-Agent-Go |
| **Answer Similarity (相似度)**       | 0.5416     | **0.5585**    | 0.5219     | 0.5458     | 0.5140     | ✅ tRPC-Agent-Go |
| **Context Precision (精确率)**       | 0.6976     | **0.7099**    | 0.7064     | 0.7052     | 0.7067     | ✅ tRPC-Agent-Go |
| **Context Recall (召回率)**          | 0.9678     | 0.9711        | 0.9667     | 0.9717     | **0.9750** | ✅ AutoGen       |
| **Context Entity Recall (实体召回)** | 0.6665     | 0.6793        | 0.6678     | 0.6728     | **0.6854** | ✅ AutoGen       |

**各子集第一名统计**（5 个框架参与 3 个子集；en/en_fact 的 Context Recall 多方并列不计入）：

| 框架 | 第一名次数 | 优势领域 |
|------|-----------|---------|
| **tRPC-Agent-Go** | **9** | Answer Correctness（全部）、Answer Similarity（全部）、Faithfulness (en_int)、Answer Relevancy (en_fact)、Context Entity Recall (en_fact) |
| **AutoGen** | **3** | Context Precision (en)、Context Recall (en_int)、Context Entity Recall (en_int) |
| **CrewAI** | **3** | Faithfulness (en、en_fact)、Context Precision (en_fact) |
| **Agno** | **3** | Answer Relevancy (en)、Context Precision (en_int)、Context Entity Recall (en) |
| **LangChain** | **1** | Answer Relevancy (en_int) |

**核心发现：**

1. **tRPC-Agent-Go 在回答质量上持续领先**：在所有 3 个子集中均拿下 **Answer Correctness** 和 **Answer Similarity** 第一，展现出最准确、最可靠的回答能力，不受检索场景影响。
2. **AutoGen 在上下文检索上表现突出**：en 子集 **Context Precision** 最高 (0.9715)，en_int 子集 **Context Recall** 和 **Context Entity Recall** 均为第一，展现出较强的检索能力，尤其在多文档场景下。
3. **大部分框架忠实度表现优异**：LangChain、tRPC-Agent-Go、CrewAI 均在各子集达到 > 0.95 的忠实度，幻觉问题控制良好。Agno (0.69-0.86) 和 AutoGen (0.67-0.88) 的忠实度相对偏低，更容易生成超出检索文档的内容。
4. **信息整合 (en_int) 是最难的任务**：所有框架的 Context Precision 显著下降（0.28-0.32 vs 其他子集的 0.85-0.97），反映了多文档推理的固有难度。
5. **标准事实性 QA (en) 上所有框架召回率满分**：5 个框架 Context Recall 均为 1.0，说明在文档较为直接的情况下，检索步骤的效果都非常好。

---

### 评测观察

在评测过程中，我们通过抓包分析发现，各框架在使用相同 LLM 模型的情况下，**框架发起请求的流程比较相似**——本质上都是 Agent 调用搜索工具、获取上下文、生成回答的标准 RAG 流程。

需要注意的是：

- **数据集规模**：HuggingFace 评测集仅有 1900+ 文档和 54 个 QA 对。RGB 数据集提供了 300 + 100 + 100 = 500 个 QA 对，覆盖不同的检索场景。
- **Prompt 对分数影响**：不可否认，在当前数据集下系统提示词对 Agent 的执行影响比较大，同样也会对最终的分数产生很大的影响，我们保证了统一的系统提示词。
- **切块策略可能有影响**：排除系统提示词的影响后，不同框架的切块实现（chunk size、overlap、边界识别等）可能会对检索和回答质量产生影响，进而影响 Context Precision、Context Recall 等检索指标。
