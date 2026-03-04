# RAG Evaluation: tRPC-Agent-Go vs LangChain vs Agno vs CrewAI vs AutoGen

This directory contains a comprehensive evaluation framework for comparing RAG (Retrieval-Augmented Generation) systems using [RAGAS](https://docs.ragas.io/) metrics.

## Overview

We evaluate five RAG implementations with **identical configurations** to ensure a fair comparison:

- **tRPC-Agent-Go**: Our Go-based RAG implementation
- **LangChain**: Python-based reference implementation
- **Agno**: Python-based AI agent framework with built-in knowledge base support
- **CrewAI**: Python-based multi-agent framework with ChromaDB vector store
- **AutoGen**: Microsoft's Python-based multi-agent framework

## Quick Start

### Prerequisites

```bash
# Install Python dependencies
pip install -r requirements.txt

# Set environment variables
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="your-base-url"  # Optional
export MODEL_NAME="deepseek-v3.2"        # Optional, model for RAG
export EVAL_MODEL_NAME="gemini-3-flash"   # Optional, model for evaluation
export EMBEDDING_MODEL="server:274214"  # Optional

# PostgreSQL (PGVector) configuration
export PGVECTOR_HOST="127.0.0.1"
export PGVECTOR_PORT="5432"
export PGVECTOR_USER="root"
export PGVECTOR_PASSWORD="123"           # Default password
export PGVECTOR_DATABASE="vector"
```

### Run Evaluation

```bash
# Evaluate with LangChain
python3 main.py --kb=langchain

# Evaluate with tRPC-Agent-Go
python3 main.py --kb=trpc-agent-go

# Evaluate with Agno
python3 main.py --kb=agno

# Evaluate with AutoGen
python3 main.py --kb=autogen

# Full log output (show complete answers and contexts)
python3 main.py --kb=trpc-agent-go --max-qa=1 --full-log
```

## Configuration Alignment

All five systems use **identical parameters** to ensure fair comparison:


| Parameter | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen |
|-----------|-----------|---------------|------|--------|---------|
| **Temperature** | 0 | 0 | 0 | 0 | 0 |
| **Chunk Size** | 500 | 500 | 500 | 500 | 500 |
| **Chunk Overlap** | 50 | 50 | 50 | 50 | 50 |
| **Embedding Dimensions** | 1024 | 1024 | 1024 | 1024 | 1024 |
| **Vector Store** | PGVector | PGVector | PgVector | ChromaDB | PGVector |
| **Search Mode** | Vector | Vector (Hybrid disabled) | Vector | Vector | Vector |
| **Knowledge Base Build** | Native framework method | Native framework method | Native framework method | Native framework method | Native framework method |
| **Agent Type** | Agent + KB (ReAct disabled) | Agent + KB (ReAct disabled) | Agent + KB (ReAct disabled) | Agent + KB (ReAct disabled) | Agent + KB (ReAct disabled) |
| **Max Retrieval Results (k)** | 4 | 4 | 4 | 4 | 4 |

> 📝 **tRPC-Agent-Go Notes**:
>
> - **Search Mode**: tRPC-Agent-Go uses Hybrid Search (vector similarity + full-text search) by default, but for fair comparison with other frameworks, **Hybrid Search is disabled** in this evaluation. All frameworks use pure Vector Search (vector similarity only).

> 📝 **CrewAI Notes**:
>
> - **Vector Store**: CrewAI does not currently support PGVector for knowledge base construction, so ChromaDB is used as the vector store.
> - **Bug Fix**: CrewAI (v1.9.0) has a bug where it prioritizes `content` over `tool_calls` when the LLM (e.g., DeepSeek-V3.2) returns both simultaneously, causing the Agent to skip tool invocations. We applied a Monkey Patch to `LLM._handle_non_streaming_response` to prioritize `tool_calls`, ensuring fair evaluation. See `knowledge_system/crewai/knowledge_base.py` for details.

## System Prompt

To ensure fair comparison, all five systems are configured with **identical** instructions.

**Prompt for LangChain, Agno, tRPC-Agent-Go, CrewAI & AutoGen:**

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

## Datasets

### HuggingFace Documentation

We use the [HuggingFace Documentation](https://huggingface.co/datasets/m-ric/huggingface_doc) dataset.

**Important Filtering**: To ensure data quality and consistency, we specifically **filter and only use Markdown files** (`.md`) for both the source documents and the QA evaluation pairs.

- **Documents**: `m-ric/huggingface_doc` - Filtered for `.md` documentation files
- **QA Pairs**: `m-ric/huggingface_doc_qa_eval` - Filtered for questions whose source is a `.md` file

### RGB (Retrieval-Augmented Generation Benchmark)

We also use the [RGB Benchmark](https://github.com/chen700564/RGB) ([paper](https://arxiv.org/abs/2309.01431)) for evaluating RAG systems across different retrieval scenarios. RGB provides queries with pre-defined positive (relevant) and negative (irrelevant) passages, enabling controlled noise-rate evaluation.

The English portion includes 3 subsets testing different RAG capabilities:

| Subset | QA Count | Focus | Description |
|--------|----------|-------|-------------|
| **en** (Noise Robustness) | 300 | Noise robustness | Standard queries with positive/negative passages. Tests whether the model can correctly answer when retrieved context contains irrelevant noise documents. |
| **en_int** (Information Integration) | 100 | Information integration | Queries that require combining facts from **multiple** positive passages. Tests the model's ability to synthesize information scattered across documents. |
| **en_fact** (Counterfactual Robustness) | 100 | Counterfactual robustness | Includes `positive_wrong` passages that look similar to real answers but contain **altered facts** (e.g., replacing "Facebook" with "Apple"). Tests whether the model can identify and reject counterfactual information. |

## RAGAS Metrics

### Answer Quality


| Metric | Description | Higher value means |
|--------|-------------|-------------------|
| **Faithfulness** | Is the answer faithful to the retrieved context? (no hallucination) | Answer is more trustworthy, no fabricated content |
| **Answer Relevancy** | Is the answer relevant to the question? | Answer is more on-topic and complete |
| **Answer Correctness** | Is the answer correct compared to ground truth? | Answer is closer to correct answer |
| **Answer Similarity** | Semantic similarity to ground truth answer | Answer text expression is more similar |

### Context Quality


| Metric | Description | Higher value means |
|--------|-------------|-------------------|
| **Context Precision** | Are the retrieved documents relevant? | Retrieval is more precise, less noise |
| **Context Recall** | Are all relevant documents retrieved? | Retrieval is more comprehensive, no missing key info |
| **Context Entity Recall** | Are important entities from ground truth retrieved? | Key information retrieval is more complete |

### Simple Understanding

- **Faithfulness**: "Did you answer based only on retrieved content?" (check for hallucinations)
- **Answer Relevancy**: "Did you answer the question I asked?" (check for relevance)
- **Answer Correctness**: "Did you answer correctly?" (compare with ground truth)
- **Answer Similarity**: "Is your answer similar to the correct answer?" (semantic similarity)
- **Context Precision**: "Is the retrieved content useful?" (check retrieval quality)
- **Context Recall**: "Is the retrieved content sufficient?" (check for missing information)
- **Context Entity Recall**: "Are all key information retrieved?" (check entity coverage)

## Evaluation Results

### 1. HuggingFace Dataset (54 QA Pairs)

**Test Configuration:**

- **Dataset**: Full HuggingFace Markdown documentation dataset (54 QA)
- **Embedding Model**: `BGE-M3` (1024 dimensions)
- **Agent Model**: `DeepSeek-V3.2`
- **Evaluation Model**: `Gemini 3 Flash`

#### Answer Quality Metrics


| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.8614 | **0.9853** | 0.7213 | 0.9655 | 0.9113 | ✅ tRPC-Agent-Go |
| **Answer Relevancy** | 0.8529 | 0.8890 | 0.9013 | 0.8383 | **0.9040** | ✅ AutoGen |
| **Answer Correctness** | 0.6912 | **0.8299** | 0.6916 | 0.8101 | 0.7725 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.6740 | **0.7251** | 0.6772 | 0.6948 | 0.6830 | ✅ tRPC-Agent-Go |

#### Context Quality Metrics


| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Context Precision** | 0.6314 | **0.7278** | 0.7046 | 0.6673 | 0.6142 | ✅ tRPC-Agent-Go |
| **Context Recall** | 0.8333 | 0.9259 | 0.9259 | **0.9444** | **0.9444** | ✅ CrewAI / AutoGen |
| **Context Entity Recall** | 0.4138 | **0.5034** | 0.4331 | 0.3922 | 0.2902 | ✅ tRPC-Agent-Go |

#### Key Conclusions

1. **tRPC-Agent-Go achieves the best overall performance**: Ranks 1st in 5 out of 7 metrics — **Faithfulness (0.9853)**, **Answer Correctness (0.8299)**, **Answer Similarity (0.7251)**, **Context Precision (0.7278)**, and **Context Entity Recall (0.5034)**, leading in both answer quality and retrieval precision.
2. **AutoGen leads in relevancy**: **Answer Relevancy (0.9040)** ranks 1st (close to Agno's 0.9013), showing the best on-topic answers. Also ties for 1st in **Context Recall (0.9444)**.
3. **CrewAI has the highest recall**: **Context Recall (0.9444)** ties for 1st, indicating the most comprehensive retrieval recall.
4. **Agno excels in relevancy**: **Answer Relevancy (0.9013)** ranks 2nd, demonstrating excellent on-topic answers.
5. **Each framework has its strengths**: LangChain provides a stable and balanced baseline, with each framework excelling in different dimensions.

---

### 2. RGB Dataset

**Test Configuration:**

- **Dataset**: [RGB Benchmark](https://github.com/chen700564/RGB) (English subsets)
- **Embedding Model**: `BGE-M3` (1024 dimensions)
- **Agent Model**: `DeepSeek-V3.2`
- **Evaluation Model**: `Gemini 3 Flash`

> **Note**: AutoGen results are pending (evaluation in progress).

#### 2.1 RGB-en: Noise Robustness (300 QA Pairs)

Tests whether the model can correctly answer questions when retrieved documents contain irrelevant noise.

**Answer Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | Best |
|--------|-----------|---------------|------|--------|------|
| **Faithfulness** | **0.9783** | 0.9872 | 0.7554 | **0.9948** | ✅ CrewAI |
| **Answer Relevancy** | 0.9493 | 0.9534 | **0.9612** | 0.9125 | ✅ Agno |
| **Answer Correctness** | 0.7969 | **0.8462** | 0.7141 | 0.7680 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5308 | **0.5401** | 0.5040 | 0.5327 | ✅ tRPC-Agent-Go |

**Context Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | Best |
|--------|-----------|---------------|------|--------|------|
| **Context Precision** | 0.9407 | **0.9539** | 0.9452 | 0.9393 | ✅ tRPC-Agent-Go |
| **Context Recall** | **1.0000** | **1.0000** | **1.0000** | **1.0000** | Tied |
| **Context Entity Recall** | 0.6378 | **0.6478** | 0.6583 | 0.6467 | ✅ Agno |

#### 2.2 RGB-en_int: Information Integration (100 QA Pairs)

Tests the model's ability to synthesize information scattered across multiple documents.

**Answer Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | Best |
|--------|-----------|---------------|------|--------|------|
| **Faithfulness** | 0.9523 | **0.9743** | 0.8615 | 0.9623 | ✅ tRPC-Agent-Go |
| **Answer Relevancy** | 0.9301 | 0.9061 | 0.9146 | **0.9094** | ✅ LangChain |
| **Answer Correctness** | 0.7258 | **0.8059** | 0.7203 | 0.7277 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5441 | **0.5683** | 0.5447 | 0.5546 | ✅ tRPC-Agent-Go |

**Context Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | Best |
|--------|-----------|---------------|------|--------|------|
| **Context Precision** | 0.2868 | **0.3118** | **0.3244** | 0.3069 | ✅ Agno |
| **Context Recall** | 0.9133 | **0.9233** | **0.9300** | 0.9250 | ✅ Agno |
| **Context Entity Recall** | 0.6317 | **0.6500** | 0.6350 | 0.6417 | ✅ tRPC-Agent-Go |

#### 2.3 RGB-en_fact: Counterfactual Robustness (100 QA Pairs)

Tests whether the model can identify and reject counterfactual (altered facts) information in retrieved documents.

**Answer Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | Best |
|--------|-----------|---------------|------|--------|------|
| **Faithfulness** | 0.9529 | 0.9533 | 0.6966 | **0.9653** | ✅ CrewAI |
| **Answer Relevancy** | 0.9204 | **0.9471** | 0.9317 | 0.8753 | ✅ tRPC-Agent-Go |
| **Answer Correctness** | 0.7936 | **0.8467** | 0.6816 | 0.7334 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5499 | **0.5672** | 0.5171 | 0.5500 | ✅ tRPC-Agent-Go |

**Context Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | Best |
|--------|-----------|---------------|------|--------|------|
| **Context Precision** | **0.8652** | 0.8641 | 0.8495 | **0.8694** | ✅ CrewAI |
| **Context Recall** | **0.9900** | **0.9900** | 0.9700 | **0.9900** | Tied |
| **Context Entity Recall** | 0.7300 | **0.7400** | 0.7100 | 0.7300 | ✅ tRPC-Agent-Go |

#### RGB Summary & Analysis

**Average Across All 3 Subsets (en + en_int + en_fact):**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | Best |
|--------|-----------|---------------|------|--------|------|
| **Faithfulness** | 0.9612 | **0.9716** | 0.7712 | **0.9741** | ✅ CrewAI |
| **Answer Relevancy** | 0.9333 | **0.9355** | **0.9358** | 0.8991 | ✅ Agno |
| **Answer Correctness** | 0.7721 | **0.8329** | 0.7053 | 0.7430 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5416 | **0.5585** | 0.5219 | 0.5458 | ✅ tRPC-Agent-Go |
| **Context Precision** | 0.6976 | **0.7099** | 0.7064 | 0.7052 | ✅ tRPC-Agent-Go |
| **Context Recall** | 0.9678 | **0.9711** | 0.9667 | **0.9717** | ✅ CrewAI |
| **Context Entity Recall** | 0.6665 | **0.6793** | 0.6678 | 0.6728 | ✅ tRPC-Agent-Go |

**Cross-subset Winner Count** (1st place across all 3 subsets x 7 metrics = 21 comparisons):

| Framework | 1st Place Count | Strongest Areas |
|-----------|----------------|-----------------|
| **tRPC-Agent-Go** | **11** | Answer Correctness, Answer Similarity, Faithfulness, Context Entity Recall |
| **Agno** | **4** | Answer Relevancy, Context Precision (en_int), Context Recall (en_int) |
| **CrewAI** | **3** | Faithfulness (en, en_fact), Context Precision (en_fact) |
| **LangChain** | **2** | Answer Relevancy (en_int), Context Precision (en_fact) |

**Key Findings:**

1. **tRPC-Agent-Go consistently leads in answer quality**: Ranks 1st in **Answer Correctness** and **Answer Similarity** across all 3 subsets, demonstrating the most accurate and reliable answers regardless of retrieval scenario.
2. **Faithfulness is strong across frameworks**: All frameworks (except Agno) achieve > 0.95 faithfulness, indicating minimal hallucination. CrewAI edges ahead in the noise robustness and counterfactual subsets.
3. **Information Integration (en_int) is the hardest task**: Context Precision drops significantly for all frameworks (0.28-0.32 vs 0.85-0.95 in other subsets), reflecting the inherent difficulty of multi-document reasoning. Agno performs relatively better here.
4. **All frameworks achieve near-perfect Context Recall on en**: Context Recall = 1.0 for all frameworks on the noise robustness subset, suggesting the retrieval step is highly effective when documents are straightforward.
5. **Agno shows weaker faithfulness**: Consistently lower Faithfulness (0.69-0.86) across all subsets indicates a higher tendency to generate content beyond retrieved documents.

---

### Evaluation Observations

Through packet capture analysis during evaluation, we found that all frameworks follow **fairly similar request flows** when using the same LLM model — essentially the standard RAG pipeline of agent calling search tool, retrieving context, and generating answers.

Key considerations:

- **Small dataset size**: The HuggingFace evaluation dataset contains only 1900+ documents and 54 QA pairs. The RGB dataset provides 300 + 100 + 100 = 500 QA pairs with controlled retrieval scenarios.
- **Prompt sensitivity**: It is undeniable that system prompts have a significant impact on agent execution under the current dataset, which in turn greatly affects the final scores. We have ensured unified system prompts across all frameworks.
- **Chunking strategy may have an impact**: After controlling for system prompt differences, different frameworks' chunking implementations (chunk size, overlap, boundary detection, etc.) may affect retrieval and answer quality, which in turn could influence Context Precision, Context Recall, and other retrieval metrics.
