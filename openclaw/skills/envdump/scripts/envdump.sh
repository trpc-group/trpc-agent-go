#!/usr/bin/env bash
set -euo pipefail

out_file="${1:?output file required}"
mkdir -p "$(dirname "${out_file}")"

{
  echo "date: $(date)"
  echo "pwd: $(pwd)"
  echo "uname: $(uname -a 2>/dev/null || true)"
  echo ""
  echo "env:"
  env | sort
} > "${out_file}"

echo "wrote ${out_file}"
