# Memory Evaluation Benchmark

This benchmark evaluates the long-term conversational memory capabilities of
trpc-agent-go using the LoCoMo dataset.

## Overview

Based on:

- [LoCoMo: Long-Context Conversational Memory](https://arxiv.org/abs/2402.17753)
- [Memory in the Age of AI Agents](https://arxiv.org/abs/2512.13564)

## Evaluation Metrics

Aligned with LoCoMo paper and industry standards (Mem0, MemMachine):

| Metric     | Description                      |
| ---------- | -------------------------------- |
| F1 Score   | Token-level F1 (LoCoMo standard) |
| BLEU Score | N-gram overlap                   |
| LLM-score  | LLM-as-Judge evaluation          |

## QA Categories

| Category    | Description                                        |
| ----------- | -------------------------------------------------- |
| single-hop  | Single-hop questions from one conversation segment |
| multi-hop   | Multi-hop questions requiring multiple segments    |
| temporal    | Temporal reasoning questions                       |
| open-domain | Open-domain questions requiring world knowledge    |
| adversarial | Adversarial questions testing robustness           |

## Evaluation Scenarios

### 1. Long-Context (Baseline)

Full conversation as context, evaluates model's native long-context ability.

```bash
go run main.go -scenario long_context
```

### 2. RAG with Memory Service

Uses memory service for retrieval-augmented generation.
RAG content is controlled by `-rag-mode`.

Supported `-rag-mode` values:

- **full**: Stores the full dialog turns per session as one memory.
- **observation**: Stores the dataset-provided session observation text.
- **summary**: Stores the dataset-provided session summary text.
- **fallback**: Uses `observation`, then `summary`, then `full` as a fallback chain.

Data sources for `observation` and `summary` come directly from the LoCoMo
JSON files.

```text
RAG memory construction (rag_memory scenario)

  observation  summary  full dialog turns
      |          |           |
      |          |           |
      +----------+-----------+
                 |
                 v
            memory store
                 |
                 v
           Top-K retrieval
                 |
                 v
            LLM answer
```

```bash
# Observation-based RAG (inmemory backend).
go run main.go -scenario rag_memory -rag-mode observation

# Summary-based RAG (inmemory backend).
go run main.go -scenario rag_memory -rag-mode summary

# Full dialog RAG (inmemory backend).
go run main.go -scenario rag_memory -rag-mode full

# Fallback RAG (observation -> summary -> full).
go run main.go -scenario rag_memory -rag-mode fallback

# RAG with pgvector backend (requires PostgreSQL with pgvector).
export PGVECTOR_DSN="postgres://user:password@localhost:5432/memory_eval\
?sslmode=disable"
go run main.go -scenario rag_memory -memory-backend pgvector \
  -rag-mode observation
```

### 3. Agentic (Memory Tools)

Agent uses memory tools to add and search memories. The agent processes each
conversation session separately and decides what to store.

**Note**: The `-rag-mode` option does not apply to this scenario. The agent
autonomously extracts and stores memories.

```bash
go run main.go -scenario agentic
```

### 4. Auto (Memory Extractor + Search)

Auto mode uses the built-in memory extractor to generate memories in the
background. The QA stage only performs memory search.

**Note**: The `-rag-mode` option does not apply to this scenario. Memory
extraction is handled by the configured extractor.

```bash
go run main.go -scenario auto
```

Memory backends apply to `rag_memory`, `agentic`, and `auto` scenarios.
Auto mode uses the built-in extractor provided by the memory service.

### 5. All Scenarios

Run all scenarios for comparison.

```bash
go run main.go -scenario all

# Run all scenarios on both backends.
go run main.go -scenario all -memory-backend inmemory,pgvector
```

## Command-Line Options

| Option            | Default                | Description                                  |
| ----------------- | ---------------------- | -------------------------------------------- |
| `-model`          | gpt-4o-mini            | Model name                                   |
| `-eval-model`     | same as model          | Evaluation model for LLM judge               |
| `-dataset`        | ../data                | Dataset directory                            |
| `-data-file`      | locomo_sample.json     | Dataset file name                            |
| `-output`         | ../results             | Output directory                             |
| `-scenario`       | long_context           | Evaluation scenario                          |
| `-rag-mode`       | observation            | RAG mode (rag_memory scenario only)          |
| `-memory-backend` | inmemory               | Memory backend: inmemory, pgvector           |
| `-pgvector-dsn`   | (env)                  | PostgreSQL DSN for pgvector                  |
| `-embed-model`    | text-embedding-3-small | Embedding model for pgvector                 |
| `-top-k`          | 5                      | Top-K memories for RAG                       |
| `-sample-id`      |                        | Filter by sample ID                          |
| `-max-tasks`      | 0                      | Maximum tasks (0=all)                        |
| `-llm-judge`      | false                  | Enable LLM-as-Judge                          |
| `-verbose`        | false                  | Verbose output                               |
| `-resume`         | false                  | Resume from checkpoint                       |

## Environment Variables

| Variable           | Description                         |
| ------------------ | ----------------------------------- |
| `MODEL_NAME`       | Default model name                  |
| `EVAL_MODEL_NAME`  | Evaluation model name               |
| `OPENAI_API_KEY`   | OpenAI API key                      |
| `PGVECTOR_DSN`     | PostgreSQL DSN for pgvector backend |
| `EMBED_MODEL_NAME` | Embedding model for pgvector        |

## Dataset Setup

1. Download the LoCoMo dataset:

```bash
git clone https://github.com/snap-research/locomo.git
cp locomo/data/locomo10/*.json ../data/
```

2. Or use the sample data for testing:

```bash
# Sample data should be in ../data/locomo_sample.json.
```

## Running the Benchmark

```bash
cd benchmark/memory/trpc-agent-go-impl

# Install dependencies.
go mod tidy

# Run with default settings (long_context + inmemory).
go run main.go

# Run with LLM judge enabled.
go run main.go -llm-judge -model gpt-4o

# Run RAG evaluation with inmemory backend.
go run main.go -scenario rag_memory -rag-mode observation

# Run RAG evaluation with pgvector backend.
export PGVECTOR_DSN="postgres://user:password@localhost:5432/memory_eval\
?sslmode=disable"
go run main.go -scenario rag_memory -memory-backend pgvector \
  -rag-mode observation

# Run all scenarios.
go run main.go -scenario all -output ../results/full_eval
```

## Output Format

Results are saved in JSON format:

```json
{
  "metadata": {
    "framework": "trpc-agent-go",
    "model": "gpt-4o-mini",
    "scenario": "rag_memory",
    "rag_mode": "observation"
  },
  "summary": {
    "total_questions": 200,
    "overall_f1": 0.412,
    "overall_bleu": 0.156
  },
  "by_category": {
    "single-hop": { "count": 60, "f1": 0.523, "bleu": 0.182 },
    "multi-hop": { "count": 50, "f1": 0.384, "bleu": 0.145 }
  }
}
```

## Comparison with Baselines

| System             | F1   | LLM-score |
| ------------------ | ---- | --------- |
| GPT-4 (4K context) | 32.1 | -         |
| GPT-3.5-16K        | 37.8 | -         |
| RAG-Observation    | 41.4 | -         |
| Mem0               | -    | 0.80      |
| MemMachine         | 91.2 | 0.91      |

## Memory Backend Comparison

| Backend  | Pros                               | Cons                                         |
| -------- | ---------------------------------- | -------------------------------------------- |
| inmemory | Fast, no setup required            | No vector similarity, keyword-based matching |
| pgvector | Vector similarity search, scalable | Requires PostgreSQL setup                    |

### Expected Results

- **pgvector** should outperform **inmemory** for semantic retrieval tasks.
- For exact-match questions, both backends may perform similarly.
- pgvector is recommended for production and realistic evaluation.
