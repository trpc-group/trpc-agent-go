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
func rewriteToEcho(t *testing.T, marker string) PreStartHook {
	t.Helper()

	sh, err := exec.LookPath("sh")
	require.NoError(t, err)
	return func(_ context.Context, cmd *exec.Cmd) error {
		cmd.Path = sh
		cmd.Args = []string{"sh", "-c", "echo " + marker}
		return nil
	}
}

// attrsSeen copies the attributes the hook observed, so a test asserts what
// the hook saw rather than what the tool set restored afterwards.
func attrsSeen(cmd *exec.Cmd) *syscall.SysProcAttr {
	if cmd.SysProcAttr == nil {
		return nil
	}
	seen := *cmd.SysProcAttr
	return &seen
}

func TestPreStartHook_RewritesForegroundCommand(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	var seen *syscall.SysProcAttr
	rewrite := rewriteToEcho(t, "hooked-foreground")
	set, err := NewToolSet(WithPreStartHook(
		func(ctx context.Context, cmd *exec.Cmd) error {
			seen = attrsSeen(cmd)
			return rewrite(ctx, cmd)
		},
	))
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

func TestPreStartHook_RewritesBackgroundCommand(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	var seen *syscall.SysProcAttr
	rewrite := rewriteToEcho(t, "hooked-background")
	set, err := NewToolSet(WithPreStartHook(
		func(ctx context.Context, cmd *exec.Cmd) error {
			seen = attrsSeen(cmd)
			return rewrite(ctx, cmd)
		},
	))
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

// The hook sees the attributes a PTY child starts with — a new session with
// the PTY as controlling terminal — not the bare command pty.Start would
// otherwise fill in later, so sandbox setup can recognize PTY mode.
func TestPreStartHook_RewritesPTYCommand(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	var seen *syscall.SysProcAttr
	rewrite := rewriteToEcho(t, "hooked-pty")
	set, err := NewToolSet(WithPreStartHook(
		func(ctx context.Context, cmd *exec.Cmd) error {
			seen = attrsSeen(cmd)
			return rewrite(ctx, cmd)
		},
	))
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
	if err != nil && seen == nil {
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
	require.NotNil(t, seen)
	require.True(t, seen.Setsid)
	require.True(t, seen.Setctty)
	require.False(t, seen.Setpgid)
}

type hookContextKey struct{}

// The hook receives the tool call's context on every spawn path.
func TestPreStartHook_ReceivesCallContext(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	var seen []any
	set, err := NewToolSet(WithPreStartHook(
		func(ctx context.Context, _ *exec.Cmd) error {
			seen = append(seen, ctx.Value(hookContextKey{}))
			return nil
		},
	))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, mgr := toolSetTools(t, set)
	for i, args := range []map[string]any{
		{"command": "echo ctx", "yieldMs": 0},
		{"command": "echo ctx", "background": true},
		{"command": "echo ctx", "tty": true, "yieldMs": 0},
	} {
		ctx := context.WithValue(context.Background(), hookContextKey{}, i)
		out, err := execTool.Call(ctx, mustJSON(t, args))
		if err != nil && args["tty"] == true && len(seen) == i {
			t.Skip(err.Error())
		}
		require.NoError(t, err)
		if id, _ := out.(map[string]any)["session_id"].(string); id != "" {
			pollUntilExited(t, mgr, id)
		}
	}
	require.Equal(t, []any{0, 1, 2}, seen)
}

func TestPreStartHook_ErrorAbortsWithoutSession(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	hookErr := errors.New("hook refused")
	set, err := NewToolSet(WithPreStartHook(
		func(context.Context, *exec.Cmd) error {
			return hookErr
		},
	))
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

func TestPreStartHook_NilIsNoop(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet(WithPreStartHook(nil))
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

// A hook that replaces SysProcAttr, or enables the attribute the other pipe
// mode uses, must not stop the command from starting: Setsid and Setpgid
// together make exec fail with EPERM, and dropping both would detach the
// child from the process group the manager signals. The tool set restores the
// exact state of its mode after the hook in both modes.
func TestPreStartHook_RestoresPipeProcessAttributes(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	for _, tc := range []struct {
		name       string
		background bool
		mutate     func(*syscall.SysProcAttr) *syscall.SysProcAttr
	}{
		{"foreground-adds-setpgid", false,
			func(a *syscall.SysProcAttr) *syscall.SysProcAttr {
				a.Setpgid = true
				return a
			}},
		{"foreground-replaces-with-setpgid", false,
			func(*syscall.SysProcAttr) *syscall.SysProcAttr {
				return &syscall.SysProcAttr{Setpgid: true}
			}},
		{"background-adds-setsid", true,
			func(a *syscall.SysProcAttr) *syscall.SysProcAttr {
				a.Setsid = true
				return a
			}},
		{"background-replaces-with-setsid", true,
			func(*syscall.SysProcAttr) *syscall.SysProcAttr {
				return &syscall.SysProcAttr{Setsid: true}
			}},
		{"background-replaces-with-empty", true,
			func(*syscall.SysProcAttr) *syscall.SysProcAttr {
				return &syscall.SysProcAttr{}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var started *exec.Cmd
			set, err := NewToolSet(WithPreStartHook(
				func(_ context.Context, cmd *exec.Cmd) error {
					started = cmd
					cmd.SysProcAttr = tc.mutate(cmd.SysProcAttr)
					return nil
				},
			))
			require.NoError(t, err)
			defer set.Close()

			execTool, _, _, mgr := toolSetTools(t, set)
			if !tc.background {
				out, err := execTool.Call(
					context.Background(),
					mustJSON(t, map[string]any{
						"command": "echo restored",
						"yieldMs": 0,
					}),
				)
				require.NoError(t, err)
				res := out.(map[string]any)
				require.Equal(t, programStatusExited, res["status"])
				require.Equal(t, "restored", outputField(res))
				require.NotNil(t, started)
				require.True(t, started.SysProcAttr.Setsid)
				require.False(t, started.SysProcAttr.Setpgid)
				return
			}

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
			require.Same(t, started, sess.cmd)
			require.True(t, sess.cmd.SysProcAttr.Setpgid)
			require.False(t, sess.cmd.SysProcAttr.Setsid)
			require.Equal(t, sess.cmd.Process.Pid, sess.processGroupID)
			require.NoError(t, mgr.kill(sessionID))
			pollUntilExited(t, mgr, sessionID)
			require.False(t,
				processTreeAlive(sess.cmd.Process, sess.processGroupID))
		})
	}
}

// The PTY attributes are re-established after the hook too, so a hook that
// replaces SysProcAttr still gets a child in its own session on the terminal.
func TestPreStartHook_RestoresPTYProcessAttributes(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	var started *exec.Cmd
	set, err := NewToolSet(WithPreStartHook(
		func(_ context.Context, cmd *exec.Cmd) error {
			started = cmd
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			return nil
		},
	))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, mgr := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command": "echo restored",
			"tty":     true,
			"yieldMs": 0,
		}),
	)
	if err != nil && started == nil {
		t.Skip(err.Error())
	}
	require.NoError(t, err)

	res := out.(map[string]any)
	all := outputField(res)
	if sessionID, _ := res["session_id"].(string); sessionID != "" {
		all += pollUntilExited(t, mgr, sessionID)
	}
	require.Contains(t, all, "restored")
	require.NotNil(t, started)
	require.True(t, started.SysProcAttr.Setsid)
	require.True(t, started.SysProcAttr.Setctty)
	require.False(t, started.SysProcAttr.Setpgid)
}

func TestPreStartHook_ErrorOpensNoPipes(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet(WithPreStartHook(
		func(context.Context, *exec.Cmd) error {
			return errors.New("hook refused")
		},
	))
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
