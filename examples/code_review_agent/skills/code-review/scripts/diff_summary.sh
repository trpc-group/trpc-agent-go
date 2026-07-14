#!/usr/bin/env bash
set -euo pipefail

diff_file="${1:-}"
if [[ -z "${diff_file}" ]]; then
  echo "usage: diff_summary.sh <diff-file>" >&2
  exit 2
fi

printf 'files_changed='
grep -E '^\+\+\+ b/' "${diff_file}" | wc -l | tr -d ' '
printf 'added_lines='
grep -E '^\+[^+]' "${diff_file}" | wc -l | tr -d ' '
