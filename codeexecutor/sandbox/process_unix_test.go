//go:build !windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProcessWaitCleansDescendantsAfterRootExits(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
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
	parentCode := fmt.Sprintf(
		"import subprocess,sys\nsubprocess.Popen([sys.executable, '-c', %q], "+
			"stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, "+
			"stderr=subprocess.DEVNULL)\n",
		childCode,
	)
	rt, ws := newProcessTestRuntime(t)
	process, err := rt.StartProcess(context.Background(), ws, ProcessSpec{
		Cmd:      python,
		Args:     []string{"-c", parentCode},
		CleanEnv: true,
	})
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, process.Stdout())
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, process.Stderr())
	require.NoError(t, err)
	require.NoError(t, process.Wait())

	require.NoError(t, os.WriteFile(gate, []byte("go"), 0o600))
	require.Never(t, func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}, time.Second, 10*time.Millisecond)
	_, err = os.Stat(marker)
	require.ErrorIs(t, err, os.ErrNotExist)
}
