//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package codeact

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
)

func TestSandboxRunnerRoutesToolCalls(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	runner := NewSandboxRunner(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
		sandbox.WithWorkspaceRoot(root),
	)
	handler := toolCallHandlerFunc(func(_ context.Context, call ToolCall) (json.RawMessage, error) {
		require.Equal(t, "add", call.Name)
		require.JSONEq(t, `{"a":20,"b":22}`, string(call.Args))
		return json.RawMessage(`42`), nil
	})

	result, err := Execute(
		context.Background(),
		runner,
		handler,
		"value = await call_tool('add', a=20, b=22)\nreturn {'answer': value}",
	)
	require.NoError(t, err)
	require.JSONEq(t, `{"answer":42}`, string(result.Value))
	requireNoSandboxGuestScripts(t, root)
}

func TestSandboxRunnerUsesCleanEnvironment(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	t.Setenv("TRPC_SANDBOX_ENV_PROBE", "must-not-leak")
	runner := NewSandboxRunner(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
	)
	result, err := Execute(
		context.Background(),
		runner,
		fakeToolCallHandler{},
		"import os\nreturn os.getenv('TRPC_SANDBOX_ENV_PROBE')",
	)
	require.NoError(t, err)
	require.JSONEq(t, "null", string(result.Value))
}

func TestSandboxRunnerManagedBackendBlocksNetworkAndHostWrites(t *testing.T) {
	requireManagedSandbox(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	address := listener.Addr().(*net.TCPAddr)
	probe, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	require.NoError(t, err)
	require.NoError(t, probe.Close())
	escapePath := filepath.Join(t.TempDir(), "sandbox-escape")
	code := fmt.Sprintf(`
import socket
network_blocked = False
s = socket.socket()
s.settimeout(0.2)
try:
    s.connect((%q, %d))
except Exception:
    network_blocked = True
finally:
    s.close()
write_blocked = False
try:
    with open(%q, "w") as f:
        f.write("escaped")
except Exception:
    write_blocked = True
return {"network_blocked": network_blocked, "write_blocked": write_blocked}
`, address.IP.String(), address.Port, escapePath)

	result, err := Execute(
		context.Background(),
		NewSandboxRunner(),
		fakeToolCallHandler{},
		code,
	)
	require.NoError(t, err)
	require.JSONEq(t, `{"network_blocked":true,"write_blocked":true}`, string(result.Value))
	require.NoFileExists(t, escapePath)
}

func TestSandboxRunnerEnforcesCodeLimit(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	runner := NewSandboxRunner(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
	)
	runner.MaxCodeBytes = 8
	_, err := Execute(
		context.Background(),
		runner,
		fakeToolCallHandler{},
		"return None",
	)
	require.ErrorContains(t, err, "code exceeds 8 bytes")
}

func TestSandboxRunnerOptionalTimeoutStopsBusyCode(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	runner := NewSandboxRunner(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
	)
	runner.Timeout = 100 * time.Millisecond
	started := time.Now()
	_, err := Execute(
		context.Background(),
		runner,
		fakeToolCallHandler{},
		"while True:\n    pass",
	)
	require.Error(t, err)
	require.Less(t, time.Since(started), 5*time.Second)
}

func TestSandboxRunnerIncludesGuestStderrInFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper is Unix-specific")
	}
	wrapper := filepath.Join(t.TempDir(), "python-with-stderr")
	require.NoError(t, os.WriteFile(
		wrapper,
		[]byte(
			"#!/bin/sh\n"+
				"IFS= read -r _\n"+
				"echo 'guest bootstrap failed' >&2\n"+
				"exit 97\n",
		),
		0o700,
	))
	runner := NewSandboxRunner(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
	)
	runner.Python = wrapper

	_, err := Execute(
		context.Background(),
		runner,
		fakeToolCallHandler{},
		"return None",
	)
	require.ErrorContains(t, err, "guest bootstrap failed")
}

func TestSandboxRunnerDrainsGuestStderrBeforeWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper is Unix-specific")
	}
	wrapper := filepath.Join(t.TempDir(), "python-with-stderr-burst")
	require.NoError(t, os.WriteFile(
		wrapper,
		[]byte(`#!/bin/sh
IFS= read -r _
i=0
while [ "$i" -lt 8192 ]; do
  printf '0123456789abcdef0123456789abcdef'
  i=$((i + 1))
done >&2
printf '\nfinal stderr marker\n' >&2
exit 97
`),
		0o700,
	))
	runner := NewSandboxRunner(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
	)
	runner.Python = wrapper

	_, err := Execute(
		context.Background(),
		runner,
		fakeToolCallHandler{},
		"return None",
	)
	require.ErrorContains(t, err, "final stderr marker")
}

func TestSandboxRunnerKillsDescendantHoldingStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group cleanup is Unix-specific")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	originalTimeout := completedGuestWaitTimeout
	completedGuestWaitTimeout = 50 * time.Millisecond
	t.Cleanup(func() { completedGuestWaitTimeout = originalTimeout })
	runner := NewSandboxRunner(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
	)

	started := time.Now()
	result, err := Execute(
		context.Background(),
		runner,
		fakeToolCallHandler{},
		"import subprocess, sys\n"+
			"subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(30)'], "+
			"stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL)\n"+
			"return 'done'",
	)
	require.NoError(t, err)
	require.JSONEq(t, `"done"`, string(result.Value))
	require.Less(t, time.Since(started), 2*time.Second)
}

func TestSandboxStderrCaptureBoundsOutput(t *testing.T) {
	capture := newSandboxStderrCapture(4)
	n, err := capture.Write([]byte("abcdef"))
	require.NoError(t, err)
	require.Equal(t, 6, n)
	require.Equal(t, "abcd", capture.String())
	require.True(t, capture.Exceeded())

	process := &sandboxStdioProcess{stderr: capture}
	require.Equal(t, "abcd\n[stderr truncated]", process.diagnosticOutput())
	var nilProcess *sandboxStdioProcess
	require.Empty(t, nilProcess.diagnosticOutput())
}

func TestSandboxRunnerTimeoutCancelsToolCallAndCleansWorkspace(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	runner := NewSandboxRunner(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
		sandbox.WithWorkspaceRoot(root),
	)
	runner.Timeout = 500 * time.Millisecond
	handlerErr := make(chan error, 1)
	handler := toolCallHandlerFunc(func(ctx context.Context, _ ToolCall) (json.RawMessage, error) {
		<-ctx.Done()
		handlerErr <- ctx.Err()
		return nil, ctx.Err()
	})

	_, err := Execute(
		context.Background(),
		runner,
		handler,
		"value = await call_tool('wait')\nreturn value",
	)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	select {
	case err := <-handlerErr:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(2 * time.Second):
		t.Fatal("tool call handler did not observe the runner timeout")
	}
	requireNoSandboxGuestScripts(t, root)
}

func TestSandboxRunnerRequiresConstructor(t *testing.T) {
	_, err := (&SandboxRunner{}).ExecuteCodeAct(
		context.Background(),
		Request{Code: "return None", Language: "python"},
		fakeToolCallHandler{},
	)
	require.ErrorContains(t, err, "sandbox runner is required")
}

func requireNoSandboxGuestScripts(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name() == "guest.py" {
			t.Fatalf("guest script was not cleaned up: %s", path)
		}
		return nil
	}))
}

func requireManagedSandbox(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	switch runtime.GOOS {
	case "linux":
		if _, err := exec.LookPath("bwrap"); err != nil {
			t.Skip("bubblewrap is not installed")
		}
	case "darwin":
		if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
			t.Skip("sandbox-exec is not installed")
		}
	default:
		t.Skip("managed sandbox backend is unavailable")
	}
}
