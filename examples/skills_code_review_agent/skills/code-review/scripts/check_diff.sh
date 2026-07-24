#!/usr/bin/env bash
set -euo pipefail

readonly max_bytes=$((8 * 1024 * 1024))

if [[ "$#" -ne 1 ]]; then
  echo "usage: check_diff.sh <unified-diff>" >&2
  exit 2
fi

diff_file="$1"
if [[ ! -f "${diff_file}" ]]; then
  echo "diff file does not exist" >&2
  exit 2
fi

size_bytes="$(wc -c < "${diff_file}")"
if (( size_bytes > max_bytes )); then
  echo "diff exceeds input limit" >&2
  exit 2
fi

awk '
  BEGIN { files = 0; hunks = 0; additions = 0; deletions = 0 }
  /^diff --git / { files++ }
  /^@@ / { hunks++ }
  /^\+[^+]/ { additions++ }
  /^-[^-]/ { deletions++ }
  END {
    printf "{\"files\":%d,\"hunks\":%d,\"additions\":%d,\"deletions\":%d}\n",
      files, hunks, additions, deletions
  }
' "${diff_file}"
