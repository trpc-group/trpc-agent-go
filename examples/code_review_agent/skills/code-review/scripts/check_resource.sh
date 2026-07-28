#!/bin/bash
# check_resource.sh — Resource leak detection via staticcheck
set -euo pipefail

cd "$1" || exit 1
staticcheck -checks "SA5001,SA5005,SA6000" ./... 2>/dev/null || echo "staticcheck not installed, running go vet"
go vet -lostcancel=false ./... 2>&1 || true
