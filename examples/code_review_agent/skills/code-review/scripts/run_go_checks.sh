#!/usr/bin/env bash
set -euo pipefail

if [ -f go.mod ]; then
  go test ./...
  go vet ./...
else
  echo "no go.mod found; deterministic diff-only review completed"
fi
