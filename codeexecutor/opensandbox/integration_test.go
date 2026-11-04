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
	"runtime"
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
	base := []Option{
		WithDomain(endpoint),
		WithProtocol("http"),
	}
	if k := os.Getenv("OPENSANDBOX_API_KEY"); k != "" {
		base = append(base, WithAPIKey(k))
	}
	// WSL2 / Docker Desktop: sandbox containers live on a bridge
	// network that the host cannot reach directly. Linux CI talks to
	// execd on the docker network without the proxy. Windows Docker
	// Desktop needs the proxy unless the caller explicitly disables it.
	switch os.Getenv("OPENSANDBOX_USE_SERVER_PROXY") {
	case "1":
		base = append(base, WithUseServerProxy(true))
	case "0":
		// explicit off
	default:
		if runtime.GOOS == "windows" {
			base = append(base, WithUseServerProxy(true))
		}
	}
	all := append(base, opts...)
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

func TestIntegration_StderrAndNonZeroExit(t *testing.T) {
	skipIfNoIntegration(t)
	exec := newIntegrationExecutor(t)

	res, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "integration-stderr",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "bash", Code: "echo oops >&2; exit 7"},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Output, "[stderr]")
	assert.Contains(t, res.Output, "oops")
	assert.Contains(t, res.Output, "[exit 7]")
}

func TestIntegration_Timeout(t *testing.T) {
	skipIfNoIntegration(t)
	exec := newIntegrationExecutor(t, WithExecutionTimeout(2*time.Second))

	res, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "integration-timeout",
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "bash", Code: "sleep 20; echo should-not-appear"},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Output, "[timeout")
	assert.NotContains(t, res.Output, "should-not-appear")
}

func TestIntegration_CollectFilenameWithNewline(t *testing.T) {
	skipIfNoIntegration(t)
	exec := newIntegrationExecutor(t)

	ctx := context.Background()
	ws, err := exec.CreateWorkspace(ctx, "integration-newline-name", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	defer exec.Cleanup(ctx, ws)

	err = exec.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "writer.py",
		Content: []byte("open('trailing.txt\\n', 'w').write('ok')\n"),
		Mode:    0o644,
	}})
	require.NoError(t, err)

	run, err := exec.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "python3",
		Args:    []string{"writer.py"},
		Timeout: 15 * time.Second,
	})
	require.NoError(t, err)
	require.Equal(t, 0, run.ExitCode, run.Stderr)

	listed, err := exec.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "python3",
		Args:    []string{"-c", "import os; print(repr(os.listdir('.')))"},
		Timeout: 15 * time.Second,
	})
	require.NoError(t, err)
	require.Equal(t, 0, listed.ExitCode, listed.Stderr)
	require.Contains(t, listed.Stdout, `trailing.txt\n`,
		"writer.py must create a filename ending in newline, got %s", listed.Stdout)

	files, err := exec.Collect(ctx, ws, []string{"*"})
	require.NoError(t, err)
	found := false
	for _, f := range files {
		if f.Name == "trailing.txt\n" || strings.HasSuffix(f.Name, "trailing.txt\n") {
			found = true
			assert.Equal(t, "ok", f.Content)
			break
		}
	}
	assert.True(t, found, "filename ending in newline must survive Collect, listdir=%s files=%q",
		listed.Stdout, namesOf(files))
}

func namesOf(files []codeexecutor.File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Name
	}
	return out
}

// TestIntegration_CollectListingSemantics pins the execd behaviour
// Collect relies on against a live server: the workspace is listed
// once, symlinks are reported as symlinks (so a link to a file outside
// the workspace is never downloaded, and neither is a link whose target
// is inside), directories are not collected, a path glob matches the
// relative path while a bare basename matches at any depth, and a
// FIFO does not hang the collection.
func TestIntegration_CollectListingSemantics(t *testing.T) {
	skipIfNoIntegration(t)
	exec := newIntegrationExecutor(t)

	ctx := context.Background()
	ws, err := exec.CreateWorkspace(ctx, "integration-listing", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	defer exec.Cleanup(ctx, ws)

	setup := strings.Join([]string{
		"set -e",
		"mkdir -p out/reports sub/deep /tmp/osb-outside-" + ws.ID,
		"echo secret > /tmp/osb-outside-" + ws.ID + "/secret.txt",
		"echo q1 > out/reports/q1.csv",
		"echo q3 > sub/q3.csv",
		"echo top > notes.txt",
		"echo deep > sub/deep/notes.txt",
		"ln -s /tmp/osb-outside-" + ws.ID + "/secret.txt leak.txt",
		"ln -s notes.txt inside_link.txt",
		"ln -s /tmp/osb-outside-" + ws.ID + " leak_dir",
		"mkfifo pipe.txt",
	}, "\n")
	run, err := exec.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "bash",
		Args:    []string{"-c", setup},
		Timeout: 15 * time.Second,
	})
	require.NoError(t, err)
	require.Equal(t, 0, run.ExitCode, run.Stderr)

	files, err := exec.Collect(ctx, ws, []string{"*.txt"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"notes.txt", "sub/deep/notes.txt"}, namesOf(files),
		"symlinks (inside or outside), the FIFO and files reachable only through leak_dir must be skipped")
	for _, f := range files {
		assert.NotContains(t, f.Content, "secret", "symlink target must never be read")
	}

	files, err = exec.Collect(ctx, ws, []string{"out/reports/*.csv"})
	require.NoError(t, err)
	assert.Equal(t, []string{"out/reports/q1.csv"}, namesOf(files),
		"a path glob must match the relative path, not every *.csv")

	files, err = exec.Collect(ctx, ws, []string{"*.csv"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"out/reports/q1.csv", "sub/q3.csv"}, namesOf(files))
}

func TestIntegration_EngineRefusesCleanEnvCapability(t *testing.T) {
	skipIfNoIntegration(t)
	exec := newIntegrationExecutor(t)
	assert.False(t, exec.Engine().Describe().SupportsCleanEnv)
}
