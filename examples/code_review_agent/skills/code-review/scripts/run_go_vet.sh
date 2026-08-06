#!/usr/bin/env bash
# Optional helper: run go vet when a Go module is present in REVIEW_REPO_PATH.
set -euo pipefail
ROOT="${REVIEW_REPO_PATH:-}"
if [[ -z "${ROOT}" || ! -d "${ROOT}" ]]; then
  echo '[]'
  exit 0
fi
if [[ ! -f "${ROOT}/go.mod" ]]; then
  echo '[]'
  exit 0
fi
(cd "${ROOT}" && go vet ./...) || true
echo '[]'
