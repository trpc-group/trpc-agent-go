//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCheckPermissionDeniesHighRisk(t *testing.T) {
	decision := checkPermission("workspace_exec", "rm -rf /tmp")
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("action = %q, want deny", decision.Action)
	}
}

func TestCheckPermissionAllowsSkillScript(t *testing.T) {
	decision := checkPermission("skill_run", "bash scripts/run_checks.sh work/inputs/changes.diff")
	if decision.Action != tool.PermissionActionAllow {
		t.Fatalf("action = %q, want allow", decision.Action)
	}
}

func TestRunChecksCleanDiff(t *testing.T) {
	diff := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new\n"
	stdout, stderr, code := runChecks(diff)
	if code != 0 {
		t.Fatalf("code = %d stderr = %q", code, stderr)
	}
	if stdout == "" {
		t.Fatal("expected stdout")
	}
}

func TestRunChecksIgnoredError(t *testing.T) {
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n+_ = err\n"
	_, _, code := runChecks(diff)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestRunSandboxFailureDoesNotPanic(t *testing.T) {
	result, err := Run(t.Context(), Options{
		TaskID:  "task-1",
		DiffRaw: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n+_ = err\n",
		Runtime: RuntimeLocal,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != "failed" {
		t.Fatalf("runs = %+v", result.Runs)
	}
	if result.Exceptions["check_failed"] != 1 {
		t.Fatalf("exceptions = %+v", result.Exceptions)
	}
	if result.DenyCount < 2 {
		t.Fatalf("deny count = %d, want at least 2 probe denials", result.DenyCount)
	}
}
