#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
OUTPUT_ROOT="${ROOT_DIR}/output"
mkdir -p "${OUTPUT_ROOT}"
OUTPUT_ROOT="$(cd "${OUTPUT_ROOT}" && pwd -P)"
DEFAULT_OUT_DIR="${OUTPUT_ROOT}/integration-proof"
OUT_ARG="${1:-"${DEFAULT_OUT_DIR}"}"
OWNERSHIP_MARKER=".integration-proof-owned"

canonical_output_target() {
	local target="$1"
	case "${target}" in
		/*) ;;
		*) target="${ROOT_DIR}/${target}" ;;
	esac
	target="${target%/}"
	local existing="${target}"
	local suffix=""
	while [[ ! -e "${existing}" && "${existing}" != "/" ]]; do
		suffix="/$(basename "${existing}")${suffix}"
		existing="$(dirname "${existing}")"
	done
	if [[ ! -d "${existing}" ]]; then
		echo "refusing to use output path below non-directory parent: ${target}" >&2
		exit 2
	fi
	local real_existing
	real_existing="$(cd "${existing}" && pwd -P)"
	printf '%s%s\n' "${real_existing}" "${suffix}"
}

ensure_output_contained() {
	local real_target
	real_target="$(canonical_output_target "$1")"
	case "${real_target}" in
		"${OUTPUT_ROOT}"/*) printf '%s\n' "${real_target}" ;;
		*)
			echo "integration proof output must be under ${OUTPUT_ROOT}" >&2
			exit 2
			;;
	esac
}

OUT_DIR="$(ensure_output_contained "${OUT_ARG}")"
PROOF="${OUT_DIR}/proof.md"

if [[ "${OUT_DIR}" != "${DEFAULT_OUT_DIR}" && -d "${OUT_DIR}" && ! -f "${OUT_DIR}/${OWNERSHIP_MARKER}" ]]; then
	if find "${OUT_DIR}" -mindepth 1 -maxdepth 1 | grep -q .; then
		echo "refusing to clean unowned output directory: ${OUT_DIR}" >&2
		echo "remove it manually or add ${OWNERSHIP_MARKER} after verifying it only contains generated proof files" >&2
		exit 2
	fi
fi
if [[ -e "${OUT_DIR}" && ! -d "${OUT_DIR}" ]]; then
	echo "refusing to replace non-directory output path: ${OUT_DIR}" >&2
	exit 2
fi

OUT_DIR="$(ensure_output_contained "${OUT_DIR}")"
rm -rf "${OUT_DIR}"
mkdir -p "${OUT_DIR}"
touch "${OUT_DIR}/${OWNERSHIP_MARKER}"

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
