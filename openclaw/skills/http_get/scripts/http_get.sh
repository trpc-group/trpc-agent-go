#!/usr/bin/env bash
set -euo pipefail

url="${1:?url required}"
out_file="${2:?output file required}"
mkdir -p "$(dirname "${out_file}")"

curl -fsSL "${url}" -o "${out_file}"
echo "wrote ${out_file}"
