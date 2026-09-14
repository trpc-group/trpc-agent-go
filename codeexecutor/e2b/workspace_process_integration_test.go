//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

//go:build integration

package e2b

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// TestIntegrationWorkspaceProcess tests the filesystem/process lifecycle on a
// fresh sandbox and a second executor connected to it. It creates a billable
// sandbox only when the integration tag and E2B_API_KEY are provided. E2B_API_URL and
// E2B_DOMAIN follow normal executor configuration; E2B_TEMPLATE and E2B_ENVD_USER
// can select a compatible template and process account. E2B_ENVD_USER must
// match the template's Code Interpreter kernel account. Filesystem operations
// use /execute, so this catches identity mismatches with native processes.
func TestIntegrationWorkspaceProcess(t *testing.T) {
	if os.Getenv("E2B_API_KEY") == "" {
		t.Skip("set E2B_API_KEY to run the workspace process integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	opts := []Option{WithSandboxTimeout(3 * time.Minute)}
	if template := os.Getenv("E2B_TEMPLATE"); template != "" {
		opts = append(opts, WithTemplate(template))
	}
	if user := os.Getenv("E2B_ENVD_USER"); user != "" {
		opts = append(opts, WithHeaders(map[string]string{
			"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":")),
		}))
	}
	owner, err := NewWithContext(ctx, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, owner.Close()) })
	connected, err := NewWithContext(ctx, append(opts, WithSandboxID(owner.SandboxID()))...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connected.Close()) })

	ws, err := owner.CreateWorkspace(ctx, fmt.Sprintf("integration-%d", time.Now().UnixNano()), codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	for _, tc := range []struct {
		name     string
		executor *CodeExecutor
	}{{"created", owner}, {"connected", connected}} {
		t.Run(tc.name, func(t *testing.T) {
			executor := tc.executor
			checkWorkspaceProcessFiles(t, ctx, executor, ws)
			result, err := executor.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
				Cmd: "/bin/sh", Args: []string{"-c", `printf 'before\n__E2B_STDOUT_END__\nafter\n\n'; printf 'error\n' >&2; exit 7`},
				Cwd: "out", CleanEnv: true,
			})
			require.NoError(t, err)
			require.Equal(t, "before\n__E2B_STDOUT_END__\nafter\n\n", result.Stdout)
			require.Equal(t, "error\n", result.Stderr)
			require.Equal(t, 7, result.ExitCode)
			t.Run("empty stdin", func(t *testing.T) {
				result, err := executor.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{Cmd: "cat", Timeout: 5 * time.Second})
				require.NoError(t, err)
				require.False(t, result.TimedOut)
				require.Zero(t, result.ExitCode)
			})
			t.Run("finite stdin", func(t *testing.T) {
				input := "input\x00\n"
				result, err := executor.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{Cmd: "cat", Stdin: input, Timeout: 5 * time.Second})
				if err != nil && strings.Contains(err.Error(), "finite stdin requires envd >= 0.5.2; configured version is") {
					t.Skipf("deployment capability: %v", err)
				}
				require.NoError(t, err)
				require.False(t, result.TimedOut)
				require.Zero(t, result.ExitCode)
				require.Equal(t, input, result.Stdout)
			})
		})
	}
}
