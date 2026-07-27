//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandboxrunner

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestManagedSandboxIntegration(t *testing.T) {
	if os.Getenv("TRPC_CR_TEST_MANAGED") != "1" {
		t.Skip("set TRPC_CR_TEST_MANAGED=1 to run the managed sandbox integration test")
	}
	if runtime.GOOS == "windows" {
		t.Skip("managed OS sandbox is not implemented on Windows")
	}
	assertSandboxIntegration(t, "managed")
}

func TestContainerSandboxIntegration(t *testing.T) {
	if os.Getenv("TRPC_CR_TEST_CONTAINER") != "1" {
		t.Skip("set TRPC_CR_TEST_CONTAINER=1 with Docker running")
	}
	assertSandboxIntegration(t, "container")
}

func TestE2BSandboxIntegration(t *testing.T) {
	if os.Getenv("TRPC_CR_TEST_E2B") != "1" {
		t.Skip("set TRPC_CR_TEST_E2B=1 with E2B_API_KEY configured")
	}
	if os.Getenv("E2B_API_KEY") == "" {
		t.Fatal("TRPC_CR_TEST_E2B=1 requires E2B_API_KEY")
	}
	assertSandboxIntegration(t, "e2b")
}

func assertSandboxIntegration(t *testing.T, kind string) {
	t.Helper()
	repo := writeRepo(t, map[string]string{
		"go.mod":        "module sandboxcheck\n\ngo 1.21\n",
		"check.go":      "package sandboxcheck\n\nfunc Add(a, b int) int { return a + b }\n",
		"check_test.go": "package sandboxcheck\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal(\"bad sum\") } }\n",
	})
	result := RunChecks(context.Background(), Config{
		TaskID: "integration-" + kind, RepoPath: repo, SandboxKind: kind,
		Timeout: 2 * time.Minute,
	})
	if len(result.Runs) != 2 {
		t.Fatalf("%s runs=%d, want 2: %+v", kind, len(result.Runs), result.Runs)
	}
	for _, run := range result.Runs {
		if run.Status != "completed" || run.ExitCode != 0 {
			t.Fatalf("%s sandbox check failed: %+v", kind, run)
		}
	}
}
