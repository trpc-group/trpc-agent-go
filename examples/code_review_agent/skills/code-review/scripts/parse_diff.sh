#!/bin/sh
set -e

# Validate and stage a unified diff file for review.
#
# Usage:
#   sh scripts/parse_diff.sh <diff-file>
#
# Validates that the supplied diff file exists and is readable, then copies it
# into the skill workspace's out/input.diff so subsequent review steps operate
# on a stable, immutable copy. Exits 0 on success.

if [ "$#" -lt 1 ]; then
    echo "usage: sh scripts/parse_diff.sh <diff-file>" >&2
    exit 2
fi

DIFF_FILE="$1"

if [ ! -f "$DIFF_FILE" ]; then
    echo "parse_diff: diff file not found: $DIFF_FILE" >&2
    exit 2
fi

if [ ! -r "$DIFF_FILE" ]; then
    echo "parse_diff: diff file not readable: $DIFF_FILE" >&2
    exit 2
fi

OUT="${WORKSPACE_DIR}/out"
mkdir -p "$OUT"
# Prefix with ./ if the path starts with a dash so cp does not interpret
# it as an option (POSIX sh does not support -- before operands).
case "$DIFF_FILE" in
    -*) cp "./$DIFF_FILE" "$OUT/input.diff" ;;
    *)  cp "$DIFF_FILE" "$OUT/input.diff" ;;
esac

exit 0
