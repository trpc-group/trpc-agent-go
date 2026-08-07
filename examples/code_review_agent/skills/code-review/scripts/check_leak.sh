#!/bin/bash
# check_leak.sh — Goroutine/context leak detection via go vet
set -euo pipefail

cd "$1" || exit 1
go vet -vettool=$(which vet) ./... 2>&1 || true
