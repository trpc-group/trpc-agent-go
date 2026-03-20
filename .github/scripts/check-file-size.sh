#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
cd "${repo_root}"

file_size_limit_bytes="${CHECK_FILE_SIZE_LIMIT_BYTES:-1048576}"

changed_files=()
change_scope=""

human_size() {
  local bytes="$1"
  awk -v bytes="${bytes}" 'BEGIN {
    split("B KiB MiB GiB TiB", units, " ")
    value = bytes + 0
    unit = 1
    while (value >= 1024 && unit < length(units)) {
      value /= 1024
      unit++
    }
    if (unit == 1) {
      printf "%.0f %s", value, units[unit]
    } else {
      printf "%.1f %s", value, units[unit]
    }
  }'
}

resolve_limit() {
  printf '%s|uniform limit\n' "${file_size_limit_bytes}"
}

load_changed_files() {
  local base_ref before_sha merge_base zero_sha
  zero_sha="0000000000000000000000000000000000000000"

  if [ -n "${CHECK_FILE_SIZE_COMPARE_RANGE:-}" ]; then
    change_scope="compare range ${CHECK_FILE_SIZE_COMPARE_RANGE}"
    mapfile -d '' changed_files < <(git diff --name-only -z --diff-filter=ACMR "${CHECK_FILE_SIZE_COMPARE_RANGE}" --)
    return
  fi

  if [ "${GITHUB_ACTIONS:-false}" = "true" ]; then
    base_ref="${CHECK_FILE_SIZE_BASE_REF:-${GITHUB_BASE_REF:-}}"
    before_sha="${CHECK_FILE_SIZE_BEFORE_SHA:-}"
    if [ -n "${base_ref}" ]; then
      if git fetch --no-tags origin "${base_ref}" >/dev/null 2>&1; then
        merge_base="$(git merge-base HEAD FETCH_HEAD 2>/dev/null || true)"
        if [ -n "${merge_base}" ]; then
          change_scope="merge base ${merge_base}..HEAD (base ref ${base_ref})"
          mapfile -d '' changed_files < <(git diff --name-only -z --diff-filter=ACMR "${merge_base}..HEAD" --)
          return
        fi
      fi
    fi
    if [ -n "${before_sha}" ] && [ "${before_sha}" != "${zero_sha}" ] && git cat-file -e "${before_sha}^{commit}" >/dev/null 2>&1; then
      change_scope="push range ${before_sha}..HEAD"
      mapfile -d '' changed_files < <(git diff --name-only -z --diff-filter=ACMR "${before_sha}..HEAD" --)
      return
    fi
    if git rev-parse HEAD^ >/dev/null 2>&1; then
      change_scope="fallback range HEAD^..HEAD"
      mapfile -d '' changed_files < <(git diff --name-only -z --diff-filter=ACMR HEAD^..HEAD --)
      return
    fi
    change_scope="tracked files"
    mapfile -d '' changed_files < <(git ls-files -z)
    return
  fi

  if ! git diff --quiet HEAD --; then
    change_scope="working tree diff against HEAD"
    mapfile -d '' changed_files < <(git diff --name-only -z --diff-filter=ACMR HEAD --)
    return
  fi
  if git rev-parse HEAD^ >/dev/null 2>&1; then
    change_scope="local range HEAD^..HEAD"
    mapfile -d '' changed_files < <(git diff --name-only -z --diff-filter=ACMR HEAD^..HEAD --)
    return
  fi
  change_scope="tracked files"
  mapfile -d '' changed_files < <(git ls-files -z)
}

echo "::group::Checking changed file sizes"
load_changed_files

echo "Change scope: ${change_scope}"
echo "Applied limit: $(human_size "${file_size_limit_bytes}") per file"

if [ "${#changed_files[@]}" -eq 0 ]; then
  echo "No added or modified files detected."
  echo "::endgroup::"
  exit 0
fi

checked_count=0
violations=()

for path in "${changed_files[@]}"; do
  if [ ! -f "${path}" ]; then
    continue
  fi
  checked_count=$((checked_count + 1))
  size_bytes="$(stat -c '%s' "${path}")"
  IFS='|' read -r limit_bytes rule_name < <(resolve_limit)
  if [ "${size_bytes}" -le "${limit_bytes}" ]; then
    continue
  fi
  echo "::error file=${path}::File size $(human_size "${size_bytes}") exceeds limit $(human_size "${limit_bytes}") for rule '${rule_name}'."
  violations+=("${path}|${size_bytes}|${limit_bytes}|${rule_name}")
done

echo "Checked ${checked_count} file(s)."

if [ "${#violations[@]}" -gt 0 ]; then
  echo "Oversized files detected:"
  for entry in "${violations[@]}"; do
    IFS='|' read -r path size_bytes limit_bytes rule_name <<< "${entry}"
    echo "- ${path}: $(human_size "${size_bytes}") > $(human_size "${limit_bytes}") (${rule_name})"
  done
  echo "::error::Found files that exceed the configured size limits."
  echo "::endgroup::"
  exit 1
fi

echo "All checked files are within the configured size limits."
echo "::endgroup::"
