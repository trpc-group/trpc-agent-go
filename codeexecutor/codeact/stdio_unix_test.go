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

	time.Sleep(50 * time.Millisecond)
	require.NoError(t, os.WriteFile(gate, []byte("go"), 0o600))
	time.Sleep(200 * time.Millisecond)
	_, err = os.Stat(marker)
	require.ErrorIs(t, err, os.ErrNotExist)
}
