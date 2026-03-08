#!/bin/bash
# Vertical evaluation runner for trpc-agent-go knowledge system.
#
# Usage:
#   ./vertical_eval/run.sh hybrid_weight  # Compare hybrid weights
#   ./vertical_eval/run.sh retrieval_k    # Compare retrieval k values
#   ./vertical_eval/run.sh all            # Run all suites
#
# Environment variables (must be set):
#   OPENAI_API_KEY, OPENAI_BASE_URL, MODEL_NAME
#
# Optional:
#   MAX_QA=10           Number of QA items per experiment (default: 10)
#   WORKERS=30          RAGAS evaluation workers (default: 30)
#   BASE_PORT=9000      Go service port (default: 9000)
#   SKIP_LOAD=false     Skip document loading (default: false)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EVAL_DIR="$(dirname "$SCRIPT_DIR")"

cd "$EVAL_DIR"

SUITE="${1:-hybrid_weight}"
MAX_QA="${MAX_QA:-10}"
WORKERS="${WORKERS:-30}"
BASE_PORT="${BASE_PORT:-9000}"

EXTRA_ARGS=""
if [ "${SKIP_LOAD:-false}" = "true" ]; then
    EXTRA_ARGS="$EXTRA_ARGS --skip-load"
fi

echo "============================================"
echo "Vertical Evaluation: $SUITE"
echo "Max QA: $MAX_QA"
echo "Workers: $WORKERS"
echo "Base Port: $BASE_PORT"
echo "============================================"

python -m vertical_eval.main \
    --suite "$SUITE" \
    --max-qa "$MAX_QA" \
    --workers "$WORKERS" \
    --base-port "$BASE_PORT" \
    $EXTRA_ARGS \
    2>&1 | tee "vertical_eval/results/${SUITE}_$(date +%Y%m%d_%H%M%S).log"
