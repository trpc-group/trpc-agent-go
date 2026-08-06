#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_BASE="${ROOT_DIR}/output"
DEFAULT_OUTPUT_ROOT="${OUTPUT_BASE}/fixtures"
OUTPUT_ROOT="${1:-"${DEFAULT_OUTPUT_ROOT}"}"
OWNERSHIP_MARKER=".fixtures-owned"
CHILD_MARKER=".fixture-owned"

canonical_path() {
  local target="$1"
  if [[ -e "${target}" ]]; then
    (cd "${target}" && pwd -P)
    return
  fi
  local parent
  parent="$(dirname "${target}")"
  local base
  base="$(basename "${target}")"
  mkdir -p "${parent}"
  parent="$(cd "${parent}" && pwd -P)"
  printf '%s/%s\n' "${parent}" "${base}"
}

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

mkdir -p "${OUTPUT_BASE}"
OUTPUT_BASE="$(cd "${OUTPUT_BASE}" && pwd -P)"
DEFAULT_OUTPUT_ROOT="${OUTPUT_BASE}/fixtures"
OUTPUT_ROOT="$(canonical_path "${OUTPUT_ROOT}")"

case "${OUTPUT_ROOT}" in
  "${OUTPUT_BASE}"/*) ;;
  *)
    echo "fixture output root must stay under ${OUTPUT_BASE}" >&2
    exit 2
    ;;
esac

if [[ -e "${OUTPUT_ROOT}" && ! -d "${OUTPUT_ROOT}" ]]; then
  echo "fixture output root must be a directory: ${OUTPUT_ROOT}" >&2
  exit 2
fi

if [[ "${OUTPUT_ROOT}" != "${DEFAULT_OUTPUT_ROOT}" && -d "${OUTPUT_ROOT}" && ! -f "${OUTPUT_ROOT}/${OWNERSHIP_MARKER}" ]]; then
  if find "${OUTPUT_ROOT}" -mindepth 1 -maxdepth 1 | grep -q .; then
    echo "refusing to reuse unowned fixture output root: ${OUTPUT_ROOT}" >&2
    exit 2
  fi
fi

mkdir -p "${OUTPUT_ROOT}"
touch "${OUTPUT_ROOT}/${OWNERSHIP_MARKER}"
summary="${OUTPUT_ROOT}/summary.tsv"
printf 'fixture\tfindings\twarnings\tneeds_human_review\tsandbox_runs\tpermission_decisions\tpermission_needs_human_review\tstatus\n' > "${summary}"

for fixture in "${fixtures[@]}"; do
  out_dir="${OUTPUT_ROOT}/${fixture}"
  if [[ -e "${out_dir}" && ! -d "${out_dir}" ]]; then
    echo "fixture output path must be a directory: ${out_dir}" >&2
    exit 2
  fi
  if [[ -d "${out_dir}" && ! -f "${out_dir}/${CHILD_MARKER}" ]]; then
    if find "${out_dir}" -mindepth 1 -maxdepth 1 | grep -q .; then
      echo "refusing to clean unowned fixture directory: ${out_dir}" >&2
      exit 2
    fi
  fi
  rm -rf "${out_dir}"
  mkdir -p "${out_dir}"
  touch "${out_dir}/${CHILD_MARKER}"
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
if [[ "${OUTPUT_ROOT}" == "${DEFAULT_OUTPUT_ROOT}" ]]; then
  cp "${summary}" "${ROOT_DIR}/output/fixtures_summary.tsv"
  echo "Tracked summary example updated at ${ROOT_DIR}/output/fixtures_summary.tsv"
fi
