//go:build windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hostexec

import (
	"testing"
	"time"
)

func processExists(_ int) bool {
	return false
}

func waitForProcessExit(
	t *testing.T,
	_ int,
	_ time.Duration,
) {
	t.Helper()
}
