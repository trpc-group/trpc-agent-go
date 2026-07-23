# Usage

```bash
export REVIEW_DIFF_PATH=/path/to/diff.patch
export REVIEW_OUT_DIR=/path/to/out
bash scripts/run_checks.sh
```

The script prints a JSON array of findings to stdout (may be empty `[]`).
Host Orchestrator also runs a deterministic Go rule engine and merges results.
