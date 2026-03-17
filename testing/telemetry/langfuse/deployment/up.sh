#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LANGFUSE_DIR="$SCRIPT_DIR/langfuse"

if [[ ! -d "$LANGFUSE_DIR/.git" ]]; then
  echo "Local Langfuse checkout not found. Run sync-langfuse.sh first." >&2
  exit 1
fi

echo "Reminder: update secrets marked # CHANGEME in $LANGFUSE_DIR/docker-compose.yml before the first start." >&2

cd "$LANGFUSE_DIR"
exec "$SCRIPT_DIR/docker-compose" up "$@"
