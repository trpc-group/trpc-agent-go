#!/usr/bin/env bash
set -euo pipefail

mode="${1:-}"
if [ "$#" -ne 1 ]; then
  echo "usage: run_checks.sh test|vet|staticcheck" >&2
  exit 2
fi

repo_dir="${REVIEW_REPO_DIR:-.}"
case "$mode" in
  test)
    cd "$repo_dir"
    go test ./...
    ;;
  vet)
    cd "$repo_dir"
    go vet ./...
    ;;
  staticcheck)
    if ! command -v staticcheck >/dev/null 2>&1; then
      echo "staticcheck skipped: command not found" >&2
      exit 0
    fi
    cd "$repo_dir"
    staticcheck ./...
    ;;
  *)
    echo "unsupported check: $mode" >&2
    exit 2
    ;;
esac
