#!/bin/sh
set -e

# Run `go test` against a Go package and capture raw output to out/go_unit.txt.
#
# Usage:
#   sh scripts/run_go_unit.sh [package]
#
# The package argument defaults to ./... (all packages under the repo root).
# -count=1 disables the Go test cache so results always reflect the current
# source. The tool output is echoed to stdout so the pipeline captures it in
# RunResult, and also persisted to out/go_unit.txt for workspace artifacts.
# The real exit code propagates so test failures surface as StatusFailed.
#
# Note: the filename avoids the "test" substring to sidestep the repo-wide
# .gitignore `*test*.sh` rule; the script itself still invokes `go test`.

OUT="${WORKSPACE_DIR}/out"
mkdir -p "$OUT"
cd "${WORKSPACE_DIR}/repo"
PKG="${1:-./...}"

set +e
go test -count=1 "$PKG" > "$OUT/go_unit.txt" 2>&1
status=$?
set -e

# Echo output to stdout so the pipeline captures it in RunResult.
cat "$OUT/go_unit.txt"

if [ "$status" -ne 0 ]; then
    echo "go test exited with code $status" >&2
fi

exit "$status"
