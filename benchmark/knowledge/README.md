# RAG Evaluation: tRPC-Agent-Go vs LangChain vs LangChain-Chain vs Agno vs CrewAI vs AutoGen

This directory contains a comprehensive evaluation framework for comparing RAG (Retrieval-Augmented Generation) systems using [RAGAS](https://docs.ragas.io/) metrics.

## Overview

We evaluate six RAG implementations with **identical configurations** to ensure a fair comparison:

- **tRPC-Agent-Go**: Our Go-based RAG implementation
- **LangChain**: Python-based Agent reference implementation
- **LangChain-Chain**: Deterministic LCEL chain pipeline (retrieve → prompt → LLM, no agent loop)
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

# Evaluate with LangChain-Chain (deterministic chain pipeline)
python3 main.py --kb=langchain_chain

# Evaluate with tRPC-Agent-Go
python3 main.py --kb=trpc-agent-go

# Evaluate with Agno
python3 main.py --kb=agno

# Evaluate with AutoGen
python3 main.py --kb=autogen
```

### Run Vertical Evaluation

Vertical evaluation runs tRPC-Agent-Go-only ablation experiments (hybrid search weight sweep, RRF mode). It automatically builds and manages the Go service for each experiment configuration.

```bash
# Run hybrid weight ablation (11 weight combinations from pure text to pure vector)
python3 -m vertical_eval.main --suite hybrid_weight

# Run RRF fusion experiment
python3 -m vertical_eval.main --suite hybrid_rrf

# Run all suites
python3 -m vertical_eval.main --suite all

# Skip document loading (if documents are already loaded in PGVector)
python3 -m vertical_eval.main --suite hybrid_weight --skip-load

# Run specific experiments only
python3 -m vertical_eval.main --suite hybrid_weight --experiments hybrid_v80_t20 hybrid_v90_t10

# Override PGVector table name
python3 -m vertical_eval.main --suite hybrid_rrf --skip-load --pg-table veval_hw_rrf
```

Results are saved to `vertical_eval/results/<suite>_<timestamp>/` with per-experiment JSON files and a combined markdown report.

## Configuration Alignment

All six systems use **identical parameters** to ensure fair comparison:


| Parameter | LangChain | LangChain-Chain | tRPC-Agent-Go | Agno | CrewAI | AutoGen |
|-----------|-----------|-----------------|---------------|------|--------|---------|
| **Temperature** | 0 | 0 | 0 | 0 | 0 | 0 |
| **Chunk Size** | 500 | 500 | 500 | 500 | 500 | 500 |
| **Chunk Overlap** | 50 | 50 | 50 | 50 | 50 | 50 |
| **Embedding Dimensions** | 1024 | 1024 | 1024 | 1024 | 1024 | 1024 |
| **Vector Store** | PGVector | PGVector | PGVector | PgVector | ChromaDB | PGVector |
| **Search Mode** | Vector | Vector | Vector (Hybrid disabled) | Vector | Vector | Vector |
| **Knowledge Base Build** | Native framework method | Native framework method | Native framework method | Native framework method | Native framework method | Native framework method |
| **Agent Type** | Agent + KB (ReAct disabled) | Chain (no agent loop) | Agent + KB (ReAct disabled) | Agent + KB (ReAct disabled) | Agent + KB (ReAct disabled) | Agent + KB (ReAct disabled) |
| **Max Retrieval Results (k)** | 4 | 4 | 4 | 4 | 4 | 4 |

> 📝 **tRPC-Agent-Go Notes**:
>
> - **Search Mode**: tRPC-Agent-Go uses Hybrid Search (vector similarity + full-text search) by default, but for fair comparison with other frameworks, **Hybrid Search is disabled** in this evaluation. All frameworks use pure Vector Search (vector similarity only).

> 📝 **LangChain-Chain Notes**:
>
> - **Pipeline Mode**: LangChain-Chain uses LCEL (LangChain Expression Language) to build a deterministic chain pipeline (retrieve → format → prompt → LLM → parse), without agent loops or tool calling. Each question triggers exactly one retrieval, and the LLM receives the exact same prompt template, making the flow fully deterministic and reproducible.

> 📝 **CrewAI Notes**:
>
> - **Vector Store**: CrewAI does not currently support PGVector for knowledge base construction, so ChromaDB is used as the vector store.
> - **Bug Fix**: CrewAI (v1.9.0) has a bug where it prioritizes `content` over `tool_calls` when the LLM (e.g., DeepSeek-V3.2) returns both simultaneously, causing the Agent to skip tool invocations. We applied a Monkey Patch to `LLM._handle_non_streaming_response` to prioritize `tool_calls`, ensuring fair evaluation. See `knowledge_system/crewai/knowledge_base.py` for details.

## System Prompt

To ensure fair comparison, all six systems are configured with **identical** instructions.

**Prompt for LangChain, LangChain-Chain, Agno, tRPC-Agent-Go, CrewAI & AutoGen:**

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

> **Important: How we use RGB differs from the original paper.** In the original RGB evaluation, pre-selected positive + negative passages are directly concatenated and fed to the LLM as context. In our evaluation, we load `positive`, `negative`, and `positive_wrong` passages (when present) into each framework's knowledge base as documents, and let the framework perform its own retrieval + generation pipeline end-to-end. This means the knowledge base contains relevant information, noise, and counterfactual interference simultaneously, which is closer to a real-world RAG retrieval environment.
>
> **Loading strategy (consistent with code):**
> - **Document source**: Original JSON files from `en / en_int / en_fact` subsets.
> - **Loading scope**: For each sample, all three passage types — `positive`, `negative`, `positive_wrong` — are loaded (when the field exists).
> - **Deduplication**: Global text-level deduplication before writing to the knowledge base document directory, avoiding duplicate chunks.
> - **en_int**: The information integration challenge is preserved, since answers genuinely require synthesizing multiple retrieved documents.

### MultiHop-RAG

We use the [MultiHop-RAG](https://github.com/yixuantt/MultiHop-RAG) ([paper](https://arxiv.org/abs/2401.15391)) benchmark dataset, proposed by Yixuan Tang and Yi Yang in 2024. MultiHop-RAG is designed to evaluate RAG systems on **multi-hop queries** — questions that require reasoning over **2-4 documents** to arrive at the correct answer, making it a key benchmark for testing complex reasoning in RAG systems.

**Dataset Structure:**

The dataset consists of two parts, both auto-downloaded from the GitHub repository's `dataset/` directory:

1. **`corpus.json` — News Article Corpus (609 articles)**
   - Sourced from 48 news outlets (The New York Times, BBC News, TechCrunch, The Verge, Financial Times, etc.), covering technology, sports, business, entertainment, and more
   - Each article contains `title`, `body`, `source` (outlet name), `published_at`, `category`, and other metadata
   - We export each article as an individual `.txt` file and load them into each framework's knowledge base

2. **`MultiHopRAG.json` — Multi-hop Query Set (2556 queries)**
   - Each QA entry contains `query`, `answer` (ground truth), `question_type`, and `evidence_list`
   - Each item in `evidence_list` includes a `fact` (key evidence passage) along with source article metadata (title, source, url, etc.), representing the gold-standard evidence required to answer the query

**Question Types and Selection:**

The original dataset has 4 question types. We exclude `null_query` (301 entries) — questions that cannot be answered from the corpus — and take the first 150 from each remaining type:

| Question Type | Selected / Total | Description |
|---------------|-----------------|-------------|
| **comparison_query** | 150 / 856 | Cross-document comparison (e.g., "Which was released earlier, A or B?") |
| **inference_query** | 150 / 816 | Inference from facts scattered across documents (e.g., "Who is the person associated with event X?") |
| **temporal_query** | 150 / 583 | Temporal reasoning across documents (e.g., "Did event X happen before or after event Y?") |

**Total: 450 QA pairs** (3 types × 150) used for evaluation. Gold evidence is derived from the `fact` field in `evidence_list`.

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
- **Evaluation Model**: `Qwen3.5-397B-A17B`

#### Answer Quality Metrics


| Metric | LangChain | LangChain-Chain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|-----------------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.9722 | 0.9167 | **0.9815** | 0.9660 | 0.9753 | 0.8688 | ✅ tRPC-Agent-Go |
| **Answer Relevancy** | 0.8914 | 0.6573 | 0.8799 | **0.8917** | 0.7820 | 0.8304 | ✅ Agno |
| **Answer Correctness** | 0.6984 | 0.7801 | **0.8104** | 0.7741 | 0.7575 | 0.6707 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.6758 | **0.8373** | 0.7240 | 0.6989 | 0.7025 | 0.6653 | ✅ LangChain-Chain |

#### Context Quality Metrics


| Metric | LangChain | LangChain-Chain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|-----------------|---------------|------|--------|---------|------|
| **Context Precision** | 0.6051 | **0.7716** | 0.7098 | 0.6712 | 0.6391 | 0.5445 | ✅ LangChain-Chain |
| **Context Recall** | 0.8704 | 0.8704 | **0.9444** | 0.9259 | **0.9444** | 0.8889 | ✅ tRPC-Agent-Go / CrewAI |
| **Context Entity Recall** | 0.4898 | **0.5093** | 0.4867 | 0.4707 | 0.4599 | 0.3833 | ✅ LangChain-Chain |

#### Key Conclusions

1. **tRPC-Agent-Go leads comprehensively**: **Faithfulness (0.9815)**, **Answer Correctness (0.8104)**, **Answer Similarity (0.7240)**, and **Context Precision (0.7098)** all rank among the top, with **Context Recall (0.9444)** tied for 1st with CrewAI. Strongest overall performance.
2. **LangChain-Chain excels in similarity and context quality**: Ranks 1st in 3 metrics — **Answer Similarity (0.8373)**, **Context Precision (0.7716)**, and **Context Entity Recall (0.5093)**. Its deterministic chain pipeline (no agent loop) delivers the most precise context retrieval.
3. **Agno leads in relevancy**: **Answer Relevancy (0.8917)** ranks 1st.
4. **LangChain leads in entity recall**: **Context Entity Recall (0.4898)** ranks 1st among non-Chain frameworks.
5. **AutoGen underperforms on this dataset**: All metrics are lower than other frameworks, possibly related to its retrieval strategy on small-scale knowledge bases.

---

### 2. RGB Dataset

**Test Configuration:**

- **Dataset**: [RGB Benchmark](https://github.com/chen700564/RGB) (English subsets)
- **Embedding Model**: `BGE-M3` (1024 dimensions)
- **Agent Model**: `DeepSeek-V3.2`
- **Evaluation Model**: `Qwen3.5-397B-A17B`

#### 2.1 RGB-en: Standard Factual QA (300 QA Pairs)

Standard factual queries with clear single-source answers. (Original RGB focus: noise robustness; in our end-to-end evaluation, `negative` passages are loaded into the knowledge base alongside `positive` passages, so noise interference is preserved.)

**Answer Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.9677 | 0.9861 | 0.9797 | **0.9925** | 0.7664 | ✅ CrewAI |
| **Answer Relevancy** | 0.9385 | **0.9543** | 0.9485 | 0.9073 | 0.8544 | ✅ tRPC-Agent-Go |
| **Answer Correctness** | 0.7901 | **0.8379** | 0.8072 | 0.7608 | 0.6683 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5329 | **0.5421** | 0.5421 | 0.5305 | 0.4923 | ✅ tRPC-Agent-Go / Agno |

**Context Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Context Precision** | 0.8777 | **0.8973** | 0.8691 | 0.8665 | 0.8876 | ✅ tRPC-Agent-Go |
| **Context Recall** | 0.9965 | **1.0000** | 0.9792 | 0.9967 | 0.9933 | ✅ tRPC-Agent-Go |
| **Context Entity Recall** | **0.6467** | 0.6461 | 0.6416 | 0.6350 | 0.6278 | ✅ LangChain |

#### 2.2 RGB-en_int: Multi-document Information Integration (100 QA Pairs)

Tests the model's ability to retrieve and synthesize information scattered across multiple documents. This is the subset where the original RGB challenge is best preserved in our end-to-end evaluation.

> **LangChain-Chain as Single-Retrieval Baseline**: In this subset, we additionally include **LangChain-Chain** (deterministic chain pipeline with exactly one retrieval per query) as a baseline to validate the advantage of **agentic multi-step retrieval** over **single-retrieval** approaches. Since en_int queries require synthesizing information from multiple documents, an agentic framework that can perform iterative searches should significantly outperform a single-pass retrieval pipeline.

**Answer Quality:**

| Metric | LangChain-Chain *(Baseline)* | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|------------------------------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.9325 | 0.9647 | 0.9716 | 0.9481 | **0.9740** | 0.9130 | ✅ CrewAI |
| **Answer Relevancy** | 0.7559 | 0.9063 | 0.9196 | 0.8754 | 0.9238 | **0.9327** | ✅ AutoGen |
| **Answer Correctness** | 0.6677 | 0.7244 | **0.7494** | 0.6960 | 0.6907 | 0.7330 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5417 | 0.5401 | **0.5599** | 0.5411 | 0.5508 | 0.5414 | ✅ tRPC-Agent-Go |

**Context Quality:**

| Metric | LangChain-Chain *(Baseline)* | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|------------------------------|-----------|---------------|------|--------|---------|------|
| **Context Precision** | **0.3136** | 0.2718 | 0.2886 | 0.2943 | 0.2925 | 0.2816 | ✅ LangChain-Chain |
| **Context Recall** | 0.7000 | 0.8776 | 0.8800 | 0.8083 | **0.9150** | 0.9033 | ✅ CrewAI |
| **Context Entity Recall** | 0.4917 | 0.5790 | 0.5933 | 0.5833 | 0.6167 | **0.6317** | ✅ AutoGen |

#### 2.3 RGB-en_fact: Factual QA (100 QA Pairs)

Factual queries with different question characteristics from en subset. (Original RGB focus: counterfactual robustness; in our end-to-end evaluation, `positive_wrong` passages are loaded into the knowledge base alongside `positive` passages, so counterfactual interference is preserved.)

**Answer Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | **0.9719** | 0.9608 | 0.9505 | 0.9648 | 0.7810 | ✅ LangChain |
| **Answer Relevancy** | 0.8370 | **0.9163** | 0.6748 | 0.8594 | 0.7627 | ✅ tRPC-Agent-Go |
| **Answer Correctness** | 0.7226 | **0.8055** | 0.6148 | 0.6755 | 0.6634 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5330 | **0.5462** | 0.5062 | 0.5266 | 0.5058 | ✅ tRPC-Agent-Go |

**Context Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Context Precision** | 0.6329 | **0.6466** | 0.6050 | 0.6308 | 0.6311 | ✅ tRPC-Agent-Go |
| **Context Recall** | 0.9796 | **0.9900** | 0.8700 | **0.9900** | **0.9900** | ✅ tRPC-Agent-Go / CrewAI / AutoGen |
| **Context Entity Recall** | 0.7143 | 0.7100 | 0.6100 | **0.7200** | **0.7200** | ✅ CrewAI / AutoGen |

#### RGB Summary & Analysis

> **Note**: LangChain-Chain is excluded from the RGB summary statistics below, as it serves only as a single-retrieval baseline in the en_int subset. See Section 2.2 for the agentic vs single-retrieval comparison.

**Simple Average Across All 3 Subsets (en + en_int + en_fact):**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.9681 | 0.9728 | 0.9594 | **0.9771** | 0.8201 | ✅ CrewAI |
| **Answer Relevancy** | 0.8939 | **0.9301** | 0.8329 | 0.8968 | 0.8499 | ✅ tRPC-Agent-Go |
| **Answer Correctness** | 0.7457 | **0.7976** | 0.7060 | 0.7090 | 0.6882 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5353 | **0.5494** | 0.5298 | 0.5360 | 0.5132 | ✅ tRPC-Agent-Go |
| **Context Precision** | 0.5941 | **0.6108** | 0.5895 | 0.5966 | 0.6001 | ✅ tRPC-Agent-Go |
| **Context Recall** | 0.9512 | 0.9567 | 0.8858 | **0.9672** | 0.9622 | ✅ CrewAI |
| **Context Entity Recall** | 0.6467 | 0.6498 | 0.6116 | 0.6572 | **0.6598** | ✅ AutoGen |

**Weighted Average Across All 3 Subsets** (en×300 + en_int×100 + en_fact×100, total 500 QA pairs):

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.9679 | 0.9781 | 0.9675 | **0.9833** | 0.7986 | ✅ CrewAI |
| **Answer Relevancy** | 0.9118 | **0.9398** | 0.8791 | 0.9010 | 0.8517 | ✅ tRPC-Agent-Go |
| **Answer Correctness** | 0.7635 | **0.8137** | 0.7465 | 0.7297 | 0.6803 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5344 | **0.5465** | 0.5347 | 0.5338 | 0.5048 | ✅ tRPC-Agent-Go |
| **Context Precision** | 0.7076 | **0.7254** | 0.7013 | 0.7046 | 0.7151 | ✅ tRPC-Agent-Go |
| **Context Recall** | 0.9693 | 0.9740 | 0.9232 | **0.9790** | 0.9746 | ✅ CrewAI |
| **Context Entity Recall** | 0.6467 | **0.6483** | 0.6236 | **0.6483** | 0.6470 | ✅ tRPC-Agent-Go / CrewAI |

**Cross-subset Winner Count** (5 agentic frameworks across 3 subsets; LangChain-Chain counted only in en_int as baseline; tied categories with 3+ winners are excluded):

| Framework | 1st Place Count | Strongest Areas |
|-----------|----------------|-----------------|
| **tRPC-Agent-Go** | **11** | Answer Relevancy (en, en_fact), Answer Correctness (en, en_int, en_fact), Answer Similarity (en†, en_int, en_fact), Context Precision (en, en_fact), Context Recall (en) |
| **CrewAI** | **4** | Faithfulness (en, en_int), Context Recall (en_int), Context Entity Recall (en_fact†) |
| **AutoGen** | **3** | Answer Relevancy (en_int), Context Entity Recall (en_int, en_fact†) |
| **LangChain** | **2** | Context Entity Recall (en), Faithfulness (en_fact) |
| **Agno** | **1** | Answer Similarity (en†) |
| **LangChain-Chain** *(baseline)* | **1** | Context Precision (en_int) |

> † = tied with another framework

**Key Findings:**

1. **tRPC-Agent-Go ranks 1st in 11 categories across 3 subsets**: Average Answer Relevancy (0.9301), Answer Correctness (0.7976), Answer Similarity (0.5494), Context Precision (0.6108) are all highest; en subset Context Recall reaches 1.0000.
2. **Agentic multi-step retrieval vs single-retrieval on en_int**: LangChain-Chain (single retrieval) scores Answer Relevancy 0.7559, Answer Correctness 0.6677, Context Recall 0.7000; all agentic frameworks score higher (e.g., tRPC 0.9196 / 0.7494 / 0.8800 respectively).
3. **CrewAI ranks 1st in 4 categories**: Faithfulness (en: 0.9925, en_int: 0.9740) and Context Recall (en_int: 0.9150).
4. **Faithfulness across frameworks**: LangChain, tRPC-Agent-Go, CrewAI, and Agno average > 0.93; AutoGen averages 0.82.
5. **en_int subset has the lowest Context Precision**: All frameworks score 0.27–0.31, compared to 0.59–0.90 in other subsets.
6. **Weighted average results**: tRPC-Agent-Go ranks 1st in 5 of 7 metrics (Answer Relevancy 0.9398, Answer Correctness 0.8137, Answer Similarity 0.5465, Context Precision 0.7254, Context Entity Recall 0.6483 tied with CrewAI).

---

### 3. MultiHop-RAG Dataset (450 QA Pairs)

**Test Configuration:**

- **Dataset**: [MultiHop-RAG](https://github.com/yixuantt/MultiHop-RAG) ([paper](https://arxiv.org/abs/2401.15391)) — 609 news-article corpus, 450 multi-hop QA pairs (150 per question type)
- **Embedding Model**: `BGE-M3` (1024 dimensions)
- **Agent Model**: `DeepSeek-V3.2`
- **Evaluation Model**: `Qwen3.5-397B-A17B`

> **LangChain-Chain as single-retrieval baseline**: Similar to the RGB-en_int subset, we additionally include LangChain-Chain (deterministic chain pipeline, exactly one retrieval per query) as a baseline to demonstrate the advantage of agentic multi-step retrieval in multi-hop reasoning scenarios.

**Answer Quality:**

| Metric | LangChain-Chain *(baseline)* | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|------------------------------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.4672 | 0.7639 | 0.7060 | **0.7887** | 0.7460 | 0.7468 | ✅ Agno |
| **Answer Relevancy** | 0.5213 | 0.5955 | **0.6424** | 0.5638 | 0.5639 | 0.5342 | ✅ tRPC-Agent-Go |
| **Answer Correctness** | 0.4677 | 0.4243 | **0.4984** | 0.4524 | 0.4371 | 0.4495 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | **0.5118** | 0.4376 | 0.4699 | 0.4715 | 0.4615 | 0.4904 | ✅ LangChain-Chain |

**Context Quality:**

| Metric | LangChain-Chain *(baseline)* | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|------------------------------|-----------|---------------|------|--------|---------|------|
| **Context Precision** | **0.3820** | 0.3209 | 0.3574 | 0.3526 | 0.3409 | 0.3520 | ✅ LangChain-Chain |
| **Context Recall** | 0.5644 | 0.7416 | 0.7733 | 0.7756 | 0.7523 | **0.8111** | ✅ AutoGen |
| **Context Entity Recall** | 0.2422 | **0.2711** | 0.2667 | 0.2622 | 0.2599 | 0.2556 | ✅ LangChain |

**Observations:**

1. **Multi-hop queries are significantly harder than single-hop**: All metrics drop substantially compared to RGB and HuggingFace datasets, reflecting the inherent difficulty of reasoning across multiple documents.
2. **tRPC-Agent-Go leads in answer quality**: Ranks 1st in **Answer Relevancy** (0.6424) and **Answer Correctness** (0.4984), continuing its advantage in generating accurate answers.
3. **AutoGen has the strongest context recall**: **Context Recall** (0.8111) significantly outperforms all other frameworks, indicating more comprehensive evidence retrieval.
4. **Agno has the highest faithfulness**: **Faithfulness** (0.7887) ranks 1st, indicating better adherence to retrieved content for multi-hop reasoning.
5. **Context Precision is universally low (~0.32-0.38)**: Similar to the RGB-en_int subset, multi-hop queries push all frameworks' retrieval precision down, as relevant evidence is scattered across multiple documents.
6. **Agentic multi-step retrieval vs single-retrieval**: LangChain-Chain (single retrieval) scores only 0.4672 Faithfulness and 0.5644 Context Recall, far below all agentic frameworks (Faithfulness 0.70–0.79, Context Recall 0.74–0.81). However, LangChain-Chain ranks 1st in Answer Similarity (0.5118) and Context Precision (0.3820), consistent with the RGB-en_int pattern — single retrieval returns more compact context with higher precision, but significantly lacks recall and faithfulness.

---

### 4. Vertical Evaluation: tRPC-Agent-Go Hybrid Search Weight Ablation

To find the optimal weight ratio for PGVector Hybrid Search (vector similarity + sparse text retrieval) in tRPC-Agent-Go, we designed a gradient ablation experiment with 11 steps ranging from pure text (`v0_t100`) to pure vector (`v100_t0`).

**Test Configuration:**
- **Dataset**: Full HuggingFace Markdown documentation dataset (54 QA)
- **Retrieval Configuration**: Top K = 4
- **Embedding / Agent / Eval Models**: Same as the main evaluation

**Results (sorted by vector weight from low to high):**

| Config (vector_weight\_text_weight) | Faithfulness | Answer Relevancy | Answer Correctness | Answer Similarity | Context Precision | Context Recall | Context Entity Recall |
| ----------------------------------- | ------------ | ---------------- | ------------------ | ----------------- | ----------------- | -------------- | --------------------- |
| **hybrid_v0_t100** (pure text)      | 0.8920 | 0.7586 | 0.6588 | 0.6741 | 0.4389 | 0.7925 | 0.3302 |
| **hybrid_v10_t90**                  | 0.9064 | 0.7677 | 0.6875 | 0.6741 | 0.5243 | 0.8113 | 0.3519 |
| **hybrid_v20_t80**                  | 0.9143 | 0.8164 | 0.6861 | 0.6827 | 0.5592 | 0.8519 | 0.3951 |
| **hybrid_v30_t70**                  | 0.9226 | 0.7842 | 0.7188 | 0.6883 | 0.5980 | 0.8704 | 0.3962 |
| **hybrid_v40_t60**                  | 0.9681 | 0.7919 | 0.7333 | 0.6939 | 0.6077 | 0.8679 | 0.4031 |
| **hybrid_v50_t50**                  | 0.9346 | 0.7948 | 0.7365 | 0.7064 | 0.6441 | 0.8889 | 0.4414 |
| **hybrid_v60_t40**                  | 0.9685 | 0.8162 | 0.7503 | 0.7027 | 0.6772 | 0.8889 | 0.4759 |
| **hybrid_v70_t30**                  | 0.9593 | 0.8495 | 0.7706 | 0.7107 | 0.7095 | 0.9259 | 0.4883 |
| **hybrid_v80_t20**                  | **0.9753** | 0.8830 | 0.7848 | 0.7094 | 0.7205 | 0.9259 | 0.4815 |
| **hybrid_v90_t10**                  | 0.9506 | 0.8616 | 0.7953 | 0.7206 | **0.7320** | 0.9259 | 0.4552 |
| **hybrid_v100_t0** (pure vector)    | 0.9748 | **0.8635** | **0.8072** | **0.7229** | 0.6991 | **0.9630** | **0.5219** |

**Key Findings & Analysis:**

1. **Pure vector retrieval (v100_t0) achieves the best overall performance**:
   On the full 54 QA dataset, pure vector retrieval ranks 1st in **Answer Relevancy (0.8635)**, **Answer Correctness (0.8072)**, **Answer Similarity (0.7229)**, **Context Recall (0.9630)**, and **Context Entity Recall (0.5219)** — 5 out of 7 metrics, leading in overall answer quality.
2. **High vector weight range (v80-v100) forms a performance plateau**:
   Metrics vary only slightly between v80_t20 and v100_t0 (e.g., Answer Correctness ranges from 0.78 to 0.81), indicating that system performance stabilizes when vector weight ≥ 0.8. Notably, v80_t20 achieves the highest Faithfulness (0.9753) and v90_t10 achieves the highest Context Precision (0.7320).
3. **Pure text retrieval (v0_t100) still performs worst**:
   Sparse text retrieval achieves only 0.4389 Context Precision, 0.7925 Context Recall, and 0.6588 Answer Correctness — the lowest across all configurations.
4. **"Text penalty" phenomenon remains significant**:
   A clear monotonic decline is visible from v100 to v0: Answer Correctness drops from 0.8072 (v100) to 0.6588 (v0), Context Precision from 0.6991 to 0.4389. This trend is smoother and more consistent on the full dataset compared to the sampled subset.

**Practical Recommendations**:
For standard RAG scenarios (especially systems with high-quality LLMs and embeddings), **it is recommended to set the vector retrieval weight as the dominant factor (≥0.8)**. The v80_t20 to v100_t0 range all perform excellently and can be fine-tuned based on specific scenarios. Only consider increasing sparse text retrieval weight in scenarios with highly specialized jargon or non-semantic identifiers (e.g., product codes).

### 5. Vertical Evaluation: Reciprocal Rank Fusion (RRF) Mode

In addition to Weighted Score Fusion, PGVector also supports **Reciprocal Rank Fusion (RRF)** as a hybrid search fusion strategy. RRF does not rely on the absolute values of raw scores but instead fuses results based on the **ranking** from each retrieval channel:

```
score(d) = sum(1 / (k + rank_i))
```

where `k` is a constant (default 60) and `rank_i` is the rank of document `d` in the `i`-th retrieval channel. This approach naturally avoids the issue of inconsistent score scales between vector and text scores.

**Test Configuration:**
- **Dataset**: Full HuggingFace Markdown documentation dataset (54 QA)
- **Retrieval Configuration**: Top K = 4, RRF k=60, CandidateRatio=3
- **Embedding / Agent / Eval Models**: Same as the main evaluation

**Results:**

| Fusion Strategy | Faithfulness | Answer Relevancy | Answer Correctness | Answer Similarity | Context Precision | Context Recall | Context Entity Recall |
| --------------- | ------------ | ---------------- | ------------------ | ----------------- | ----------------- | -------------- | --------------------- |
| **RRF** (k=60) | 0.9389 | 0.8164 | 0.7791 | 0.7177 | 0.6460 | 0.9259 | 0.4296 |
| **Weighted** (v100_t0, pure vector) | 0.9748 | 0.8635 | 0.8072 | 0.7229 | 0.6991 | 0.9630 | 0.5219 |
| **Weighted** (v90_t10) | 0.9506 | 0.8616 | 0.7953 | 0.7206 | 0.7320 | 0.9259 | 0.4552 |
| **Weighted** (v50_t50, equal) | 0.9346 | 0.7948 | 0.7365 | 0.7064 | 0.6441 | 0.8889 | 0.4414 |

**Analysis:**

1. **RRF performs comparably to v50_t50 weighted fusion**: RRF's Faithfulness (0.9389), Answer Relevancy (0.8164), and Answer Correctness (0.7791) are comparable to or slightly better than v50_t50, but overall lower than high vector weight configurations (v90_t10 and v100_t0).
2. **Pure vector weighted fusion (v100_t0) achieves the best overall performance**: Across all 7 metrics, pure vector outperforms RRF in Faithfulness, Answer Relevancy, Answer Correctness, Answer Similarity, Context Precision, Context Recall, and Context Entity Recall.
3. **RRF's Context metrics are slightly lower**: Context Precision (0.6460) and Context Entity Recall (0.4296) are lower than v90_t10 and v100_t0, indicating that when vector retrieval quality significantly exceeds text retrieval, RRF's rank-based fusion cannot fully leverage the vector channel's advantage.

**Conclusion**: In this evaluation scenario (high-quality embeddings + small-scale knowledge base), **weighted fusion outperforms RRF**. RRF is better suited for scenarios where **both retrieval channels are of comparable quality** (e.g., when high-quality vector retrieval and BM25 retrieval coexist). When the vector retrieval channel is clearly superior to text retrieval, weighted fusion with high vector weight (≥0.8) is the better choice.

---

### Evaluation Observations

Through packet capture analysis during evaluation, we found that all frameworks follow **fairly similar request flows** when using the same LLM model — essentially the standard RAG pipeline of agent calling search tool, retrieving context, and generating answers.

Key considerations:

- **Dataset scale**: The HuggingFace evaluation dataset contains only 1900+ documents and 54 QA pairs. The RGB dataset provides 300 + 100 + 100 = 500 QA pairs with controlled retrieval scenarios. The MultiHop-RAG dataset adds 609 documents and 450 multi-hop QA pairs requiring cross-document reasoning.
- **Prompt sensitivity**: It is undeniable that system prompts have a significant impact on agent execution under the current dataset, which in turn greatly affects the final scores. We have ensured unified system prompts across all frameworks.
- **Chunking strategy may have an impact**: After controlling for system prompt differences, different frameworks' chunking implementations (chunk size, overlap, boundary detection, etc.) may affect retrieval and answer quality, which in turn could influence Context Precision, Context Recall, and other retrieval metrics.
