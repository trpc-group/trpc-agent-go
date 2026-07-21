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
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/permission"
)

const testDiff = `diff --git a/config.go b/config.go
--- a/config.go
+++ b/config.go
@@ -1,3 +1,4 @@
 package config
+const password = "hunter2secretvalue"
 const name = "demo"
`

const skillsRoot = "../skills"

// TestRunScriptsLocalDev runs the bundled skill scripts on the host.
func TestRunScriptsLocalDev(t *testing.T) {
	result := RunScripts(context.Background(), Config{
		TaskID:      "test-local",
		SkillsRoot:  skillsRoot,
		SandboxKind: "local-dev",
		Timeout:     time.Minute,
		DiffText:    testDiff,
	})
	if result.Err != nil {
		t.Fatalf("RunScripts error: %v", result.Err)
	}
	if !result.SkillLoaded {
		t.Fatal("skill was not loaded")
	}
	if result.LoadMessage != "loaded: code-review" {
		t.Fatalf("unexpected load message %q", result.LoadMessage)
	}
	if len(result.Runs) != 3 || len(result.Decisions) != 3 {
		t.Fatalf("expected 3 runs and 3 decisions, got %d/%d",
			len(result.Runs), len(result.Decisions))
	}
	for _, d := range result.Decisions {
		if d.Decision != permission.DecisionAllow {
			t.Fatalf("command %q decision %q", d.Command, d.Decision)
		}
	}

	diffRun := result.Runs[0]
	if diffRun.Status != "completed" || diffRun.ExitCode != 0 {
		t.Fatalf("diff_summary run: %+v", diffRun)
	}
	if !strings.Contains(diffRun.StdoutExcerpt, "files_changed=1") ||
		!strings.Contains(diffRun.StdoutExcerpt, "added_lines=1") {
		t.Fatalf("diff_summary stdout: %q", diffRun.StdoutExcerpt)
	}

	secretRun := result.Runs[1]
	if secretRun.Status != "completed" || secretRun.ExitCode != 0 {
		t.Fatalf("secret_scan run: %+v", secretRun)
	}
	if !strings.Contains(secretRun.StdoutExcerpt, "password") {
		t.Fatalf("secret_scan did not flag the secret: %q",
			secretRun.StdoutExcerpt)
	}
	if strings.Contains(secretRun.StdoutExcerpt, "hunter2secretvalue") {
		t.Fatalf("secret leaked into the excerpt: %q",
			secretRun.StdoutExcerpt)
	}

	staticRun := result.Runs[2]
	if staticRun.Status != "skipped" {
		t.Fatalf("go_static_checks without repo should skip: %+v", staticRun)
	}
}

// TestRunScriptsMock verifies mock mode skips script execution.
func TestRunScriptsMock(t *testing.T) {
	result := RunScripts(context.Background(), Config{
		TaskID:      "test-mock",
		SkillsRoot:  skillsRoot,
		SandboxKind: "mock",
		DiffText:    testDiff,
	})
	if result.Err != nil {
		t.Fatalf("RunScripts error: %v", result.Err)
	}
	if !result.SkillLoaded {
		t.Fatal("skill was not loaded")
	}
	if len(result.Runs) != 3 || len(result.Decisions) != 3 {
		t.Fatalf("expected 3 runs and 3 decisions, got %d/%d",
			len(result.Runs), len(result.Decisions))
	}
	for _, run := range result.Runs {
		if run.Status != "skipped" {
			t.Fatalf("mock run should skip: %+v", run)
		}
	}
}

// TestRunScriptsUnsupportedSandbox verifies unknown sandboxes skip scripts.
func TestRunScriptsUnsupportedSandbox(t *testing.T) {
	result := RunScripts(context.Background(), Config{
		TaskID:      "test-unsupported",
		SkillsRoot:  skillsRoot,
		SandboxKind: "bogus",
		DiffText:    testDiff,
	})
	if result.Err != nil {
		t.Fatalf("RunScripts error: %v", result.Err)
	}
	if len(result.Runs) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(result.Runs))
	}
	for _, run := range result.Runs {
		if run.Status != "skipped" {
			t.Fatalf("unsupported sandbox should skip: %+v", run)
		}
	}
}

// TestRunScriptsUnknownSkill verifies missing skills fail the load step.
func TestRunScriptsUnknownSkill(t *testing.T) {
	result := RunScripts(context.Background(), Config{
		TaskID:     "test-missing",
		SkillsRoot: t.TempDir(),
		DiffText:   testDiff,
	})
	if result.Err == nil {
		t.Fatal("expected a skill load error")
	}
	if result.SkillLoaded {
		t.Fatal("skill should not be loaded")
	}
	if len(result.Runs) != 0 {
		t.Fatalf("no runs expected after load failure, got %d",
			len(result.Runs))
	}
}

// TestScriptRunStatusClassification covers completed, failed, and timeout mapping.
func TestScriptRunStatusClassification(t *testing.T) {
	start := time.Now()
	ok := scriptRun("bash scripts/diff_summary.sh", start,
		runResult{ExitCode: 0, Duration: 5})
	if ok.Status != "completed" || ok.Error != "" {
		t.Fatalf("zero exit should stay completed: %+v", ok)
	}
	failed := scriptRun("bash scripts/secret_scan.sh", start,
		runResult{ExitCode: 2, Duration: 5})
	if failed.Status != "failed" || failed.ExitCode != 2 {
		t.Fatalf("non-zero exit should be failed: %+v", failed)
	}
	if failed.Error == "" {
		t.Fatalf("failed run should record an error: %+v", failed)
	}
	timedOut := scriptRun("bash scripts/go_static_checks.sh", start,
		runResult{ExitCode: 1, TimedOut: true})
	if timedOut.Status != "timeout" {
		t.Fatalf("timeout should win over exit code: %+v", timedOut)
	}
}
