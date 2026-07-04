//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package opensandbox

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// integrationEnvVar gates the integration tests. Set OPENSANDBOX_INTEGRATION=1
// to run them against a live OpenSandbox server.
const integrationEnvVar = "OPENSANDBOX_INTEGRATION"

// skipIfNoIntegration skips the test unless OPENSANDBOX_INTEGRATION=1 is set.
func skipIfNoIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv(integrationEnvVar) != "1" {
		t.Skipf("skipping integration test; set %s=1 to run", integrationEnvVar)
	}
}

// newIntegrationExecutor creates a CodeExecutor against a live server.
// Endpoint defaults to localhost:8080 (WSL2 localhost forwarding).
func newIntegrationExecutor(t *testing.T, opts ...Option) *CodeExecutor {
	t.Helper()
	endpoint := os.Getenv("OPENSANDBOX_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:8080"
	}
	all := append([]Option{
		WithDomain(endpoint),
		WithProtocol("http"),
	}, opts...)
	exec, err := New(all...)
	require.NoErrorf(t, err, "failed to create executor against %s", endpoint)
	t.Cleanup(func() {
		_ = exec.Close()
	})
	return exec
}

// TestIntegration_Python executes a Python code block on a live
// OpenSandbox server and checks that stdout is returned.
func TestIntegration_Python(t *testing.T) {
	skipIfNoIntegration(t)
	exec := newIntegrationExecutor(t)

	res, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "integration-py",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "python", Code: `print("hello-from-opensandbox")`},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Output, "hello-from-opensandbox")
}

// TestIntegration_Bash executes a Bash code block on a live server.
func TestIntegration_Bash(t *testing.T) {
	skipIfNoIntegration(t)
	exec := newIntegrationExecutor(t)

	res, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "integration-sh",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "bash", Code: `echo hello-bash-integration`},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Output, "hello-bash-integration")
}

// TestIntegration_StreamingTimeoutNotKilledByRequestTimeout
// verifies the requestTimeout fix: a RunProgram call whose
// executionTimeout exceeds the SDK default requestTimeout (30s) must
// NOT be killed by the HTTP client. We run a command that sleeps 3
// seconds with executionTimeout=15s; before the fix, the default
// requestTimeout (30s) would not have killed this, but we also set
// WithRequestTimeout(2s) to force the clamp path. After clamping,
// requestTimeout becomes 15s+10s=25s, so the 3-second sleep completes
// successfully.
func TestIntegration_StreamingTimeoutNotKilledByRequestTimeout(t *testing.T) {
	skipIfNoIntegration(t)
	// WithRequestTimeout(2s) < WithExecutionTimeout(15s) + buffer(10s)
	// => NewWithContext clamps requestTimeout to 25s.
	exec := newIntegrationExecutor(t,
		WithRequestTimeout(2*time.Second),
		WithExecutionTimeout(15*time.Second),
	)

	// A 3-second sleep should complete well within the clamped 25s HTTP
	// timeout, but would fail if requestTimeout stayed at 2s.
	res, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "integration-stream-timeout",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "bash", Code: `sleep 3; echo stream-ok`},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Output, "stream-ok",
		"streaming /command should not be killed by HTTP client timeout when requestTimeout is clamped")
}

// TestIntegration_OutputFileCollection runs code that writes a file and
// verifies the file is collected via the default output patterns.
func TestIntegration_OutputFileCollection(t *testing.T) {
	skipIfNoIntegration(t)
	exec := newIntegrationExecutor(t)

	res, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "integration-files",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "python", Code: `
import os
ws = os.environ.get("WORKSPACE_DIR", "/tmp/run")
print("WORKSPACE_DIR=" + ws)
print("OUTPUT_DIR=" + os.environ.get("OUTPUT_DIR", "<unset>"))
# Write result.txt directly under the workspace root.
with open(os.path.join(ws, "result.txt"), "w") as f:
    f.write("42")
print("file-written")
`},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Output, "file-written")
	// The default outputPatterns includes "*.txt", so result.txt should
	// be collected.
	require.NotEmpty(t, res.OutputFiles, "output files should be collected")
	found := false
	for _, f := range res.OutputFiles {
		if strings.Contains(f.Name, "result.txt") {
			found = true
			break
		}
	}
	assert.True(t, found, "result.txt should be in collected output files")
}

// TestIntegration_PutFilesAndRun verifies that files staged via PutFiles
// are visible to RunProgram inside the sandbox.
func TestIntegration_PutFilesAndRun(t *testing.T) {
	skipIfNoIntegration(t)
	exec := newIntegrationExecutor(t)

	ctx := context.Background()
	ws, err := exec.CreateWorkspace(ctx, "integration-putfiles", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	defer exec.Cleanup(ctx, ws)

	err = exec.PutFiles(ctx,
		ws,
		[]codeexecutor.PutFile{
			{Path: "src/hello.py", Content: []byte("print('hello-from-putfiles')\n"), Mode: 0o644},
		},
	)
	require.NoError(t, err)

	res, err := exec.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "python3",
		Args:    []string{"src/hello.py"},
		Cwd:     "",
		Timeout: 15 * time.Second,
	})
	require.NoError(t, err)
	assert.Contains(t, res.Stdout, "hello-from-putfiles")
}
