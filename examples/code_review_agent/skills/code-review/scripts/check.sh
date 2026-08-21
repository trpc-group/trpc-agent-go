#!/usr/bin/env bash
set -euo pipefail

script_dir="${BASH_SOURCE[0]%/*}"
if [[ "$script_dir" == "${BASH_SOURCE[0]}" ]]; then
  script_dir="."
fi

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
cat > "$tmp"

if command -v python3 >/dev/null 2>&1; then
  python3 "$script_dir/check_rules.py" "$tmp"
else
  GO111MODULE=off go run "$script_dir/check_fallback.go" "$tmp"
fi
