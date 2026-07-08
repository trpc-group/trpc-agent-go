#!/bin/bash
set -euo pipefail

PACKAGE_PATH="${1:-.}"

mkdir -p out

echo "=== go vet ===" > out/go_vet_results.txt

if command -v go > /dev/null 2>&1; then
    go vet "$PACKAGE_PATH" >> out/go_vet_results.txt 2>&1 || true
else
    echo "go command not found" >> out/go_vet_results.txt
    exit 1
fi

echo "go vet completed" >> out/go_vet_results.txt