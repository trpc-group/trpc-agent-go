#!/usr/bin/env bash
# parse_diff.sh — Parse unified diff and extract file/line info
# Usage: ./parse_diff.sh <diff_file>
set -euo pipefail

DIFF_FILE="${1:-}"
if [ -z "$DIFF_FILE" ]; then
    echo "Usage: $0 <diff_file>"
    exit 1
fi

if [ ! -f "$DIFF_FILE" ]; then
    echo "Error: file not found: $DIFF_FILE"
    exit 1
fi

echo "=== Files changed ==="
grep -E '^\+\+\+ b/' "$DIFF_FILE" | sed 's|^+++ b/||'

echo ""
echo "=== Hunk headers ==="
grep -E '^@@' "$DIFF_FILE"

echo ""
echo "=== Added lines (first 50) ==="
grep -E '^\+' "$DIFF_FILE" | grep -v '^\+\+\+' | head -50

echo ""
echo "=== Removed lines (first 50) ==="
grep -E '^\-' "$DIFF_FILE" | grep -v '^\-\-\-' | head -50
