#!/usr/bin/env bash
set -euo pipefail

out_file="${1:?output file required}"
mkdir -p "$(dirname "${out_file}")"
echo "hello from openclaw" > "${out_file}"

