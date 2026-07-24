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
	result := RunScripts(context.Background(), Config{
		TaskID: "skill-integration-" + kind, SkillsRoot: skillsRoot,
		SandboxKind: kind, Timeout: time.Minute, DiffText: testDiff,
	})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if len(result.Runs) != 3 {
		t.Fatalf("%s skill runs=%d, want 3", kind, len(result.Runs))
	}
	for _, run := range result.Runs[:2] {
		if run.Status != "completed" || run.ExitCode != 0 {
			t.Fatalf("%s skill script failed: %+v", kind, run)
		}
	}
}
