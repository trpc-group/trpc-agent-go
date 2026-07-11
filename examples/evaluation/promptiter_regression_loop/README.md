# PromptIter Regression Loop Example

This example demonstrates the **Evaluation + Optimization regression pipeline** that wraps the PromptIter engine to add failure attribution, enhanced acceptance gates, overfitting detection, and comprehensive audit reporting.

## Design Overview

The pipeline implements a complete "evaluate → attribute failures → optimize → validate → gate → audit" closed loop. **Failure attribution** uses a two-phase rule engine: first matching metric evaluator names (e.g., `tool_trajectory_avg_score` → tool call error), then scanning failure reason text for keyword patterns (e.g., "wrong argument" → tool argument error). Nine failure categories are supported: final response mismatch, tool call error, tool argument error, route error, format error, knowledge recall, hallucination, quality below threshold, and unknown.

The **enhanced acceptance gate** extends the basic `MinScoreGain` check with four additional rules: (1) no new hard failures—cases that passed baseline must not fail in candidate; (2) critical case protection—specified case IDs must not regress in score; (3) cost budget—total estimated cost must stay within limit; (4) overfitting detection—if training score improves by more than `OverfitThreshold` while validation score decreases, the candidate is automatically rejected. This prevents the optimizer from overfitting to training data at the expense of generalization.

**Audit reporting** generates both JSON (machine-readable) and Markdown (human-readable) reports containing: baseline/candidate scores, per-case deltas with regression/improvement flags, gate decision with rule-level details, failure attribution statistics, round-by-round patch history, and cost/latency summary. The pipeline integrates with PromptIter's engine through the `engine.Engine` interface, receiving `RunResult` with full trace data for attribution analysis.

## Running

### With fake models (no API key required):

```bash
go run . --fake --data-dir=./data --output-dir=./output
```

### With real models:

```bash
go run . --model=deepseek-chat --judge-model=deepseek-chat --data-dir=./data --output-dir=./output
```

### Configuration flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--data-dir` | `./data` | Directory containing evalset and metrics JSON files |
| `--output-dir` | `./output` | Directory for generated reports |
| `--max-rounds` | `3` | Maximum optimization rounds |
| `--fake` | `false` | Use deterministic fake models |
| `--model` | `deepseek-chat` | Model for candidate agent |
| `--judge-model` | `deepseek-chat` | Model for evaluation judge |
| `--verbose` | `false` | Enable verbose logging |

## Input Files

- `data/train.evalset.json` — 3 training cases (optimizable, passing, hard-failure)
- `data/validation.evalset.json` — 3 validation cases (stable, potential-regression, optimizable)
- `data/metrics.json` — Metric definitions with thresholds
- `data/pipeline.json` — Pipeline configuration (gate rules, cost budget, etc.)

## Output Files

- `output/optimization_report.json` — Full structured audit report
- `output/optimization_report.md` — Human-readable Markdown report

## Pipeline Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Regression Loop Pipeline                  │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  1. Baseline Evaluation                                      │
│     ├── Train set scoring                                    │
│     └── Validation set scoring                               │
│                                                              │
│  2. PromptIter Optimization (per round)                      │
│     ├── Train evaluation → Loss extraction                   │
│     ├── Failure Attribution (rule-based classifier)          │
│     ├── Backward gradient propagation                        │
│     ├── Aggregation & Optimization                           │
│     └── Candidate validation                                 │
│                                                              │
│  3. Enhanced Acceptance Gate                                 │
│     ├── Min score gain check                                 │
│     ├── No new hard failures                                 │
│     ├── Critical case protection                             │
│     ├── Cost budget enforcement                              │
│     └── Overfitting detection (train↑ val↓)                  │
│                                                              │
│  4. Audit Report Generation                                  │
│     ├── optimization_report.json                             │
│     └── optimization_report.md                               │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

## Key Features

- **Failure Attribution**: 9-category rule-based classifier with metric-name and reason-keyword matching
- **Overfitting Detection**: Automatically rejects candidates when training improves but validation degrades
- **Critical Case Protection**: Prevents regression on specified high-priority test cases
- **Cost Budgeting**: Enforces maximum token/cost limits across optimization rounds
- **Comprehensive Auditing**: JSON + Markdown reports with per-case deltas, round history, and decision reasoning
- **Fake Model Support**: Run full pipeline without API keys using deterministic fake models
