#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/common.sh"

REPO_ROOT="$(cd "$SCRIPT_DIR/../../../.." && pwd)"
EXAMPLE_DIR="${1:-$REPO_ROOT/examples/llmagent}"

langfuse_run_go_example "$SCRIPT_DIR" "$EXAMPLE_DIR" -model "deepseek-v3.1-terminus"
