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
	"os"
	"path/filepath"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/permission"
)

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
