//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package opensandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// TestPutDirectory_RootSwappedForFIFO is the swapped-root FIFO
// regression: after PutDirectory's os.Stat accepts the source
// directory, a mutable staging tree can replace the root with a FIFO
// before the walk opens it. The root open must fail promptly
// (O_DIRECTORY|O_NONBLOCK reject with ENOTDIR) instead of blocking
// until a writer appears, and nothing may be uploaded.
func TestPutDirectory_RootSwappedForFIFO(t *testing.T) {
	m := newMockServer(t)
	defer m.close()
	exec := newTestExecutor(t, m)
	defer exec.Close()

	ws, err := exec.CreateWorkspace(
		context.Background(), "ws-fifo-root", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)

	host := filepath.Join(t.TempDir(), "staging-root")
	require.NoError(t, os.MkdirAll(host, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(host, "file.txt"), []byte("data"), 0o644,
	))
	t.Cleanup(func() { _ = os.Remove(host) })

	// Swap the checked root for a FIFO during the destination readlink
	// probe — the remote round-trip that sits between PutDirectory's
	// os.Stat and walkAndUpload's root open. The hook disables itself
	// so later commands run unaffected.
	m.mu.Lock()
	m.commandHook = func(command string) {
		if !strings.Contains(command, "readlink") {
			return
		}
		m.mu.Lock()
		m.commandHook = nil
		m.mu.Unlock()
		if err := os.RemoveAll(host); err != nil {
			return
		}
		_ = syscall.Mkfifo(host, 0o600)
	}
	m.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- exec.rt.PutDirectory(
			context.Background(), ws, host, "staged",
		)
	}()
	select {
	case err := <-done:
		require.Error(t, err,
			"a root swapped for a FIFO must be rejected promptly")
		assert.Contains(t, err.Error(), "walk and upload")
	case <-time.After(30 * time.Second):
		t.Fatal("PutDirectory hangs on a FIFO staging root: " +
			"the root open is blocking instead of failing promptly")
	}

	m.mu.Lock()
	uploaded := len(m.files)
	m.mu.Unlock()
	assert.Zero(t, uploaded,
		"nothing may be uploaded when the staging root is a FIFO")
}
