//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package octool

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	sessionpkg "trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func newExecCommandTool(mgr *Manager) tool.CallableTool {
	return NewExecCommandTool(mgr).(tool.CallableTool)
}

func newWriteStdinTool(mgr *Manager) tool.CallableTool {
	return NewWriteStdinTool(mgr).(tool.CallableTool)
}

func newKillSessionTool(mgr *Manager) tool.CallableTool {
	return NewKillSessionTool(mgr).(tool.CallableTool)
}

func TestExecTool_Foreground(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager()
	tool := newExecCommandTool(mgr)

	args := mustJSON(t, map[string]any{
		"command": "echo hello",
		"yieldMs": 0,
	})
	out, err := tool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)
	require.Contains(t, res.Output, "hello")
	require.Equal(t, 0, res.ExitCode)
}

func TestExecTool_YieldBackgroundAndPoll(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10 * time.Second))
	execTool := newExecCommandTool(mgr)

	args := mustJSON(t, map[string]any{
		"command": "echo start; sleep 0.2; echo end",
		"yieldMs": 10,
	})
	out, err := execTool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "running", res.Status)
	require.NotEmpty(t, res.SessionID)

	const (
		pollDeadline = 2 * time.Second
		pollInterval = 50 * time.Millisecond
	)
	deadline := time.Now().Add(pollDeadline)
	var all string
	for time.Now().Before(deadline) {
		poll, err := mgr.poll(res.SessionID, nil)
		require.NoError(t, err)
		if poll.Output != "" {
			all += "\n" + poll.Output
		}
		if poll.Status == "exited" {
			require.Contains(t, all, "start")
			require.Contains(t, all, "end")
			return
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("process did not exit; output: %s", all)
}

func TestProcessTool_Submit(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10 * time.Second))
	execTool := newExecCommandTool(mgr)
	writeTool := newWriteStdinTool(mgr)

	args := mustJSON(t, map[string]any{
		"command":    `read -r line; echo got:$line`,
		"background": true,
	})
	out, err := execTool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "running", res.Status)
	require.NotEmpty(t, res.SessionID)

	submitArgs := mustJSON(t, map[string]any{
		"session_id":     res.SessionID,
		"chars":          "hi",
		"append_newline": true,
	})
	writeAny, err := writeTool.Call(context.Background(), submitArgs)
	require.NoError(t, err)
	writeRes := writeAny.(map[string]any)
	all := outputField(writeRes)

	const (
		pollDeadline = 2 * time.Second
		pollInterval = 50 * time.Millisecond
	)
	deadline := time.Now().Add(pollDeadline)
	var exited bool
	for time.Now().Before(deadline) {
		poll, err := mgr.poll(res.SessionID, nil)
		require.NoError(t, err)
		if poll.Output != "" {
			all += "\n" + poll.Output
		}
		if poll.Status == "exited" {
			exited = true
			if strings.Contains(all, "got:hi") {
				return
			}
		}
		time.Sleep(pollInterval)
	}
	if exited {
		t.Fatalf("process exited; output: %s", all)
	}
	t.Fatalf("process did not exit; output: %s", all)
}

func TestExecTool_PTYForeground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty is not supported on windows")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager()
	tool := newExecCommandTool(mgr)

	args := mustJSON(t, map[string]any{
		"command": "echo hi",
		"pty":     true,
		"yieldMs": 0,
	})
	out, err := tool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)
	require.Contains(t, res.Output, "hi")
	require.Equal(t, 0, res.ExitCode)
}

func TestManager_MaxLinesTrimsOutput(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10*time.Second), WithMaxLines(1))
	execTool := newExecCommandTool(mgr)

	args := mustJSON(t, map[string]any{
		"command":    "printf 'a\\nb\\nc\\n'",
		"background": true,
	})
	out, err := execTool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "running", res.Status)
	require.NotEmpty(t, res.SessionID)

	pollUntilExited(t, mgr, res.SessionID)

	logAny, err := mgr.log(res.SessionID, nil, nil)
	require.NoError(t, err)

	log := logAny
	require.Equal(t, "c", strings.TrimSpace(log.Output))
}

func TestProcessTool_ListKillClearRemove(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10 * time.Second))
	execTool := newExecCommandTool(mgr)
	killTool := newKillSessionTool(mgr)

	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "sleep 5",
			"background": true,
		}),
	)
	require.NoError(t, err)

	res := out.(execResult)
	require.NotEmpty(t, res.SessionID)

	err = mgr.clearFinished(res.SessionID)
	require.Error(t, err)

	list := map[string]any{
		"sessions": mgr.list(),
	}
	sessions := list["sessions"].([]processSession)
	require.NotEmpty(t, sessions)

	_, err = killTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"session_id": res.SessionID,
		}),
	)
	require.NoError(t, err)

	pollUntilExited(t, mgr, res.SessionID)

	err = mgr.clearFinished(res.SessionID)
	require.NoError(t, err)

	_, err = mgr.poll("", nil)
	require.Error(t, err)

	err = mgr.remove("missing")
	require.Error(t, err)

	out, err = execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "echo bye",
			"background": true,
		}),
	)
	require.NoError(t, err)

	res = out.(execResult)
	err = mgr.remove(res.SessionID)
	require.NoError(t, err)
}

func TestManager_MergedEnvAndExitCode(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	env := mergedEnv(map[string]string{
		"FOO":  "bar",
		"PATH": "testpath",
	})
	require.NotNil(t, env)
	require.Contains(t, env, "FOO=bar")
	require.Contains(t, env, "PATH=testpath")

	err := exec.Command("bash", "-lc", "exit 7").Run()
	require.Error(t, err)
	require.Equal(t, 7, exitCode(err))
}

func TestResolveWorkdir(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	wd, err := resolveWorkdir("")
	require.NoError(t, err)
	require.Empty(t, wd)

	wd, err = resolveWorkdir("~")
	require.NoError(t, err)
	require.Equal(t, home, wd)

	wd, err = resolveWorkdir("~/x")
	require.NoError(t, err)
	require.Equal(t, filepath.ToSlash(home)+"/x", filepath.ToSlash(wd))
}

func TestManager_CleanupExpiredRemovesFinished(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(1 * time.Nanosecond))
	execTool := newExecCommandTool(mgr)

	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "echo done",
			"background": true,
		}),
	)
	require.NoError(t, err)

	res := out.(execResult)
	pollUntilExited(t, mgr, res.SessionID)

	sess, err := mgr.get(res.SessionID)
	require.NoError(t, err)
	doneAt := sess.doneAt()
	mgr.clock = func() time.Time {
		return doneAt.Add(10 * time.Second)
	}

	sessions := mgr.list()
	require.Empty(t, sessions)
}

func TestExitCode_NonExitError(t *testing.T) {
	require.Equal(t, -1, exitCode(errors.New("x")))
}

func TestProcessTool_Write(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10 * time.Second))
	execTool := newExecCommandTool(mgr)
	writeTool := newWriteStdinTool(mgr)

	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "read -r x; echo got:$x",
			"background": true,
		}),
	)
	require.NoError(t, err)

	res := out.(execResult)
	writeAny, err := writeTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"session_id": res.SessionID,
			"chars":      "ok\n",
		}),
	)
	require.NoError(t, err)

	output := outputField(writeAny.(map[string]any))
	output += pollUntilExited(t, mgr, res.SessionID)
	require.Contains(t, output, "got:ok")
}

func TestTools_InvalidArgs(t *testing.T) {
	mgr := NewManager()
	execTool := newExecCommandTool(mgr)
	_, err := execTool.Call(context.Background(), []byte("{"))
	require.Error(t, err)

	writeTool := newWriteStdinTool(mgr)
	_, err = writeTool.Call(context.Background(), []byte("{"))
	require.Error(t, err)
}

func TestSortSessions_SortsBySessionID(t *testing.T) {
	s := []processSession{
		{SessionID: "b"},
		{SessionID: "a"},
	}
	sortSessions(s)
	require.Equal(t, "a", s[0].SessionID)
	require.Equal(t, "b", s[1].SessionID)
}

func TestTools_Declaration(t *testing.T) {
	mgr := NewManager()
	execTool := newExecCommandTool(mgr)
	writeTool := newWriteStdinTool(mgr)
	killTool := newKillSessionTool(mgr)

	require.Equal(t, toolExecCommand, execTool.Declaration().Name)
	require.Equal(t, toolWriteStdin, writeTool.Declaration().Name)
	require.Equal(t, toolKillSession, killTool.Declaration().Name)
}

func TestManager_ListIncludesExitedSession(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10 * time.Second))
	execTool := newExecCommandTool(mgr)

	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "echo hi",
			"background": true,
		}),
	)
	require.NoError(t, err)

	res := out.(execResult)
	pollUntilExited(t, mgr, res.SessionID)

	sessions := mgr.list()
	require.NotEmpty(t, sessions)
	require.Equal(t, "exited", sessions[0].Status)
}

func TestManager_RemoveRunningSession(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10 * time.Second))
	execTool := newExecCommandTool(mgr)

	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "sleep 5",
			"background": true,
		}),
	)
	require.NoError(t, err)

	res := out.(execResult)
	err = mgr.remove(res.SessionID)
	require.NoError(t, err)

	sessions := mgr.list()
	require.Empty(t, sessions)
}

func TestStartPipes_ErrorWhenStdioSet(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	t.Run("stdin set", func(t *testing.T) {
		cmd := exec.Command("bash", "-lc", "echo ok")
		cmd.Stdin = strings.NewReader("x")
		_, _, _, err := startPipes(cmd)
		require.Error(t, err)
	})

	t.Run("stdout set", func(t *testing.T) {
		cmd := exec.Command("bash", "-lc", "echo ok")
		cmd.Stdout = io.Discard
		_, _, _, err := startPipes(cmd)
		require.Error(t, err)
	})

	t.Run("stderr set", func(t *testing.T) {
		cmd := exec.Command("bash", "-lc", "echo ok")
		cmd.Stderr = io.Discard
		_, _, _, err := startPipes(cmd)
		require.Error(t, err)
	})
}

func TestStartPTY_NilCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty is not supported on windows")
	}
	_, _, err := startPTY(nil)
	require.Error(t, err)
}

func TestSession_TailAllOutputAndMarkDone(t *testing.T) {
	s := newSession("id", "cmd", 0)

	require.Empty(t, s.tail(0))

	s.appendOutput("a\nb")
	require.Equal(t, "a\nb", s.tail(10))

	out, code := s.allOutput()
	require.Equal(t, "a\nb", out)
	require.Equal(t, 0, code)

	s.markDone(7)
	out, code = s.allOutput()
	require.Equal(t, "a\nb", out)
	require.Equal(t, 7, code)

	s.markDone(9)
	_, code = s.allOutput()
	require.Equal(t, 7, code)

	snap := s.snapshot()
	require.Equal(t, "exited", snap.Status)
	require.NotNil(t, snap.ExitCode)
	require.Equal(t, 7, *snap.ExitCode)
}

func TestSession_Log(t *testing.T) {
	s := newSession("id", "cmd", 0)

	total := defaultLogLimit + 50
	for i := 0; i < total; i++ {
		s.appendOutput("x\n")
	}

	got := s.log(nil, nil)
	require.Equal(t, 50, got.Offset)
	require.Equal(t, total, got.NextOffset)
	require.Len(t, strings.Split(got.Output, "\n"), defaultLogLimit)

	offset := 999
	got = s.log(&offset, nil)
	require.Empty(t, got.Output)
	require.Equal(t, total, got.Offset)
	require.Equal(t, total, got.NextOffset)

	offset = 20
	limit := 2
	got = s.log(&offset, &limit)
	require.Len(t, strings.Split(got.Output, "\n"), 2)
	require.Equal(t, offset, got.Offset)
	require.Equal(t, offset+limit, got.NextOffset)
}

func TestTools_NilManagers(t *testing.T) {
	execTool := newExecCommandTool(nil)
	_, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{"command": "echo hi"}),
	)
	require.Error(t, err)

	writeTool := newWriteStdinTool(nil)
	_, err = writeTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{"session_id": "x"}),
	)
	require.Error(t, err)
}

func TestManager_ExecErrors(t *testing.T) {
	mgr := NewManager()

	_, err := mgr.Exec(nil, execParams{Command: "echo hi"})
	require.Error(t, err)

	_, err = mgr.Exec(context.Background(), execParams{})
	require.Error(t, err)
}

func TestUploadEnvFromContext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "report.pdf")
	require.NoError(t, os.WriteFile(
		filePath,
		[]byte("pdf"),
		0o600,
	))

	userMsg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{
				Type: model.ContentTypeFile,
				File: &model.File{
					Name:   "report.pdf",
					FileID: "host://" + filePath,
				},
			},
		},
	}
	ev := event.NewResponseEvent("inv", "user", &model.Response{
		Choices: []model.Choice{{Message: userMsg}},
	})
	inv := agent.NewInvocation(agent.WithInvocationSession(
		&sessionpkg.Session{
			Events: []event.Event{*ev},
		},
	))
	ctx := agent.NewInvocationContext(context.Background(), inv)

	env := uploadEnvFromContext(ctx)
	require.Equal(t, filePath, env[envLastUploadPath])
	require.Equal(t, dir, env[envSessionUploadsDir])
	require.Equal(t, "report.pdf", env[envLastUploadName])
}

func TestMergeExecEnv_PreservesExplicitEnv(t *testing.T) {
	t.Parallel()

	merged := mergeExecEnv(
		map[string]string{envLastUploadPath: "explicit"},
		map[string]string{
			envLastUploadPath: "derived",
			envLastUploadName: "report.pdf",
		},
	)
	require.Equal(t, "explicit", merged[envLastUploadPath])
	require.Equal(t, "report.pdf", merged[envLastUploadName])
}

func pollUntilExited(t *testing.T, mgr *Manager, id string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		pollAny, err := mgr.poll(id, nil)
		require.NoError(t, err)

		poll := pollAny
		if poll.Output != "" {
			out += "\n" + poll.Output
		}
		if poll.Status == "exited" {
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("process did not exit; output: %s", out)
	return ""
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func outputField(result map[string]any) string {
	value, ok := result["output"].(string)
	if !ok {
		return ""
	}
	return value
}
