#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: summarize_file.sh <input> <output>" >&2
  exit 1
fi

input="$1"
output="$2"

if [ ! -f "$input" ]; then
  echo "input file not found: $input" >&2
  exit 1
fi

mkdir -p "$(dirname "$output")"

lines=$(wc -l <"$input" | awk '{print $1}')
words=$(wc -w <"$input" | awk '{print $1}')
bytes=$(wc -c <"$input" | awk '{print $1}')

{
  echo "File: $input"
  echo "Lines: $lines"
  echo "Words: $words"
  echo "Bytes: $bytes"
  echo
  echo "First 20 non-empty lines:"
  nl -ba "$input" \
    | sed '/^[[:space:]]*$/d' \
    | head -n 20 || true
} >"$output"
