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

We also use the [RGB Benchmark](https://github.com/chen700564/RGB) ([paper](https://arxiv.org/abs/2309.01431)) as a QA data source. RGB originally provides queries with pre-defined positive (relevant) and negative (irrelevant) passages for evaluating 4 RAG abilities: noise robustness, negative rejection, information integration, and counterfactual robustness.

The English portion includes 3 subsets with different QA characteristics:

| Subset | QA Count | Original Focus | Description |
|--------|----------|----------------|-------------|
| **en** | 300 | Noise robustness | Standard factual queries with clear single-source answers. Each query has well-defined positive passages and separate negative (irrelevant) passages. |
| **en_int** | 100 | Information integration | Queries whose answers require combining facts from **multiple** positive passages. The answer is scattered across several documents. |
| **en_fact** | 100 | Counterfactual robustness | Queries with both correct `positive` passages and `positive_wrong` passages that contain **altered facts** (e.g., replacing "Facebook" with "Apple"). |

> **Important: How we use RGB differs from the original paper.** In the original RGB evaluation, pre-selected positive + negative passages are directly concatenated and fed to the LLM as context. In our evaluation, we only load the **positive passages** into each framework's knowledge base as documents, and let the framework perform its own retrieval + generation pipeline end-to-end. This means:
> - **en**: Negative (noise) passages are **not** loaded into the knowledge base, so the "noise robustness" aspect is not directly tested. Instead, it serves as a **standard factual QA** benchmark.
> - **en_fact**: `positive_wrong` (counterfactual) passages are **not** loaded, so "counterfactual robustness" is not directly tested. Instead, it serves as another **factual QA** benchmark with different question characteristics.
> - **en_int**: The information integration challenge **is** preserved, since answers genuinely require synthesizing multiple retrieved documents.

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

#### 2.1 RGB-en: Standard Factual QA (300 QA Pairs)

Standard factual queries with clear single-source answers. (Original RGB focus: noise robustness, but negative passages are not loaded into the knowledge base in our end-to-end evaluation.)

**Answer Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.9783 | 0.9872 | 0.7554 | **0.9948** | 0.7816 | ✅ CrewAI |
| **Answer Relevancy** | 0.9493 | 0.9534 | **0.9612** | 0.9125 | 0.8866 | ✅ Agno |
| **Answer Correctness** | 0.7969 | **0.8462** | 0.7141 | 0.7680 | 0.6775 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5308 | **0.5401** | 0.5040 | 0.5327 | 0.5014 | ✅ tRPC-Agent-Go |

**Context Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Context Precision** | 0.9407 | 0.9539 | 0.9452 | 0.9393 | **0.9715** | ✅ AutoGen |
| **Context Recall** | **1.0000** | **1.0000** | **1.0000** | **1.0000** | **1.0000** | Tied |
| **Context Entity Recall** | 0.6378 | 0.6478 | **0.6583** | 0.6467 | 0.6328 | ✅ Agno |

#### 2.2 RGB-en_int: Multi-document Information Integration (100 QA Pairs)

Tests the model's ability to retrieve and synthesize information scattered across multiple documents. This is the subset where the original RGB challenge is best preserved in our end-to-end evaluation.

**Answer Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.9523 | **0.9743** | 0.8615 | 0.9623 | 0.8814 | ✅ tRPC-Agent-Go |
| **Answer Relevancy** | **0.9301** | 0.9061 | 0.9146 | 0.9094 | 0.8764 | ✅ LangChain |
| **Answer Correctness** | 0.7258 | **0.8059** | 0.7203 | 0.7277 | 0.7142 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5441 | **0.5683** | 0.5447 | 0.5546 | 0.5403 | ✅ tRPC-Agent-Go |

**Context Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Context Precision** | 0.2868 | 0.3118 | **0.3244** | 0.3069 | 0.3204 | ✅ Agno |
| **Context Recall** | 0.9133 | 0.9233 | 0.9300 | 0.9250 | **0.9350** | ✅ AutoGen |
| **Context Entity Recall** | 0.6317 | 0.6500 | 0.6350 | 0.6417 | **0.6933** | ✅ AutoGen |

#### 2.3 RGB-en_fact: Factual QA (100 QA Pairs)

Factual queries with different question characteristics from en subset. (Original RGB focus: counterfactual robustness, but `positive_wrong` passages are not loaded into the knowledge base in our end-to-end evaluation.)

**Answer Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.9529 | 0.9533 | 0.6966 | **0.9653** | 0.6714 | ✅ CrewAI |
| **Answer Relevancy** | 0.9204 | **0.9471** | 0.9317 | 0.8753 | 0.7334 | ✅ tRPC-Agent-Go |
| **Answer Correctness** | 0.7936 | **0.8467** | 0.6816 | 0.7334 | 0.6106 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5499 | **0.5672** | 0.5171 | 0.5500 | 0.5002 | ✅ tRPC-Agent-Go |

**Context Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Context Precision** | 0.8652 | 0.8641 | 0.8495 | **0.8694** | 0.8282 | ✅ CrewAI |
| **Context Recall** | **0.9900** | **0.9900** | 0.9700 | **0.9900** | **0.9900** | Tied |
| **Context Entity Recall** | 0.7300 | **0.7400** | 0.7100 | 0.7300 | 0.7300 | ✅ tRPC-Agent-Go |

#### RGB Summary & Analysis

**Average Across All 3 Subsets (en + en_int + en_fact):**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.9612 | 0.9716 | 0.7712 | **0.9741** | 0.7781 | ✅ CrewAI |
| **Answer Relevancy** | 0.9333 | 0.9355 | **0.9358** | 0.8991 | 0.8321 | ✅ Agno |
| **Answer Correctness** | 0.7721 | **0.8329** | 0.7053 | 0.7430 | 0.6674 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5416 | **0.5585** | 0.5219 | 0.5458 | 0.5140 | ✅ tRPC-Agent-Go |
| **Context Precision** | 0.6976 | **0.7099** | 0.7064 | 0.7052 | 0.7067 | ✅ tRPC-Agent-Go |
| **Context Recall** | 0.9678 | 0.9711 | 0.9667 | 0.9717 | **0.9750** | ✅ AutoGen |
| **Context Entity Recall** | 0.6665 | 0.6793 | 0.6678 | 0.6728 | **0.6854** | ✅ AutoGen |

**Cross-subset Winner Count** (all 5 frameworks across 3 subsets; tied categories like Context Recall on en/en_fact are excluded):

| Framework | 1st Place Count | Strongest Areas |
|-----------|----------------|-----------------|
| **tRPC-Agent-Go** | **9** | Answer Correctness (all), Answer Similarity (all), Faithfulness (en_int), Answer Relevancy (en_fact), Context Entity Recall (en_fact) |
| **AutoGen** | **3** | Context Precision (en), Context Recall (en_int), Context Entity Recall (en_int) |
| **CrewAI** | **3** | Faithfulness (en, en_fact), Context Precision (en_fact) |
| **Agno** | **3** | Answer Relevancy (en), Context Precision (en_int), Context Entity Recall (en) |
| **LangChain** | **1** | Answer Relevancy (en_int) |

**Key Findings:**

1. **tRPC-Agent-Go consistently leads in answer quality**: Ranks 1st in **Answer Correctness** and **Answer Similarity** across all 3 subsets, demonstrating the most accurate and reliable answers regardless of retrieval scenario.
2. **AutoGen excels in context retrieval**: Achieves the highest **Context Precision** on en (0.9715) and leads in **Context Recall** and **Context Entity Recall** on en_int, showing strong retrieval capabilities especially for multi-document scenarios.
3. **Faithfulness is strong across most frameworks**: LangChain, tRPC-Agent-Go, and CrewAI all achieve > 0.95 faithfulness, indicating minimal hallucination. Agno (0.69-0.86) and AutoGen (0.67-0.88) show relatively higher tendency to generate content beyond retrieved documents.
4. **Information Integration (en_int) is the hardest task**: Context Precision drops significantly for all frameworks (0.28-0.32 vs 0.85-0.97 in other subsets), reflecting the inherent difficulty of multi-document reasoning.
5. **All frameworks achieve perfect Context Recall on en**: Context Recall = 1.0 for all 5 frameworks on the standard factual QA subset, suggesting the retrieval step is highly effective when documents are straightforward.

---

### Evaluation Observations

Through packet capture analysis during evaluation, we found that all frameworks follow **fairly similar request flows** when using the same LLM model — essentially the standard RAG pipeline of agent calling search tool, retrieving context, and generating answers.

Key considerations:

- **Small dataset size**: The HuggingFace evaluation dataset contains only 1900+ documents and 54 QA pairs. The RGB dataset provides 300 + 100 + 100 = 500 QA pairs with controlled retrieval scenarios.
- **Prompt sensitivity**: It is undeniable that system prompts have a significant impact on agent execution under the current dataset, which in turn greatly affects the final scores. We have ensured unified system prompts across all frameworks.
- **Chunking strategy may have an impact**: After controlling for system prompt differences, different frameworks' chunking implementations (chunk size, overlap, boundary detection, etc.) may affect retrieval and answer quality, which in turn could influence Context Precision, Context Recall, and other retrieval metrics.
