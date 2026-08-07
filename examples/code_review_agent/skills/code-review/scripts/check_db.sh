#!/bin/bash
# check_db.sh — Database lifecycle checks
set -euo pipefail

cd "$1" || exit 1
staticcheck -checks "SA1017,SA5001" ./... 2>/dev/null || true
go vet ./... 2>&1 | grep -i 'rows\|tx\|Rollback\|Close' || true
