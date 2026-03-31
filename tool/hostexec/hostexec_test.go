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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const backgroundPIDMarker = "__hostexec_bg_pid:"

func TestNewToolSet_Foreground(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet()
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, mgr := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command": "echo hello",
			"yieldMs": 0,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	require.Equal(t, programStatusExited, res["status"])
	require.Contains(t, outputField(res), "hello")
	require.EqualValues(t, 0, res["exit_code"])
	require.Empty(t, mgr.sessions)
}

func TestNewToolSet_BaseDirAndRelativeWorkdir(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	baseDir := t.TempDir()
	subDir := filepath.Join(baseDir, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(subDir, "note.txt"),
		[]byte("hostexec"),
		0o644,
	))

	set, err := NewToolSet(WithBaseDir(baseDir))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, _ := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command": "cat note.txt",
			"workdir": "sub",
			"yieldMs": 0,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	require.Equal(t, programStatusExited, res["status"])
	require.Contains(t, outputField(res), "hostexec")
}

func TestNewToolSet_YieldAndPoll(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet(WithJobTTL(10 * time.Second))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, mgr := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command": "echo start; sleep 0.2; echo end",
			"yieldMs": 10,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	require.Equal(t, programStatusRunning, res["status"])
	sessionID := res["session_id"].(string)
	require.NotEmpty(t, sessionID)

	const (
		pollDeadline = 2 * time.Second
		pollInterval = 50 * time.Millisecond
	)
	deadline := time.Now().Add(pollDeadline)
	all := outputField(res)
	for time.Now().Before(deadline) {
		poll, err := mgr.poll(sessionID, nil)
		require.NoError(t, err)
		if poll.Output != "" {
			all += "\n" + poll.Output
		}
		if poll.Status == programStatusExited {
			require.Contains(t, all, "start")
			require.Contains(t, all, "end")
			return
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("process did not exit; output: %s", all)
}

func TestNewToolSet_WriteStdin(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet(WithJobTTL(10 * time.Second))
	require.NoError(t, err)
	defer set.Close()

	execTool, writeTool, _, mgr := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    `read -r line; echo got:$line`,
			"background": true,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	sessionID := res["session_id"].(string)
	require.NotEmpty(t, sessionID)

	writeOut, err := writeTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"session_id":     sessionID,
			"chars":          "hi",
			"append_newline": true,
		}),
	)
	require.NoError(t, err)

	all := outputField(writeOut.(map[string]any))
	all += pollUntilExited(t, mgr, sessionID)
	require.Contains(t, all, "got:hi")
}

func TestNewToolSet_WriteStdin_NoRepeatedInitialOutput(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet(WithJobTTL(10 * time.Second))
	require.NoError(t, err)
	defer set.Close()

	execTool, writeTool, _, mgr := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command": "printf 'ready\\n'; read -r line; " +
				"echo got:$line",
			"yieldMs": 100,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	require.Equal(t, programStatusRunning, res["status"])
	sessionID := res["session_id"].(string)
	require.NotEmpty(t, sessionID)
	initial := outputField(res)
	if !strings.Contains(initial, "ready") {
		initial += waitForOutputContains(
			t,
			mgr,
			sessionID,
			"ready",
		)
	}
	require.Contains(t, initial, "ready")

	writeOut, err := writeTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"session_id":     sessionID,
			"chars":          "hi",
			"append_newline": true,
		}),
	)
	require.NoError(t, err)
	postWrite := outputField(writeOut.(map[string]any))
	postWrite += pollUntilExited(t, mgr, sessionID)
	require.NotContains(t, postWrite, "ready")
	require.Contains(t, postWrite, "got:hi")
}

func TestNewToolSet_KillSession(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet(WithJobTTL(10 * time.Second))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, killTool, mgr := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "sleep 5",
			"background": true,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	sessionID := res["session_id"].(string)
	require.NotEmpty(t, sessionID)

	killOut, err := killTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"session_id": sessionID,
		}),
	)
	require.NoError(t, err)

	killRes := killOut.(map[string]any)
	require.Equal(t, true, killRes["ok"])
	require.Equal(t, sessionID, killRes["session_id"])
	_ = pollUntilExited(t, mgr, sessionID)
}

func TestNewToolSet_KillSessionRespectsContext(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet(WithJobTTL(10 * time.Second))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, killTool, mgr := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "sleep 5",
			"background": true,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	sessionID := res["session_id"].(string)
	require.NotEmpty(t, sessionID)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := time.Now()
	killOut, err := killTool.Call(
		ctx,
		mustJSON(t, map[string]any{
			"session_id": sessionID,
		}),
	)
	require.NoError(t, err)
	require.Less(t, time.Since(started), time.Second)

	killRes := killOut.(map[string]any)
	require.Equal(t, true, killRes["ok"])
	require.Equal(t, sessionID, killRes["session_id"])
	_ = pollUntilExited(t, mgr, sessionID)
}

func TestNewToolSet_CloseKillsSessions(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet(WithJobTTL(10 * time.Second))
	require.NoError(t, err)

	execTool, _, _, toolMgr := toolSetTools(t, set)
	_, err = execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "sleep 5",
			"background": true,
		}),
	)
	require.NoError(t, err)
	require.NotEmpty(t, toolMgr.sessions)
	require.NoError(t, set.Close())
	require.Empty(t, toolMgr.sessions)
}

func TestNewToolSet_KillSessionKillsBackgroundChild(
	t *testing.T,
) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group cleanup is tested on unix")
	}
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet(WithJobTTL(10 * time.Second))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, killTool, mgr := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    backgroundChildCommand(true),
			"background": true,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	sessionID := res["session_id"].(string)
	require.NotEmpty(t, sessionID)

	output := outputField(res)
	if !strings.Contains(output, backgroundPIDMarker) {
		output += waitForOutputContains(
			t,
			mgr,
			sessionID,
			backgroundPIDMarker,
		)
	}
	bgPID := parseBackgroundPID(t, output)
	require.True(t, processExists(bgPID))

	_, err = killTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"session_id": sessionID,
		}),
	)
	require.NoError(t, err)

	_ = pollUntilExited(t, mgr, sessionID)
	waitForProcessExit(t, bgPID, 3*time.Second)
}

func TestNewToolSet_ExitedShellCleansBackgroundChild(
	t *testing.T,
) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group cleanup is tested on unix")
	}
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet(WithJobTTL(10 * time.Second))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, mgr := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    backgroundChildCommand(false),
			"background": true,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	sessionID := res["session_id"].(string)
	require.NotEmpty(t, sessionID)

	output := outputField(res)
	if !strings.Contains(output, backgroundPIDMarker) {
		output += waitForOutputContains(
			t,
			mgr,
			sessionID,
			backgroundPIDMarker,
		)
	}
	bgPID := parseBackgroundPID(t, output)
	require.True(t, processExists(bgPID))

	_ = pollUntilExited(t, mgr, sessionID)
	waitForProcessExit(t, bgPID, 3*time.Second)
}

func TestNewToolSet_TimeoutKillsBackgroundChild(
	t *testing.T,
) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group cleanup is tested on unix")
	}
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet(WithJobTTL(10 * time.Second))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, mgr := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":     backgroundChildCommand(true),
			"background":  true,
			"timeout_sec": 1,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	sessionID := res["session_id"].(string)
	require.NotEmpty(t, sessionID)

	output := outputField(res)
	if !strings.Contains(output, backgroundPIDMarker) {
		output += waitForOutputContains(
			t,
			mgr,
			sessionID,
			backgroundPIDMarker,
		)
	}
	bgPID := parseBackgroundPID(t, output)
	require.True(t, processExists(bgPID))

	_ = pollUntilExited(t, mgr, sessionID)
	waitForProcessExit(t, bgPID, 3*time.Second)
}

func TestNewToolSet_TimeoutDoesNotWaitForGrace(
	t *testing.T,
) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group cleanup is tested on unix")
	}
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet(WithJobTTL(10 * time.Second))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, mgr := toolSetTools(t, set)
	started := time.Now()
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":     `bash -lc "trap '' TERM; sleep 1000"`,
			"background":  true,
			"timeout_sec": 1,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	sessionID := res["session_id"].(string)
	require.NotEmpty(t, sessionID)

	_ = pollUntilExited(t, mgr, sessionID)
	require.Less(
		t,
		time.Since(started),
		2500*time.Millisecond,
	)
}

func TestRunForeground_ContextCancel(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	const cancelDelay = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		time.Sleep(cancelDelay)
		cancel()
	}()

	output, exitCode, err := runForeground(
		ctx,
		execParams{Command: "sleep 5"},
		5*time.Second,
		nil,
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, output)
	require.Zero(t, exitCode)
}

func TestNewToolSet_PTYForeground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty is not supported on windows")
	}
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet()
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, _ := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command": "echo hi",
			"pty":     true,
			"yieldMs": 0,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	require.Equal(t, programStatusExited, res["status"])
	require.Contains(t, outputField(res), "hi")
}

func TestNewToolSet_PTYKillSessionKillsBackgroundChild(
	t *testing.T,
) {
	if runtime.GOOS == "windows" {
		t.Skip("pty is not supported on windows")
	}
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet(WithJobTTL(10 * time.Second))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, killTool, mgr := toolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    backgroundChildCommand(true),
			"background": true,
			"tty":        true,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	sessionID := res["session_id"].(string)
	require.NotEmpty(t, sessionID)

	output := outputField(res)
	if !strings.Contains(output, backgroundPIDMarker) {
		output += waitForOutputContains(
			t,
			mgr,
			sessionID,
			backgroundPIDMarker,
		)
	}
	bgPID := parseBackgroundPID(t, output)
	require.True(t, processExists(bgPID))

	_, err = killTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"session_id": sessionID,
		}),
	)
	require.NoError(t, err)

	_ = pollUntilExited(t, mgr, sessionID)
	waitForProcessExit(t, bgPID, 3*time.Second)
}

func TestResolveWorkdir(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	wd, err := resolveWorkdir("", "/tmp/base")
	require.NoError(t, err)
	require.Equal(t, "/tmp/base", wd)

	wd, err = resolveWorkdir("~", "")
	require.NoError(t, err)
	require.Equal(t, home, wd)

	wd, err = resolveWorkdir("~/x", "")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, "x"), wd)

	wd, err = resolveWorkdir("sub", "/tmp/base")
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/tmp/base", "sub"), wd)
}

func TestTools_InvalidArgs(t *testing.T) {
	set, err := NewToolSet()
	require.NoError(t, err)
	defer set.Close()

	execTool, writeTool, killTool, _ := toolSetTools(t, set)

	_, err = execTool.Call(context.Background(), []byte("{"))
	require.Error(t, err)

	_, err = writeTool.Call(context.Background(), []byte("{"))
	require.Error(t, err)

	_, err = killTool.Call(context.Background(), []byte("{"))
	require.Error(t, err)
}

func TestToolDeclarations_UseIntegerDurations(t *testing.T) {
	set, err := NewToolSet()
	require.NoError(t, err)
	defer set.Close()

	execTool, writeTool, _, _ := toolSetTools(t, set)
	execDecl := execTool.Declaration().InputSchema.Properties
	require.Equal(t, "integer", execDecl["yield_time_ms"].Type)
	require.Equal(t, "integer", execDecl["yieldMs"].Type)
	require.Equal(t, "integer", execDecl["timeout_sec"].Type)
	require.Equal(t, "integer", execDecl["timeoutSec"].Type)

	writeDecl := writeTool.Declaration().InputSchema.Properties
	require.Equal(t, "integer", writeDecl["yield_time_ms"].Type)
	require.Equal(t, "integer", writeDecl["yieldMs"].Type)
}

func TestManager_GetUnknownSession(t *testing.T) {
	mgr := newManager()

	_, err := mgr.get("missing")
	require.ErrorIs(t, err, errUnknownSession)
}

func TestNewToolSet_HugeTimeout(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	set, err := NewToolSet()
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, _ := toolSetTools(t, set)
	hugeTimeout := int(^uint(0) >> 1)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":       "sleep 0.1; echo ok",
			"yield_time_ms": 0,
			"timeout_sec":   hugeTimeout,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	require.Equal(t, programStatusExited, res["status"])
	require.Contains(t, outputField(res), "ok")
	require.EqualValues(t, 0, res["exit_code"])
}

func TestSessionKill_IgnoresProcessDone(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd, err := shellCmd(ctx, "true")
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	_, err = cmd.Process.Wait()
	require.NoError(t, err)

	sess := newSession("done", "true", defaultMaxLines)
	sess.cmd = cmd
	sess.cancel = cancel

	require.NoError(t, sess.kill(context.Background(), 0))
}

func TestToolSet_MetadataAndOptions(t *testing.T) {
	baseDir := t.TempDir()
	baseEnv := map[string]string{
		"HOSTEXEC_ONE": "1",
		" ":            "skip",
	}

	set, err := NewToolSet(
		WithBaseDir(baseDir),
		WithName(" custom-tool "),
		WithMaxLines(7),
		WithJobTTL(time.Second),
		WithBaseEnv(baseEnv),
	)
	require.NoError(t, err)
	defer set.Close()

	typed := set.(*toolSet)
	require.Equal(t, "custom-tool", typed.Name())
	require.Len(t, typed.Tools(context.Background()), 3)
	require.Equal(t, 7, typed.mgr.maxLines)
	require.Equal(t, time.Second, typed.mgr.jobTTL)
	require.Equal(
		t,
		map[string]string{"HOSTEXEC_ONE": "1"},
		typed.mgr.baseEnv,
	)

	baseEnv["HOSTEXEC_ONE"] = "2"
	require.Equal(t, "1", typed.mgr.baseEnv["HOSTEXEC_ONE"])

	blank, err := NewToolSet(WithName("   "))
	require.NoError(t, err)
	defer blank.Close()
	require.Equal(t, defaultToolSetName, blank.(*toolSet).Name())

	var nilSet *toolSet
	require.NoError(t, nilSet.Close())
}

func TestToolCalls_NotConfigured(t *testing.T) {
	var execTool *execCommandTool
	_, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{"command": "echo hi"}),
	)
	require.EqualError(t, err, errExecToolNotConfigured)

	var writeTool *writeStdinTool
	_, err = writeTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{"session_id": "x"}),
	)
	require.EqualError(t, err, errWriteToolNotConfigured)

	var killTool *killSessionTool
	require.Equal(
		t,
		toolKillSession,
		killTool.Declaration().Name,
	)
	_, err = killTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{"session_id": "x"}),
	)
	require.EqualError(t, err, errKillToolNotConfigured)
}

func TestWriteStdin_CanceledBeforePoll(t *testing.T) {
	mgr := newManager()
	sess := newSession("session", "cat", defaultMaxLines)
	sess.stdin = &testWriteCloser{}
	mgr.sessions[sess.id] = sess

	tool := &writeStdinTool{mgr: mgr}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.Call(
		ctx,
		mustJSON(t, map[string]any{
			"sessionId": "session",
			"chars":     "hello",
			"yieldMs":   1,
			"submit":    true,
		}),
	)
	require.ErrorIs(t, err, context.Canceled)
}

func TestHostexec_HelperFunctions(t *testing.T) {
	updated := setEnv([]string{"HOSTEXEC_A=1"}, "HOSTEXEC_A", "2")
	require.Equal(t, []string{"HOSTEXEC_A=2"}, updated)

	env := mergedEnv(
		map[string]string{"HOSTEXEC_A": "base"},
		map[string]string{"HOSTEXEC_A": "extra", "HOSTEXEC_B": "2"},
	)
	require.Contains(t, env, "HOSTEXEC_A=extra")
	require.Contains(t, env, "HOSTEXEC_B=2")

	require.Equal(t, 0, exitCode(nil))
	require.Equal(t, -1, exitCode(errors.New("boom")))

	cmd := exec.Command("sh", "-c", "exit 7")
	err := cmd.Run()
	require.Equal(t, 7, exitCode(err))

	require.Equal(
		t,
		time.Duration(defaultTimeoutS)*time.Second,
		timeoutDuration(0),
	)
	require.Equal(
		t,
		time.Duration(maxTimeoutSeconds)*time.Second,
		timeoutDuration(int(maxTimeoutSeconds)+1),
	)

	require.Nil(t, firstInt())
	first := 1
	second := 2
	require.Same(t, &first, firstInt(nil, &first, &second))

	require.False(t, firstBool())
	no := false
	yes := true
	require.False(t, firstBool(nil, &no, &yes))
	require.True(t, firstBool(nil, &yes))

	require.Equal(t, "", firstNonEmpty(" ", "\t"))

	baseDir, err := resolveBaseDir("")
	require.NoError(t, err)
	require.Equal(t, "", baseDir)

	cloned := cloneEnvMap(map[string]string{
		"HOSTEXEC_A": "1",
		" ":          "skip",
	})
	require.Equal(t, map[string]string{"HOSTEXEC_A": "1"}, cloned)
}

func TestShellSpec_ErrorWhenShellMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell lookup differs on windows")
	}

	t.Setenv("PATH", "")
	_, _, err := shellSpec()
	require.ErrorContains(t, err, "bash or sh is required")
}

func TestManager_HelperBranches(t *testing.T) {
	mgr := newManager()

	_, err := mgr.poll("missing", nil)
	require.ErrorIs(t, err, errUnknownSession)
	require.ErrorIs(
		t,
		mgr.write("missing", "x", false),
		errUnknownSession,
	)
	require.ErrorIs(
		t,
		mgr.killContext(context.Background(), "missing"),
		errUnknownSession,
	)

	running := newSession("running", "sleep", defaultMaxLines)
	mgr.sessions[running.id] = running
	require.EqualError(
		t,
		mgr.clearFinished(running.id),
		"session is still running",
	)

	now := time.Now()
	oldDone := newSession("old", "done", defaultMaxLines)
	oldDone.finished = now.Add(-2 * time.Hour)
	recentDone := newSession("recent", "done", defaultMaxLines)
	recentDone.finished = now
	mgr.sessions[oldDone.id] = oldDone
	mgr.sessions[recentDone.id] = recentDone
	mgr.jobTTL = time.Minute
	mgr.clock = func() time.Time { return now }
	mgr.cleanupExpired()
	require.NotContains(t, mgr.sessions, oldDone.id)
	require.Contains(t, mgr.sessions, recentDone.id)
	require.Contains(t, mgr.sessions, running.id)

	waitDone(nil, time.Millisecond)
	waitDone(make(chan struct{}), 0)
	done := make(chan struct{})
	close(done)
	waitDone(done, time.Second)
}

func TestManager_ExecValidationAndStartErrors(t *testing.T) {
	mgr := newManager()

	_, err := mgr.exec(nil, execParams{Command: "echo hi"})
	require.EqualError(t, err, "nil context")

	_, err = mgr.exec(context.Background(), execParams{})
	require.EqualError(t, err, errCommandRequired)

	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	_, err = mgr.startBackground(
		execParams{
			Command: "echo hi",
			Workdir: filepath.Join(t.TempDir(), "missing"),
		},
		time.Second,
	)
	require.Error(t, err)

	yield := 200
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err = mgr.exec(
		ctx,
		execParams{
			Command: "sleep 5",
			YieldMs: &yield,
		},
	)
	require.ErrorIs(t, err, context.Canceled)
}

func TestSession_HelpersAndBranches(t *testing.T) {
	sess := newSession("s", "cmd", 1)
	require.True(t, sess.doneAt().IsZero())

	sess.appendOutput("first\nsecond")
	sess.appendOutput("\nthird\n")
	require.Equal(t, 2, sess.lineBase)
	require.Equal(t, []string{"third"}, sess.lines)

	sess.pollCursor = 0
	limit := 1
	poll := sess.poll(&limit)
	require.Equal(t, 2, poll.Offset)
	require.Equal(t, 3, poll.NextOffset)
	require.Equal(t, "third", poll.Output)

	require.Equal(t, "", sess.tail(0))
	require.Equal(t, "third", sess.tail(1))

	sess.partial = "tail"
	require.Equal(t, "tail", trimOutputTail("head\ntail", 1))
	require.Equal(t, "", trimOutputTail("", 1))
	require.Equal(t, "", trimOutputTail("head", 0))

	sess.markDone(7)
	doneAt := sess.doneAt()
	require.False(t, doneAt.IsZero())
	sess.markDone(9)

	out, code := sess.allOutput()
	require.Equal(t, "third\ntail", out)
	require.Equal(t, 7, code)

	exited := sess.poll(nil)
	require.Equal(t, programStatusExited, exited.Status)
	require.NotNil(t, exited.ExitCode)
	require.Equal(t, 7, *exited.ExitCode)
}

func TestSession_WriteAndCloseBranches(t *testing.T) {
	sess := newSession("write", "cmd", defaultMaxLines)
	writer := &testWriteCloser{}
	sess.stdin = writer

	require.NoError(t, sess.write("", false))
	require.NoError(t, sess.write("hello", false))
	require.Equal(t, "hello", writer.String())

	noStdin := newSession("no-stdin", "cmd", defaultMaxLines)
	require.EqualError(
		t,
		noStdin.write("hello", false),
		"stdin is not available",
	)

	stopped := newSession("stopped", "cmd", defaultMaxLines)
	stopped.stdin = &testWriteCloser{}
	stopped.finished = time.Now()
	require.EqualError(
		t,
		stopped.write("hello", false),
		"session is not running",
	)

	closeErr := errors.New("close failed")
	count := 0
	closer := newSession("close", "cmd", defaultMaxLines)
	closer.closeIO = func() error {
		count++
		return closeErr
	}
	require.ErrorIs(t, closer.close(), closeErr)
	require.NoError(t, closer.close())
	require.Equal(t, 1, count)
}

func TestSession_KillBranches(t *testing.T) {
	sess := newSession("nil", "cmd", defaultMaxLines)
	canceled := false
	sess.cancel = func() { canceled = true }
	require.NoError(t, sess.kill(context.Background(), -1))
	require.True(t, canceled)

	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	cmd, err := shellCmd(context.Background(), "sleep 5")
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	killSess := newSession("kill", "sleep 5", defaultMaxLines)
	killSess.cmd = cmd
	require.NoError(t, killSess.kill(ctx, -1))
	_, err = cmd.Process.Wait()
	require.NoError(t, err)
}

func TestStartPTY_ErrorBranches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty is not supported on windows")
	}

	_, _, err := startPTY(nil)
	require.EqualError(t, err, "nil command")

	cmd := exec.Command("definitely-missing-binary")
	_, _, err = startPTY(cmd)
	require.Error(t, err)
}

type testWriteCloser struct {
	bytes.Buffer
}

func (w *testWriteCloser) Close() error {
	return nil
}

func toolSetTools(
	t *testing.T,
	set tool.ToolSet,
) (
	tool.CallableTool,
	tool.CallableTool,
	tool.CallableTool,
	*manager,
) {
	t.Helper()

	typed := set.(*toolSet)
	return typed.tools[0].(tool.CallableTool),
		typed.tools[1].(tool.CallableTool),
		typed.tools[2].(tool.CallableTool),
		typed.mgr
}

func pollUntilExited(
	t *testing.T,
	mgr *manager,
	sessionID string,
) string {
	t.Helper()

	const (
		pollDeadline = 2 * time.Second
		pollInterval = 50 * time.Millisecond
	)
	deadline := time.Now().Add(pollDeadline)
	var all string
	for time.Now().Before(deadline) {
		poll, err := mgr.poll(sessionID, nil)
		require.NoError(t, err)
		if poll.Output != "" {
			all += "\n" + poll.Output
		}
		if poll.Status == programStatusExited {
			return all
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("process did not exit; output: %s", all)
	return ""
}

func waitForOutputContains(
	t *testing.T,
	mgr *manager,
	sessionID string,
	want string,
) string {
	t.Helper()

	const (
		pollDeadline = 2 * time.Second
		pollInterval = 50 * time.Millisecond
	)
	deadline := time.Now().Add(pollDeadline)
	var all string
	for time.Now().Before(deadline) {
		poll, err := mgr.poll(sessionID, nil)
		require.NoError(t, err)
		if poll.Output != "" {
			if all != "" {
				all += "\n"
			}
			all += poll.Output
			if strings.Contains(all, want) {
				return all
			}
		}
		if poll.Status == programStatusExited {
			break
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("did not observe %q; output: %s", want, all)
	return ""
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()

	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}

func outputField(out map[string]any) string {
	value, _ := out["output"].(string)
	return strings.TrimSpace(value)
}

func backgroundChildCommand(wait bool) string {
	command := "sleep 1000 & bg=$!; echo " +
		backgroundPIDMarker + "$bg"
	if wait {
		return command + "; wait"
	}
	return command
}

func parseBackgroundPID(t *testing.T, output string) int {
	t.Helper()

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		index := strings.Index(line, backgroundPIDMarker)
		if index < 0 {
			continue
		}
		pidText := strings.TrimSpace(
			line[index+len(backgroundPIDMarker):],
		)
		pid, err := strconv.Atoi(pidText)
		require.NoError(t, err)
		return pid
	}

	t.Fatalf("did not find background pid marker in %q", output)
	return 0
}
