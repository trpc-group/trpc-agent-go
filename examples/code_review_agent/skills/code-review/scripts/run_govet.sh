#!/usr/bin/env bash
# run_govet.sh — Run go vet and capture output
# Usage: ./run_govet.sh [package_path]
set -euo pipefail

if ! command -v go &>/dev/null; then
    echo "Error: 'go' binary not found in PATH"
    exit 1
fi

PKG="${1:-./...}"

# Check that we are inside a Go module
if [ ! -f go.mod ] && [ ! -f "$(dirname "$PKG" 2>/dev/null || echo '.')/go.mod" ]; then
    echo "Warning: no go.mod found — 'go vet' may fail without a module"
fi

echo "=== go vet $PKG ==="
go vet "$PKG" 2>&1
EXIT=$?
echo "=== go vet exit: $EXIT ==="
exit $EXIT
