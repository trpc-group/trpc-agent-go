//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skillrunner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestContainerSkillIntegration(t *testing.T) {
	if os.Getenv("TRPC_CR_TEST_CONTAINER") != "1" {
		t.Skip("set TRPC_CR_TEST_CONTAINER=1 with Docker running")
	}
	assertSkillIntegration(t, "container")
}

func TestE2BSkillIntegration(t *testing.T) {
	if os.Getenv("TRPC_CR_TEST_E2B") != "1" {
		t.Skip("set TRPC_CR_TEST_E2B=1 with E2B_API_KEY configured")
	}
	if os.Getenv("E2B_API_KEY") == "" {
		t.Fatal("TRPC_CR_TEST_E2B=1 requires E2B_API_KEY")
	}
	assertSkillIntegration(t, "e2b")
}

func assertSkillIntegration(t *testing.T, kind string) {
	t.Helper()
	repo := t.TempDir()
	files := map[string]string{
		"go.mod":        "module skillcheck\n\ngo 1.21\n",
		"check.go":      "package skillcheck\n\nfunc Add(a, b int) int { return a + b }\n",
		"check_test.go": "package skillcheck\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal(\"bad sum\") } }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(repo, name),
			[]byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result := RunScripts(context.Background(), Config{
		TaskID: "skill-integration-" + kind, SkillsRoot: skillsRoot,
		SandboxKind: kind, Timeout: time.Minute, DiffText: testDiff,
		RepoPath: repo,
	})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if len(result.Runs) != 3 {
		t.Fatalf("%s skill runs=%d, want 3", kind, len(result.Runs))
	}
	for _, run := range result.Runs {
		if run.Status != "completed" || run.ExitCode != 0 {
			t.Fatalf("%s skill script failed: %+v", kind, run)
		}
	}
}
