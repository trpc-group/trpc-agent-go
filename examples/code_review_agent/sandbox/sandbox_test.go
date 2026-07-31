//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

func TestNewExecutorDefaultsToContainer(t *testing.T) {
	executor := NewExecutor(t.TempDir())
	if executor.config.ExecutorType != ExecutorContainer {
		t.Fatalf("ExecutorType = %s, want container", executor.config.ExecutorType)
	}
	if executor.config.Timeout != 5*time.Minute {
		t.Fatalf("Timeout = %v, want 5m", executor.config.Timeout)
	}
	if executor.config.MaxOutputSize != 1024*1024 {
		t.Fatalf("MaxOutputSize = %d, want 1048576", executor.config.MaxOutputSize)
	}
}

func TestNewExecutorWithType(t *testing.T) {
	executor := NewExecutorWithType(t.TempDir(), ExecutorLocal)
	if executor.config.ExecutorType != ExecutorLocal {
		t.Fatalf("ExecutorType = %s, want local", executor.config.ExecutorType)
	}
}

func TestDefaultSandboxConfig(t *testing.T) {
	config := DefaultSandboxConfig()
	if config.ExecutorType != ExecutorContainer {
		t.Fatalf("ExecutorType = %s, want container", config.ExecutorType)
	}
	if len(config.EnvWhitelist) == 0 {
		t.Fatal("EnvWhitelist should not be empty")
	}
}

func TestExecutorRejectsUnknownExecutor(t *testing.T) {
	executor := NewExecutorWithType(t.TempDir(), ExecutorType("unknown"))
	if _, err := executor.RunAllChecks(context.Background(), "task_unknown"); err == nil {
		t.Fatal("RunAllChecks() should reject unknown executor")
	}
}

func TestExecutorE2BRequiresConfiguration(t *testing.T) {
	t.Setenv("E2B_API_KEY", "")
	executor := NewExecutorWithType(t.TempDir(), ExecutorE2B)
	if _, err := executor.RunAllChecks(context.Background(), "task_e2b"); err == nil {
		t.Fatal("RunAllChecks() should reject E2B without API key")
	}
}

func TestExecutorRejectsEmptyRepository(t *testing.T) {
	executor := NewExecutorWithType("", ExecutorLocal)
	if _, err := executor.RunAllChecks(context.Background(), "task_empty"); err == nil {
		t.Fatal("RunAllChecks() should reject an empty repository path")
	}
}

func TestLimitedBuffer(t *testing.T) {
	buffer := newLimitedBuffer(5)
	if n, err := buffer.Write([]byte("hello world")); err != nil || n != 11 {
		t.Fatalf("Write() = %d, %v", n, err)
	}
	if !buffer.Truncated() || len(buffer.String()) <= 5 {
		t.Fatalf("buffer did not record truncation: %q", buffer.String())
	}
}

func TestExecutorFactoryIsUsedByNativeAdapter(t *testing.T) {
	executor := NewExecutorWithType(t.TempDir(), ExecutorLocal)
	called := false
	executor.WithExecutorFactory(func() (codeexecutor.CodeExecutor, error) {
		called = true
		return nil, nil
	})
	_, _ = executor.RunAllChecks(context.Background(), "task_factory")
	if !called {
		t.Fatal("native executor factory was not called")
	}
}

func TestRealContainerWorkspace(t *testing.T) {
	if os.Getenv("GOLENS_RUN_REAL_SANDBOX") != "1" {
		t.Skip("set GOLENS_RUN_REAL_SANDBOX=1 to run Docker integration tests")
	}
	repo := writeMinimalGoRepo(t)
	runs, err := NewExecutorWithType(repo, ExecutorContainer).RunAllChecks(context.Background(), "task_real_container")
	if err != nil {
		t.Fatalf("container workspace execution failed: %v", err)
	}
	assertSuccessfulChecks(t, runs)
}

func TestRealE2BWorkspace(t *testing.T) {
	if os.Getenv("GOLENS_RUN_REAL_SANDBOX") != "1" {
		t.Skip("set GOLENS_RUN_REAL_SANDBOX=1 to run E2B integration tests")
	}
	if os.Getenv("E2B_API_KEY") == "" {
		t.Skip("E2B_API_KEY is required for E2B integration tests")
	}
	repo := writeMinimalGoRepo(t)
	runs, err := NewExecutorWithType(repo, ExecutorE2B).RunAllChecks(context.Background(), "task_real_e2b")
	if err != nil {
		t.Fatalf("E2B workspace execution failed: %v", err)
	}
	assertSuccessfulChecks(t, runs)
}

func writeMinimalGoRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/sandboxfixture\n\ngo 1.25\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package fixture\n\nfunc Add(a, b int) int { return a + b }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func assertSuccessfulChecks(t *testing.T, runs []store.SandboxRun) {
	t.Helper()
	if len(runs) == 0 {
		t.Fatal("no sandbox runs were recorded")
	}
	for _, run := range runs {
		if run.ExitCode != 0 {
			t.Fatalf("%s failed: command=%q exit=%d stdout=%q stderr=%q error_type=%q", run.ScriptName, run.Command, run.ExitCode, run.Stdout, run.Stderr, run.ErrorType)
		}
	}
}
