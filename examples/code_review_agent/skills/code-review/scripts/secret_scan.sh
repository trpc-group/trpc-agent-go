#!/usr/bin/env bash
set -euo pipefail

# secret_scan.sh scans a directory (or stdin with "-") for secret-looking text.
# Usage: secret_scan.sh [target-dir]
#        secret_scan.sh -             (scan text from stdin)
target="${1:-.}"

pattern='(api[_-]?key|token|password|secret)\s*[:=]'
if [[ "${target}" == "-" ]]; then
  grep -InE "${pattern}" - || true
else
  grep -RInE "${pattern}" "${target}" || true
fi
