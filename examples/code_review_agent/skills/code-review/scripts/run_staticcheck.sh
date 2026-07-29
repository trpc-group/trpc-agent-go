#!/bin/bash
# run_staticcheck.sh — Run staticcheck and capture output
# Usage: ./run_staticcheck.sh [package_path]
set -euo pipefail

PKG="${1:-./...}"
echo "=== staticcheck $PKG ==="
staticcheck "$PKG" 2>&1
EXIT=$?
echo "=== staticcheck exit: $EXIT ==="
exit $EXIT
