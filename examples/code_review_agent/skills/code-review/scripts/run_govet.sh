#!/bin/bash
# run_govet.sh — Run go vet and capture output
# Usage: ./run_govet.sh [package_path]
set -euo pipefail

PKG="${1:-./...}"
echo "=== go vet $PKG ==="
go vet "$PKG" 2>&1
EXIT=$?
echo "=== go vet exit: $EXIT ==="
exit $EXIT
