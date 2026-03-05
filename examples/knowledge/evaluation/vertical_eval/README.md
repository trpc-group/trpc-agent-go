# Vertical Evaluation for trpc-agent-go Knowledge System

Vertical evaluation framework for systematically benchmarking different configurations
of the trpc-agent-go knowledge system using the HuggingFace QA dataset.

## Experiment Suites

| Suite | What it tests | Configs |
|-------|--------------|---------|
| `hybrid_weight` | Different vector/text weight ratios for hybrid search | 6 |
| `retrieval_k` | Number of retrieved documents (2/4/8/16) | 4 |

## Quick Start

```bash
# Set environment variables
export OPENAI_API_KEY="your-key"
export OPENAI_BASE_URL="http://your-api-endpoint/"
export MODEL_NAME="your-model"

# Run a single suite
cd examples/knowledge/evaluation
python -m vertical_eval.main --suite hybrid_weight --max-qa 10

# Run all suites
python -m vertical_eval.main --suite all --max-qa 10

# Run specific experiments from a suite
python -m vertical_eval.main --suite hybrid_weight --experiments hybrid_v90_t10 hybrid_v50_t50

# Use the shell script
./vertical_eval/run.sh hybrid_weight
```

## CLI Options

```
--suite          Experiment suite: hybrid_weight|retrieval_k|all
--max-qa         Max QA items per experiment (default: 10)
--k              Override retrieval k for all experiments
--skip-load      Skip document loading (assume already loaded)
--base-port      Go service port (default: 9000)
--output-dir     Custom output directory
--workers        RAGAS evaluation concurrency (default: 30)
--timeout        Evaluation timeout in seconds (default: 600)
--experiments    Run only specific experiments by name
```

## Go Service Parameters

The Go knowledge service supports these command-line flags for vertical evaluation:

```
--hybrid-vector-weight   Vector weight for hybrid search (default: 0.99999)
--hybrid-text-weight     Text weight for hybrid search (default: 0.00001)
--pg-table               PGVector table name override
```

## Output

Results are saved to `vertical_eval/results/<suite>/`:
- `<experiment_name>.json` - Individual experiment results
- `_combined_<suite>.json` - All results in one file
- `REPORT_<suite>.md` - Markdown comparison table

## Architecture

```
vertical_eval/
├── __init__.py
├── main.py          # CLI entry point
├── config.py        # Experiment configurations
├── runner.py        # Go service manager + experiment runner
├── report.py        # Report generation
├── run.sh           # Shell wrapper
├── results/         # Output directory
└── README.md
```

Each experiment:
1. Starts a Go service with specific hybrid weight flags
2. Loads documents into a dedicated PGVector table
3. Runs Q&A against the HuggingFace dataset
4. Evaluates with RAGAS metrics
5. Stops the Go service and saves results
