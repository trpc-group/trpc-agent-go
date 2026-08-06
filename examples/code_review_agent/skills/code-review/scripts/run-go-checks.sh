#!/bin/sh

# Run the baseline deterministic checks for one Go module during code review.
# Both phases are attempted so each can contribute evidence; the script returns
# unsuccessfully when either phase fails.

if [ "$#" -ne 1 ]; then
  echo "usage: run-go-checks.sh <module>" >&2
  exit 2
fi

if ! command -v go >/dev/null 2>&1; then
  echo "go is not installed" >&2
  exit 127
fi

cd "$1" || exit 1

if [ ! -f go.mod ]; then
  echo "not a Go module: go.mod not found in $1" >&2
  exit 2
fi

status=0

echo "==> go test ./..."
if ! go test ./... 2>&1; then
  status=1
fi

echo "==> go vet ./..."
if ! go vet ./... 2>&1; then
  status=1
fi

exit "$status"
