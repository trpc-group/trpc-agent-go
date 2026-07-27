//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package execution

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

func TestContainerHostConfigEnforcesProductionIsolation(t *testing.T) {
	t.Parallel()

	host := ContainerHostConfig()
	if host.NetworkMode != "none" || host.Privileged {
		t.Fatalf("container network/privilege boundary is unsafe: %+v", host)
	}
	if host.PidsLimit == nil || *host.PidsLimit <= 0 || host.Resources.Memory <= 0 || host.Resources.NanoCPUs <= 0 {
		t.Fatalf("container resource limits are incomplete: %+v", host.Resources)
	}
	if !containsString(host.CapDrop, "ALL") || !containsString(host.SecurityOpt, "no-new-privileges") {
		t.Fatalf("container capabilities/security options are incomplete: %+v", host)
	}
}

func TestBoundedSandboxCommandUsesFixedPipefailWrapper(t *testing.T) {
	t.Parallel()

	got := BoundedSandboxCommand("go test ./...", 4096)
	want := "bash -o pipefail -c '{ go test ./...; } 2>&1 | { head -c 4096; cat >/dev/null; }'"
	if got != want {
		t.Fatalf("bounded command = %q, want %q", got, want)
	}
	if unbounded := BoundedSandboxCommand("go test ./...", 0); unbounded != "go test ./..." {
		t.Fatalf("zero-limit command = %q, want original", unbounded)
	}
}

func TestBoundedSandboxCommandPreservesExitStatusAfterLargeOutput(t *testing.T) {
	t.Parallel()

	command := BoundedSandboxCommand("dd if=/dev/zero bs=131072 count=1 2>/dev/null", 1024)
	out, err := exec.Command("bash", "-c", command).CombinedOutput()
	if err != nil {
		t.Fatalf("bounded successful command failed: %v output=%q", err, out)
	}
	if len(out) != 1024 {
		t.Fatalf("bounded output length = %d, want 1024", len(out))
	}

	command = BoundedSandboxCommand("printf failure; exit 7", 1024)
	out, err = exec.Command("bash", "-c", command).CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("failing command error = %v output=%q, want exit 7", err, out)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestSandboxEnvUsesOnlyWhitelistedKeysAndDropsSecrets(t *testing.T) {
	t.Setenv("PATH", "/host/bin")
	t.Setenv("HOME", "/Users/example")
	t.Setenv("TMPDIR", "/tmp/host")
	t.Setenv("OPENAI_API_KEY", "sk-openai-secret-1234567890")
	t.Setenv("DEEPSEEK_API_KEY", "sk-deepseek-secret-1234567890")
	t.Setenv("CR_AGENT_TEST_SECRET", "secret-value")

	env := SandboxEnv(RuntimeLocalFallback)
	for key, value := range env {
		if !AllowedSandboxEnvKey(key) {
			t.Fatalf("sandbox env included non-whitelisted key %q=%q", key, value)
		}
		if strings.Contains(value, "secret") || strings.Contains(value, "sk-") {
			t.Fatalf("sandbox env leaked secret value through %q=%q", key, value)
		}
	}
	for _, forbidden := range []string{"OPENAI_API_KEY", "DEEPSEEK_API_KEY", "CR_AGENT_TEST_SECRET"} {
		if _, ok := env[forbidden]; ok {
			t.Fatalf("sandbox env must not include secret key %q: %+v", forbidden, env)
		}
	}
	if env["GOCACHE"] != GoSandboxCacheDir {
		t.Fatalf("GOCACHE = %q, want %q", env["GOCACHE"], GoSandboxCacheDir)
	}
	if env["PATH"] != "/host/bin" {
		t.Fatalf("local fallback PATH = %q, want host PATH", env["PATH"])
	}
}

func TestSandboxEnvWhitelistMatchesActualEnvKeys(t *testing.T) {
	t.Parallel()

	env := SandboxEnv(RuntimeContainer)
	for _, key := range []string{"PATH", "GOCACHE"} {
		if _, ok := env[key]; !ok {
			t.Fatalf("container env missing expected key %q: %+v", key, env)
		}
		if !strings.Contains(SandboxEnvWhitelist, key) {
			t.Fatalf("audit whitelist %q does not include actual key %q", SandboxEnvWhitelist, key)
		}
	}
	for _, audited := range strings.Split(SandboxEnvWhitelist, ",") {
		if strings.TrimSpace(audited) == "" {
			t.Fatalf("empty env whitelist entry in %q", SandboxEnvWhitelist)
		}
		if !AllowedSandboxEnvKey(strings.TrimSpace(audited)) {
			t.Fatalf("audit whitelist contains non-allowed key %q", audited)
		}
	}
}

func TestContainerSandboxEnvUsesContainerLocalPaths(t *testing.T) {
	t.Setenv("HOME", "/Users/example")
	t.Setenv("TMPDIR", "/var/folders/example-host-tmp")

	env := SandboxEnv(RuntimeContainer)
	if env["HOME"] != "/tmp" {
		t.Fatalf("container HOME = %q, want /tmp", env["HOME"])
	}
	if env["TMPDIR"] != "/tmp" {
		t.Fatalf("container TMPDIR = %q, want /tmp", env["TMPDIR"])
	}
}

func TestContainerGoChecksUnsupportedReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		repoSetup func(t *testing.T, repo string)
		want      string
	}{
		{
			name: "non module repo stays supported",
			repoSetup: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644); err != nil {
					t.Fatalf("write main.go: %v", err)
				}
			},
		},
		{
			name: "go.mod without vendor is unsupported",
			repoSetup: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/plain\n\ngo 1.25.0\n"), 0o644); err != nil {
					t.Fatalf("write go.mod: %v", err)
				}
			},
			want: "vendor/modules.txt",
		},
		{
			name: "go.work without vendor is unsupported",
			repoSetup: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(repo, "go.work"), []byte("go 1.25.0\n"), 0o644); err != nil {
					t.Fatalf("write go.work: %v", err)
				}
			},
			want: "vendor/modules.txt",
		},
		{
			name: "vendored module repo stays supported",
			repoSetup: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/vendored\n\ngo 1.25.0\n"), 0o644); err != nil {
					t.Fatalf("write go.mod: %v", err)
				}
				if err := os.MkdirAll(filepath.Join(repo, "vendor"), 0o755); err != nil {
					t.Fatalf("mkdir vendor: %v", err)
				}
				if err := os.WriteFile(filepath.Join(repo, "vendor", "modules.txt"), []byte("# example.com/vendored\n"), 0o644); err != nil {
					t.Fatalf("write vendor/modules.txt: %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			tc.repoSetup(t, repo)
			reason, err := ContainerGoChecksUnsupportedReason(repo)
			if err != nil {
				t.Fatalf("ContainerGoChecksUnsupportedReason returned error: %v", err)
			}
			if tc.want == "" && reason != "" {
				t.Fatalf("reason = %q, want supported repo", reason)
			}
			if tc.want != "" && !strings.Contains(reason, tc.want) {
				t.Fatalf("reason = %q, want containing %q", reason, tc.want)
			}
		})
	}
}

func TestFakeExecutionRuntimeIsTestOnlyAndSeparateFromLocalFallback(t *testing.T) {
	t.Parallel()

	if RuntimeFakeExecution == RuntimeLocalFallback {
		t.Fatalf("fake execution runtime must not alias local fallback")
	}
	exec, err := NewExecutor(Config{Runtime: RuntimeFakeExecution})
	if err != nil {
		t.Fatalf("NewExecutor fake runtime returned error: %v", err)
	}
	if _, ok := exec.(FakeExecutor); !ok {
		t.Fatalf("expected FakeExecutor, got %T", exec)
	}
	result, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{
			Language: "bash",
			Code:     "echo should-not-run",
		}},
	})
	if err != nil {
		t.Fatalf("fake executor should not error: %v", err)
	}
	if !strings.Contains(result.Output, RuntimeFakeExecution) {
		t.Fatalf("fake executor output should identify test-only runtime, got %q", result.Output)
	}
}

func TestCleanupExecutorRemovesLocalFallbackWorkDirExactlyOnce(t *testing.T) {
	base := t.TempDir()
	t.Setenv("TMPDIR", base)

	exec, err := NewExecutor(Config{Runtime: RuntimeLocalFallback})
	if err != nil {
		t.Fatalf("NewExecutor returned error: %v", err)
	}
	localExec, ok := exec.(*localexec.CodeExecutor)
	if !ok {
		t.Fatalf("expected local executor, got %T", exec)
	}
	workDir := localExec.WorkDir
	if workDir == "" {
		t.Fatal("expected local fallback workdir")
	}
	if err := os.WriteFile(filepath.Join(workDir, "probe.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write probe file: %v", err)
	}

	if err := CleanupExecutor(exec); err != nil {
		t.Fatalf("CleanupExecutor returned error: %v", err)
	}
	if _, err := os.Stat(workDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected workdir %q to be removed, stat err=%v", workDir, err)
	}
	if err := CleanupExecutor(exec); err != nil {
		t.Fatalf("second CleanupExecutor returned error: %v", err)
	}
}

type closeSpyExecutor struct {
	execCalls  int
	closeCalls int
	closeErr   error
	closeStart chan struct{}
	closeAllow chan struct{}
}

func (e *closeSpyExecutor) ExecuteCode(
	context.Context,
	codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	e.execCalls++
	return codeexecutor.CodeExecutionResult{Output: "ok"}, nil
}

func (*closeSpyExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "~~~", End: "~~~"}
}

func (e *closeSpyExecutor) Close() error {
	e.closeCalls++
	if e.closeStart != nil {
		close(e.closeStart)
	}
	if e.closeAllow != nil {
		<-e.closeAllow
	}
	return e.closeErr
}

func TestLazyExecutorDefersFactoryUntilUseAndUnusedClose(t *testing.T) {
	t.Parallel()

	factoryCalls := 0
	exec := NewLazyExecutor(Config{Runtime: RuntimeContainer}, func(Config) (codeexecutor.CodeExecutor, error) {
		factoryCalls++
		return &closeSpyExecutor{}, nil
	})

	if got := exec.CodeBlockDelimiter(); got != defaultCodeBlockDelimiter {
		t.Fatalf("default delimiter = %+v, want %+v", got, defaultCodeBlockDelimiter)
	}
	if factoryCalls != 0 {
		t.Fatalf("factory calls after construction = %d, want 0", factoryCalls)
	}
	if err := exec.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("factory calls after unused Close = %d, want 0", factoryCalls)
	}
}

func TestLazyExecutorInitializesAndClosesUnderlyingExecutorOnce(t *testing.T) {
	t.Parallel()

	spy := &closeSpyExecutor{}
	factoryCalls := 0
	exec := NewLazyExecutor(Config{Runtime: RuntimeContainer}, func(Config) (codeexecutor.CodeExecutor, error) {
		factoryCalls++
		return spy, nil
	})

	result, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{})
	if err != nil {
		t.Fatalf("ExecuteCode returned error: %v", err)
	}
	if result.Output != "ok" {
		t.Fatalf("ExecuteCode output = %q, want ok", result.Output)
	}
	if factoryCalls != 1 || spy.execCalls != 1 {
		t.Fatalf("factory/execution calls = %d/%d, want 1/1", factoryCalls, spy.execCalls)
	}
	if got := exec.CodeBlockDelimiter(); got.Start != "~~~" || got.End != "~~~" {
		t.Fatalf("initialized delimiter = %+v, want spy delimiter", got)
	}
	if err := exec.Close(); err != nil {
		t.Fatalf("first Close returned error: %v", err)
	}
	if err := exec.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
	if spy.closeCalls != 1 {
		t.Fatalf("underlying close calls = %d, want 1", spy.closeCalls)
	}
}

func TestLazyExecutorCloseWaitsForRacingInitializationCleanup(t *testing.T) {
	t.Parallel()

	cleanupErr := errors.New("cleanup failed")
	factoryStarted := make(chan struct{})
	releaseFactory := make(chan struct{})
	spy := &closeSpyExecutor{
		closeErr:   cleanupErr,
		closeStart: make(chan struct{}),
		closeAllow: make(chan struct{}),
	}
	exec := NewLazyExecutor(Config{Runtime: RuntimeContainer}, func(Config) (codeexecutor.CodeExecutor, error) {
		close(factoryStarted)
		<-releaseFactory
		return spy, nil
	})

	executeDone := make(chan error, 1)
	go func() {
		_, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{})
		executeDone <- err
	}()
	<-factoryStarted

	closeDone := make(chan error, 1)
	go func() { closeDone <- exec.Close() }()
	waitForLazyExecutorClosed(t, exec)
	close(releaseFactory)
	<-spy.closeStart

	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before cleanup completed: %v", err)
	default:
	}

	close(spy.closeAllow)
	if err := <-closeDone; !errors.Is(err, cleanupErr) {
		t.Fatalf("Close error = %v, want cleanup error", err)
	}
	if err := <-executeDone; err == nil || !strings.Contains(err.Error(), "executor is closed") {
		t.Fatalf("ExecuteCode error = %v, want closed executor", err)
	}
}

func waitForLazyExecutorClosed(t *testing.T, exec *LazyExecutor) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		exec.mu.Lock()
		closed := exec.closed
		exec.mu.Unlock()
		if closed {
			return
		}
		select {
		case <-deadline:
			t.Fatal("Close did not mark LazyExecutor closed")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}
