//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package dynamicworkflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
)

func TestSandboxRunnerRoutesWorkflowCalls(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	root := t.TempDir()
	runner := NewSandboxRunner(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
		sandbox.WithWorkspaceRoot(root),
	)
	handler := callHandlerFunc(func(_ context.Context, call Call) (json.RawMessage, error) {
		require.Equal(t, CallKindTool, call.Kind)
		require.Equal(t, "add", call.Name)
		require.JSONEq(t, `{"a":20,"b":22}`, string(call.Args))
		return json.RawMessage(`42`), nil
	})

	result, err := Execute(context.Background(), runner, handler, `
answer = await call_tool("add", a=20, b=22)
return {"answer": answer}
`)
	require.NoError(t, err)
	require.JSONEq(t, `{"answer":42}`, string(result.Value))
	requireNoGuestScripts(t, root)
}

func TestSandboxRunnerRoutesAgentCalls(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	runner := NewSandboxRunner(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
	)
	handler := callHandlerFunc(func(_ context.Context, call Call) (json.RawMessage, error) {
		require.Equal(t, CallKindAgent, call.Kind)
		require.Empty(t, call.Name)
		require.JSONEq(t, `{
			"input":{"draft":"hello"},
			"options":{"template":"reviewer"}
		}`, string(call.Args))
		return json.RawMessage(`{"text":"approved"}`), nil
	})

	result, err := Execute(context.Background(), runner, handler, `
review = await agent({"draft": "hello"}, "reviewer")
return {"review": review["text"]}
`)
	require.NoError(t, err)
	require.JSONEq(t, `{"review":"approved"}`, string(result.Value))
}

func TestSandboxRunnerUsesCleanEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper is Unix-specific")
	}
	python, err := exec.LookPath("python3")
	require.NoError(t, err)
	python, err = filepath.Abs(python)
	require.NoError(t, err)
	t.Setenv("TRPC_SANDBOX_ENV_PROBE", "must-not-leak")
	wrapper := filepath.Join(t.TempDir(), "python-clean-env-check")
	script := fmt.Sprintf(`#!/bin/sh
if [ -n "$TRPC_SANDBOX_ENV_PROBE" ]; then
  echo "host environment leaked" >&2
  exit 97
fi
exec %s "$@"
`, shellQuote(python))
	require.NoError(t, os.WriteFile(wrapper, []byte(script), 0o700))

	runner := NewSandboxRunner(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
	)
	runner.Python = wrapper
	result, err := Execute(
		context.Background(),
		runner,
		callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
			return nil, nil
		}),
		"return {'clean': True}",
	)
	require.NoError(t, err)
	require.JSONEq(t, `{"clean":true}`, string(result.Value))
}

func TestSandboxRunnerManagedBackend(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
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
	runner := NewSandboxRunner()
	result, err := Execute(
		context.Background(),
		runner,
		callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
			return nil, nil
		}),
		"return {'sandboxed': True}",
	)
	require.NoError(t, err)
	require.JSONEq(t, `{"sandboxed":true}`, string(result.Value))
}

func TestSandboxRunnerEnforcesCodeLimit(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	runner := NewSandboxRunner(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
	)
	runner.MaxCodeBytes = 8
	_, err := Execute(
		context.Background(),
		runner,
		callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
			return nil, nil
		}),
		"return None",
	)
	require.ErrorContains(t, err, "code exceeds 8 bytes")
}

func TestSandboxRunnerOptionalTimeoutStopsBusyWorkflow(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	runner := NewSandboxRunner(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
	)
	runner.Timeout = 100 * time.Millisecond
	started := time.Now()
	_, err := Execute(
		context.Background(),
		runner,
		callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
			return nil, nil
		}),
		"while True:\n    pass\nreturn None",
	)
	require.Error(t, err)
	require.Less(t, time.Since(started), 5*time.Second)
}

func TestSandboxRunnerTimeoutCancelsCallbackAndCleansWorkspace(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	root := t.TempDir()
	runner := NewSandboxRunner(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
		sandbox.WithWorkspaceRoot(root),
	)
	runner.Timeout = 500 * time.Millisecond
	handlerErr := make(chan error, 1)
	handler := callHandlerFunc(func(ctx context.Context, _ Call) (json.RawMessage, error) {
		<-ctx.Done()
		handlerErr <- ctx.Err()
		return nil, ctx.Err()
	})

	_, err := Execute(
		context.Background(),
		runner,
		handler,
		`return await call_tool("wait")`,
	)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	select {
	case err := <-handlerErr:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(2 * time.Second):
		t.Fatal("workflow callback did not observe the runner timeout")
	}
	requireNoGuestScripts(t, root)
}

func TestSandboxRunnerRequiresConstructor(t *testing.T) {
	_, err := (&SandboxRunner{}).ExecuteWorkflow(
		context.Background(),
		Request{Code: "return None"},
		callHandlerFunc(func(context.Context, Call) (json.RawMessage, error) {
			return nil, nil
		}),
	)
	require.ErrorContains(t, err, "sandbox runner is required")
}

func TestSandboxRunnerRequiresCallHandler(t *testing.T) {
	_, err := NewSandboxRunner(
		sandbox.WithPermissionProfile(sandbox.DangerFullAccessProfile()),
	).ExecuteWorkflow(
		context.Background(),
		Request{Code: "return None"},
		nil,
	)
	require.ErrorContains(t, err, "call handler is required")
}

func requireNoGuestScripts(t *testing.T, root string) {
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

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
