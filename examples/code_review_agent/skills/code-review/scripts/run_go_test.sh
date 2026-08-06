#!/usr/bin/env bash
# Optional helper for go test (ask/permission gated by host for ./...).
set -euo pipefail
ROOT="${REVIEW_REPO_PATH:-}"
PKG="${REVIEW_GO_PACKAGE:-./...}"
if [[ -z "${ROOT}" || ! -d "${ROOT}" ]]; then
  echo '[]'
  exit 0
fi
(cd "${ROOT}" && go test "${PKG}" -count=1 -timeout=30s) || true
echo '[]'
