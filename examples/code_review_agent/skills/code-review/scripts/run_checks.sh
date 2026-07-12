#!/usr/bin/env bash
#
# run_checks.sh — Runs go vet and go test on the repository.
# This script is intended to be executed inside a sandbox workspace.
#
# Usage: bash scripts/run_checks.sh <repo-path>
#
set -euo pipefail

REPO_PATH="${1:-.}"
cd "$REPO_PATH"

echo "=== Running go vet ==="
go vet ./... 2>&1 || true

echo ""
echo "=== Running go test ==="
go test ./... -count=1 -timeout=30s 2>&1 || true

echo ""
echo "=== Running staticcheck (if available) ==="
if command -v staticcheck &>/dev/null; then
    staticcheck ./... 2>&1 || true
else
    echo "staticcheck not installed, skipping"
fi
