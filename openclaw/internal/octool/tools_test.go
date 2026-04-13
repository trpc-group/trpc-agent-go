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
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/conversationscope"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
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

func TestExecTool_UsesManagerBaseEnv(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithBaseEnv(map[string]string{
		"OPENCLAW_TEST_ENV": "ok",
	}))
	tool := newExecCommandTool(mgr)

	args := mustJSON(t, map[string]any{
		"command": "printf %s \"$OPENCLAW_TEST_ENV\"",
		"yieldMs": 0,
	})
	out, err := tool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)
	require.Contains(t, strings.TrimSpace(res.Output), "ok")
}

func TestExecTool_RedactsSensitiveEnvValueOutput(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(
		WithOutputRedactor(NewChatCommandOutputRedactor()),
	)
	tool := newExecCommandTool(mgr)

	args := mustJSON(t, map[string]any{
		"command": "printf %s 'sk-test-secret'",
		"env": map[string]string{
			"OPENAI_API_KEY": "sk-test-secret",
		},
		"yieldMs": 0,
	})
	out, err := tool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Contains(t, res.Output, "[REDACTED:OPENAI_API_KEY]")
	require.NotContains(t, res.Output, "sk-test-secret")
}

func TestExecTool_RedactsShortSensitiveEnvValueOutput(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(
		WithOutputRedactor(NewChatCommandOutputRedactor()),
	)
	tool := newExecCommandTool(mgr)

	args := mustJSON(t, map[string]any{
		"command": "printf %s '12345'",
		"env": map[string]string{
			"DB_PASSWORD": "12345",
		},
		"yieldMs": 0,
	})
	out, err := tool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Contains(t, res.Output, "[REDACTED:DB_PASSWORD]")
	require.NotContains(t, res.Output, "12345")
}

func TestKnownSensitiveValues_InlineAssignmentOverridesEnv(
	t *testing.T,
) {
	t.Parallel()

	values := knownSensitiveValues(CommandRequest{
		Command: `export OPENAI_API_KEY='sk-inline-secret'`,
		Env: map[string]string{
			"OPENAI_API_KEY": "sk-env-secret",
		},
	})
	require.Len(t, values, 1)
	require.Equal(t, "OPENAI_API_KEY", values[0].Name)
	require.Equal(t, "sk-inline-secret", values[0].Value)
	require.False(t, values[0].AllowShort)
}

func TestKnownSensitiveValues_SkipsShortInlineValue(t *testing.T) {
	t.Parallel()

	values := knownSensitiveValues(CommandRequest{
		Command: "export OPENAI_API_KEY=short",
	})
	require.Empty(t, values)
}

func TestTrimMatchingQuotes(t *testing.T) {
	t.Parallel()

	require.Equal(t, "value", trimMatchingQuotes(`"value"`))
	require.Equal(t, "value", trimMatchingQuotes(`'value'`))
	require.Equal(t, "value", trimMatchingQuotes("value"))
	require.False(t, hasWrappedQuotes("x", '"'))
}

func TestRedactCommandOutput_EmptyOutput(t *testing.T) {
	t.Parallel()

	output := " \n"
	require.Equal(
		t,
		output,
		redactCommandOutput(CommandRequest{}, output),
	)
}

func TestAddSensitiveEnvValues_IgnoresBlankAndSafeValues(t *testing.T) {
	t.Parallel()

	values := make(map[string]sensitiveValue)
	addSensitiveEnvValues(values, map[string]string{
		"OPENAI_API_KEY": "",
		"SAFE_NAME":      "ok",
	})
	require.Empty(t, values)
}

func TestAddInlineSensitiveValues_IgnoresBlankAndSafeValues(
	t *testing.T,
) {
	t.Parallel()

	values := make(map[string]sensitiveValue)
	addInlineSensitiveValues(
		values,
		`export OPENAI_API_KEY='' SAFE_NAME=ok`,
	)
	require.Empty(t, values)
}

func TestRedactSensitiveValues_IgnoresEmptyTrackedValue(t *testing.T) {
	t.Parallel()

	output := redactSensitiveValues("safe", []sensitiveValue{{
		Name:  "OPENAI_API_KEY",
		Value: "",
	}})
	require.Equal(t, "safe", output)
}

func TestRedactColonLine_IgnoresSafeName(t *testing.T) {
	t.Parallel()

	redacted, ok := redactColonLine(`"SAFE_NAME": "ok"`)
	require.False(t, ok)
	require.Empty(t, redacted)
}

func TestExecTool_BlocksShellProfileAccess(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(
		WithCommandPolicy(NewChatCommandSafetyPolicy()),
	)
	tool := newExecCommandTool(mgr)

	args := mustJSON(t, map[string]any{
		"command": "cat ~/.bashrc",
		"yieldMs": 0,
	})
	_, err := tool.Call(context.Background(), args)
	require.ErrorContains(
		t,
		err,
		"shell or credential files is not allowed",
	)
}

func TestExecTool_RedactsSensitiveKeyValueOutput(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(
		WithOutputRedactor(NewChatCommandOutputRedactor()),
	)
	tool := newExecCommandTool(mgr)

	args := mustJSON(t, map[string]any{
		"command": `printf 'OPENAI_API_KEY=sk-test-secret
SAFE_NAME=ok
"OPENAI_API_KEY": "sk-test-secret",
'`,
		"yieldMs": 0,
	})
	out, err := tool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Contains(t, res.Output, "OPENAI_API_KEY=[REDACTED]")
	require.Contains(t, res.Output, `OPENAI_API_KEY": "[REDACTED]"`)
	require.Contains(t, res.Output, "SAFE_NAME=ok")
	require.NotContains(t, res.Output, "sk-test-secret")
}

func TestExecTool_UsesMemoryFileEnvFromContext(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	stateDir := t.TempDir()
	root, err := memoryfile.DefaultRoot(stateDir)
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	mgr := NewManager()
	execTool := NewExecCommandToolWithMemoryFileStore(
		mgr,
		nil,
		store,
	).(tool.CallableTool)

	sessionID := "telegram:dm:u1:s1"
	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			sessionpkg.NewSession("app", "u1", sessionID),
		),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	args := mustJSON(t, map[string]any{
		"command": "printf %s \"$OPENCLAW_MEMORY_FILE\"",
		"yieldMs": 0,
	})
	out, err := execTool.Call(ctx, args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)

	path, err := store.MemoryPath("app", "u1")
	require.NoError(t, err)
	require.Contains(t, res.Output, path)
	require.FileExists(t, path)
}

func TestExecTool_UsesStorageScopedMemoryFileEnvFromContext(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	stateDir := t.TempDir()
	root, err := memoryfile.DefaultRoot(stateDir)
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	mgr := NewManager()
	execTool := NewExecCommandToolWithMemoryFileStore(
		mgr,
		nil,
		store,
	).(tool.CallableTool)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			sessionpkg.NewSession("app", "u1", "wecom:chat:room-1"),
		),
	)
	ctx := agent.NewInvocationContext(
		conversationscope.WithStorageUserID(
			context.Background(),
			"wecom:chat:room-1",
		),
		inv,
	)

	args := mustJSON(t, map[string]any{
		"command": "printf %s \"$OPENCLAW_MEMORY_FILE\"",
		"yieldMs": 0,
	})
	out, err := execTool.Call(ctx, args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)

	path, err := store.MemoryPath("app", "wecom:chat:room-1")
	require.NoError(t, err)
	require.Contains(t, res.Output, path)
	require.FileExists(t, path)
}

func TestMemoryFileEnvFromContext_EmptyScopeReturnsNil(t *testing.T) {
	t.Parallel()

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			sessionpkg.NewSession("", "u1", "telegram:dm:u1:s1"),
		),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	require.Nil(t, memoryFileEnvFromContext(ctx, store))
}

func TestMemoryFileEnvFromContext_EnsureMemoryErrorReturnsNil(t *testing.T) {
	t.Parallel()

	rootFile := filepath.Join(t.TempDir(), "memory-root")
	require.NoError(t, os.WriteFile(rootFile, []byte("x"), 0o600))

	root, err := memoryfile.NewStore(rootFile)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			sessionpkg.NewSession("app", "u1", "telegram:dm:u1:s1"),
		),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	require.Nil(t, memoryFileEnvFromContext(ctx, root))
}

func TestAnnotateExecResult_ParsesMediaMarkers(t *testing.T) {
	t.Parallel()

	res := execResult{
		Status: "exited",
		Output: "done\nMEDIA: /tmp/a.png\n" +
			"MEDIA_DIR: /tmp/out frames\n" +
			"MEDIA: /tmp/a.png\n",
	}
	annotateExecResult(&res)
	require.Equal(t, []string{"/tmp/a.png"}, res.MediaFiles)
	require.Equal(t, []string{"/tmp/out frames"}, res.MediaDirs)
}

func TestMapPollResult_IncludesMediaMarkers(t *testing.T) {
	t.Parallel()

	code := 0
	out := mapPollResult("sess-1", processPoll{
		Status:     "exited",
		Output:     "MEDIA: page1.png\nMEDIA_DIR: out_pdf_split",
		Offset:     1,
		NextOffset: 3,
		ExitCode:   &code,
	})
	require.Equal(t, []string{"page1.png"}, out["media_files"])
	require.Equal(
		t,
		[]string{"out_pdf_split"},
		out["media_dirs"],
	)
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

	env := mergedEnv(nil, map[string]string{
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

func TestProcessTool_WriteRedactsSensitiveValueOutput(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(
		WithJobTTL(10*time.Second),
		WithOutputRedactor(NewChatCommandOutputRedactor()),
	)
	execTool := newExecCommandTool(mgr)
	writeTool := newWriteStdinTool(mgr)

	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command": "read -r x; printf %s 'sk-live-secret'",
			"env": map[string]string{
				"OPENAI_API_KEY": "sk-live-secret",
			},
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
	require.Contains(t, output, "[REDACTED:OPENAI_API_KEY]")
	require.NotContains(t, output, "sk-live-secret")

	logAny, err := mgr.log(res.SessionID, nil, nil)
	require.NoError(t, err)
	require.Contains(t, logAny.Output, "[REDACTED:OPENAI_API_KEY]")
	require.NotContains(t, logAny.Output, "sk-live-secret")
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

func TestExecToolDeclaration_HidesMemoryFileGuidanceWithoutStore(
	t *testing.T,
) {
	t.Parallel()

	decl := newExecCommandTool(NewManager()).Declaration()
	require.NotNil(t, decl)
	require.NotContains(t, decl.Description, envMemoryFile)
}

func TestExecToolDeclaration_ExposesMemoryFileGuidanceWithStore(
	t *testing.T,
) {
	t.Parallel()

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	decl := NewExecCommandToolWithMemoryFileStore(
		NewManager(),
		nil,
		store,
	).Declaration()
	require.NotNil(t, decl)
	require.Contains(t, decl.Description, envMemoryFile)
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

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := mgr.Exec(ctx, execParams{Command: "echo hi"})
	require.Error(t, err)

	_, err = mgr.Exec(context.Background(), execParams{})
	require.Error(t, err)
}

func TestManager_ExecSkipsShellSnapshotWithoutHooks(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager()
	mgr.shellEnvSnapshot = func(
		context.Context,
		string,
	) map[string]string {
		t.Fatal("unexpected shell env snapshot")
		return nil
	}

	yieldMs := 0
	out, err := mgr.Exec(context.Background(), execParams{
		Command: "echo ok",
		YieldMs: &yieldMs,
	})
	require.NoError(t, err)
	require.Equal(t, "exited", out.Status)
	require.Contains(t, out.Output, "ok")
}

func TestManager_CommandEnvUsesWorkdirSnapshot(t *testing.T) {
	mgr := NewManager(WithBaseEnv(map[string]string{
		"BASE_ONLY": "base",
		"SHARED":    "base",
		" ":         "skip",
	}))

	var gotWorkdir string
	mgr.shellEnvSnapshot = func(
		_ context.Context,
		workdir string,
	) map[string]string {
		gotWorkdir = workdir
		return map[string]string{
			"OPENAI_API_KEY": "sk-shell-secret",
			"SHARED":         "shell",
		}
	}

	env := mgr.commandEnv(
		context.Background(),
		"/tmp/work",
		map[string]string{
			"EXTRA_ONLY": "extra",
			"SHARED":     "extra",
			" ":          "skip",
		},
	)
	require.Equal(t, "/tmp/work", gotWorkdir)
	require.Equal(t, "sk-shell-secret", env["OPENAI_API_KEY"])
	require.Equal(t, "base", env["BASE_ONLY"])
	require.Equal(t, "extra", env["EXTRA_ONLY"])
	require.Equal(t, "extra", env["SHARED"])
	_, ok := env[" "]
	require.False(t, ok)
}

func TestManager_CommandEnvFallsBackToProcessEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-process-secret")

	mgr := NewManager()
	mgr.shellEnvSnapshot = func(
		context.Context,
		string,
	) map[string]string {
		return nil
	}

	env := mgr.commandEnv(
		context.Background(),
		"",
		map[string]string{
			"EXTRA_ONLY": "extra",
		},
	)
	require.Equal(t, "sk-process-secret", env["OPENAI_API_KEY"])
	require.Equal(t, "extra", env["EXTRA_ONLY"])
}

func TestMergeEnvMaps_EmptyInputsReturnNil(t *testing.T) {
	t.Parallel()

	require.Nil(t, mergeEnvMaps(nil, nil))
}

func TestEnvListToMap_IgnoresInvalidPairs(t *testing.T) {
	t.Parallel()

	require.Nil(t, envListToMap(nil))

	env := envListToMap([]string{
		"OPENAI_API_KEY=sk-test-secret",
		"EMPTY_VALUE=",
		"INVALID",
		"=MISSING_KEY",
		"",
	})
	require.Equal(t, "sk-test-secret", env["OPENAI_API_KEY"])
	require.Equal(t, "", env["EMPTY_VALUE"])
	_, ok := env[""]
	require.False(t, ok)
}

func TestBlocksSensitivePathValue_EmptyInput(t *testing.T) {
	t.Parallel()

	require.False(t, blocksSensitivePathValue("", nil, nil, nil))
}

func TestAppendProtectedPathDir_IgnoresRelativePath(t *testing.T) {
	t.Parallel()

	require.Empty(t, appendProtectedPathDir(nil, "env.sh"))
}

func TestManager_LoginShellEnvRespectsContextAndWorkdir(
	t *testing.T,
) {
	mgr := NewManager()
	calls := 0
	workdirs := make([]string, 0, 2)
	mgr.shellEnvSnapshot = func(
		ctx context.Context,
		workdir string,
	) map[string]string {
		calls++
		workdirs = append(workdirs, workdir)
		if calls == 1 {
			<-ctx.Done()
			return nil
		}
		return map[string]string{
			"OPENAI_API_KEY": "sk-test-secret",
		}
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan map[string]string, 1)
	go func() {
		done <- mgr.loginShellEnv(canceled, "/tmp/one")
	}()

	select {
	case env := <-done:
		require.Nil(t, env)
	case <-time.After(time.Second):
		t.Fatal("login shell env snapshot ignored request context")
	}

	env := mgr.loginShellEnv(context.Background(), "/tmp/two")
	require.Equal(t, "sk-test-secret", env["OPENAI_API_KEY"])
	require.Equal(t, 2, calls)
	require.Equal(t, []string{"/tmp/one", "/tmp/two"}, workdirs)
}

func TestUploadEnvFromContext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "report.pdf")
	audioPath := filepath.Join(dir, "clip.ogg")
	videoPath := filepath.Join(dir, "movie.mp4")
	imagePath := filepath.Join(dir, "frame.png")
	require.NoError(t, os.WriteFile(
		filePath,
		[]byte("pdf"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		audioPath,
		[]byte("ogg"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		videoPath,
		[]byte("mp4"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		imagePath,
		[]byte("png"),
		0o600,
	))

	userMsg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{
				Type: model.ContentTypeFile,
				File: &model.File{
					Name:     "report.pdf",
					FileID:   "host://" + filePath,
					MimeType: "application/pdf",
				},
			},
			{
				Type: model.ContentTypeFile,
				File: &model.File{
					Name:     "frame.png",
					FileID:   "host://" + imagePath,
					MimeType: "image/png",
				},
			},
		},
	}
	currentMsg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{
				Type: model.ContentTypeFile,
				File: &model.File{
					Name:     "clip.ogg",
					FileID:   "host://" + audioPath,
					MimeType: "audio/ogg",
				},
			},
			{
				Type: model.ContentTypeFile,
				File: &model.File{
					Name:     "movie.mp4",
					FileID:   "host://" + videoPath,
					MimeType: "video/mp4",
				},
			},
		},
	}
	ev := event.NewResponseEvent("inv", "user", &model.Response{
		Choices: []model.Choice{{Message: userMsg}},
	})
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(currentMsg),
		agent.WithInvocationSession(
			&sessionpkg.Session{
				Events: []event.Event{*ev},
			},
		),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	env := uploadEnvFromContext(ctx, nil)
	require.Equal(t, videoPath, env[envLastUploadPath])
	require.Equal(t, uploads.HostRef(videoPath), env[envLastUploadHostRef])
	require.Equal(t, dir, env[envSessionUploadsDir])
	require.Equal(t, "movie.mp4", env[envLastUploadName])
	require.Equal(
		t,
		"video/mp4",
		env[envLastUploadMIME],
	)
	require.Equal(t, audioPath, env[envLastAudioPath])
	require.Equal(t, uploads.HostRef(audioPath), env[envLastAudioHostRef])
	require.Equal(t, "clip.ogg", env[envLastAudioName])
	require.Equal(t, "audio/ogg", env[envLastAudioMIME])
	require.Equal(t, videoPath, env[envLastVideoPath])
	require.Equal(t, uploads.HostRef(videoPath), env[envLastVideoHostRef])
	require.Equal(t, "movie.mp4", env[envLastVideoName])
	require.Equal(t, "video/mp4", env[envLastVideoMIME])
	require.Equal(t, imagePath, env[envLastImagePath])
	require.Equal(t, uploads.HostRef(imagePath), env[envLastImageHostRef])
	require.Equal(t, "frame.png", env[envLastImageName])
	require.Equal(t, "image/png", env[envLastImageMIME])
	require.Equal(t, filePath, env[envLastPDFPath])
	require.Equal(t, uploads.HostRef(filePath), env[envLastPDFHostRef])
	require.Equal(t, "report.pdf", env[envLastPDFName])
	require.Equal(
		t,
		"application/pdf",
		env[envLastPDFMIME],
	)

	var recent []execUploadMeta
	require.NoError(
		t,
		json.Unmarshal([]byte(env[envRecentUploadsJSON]), &recent),
	)
	require.Len(t, recent, 4)
	require.Equal(t, videoPath, recent[0].Path)
	require.Equal(t, uploadKindVideo, recent[0].Kind)
	require.Equal(t, audioPath, recent[1].Path)
	require.Equal(t, uploadKindAudio, recent[1].Kind)
	require.Equal(t, imagePath, recent[2].Path)
	require.Equal(t, uploadKindImage, recent[2].Kind)
	require.Equal(t, filePath, recent[3].Path)
	require.Equal(t, uploadKindPDF, recent[3].Kind)
}

func TestUploadEnvFromContext_UsesUploadStore(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	scope := uploads.Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "telegram:dm:u1:s1",
	}
	derived, err := store.SaveWithInfo(
		context.Background(),
		scope,
		"split-page-3.pdf",
		uploads.FileMetadata{
			MimeType: "application/pdf",
			Source:   uploads.SourceDerived,
		},
		[]byte("%PDF-1.4"),
	)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			sessionpkg.NewSession(
				"app",
				"u1",
				"telegram:dm:u1:s1",
			),
		),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	env := uploadEnvFromContext(ctx, store)
	require.Equal(t, derived.Path, env[envLastUploadPath])
	require.Equal(t, derived.HostRef, env[envLastUploadHostRef])
	require.Equal(t, derived.Path, env[envLastPDFPath])
	require.Equal(t, derived.HostRef, env[envLastPDFHostRef])
	require.Equal(
		t,
		filepath.Dir(derived.Path),
		env[envSessionUploadsDir],
	)

	var recent []execUploadMeta
	require.NoError(
		t,
		json.Unmarshal([]byte(env[envRecentUploadsJSON]), &recent),
	)
	require.Len(t, recent, 1)
	require.Equal(t, derived.Path, recent[0].Path)
	require.Equal(t, uploadKindPDF, recent[0].Kind)
	require.Equal(t, uploads.SourceDerived, recent[0].Source)
}

func TestUploadEnvFromContext_RewritesGeneratedUploadNames(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	videoPath := filepath.Join(dir, "file_10.mp4")
	require.NoError(t, os.WriteFile(videoPath, []byte("mp4"), 0o600))

	msg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{{
			Type: model.ContentTypeFile,
			File: &model.File{
				Name:     "file_10.mp4",
				FileID:   "host://" + videoPath,
				MimeType: "video/mp4",
			},
		}},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(msg),
		agent.WithInvocationSession(
			sessionpkg.NewSession("app", "u1", "telegram:dm:u1:s1"),
		),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	env := uploadEnvFromContext(ctx, nil)
	require.Equal(t, "video.mp4", env[envLastUploadName])
	require.Equal(t, "video.mp4", env[envLastVideoName])

	var recent []execUploadMeta
	require.NoError(
		t,
		json.Unmarshal([]byte(env[envRecentUploadsJSON]), &recent),
	)
	require.Len(t, recent, 1)
	require.Equal(t, "video.mp4", recent[0].Name)
}

func TestUploadEnvFromContext_UsesSessionDirWithoutRecentUploads(
	t *testing.T,
) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			sessionpkg.NewSession(
				"app",
				"u1",
				"telegram:dm:u1:s1",
			),
		),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	env := uploadEnvFromContext(ctx, store)
	require.Equal(
		t,
		store.ScopeDir(uploads.Scope{
			Channel:   "telegram",
			UserID:    "u1",
			SessionID: "telegram:dm:u1:s1",
		}),
		env[envSessionUploadsDir],
	)
	require.NotContains(t, env, envLastUploadPath)
}

func TestUploadKindFromMeta(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		uploadKindImage,
		uploadKindFromMeta("frame.png", ""),
	)
	require.Equal(
		t,
		uploadKindAudio,
		uploadKindFromMeta("voice.bin", "audio/ogg"),
	)
	require.Equal(
		t,
		uploadKindVideo,
		uploadKindFromMeta("clip.mp4", ""),
	)
	require.Equal(
		t,
		uploadKindPDF,
		uploadKindFromMeta("report.pdf", ""),
	)
	require.Equal(
		t,
		uploadKindFile,
		uploadKindFromMeta("archive.bin", ""),
	)
}

func TestMergeExecEnv_PreservesExplicitEnv(t *testing.T) {
	t.Parallel()

	merged := mergeExecEnv(
		map[string]string{envLastUploadPath: "explicit"},
		map[string]string{
			envLastUploadPath: "derived",
			envLastUploadName: "report.pdf",
			envLastUploadMIME: "application/pdf",
		},
	)
	require.Equal(t, "explicit", merged[envLastUploadPath])
	require.Equal(t, "report.pdf", merged[envLastUploadName])
	require.Equal(
		t,
		"application/pdf",
		merged[envLastUploadMIME],
	)
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
