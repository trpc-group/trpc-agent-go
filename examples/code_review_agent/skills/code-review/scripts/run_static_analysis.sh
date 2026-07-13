#!/bin/bash
set -euo pipefail

DIFF_FILE="${1:-}"
if [ -z "$DIFF_FILE" ]; then
    echo "Usage: $0 <diff_file>"
    exit 1
fi

mkdir -p out

echo "=== Running Static Analysis ===" > out/static_analysis.txt

if [ -f "$DIFF_FILE" ]; then
    echo "Parsing diff file: $DIFF_FILE" >> out/static_analysis.txt
    changed_files=$(bash scripts/parse_diff.sh "$DIFF_FILE" | grep -v "^---" || true)
    echo "Changed files:" >> out/static_analysis.txt
    echo "$changed_files" >> out/static_analysis.txt
    
    for file in $changed_files; do
        if [ -f "$file" ]; then
            echo "" >> out/static_analysis.txt
            echo "=== Analyzing: $file ===" >> out/static_analysis.txt
            
            if command -v go > /dev/null 2>&1; then
                echo "" >> out/static_analysis.txt
                echo "--- go vet ---" >> out/static_analysis.txt
                go vet "$file" 2>&1 | head -100 >> out/static_analysis.txt || true
            fi
            
            if command -v staticcheck > /dev/null 2>&1; then
                echo "" >> out/static_analysis.txt
                echo "--- staticcheck ---" >> out/static_analysis.txt
                staticcheck "$file" 2>&1 | head -100 >> out/static_analysis.txt || true
            fi
        fi
    done
else
    echo "Diff file not found: $DIFF_FILE" >> out/static_analysis.txt
    exit 1
fi

echo "Static analysis completed" >> out/static_analysis.txt