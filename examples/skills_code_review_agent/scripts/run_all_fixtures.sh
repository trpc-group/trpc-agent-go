#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_ROOT="${1:-"${ROOT_DIR}/output/fixtures"}"

fixtures=(
  no_issue
  security_issue
  goroutine_context_leak
  resource_not_closed
  db_lifecycle
  missing_test
  duplicate_finding
  sandbox_failure
  sensitive_redaction
  advanced_risks
)

mkdir -p "${OUTPUT_ROOT}"
summary="${OUTPUT_ROOT}/summary.tsv"
printf 'fixture\tfindings\twarnings\tneeds_human_review\tsandbox_runs\tpermission_decisions\tpermission_needs_human_review\tstatus\n' > "${summary}"

for fixture in "${fixtures[@]}"; do
  out_dir="${OUTPUT_ROOT}/${fixture}"
  rm -rf "${out_dir}"
  if [[ "${fixture}" == "sandbox_failure" ]]; then
    (cd "${ROOT_DIR}" && go run . --fixture "${fixture}" --executor fake-fail --output-dir "${out_dir}")
  else
    (cd "${ROOT_DIR}" && go run . --fixture "${fixture}" --dry-run --output-dir "${out_dir}")
  fi
  jq -r --arg fixture "${fixture}" '
    [
      $fixture,
      (.findings | length),
      (.warnings | length),
      (.needs_human_review | length),
      (.sandbox_runs | length),
      (.permission_decisions | length),
      (.permission_summary.needs_human_review_count // 0),
      .task.status
    ] | @tsv
  ' "${out_dir}/review_report.json" >> "${summary}"
done

echo "Fixture reports written to ${OUTPUT_ROOT}"
echo "Summary written to ${summary}"
if [[ "${OUTPUT_ROOT}" == "${ROOT_DIR}/output/fixtures" ]]; then
  cp "${summary}" "${ROOT_DIR}/output/fixtures_summary.tsv"
  echo "Tracked summary example updated at ${ROOT_DIR}/output/fixtures_summary.tsv"
fi
