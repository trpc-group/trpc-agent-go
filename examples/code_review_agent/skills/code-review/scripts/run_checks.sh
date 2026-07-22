#!/usr/bin/env bash
#
# run_checks.sh — Runs go vet and go test on the repository.
# This script is intended to be executed inside a sandbox workspace.
#
# Usage: bash scripts/run_checks.sh <repo-path>
#
set -uo pipefail

REPO_PATH="${1:-.}"
cd "$REPO_PATH"

echo "=== Running go vet ==="
status=0
if ! go vet ./... 2>&1; then
    status=1
fi

echo ""
echo "=== Running go test ==="
if ! go test ./... -count=1 -timeout=30s 2>&1; then
    status=1
fi

echo ""
echo "=== Running staticcheck (if available) ==="
if command -v staticcheck &>/dev/null; then
    if ! staticcheck ./... 2>&1; then
        status=1
    fi
else
    echo "staticcheck not installed, skipping"
fi

exit "$status"
