#!/bin/sh
set -e

# Run `go test` against a Go package and capture raw output to out/test.txt.
#
# Usage:
#   sh scripts/run_go_test.sh [package]
#
# The package argument defaults to ./... (all packages under the repo root).
# -count=1 disables the Go test cache so results always reflect the current
# source. Test failures are propagated as a non-zero exit code so the review
# pipeline can surface them, but the raw output is preserved in out/test.txt
# either way.

mkdir -p out
PKG="${1:-./...}"

set +e
go test -count=1 "$PKG" > out/test.txt 2>&1
status=$?
set -e

if [ "$status" -ne 0 ]; then
    echo "go test exited with code $status (see out/test.txt for details)" >&2
fi

exit "$status"
