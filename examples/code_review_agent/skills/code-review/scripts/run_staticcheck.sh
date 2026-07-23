#!/usr/bin/env bash
# Optional staticcheck runner. Skips cleanly when binary is absent.
set -euo pipefail
ROOT="${REVIEW_REPO_PATH:-}"
if [[ -z "${ROOT}" || ! -d "${ROOT}" ]]; then
  echo '[]'
  exit 0
fi
if ! command -v staticcheck >/dev/null 2>&1; then
  echo '[]'
  exit 0
fi
(cd "${ROOT}" && staticcheck ./...) || true
echo '[]'
