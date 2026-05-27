//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package local

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestInteractiveSession_PollLogAndTrim(t *testing.T) {
	sess := newInteractiveSession("s1", "echo hi", 2)
	sess.appendOutput("one\ntwo\nthree", "stdout")
	sess.appendOutput("\nerr\n", "stderr")

	require.Equal(t, "one\ntwo\nthree", sess.stdout.String())
	require.Equal(t, "\nerr\n", sess.stderr.String())

	poll := sess.Poll(nil)
	require.Equal(t, codeexecutor.ProgramStatusRunning, poll.Status)
	require.Equal(t, 2, poll.Offset)
	require.Equal(t, 4, poll.NextOffset)
	require.Equal(t, "three\nerr", poll.Output)

	logged := sess.Log(nil, nil)
	require.Equal(t, "three\nerr", logged.Output)

	sess.markDone(3, time.Second, true)
	done := sess.Poll(nil)
	require.Equal(t, codeexecutor.ProgramStatusExited, done.Status)
	require.NotNil(t, done.ExitCode)
	require.Equal(t, 3, *done.ExitCode)

	result := sess.RunResult()
	require.Equal(t, 3, result.ExitCode)
	require.True(t, result.TimedOut)
}

func TestInteractiveSession_WriteKillAndClose(t *testing.T) {
	sess := newInteractiveSession("s2", "cat", 5)

	err := sess.Write("hello", false)
	require.EqualError(t, err, "stdin is not available")

	sess.markDone(0, 0, false)
	err = sess.Write("hello", false)
	require.EqualError(t, err, "session is not running")

	canceled := false
	sess.cancel = func() { canceled = true }
	require.NoError(t, sess.Kill(10*time.Millisecond))
	require.True(t, canceled)

	closeCount := 0
	sess.closeIO = func() error {
		closeCount++
		return nil
	}
	require.NoError(t, sess.Close())
	require.NoError(t, sess.Close())
	require.Equal(t, 1, closeCount)
}

func TestRuntime_StartProgramInteractivePipes(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	ws := codeexecutor.Workspace{
		ID:   "ws1",
		Path: t.TempDir(),
	}
	_, err := codeexecutor.EnsureLayout(ws.Path)
	require.NoError(t, err)

	proc, err := rt.StartProgram(
		context.Background(),
		ws,
		codeexecutor.InteractiveProgramSpec{
			RunProgramSpec: codeexecutor.RunProgramSpec{
				Cmd: "sh",
				Args: []string{
					"-lc",
					"printf 'ready\\n'; read v; " +
						"echo out:$v; echo err:$v >&2",
				},
				Cwd:     "work",
				Timeout: 2 * time.Second,
			},
		},
	)
	require.NoError(t, err)

	waitInteractiveExit(t, proc, "ready")
	require.NoError(t, proc.Write("hello", true))

	waitInteractiveExit(t, proc, "out:hello")
	poll := waitInteractiveStatus(
		t,
		proc,
		codeexecutor.ProgramStatusExited,
	)
	require.Equal(t, codeexecutor.ProgramStatusExited, poll.Status)

	provider, ok := proc.(codeexecutor.ProgramResultProvider)
	require.True(t, ok)
	var result codeexecutor.RunResult
	require.Eventually(t, func() bool {
		result = provider.RunResult()
		return strings.Contains(result.Stdout, "out:hello") &&
			strings.Contains(result.Stderr, "err:hello")
	}, 5*time.Second, 50*time.Millisecond)
	require.NoError(t, proc.Close())
}

func TestRuntime_StartProgramInteractiveTTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tty coverage runs on unix-like systems")
	}

	rt := NewRuntime(t.TempDir())
	ws := codeexecutor.Workspace{
		ID:   "ws2",
		Path: t.TempDir(),
	}
	_, err := codeexecutor.EnsureLayout(ws.Path)
	require.NoError(t, err)

	proc, err := rt.StartProgram(
		context.Background(),
		ws,
		codeexecutor.InteractiveProgramSpec{
			RunProgramSpec: codeexecutor.RunProgramSpec{
				Cmd: "sh",
				Args: []string{
					"-lc",
					"printf 'choose: '; read v; echo tty:$v",
				},
				Timeout: 2 * time.Second,
			},
			TTY: true,
		},
	)
	require.NoError(t, err)

	waitInteractiveExit(t, proc, "choose:")
	require.NoError(t, proc.Write("7", true))

	waitInteractiveExit(t, proc, "tty:7")

	poll := waitInteractiveStatus(
		t,
		proc,
		codeexecutor.ProgramStatusExited,
	)
	require.Equal(t, codeexecutor.ProgramStatusExited, poll.Status)

	provider, ok := proc.(codeexecutor.ProgramResultProvider)
	require.True(t, ok)
	require.Eventually(t, func() bool {
		return strings.Contains(
			provider.RunResult().Stdout,
			"tty:7",
		)
	}, 5*time.Second, 50*time.Millisecond)
}

func TestInteractiveHelpers_FormatEnvAndExitCode(t *testing.T) {
	require.Equal(t, "cmd", formatInteractiveCommand("cmd", nil))
	require.Equal(
		t,
		"cmd a b",
		formatInteractiveCommand("cmd", []string{"a", "b"}),
	)

	require.Equal(t, 0, interactiveExitCode(nil))

	err := exec.Command("sh", "-c", "exit 9").Run() //nolint:gosec
	require.Equal(t, 9, interactiveExitCode(err))
	require.Equal(t, -1, interactiveExitCode(os.ErrInvalid))

	rt := NewRuntime(t.TempDir())
	ws := codeexecutor.Workspace{
		ID:   "ws3",
		Path: t.TempDir(),
	}
	_, err = codeexecutor.EnsureLayout(ws.Path)
	require.NoError(t, err)
	_, envErr := rt.buildProgramEnv(
		ws,
		codeexecutor.RunProgramSpec{
			Env: map[string]string{"CUSTOM_ENV": "1"},
		},
	)
	require.NoError(t, envErr)
}

func TestBuildProgramEnv_CleanEnvDropsHostEnv(t *testing.T) {
	t.Setenv("BASH_ENV", "/tmp/host-bashenv")
	t.Setenv("LD_PRELOAD", "/tmp/host-preload.so")

	rt := NewRuntime(t.TempDir())
	ws := codeexecutor.Workspace{
		ID:   "ws-clean-env",
		Path: t.TempDir(),
	}
	env, err := rt.buildProgramEnv(
		ws,
		codeexecutor.RunProgramSpec{
			CleanEnv: true,
			Env: map[string]string{
				"LANG": "en_US.UTF-8",
			},
		},
	)
	require.NoError(t, err)

	_, hasBashEnv := envValue(env, "BASH_ENV")
	require.False(t, hasBashEnv)
	_, hasPreload := envValue(env, "LD_PRELOAD")
	require.False(t, hasPreload)
	lang, hasLang := envValue(env, "LANG")
	require.True(t, hasLang)
	require.Equal(t, "en_US.UTF-8", lang)
	path, hasPath := envValue(env, envPathKey)
	require.True(t, hasPath)
	require.Equal(t, cleanEnvPath(), path)
	workspace, hasWorkspace := envValue(env, codeexecutor.WorkspaceEnvDirKey)
	require.True(t, hasWorkspace)
	require.Equal(t, ws.Path, workspace)
}

func TestBuildProgramEnv_DefaultInheritsHostEnv(t *testing.T) {
	t.Setenv("BASH_ENV", "/tmp/host-bashenv")

	rt := NewRuntime(t.TempDir())
	ws := codeexecutor.Workspace{
		ID:   "ws-default-env",
		Path: t.TempDir(),
	}
	env, err := rt.buildProgramEnv(ws, codeexecutor.RunProgramSpec{})
	require.NoError(t, err)

	got, ok := envValue(env, "BASH_ENV")
	require.True(t, ok)
	require.Equal(t, "/tmp/host-bashenv", got)
}

func TestBuildProgramEnv_CleanEnvPreservesSpecPATH(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	ws := codeexecutor.Workspace{
		ID:   "ws-spec-path",
		Path: t.TempDir(),
	}
	env, err := rt.buildProgramEnv(
		ws,
		codeexecutor.RunProgramSpec{
			CleanEnv: true,
			Env: map[string]string{
				envPathKey: "/custom/bin",
			},
		},
	)
	require.NoError(t, err)

	got, ok := envValue(env, envPathKey)
	require.True(t, ok)
	require.Equal(t, "/custom/bin", got)
	require.Equal(t, 1, countEnvKey(env, envPathKey))
}

func TestEnvValue_UsesLastAssignment(t *testing.T) {
	value, ok := envValue([]string{
		envPathKey + "=/first",
		"CUSTOM_ENV=1",
		envPathKey + "=/second",
	}, envPathKey)

	require.True(t, ok)
	require.Equal(t, "/second", value)
}

func TestEnvValue_WindowsPathCase(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows path environment keys are case-insensitive")
	}

	value, ok := envValue([]string{"Path=C:\\bin"}, envPathKey)

	require.True(t, ok)
	require.Equal(t, "C:\\bin", value)
}

func countEnvKey(env []string, key string) int {
	count := 0
	for _, item := range env {
		name, _, ok := strings.Cut(item, "=")
		if ok && envKeyEqual(name, key) {
			count++
		}
	}
	return count
}

func TestEnvKeyEqualForGOOS(t *testing.T) {
	require.True(t, envKeyEqualForGOOS("Path", envPathKey, "windows"))
	require.False(t, envKeyEqualForGOOS("Path", envPathKey, "linux"))
}

func TestNewLocalProgramCommand_NonBareCommand(t *testing.T) {
	cmd := newLocalProgramCommand(
		context.Background(),
		t.TempDir(),
		codeexecutor.RunProgramSpec{Cmd: "./tool"},
		nil,
	)

	require.Equal(t, "./tool", cmd.Path)
}

func TestNewLocalProgramCommand_PathMissDoesNotUseProcessPATH(t *testing.T) {
	cmd := newLocalProgramCommand(
		context.Background(),
		t.TempDir(),
		codeexecutor.RunProgramSpec{Cmd: "sh"},
		[]string{envPathKey + "=" + t.TempDir()},
	)

	require.Equal(t, "sh", cmd.Path)
	require.ErrorIs(t, cmd.Err, exec.ErrNotFound)
}

func TestLocalProgramCommandPath_NonBareCommand(t *testing.T) {
	got, ok := localProgramCommandPath(t.TempDir(), "./tool", nil)

	require.True(t, ok)
	require.Equal(t, "./tool", got)
}

func TestLocalProgramCommandPath_EmptyPATHEntryUsesCWD(
	t *testing.T,
) {
	cwd := t.TempDir()
	toolPath := filepath.Join(cwd, "tool")
	require.NoError(t, os.WriteFile(
		toolPath,
		[]byte("#!/bin/sh\n"),
		0o755,
	))

	got, ok := localProgramCommandPath(cwd, "tool", []string{
		envPathKey + "=" + string(os.PathListSeparator),
	})

	require.True(t, ok)
	require.Equal(t, toolPath, got)
}

func TestLocalProgramCandidateNamesForGOOS(t *testing.T) {
	require.Equal(
		t,
		[]string{"tool"},
		localProgramCandidateNamesForGOOS("tool", "", "linux", ":"),
	)
	require.Equal(
		t,
		[]string{"tool.sh"},
		localProgramCandidateNamesForGOOS("tool.sh", "", "windows", ";"),
	)
	require.Equal(
		t,
		[]string{"tool.cmd", "tool.exe"},
		localProgramCandidateNamesForGOOS(
			"tool",
			".cmd;exe",
			"windows",
			";",
		),
	)
	require.Equal(
		t,
		[]string{"tool.com", "tool.exe", "tool.bat", "tool.cmd"},
		localProgramCandidateNamesForGOOS("tool", "", "windows", ";"),
	)
	require.Equal(
		t,
		[]string{"tool.exe"},
		localProgramCandidateNamesForGOOS("tool", ".exe;;", "windows", ";"),
	)
}

func TestIsLocalExecutableFileForGOOS(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tool")
	require.NoError(t, os.WriteFile(file, []byte(""), 0o644))

	require.True(t, isLocalExecutableFileForGOOS(file, "windows"))
	require.False(t, isLocalExecutableFileForGOOS(file, "linux"))
	require.False(t, isLocalExecutableFileForGOOS(t.TempDir(), "windows"))
}

func TestLocalProgramCommandPath_WindowsPathExt(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PATHEXT lookup only applies on Windows")
	}
	cwd := t.TempDir()
	bin := filepath.Join(cwd, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(bin, "tool"),
		[]byte("@echo off"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(bin, "tool.cmd"),
		[]byte("@echo off"),
		0o644,
	))

	got, ok := localProgramCommandPath(cwd, "tool", []string{
		envPathKey + "=bin",
		envPathExtKey + "=.CMD;.EXE",
	})

	require.True(t, ok)
	require.Equal(t, filepath.Join(bin, "tool.cmd"), got)
}

func TestStartPipes_RejectsPresetFields(t *testing.T) {
	cmd := exec.Command("echo", "hi")
	cmd.Stdout = os.Stdout
	_, _, _, _, err := startPipes(cmd)
	require.ErrorContains(t, err, "Stdout already set")

	cmd2 := exec.Command("echo", "hi")
	cmd2.Stderr = os.Stderr
	_, _, _, _, err = startPipes(cmd2)
	require.ErrorContains(t, err, "Stderr already set")

	cmd3 := exec.Command("echo", "hi")
	cmd3.Stdin = strings.NewReader("x")
	_, _, _, _, err = startPipes(cmd3)
	require.Error(t, err)
}

func TestInteractiveSession_IDLogOffsetsAndMarkDone(t *testing.T) {
	sess := newInteractiveSession("sess-1", "echo hi", 2)
	require.Equal(t, "sess-1", sess.ID())

	sess.appendOutput("one\ntwo\nthree\nfour\n", "stdout")
	sess.appendOutput("tail", "stdout")

	logged := sess.Log(intPtr(0), intPtr(1))
	require.Equal(t, "three", logged.Output)
	require.Equal(t, 2, logged.Offset)
	require.Equal(t, 3, logged.NextOffset)

	logged = sess.Log(intPtr(10), nil)
	require.Equal(t, "tail", logged.Output)
	require.Equal(t, 4, logged.Offset)
	require.Equal(t, 4, logged.NextOffset)

	poll := sess.Poll(intPtr(1))
	require.Equal(t, "three", poll.Output)
	require.Equal(t, 2, poll.Offset)
	require.Equal(t, 3, poll.NextOffset)

	poll = sess.Poll(nil)
	require.Equal(t, "four\ntail", poll.Output)
	require.Equal(t, 3, poll.Offset)
	require.Equal(t, 4, poll.NextOffset)

	sess.markDone(7, time.Second, false)
	sess.markDone(9, 2*time.Second, true)

	result := sess.RunResult()
	require.Equal(t, 7, result.ExitCode)
	require.False(t, result.TimedOut)
}

func TestInteractiveSession_StateTransitions(t *testing.T) {
	sess := newInteractiveSession("state-1", "echo hi", 2)

	state := sess.State()
	require.Equal(t, codeexecutor.ProgramStatusRunning, state.Status)
	require.Nil(t, state.ExitCode)

	sess.markDone(9, time.Second, false)

	state = sess.State()
	require.Equal(t, codeexecutor.ProgramStatusExited, state.Status)
	require.NotNil(t, state.ExitCode)
	require.Equal(t, 9, *state.ExitCode)
}

func TestInteractiveSession_KillRunningProcess(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	ws := codeexecutor.Workspace{
		ID:   "ws-kill",
		Path: t.TempDir(),
	}
	_, err := codeexecutor.EnsureLayout(ws.Path)
	require.NoError(t, err)

	t.Run("exits on term", func(t *testing.T) {
		proc, err := rt.StartProgram(
			context.Background(),
			ws,
			codeexecutor.InteractiveProgramSpec{
				RunProgramSpec: codeexecutor.RunProgramSpec{
					Cmd:     "sh",
					Args:    []string{"-lc", "sleep 30"},
					Timeout: 5 * time.Second,
				},
			},
		)
		require.NoError(t, err)

		sess, ok := proc.(*interactiveSession)
		require.True(t, ok)
		require.NoError(t, sess.Kill(200*time.Millisecond))

		poll := waitInteractiveStatus(
			t,
			proc,
			codeexecutor.ProgramStatusExited,
		)
		require.Equal(t, codeexecutor.ProgramStatusExited, poll.Status)
		require.NoError(t, proc.Close())
	})

	t.Run("falls back to kill", func(t *testing.T) {
		proc, err := rt.StartProgram(
			context.Background(),
			ws,
			codeexecutor.InteractiveProgramSpec{
				RunProgramSpec: codeexecutor.RunProgramSpec{
					Cmd: "sh",
					Args: []string{
						"-lc",
						"trap '' TERM; sleep 30",
					},
					Timeout: 5 * time.Second,
				},
			},
		)
		require.NoError(t, err)

		sess, ok := proc.(*interactiveSession)
		require.True(t, ok)
		require.NoError(t, sess.Kill(20*time.Millisecond))

		poll := waitInteractiveStatus(
			t,
			proc,
			codeexecutor.ProgramStatusExited,
		)
		require.Equal(t, codeexecutor.ProgramStatusExited, poll.Status)
		require.NoError(t, proc.Close())
	})
}

func TestInteractiveSession_KillIgnoresProcessDone(t *testing.T) {
	cmdName := "sh"
	args := []string{"-lc", "true"}
	if runtime.GOOS == "windows" {
		cmdName = "cmd"
		args = []string{"/c", "exit 0"}
	}

	cmd := exec.Command(cmdName, args...) //nolint:gosec
	require.NoError(t, cmd.Start())
	require.NoError(t, cmd.Wait())

	sess := newInteractiveSession("done", "true", 2)
	sess.cmd = cmd

	require.NoError(t, sess.Kill(10*time.Millisecond))
}

func TestRuntime_StartProgram_StdinAndPipeErrors(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	ws := codeexecutor.Workspace{
		ID:   "ws-stdin",
		Path: t.TempDir(),
	}
	_, err := codeexecutor.EnsureLayout(ws.Path)
	require.NoError(t, err)

	proc, err := rt.StartProgram(
		context.Background(),
		ws,
		codeexecutor.InteractiveProgramSpec{
			RunProgramSpec: codeexecutor.RunProgramSpec{
				Cmd: "sh",
				Args: []string{
					"-lc",
					"read v; printf '%s|%s|%s' " +
						"\"$v\" \"$WORK_DIR\" \"$CUSTOM_ENV\"",
				},
				Timeout: 2 * time.Second,
				Stdin:   "hello\n",
				Env: map[string]string{
					codeexecutor.EnvWorkDir: "override",
					"CUSTOM_ENV":            "ok",
				},
			},
		},
	)
	require.NoError(t, err)

	waitInteractiveStatus(t, proc, codeexecutor.ProgramStatusExited)

	provider, ok := proc.(codeexecutor.ProgramResultProvider)
	require.True(t, ok)
	require.Eventually(t, func() bool {
		return strings.Contains(
			provider.RunResult().Stdout,
			"hello|override|ok",
		)
	}, 5*time.Second, 50*time.Millisecond)
	require.NoError(t, proc.Close())

	cmd := exec.Command("sh", "-lc", "true")
	cmd.Stdout = os.Stdout
	stdin, stdout, stderr, _, err := startPipes(cmd)
	require.Error(t, err)
	require.Nil(t, stdin)
	require.Nil(t, stdout)
	require.Nil(t, stderr)

	cmd = exec.Command("sh", "-lc", "true")
	cmd.Stderr = os.Stderr
	stdin, stdout, stderr, _, err = startPipes(cmd)
	require.Error(t, err)
	require.Nil(t, stdin)
	require.Nil(t, stdout)
	require.Nil(t, stderr)

}

func TestRuntime_StartProgram_MkdirError(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	wsPath := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, os.WriteFile(wsPath, []byte("x"), 0o600))

	_, err := rt.StartProgram(
		context.Background(),
		codeexecutor.Workspace{
			ID:   "ws-bad",
			Path: wsPath,
		},
		codeexecutor.InteractiveProgramSpec{
			RunProgramSpec: codeexecutor.RunProgramSpec{
				Cmd:     "sh",
				Args:    []string{"-lc", "true"},
				Cwd:     "work",
				Timeout: time.Second,
			},
		},
	)
	require.Error(t, err)
}

func waitInteractiveExit(
	t *testing.T,
	proc codeexecutor.ProgramSession,
	want string,
) codeexecutor.ProgramPoll {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		poll := proc.Poll(nil)
		if want == "" || strings.Contains(poll.Output, want) ||
			poll.Status == codeexecutor.ProgramStatusExited {
			return poll
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("interactive program did not produce expected output")
	return codeexecutor.ProgramPoll{}
}

func waitInteractiveStatus(
	t *testing.T,
	proc codeexecutor.ProgramSession,
	status string,
) codeexecutor.ProgramPoll {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		poll := proc.Poll(nil)
		if poll.Status == status {
			return poll
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("interactive program did not reach expected status")
	return codeexecutor.ProgramPoll{}
}
