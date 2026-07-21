//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandboxrunner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/permission"
)

// writeRepo materializes an in-memory file map as a temp repo.
func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestRunChecksLocalDevFailureDoesNotCrash asserts broken repos yield failed runs, not panics.
func TestRunChecksLocalDevFailureDoesNotCrash(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"go.mod": "module broken\n\ngo 1.21\n",
		// Broken syntax so both go test and go vet fail.
		"broken.go": "package broken\n\nfunc Broken() {\n\tif true {\n\t\treturn\n}\n",
	})
	result := RunChecks(context.Background(), Config{
		TaskID:      "test-failure",
		RepoPath:    repo,
		SandboxKind: "local-dev",
		Timeout:     2 * time.Minute,
	})
	if len(result.Runs) != 2 || len(result.Decisions) != 2 {
		t.Fatalf("expected 2 runs and 2 decisions, got %d/%d",
			len(result.Runs), len(result.Decisions))
	}
	for _, d := range result.Decisions {
		if d.Decision != permission.DecisionAllow {
			t.Fatalf("command %q decision %q", d.Command, d.Decision)
		}
	}
	for _, run := range result.Runs {
		if run.Status != "failed" {
			t.Fatalf("broken repo should fail, got: %+v", run)
		}
		if run.ExitCode == 0 {
			t.Fatalf("expected non-zero exit code: %+v", run)
		}
		if run.Error == "" {
			t.Fatalf("failed run should record an error: %+v", run)
		}
	}
}

// TestRunChecksLocalDevTimeoutDoesNotCrash asserts command timeouts degrade to timeout runs.
func TestRunChecksLocalDevTimeoutDoesNotCrash(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"go.mod": "module ok\n\ngo 1.21\n",
		"ok.go":  "package ok\n",
	})
	result := RunChecks(context.Background(), Config{
		TaskID:      "test-timeout",
		RepoPath:    repo,
		SandboxKind: "local-dev",
		Timeout:     time.Millisecond,
	})
	if len(result.Runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(result.Runs))
	}
	for _, run := range result.Runs {
		if run.Status != "timeout" {
			t.Fatalf("expected timeout status, got: %+v", run)
		}
		if run.Error == "" {
			t.Fatalf("timeout run should record an error: %+v", run)
		}
	}
}

// TestRunChecksMockSkipsExecution asserts mock sandboxes never execute commands.
func TestRunChecksMockSkipsExecution(t *testing.T) {
	result := RunChecks(context.Background(), Config{
		TaskID:      "test-mock",
		RepoPath:    t.TempDir(),
		SandboxKind: "mock",
	})
	if len(result.Runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(result.Runs))
	}
	for _, run := range result.Runs {
		if run.Status != "skipped" {
			t.Fatalf("mock run should skip: %+v", run)
		}
	}
}

// TestRunChecksUnsupportedKindSkips asserts unknown sandbox kinds skip all runs.
func TestRunChecksUnsupportedKindSkips(t *testing.T) {
	result := RunChecks(context.Background(), Config{
		TaskID:      "test-unknown",
		RepoPath:    t.TempDir(),
		SandboxKind: "bogus",
	})
	if len(result.Runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(result.Runs))
	}
	for _, run := range result.Runs {
		if run.Status != "skipped" {
			t.Fatalf("unsupported sandbox should skip: %+v", run)
		}
	}
}

// TestRunChecksNoRepoNoRuns asserts absent repos produce no runs or decisions.
func TestRunChecksNoRepoNoRuns(t *testing.T) {
	result := RunChecks(context.Background(), Config{
		TaskID:      "test-empty",
		SandboxKind: "local-dev",
	})
	if len(result.Runs) != 0 || len(result.Decisions) != 0 {
		t.Fatalf("no repo should produce no runs, got %d/%d",
			len(result.Runs), len(result.Decisions))
	}
}

// TestEngineRunStatusClassification covers completed, failed, and timeout mapping.
func TestEngineRunStatusClassification(t *testing.T) {
	start := time.Now()
	ok := engineRun("go vet ./...", start, codeexecutor.RunResult{ExitCode: 0}, nil)
	if ok.Status != "completed" || ok.Error != "" {
		t.Fatalf("zero exit should stay completed: %+v", ok)
	}
	failed := engineRun("go test ./...", start, codeexecutor.RunResult{ExitCode: 1}, nil)
	if failed.Status != "failed" || failed.ExitCode != 1 {
		t.Fatalf("non-zero exit should be failed: %+v", failed)
	}
	if failed.Error == "" {
		t.Fatalf("failed run should record an error: %+v", failed)
	}
	timedOut := engineRun("go test ./...", start,
		codeexecutor.RunResult{ExitCode: 1, TimedOut: true}, nil)
	if timedOut.Status != "timeout" {
		t.Fatalf("timeout should win over exit code: %+v", timedOut)
	}
	errRun := engineRun("go test ./...", start,
		codeexecutor.RunResult{ExitCode: 1}, errors.New("boom"))
	if errRun.Status != "failed" || errRun.Error != "boom" {
		t.Fatalf("engine error should be failed with error: %+v", errRun)
	}
	timedOutErr := engineRun("go test ./...", start,
		codeexecutor.RunResult{ExitCode: 1, TimedOut: true},
		errors.New("context deadline exceeded"))
	if timedOutErr.Status != "timeout" {
		t.Fatalf("timeout should win over engine error: %+v", timedOutErr)
	}
}
