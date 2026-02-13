# RAG Evaluation: tRPC-Agent-Go vs LangChain vs Agno vs CrewAI

This directory contains a comprehensive evaluation framework for comparing RAG (Retrieval-Augmented Generation) systems using [RAGAS](https://docs.ragas.io/) metrics.

## Overview

We evaluate four RAG implementations with **identical configurations** to ensure a fair comparison:

- **tRPC-Agent-Go**: Our Go-based RAG implementation
- **LangChain**: Python-based reference implementation
- **Agno**: Python-based AI agent framework with built-in knowledge base support
- **CrewAI**: Python-based multi-agent framework with ChromaDB vector store

## Quick Start

### Prerequisites

```bash
# Install Python dependencies
pip install -r requirements.txt

# Set environment variables
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="your-base-url"  # Optional
export MODEL_NAME="deepseek-v3.2"       # Optional, model for knowledge/RAG
export EVAL_MODEL_NAME="gemini-3-flash"    # Optional, model for evaluation (default: gemini-3-flash)
export EMBEDDING_MODEL="server:274214"  # Optional

# Optional: Use different API endpoint for evaluation
# export EVAL_API_KEY="your-eval-api-key"     # Default: same as OPENAI_API_KEY
# export EVAL_BASE_URL="your-eval-base-url"   # Default: same as OPENAI_BASE_URL

# PostgreSQL (PGVector) configuration
export PGVECTOR_HOST="127.0.0.1"
export PGVECTOR_PORT="5432"
export PGVECTOR_USER="root"
export PGVECTOR_PASSWORD="123"           # Default password
export PGVECTOR_DATABASE="vector"
```

### Run Evaluation

```bash
# Evaluate with LangChain knowledge base (native mode, default)
python3 main.py --evaluator=ragas --kb=langchain

# Evaluate with strict mode (fair baseline comparison)
python3 main.py --evaluator=ragas --kb=langchain --eval-mode=strict

# Evaluate with tRPC-Agent-Go knowledge base
python3 main.py --evaluator=ragas --kb=trpc-agent-go

# Evaluate with Agno knowledge base
python3 main.py --evaluator=ragas --kb=agno

# With custom retrieval k value
python3 main.py --evaluator=ragas --kb=langchain --k=10

# Full log output (show complete answers and contexts)
python3 main.py --evaluator=ragas --kb=trpc-agent-go --max-qa=1 --full-log

# Save results to JSON file
python3 main.py --evaluator=ragas --kb=langchain --output=results.json
```

### Command Line Options

| Option | Default | Description |
|--------|---------|-------------|
| `--kb` | langchain | Knowledge base: `langchain`, `trpc-agent-go`, `agno`, or `crewai` |
| `--evaluator` | ragas | Evaluator to use (currently only `ragas`) |
| `--eval-mode` | native | `strict`: single search() for contexts (fair baseline); `native`: contexts from agent tool calls |
| `--k` | 4 | Number of documents to retrieve per query |
| `--max-qa` | all | Maximum QA items for evaluation |
| `--max-docs` | all | Maximum documents to load |
| `--load` | false | Force reload documents into knowledge base |
| `--skip-load` | false | Skip loading documents |
| `--full-log` | false | Print full answer results for each question |
| `--output` | none | Output file path to save results as JSON |
| `--workers` | 10 | Number of concurrent workers for evaluation |
| `--timeout` | 600 | Timeout in seconds for evaluation |

## Configuration Alignment

All four systems use **identical parameters** to ensure fair comparison:

| Parameter | LangChain | tRPC-Agent-Go | Agno | CrewAI |
|-----------|-----------|---------------|------|--------|
| **Temperature** | 0 | 0 | 0 | 0 |
| **Chunk Size** | 500 | 500 | 500 | 500 |
| **Chunk Overlap** | 50 | 50 | 50 | 50 |
| **Embedding Dimensions** | 1024 | 1024 | 1024 | 1024 |
| **Vector Store** | PGVector | PGVector | PgVector | ChromaDB |
| **Search Mode** | Vector | Vector (Hybrid disabled) | Vector | Vector |
| **Knowledge Base Build** | Native framework method | Native framework method | Native framework method | Native framework method |
| **Agent Type** | Agent + KB (ReAct disabled) | Agent + KB (ReAct disabled) | Agent + KB (ReAct disabled) | Agent + KB (ReAct disabled) |
| **Max Retrieval Results (k)** | 4 | 4 | 4 | 4 |

> 📝 **tRPC-Agent-Go Notes**:
> - **Search Mode**: tRPC-Agent-Go uses Hybrid Search (vector similarity + full-text search) by default, but for fair comparison with other frameworks, **Hybrid Search is disabled** in this evaluation. All frameworks use pure Vector Search (vector similarity only).

> 📝 **CrewAI Notes**:
> - **Vector Store**: CrewAI does not currently support PGVector for knowledge base construction, so ChromaDB is used as the vector store.
> - **Bug Fix**: CrewAI (v1.9.0) has a bug where it prioritizes `content` over `tool_calls` when the LLM (e.g., DeepSeek-V3.2) returns both simultaneously, causing the Agent to skip tool invocations. We applied a Monkey Patch to `LLM._handle_non_streaming_response` to prioritize `tool_calls`, ensuring fair evaluation. See `knowledge_system/crewai/knowledge_base.py` for details.

## Evaluation Modes

The framework supports two evaluation modes to address different comparison goals:

### `strict` Mode (Fair Baseline)

```bash
python3 main.py --kb=langchain --eval-mode=strict
```

- **Context collection**: A single deterministic `search(question, k)` call provides the contexts for RAGAS evaluation, **decoupled from the agent**.
- **Answer generation**: The agent's `answer()` is still called normally, but its internally retrieved contexts are **not** used for evaluation.
- **Search mode**: All frameworks use **vector-only** search (tRPC-Agent-Go's default Hybrid Search is disabled), ensuring consistent retrieval conditions.
- **Purpose**: Ensures all frameworks are evaluated on the **exact same retrieval result** for each question, eliminating differences caused by agent multi-turn tool calls, query rewriting, or hybrid search strategies.
- **Failed samples**: Preserved with placeholder values to guarantee identical sample sets across frameworks.

### `native` Mode (Real-World Behavior)

```bash
python3 main.py --kb=langchain --eval-mode=native
```

- **Context collection**: Contexts come from the agent's actual tool call responses during `answer()`.
- **Search mode**: Each framework uses its **native retrieval pipeline** (tRPC-Agent-Go's Hybrid Search is also disabled in this evaluation to ensure fair comparison).
- **Purpose**: Measures the **end-to-end real-world performance** of each framework's complete RAG pipeline, including agent behavior, multi-turn retrieval, and framework-specific optimizations.
- **Failed samples**: Preserved with placeholder values.

### When to Use Which Mode

| Goal | Mode |
|------|------|
| Fair horizontal comparison of retrieval + generation quality | `strict` |
| Measure real-world production performance | `native` |
| Debug retrieval pipeline differences | `strict` (compare contexts directly) |
| Evaluate agent tool-calling behavior | `native` |

## System Prompt

To ensure fair comparison, all four systems are configured with **identical** instructions.

**Prompt for LangChain, Agno, tRPC-Agent-Go & CrewAI:**
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

We use the [HuggingFace Documentation](https://huggingface.co/datasets/m-ric/huggingface_doc) datasets. 

**Important Filtering**: To ensure data quality and consistency, we specifically **filter and only use Markdown files** (`.md`) for both the source documents and the QA evaluation pairs.

- **Documents**: `m-ric/huggingface_doc` - Filtered for `.md` documentation files
- **QA Pairs**: `m-ric/huggingface_doc_qa_eval` - Filtered for questions whose source is a `.md` file

## RAGAS Metrics

We evaluate 7 metrics across 3 categories:

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

### Latest Evaluation (54 QA items)

**Test Configuration:**
- **Dataset**: Full LangChain HuggingFace documentation dataset (54 QA items)
- **Embedding Model**: `BGE-M3` (1024 dimensions)
- **Agent Model**: `DeepSeek-V3.2`
- **Evaluation Model**: `Gemini 3 Flash`
- **Retrieval k**: 4 documents per query
- **Chunk Size**: 500 characters
- **Chunk Overlap**: 50 characters

#### Answer Quality Metrics

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | Best |
|--------|-----------|---------------|------|--------|------|
| **Faithfulness** | 0.9340 | 0.9853 | 0.9815 | **0.9907** | ✅ CrewAI |
| **Answer Relevancy** | 0.7430 | **0.8890** | 0.7814 | 0.8073 | ✅ tRPC-Agent-Go |
| **Answer Correctness** | 0.7417 | 0.8299 | **0.8357** | 0.7855 | ✅ Agno |
| **Answer Similarity** | 0.7313 | 0.7251 | **0.7711** | 0.7043 | ✅ Agno |

#### Context Quality Metrics

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | Best |
|--------|-----------|---------------|------|--------|------|
| **Context Precision** | 0.6026 | **0.7278** | 0.6932 | 0.6623 | ✅ tRPC-Agent-Go |
| **Context Recall** | 0.8704 | 0.9259 | **0.9444** | **0.9444** | ✅ Agno / CrewAI |
| **Context Entity Recall** | 0.4251 | **0.5034** | 0.4205 | 0.4189 | ✅ tRPC-Agent-Go |

#### Execution Time

> ⚠️ **Important Note**: The evaluations were run at **different time periods**, and model inference speed can vary significantly depending on API server load and network conditions. **Time metrics are for reference only and should not be used for strict performance comparison.**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI |
|--------|-----------|---------------|------|--------|
| **Q&A Total Time** | 378.94s | 731.65s | 571.68s | 521.72s |
| **Avg Time per Question** | 7.02s | 13.55s | 10.59s | 9.66s |
| **Evaluation Time** | 4471.66s | 3696.36s | 3745.98s | 3909.57s |
| **Total Time** | 4850.60s | 4428.01s | 4317.66s | 4431.29s |

### Summary

| Category | LangChain | tRPC-Agent-Go | Agno | CrewAI |
|----------|-----------|---------------|------|--------|
| **Faithfulness** | 4th | 2nd | 3rd | ✅ Best (0.9907) |
| **Answer Relevancy** | 4th | ✅ Best (0.8890) | 3rd | 2nd |
| **Answer Correctness** | 4th | 2nd | ✅ Best (0.8357) | 3rd |
| **Answer Similarity** | 2nd | 3rd | ✅ Best (0.7711) | 4th |
| **Context Precision** | 4th | ✅ Best (0.7278) | 2nd | 3rd |
| **Context Recall** | 4th | 3rd | ✅ Best (tie, 0.9444) | ✅ Best (tie, 0.9444) |
| **Context Entity Recall** | 2nd | ✅ Best (0.5034) | 3rd | 4th |

**Key Observations**:

1. **tRPC-Agent-Go** leads in **Answer Relevancy (0.8890)**, **Context Precision (0.7278)**, and **Context Entity Recall (0.5034)**, demonstrating strong retrieval precision and answer relevance. Its **Faithfulness (0.9853)** is also near-perfect.

2. **Each framework has its strengths**: CrewAI leads in **Faithfulness (0.9907)**, Agno in **Answer Correctness (0.8357)** and **Answer Similarity (0.7711)**, and LangChain provides a stable baseline.

3. **Context Recall tie**: Agno and CrewAI both achieve **0.9444**, indicating comparable retrieval recall capabilities.

### Evaluation Observations

Through packet capture analysis during evaluation, we found that all frameworks follow **fairly similar request flows** when using the same LLM model — essentially the standard RAG pipeline of agent calling search tool, retrieving context, and generating answers. Some frameworks (e.g., CrewAI) inject additional framework-level prompts internally.

Key considerations:

- **Small dataset size**: The current evaluation dataset contains only 1900+ documents, which is not large-scale data.
- **Prompt sensitivity**: It is undeniable that system prompts have a significant impact on agent execution under the current dataset, which in turn greatly affects the final scores. We have ensured unified system prompts across all frameworks.
- **Chunking strategy is the core differentiator**: After controlling for system prompt differences, **the quality of document chunking is likely the key factor that ultimately determines retrieval and answer quality**. Different frameworks' chunking implementations (chunk size, overlap, boundary detection, etc.) directly impact Context Precision, Context Recall, and consequently answer correctness.

## Project Structure

```
evaluation/
├── main.py                   # Main entry point for evaluation
├── evaluator/
│   ├── base.py               # Base evaluator interface
│   └── ragas/                # RAGAS evaluator implementation
│       └── evaluator.py
├── dataset/
│   ├── base.py               # Base dataset interface
│   └── huggingface/          # HuggingFace dataset loader
│       ├── loader.py
│       └── hf_docs/          # Cached documents
├── knowledge_system/
│   ├── base.py               # Base knowledge system interface
│   ├── langchain/            # LangChain implementation
│   │   └── knowledge_base.py
│   ├── trpc_agent_go/        # tRPC-Agent-Go implementation
│   │   ├── knowledge_base.py # Python client
│   │   └── trpc_knowledge/   # Go server
│   │       ├── knowledge.go
│   │       └── main.go
│   ├── agno/                 # Agno implementation
│   │   └── knowledge_base.py
│   └── crewai/               # CrewAI implementation
│       └── knowledge_base.py
├── util.py                   # Configuration utilities
└── requirements.txt          # Python dependencies
```

## Architecture

### Evaluator Interface

The evaluation framework uses an abstract `Evaluator` interface to support multiple evaluation methods:

```python
class Evaluator(ABC):
    @abstractmethod
    def evaluate(self, samples: List[EvaluationSample]) -> str:
        """Evaluate samples and return formatted result string."""
        pass
```

### Current Implementations

- **RAGASEvaluator**: Uses RAGAS metrics for comprehensive RAG evaluation

### Adding New Evaluators

To add a new evaluator:

1. Create a new directory under `evaluator/` (e.g., `evaluator/custom/`)
2. Implement the `Evaluator` interface:
   ```python
   from evaluator.base import Evaluator, EvaluationSample
   
   class CustomEvaluator(Evaluator):
       def evaluate(self, samples: List[EvaluationSample]) -> str:
           # Your evaluation logic
           return formatted_result_string
   ```
3. Update `main.py` to support the new evaluator:
   ```python
   if args.evaluator == "custom":
       from evaluator.custom.evaluator import CustomEvaluator
       evaluator = CustomEvaluator()
   ```
