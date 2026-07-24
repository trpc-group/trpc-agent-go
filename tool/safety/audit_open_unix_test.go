//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package safety

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAuditWriter_RejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.fifo")
	require.NoError(t, syscall.Mkfifo(path, 0o600))

	start := time.Now()
	_, err := NewAuditWriter(path, true, true)
	require.Error(t, err)
	require.Less(t, time.Since(start), time.Second)
}
