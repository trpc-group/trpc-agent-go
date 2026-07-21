#!/usr/bin/env bash
set -euo pipefail

# secret_scan.sh scans a directory (or stdin with "-") for secret-looking text.
# Usage: secret_scan.sh [target-dir]
#        secret_scan.sh -             (scan text from stdin)
target="${1:-.}"

pattern='(api[_-]?key|token|password|secret)\s*[:=]'

# run_grep tolerates "no matches" (status 1) but still fails loudly on
# real grep errors (status > 1, e.g. unreadable target).
run_grep() {
  local status=0
  grep "$@" || status=$?
  if (( status > 1 )); then
    echo "secret scan failed: grep exited with status ${status}" >&2
    exit "${status}"
  fi
}

if [[ "${target}" == "-" ]]; then
  run_grep -InE "${pattern}" -
else
  run_grep -RInE "${pattern}" "${target}"
fi
