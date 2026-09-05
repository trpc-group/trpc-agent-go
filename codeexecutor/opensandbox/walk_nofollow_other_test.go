//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package opensandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestOpenDirNoFollow_FailsClosedWithoutOpenat(t *testing.T) {
	_, err := openDirNoFollow(t.TempDir())
	require.Error(t, err)
	assert.ErrorIs(t, err, errCannotPinWalk)
}

func TestOpenChildNoFollow_FailsClosedWithoutOpenat(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Open(dir)
	require.NoError(t, err)
	defer f.Close()

	_, _, err = openChildNoFollow(f, "child")
	require.Error(t, err)
	assert.ErrorIs(t, err, errCannotPinWalk)
	assert.False(t, isSkippableOpenErr(err),
		"the pin failure must fail staging, not be skipped as a race")
}

func TestPutDirectory_FailsClosedWithoutPinnedWalk(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(
		context.Background(), "ws-unpin", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)

	host := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(host, "file.txt"), []byte("data"), 0o644,
	))

	err = exec.rt.PutDirectory(context.Background(), ws, host, "staged")
	require.Error(t, err)
	assert.ErrorIs(t, err, errCannotPinWalk)

	m.mu.Lock()
	uploaded := len(m.files)
	m.mu.Unlock()
	assert.Zero(t, uploaded,
		"nothing may be uploaded when the staging tree cannot be pinned")
}
