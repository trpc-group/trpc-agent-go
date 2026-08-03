#!/usr/bin/env bash
set -euo pipefail

run_module_checks() {
  local module_dir="$1"
  (
    cd "$module_dir"
    go test ./...
    go vet ./...
  )
}

if [ ! -f go.mod ]; then
  echo "no go.mod found; deterministic diff-only review completed"
  exit 0
fi

declare -A seen_modules=()
modules=()

if [ -n "${CODE_REVIEW_CHANGED_MODULES:-}" ]; then
  while IFS= read -r module_dir; do
    [ -n "$module_dir" ] || continue
    if [ -z "${seen_modules[$module_dir]:-}" ]; then
      seen_modules["$module_dir"]=1
      modules+=("$module_dir")
    fi
  done <<< "${CODE_REVIEW_CHANGED_MODULES}"
fi

if [ "${#modules[@]}" -eq 0 ]; then
  modules=(".")
fi

for module_dir in "${modules[@]}"; do
  run_module_checks "$module_dir"
done
