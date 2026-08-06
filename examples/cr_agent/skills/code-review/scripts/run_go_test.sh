#!/usr/bin/env bash
# Tencent is pleased to support the open source community by making
# trpc-agent-go available.
#
# Copyright (C) 2025 Tencent.  All rights reserved.
#
# trpc-agent-go is licensed under the Apache License Version 2.0.

# run_go_test.sh runs "go test" over a package pattern and reports failures.
#
# Usage:
#   bash scripts/run_go_test.sh [package-pattern] [go-test-flags...]
#
# Arguments:
#   package-pattern  Go package pattern passed to "go test". Defaults to "./...".
#   go-test-flags    Extra flags forwarded to "go test" (e.g. -run TestFoo,
#                    -short, -race, -count=1).
#
# Exit status:
#   0  all tests passed.
#   1  one or more tests failed or the build failed.
#
# Environment:
#   GOFLAGS  forwarded to the go toolchain if set.

set -euo pipefail

pkg="${1:-./...}"
shift || true

echo "Running go test on ${pkg}"

if ! go test "${pkg}" "$@"; then
  echo "go test failed for ${pkg}" >&2
  exit 1
fi

echo "go test passed for ${pkg}"
