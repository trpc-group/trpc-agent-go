//go:build !windows

// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.

package internal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSandboxTimeoutKillsChildProcessTree(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "spawn.sh")
	marker := filepath.Join(dir, "child-survived")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\n(sleep 0.4; echo survived > \"$1\") &\nsleep 5\n"), 0o700))
	sandbox := NewSandbox(SandboxConfig{Timeout: 100 * time.Millisecond, MaxOutputBytes: 1024, AllowedEnvVars: []string{"PATH"}})
	run := sandbox.Execute(context.Background(), "tree-timeout", "sh "+script+" "+marker, DecisionAllow, "")
	require.Equal(t, SandboxStatusTimeout, run.Status)
	time.Sleep(600 * time.Millisecond)
	_, err := os.Stat(marker)
	require.ErrorIs(t, err, os.ErrNotExist)
}
