#!/usr/bin/env bash
# Tencent is pleased to support the open source community by making
# trpc-agent-go available.
#
# Copyright (C) 2025 Tencent.  All rights reserved.
#
# trpc-agent-go is licensed under the Apache License Version 2.0.

# run_go_vet.sh runs "go vet" over a package pattern and reports failures.
#
# Usage:
#   bash scripts/run_go_vet.sh [package-pattern]
#
# Arguments:
#   package-pattern  Go package pattern passed to "go vet". Defaults to "./...".
#
# Exit status:
#   0  go vet reported no issues.
#   1  go vet reported issues or failed to run.
#
# Environment:
#   GOFLAGS  forwarded to the go toolchain if set.

set -euo pipefail

pkg="${1:-./...}"

echo "Running go vet on ${pkg}"

if ! go vet "${pkg}"; then
  echo "go vet found issues in ${pkg}" >&2
  exit 1
fi

echo "go vet passed for ${pkg}"
