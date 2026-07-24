#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
OUTPUT_ROOT="${ROOT_DIR}/output"
SCRIPT="${ROOT_DIR}/scripts/integration_proof.sh"

mkdir -p "${OUTPUT_ROOT}"
OUTPUT_ROOT="$(cd "${OUTPUT_ROOT}" && pwd -P)"

cleanup_paths=()
cleanup_links=()
cleanup() {
  for path in "${cleanup_links[@]}"; do
    if [[ -e "${path}" || -L "${path}" ]]; then
      if command -v cygpath >/dev/null 2>&1 && command -v cmd.exe >/dev/null 2>&1; then
        cmd.exe /c rmdir "$(cygpath -w "${path}")" >/dev/null 2>&1 || rm -f -- "${path}"
      else
        rm -f -- "${path}"
      fi
    fi
  done
  for path in "${cleanup_paths[@]}"; do
    rm -rf -- "${path}"
  done
}
trap cleanup EXIT

assert_rejected_without_deleting() {
  local target="$1"
  local sentinel="$2"
  local expected="$3"
  local output
  if output="$("${SCRIPT}" "${target}" 2>&1)"; then
    echo "expected integration proof to reject ${target}" >&2
    exit 1
  fi
  if [[ "${output}" != *"${expected}"* ]]; then
    echo "integration proof failed for an unexpected reason: ${output}" >&2
    exit 1
  fi
  if [[ ! -f "${sentinel}" ]]; then
    echo "integration proof deleted sentinel ${sentinel}" >&2
    exit 1
  fi
  echo "rejected safely: ${target}"
  printf '%s\n' "${output}"
}

lexical_dir="$(mktemp -d "${OUTPUT_ROOT}/integration-proof-regression.XXXXXX")"
cleanup_paths+=("${lexical_dir}")
lexical_sentinel="${lexical_dir}/sentinel.txt"
printf 'must survive lexical normalization\n' >"${lexical_sentinel}"
lexical_target="${lexical_dir}/../$(basename "${lexical_dir}")"
assert_rejected_without_deleting "${lexical_target}" "${lexical_sentinel}" "refusing to clean unowned output directory"

outside_dir="$(mktemp -d "${ROOT_DIR}/../integration-proof-regression.XXXXXX")"
cleanup_paths+=("${outside_dir}")
outside_sentinel="${outside_dir}/sentinel.txt"
printf 'must survive containment rejection\n' >"${outside_sentinel}"
assert_rejected_without_deleting "${outside_dir}" "${outside_sentinel}" "integration proof output must be under"

link_parent="${OUTPUT_ROOT}/integration-proof-link-regression.XXXXXX"
link_target="$(mktemp -d "${ROOT_DIR}/../integration-proof-link-target.XXXXXX")"
cleanup_paths+=("${link_target}")
if ln -s "${link_target}" "${link_parent}" 2>/dev/null; then
  cleanup_links+=("${link_parent}")
  link_sentinel="${link_target}/sentinel.txt"
  printf 'must survive symlink containment rejection\n' >"${link_sentinel}"
  assert_rejected_without_deleting "${link_parent}/proof" "${link_sentinel}" "integration proof output must be under"
elif command -v cygpath >/dev/null 2>&1 && command -v cmd.exe >/dev/null 2>&1 &&
  cmd.exe /c mklink /J "$(cygpath -w "${link_parent}")" "$(cygpath -w "${link_target}")" >/dev/null 2>&1 &&
  [[ -d "${link_parent}" ]] &&
  [[ "$(cd "${link_parent}" && pwd -P)" != "$(cd "${OUTPUT_ROOT}" && pwd -P)/$(basename "${link_parent}")" ]]; then
  cleanup_links+=("${link_parent}")
  link_sentinel="${link_target}/sentinel.txt"
  printf 'must survive junction containment rejection\n' >"${link_sentinel}"
  assert_rejected_without_deleting "${link_parent}/proof" "${link_sentinel}" "integration proof output must be under"
else
  echo "symlink case skipped: symlink and junction creation are unavailable"
fi

echo "integration_proof.sh path-safety regression passed"
