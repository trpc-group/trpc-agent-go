#!/usr/bin/env bash
set -euo pipefail

# diff_summary.sh summarizes a unified diff.
# Usage: diff_summary.sh <diff-file>
#        diff_summary.sh -            (read the diff from stdin)
diff_file="${1:-}"
if [[ -z "${diff_file}" ]]; then
  echo "usage: diff_summary.sh <diff-file|->" >&2
  exit 2
fi

if [[ "${diff_file}" == "-" ]]; then
  diff_content="$(cat)"
else
  diff_content="$(cat "${diff_file}")"
fi

printf 'files_changed='
printf '%s\n' "${diff_content}" | grep -cE '^\+\+\+ b/' || true
printf 'added_lines='
# "([^+]|$)" keeps added blank lines in the count while still skipping
# the "+++ b/..." file headers.
printf '%s\n' "${diff_content}" | grep -cE '^\+([^+]|$)' || true
