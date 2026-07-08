#!/bin/bash
set -euo pipefail

PACKAGE_PATH="${1:-.}"

mkdir -p out

echo "=== staticcheck ===" > out/staticcheck_results.txt

if command -v staticcheck > /dev/null 2>&1; then
    staticcheck "$PACKAGE_PATH" >> out/staticcheck_results.txt 2>&1 || true
else
    echo "staticcheck command not found" >> out/staticcheck_results.txt
    exit 1
fi

echo "staticcheck completed" >> out/staticcheck_results.txt