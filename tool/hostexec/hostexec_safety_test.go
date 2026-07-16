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
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestExecCommandSafetyGuardScansEffectiveRequestBeforeExecution(
	t *testing.T,
) {
	baseDir := t.TempDir()
	marker := filepath.Join(baseDir, "must-not-exist")
	command := `echo ran > "` + marker + `"`

	var scanned map[string]any
	recorder := tool.PermissionPolicyFunc(func(
		_ context.Context,
		req *tool.PermissionRequest,
	) (tool.PermissionDecision, error) {
		require.NoError(t, json.Unmarshal(req.Arguments, &scanned))
		return tool.DenyPermission("recording policy blocked execution"), nil
	})
	guard, err := safety.NewGuard(
		hostSafetyPolicy(7),
		safety.WithPermissionPolicy(recorder),
	)
	require.NoError(t, err)

	set, err := NewToolSet(
		WithBaseDir(baseDir),
		WithBaseEnv(map[string]string{"BASE_ONLY": "base"}),
		WithSafetyGuard(guard),
	)
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, _ := toolSetTools(t, set)
	result, err := execTool.Call(context.Background(), mustJSON(t, map[string]any{
		"command":    command,
		"workdir":    "child",
		"env":        map[string]string{"CALL_ONLY": "call"},
		"timeoutSec": 9,
		"pty":        true,
		"background": true,
	}))
	require.NoError(t, err)
	require.Equal(t, tool.PermissionResultStatusDenied, result.(tool.PermissionResult).Status)
	require.NoFileExists(t, marker)

	require.Equal(t, "hostexec", scanned["backend"])
	require.Equal(t, command, scanned["command"])
	require.Equal(t, filepath.Join(baseDir, "child"), scanned["workdir"])
	require.EqualValues(t, 9, scanned["timeout_sec"])
	require.Equal(t, true, scanned["pty"])
	require.Equal(t, true, scanned["background"])
	require.EqualValues(t, 7, scanned["max_output_bytes"])
	scannedEnv := scanned["env"].(map[string]any)
	require.Equal(t, "base", scannedEnv["BASE_ONLY"])
	require.Equal(t, "call", scannedEnv["CALL_ONLY"])
	require.NotEmpty(t, scannedEnv, "inherited execution environment must be scanned")
}

func TestExecCommandSafetyGuardBlocksDangerousCommand(t *testing.T) {
	baseDir := t.TempDir()
	marker := filepath.Join(baseDir, "must-not-exist")
	guard, err := safety.NewDefaultGuard()
	require.NoError(t, err)
	set, err := NewToolSet(WithBaseDir(baseDir), WithSafetyGuard(guard))
	require.NoError(t, err)
	defer set.Close()

	execTool, _, _, _ := toolSetTools(t, set)
	result, err := execTool.Call(context.Background(), mustJSON(t, map[string]any{
		"command": `rm -rf / && echo ran > "` + marker + `"`,
	}))
	require.NoError(t, err)
	require.Equal(t, tool.PermissionResultStatusDenied, result.(tool.PermissionResult).Status)
	require.NoFileExists(t, marker)
}

func TestWriteStdinSafetyGuardBlocksInputBeforeWrite(t *testing.T) {
	guard, err := safety.NewGuard(hostSafetyPolicy(0))
	require.NoError(t, err)
	mgr := newManager()
	sess := newSession("session", "cat", defaultMaxLines)
	writer := &testWriteCloser{}
	sess.stdin = writer
	mgr.sessions[sess.id] = sess
	writeTool := &writeStdinTool{mgr: mgr, safety: guard}

	result, err := writeTool.Call(context.Background(), mustJSON(t, map[string]any{
		"session_id":    sess.id,
		"chars":         "hello",
		"yield_time_ms": 0,
	}))
	require.NoError(t, err)
	require.Equal(
		t,
		tool.PermissionResultStatusApprovalRequired,
		result.(tool.PermissionResult).Status,
	)
	require.Empty(t, writer.String())

	result, err = writeTool.Call(context.Background(), mustJSON(t, map[string]any{
		"session_id":    sess.id,
		"submit":        true,
		"yield_time_ms": 0,
	}))
	require.NoError(t, err)
	require.Equal(
		t,
		tool.PermissionResultStatusApprovalRequired,
		result.(tool.PermissionResult).Status,
	)
	require.Empty(t, writer.String())
}

func TestExecCommandSafetyGuardRedactsOutput(t *testing.T) {
	const secret = "sk-abcdefghijklmnopqrst"
	secretFile := filepath.Join(t.TempDir(), "output.txt")
	require.NoError(t, os.WriteFile(secretFile, []byte(secret), 0o600))
	guard, err := safety.NewGuard(hostSafetyPolicy(0))
	require.NoError(t, err)
	set, err := NewToolSet(WithSafetyGuard(guard))
	require.NoError(t, err)
	defer set.Close()

	command := `cat "` + secretFile + `"`
	if runtime.GOOS == "windows" {
		command = `type "` + secretFile + `"`
	}
	execTool, _, _, _ := toolSetTools(t, set)
	result, err := execTool.Call(context.Background(), mustJSON(t, map[string]any{
		"command":       command,
		"yield_time_ms": 0,
	}))
	require.NoError(t, err)
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("execution unexpectedly blocked: %#v", result)
	}
	output := resultMap["output"].(string)
	require.NotContains(t, output, secret)
	require.Contains(t, output, "[REDACTED]")
}

func TestSafetyTimeoutUsesProfileAsDefaultAndRuntimeCap(t *testing.T) {
	policy := hostSafetyPolicy(0)
	profile := policy.Profiles[toolExecCommand]
	profile.MaxTimeout = safety.Duration(30 * time.Second)
	policy.Profiles[toolExecCommand] = profile
	guard, err := safety.NewGuard(policy)
	require.NoError(t, err)

	defaulted := safetyTimeoutForScan(guard, toolExecCommand, nil)
	require.NotNil(t, defaulted)
	require.Equal(t, 30, *defaulted)

	requested := 90
	require.Equal(t, 90, *safetyTimeoutForScan(
		guard, toolExecCommand, &requested,
	))
	require.Equal(t, 30, *cappedSafetyTimeout(
		guard, toolExecCommand, &requested,
	))
}

func TestHostExecRuntimeTimeoutPreservesSubSecondSafetyCeiling(t *testing.T) {
	requested := 10
	require.Equal(t, 500*time.Millisecond, execTimeout(execParams{
		TimeoutS: &requested, MaxTimeout: 500 * time.Millisecond,
	}))
}

func TestSafetyExecutionEnvDropsInheritedSecretsButScansExplicitOnes(t *testing.T) {
	const name = "HOSTEXEC_INHERITED_API_KEY"
	const value = "sk-abcdefghijklmnopqrst"
	t.Setenv(name, value)

	env := safetyExecutionEnv(nil, nil)
	require.NotContains(t, env, name)
	env = safetyExecutionEnv(nil, map[string]string{name: value})
	require.Equal(t, value, env[name])
	env = safetyExecutionEnv(nil, map[string]string{"PATH": "controlled", "CI": "1"})
	require.NotEqual(t, "controlled", env["PATH"])
	require.Equal(t, "1", env["CI"])
	trusted := map[string]string{
		"SYSTEMROOT": `C:\Windows`, "COMSPEC": `C:\Windows\System32\cmd.exe`,
	}
	env = safetyExecutionEnv(trusted, map[string]string{
		"systemroot": `C:\attacker`, "ComSpec": `C:\attacker\cmd.exe`,
		"Pathext": ".EVIL", "CI": "2",
	})
	require.Equal(t, `C:\Windows`, env["SYSTEMROOT"])
	require.Equal(t, `C:\Windows\System32\cmd.exe`, env["COMSPEC"])
	require.NotEqual(t, ".EVIL", env["PATHEXT"])
	require.Equal(t, "2", env["CI"])
	scanEnv := safetyScanEnv(nil, map[string]string{"PATH": "controlled"})
	require.Equal(t, "controlled", scanEnv["PATH"])
}

func TestHostExecFrameworkOnlySafetyGuardProvidesRuntimeProfile(t *testing.T) {
	policy := hostSafetyPolicy(1024)
	profile := policy.Profiles[toolExecCommand]
	profile.MaxTimeout = safety.Duration(30 * time.Second)
	policy.Profiles[toolExecCommand] = profile
	guard, err := safety.NewGuard(policy)
	require.NoError(t, err)
	ctx := tool.WithPermissionPolicyContext(context.Background(), guard)
	effective := effectiveHostSafetyGuard(ctx, nil)
	require.Same(t, guard, effective)
	require.EqualValues(t, 1024, safetyMaxOutputBytes(effective, toolExecCommand))
	requested := 90
	require.Equal(t, 30, *cappedSafetyTimeout(effective, toolExecCommand, &requested))
	env := safetyExecutionEnv(nil, map[string]string{"BASH_ENV": "evil", "CI": "1"})
	require.NotContains(t, env, "BASH_ENV")
	require.Equal(t, "1", env["CI"])
}

func TestHostExecRuntimeProfileUsesMostRestrictiveGuard(t *testing.T) {
	directPolicy := hostSafetyPolicy(1 << 20)
	directProfile := directPolicy.Profiles[toolExecCommand]
	directProfile.MaxTimeout = safety.Duration(2 * time.Minute)
	directPolicy.Profiles[toolExecCommand] = directProfile
	direct, err := safety.NewGuard(directPolicy)
	require.NoError(t, err)

	invocationPolicy := hostSafetyPolicy(1024)
	invocationProfile := invocationPolicy.Profiles[toolExecCommand]
	invocationProfile.MaxTimeout = safety.Duration(30 * time.Second)
	invocationPolicy.Profiles[toolExecCommand] = invocationProfile
	invocation, err := safety.NewGuard(invocationPolicy)
	require.NoError(t, err)

	ctx := tool.WithPermissionPolicyContext(context.Background(), invocation)
	profile := effectiveHostRuntimeSafetyProfile(ctx, direct, toolExecCommand)
	require.Equal(t, safety.Duration(30*time.Second), profile.MaxTimeout)
	require.EqualValues(t, 1024, profile.MaxOutputBytes)
}

func TestManagerMaxOutputBytesKillsNoNewlineOutput(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}
	command := `printf '123456789'; sleep 5`
	if runtime.GOOS == "windows" {
		command = `for /L %i in (1,1,100000000) do @<nul set /p "=123456789"`
	}

	yield := 0
	timeout := 10
	started := time.Now()
	result, err := newManager().exec(context.Background(), execParams{
		Command:        command,
		YieldMs:        &yield,
		TimeoutS:       &timeout,
		MaxOutputBytes: 5,
	})
	require.NoError(t, err)
	require.Less(t, time.Since(started), 4*time.Second)
	require.Equal(t, "12345", result.Output)
}

func TestWindowsTerminateProcessTreeKillsDescendant(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows taskkill behavior")
	}
	tempDir := t.TempDir()
	marker := filepath.Join(tempDir, "child-survived")
	ready := filepath.Join(tempDir, "child-ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHostExecWindowsProcessTreeHelper$")
	cmd.Env = append(os.Environ(),
		"TRPC_HOSTEXEC_TREE_HELPER=parent",
		"TRPC_HOSTEXEC_TREE_MARKER="+marker,
		"TRPC_HOSTEXEC_TREE_READY="+ready,
	)
	require.NoError(t, cmd.Start())
	require.Eventually(t, func() bool {
		_, err := os.Stat(ready)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond, "descendant did not start")
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, terminateProcessTree(canceledCtx, cmd.Process, 0, 0))
	_ = cmd.Wait()
	require.Never(t, func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}, 1200*time.Millisecond, 10*time.Millisecond,
		"hostexec descendant survived tree termination")
}

func TestHostExecWindowsProcessTreeHelper(t *testing.T) {
	mode := os.Getenv("TRPC_HOSTEXEC_TREE_HELPER")
	if mode == "" {
		return
	}
	if mode == "child" {
		if err := os.WriteFile(
			os.Getenv("TRPC_HOSTEXEC_TREE_READY"),
			[]byte("ready"),
			0o600,
		); err != nil {
			os.Exit(3)
		}
		time.Sleep(time.Second)
		_ = os.WriteFile(os.Getenv("TRPC_HOSTEXEC_TREE_MARKER"), []byte("survived"), 0o600)
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHostExecWindowsProcessTreeHelper$")
	cmd.Env = append(os.Environ(),
		"TRPC_HOSTEXEC_TREE_HELPER=child",
		"TRPC_HOSTEXEC_TREE_MARKER="+os.Getenv("TRPC_HOSTEXEC_TREE_MARKER"),
		"TRPC_HOSTEXEC_TREE_READY="+os.Getenv("TRPC_HOSTEXEC_TREE_READY"),
	)
	if err := cmd.Start(); err != nil {
		os.Exit(2)
	}
	time.Sleep(30 * time.Second)
}

func TestSessionMaxOutputBytesIsSharedAcrossPartialChunks(t *testing.T) {
	sess := newSession("session", "command", defaultMaxLines, 5)
	require.False(t, sess.appendOutput("abc"))
	require.True(t, sess.appendOutput("def"))
	require.True(t, sess.appendOutput("ignored"))
	output, _ := sess.allOutput()
	require.Equal(t, "abcde", output)
	require.EqualValues(t, 5, sess.outputBytes)
	require.True(t, sess.outputLimitReached)
}

func hostSafetyPolicy(maxOutputBytes int64) safety.Policy {
	return safety.Policy{
		Version: safety.CurrentPolicyVersion,
		Profiles: map[string]safety.ToolProfile{
			toolExecCommand: {
				AllowHost:       true,
				AllowBackground: true,
				AllowPTY:        true,
				MaxOutputBytes:  maxOutputBytes,
			},
			toolWriteStdin: {AllowHost: true},
		},
	}
}
