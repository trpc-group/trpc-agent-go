#!/usr/bin/env bash
set -euo pipefail

input="${1:?diff input required}"
output="${2:?statistics output required}"
mkdir -p "$(dirname "$output")"

files="$(grep -c '^diff --git ' "$input" || true)"
added="$(grep -c '^+[^+]' "$input" || true)"
deleted="$(grep -c '^-[^-]' "$input" || true)"

printf '{"files_changed":%s,"added_lines":%s,"deleted_lines":%s}\n' \
  "$files" "$added" "$deleted" >"$output"
