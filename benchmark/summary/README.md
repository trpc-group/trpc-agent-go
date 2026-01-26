# Session Summary Benchmark for trpc-agent-go

This benchmark evaluates the effectiveness of session summarization in trpc-agent-go, inspired by τ-bench and τ²-bench methodologies.

## Directory Structure

```
benchmark/summary/
├── README.md                    # This file
├── data/                        # Dataset directory
│   ├── download_datasets.sh     # Dataset download script
│   └── mt-bench-101/            # MT-Bench-101 dataset (after download)
├── results/                     # Evaluation results output directory
└── trpc-agent-go-impl/          # Evaluation program implementation
    ├── main.go
    ├── go.mod
    ├── go.sum
    └── evaluation/              # Evaluation utilities
        ├── dataset/             # Dataset loader
        └── evaluator/           # Evaluation metrics
```

## Evaluation Dimensions

The benchmark measures summarization effectiveness across three dimensions:

| Dimension | Weight | Description |
|-----------|--------|-------------|
| Response Consistency | 50% | Pass^k evaluation for semantic equivalence |
| Token Efficiency | 30% | Token savings from summarization |
| Information Retention | 20% | Key information preservation check |

## Prerequisites

### 1. Download Dataset

```bash
cd benchmark/summary/data
./download_datasets.sh
```

### 2. Configure Model

The evaluation uses `gpt-4o-mini` by default. Set a different model via:

```bash
-model <model-name>
# or
export MODEL_NAME=<model-name>
```

## Running the Evaluation

### Basic Usage

```bash
cd benchmark/summary/trpc-agent-go-impl

# Run all test cases
go run .

# Run a specific number of cases
go run . -num-cases 10

# Run with verbose output
go run . -num-cases 10 -verbose

# Filter by MT-Bench-101 task code
go run . -task CM,GR

# Resume from checkpoint
go run . -resume
```

### Command Line Arguments

| Argument | Default | Description |
|----------|---------|-------------|
| `-model` | `gpt-4o-mini` | Model name to use |
| `-dataset` | `../data/mt-bench-101` | Path to MT-Bench-101 dataset |
| `-task` | `""` | Filter by task codes (e.g., CM, GR) |
| `-num-cases` | `0` | Number of test cases (0=all) |
| `-num-runs` | `1` | Runs per case for Pass^k consistency |
| `-output` | `../results` | Output directory |
| `-events` | `2` | Event threshold for summarization |
| `-llm-eval` | `false` | Use LLM for semantic evaluation |
| `-verbose` | `false` | Print full conversation content |
| `-resume` | `false` | Resume from previous checkpoint |
| `-consistency-threshold` | `0.7` | Threshold for consistency pass/fail |
| `-retention-threshold` | `0.7` | Threshold for retention pass/fail |
| `-k-values` | `1,2,4` | Pass^k k values (comma-separated) |

### MT-Bench-101 Task Codes

| Code | Full Name | Description |
|------|-----------|-------------|
| AR | Anaphora Resolution | Identify pronoun referents throughout a multi-turn dialogue. |
| CC | Content Confusion | Avoid interference from similar-looking queries with distinct meanings. |
| CM | Context Memory | Recall early dialogue details to address the user's current question. |
| CR | Content Rephrasing | Rephrase the content of the last response per user's requirement. |
| FR | Format Rephrasing | Rephrase the format of the last response per user's requirement. |
| GR | General Reasoning | Collaboratively solve complex reasoning problems across turns. |
| IC | Instruction Clarification | Seek clarification by asking further questions on ambiguous queries. |
| MR | Mathematical Reasoning | Collaboratively solve complex mathematical problems across turns. |
| PI | Proactive Interaction | Propose questions to spark user's interest to continue the dialogue. |
| SA | Self-affirmation | Preserve the last response against inaccurate user feedback. |
| SC | Self-correction | Recorrect the last response according to user feedback. |
| SI | Separate Input | First turn outlines task requirements, following turns specify input. |
| TS | Topic Shift | Recognize and focus on new topic when users switch topics. |

## Output Format

Results are saved in JSON format:

```json
{
  "timestamp": "2025-01-26T10:00:00Z",
  "model": "gpt-4o-mini",
  "numCases": 100,
  "numRuns": 1,
  "avgTokenSavings": 25.5,
  "avgPromptSavings": 30.2,
  "avgConsistency": 0.85,
  "avgRetention": 0.92,
  "overallScore": 0.78,
  "caseResults": [...]
}
```

## References

- [MT-Bench-101 Paper (ACL 2024)](https://arxiv.org/abs/2402.14762)
- [τ-bench Paper](https://arxiv.org/abs/2406.12045)
- [τ²-bench Paper](https://arxiv.org/abs/2506.07982)
- [trpc-agent-go GitHub](https://github.com/trpc-group/trpc-agent-go)
