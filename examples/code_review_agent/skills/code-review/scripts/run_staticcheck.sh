#!/bin/sh
set -e

# Run `staticcheck` against a Go package and capture raw output to
# out/staticcheck.txt.
#
# Usage:
#   sh scripts/run_staticcheck.sh [package]
#
# The package argument defaults to ./... (all packages under the repo root).
# If the staticcheck binary is not installed, the script writes a skip notice
# to out/staticcheck.txt and exits 0 so the review pipeline can continue with
# the other checks. Like go vet, staticcheck reports findings via a non-zero
# exit; those findings are treated as normal review output (exit 0), and only
# a genuine tool failure (any exit code other than 0 or 1) is propagated.

mkdir -p out
PKG="${1:-./...}"

if ! command -v staticcheck > /dev/null 2>&1; then
    echo "staticcheck not installed, skipping" > out/staticcheck.txt
    exit 0
fi

set +e
staticcheck "$PKG" > out/staticcheck.txt 2>&1
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
        echo "staticcheck failed with exit code $status" >&2
        cat out/staticcheck.txt >&2
        exit "$status"
        ;;
esac

exit 0
