//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skillrunner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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
	if runtime.GOOS == "windows" {
		t.Skip("local-dev skill scripts require a POSIX bash workspace")
	}
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

func TestGoEnvE2BDoesNotLeakHostPaths(t *testing.T) {
	if got := goEnv("e2b"); len(got) != 0 {
		t.Fatalf("e2b env leaked host values: %#v", got)
	}
}

func TestE2BSandboxTimeoutCoversRunnableScripts(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want time.Duration
	}{
		{
			name: "default timeout without repository",
			cfg:  Config{},
			want: 2*defaultScriptTimeout + e2bSandboxLifetimeGrace,
		},
		{
			name: "default timeout with repository",
			cfg:  Config{RepoPath: "."},
			want: 3*defaultScriptTimeout + e2bSandboxLifetimeGrace,
		},
		{
			name: "custom timeout with repository",
			cfg: Config{
				RepoPath: ".",
				Timeout:  2 * time.Minute,
			},
			want: 6*time.Minute + e2bSandboxLifetimeGrace,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e2bSandboxTimeout(tt.cfg); got != tt.want {
				t.Fatalf("e2b sandbox timeout = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildScriptsSkipsLocalReplaceOutsideRepo(t *testing.T) {
	repo := t.TempDir()
	writeGoMod(t, repo, `module example.com/review

go 1.24

replace example.com/dependency => ../dependency
`)
	spec := buildScripts(Config{RepoPath: repo, SandboxKind: "container"})[2]
	if spec.skipReason != "go.mod contains a local replacement outside --repo-path" {
		t.Fatalf("skip reason = %q", spec.skipReason)
	}
	if len(spec.inputs) != 0 {
		t.Fatalf("skipped static check staged inputs: %#v", spec.inputs)
	}
}

func TestBuildScriptsAllowsContainedAndVersionedReplaces(t *testing.T) {
	tests := []struct {
		name  string
		goMod string
	}{
		{
			name: "contained local replacement",
			goMod: `module example.com/review

go 1.24

replace example.com/dependency => ./internal/dependency
`,
		},
		{
			name: "versioned module replacement",
			goMod: `module example.com/review

go 1.24

replace example.com/dependency => example.com/fork v1.2.3
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := t.TempDir()
			writeGoMod(t, repo, tt.goMod)
			spec := buildScripts(Config{RepoPath: repo, SandboxKind: "container"})[2]
			if spec.skipReason != "" {
				t.Fatalf("unexpected skip reason %q", spec.skipReason)
			}
			if len(spec.inputs) != 1 || spec.inputs[0].To != repoStageTarget {
				t.Fatalf("container staging inputs: %#v", spec.inputs)
			}
		})
	}
}

func writeGoMod(t *testing.T, repo, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCustomSkillRequiresExplicitApproval(t *testing.T) {
	result := RunScripts(context.Background(), Config{
		TaskID: "custom", SkillsRoot: t.TempDir(), CustomSkills: true,
		SandboxKind: "mock", DiffText: testDiff,
	})
	if result.Err != nil {
		t.Fatalf("blocked custom skill should be an audited result: %v", result.Err)
	}
	if len(result.Decisions) != 1 ||
		result.Decisions[0].Decision != permission.DecisionNeedsHumanReview {
		t.Fatalf("custom skill decision: %+v", result.Decisions)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != "blocked" {
		t.Fatalf("custom skill was not blocked: %+v", result.Runs)
	}
}

func TestApprovedCustomSkillRecordsDigest(t *testing.T) {
	result := RunScripts(context.Background(), Config{
		TaskID: "custom-approved", SkillsRoot: skillsRoot, CustomSkills: true,
		AllowCustomSkills: true, SandboxKind: "mock", DiffText: testDiff,
	})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if !result.SkillLoaded || len(result.Decisions) != 4 {
		t.Fatalf("approved custom skill audit: %+v", result)
	}
	decision := result.Decisions[0]
	if decision.Decision != permission.DecisionAllow ||
		!strings.Contains(decision.Reason, "sha256=") {
		t.Fatalf("custom skill digest decision: %+v", decision)
	}
}
