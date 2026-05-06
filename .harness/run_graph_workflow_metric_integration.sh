#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

if [[ -f ".env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source ".env"
  set +a
fi

cd "${repo_root}/.harness"

TRPC_AGENT_GO_RUN_REAL_LLM_GRAPH_METRIC=1 \
  go test -tags=integration . \
    -run 'Test(RealLLMGraphWorkflowMetric|RunnerGraphAgentWorkflowTypesMetric)' \
    -count=1 \
    -v
