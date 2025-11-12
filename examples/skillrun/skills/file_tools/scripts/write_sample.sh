#!/usr/bin/env bash
set -euo pipefail

msg=${1:-"Hello"}
out=${2:-"out/sample.txt"}
mkdir -p "$(dirname "$out")"
printf "%s\n" "$msg" >"$out"
printf "wrote: %s\n" "$out"

