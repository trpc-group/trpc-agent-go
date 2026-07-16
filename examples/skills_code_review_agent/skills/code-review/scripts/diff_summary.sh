#!/usr/bin/env bash
set -euo pipefail

in="${1:?diff file required}"
out="${2:?output path required}"
mkdir -p "$(dirname "$out")"

files=$(grep -c '^diff --git ' "$in" || true)
added=$(grep -c '^+[^+]' "$in" || true)
deleted=$(grep -c '^-[^-]' "$in" || true)

cat >"$out" <<JSON
{
  "files_changed": $files,
  "added_lines": $added,
  "deleted_lines": $deleted
}
JSON
