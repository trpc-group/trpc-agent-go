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

## Dataset

We use the [HuggingFace Documentation](https://huggingface.co/datasets/m-ric/huggingface_doc) dataset.

**Important Filtering**: To ensure data quality and consistency, we specifically **filter and only use Markdown files** (`.md`) for both the source documents and the QA evaluation pairs.

- **Documents**: `m-ric/huggingface_doc` - Filtered for `.md` documentation files
- **QA Pairs**: `m-ric/huggingface_doc_qa_eval` - Filtered for questions whose source is a `.md` file

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

### Full Evaluation (54 QA Pairs)

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

### Key Conclusions

1. **tRPC-Agent-Go achieves the best overall performance**: Ranks 1st in 5 out of 7 metrics — **Faithfulness (0.9853)**, **Answer Correctness (0.8299)**, **Answer Similarity (0.7251)**, **Context Precision (0.7278)**, and **Context Entity Recall (0.5034)**, leading in both answer quality and retrieval precision.
2. **AutoGen leads in relevancy**: **Answer Relevancy (0.9040)** ranks 1st (close to Agno's 0.9013), showing the best on-topic answers. Also ties for 1st in **Context Recall (0.9444)**.
3. **CrewAI has the highest recall**: **Context Recall (0.9444)** ties for 1st, indicating the most comprehensive retrieval recall.
4. **Agno excels in relevancy**: **Answer Relevancy (0.9013)** ranks 2nd, demonstrating excellent on-topic answers.
5. **Each framework has its strengths**: LangChain provides a stable and balanced baseline, with each framework excelling in different dimensions.

### Evaluation Observations

Through packet capture analysis during evaluation, we found that all frameworks follow **fairly similar request flows** when using the same LLM model — essentially the standard RAG pipeline of agent calling search tool, retrieving context, and generating answers.

Key considerations:

- **Small dataset size**: The current evaluation dataset contains only 1900+ documents and 54 QA pairs, which is not large-scale data.
- **Prompt sensitivity**: It is undeniable that system prompts have a significant impact on agent execution under the current dataset, which in turn greatly affects the final scores. We have ensured unified system prompts across all frameworks.
- **Chunking strategy may have an impact**: After controlling for system prompt differences, different frameworks' chunking implementations (chunk size, overlap, boundary detection, etc.) may affect retrieval and answer quality, which in turn could influence Context Precision, Context Recall, and other retrieval metrics.
