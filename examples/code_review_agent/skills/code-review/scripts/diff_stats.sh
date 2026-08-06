#!/usr/bin/env bash
set -euo pipefail

input="${1:?diff input required}"
output="${2:?statistics output required}"
mkdir -p "$(dirname "$output")"

files="$(grep -c '^diff --git ' "$input" || true)"
read -r added deleted < <(awk '
  /^diff --git / { in_hunk=0; next }
  /^@@ / { in_hunk=1; next }
  in_hunk && substr($0,1,1) == "+" { added++ }
  in_hunk && substr($0,1,1) == "-" { deleted++ }
  END { printf "%d %d\n", added, deleted }
' "$input")

printf '{"files_changed":%s,"added_lines":%s,"deleted_lines":%s}\n' \
  "$files" "$added" "$deleted" >"$output"
