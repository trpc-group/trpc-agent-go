#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${1:-"${ROOT_DIR}/output/integration-proof"}"
PROOF="${OUT_DIR}/proof.md"

rm -rf "${OUT_DIR}"
mkdir -p "${OUT_DIR}"

run_and_log() {
  local name="$1"
  shift
  echo "== ${name} =="
  (cd "${ROOT_DIR}" && "$@") 2>&1 | tee "${OUT_DIR}/${name}.log"
}

run_and_log go-test go test ./...
run_and_log go-vet go vet ./...

"${ROOT_DIR}/scripts/run_all_fixtures.sh" "${OUT_DIR}/fixtures" | tee "${OUT_DIR}/fixtures.log"

container_status="skipped"
if docker version >/dev/null 2>&1; then
  run_and_log container-smoke go run . --container-smoke --container-install-staticcheck \
    --container-base-image docker.m.daocloud.io/library/golang:1.23-bookworm \
    --output-dir "${OUT_DIR}/container-smoke" --timeout 120s
  if ! jq -e 'any(.sandbox_runs[]; .command == "staticcheck" and .status == "success" and .exit_code == 0)' \
    "${OUT_DIR}/container-smoke/review_report.json" >/dev/null; then
    echo "container smoke did not run staticcheck successfully" >&2
    exit 1
  fi
  container_status="passed"
fi

{
  echo "# Integration Proof"
  echo
  echo "- go test: passed"
  echo "- go vet: passed"
  echo "- fixture matrix: passed"
  echo "- container smoke: ${container_status}"
  echo
  echo "## Fixture Matrix"
  echo
  echo '```tsv'
  cat "${OUT_DIR}/fixtures/summary.tsv"
  echo '```'
  if [[ "${container_status}" == "passed" ]]; then
    echo
    echo "## Container Smoke"
    echo
    echo "The container smoke preinstalls staticcheck, so staticcheck must run successfully instead of being recorded as an optional unavailable tool."
    echo
    jq -r '.sandbox_runs[] | [.command, (.args | join(" ")), .status, .exit_code, .duration_ms] | @tsv' \
      "${OUT_DIR}/container-smoke/review_report.json"
  fi
} > "${PROOF}"

echo "Integration proof written to ${PROOF}"
