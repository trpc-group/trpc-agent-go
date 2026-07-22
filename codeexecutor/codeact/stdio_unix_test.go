//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeact

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLocalRunnerCleansDescendantsAfterSuccessfulCompletion(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	dir := t.TempDir()
	gate := filepath.Join(dir, "release-descendant")
	marker := filepath.Join(dir, "descendant-survived")
	childCode := fmt.Sprintf(
		"import pathlib,time; gate=pathlib.Path(%q); marker=pathlib.Path(%q); "+
			"\nwhile not gate.exists(): time.sleep(0.01)"+
			"\nmarker.write_text('alive')",
		gate,
		marker,
	)
	code := fmt.Sprintf(
		"import subprocess,sys\nsubprocess.Popen([sys.executable, '-c', %q], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)\nreturn 'done'",
		childCode,
	)
	result, err := Execute(
		context.Background(),
		LocalRunner{},
		fakeToolCallHandler{},
		code,
	)
	require.NoError(t, err)
	require.JSONEq(t, `"done"`, string(result.Value))

	require.NoError(t, os.WriteFile(gate, []byte("go"), 0o600))
	require.Never(t, func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}, time.Second, 10*time.Millisecond)
	_, err = os.Stat(marker)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestConfiguredLocalRunnerTimeoutKillsMovedProcessLeader(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	started := time.Now()
	_, err := Execute(
		context.Background(),
		NewLocalRunner(LocalRunnerConfig{Timeout: 100 * time.Millisecond}),
		toolCallHandlerFunc(func(ctx context.Context, _ ToolCall) (json.RawMessage, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
		`import os
import signal
import subprocess
import sys

signal.alarm(3)
subprocess.Popen(
    [sys.executable, "-c", "import time; time.sleep(3)"],
    stdin=subprocess.DEVNULL,
    stdout=subprocess.DEVNULL,
    stderr=subprocess.DEVNULL,
)
os.setpgid(0, os.getpgid(os.getppid()))
return await call_tool("wait")`,
	)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), 2*time.Second)
}
