#!/usr/bin/env bash
set -euo pipefail

mode="${1:-}"
if [ "$#" -ne 1 ]; then
  echo "usage: run_checks.sh test|vet|staticcheck" >&2
  exit 2
fi

repo_dir="${REVIEW_REPO_DIR:-.}"
module_manifest="$repo_dir/.trpc-agent-review-modules"

run_in_modules() {
  if [ ! -f "$module_manifest" ]; then
    echo "affected module manifest is missing" >&2
    return 2
  fi

  local module=""
  local module_dir=""
  local module_count=0
  local status=0
  while IFS= read -r -d '' module; do
    case "$module" in
      ""|/*|..|../*|*/..|*/../*)
        echo "unsafe module path: $module" >&2
        return 2
        ;;
    esac
    module_dir="$repo_dir"
    if [ "$module" != "." ]; then
      module_dir="$repo_dir/$module"
    fi
    if [ ! -f "$module_dir/go.mod" ]; then
      echo "module file is missing: $module/go.mod" >&2
      status=1
      continue
    fi
    module_count=$((module_count + 1))
    echo "==> $mode $module"
    (cd "$module_dir" && "$@") || status=1
  done < "$module_manifest"

  if [ "$module_count" -eq 0 ]; then
    echo "affected module manifest is empty" >&2
    return 2
  fi
  return "$status"
}

case "$mode" in
  test)
    run_in_modules go test ./...
    ;;
  vet)
    run_in_modules go vet ./...
    ;;
  staticcheck)
    if ! command -v staticcheck >/dev/null 2>&1; then
      echo "staticcheck skipped: command not found" >&2
      exit 0
    fi
    run_in_modules staticcheck ./...
    ;;
  *)
    echo "unsupported check: $mode" >&2
    exit 2
    ;;
esac
