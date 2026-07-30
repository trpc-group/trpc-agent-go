#!/usr/bin/env bash
# run_staticcheck.sh — Run staticcheck and capture output
# Usage: ./run_staticcheck.sh [package_path]
set -euo pipefail

if ! command -v staticcheck &>/dev/null; then
    echo "Warning: 'staticcheck' not found — install with: go install honnef.co/go/tools/cmd/staticcheck@latest"
    echo "=== staticcheck skipped ==="
    exit 0
fi

PKG="${1:-./...}"
echo "=== staticcheck $PKG ==="
staticcheck "$PKG" 2>&1
EXIT=$?
echo "=== staticcheck exit: $EXIT ==="
exit $EXIT
