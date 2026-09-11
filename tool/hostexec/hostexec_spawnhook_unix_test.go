//go:build !windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hostexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// rewriteToEcho replaces whatever command the tool set built with a shell
// that prints a marker, so a test can tell the hook's rewrite was what ran.
func rewriteToEcho(t *testing.T, marker string) func(*exec.Cmd) error {
	t.Helper()

	sh, err := exec.LookPath("sh")
	require.NoError(t, err)
	return func(cmd *exec.Cmd) error {
		cmd.Path = sh
		cmd.Args = []string{"sh", "-c", "echo " + marker}
		return nil
	}
}

func TestSpawnHook_RewritesForegroundCommand(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	var seen *syscall.SysProcAttr
	rewrite := rewriteToEcho(t, "hooked-foreground")
	set, err := NewToolSet(WithSpawnHook(func(cmd *exec.Cmd) error {
		seen = cmd.SysProcAttr
		return rewrite(cmd)
	}))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, _ := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command": "echo original",
			"yieldMs": 0,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	require.Equal(t, programStatusExited, res["status"])
	require.Equal(t, "hooked-foreground", outputField(res))
	require.EqualValues(t, 0, res["exit_code"])
	require.NotNil(t, seen)
	require.True(t, seen.Setsid)
	require.False(t, seen.Setpgid)
}

func TestSpawnHook_RewritesBackgroundCommand(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	var seen *syscall.SysProcAttr
	rewrite := rewriteToEcho(t, "hooked-background")
	set, err := NewToolSet(WithSpawnHook(func(cmd *exec.Cmd) error {
		seen = cmd.SysProcAttr
		return rewrite(cmd)
	}))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, mgr := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "echo original",
			"background": true,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	sessionID := res["session_id"].(string)
	require.NotEmpty(t, sessionID)
	all := outputField(res) + pollUntilExited(t, mgr, sessionID)
	require.Contains(t, all, "hooked-background")
	require.NotContains(t, all, "original")
	require.NotNil(t, seen)
	require.True(t, seen.Setpgid)
	require.False(t, seen.Setsid)
}

func TestSpawnHook_RewritesPTYCommand(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	called := false
	rewrite := rewriteToEcho(t, "hooked-pty")
	set, err := NewToolSet(WithSpawnHook(func(cmd *exec.Cmd) error {
		called = true
		return rewrite(cmd)
	}))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, mgr := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command": "echo original",
			"tty":     true,
			"yieldMs": 0,
		}),
	)
	if err != nil && !called {
		t.Skip(err.Error())
	}
	require.NoError(t, err)

	res := out.(map[string]any)
	all := outputField(res)
	if sessionID, _ := res["session_id"].(string); sessionID != "" {
		all += pollUntilExited(t, mgr, sessionID)
	}
	require.Contains(t, all, "hooked-pty")
	require.NotContains(t, all, "original")
}

func TestSpawnHook_ErrorAbortsWithoutSession(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	hookErr := errors.New("hook refused")
	set, err := NewToolSet(WithSpawnHook(func(*exec.Cmd) error {
		return hookErr
	}))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, mgr := toolSetTools(t, set)
	for _, args := range []map[string]any{
		{"command": "echo never", "yieldMs": 0},
		{"command": "echo never", "background": true},
		{"command": "echo never", "tty": true},
	} {
		_, err := execTool.Call(context.Background(), mustJSON(t, args))
		require.ErrorIs(t, err, hookErr)
	}

	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	require.Empty(t, mgr.sessions)
}

func TestSpawnHook_NilIsNoop(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet(WithSpawnHook(nil))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, _ := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command": "echo original",
			"yieldMs": 0,
		}),
	)
	require.NoError(t, err)
	require.Equal(t, "original", outputField(out.(map[string]any)))
}

func TestSpawnHook_ReplacedSysProcAttrKeepsProcessGroup(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet(WithSpawnHook(func(cmd *exec.Cmd) error {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
		return nil
	}))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, mgr := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "sleep 30",
			"background": true,
		}),
	)
	require.NoError(t, err)

	sessionID := out.(map[string]any)["session_id"].(string)
	mgr.mu.Lock()
	sess := mgr.sessions[sessionID]
	mgr.mu.Unlock()
	require.NotNil(t, sess)
	require.True(t, sess.cmd.SysProcAttr.Setpgid)
	require.Equal(t, sess.cmd.Process.Pid, sess.processGroupID)
	require.NoError(t, mgr.kill(sessionID))
	pollUntilExited(t, mgr, sessionID)
	require.False(t, processTreeAlive(sess.cmd.Process, sess.processGroupID))
}

func TestSpawnHook_ErrorOpensNoPipes(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet(WithSpawnHook(func(*exec.Cmd) error {
		return errors.New("hook refused")
	}))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, _ := toolSetTools(t, set)
	before := openFDCount(t)
	for i := 0; i < 32; i++ {
		_, err := execTool.Call(
			context.Background(),
			mustJSON(t, map[string]any{"command": "echo never", "yieldMs": 0}),
		)
		require.Error(t, err)
	}
	require.LessOrEqual(t, openFDCount(t), before+2)
}

func openFDCount(t *testing.T) int {
	t.Helper()

	dir, err := os.Open("/dev/fd")
	require.NoError(t, err)
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	require.NoError(t, err)
	return len(names)
}
