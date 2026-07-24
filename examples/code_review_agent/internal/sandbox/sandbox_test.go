//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sandbox

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// newLocalExecutor builds a local-backend Executor for tests. The local
// backend is used because the test environment may not have Docker/E2B.
func newLocalExecutor(t *testing.T, timeout time.Duration) *Executor {
	t.Helper()
	ex, err := New(Config{
		Backend:     BackendLocal,
		UnsafeLocal: true,
		WorkDir:     t.TempDir(),
		Timeout:     timeout,
	})
	if err != nil {
		t.Fatalf("New local executor: %v", err)
	}
	return ex
}

// mockEngine implements codeexecutor.Engine for fail-closed testing. Its
// Manager/FS/Runner are nil so any Run that reaches them would fail the
// test loudly instead of silently executing.
type mockEngine struct {
	caps   codeexecutor.Capabilities
	runner codeexecutor.ProgramRunner
}

func (m *mockEngine) Manager() codeexecutor.WorkspaceManager { return nil }
func (m *mockEngine) FS() codeexecutor.WorkspaceFS           { return nil }
func (m *mockEngine) Runner() codeexecutor.ProgramRunner     { return m.runner }
func (m *mockEngine) Describe() codeexecutor.Capabilities    { return m.caps }

// TestRun_LocalGoVersion_Success runs `go version` on the local backend and
// verifies a successful result with non-empty stdout.
func TestRun_LocalGoVersion_Success(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go not found on PATH; skipping success test: %v", err)
	}
	ex := newLocalExecutor(t, 30*time.Second)
	ctx := context.Background()

	ws, err := ex.CreateWorkspace(ctx)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	defer func() { _ = ex.Close(ctx, ws) }()

	res, err := ex.Run(ctx, ws, RunSpec{Cmd: "go", Args: []string{"version"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusSuccess {
		t.Fatalf("status = %q, want %q (stderr: %q)", res.Status, StatusSuccess, res.Stderr)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
	if len(res.Stdout) == 0 {
		t.Fatal("Stdout is empty; expected go version output")
	}
}

// TestLimitedRead_Truncates feeds a 2 MiB reader with a 1 MiB cap and
// verifies the result is capped to 1 MiB with truncated=true. It also
// checks the non-truncated path returns the full payload.
func TestLimitedRead_Truncates(t *testing.T) {
	const max = 1 << 20                                      // 1 MiB
	payload := strings.NewReader(strings.Repeat("a", 2<<20)) // 2 MiB

	b, truncated := limitedRead(payload, max)
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if int64(len(b)) != max {
		t.Fatalf("len = %d, want %d", len(b), max)
	}

	// Non-truncated case: small payload under the cap.
	small, smallTrunc := limitedRead(strings.NewReader("hello"), max)
	if smallTrunc {
		t.Fatal("truncated = true for small payload, want false")
	}
	if string(small) != "hello" {
		t.Fatalf("got %q, want %q", small, "hello")
	}
}

// TestRun_FailClosedOnNoCleanEnv verifies Run refuses to execute when the
// backend does not support CleanEnv, returning an error and never reaching
// the runner.
func TestRun_FailClosedOnNoCleanEnv(t *testing.T) {
	eng := &mockEngine{caps: codeexecutor.Capabilities{SupportsCleanEnv: false}}
	ex := &Executor{
		eng: eng,
		cfg: Config{Timeout: time.Second, MaxStdoutBytes: 1024, MaxStderrBytes: 1024},
	}

	_, err := ex.Run(
		context.Background(),
		codeexecutor.Workspace{ID: "mock", Path: "/nonexistent"},
		RunSpec{Cmd: "echo"},
	)
	if err == nil {
		t.Fatal("expected error when SupportsCleanEnv=false, got nil")
	}
	if !strings.Contains(err.Error(), "CleanEnv") {
		t.Fatalf("error should mention CleanEnv, got: %v", err)
	}
}

// TestRun_Timeout runs a long-running command with a short timeout and
// verifies status=timeout and TimedOut=true. Skipped on Windows where the
// `sleep` command is not available as a standalone executable.
func TestRun_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("timeout test uses the 'sleep' command, which is not available on Windows")
	}
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skipf("sleep not found on PATH: %v", err)
	}
	ex := newLocalExecutor(t, 100*time.Millisecond)
	ctx := context.Background()

	ws, err := ex.CreateWorkspace(ctx)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	defer func() { _ = ex.Close(ctx, ws) }()

	res, err := ex.Run(ctx, ws, RunSpec{Cmd: "sleep", Args: []string{"10"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusTimeout {
		t.Fatalf("status = %q, want %q", res.Status, StatusTimeout)
	}
	if !res.TimedOut {
		t.Fatal("TimedOut = false, want true")
	}
}

// TestRun_FailureDoesNotPanic runs a command that cannot start and verifies
// Run classifies it as failed without panicking.
func TestRun_FailureDoesNotPanic(t *testing.T) {
	ex := newLocalExecutor(t, 30*time.Second)
	ctx := context.Background()

	ws, err := ex.CreateWorkspace(ctx)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	defer func() { _ = ex.Close(ctx, ws) }()

	res, err := ex.Run(ctx, ws, RunSpec{Cmd: "definitely-not-a-real-command-xyz-12345"})
	if err != nil {
		t.Fatalf("Run returned unexpected error (should classify failure, not error out): %v", err)
	}
	if res.Status != StatusFailed {
		t.Fatalf("status = %q, want %q", res.Status, StatusFailed)
	}
	if res.ExitCode == 0 {
		t.Fatal("ExitCode = 0, want non-zero for a failed command")
	}
}

// TestNew_LocalRequiresUnsafeLocal verifies New refuses the local backend
// when UnsafeLocal is false (fail-closed against accidental local execution).
func TestNew_LocalRequiresUnsafeLocal(t *testing.T) {
	_, err := New(Config{Backend: BackendLocal, WorkDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error when BackendLocal without UnsafeLocal, got nil")
	}
	if !strings.Contains(err.Error(), "UnsafeLocal") {
		t.Fatalf("error should mention UnsafeLocal, got: %v", err)
	}
}

// TestBuildSandboxEnv_NoHostLeak ensures host GOPROXY/GOPATH/PATH are not
// copied into the sandbox environment.
func TestBuildSandboxEnv_NoHostLeak(t *testing.T) {
	t.Setenv("GOPROXY", "https://user:s3cret@proxy.example/go")
	t.Setenv("GOPATH", "/evil/gopath")
	t.Setenv("GOCACHE", "/evil/gocache")
	t.Setenv("PATH", "/evil/bin:/usr/bin")

	ws := codeexecutor.Workspace{Path: t.TempDir()}
	env := buildSandboxEnv(ws, nil)
	if env["GOPROXY"] != "off" {
		t.Fatalf("GOPROXY = %q, want off", env["GOPROXY"])
	}
	if env["GOPATH"] == "/evil/gopath" {
		t.Fatal("host GOPATH leaked into sandbox env")
	}
	if env["GOCACHE"] == "/evil/gocache" {
		t.Fatal("host GOCACHE leaked into sandbox env")
	}
	if env["PATH"] == "/evil/bin:/usr/bin" {
		t.Fatal("host PATH leaked into sandbox env")
	}
	if env["GOPATH"] != filepath.Join(ws.Path, ".gopath") {
		t.Fatalf("GOPATH = %q, want workspace-local", env["GOPATH"])
	}
}
