#!/usr/bin/env bash
set -euo pipefail

# go_static_checks.sh runs Go static checks against a target module directory.
# Usage: go_static_checks.sh [target-dir]   (defaults to the current directory)
target="${1:-.}"
if [[ ! -d "${target}" ]]; then
  echo "target directory not found: ${target}" >&2
  exit 2
fi
cd "${target}"

# Default Go caches into the sandbox tmp dir when the caller does not
# provide them (policy mode scrubs HOME, so go needs explicit caches).
: "${GOCACHE:=${TMPDIR:-/tmp}/gocache}"
: "${GOPATH:=${TMPDIR:-/tmp}/gopath}"
export GOCACHE GOPATH

go test ./...
go vet ./...
