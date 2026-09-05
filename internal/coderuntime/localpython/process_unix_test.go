//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package localpython

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfigureProcessUsesProcessGroup(t *testing.T) {
	cmd := &exec.Cmd{}
	configureProcess(cmd)
	require.NotNil(t, cmd.SysProcAttr)
	require.True(t, cmd.SysProcAttr.Setpgid)
}

func TestKillProcessGroupNoProcess(t *testing.T) {
	require.NoError(t, killProcessGroup(nil))
	require.NoError(t, killProcessGroup(&exec.Cmd{}))
	cleanupProcessTree(nil)
}

func TestCommandContextKillsLeaderAfterItChangesProcessGroup(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-c", `
import os
import subprocess
import sys

subprocess.Popen(
    [sys.executable, "-c", "import time; time.sleep(30)"],
    stdin=subprocess.DEVNULL,
    stdout=subprocess.DEVNULL,
    stderr=subprocess.DEVNULL,
)
os.setpgid(0, os.getpgid(os.getppid()))
print("ready", flush=True)
while True:
    pass
`)
	configureProcess(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "ready", strings.TrimSpace(line))
	cancel()

	select {
	case err := <-waitCh:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		<-waitCh
		t.Fatal("command did not exit after context cancellation")
	}
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestProcessWaitCleansDescendantsAfterRootExits(t *testing.T) {
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
	script := fmt.Sprintf(
		"import subprocess,sys\nsubprocess.Popen([sys.executable, '-c', %q], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)\n",
		childCode,
	)
	proc, err := StartScript(
		context.Background(),
		Config{},
		script,
		"guest.py",
		[]byte(script),
		nil,
		nil,
		io.Discard,
	)
	require.NoError(t, err)
	require.NoError(t, proc.Wait())
	require.NoError(t, os.WriteFile(gate, []byte("go"), 0o600))
	require.Never(t, func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}, time.Second, 10*time.Millisecond)
	_, err = os.Stat(marker)
	require.ErrorIs(t, err, os.ErrNotExist)
}
