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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/conversationscope"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
	"trpc.group/trpc-go/trpc-agent-go/plugin/identity"
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

func newSandboxExecCommandTool(engine codeexecutor.Engine) tool.CallableTool {
	return NewSandboxExecCommandTool(engine).(tool.CallableTool)
}

func TestSandboxExecToolDescription(t *testing.T) {
	t.Parallel()

	withoutMemory := sandboxExecToolDescription(false)
	require.Contains(t, withoutMemory, "inside the configured sandbox")
	require.Contains(
		t,
		withoutMemory,
		"Only foreground non-interactive commands are supported",
	)
	require.NotContains(
		t,
		withoutMemory,
		"host paths are not automatically mounted into the sandbox",
	)

	withMemory := sandboxExecToolDescription(true)
	require.Contains(
		t,
		withMemory,
		"host paths are not automatically mounted into the sandbox",
	)
}

func TestExecToolDescriptionMentionsBackgroundForPersistentProcesses(
	t *testing.T,
) {
	t.Parallel()

	desc := execToolDescription(false)
	require.Contains(t, desc, "Foreground commands clean up child jobs")
	require.Contains(t, desc, "`background: true`")
	require.Contains(t, desc, "later tools must keep using them")
}

func TestNewSandboxExecCommandToolWithMemoryFileStore_WiresRegistry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	tool := NewSandboxExecCommandToolWithMemoryFileStore(
		&fakeSandboxExecEngine{},
		nil,
		store,
	)
	sandboxTool, ok := tool.(*sandboxExecTool)
	require.True(t, ok)
	require.Same(t, store, sandboxTool.memoryStore)
	require.NotNil(t, sandboxTool.registry)
}

func TestSandboxExecTool_WorkspaceFallsBackWithoutRegistry(t *testing.T) {
	t.Parallel()

	engine := &fakeSandboxExecEngine{}
	tl := &sandboxExecTool{engine: engine}
	ctx := agent.NewInvocationContext(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationSession(
				sessionpkg.NewSession("app", "u1", "s1"),
			),
		),
	)

	ws, err := tl.workspace(ctx)
	require.NoError(t, err)
	require.Equal(t, "app/u1/s1", ws.ID)
	require.Equal(t, "app/u1/s1", ws.Path)
	require.Len(t, engine.manager.workspaces, 1)
	require.Equal(t, ws, engine.manager.workspaces[0])
}

func TestSandboxExecTool_Foreground(t *testing.T) {
	t.Parallel()

	engine := &fakeSandboxExecEngine{
		runResult: codeexecutor.RunResult{
			Stdout:   "hello\n",
			Stderr:   "warn\n",
			ExitCode: 7,
		},
	}
	tl := newSandboxExecCommandTool(engine)

	out, err := tl.Call(context.Background(), mustJSON(t, map[string]any{
		"command":     "echo hello",
		"workdir":     "subdir",
		"timeout_sec": 3,
		"env": map[string]string{
			"OPENCLAW_TEST_ENV": "ok",
		},
	}))
	require.NoError(t, err)
	res := out.(execResult)
	require.Equal(t, "exited", res.Status)
	require.Equal(t, "hello\nwarn\n", res.Output)
	require.Equal(t, 7, res.ExitCode)

	require.Len(t, engine.runner.specs, 1)
	spec := engine.runner.specs[0]
	require.Equal(t, shellProgram, spec.Cmd)
	require.Equal(t, []string{shellLoginFlag, "echo hello"}, spec.Args)
	require.Equal(t, "subdir", spec.Cwd)
	require.Equal(t, 3*time.Second, spec.Timeout)
	require.Equal(t, "ok", spec.Env["OPENCLAW_TEST_ENV"])
	require.Len(t, engine.manager.workspaces, 1)
}

func TestSandboxExecTool_AppliesCommandPolicy(t *testing.T) {
	t.Parallel()

	engine := &fakeSandboxExecEngine{}
	tl := NewSandboxExecCommandToolWithPolicy(
		engine,
		nil,
		nil,
		NewChatCommandSafetyPolicy(),
		nil,
	).(tool.CallableTool)

	_, err := tl.Call(context.Background(), mustJSON(t, map[string]any{
		"command": "cat ~/.ssh/id_rsa",
	}))
	require.ErrorContains(t, err, reasonSensitivePath)
	require.Empty(t, engine.runner.specs)
	require.Empty(t, engine.manager.workspaces)
}

func TestSandboxExecTool_RedactsSensitiveEnvValueOutput(t *testing.T) {
	t.Parallel()

	engine := &fakeSandboxExecEngine{
		runResult: codeexecutor.RunResult{
			Stdout:   "token=sk-test-secret\n",
			ExitCode: 0,
		},
	}
	tl := NewSandboxExecCommandToolWithPolicy(
		engine,
		nil,
		nil,
		nil,
		NewChatCommandOutputRedactor(),
	).(tool.CallableTool)

	out, err := tl.Call(context.Background(), mustJSON(t, map[string]any{
		"command": "echo \"$OPENAI_API_KEY\"",
		"env": map[string]string{
			"OPENAI_API_KEY": "sk-test-secret",
		},
	}))
	require.NoError(t, err)
	res := out.(execResult)
	require.Contains(t, res.Output, redactedValue)
	require.NotContains(t, res.Output, "sk-test-secret")
}

func TestSandboxExecTool_ReusesWorkspacePerSession(t *testing.T) {
	t.Parallel()

	engine := &fakeSandboxExecEngine{}
	tl := newSandboxExecCommandTool(engine)
	ctx := agent.NewInvocationContext(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationSession(
				sessionpkg.NewSession("app", "u1", "s1"),
			),
		),
	)

	for i := 0; i < 2; i++ {
		_, err := tl.Call(ctx, mustJSON(t, map[string]any{
			"command": "pwd",
		}))
		require.NoError(t, err)
	}

	require.Len(t, engine.manager.workspaces, 1)
	require.Equal(t, "app/u1/s1", engine.manager.workspaces[0].ID)
}

func TestSandboxExecTool_IsolatesWorkspacesAcrossSessions(t *testing.T) {
	t.Parallel()

	engine := &fakeSandboxExecEngine{}
	tl := newSandboxExecCommandTool(engine)
	ctx1 := agent.NewInvocationContext(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationSession(
				sessionpkg.NewSession("app", "u1", "s1"),
			),
		),
	)
	ctx2 := agent.NewInvocationContext(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationSession(
				sessionpkg.NewSession("app", "u1", "s2"),
			),
		),
	)

	_, err := tl.Call(ctx1, mustJSON(t, map[string]any{
		"command": "pwd",
	}))
	require.NoError(t, err)
	_, err = tl.Call(ctx2, mustJSON(t, map[string]any{
		"command": "pwd",
	}))
	require.NoError(t, err)

	require.Len(t, engine.manager.workspaces, 2)
	require.Equal(t, "app/u1/s1", engine.manager.workspaces[0].ID)
	require.Equal(t, "app/u1/s2", engine.manager.workspaces[1].ID)
}

func TestSandboxExecTool_RejectsUnsupportedSessionModes(t *testing.T) {
	t.Parallel()

	tl := newSandboxExecCommandTool(&fakeSandboxExecEngine{})
	cases := []map[string]any{
		{"command": "sleep 1", "background": true},
		{"command": "vim", "tty": true},
		{"command": "vim", "pty": true},
		{"command": "sleep 1", "yield_time_ms": 10},
		{"command": "pwd", "workdir": "/tmp"},
	}
	for _, args := range cases {
		_, err := tl.Call(context.Background(), mustJSON(t, args))
		require.Error(t, err)
		require.Contains(t, err.Error(), errSandboxExecUnsupported)
	}
}

func TestSandboxExecTool_TimeoutResult(t *testing.T) {
	t.Parallel()

	tl := newSandboxExecCommandTool(&fakeSandboxExecEngine{
		runResult: codeexecutor.RunResult{
			Stderr:   "timed out\n",
			ExitCode: 124,
			TimedOut: true,
		},
	})
	out, err := tl.Call(context.Background(), mustJSON(t, map[string]any{
		"command":     "sleep 10",
		"timeout_sec": 1,
	}))
	require.NoError(t, err)
	res := out.(execResult)
	require.Equal(t, "timeout", res.Status)
	require.Equal(t, 124, res.ExitCode)
	require.Contains(t, res.Output, "timed out")
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

func TestExecTool_UsesManagerDefaultTimeout(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithDefaultTimeout(50 * time.Millisecond))
	tool := newExecCommandTool(mgr)

	started := time.Now()
	out, err := tool.Call(context.Background(), mustJSON(t, map[string]any{
		"command": "sleep 1; printf done",
		"yieldMs": 0,
	}))
	require.NoError(t, err)
	require.Less(t, time.Since(started), 900*time.Millisecond)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)
	require.NotEqual(t, 0, res.ExitCode)
	require.NotContains(t, res.Output, "done")
}

func TestExecTool_MaxTimeoutCapsRequestedTimeout(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithMaxTimeout(50 * time.Millisecond))
	tool := newExecCommandTool(mgr)

	started := time.Now()
	out, err := tool.Call(context.Background(), mustJSON(t, map[string]any{
		"command":     "sleep 1; printf done",
		"yieldMs":     0,
		"timeout_sec": 5,
	}))
	require.NoError(t, err)
	require.Less(t, time.Since(started), 900*time.Millisecond)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)
	require.NotEqual(t, 0, res.ExitCode)
	require.NotContains(t, res.Output, "done")
}

func TestManagerTimeoutAndYieldBranches(t *testing.T) {
	t.Parallel()

	defaultMgr := NewManager()
	require.Equal(
		t,
		time.Duration(defaultTimeoutS)*time.Second,
		defaultMgr.commandTimeout(nil),
	)
	zeroMgr := &Manager{}
	require.Equal(
		t,
		time.Duration(defaultTimeoutS)*time.Second,
		zeroMgr.commandTimeout(nil),
	)

	requested := 1
	cappedMgr := NewManager(WithMaxTimeout(5 * time.Second))
	require.Equal(t, time.Second, cappedMgr.commandTimeout(&requested))

	yieldMgr := NewManager(WithMaxYield(2 * time.Second))
	require.Equal(t, 1000, yieldMgr.clampYieldMs(1000))

	tinyYieldMgr := NewManager(WithMaxYield(time.Nanosecond))
	require.Equal(t, 1, tinyYieldMgr.clampYieldMs(5000))

	var nilMgr *Manager
	require.Equal(t, 10, nilMgr.clampYieldMs(10))
}

func TestManagerStartBackgroundUsesBackgroundForNilParentContext(
	t *testing.T,
) {
	if _, err := exec.LookPath(shellProgram); err != nil {
		t.Skip("shell is not available")
	}

	mgr := NewManager()
	sess, err := mgr.startBackground(
		nil,
		execParams{Command: "printf ok"},
		time.Second,
		nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = mgr.kill(sess.id)
	})

	require.Eventually(t, func() bool {
		poll, err := mgr.poll(sess.id, nil)
		return err == nil && poll.Status == "exited"
	}, 3*time.Second, 20*time.Millisecond)
}

func TestManagerStartBackgroundRejectsCanceledParentContext(t *testing.T) {
	mgr := NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sess, err := mgr.startBackground(
		ctx,
		execParams{Command: "printf unexpected"},
		time.Second,
		nil,
	)
	require.Nil(t, sess)
	require.ErrorIs(t, err, context.Canceled)
}

func TestManagerStartBackgroundStopsCancellationOnStartFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty and process startup behavior is unix-specific")
	}

	for _, usePTY := range []bool{false, true} {
		t.Run(fmt.Sprintf("pty=%t", usePTY), func(t *testing.T) {
			mgr := NewManager()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sess, err := mgr.startBackground(
				ctx,
				execParams{
					Command: "printf unexpected",
					Workdir: filepath.Join(t.TempDir(), "missing"),
					Pty:     usePTY,
				},
				time.Second,
				nil,
			)
			require.Nil(t, sess)
			require.Error(t, err)
		})
	}
}

func TestExecTool_RuntimeProfileWorkspacePolicy(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	denied := filepath.Join(root, "denied")
	require.NoError(t, os.MkdirAll(allowed, 0o755))
	require.NoError(t, os.MkdirAll(denied, 0o755))

	ctx := runtimeprofile.WithProfile(
		context.Background(),
		runtimeprofile.Profile{
			Workspace: runtimeprofile.WorkspacePolicy{
				Workdir:      allowed,
				AllowedRoots: []string{allowed},
			},
		},
	)
	mgr := NewManager()
	tool := newExecCommandTool(mgr)

	out, err := tool.Call(ctx, mustJSON(t, map[string]any{
		"command": "pwd",
	}))
	require.NoError(t, err)
	res := out.(execResult)
	require.Contains(t, filepath.Clean(res.Output), allowed)

	_, err = tool.Call(ctx, mustJSON(t, map[string]any{
		"command": "pwd",
		"workdir": denied,
	}))
	require.ErrorIs(t, err, runtimeprofile.ErrWorkspaceDenied)
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

func TestExecTool_CleanShellStartupSkipsBashEnv(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	dir := t.TempDir()
	bashEnv := filepath.Join(dir, "bash-env.sh")
	require.NoError(t, os.WriteFile(
		bashEnv,
		[]byte("printf 'startup-noise\\n'\n"),
		0o600,
	))
	t.Setenv("BASH_ENV", bashEnv)

	mgr := NewManager(WithCleanShellStartup(true))
	tool := newExecCommandTool(mgr)

	args := mustJSON(t, map[string]any{
		"command": "printf clean",
		"yieldMs": 0,
	})
	out, err := tool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)
	require.Equal(t, "clean", res.Output)
}

func TestExecTool_UsesIdentityEnvFromContext(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager()
	tool := newExecCommandTool(mgr)

	ctx := identity.NewContext(context.Background(), &identity.Identity{
		EnvVars: map[string]string{
			"OPENCLAW_IDENTITY_ENV":   "ctx-value",
			"OPENCLAW_CONTEXT_TARGET": "from-context",
		},
	})

	args := mustJSON(t, map[string]any{
		"command": "printf '%s|%s' \"$OPENCLAW_IDENTITY_ENV\" \"$OPENCLAW_CONTEXT_TARGET\"",
		"env": map[string]string{
			"OPENCLAW_CONTEXT_TARGET": "explicit",
		},
		"yieldMs": 0,
	})
	out, err := tool.Call(ctx, args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)
	require.Contains(t, strings.TrimSpace(res.Output), "ctx-value|explicit")
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

func TestChatCommandSafetyPolicyAllowsStateScratchWorkdir(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	policy := NewChatCommandSafetyPolicy()
	err := policy(context.Background(), CommandRequest{
		Command: "pwd",
		Workdir: filepath.Join(stateDir, "workspaces", "scratch"),
		Env: map[string]string{
			envTRPCClawStateDir: stateDir,
		},
	})
	require.NoError(t, err)
}

func TestChatCommandSafetyPolicyBlocksRuntimeEnvWorkdir(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	policy := NewChatCommandSafetyPolicy()
	err := policy(context.Background(), CommandRequest{
		Command: "pwd",
		Workdir: filepath.Join(stateDir, "runtime"),
		Env: map[string]string{
			envTRPCClawStateDir: stateDir,
		},
	})
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
WECOM_ENCODING_AES_KEY=wecom-aes-secret
SERVICE_CREDENTIAL=service-credential-secret
SAFE_NAME=ok
"OPENAI_API_KEY": "sk-test-secret",
"WECOM_ENCODING_AES_KEY": "wecom-aes-secret",
'`,
		"yieldMs": 0,
	})
	out, err := tool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Contains(t, res.Output, "OPENAI_API_KEY=[REDACTED]")
	require.Contains(t, res.Output, "WECOM_ENCODING_AES_KEY=[REDACTED]")
	require.Contains(t, res.Output, "SERVICE_CREDENTIAL=[REDACTED]")
	require.Contains(t, res.Output, `OPENAI_API_KEY": "[REDACTED]"`)
	require.Contains(t, res.Output, `WECOM_ENCODING_AES_KEY": "[REDACTED]"`)
	require.Contains(t, res.Output, "SAFE_NAME=ok")
	require.NotContains(t, res.Output, "sk-test-secret")
	require.NotContains(t, res.Output, "wecom-aes-secret")
	require.NotContains(t, res.Output, "service-credential-secret")
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
		"command": "printf '%s\\n%s\\n%s' " +
			"\"$OPENCLAW_MEMORY_FILE\" " +
			"\"$OPENCLAW_USER_MEMORY_FILE\" " +
			"\"$OPENCLAW_CHAT_MEMORY_FILE\"",
		"yieldMs": 0,
	})
	out, err := execTool.Call(ctx, args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)

	path, err := store.MemoryPath("app", "u1")
	require.NoError(t, err)
	require.Contains(t, res.Output, path)
	require.Equal(t, strings.Count(res.Output, path), 3)
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
			sessionpkg.NewSession(
				"app",
				"wecom:dm:wineguo",
				"wecom:chat:room-1",
			),
		),
	)
	ctx := agent.NewInvocationContext(
		conversationscope.WithStorageUserID(
			conversationscope.WithUserStorageID(
				context.Background(),
				"wecom:dm:T123",
			),
			"wecom:chat:room-1",
		),
		inv,
	)

	args := mustJSON(t, map[string]any{
		"command": "printf '%s\\n%s\\n%s' " +
			"\"$OPENCLAW_MEMORY_FILE\" " +
			"\"$OPENCLAW_USER_MEMORY_FILE\" " +
			"\"$OPENCLAW_CHAT_MEMORY_FILE\"",
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
	userPath, err := store.MemoryPath("app", "wecom:dm:T123")
	require.NoError(t, err)
	require.Contains(t, res.Output, userPath)
	require.NotContains(t, res.Output, "wecom:dm:wineguo")
	require.FileExists(t, userPath)
}

func TestExecTool_UsesUserStorageScopedMemoryFileEnvFallback(
	t *testing.T,
) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	const (
		appName       = "app"
		sessionUserID = "wecom:dm:wineguo"
		storageUserID = "wecom:dm:T123"
		sessionID     = "wecom:dm:wineguo:s1"
	)

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
			sessionpkg.NewSession(appName, sessionUserID, sessionID),
		),
	)
	ctx := agent.NewInvocationContext(
		conversationscope.WithUserStorageID(
			context.Background(),
			storageUserID,
		),
		inv,
	)

	args := mustJSON(t, map[string]any{
		"command": "printf '%s\\n%s\\n%s' " +
			"\"$OPENCLAW_MEMORY_FILE\" " +
			"\"$OPENCLAW_USER_MEMORY_FILE\" " +
			"\"$OPENCLAW_CHAT_MEMORY_FILE\"",
		"yieldMs": 0,
	})
	out, err := execTool.Call(ctx, args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)

	path, err := store.MemoryPath(appName, storageUserID)
	require.NoError(t, err)
	require.Contains(t, res.Output, path)
	require.Equal(t, strings.Count(res.Output, path), 3)
	require.NotContains(t, res.Output, sessionUserID)
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

func TestMemoryFileEnvFromContext_CanceledContextReturnsNil(t *testing.T) {
	t.Parallel()

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			sessionpkg.NewSession("app", "u1", "telegram:dm:u1:s1"),
		),
	)
	baseCtx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx := agent.NewInvocationContext(baseCtx, inv)

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

func TestExecTool_YieldSessionSurvivesCallerContextCancel(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10 * time.Second))
	execTool := newExecCommandTool(mgr)
	ctx, cancel := context.WithCancel(context.Background())

	args := mustJSON(t, map[string]any{
		"command": "printf 'start\\n'; sleep 0.2; printf 'end\\n'",
		"yieldMs": 10,
	})
	out, err := execTool.Call(ctx, args)
	require.NoError(t, err)
	cancel()

	res := out.(execResult)
	require.Equal(t, "running", res.Status)
	require.NotEmpty(t, res.SessionID)

	var all string
	require.Eventually(t, func() bool {
		poll, err := mgr.poll(res.SessionID, nil)
		require.NoError(t, err)
		if poll.Output != "" {
			all += "\n" + poll.Output
		}
		if poll.Status != "exited" {
			return false
		}
		require.NotNil(t, poll.ExitCode)
		require.Equal(t, 0, *poll.ExitCode)
		return strings.Contains(all, "start") &&
			strings.Contains(all, "end")
	}, 2*time.Second, 20*time.Millisecond, "output: %s", all)
}

func TestExecTool_CancelAtSessionHandoffLeavesNoRunningSession(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	marker := filepath.Join(t.TempDir(), "orphan-marker")
	mgr := NewManager(WithJobTTL(10 * time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	mgr.beforeSessionHandoff = cancel

	res, err := mgr.Exec(ctx, execParams{
		Command:    "sleep 0.3; echo orphan > " + strconv.Quote(marker),
		Background: true,
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, res.SessionID)

	require.Eventually(t, func() bool {
		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		for _, sess := range mgr.sessions {
			if sess.running() {
				return false
			}
		}
		return true
	}, time.Second, 20*time.Millisecond)
	require.Never(t, func() bool {
		_, statErr := os.Stat(marker)
		return statErr == nil
	}, 500*time.Millisecond, 20*time.Millisecond)
}

func TestExecTool_CancelAtYieldHandoffLeavesNoRunningSession(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10 * time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	mgr.beforeSessionHandoff = cancel
	yieldMs := 10

	res, err := mgr.Exec(ctx, execParams{
		Command: "sleep 5",
		YieldMs: &yieldMs,
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, res.SessionID)

	require.Eventually(t, func() bool {
		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		return len(mgr.sessions) == 0
	}, time.Second, 20*time.Millisecond)
}

func TestExecTool_CancelWhilePreparingYieldResultKillsSession(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	redactorEntered := make(chan struct{})
	releaseRedactor := make(chan struct{})
	mgr := NewManager(
		WithJobTTL(10*time.Second),
		WithCleanShellStartup(true),
		WithOutputRedactor(func(_ CommandRequest, output string) string {
			close(redactorEntered)
			<-releaseRedactor
			return output
		}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	yieldMs := 500

	type execOutcome struct {
		result execResult
		err    error
	}
	done := make(chan execOutcome, 1)
	go func() {
		result, err := mgr.Exec(ctx, execParams{
			Command: "printf 'ready\\n'; sleep 5",
			YieldMs: &yieldMs,
		})
		done <- execOutcome{result: result, err: err}
	}()

	select {
	case <-redactorEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("output redactor was not called")
	}
	cancel()
	close(releaseRedactor)

	outcome := <-done
	require.ErrorIs(t, outcome.err, context.Canceled)
	require.Empty(t, outcome.result.SessionID)
	require.Eventually(t, func() bool {
		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		return len(mgr.sessions) == 0
	}, time.Second, 20*time.Millisecond)
}

func TestCommitRunningSession_RechecksCanceledParentAfterDetach(
	t *testing.T,
) {
	t.Parallel()

	mgr := NewManager()
	sess := newSession("handoff-race", "sleep", defaultMaxLines)
	stopCalled := false
	sess.parentCancelStop = func() bool {
		stopCalled = true
		return true
	}
	sess.cancel = func() {
		sess.markDone(-1)
	}
	mgr.sessions[sess.id] = sess

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := mgr.commitRunningSession(ctx, sess)
	require.ErrorIs(t, err, context.Canceled)
	require.True(t, stopCalled)
	mgr.mu.Lock()
	_, exists := mgr.sessions[sess.id]
	mgr.mu.Unlock()
	require.False(t, exists)
}

func TestCommitRunningSession_RejectsCompletedCancellationWithoutParent(
	t *testing.T,
) {
	t.Parallel()

	mgr := NewManager()
	sess := newSession("completed-cancel", "sleep", defaultMaxLines)
	sess.parentCancelStop = func() bool { return false }
	sess.cancel = func() {
		sess.markDone(-1)
	}
	mgr.sessions[sess.id] = sess

	err := mgr.commitRunningSession(nil, sess)
	require.ErrorIs(t, err, context.Canceled)
	mgr.mu.Lock()
	_, exists := mgr.sessions[sess.id]
	mgr.mu.Unlock()
	require.False(t, exists)
}

func TestSessionDetachParentCancellationWithoutParent(t *testing.T) {
	t.Parallel()

	sess := newSession("no-parent", "printf ok", defaultMaxLines)
	require.True(t, sess.detachParentCancellation())
}

func TestExecTool_DefaultTimeoutKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group signaling is unix-specific")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	marker := filepath.Join(t.TempDir(), "orphan-marker")
	mgr := NewManager(WithDefaultTimeout(100 * time.Millisecond))
	yieldZero := 0

	res, err := mgr.Exec(context.Background(), execParams{
		Command: "(sleep 0.6; echo orphan > " +
			strconv.Quote(marker) + ") & wait",
		YieldMs: &yieldZero,
	})
	require.NoError(t, err)
	require.NotEqual(t, 0, res.ExitCode)

	require.Never(t, func() bool {
		_, err = os.Stat(marker)
		if err == nil {
			return true
		}
		require.ErrorIs(t, err, os.ErrNotExist)
		return false
	}, 900*time.Millisecond, 20*time.Millisecond)
}

func TestExecTool_NormalExitCleansBackgroundProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group signaling is unix-specific")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	marker := filepath.Join(t.TempDir(), "orphan-marker")
	mgr := NewManager()

	res, err := mgr.Exec(context.Background(), execParams{
		Command: "(sleep 0.4; echo orphan > " +
			strconv.Quote(marker) + ") >/dev/null 2>&1 & echo done",
	})
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode)
	require.Contains(t, res.Output, "done")

	require.Never(t, func() bool {
		_, err = os.Stat(marker)
		if err == nil {
			return true
		}
		require.ErrorIs(t, err, os.ErrNotExist)
		return false
	}, 800*time.Millisecond, 20*time.Millisecond)
}

func TestExecTool_SessionExitCleansExecBypassedTrap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group signaling is unix-specific")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	marker := filepath.Join(t.TempDir(), "orphan-marker")
	mgr := NewManager(WithMaxYield(time.Second))
	yieldMs := 1000

	res, err := mgr.Exec(context.Background(), execParams{
		Command: "exec bash -c " + strconv.Quote(
			"(sleep 0.4; echo orphan > "+
				strconv.Quote(marker)+") >/dev/null 2>&1 & echo done",
		),
		YieldMs: &yieldMs,
	})
	require.NoError(t, err)
	output := res.Output
	switch res.Status {
	case "exited":
		require.Equal(t, 0, res.ExitCode)
	case "running":
		require.NotEmpty(t, res.SessionID)
		t.Cleanup(func() {
			_ = mgr.kill(res.SessionID)
		})
		output += pollUntilExited(t, mgr, res.SessionID)
	default:
		t.Fatalf("unexpected status: %s", res.Status)
	}
	require.Contains(t, output, "done")

	require.Never(t, func() bool {
		_, err = os.Stat(marker)
		if err == nil {
			return true
		}
		require.ErrorIs(t, err, os.ErrNotExist)
		return false
	}, 800*time.Millisecond, 20*time.Millisecond)
}

func TestExecTool_KillKillsYieldSessionProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group signaling is unix-specific")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	marker := filepath.Join(dir, "orphan-marker")
	mgr := NewManager(WithJobTTL(10 * time.Second))
	yieldMs := 10
	timeoutS := 30
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	res, err := mgr.Exec(ctx, execParams{
		Command: "(sleep 0.7; echo orphan > " +
			strconv.Quote(marker) + ") & echo ready > " +
			strconv.Quote(ready) + "; wait",
		YieldMs:  &yieldMs,
		TimeoutS: &timeoutS,
	})
	require.NoError(t, err)
	require.Equal(t, "running", res.Status)
	require.NotEmpty(t, res.SessionID)

	require.Eventually(t, func() bool {
		_, err := os.Stat(ready)
		return err == nil
	}, time.Second, 20*time.Millisecond)

	require.NoError(t, mgr.kill(res.SessionID))
	pollUntilExited(t, mgr, res.SessionID)
	require.Never(t, func() bool {
		_, err = os.Stat(marker)
		if err == nil {
			return true
		}
		require.ErrorIs(t, err, os.ErrNotExist)
		return false
	}, time.Second, 20*time.Millisecond)
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

func TestWriteStdinTool_MaxYieldCapsRequestedWait(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(
		WithJobTTL(10*time.Second),
		WithMaxYield(20*time.Millisecond),
	)
	execTool := newExecCommandTool(mgr)
	writeTool := newWriteStdinTool(mgr)

	out, err := execTool.Call(context.Background(), mustJSON(t, map[string]any{
		"command":    "sleep 1",
		"background": true,
	}))
	require.NoError(t, err)
	res := out.(execResult)
	t.Cleanup(func() {
		_ = mgr.kill(res.SessionID)
	})

	started := time.Now()
	_, err = writeTool.Call(context.Background(), mustJSON(t, map[string]any{
		"session_id":     res.SessionID,
		"yield_time_ms":  5_000,
		"append_newline": false,
	}))
	require.NoError(t, err)
	require.Less(t, time.Since(started), 500*time.Millisecond)
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

func TestManager_MaxResultOutputCharsTruncatesForeground(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	const maxResultOutputChars = 80
	mgr := NewManager(WithMaxResultOutputChars(maxResultOutputChars))
	execTool := newExecCommandTool(mgr)

	args := mustJSON(t, map[string]any{
		"command":       "printf 'abcdefghijklmnopqrstuvwxyz%.0s' {1..8}",
		"yield_time_ms": 0,
	})
	out, err := execTool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)
	prefix, _, ok := strings.Cut(res.Output, "\n\n[OpenClaw")
	require.True(t, ok)
	require.Equal(t, maxResultOutputChars, utf8.RuneCountInString(prefix))
	require.Contains(t, prefix, "abcdefghijklmnopqrstuvwxyz")
	longChunk := strings.Repeat("abcdefghijklmnopqrstuvwxyz", 4)
	require.NotContains(t, res.Output, longChunk)
	requireTruncatedExecOutput(t, res.Output)
}

func TestManager_MaxResultOutputCharsTruncatesUTF8Foreground(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithMaxResultOutputChars(81))
	execTool := newExecCommandTool(mgr)

	args := mustJSON(t, map[string]any{
		"command":       `printf '\303\251%.0s' {1..90}`,
		"yield_time_ms": 0,
	})
	out, err := execTool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)
	require.True(t, utf8.ValidString(res.Output))
	require.Contains(t, res.Output, "\xc3\xa9")
	requireTruncatedExecOutput(t, res.Output)
}

func TestManager_MaxResultOutputCharsHelperEdges(t *testing.T) {
	var nilManager *Manager
	require.Equal(t, "abc", nilManager.limitResultOutput("abc"))
	require.Equal(t, "abc", NewManager().limitResultOutput("abc"))
	require.Equal(t, "abc", truncateResultOutput("abc", 0))
	require.Equal(t, "abc", truncateResultOutput("abc", 3))
	require.Equal(t, "", firstRunes("abc", 0))
	require.Equal(t, "abc", firstRunes("abc", 5))
}

func TestManager_MaxResultOutputCharsTruncatesSessionCompletion(
	t *testing.T,
) {
	if runtime.GOOS == "windows" {
		t.Skip("pty is not supported on windows")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithMaxResultOutputChars(80))
	execTool := newExecCommandTool(mgr)

	ptyArgs := mustJSON(t, map[string]any{
		"command":       "printf 'abcdefghijklmnopqrstuvwxyz%.0s' {1..8}",
		"pty":           true,
		"yield_time_ms": 0,
	})
	out, err := execTool.Call(context.Background(), ptyArgs)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)
	requireTruncatedExecOutput(t, res.Output)

	timerArgs := mustJSON(t, map[string]any{
		"command":       "printf 'abcdefghijklmnopqrstuvwxyz%.0s' {1..8}",
		"yield_time_ms": 1000,
	})
	out, err = execTool.Call(context.Background(), timerArgs)
	require.NoError(t, err)

	res = out.(execResult)
	require.Equal(t, "exited", res.Status)
	requireTruncatedExecOutput(t, res.Output)
}

func TestManager_MaxResultOutputCharsTruncatesRunningTail(
	t *testing.T,
) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(
		WithMaxResultOutputChars(80),
		WithMaxYield(2*time.Second),
	)
	execTool := newExecCommandTool(mgr)

	out, err := execTool.Call(context.Background(), mustJSON(t, map[string]any{
		"command": "printf 'old%.0s' {1..80}; " +
			"printf '\\nLATEST\\n'; sleep 3",
		"yield_time_ms": 1500,
	}))
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "running", res.Status)
	require.NotEmpty(t, res.SessionID)
	t.Cleanup(func() {
		_ = mgr.kill(res.SessionID)
	})
	requireTruncatedExecOutput(t, res.Output)
	require.Contains(t, res.Output, "LATEST")
}

func TestExecTool_BackgroundPreservesShellManagedJobs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group behavior differs on windows")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	mgr := NewManager(WithDefaultTimeout(2 * time.Second))
	execTool := newExecCommandTool(mgr)

	out, err := execTool.Call(context.Background(), mustJSON(t, map[string]any{
		"command": fmt.Sprintf(
			"(sleep 0.2; echo survived > %q) >/dev/null 2>&1 &",
			marker,
		),
		"background":  true,
		"timeout_sec": 2,
	}))
	require.NoError(t, err)
	res := out.(execResult)
	require.Equal(t, "running", res.Status)
	require.NotEmpty(t, res.SessionID)
	t.Cleanup(func() {
		_ = mgr.kill(res.SessionID)
	})

	require.Eventually(t, func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}, time.Second, 20*time.Millisecond)
}

func TestManager_MaxResultOutputCharsTruncatesPollAndLog(
	t *testing.T,
) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(
		WithMaxResultOutputChars(80),
		WithMaxYield(2*time.Second),
	)
	execTool := newExecCommandTool(mgr)
	writeTool := newWriteStdinTool(mgr)

	out, err := execTool.Call(context.Background(), mustJSON(t, map[string]any{
		"command": "printf 'abcdefghijklmnopqrstuvwxyz%.0s' {1..8}; " +
			"printf '\\n'; sleep 3",
		"yield_time_ms": 1500,
	}))
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "running", res.Status)
	require.NotEmpty(t, res.SessionID)
	t.Cleanup(func() {
		_ = mgr.kill(res.SessionID)
	})

	writeAny, err := writeTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"session_id":    res.SessionID,
			"yield_time_ms": 0,
		}),
	)
	require.NoError(t, err)
	requireTruncatedExecOutput(t, outputField(writeAny.(map[string]any)))

	log, err := mgr.log(res.SessionID, nil, nil)
	require.NoError(t, err)
	requireTruncatedExecOutput(t, log.Output)
}

func TestManager_MaxResultOutputCharsPollKeepsNextOffset(
	t *testing.T,
) {
	mgr := NewManager(WithMaxResultOutputChars(12))
	sess := newSession("session-id", "cmd", 0)
	sess.appendOutput("alpha\nbravo\ncharlie\ndelta\n")
	sess.markDone(0)

	mgr.mu.Lock()
	mgr.sessions[sess.id] = sess
	mgr.mu.Unlock()

	poll, err := mgr.poll(sess.id, nil)
	require.NoError(t, err)
	require.Equal(t, 0, poll.Offset)
	require.Equal(t, 2, poll.NextOffset)
	require.Contains(t, poll.Output, "alpha\nbravo")
	require.NotContains(t, poll.Output, "charlie")
	requireTruncatedExecOutput(t, poll.Output)

	poll, err = mgr.poll(sess.id, nil)
	require.NoError(t, err)
	require.Equal(t, 2, poll.Offset)
	require.Equal(t, 3, poll.NextOffset)
	require.Contains(t, poll.Output, "charlie")
	require.NotContains(t, poll.Output, "delta")
	requireTruncatedExecOutput(t, poll.Output)

	log, err := mgr.log(sess.id, &poll.NextOffset, nil)
	require.NoError(t, err)
	require.Equal(t, 3, log.Offset)
	require.Equal(t, 4, log.NextOffset)
	require.Contains(t, log.Output, "delta")
}

func TestManager_MaxResultOutputCharsPollUsesRawLineOffsets(
	t *testing.T,
) {
	mgr := NewManager(WithMaxResultOutputChars(40))
	sess := newSession("session-id", "cmd", 0)
	sess.redact = func(output string) string {
		return strings.ReplaceAll(output, "bravo", "bravo\ninserted")
	}
	sess.appendOutput("alpha\nbravo\ncharlie\ndelta\n")
	sess.markDone(0)

	mgr.mu.Lock()
	mgr.sessions[sess.id] = sess
	mgr.mu.Unlock()

	limit := 2
	poll, err := mgr.poll(sess.id, &limit)
	require.NoError(t, err)
	require.Equal(t, 0, poll.Offset)
	require.Equal(t, 2, poll.NextOffset)
	require.Contains(t, poll.Output, "inserted")
	require.NotContains(t, poll.Output, "charlie")

	limit = 1
	poll, err = mgr.poll(sess.id, &limit)
	require.NoError(t, err)
	require.Equal(t, 2, poll.Offset)
	require.Equal(t, 3, poll.NextOffset)
	require.Contains(t, poll.Output, "charlie")
}

func TestManager_MaxResultOutputCharsPollCapsExpandedRedaction(
	t *testing.T,
) {
	mgr := NewManager(WithMaxResultOutputChars(12))
	sess := newSession("session-id", "cmd", 0)
	sess.redact = func(output string) string {
		return strings.ReplaceAll(
			output,
			"bravo",
			"bravo-with-expanded-redaction",
		)
	}
	sess.appendOutput("alpha\nbravo\ncharlie\n")
	sess.markDone(0)

	mgr.mu.Lock()
	mgr.sessions[sess.id] = sess
	mgr.mu.Unlock()

	poll, err := mgr.poll(sess.id, nil)
	require.NoError(t, err)
	require.Equal(t, 0, poll.Offset)
	require.Equal(t, 1, poll.NextOffset)
	requireTruncatedExecOutput(t, poll.Output)
	require.Contains(t, poll.Output, "alpha")
	require.NotContains(t, poll.Output, "bravo-with-expanded-redaction")
	require.NotContains(t, poll.Output, "charlie")

	poll, err = mgr.poll(sess.id, nil)
	require.NoError(t, err)
	require.Equal(t, 1, poll.Offset)
	require.Equal(t, 2, poll.NextOffset)
	requireTruncatedExecOutput(t, poll.Output)
	require.Contains(t, poll.Output, "bravo-with")
	require.NotContains(t, poll.Output, "charlie")

	log, err := mgr.log(sess.id, &poll.NextOffset, nil)
	require.NoError(t, err)
	require.Equal(t, 2, log.Offset)
	require.Equal(t, 3, log.NextOffset)
	require.Contains(t, log.Output, "charlie")
}

func TestManager_MaxResultOutputCharsPollOversizedSingleLine(
	t *testing.T,
) {
	mgr := NewManager(WithMaxResultOutputChars(12))
	sess := newSession("session-id", "cmd", 0)
	sess.appendOutput("abcdefghijklmnopqrstuvwxyz\nnext\n")
	sess.markDone(0)

	mgr.mu.Lock()
	mgr.sessions[sess.id] = sess
	mgr.mu.Unlock()

	poll, err := mgr.poll(sess.id, nil)
	require.NoError(t, err)
	require.Equal(t, 0, poll.Offset)
	require.Equal(t, 1, poll.NextOffset)
	require.Contains(t, poll.Output, "abcdefghijkl")
	require.NotContains(t, poll.Output, "next")
	requireTruncatedExecOutput(t, poll.Output)

	poll, err = mgr.poll(sess.id, nil)
	require.NoError(t, err)
	require.Equal(t, 1, poll.Offset)
	require.Equal(t, 2, poll.NextOffset)
	require.Contains(t, poll.Output, "mnopqrstuvwx")
	require.NotContains(t, poll.Output, "next")
	requireTruncatedExecOutput(t, poll.Output)

	poll, err = mgr.poll(sess.id, nil)
	require.NoError(t, err)
	require.Equal(t, 2, poll.Offset)
	require.Equal(t, 4, poll.NextOffset)
	require.Contains(t, poll.Output, "yz")
	require.Contains(t, poll.Output, "next")
}

func requireTruncatedExecOutput(t *testing.T, output string) {
	t.Helper()

	require.Contains(t, output, "OpenClaw truncated command output")
	require.Contains(t, output, "Write large outputs to a file")
	require.Contains(t, output, "chars")
	require.NotContains(t, output, "bytes")
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

	got := s.log(nil, nil, 0)
	require.Equal(t, 50, got.Offset)
	require.Equal(t, total, got.NextOffset)
	require.Len(t, strings.Split(got.Output, "\n"), defaultLogLimit)

	offset := 999
	got = s.log(&offset, nil, 0)
	require.Empty(t, got.Output)
	require.Equal(t, total, got.Offset)
	require.Equal(t, total, got.NextOffset)

	offset = 20
	limit := 2
	got = s.log(&offset, &limit, 0)
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

func TestManager_CleanShellCommandEnvSkipsLoginSnapshot(t *testing.T) {
	mgr := NewManager(WithCleanShellStartup(true))
	mgr.shellEnvSnapshot = func(
		context.Context,
		string,
	) map[string]string {
		t.Fatal("unexpected shell env snapshot")
		return nil
	}

	env := mgr.commandEnv(
		context.Background(),
		"/tmp/work",
		map[string]string{"EXTRA_ONLY": "extra"},
	)
	require.Equal(t, "extra", env["EXTRA_ONLY"])
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

func TestUploadEnvFromContext_UsesUploadStoreWithoutChannelPrefix(
	t *testing.T,
) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	scope := uploads.Scope{
		Channel:   "admin",
		UserID:    "u1",
		SessionID: "eval-session-1",
	}
	saved, err := store.SaveWithInfo(
		context.Background(),
		scope,
		"board.png",
		uploads.FileMetadata{
			MimeType: "image/png",
			Source:   uploads.SourceInbound,
		},
		[]byte("png"),
	)
	require.NoError(t, err)

	_, err = store.SaveWithInfo(
		context.Background(),
		uploads.Scope{
			Channel:   "admin",
			UserID:    "other-user",
			SessionID: "eval-session-1",
		},
		"other.png",
		uploads.FileMetadata{MimeType: "image/png"},
		[]byte("other"),
	)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			sessionpkg.NewSession("app", "u1", "eval-session-1"),
		),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	env := uploadEnvFromContext(ctx, store)
	require.Equal(t, saved.Path, env[envLastUploadPath])
	require.Equal(t, saved.HostRef, env[envLastUploadHostRef])
	require.Equal(t, saved.Path, env[envLastImagePath])
	require.Equal(t, saved.HostRef, env[envLastImageHostRef])
	require.Equal(t, filepath.Dir(saved.Path), env[envSessionUploadsDir])

	var recent []execUploadMeta
	require.NoError(
		t,
		json.Unmarshal([]byte(env[envRecentUploadsJSON]), &recent),
	)
	require.Len(t, recent, 1)
	require.Equal(t, saved.Path, recent[0].Path)
	require.Equal(t, uploads.SourceInbound, recent[0].Source)
}

func TestListUploadsForScopeWithoutChannelFiltersAndLimits(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	for _, tc := range []struct {
		scope uploads.Scope
		name  string
	}{
		{
			scope: uploads.Scope{
				Channel:   "admin",
				UserID:    "u1",
				SessionID: "eval-session-1",
			},
			name: "first.png",
		},
		{
			scope: uploads.Scope{
				Channel:   "admin",
				UserID:    "u1",
				SessionID: "other-session",
			},
			name: "skip.png",
		},
		{
			scope: uploads.Scope{
				Channel:   "wecom",
				UserID:    "u1",
				SessionID: "eval-session-1",
			},
			name: "second.png",
		},
	} {
		_, err := store.SaveWithInfo(
			context.Background(),
			tc.scope,
			tc.name,
			uploads.FileMetadata{MimeType: "image/png"},
			[]byte(tc.name),
		)
		require.NoError(t, err)
	}

	scope := uploads.Scope{UserID: "u1", SessionID: "eval-session-1"}
	files, err := listUploadsForScope(store, scope, 0)
	require.NoError(t, err)
	require.Len(t, files, 2)
	for _, file := range files {
		require.Equal(t, "u1", file.Scope.UserID)
		require.Equal(t, "eval-session-1", file.Scope.SessionID)
	}

	limited, err := listUploadsForScope(store, scope, 1)
	require.NoError(t, err)
	require.Len(t, limited, 1)
}

func TestListUploadsForScopeWithoutChannelPropagatesListAllError(
	t *testing.T,
) {
	t.Parallel()

	store, err := uploads.NewStore("/dev/null")
	require.NoError(t, err)

	_, err = listUploadsForScope(
		store,
		uploads.Scope{UserID: "u1", SessionID: "eval-session-1"},
		0,
	)
	require.Error(t, err)
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

type fakeSandboxExecEngine struct {
	manager   fakeSandboxExecManager
	runner    fakeSandboxExecRunner
	runResult codeexecutor.RunResult
}

func (e *fakeSandboxExecEngine) Manager() codeexecutor.WorkspaceManager {
	return &e.manager
}

func (e *fakeSandboxExecEngine) FS() codeexecutor.WorkspaceFS { return nil }

func (e *fakeSandboxExecEngine) Runner() codeexecutor.ProgramRunner {
	e.runner.result = e.runResult
	return &e.runner
}

func (e *fakeSandboxExecEngine) Describe() codeexecutor.Capabilities {
	return codeexecutor.Capabilities{}
}

type fakeSandboxExecManager struct {
	workspaces []codeexecutor.Workspace
}

func (m *fakeSandboxExecManager) CreateWorkspace(
	ctx context.Context,
	execID string,
	pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	_ = ctx
	ws := codeexecutor.Workspace{ID: execID, Path: execID}
	m.workspaces = append(m.workspaces, ws)
	return ws, nil
}

func (m *fakeSandboxExecManager) Cleanup(
	ctx context.Context,
	ws codeexecutor.Workspace,
) error {
	_, _ = ctx, ws
	return nil
}

type fakeSandboxExecRunner struct {
	specs  []codeexecutor.RunProgramSpec
	result codeexecutor.RunResult
}

func (r *fakeSandboxExecRunner) RunProgram(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	_, _ = ctx, ws
	r.specs = append(r.specs, spec)
	return r.result, nil
}

func outputField(result map[string]any) string {
	value, ok := result["output"].(string)
	if !ok {
		return ""
	}
	return value
}
