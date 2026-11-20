//go:build integration

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeinterpreter

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess"
)

// TestIntegrationNativeFilesystem creates a billable sandbox only with the
// integration build tag and E2B_API_KEY. It never calls Code Interpreter.
func TestIntegrationNativeFilesystem(t *testing.T) {
	if os.Getenv("E2B_API_KEY") == "" {
		t.Skip("set E2B_API_KEY to run native filesystem integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	opts := &SandboxOpts{Template: os.Getenv("E2B_TEMPLATE"), Timeout: 180}
	if user := os.Getenv("E2B_ENVD_USER"); user != "" {
		opts.Headers = map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"))}
	}
	owner, err := Create(ctx, opts)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		require.NoError(t, owner.Kill(cleanupCtx))
	})
	connected, err := Connect(ctx, owner.SandboxID(), opts)
	require.NoError(t, err)
	root := fmt.Sprintf("/tmp/trpc-native-files-%d", time.Now().UnixNano())
	content := []byte("binary\x00中文\n__E2B_B64_END__\n")
	for _, tc := range []struct {
		name    string
		sandbox *Sandbox
	}{{"created", owner}, {"connected", connected}} {
		t.Run(tc.name, func(t *testing.T) {
			fs, err := NewFilesystemClient(ctx, tc.sandbox)
			require.NoError(t, err)
			dir := root + "/" + tc.name
			require.NoError(t, fs.MakeDir(ctx, dir))
			file := dir + "/input.bin"
			require.NoError(t, fs.Write(ctx, file, bytes.NewReader(content)))
			info, err := fs.Stat(ctx, file)
			require.NoError(t, err)
			require.EqualValues(t, len(content), info.Size)
			got, err := fs.Read(ctx, file, 7)
			require.NoError(t, err)
			require.Equal(t, content[:7], got.Content)
			require.EqualValues(t, len(content), got.Size)
			// The process account must own the HTTP-uploaded file, even with a custom user.
			result, err := RunProcess(ctx, tc.sandbox, envdprocess.Request{Cmd: "/bin/sh", Args: []string{"-c", `test "$(stat -c %U "$1")" = "$(id -un)" && chmod 600 "$1" && cat "$1"`, "trpc-files", file}, Timeout: 10 * time.Second})
			require.NoError(t, err)
			require.Zero(t, result.ExitCode, result.Stderr)
			require.Equal(t, content, []byte(result.Stdout))
			moved := dir + "/moved.bin"
			require.NoError(t, fs.Write(ctx, moved, bytes.NewReader([]byte("old"))))
			require.NoError(t, fs.Move(ctx, file, moved))
			got, err = fs.Read(ctx, moved, int64(len(content))+1)
			require.NoError(t, err)
			require.Equal(t, content, got.Content)
			require.NoError(t, fs.Remove(ctx, dir))
			require.NoError(t, fs.Remove(ctx, dir))
		})
	}
	fs, err := NewFilesystemClient(ctx, owner)
	require.NoError(t, err)
	require.NoError(t, fs.Remove(ctx, root))
}
