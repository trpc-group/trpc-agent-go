#!/usr/bin/env bash
# Parses a unified diff and runs lightweight static checks.
set -uo pipefail

DIFF_FILE="${1:-work/inputs/changes.diff}"
MAX_LINES=5000

if [[ ! -f "$DIFF_FILE" ]]; then
  echo "error: diff file not found: $DIFF_FILE" >&2
  exit 2
fi

line_count=$(wc -l < "$DIFF_FILE" | tr -d ' ')
if [[ "$line_count" -gt "$MAX_LINES" ]]; then
  echo "error: diff exceeds line limit ($line_count > $MAX_LINES)" >&2
  exit 2
fi

if ! grep -q '^diff --git ' "$DIFF_FILE"; then
  echo "error: not a unified diff" >&2
  exit 2
fi

# Sandbox signal for ignored-error pattern (fixture 08).
if grep -E '^\+.*_\s*=\s*err' "$DIFF_FILE" >/dev/null; then
  echo "sandbox check: ignored error pattern in diff" >&2
  exit 2
fi

echo "sandbox checks passed ($line_count diff lines)"
exit 0
