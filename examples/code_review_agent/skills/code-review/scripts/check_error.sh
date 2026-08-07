#!/bin/bash
# check_error.sh — Error handling pattern checks
set -euo pipefail

cd "$1" || exit 1
go vet -errcheck ./... 2>&1 || true
staticcheck -checks "SA4006,SA5008,SA5009" ./... 2>/dev/null || true
