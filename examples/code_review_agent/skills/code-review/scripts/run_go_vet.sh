#!/bin/sh
set -e

# Run `go vet` against a Go package and capture raw output to out/vet.txt.
#
# Usage:
#   sh scripts/run_go_vet.sh [package]
#
# The package argument defaults to ./... (all packages under the repo root).
# go vet returns a non-zero exit whenever it reports findings, but findings
# are the expected output of a code review. This script therefore exits 0
# when go vet reports findings (exit code 1) and only propagates a non-zero
# exit when go vet itself fails to run (any exit code other than 0 or 1).

mkdir -p out
PKG="${1:-./...}"

set +e
go vet "$PKG" > out/vet.txt 2>&1
status=$?
set -e

case "$status" in
    0)
        # No findings.
        ;;
    1)
        # Findings reported -- normal review output, not a tool failure.
        ;;
    *)
        echo "go vet failed with exit code $status" >&2
        cat out/vet.txt >&2
        exit "$status"
        ;;
esac

exit 0
