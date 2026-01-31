#!/usr/bin/env bash
set -euo pipefail

# This script compares module zip hashes computed from git archive and from the working tree.

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
cd "${repo_root}"

tmp_dir="$(mktemp -d)"
trap 'chmod -R u+w "${tmp_dir}" >/dev/null 2>&1 || true; rm -rf "${tmp_dir}"' EXIT

tool_mod="${tmp_dir}/sumcheck.mod"
cat >"${tool_mod}" <<'EOF'
module example.com/trpc-agent-go-sumcheck

go 1.21

require golang.org/x/mod v0.20.0
EOF

export GOMODCACHE="${tmp_dir}/gomodcache"
export GOFLAGS="-modcacherw"

go run -mod=mod -modfile="${tool_mod}" .github/scripts/check-current-module-sums.go
