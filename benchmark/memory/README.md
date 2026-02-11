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

### 2. Agentic (Memory Tools)

Agent uses memory tools to add and search memories. The agent processes each
conversation session separately and decides what to store.

```bash
go run main.go -scenario agentic
```

### 3. Auto (Memory Extractor + Search)

Auto mode uses the built-in memory extractor to generate memories in the
background. The QA stage only performs memory search.

```bash
go run main.go -scenario auto
```

Memory backends apply to `agentic` and `auto` scenarios.
Auto mode uses the built-in extractor provided by the memory service.

### 4. All Scenarios

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
| `-memory-backend` | inmemory               | Memory backend: inmemory, pgvector           |
| `-pgvector-dsn`   | (env)                  | PostgreSQL DSN for pgvector                  |
| `-embed-model`    | text-embedding-3-small | Embedding model for pgvector                 |
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

# Run agentic evaluation with pgvector backend.
export PGVECTOR_DSN="postgres://user:password@localhost:5432/memory_eval\
?sslmode=disable"
go run main.go -scenario agentic -memory-backend pgvector

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
    "scenario": "agentic",
    "memory_backend": "pgvector"
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
