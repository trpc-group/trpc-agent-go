#!/bin/bash
set -euo pipefail

DIFF_FILE="${1:-}"
if [ -z "$DIFF_FILE" ]; then
    echo "Usage: $0 <diff_file>"
    exit 1
fi

if [ ! -f "$DIFF_FILE" ]; then
    echo "Error: File not found: $DIFF_FILE"
    exit 1
fi

awk '
    /^diff --git/ {
        split($0, parts, " ")
        from_file = parts[3]
        sub(/^a\//, "", from_file)
        print from_file
    }
' "$DIFF_FILE"