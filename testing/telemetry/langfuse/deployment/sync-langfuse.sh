#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LANGFUSE_DIR="$SCRIPT_DIR/langfuse"
LANGFUSE_REPO_URL="${LANGFUSE_REPO_URL:-https://github.com/langfuse/langfuse.git}"

if [[ -d "$LANGFUSE_DIR/.git" ]]; then
  git -C "$LANGFUSE_DIR" pull --ff-only
  exit 0
fi

git clone "$LANGFUSE_REPO_URL" "$LANGFUSE_DIR"
