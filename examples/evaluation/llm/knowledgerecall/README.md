# Knowledge Recall (LLM) Evaluation Example

This example measures how well the agent recalls provided knowledge. The agent uses a retrieval tool backed by a local knowledge base, and the judge model scores the relevance of the recalled knowledge with the `llm_rubric_knowledge_recall` metric.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the agent model (required) | `` |
| `OPENAI_BASE_URL` | Optional custom endpoint for OpenAI-compatible chat APIs | `https://api.openai.com/v1` |
| `JUDGE_MODEL_API_KEY` | API key for the judge model (required) | `` |
| `JUDGE_MODEL_BASE_URL` | Optional custom endpoint for the judge model | `https://api.openai.com/v1` |
| `OPENAI_EMBEDDING_API_KEY` | API key for the embedding model (required for retrieval) | `` |
| `OPENAI_EMBEDDING_BASE_URL` | Optional custom endpoint for embeddings | `https://api.openai.com/v1` |
| `OPENAI_EMBEDDING_MODEL` | Embedding model name | `text-embedding-3-small` |

The metric configuration in `data/` references the judge settings via `${JUDGE_MODEL_API_KEY}` and `${JUDGE_MODEL_BASE_URL}` placeholders.

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-model` | Model identifier used by the agent | `deepseek-chat` |
| `-streaming` | Enable streaming responses from the agent | `false` |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json` | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-eval-set` | Evaluation set ID to execute | `knowledge-recall-basic` |

## Run

```bash
cd examples/evaluation/llm/knowledgerecall
OPENAI_API_KEY=sk-... \
JUDGE_MODEL_API_KEY=sk-... \
OPENAI_EMBEDDING_API_KEY=sk-... \
go run . \
  -model "deepseek-chat" \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "knowledge-recall-basic"
```

The agent loads `knowledge/llm.md`, answers a single QA prompt, and the judge model grades whether the recalled knowledge in the answer is relevant using three samples.

## Data Layout

```
data/
└── knowledge-recall-app/
    ├── knowledge-recall-basic.evalset.json     # EvalSet with one QA case
    └── knowledge-recall-basic.metrics.json     # llm_rubric_knowledge_recall metric
knowledge/
└── llm.md                                      # Knowledge base content
```

## Output

Results are written under `./output/knowledge-recall-app`, mirroring the eval set structure. The console prints a summary of overall status and per-case knowledge-recall rubric scores.
