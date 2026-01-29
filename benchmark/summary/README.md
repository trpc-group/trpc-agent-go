# Session Summary Benchmark for trpc-agent-go

This benchmark evaluates the effectiveness of session summarization in trpc-agent-go, inspired by τ-bench and τ²-bench methodologies.

## Quick Start

```bash
# 1. Download dataset.
cd benchmark/summary/data
./download_datasets.sh

# 2. Run evaluation (example: Context Memory task with 10 cases).
cd ../trpc-agent-go-impl
go run . -task CM -num-cases 10 -llm-eval -verbose
```

## Directory Structure

```
benchmark/summary/
├── README.md                    # This file
├── data/                        # Dataset directory
│   ├── download_datasets.sh     # Dataset download script
│   └── mt-bench-101/            # MT-Bench-101 dataset (after download)
├── results/                     # Evaluation results output directory
│   ├── report.md                # Evaluation report (English)
│   └── report_zh_CN.md          # Evaluation report (Chinese)
└── trpc-agent-go-impl/          # Evaluation program implementation
    ├── main.go
    ├── go.mod
    ├── go.sum
    └── evaluation/              # Evaluation utilities
        ├── dataset/             # Dataset loader
        └── evaluator/           # Evaluation metrics
```

## Evaluation Dimensions

| Dimension | Weight | Description |
|-----------|--------|-------------|
| Response Consistency | 50% | Pass^k evaluation for semantic equivalence |
| Token Efficiency | 30% | Token savings from summarization |
| Information Retention | 20% | Key information preservation check |

## Running the Evaluation

### Example Commands

```bash
cd benchmark/summary/trpc-agent-go-impl

# Run a single task with LLM evaluation.
go run . -task CM -llm-eval

# Run multiple tasks.
go run . -task CM,GR,IC -llm-eval

# Run with custom output directory and verbose logging.
go run . -task CC -output ../results/mt-bench-101/CC -llm-eval -verbose

# Run in background (recommended for full evaluation).
nohup go run . -task GR -output ../results/mt-bench-101/GR \
    -llm-eval > ../results/mt-bench-101/GR/run.log 2>&1 &

# Run all tasks (warning: takes several hours).
go run . -llm-eval -output ../results/all

# Resume from checkpoint after interruption.
go run . -task CM -output ../results/mt-bench-101/CM -resume -llm-eval
```

### Command Line Arguments

| Argument | Default | Description |
|----------|---------|-------------|
| `-model` | `gpt-4o-mini` | Model name (uses `MODEL_NAME` env var if set) |
| `-dataset` | `../data/mt-bench-101` | Path to MT-Bench-101 dataset directory |
| `-task` | `""` | Filter by task codes (comma-separated, e.g., `CM,GR`) |
| `-num-cases` | `0` | Number of test cases per task (0 = all) |
| `-num-runs` | `1` | Runs per case for Pass^k consistency |
| `-output` | `../results` | Output directory for results |
| `-events` | `2` | Event threshold for triggering summarization |
| `-llm-eval` | `false` | Enable LLM-based semantic evaluation |
| `-verbose` | `false` | Print full conversation content |
| `-resume` | `false` | Resume from previous checkpoint |
| `-consistency-threshold` | `0.7` | Threshold for consistency pass/fail |
| `-retention-threshold` | `0.7` | Threshold for retention pass/fail |
| `-k-values` | `1,2,4` | Pass^k k values (comma-separated) |

### Environment Variables

```bash
# Set model name (takes precedence over default).
export MODEL_NAME=deepseek-v3.2

# Configure API endpoint (if using custom provider).
export OPENAI_API_BASE=https://api.example.com/v1
export OPENAI_API_KEY=your-api-key
```

## MT-Bench-101 Task Codes

| Code | Full Name | Cases | Description |
|------|-----------|------:|-------------|
| AR | Anaphora Resolution | 153 | Identify pronoun referents throughout a multi-turn dialogue. |
| CC | Content Confusion | 147 | Avoid interference from similar-looking queries with distinct meanings. |
| CM | Context Memory | 80 | Recall early dialogue details to address the user's current question. |
| CR | Content Rephrasing | 136 | Rephrase the content of the last response per user's requirement. |
| FR | Format Rephrasing | 74 | Rephrase the format of the last response per user's requirement. |
| GR | General Reasoning | 71 | Collaboratively solve complex reasoning problems across turns. |
| IC | Instruction Clarification | 150 | Seek clarification by asking further questions on ambiguous queries. |
| MR | Mathematical Reasoning | 108 | Collaboratively solve complex mathematical problems across turns. |
| PI | Proactive Interaction | 87 | Propose questions to spark user's interest to continue the dialogue. |
| SA | Self-affirmation | 73 | Preserve the last response against inaccurate user feedback. |
| SC | Self-correction | 77 | Correct the last response according to user feedback. |
| SI | Separate Input | 149 | First turn outlines task requirements, following turns specify input. |
| TS | Topic Shift | 83 | Recognize and focus on new topic when users switch topics. |

**Total**: 1388 dialogues, 4208 turns across 13 tasks.

## Output Format

Results are saved in JSON format with the following structure:

```json
{
  "timestamp": "2026-01-23T15:08:45+08:00",
  "model": "deepseek-v3.2",
  "numCases": 147,
  "numRuns": 1,
  "caseResults": [
    {
      "caseId": "CC_557",
      "tokenEfficiency": {
        "baselineTokens": 2138,
        "summaryTokens": 2245,
        "savingsPercentage": -5.00,
        "promptSavingsPercentage": -25.34
      },
      "consistency": {
        "score": 0.88,
        "passHat1": 1,
        "consistencyLevel": "medium"
      },
      "retention": {
        "retentionRate": 0.875,
        "keyInfoCount": 10,
        "retainedCount": 8
      }
    }
  ]
}
```

## Benchmark Results Summary

Latest evaluation results (deepseek-v3.2, 627 cases):

| Task | Token Savings | Prompt Savings | Consistency | Retention | Pass^1 |
|------|-------------:|---------------:|------------:|----------:|-------:|
| CC | -27.10% | -17.44% | 0.86 | 0.86 | 89.1% |
| CM | +13.68% | +25.50% | 0.82 | 0.82 | 96.2% |
| GR | +13.95% | +15.20% | 1.00 | 1.00 | 100.0% |
| IC | -0.63% | -0.44% | 0.85 | 0.82 | 95.3% |
| PI | -28.32% | -1.43% | 0.81 | 0.70 | 96.6% |
| SC | -1.78% | -0.74% | 0.88 | 0.87 | 93.5% |
| TS | -0.99% | -1.01% | 0.85 | 0.85 | 95.2% |
| **Avg** | **-8.97%** | **-1.29%** | **0.85** | **0.83** | **93.9%** |

See [results/report.md](results/report.md) for detailed analysis.

## References

- [MT-Bench-101 Paper (ACL 2024)](https://arxiv.org/abs/2402.14762)
- [τ-bench Paper](https://arxiv.org/abs/2406.12045)
- [τ²-bench Paper](https://arxiv.org/abs/2506.07982)
- [trpc-agent-go GitHub](https://github.com/trpc-group/trpc-agent-go)
