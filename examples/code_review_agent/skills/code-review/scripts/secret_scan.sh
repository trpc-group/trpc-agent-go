#!/usr/bin/env bash
set -euo pipefail

target="${1:-.}"
grep -RInE '(api[_-]?key|token|password|secret)\s*[:=]' "${target}" || true
