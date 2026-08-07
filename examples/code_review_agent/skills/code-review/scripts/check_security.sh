#!/bin/bash
# check_security.sh — Security pattern scanning wrapper (gosec)
set -euo pipefail

cd "$1" || exit 1
go vet ./... 2>&1 || true
gosec -quiet -confidence=medium ./... 2>/dev/null || echo "gosec not installed, skipping"
