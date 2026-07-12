#!/usr/bin/env bash
#
# parse_diff.sh — Parses a unified diff file and lists changed files.
# This script is intended to be executed inside a sandbox workspace.
#
# Usage: bash scripts/parse_diff.sh <diff-file>
#
set -euo pipefail

DIFF_FILE="${1:-}"
if [ -z "$DIFF_FILE" ]; then
    echo "Usage: bash scripts/parse_diff.sh <diff-file>" >&2
    exit 1
fi

if [ ! -f "$DIFF_FILE" ]; then
    echo "Error: diff file not found: $DIFF_FILE" >&2
    exit 1
fi

echo "=== Changed files ==="
grep -E '^diff --git' "$DIFF_FILE" | sed 's|diff --git a/||; s| b/.*||' | sort -u

echo ""
echo "=== Added lines per file ==="
awk '
/^diff --git/ { file=$0; sub(/^diff --git a\//, "", file); sub(/ b\/.*/, "", file) }
/^\+/ && !/^\+\+\+/ { count[file]++ }
END { for (f in count) printf "  %s: +%d lines\n", f, count[f] }
' "$DIFF_FILE"

echo ""
echo "=== Removed lines per file ==="
awk '
/^diff --git/ { file=$0; sub(/^diff --git a\//, "", file); sub(/ b\/.*/, "", file) }
/^-/ && !/^---/ { count[file]++ }
END { for (f in count) printf "  %s: -%d lines\n", f, count[f] }
' "$DIFF_FILE"
