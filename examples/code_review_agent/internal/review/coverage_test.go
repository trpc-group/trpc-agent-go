//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

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
)

func TestGitWorkingDiffIncludesTrackedAndUntrackedFiles(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "review@example.com")
	runGit(t, repo, "config", "user.name", "Review Test")
	writeFile(t, filepath.Join(repo, "go.mod"), "module example.com/review\n\ngo 1.23\n")
	writeFile(t, filepath.Join(repo, "tracked.go"), "package review\n\nconst tracked = 1\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "baseline")
	writeFile(t, filepath.Join(repo, "tracked.go"), "package review\n\nconst tracked = 2\n")
	writeFile(t, filepath.Join(repo, "untracked.go"), "package review\n\nconst untracked = 3\n")

	raw, err := gitWorkingDiff(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseUnifiedDiff(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Summary.FilesChanged != 2 || !containsString(parsed.Files, "tracked.go") || !containsString(parsed.Files, "untracked.go") {
		t.Fatalf("working diff missed files: %+v", parsed)
	}
}

func TestFileListBuildsBoundedSyntheticDiff(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "nested", "file.go"), "package nested\n\nconst value = 1\n")
	list := filepath.Join(repo, "files.txt")
	writeFile(t, list, "nested/file.go\n\n")
	raw, err := diffFromFileList(repo, list)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseUnifiedDiff(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Summary.FilesChanged != 1 || parsed.Lines[0].Package != "nested" {
		t.Fatalf("unexpected synthetic diff: %+v", parsed)
	}
}

func TestReadBoundedRejectsOversizeInput(t *testing.T) {
	file := filepath.Join(t.TempDir(), "large.diff")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxInputBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readBounded(file); err == nil {
		t.Fatal("expected input-size rejection")
	}
}

func TestNewSandboxModes(t *testing.T) {
	base, err := exampleDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "sandbox", "Dockerfile")); err != nil {
		t.Fatalf("container executor Dockerfile is missing: %v", err)
	}
	fake, err := newSandbox(context.Background(), Config{Executor: "fake", Timeout: time.Second, OutputLimit: 128}, base)
	if err != nil || fake.engine != nil || fake.executor != "fake" {
		t.Fatalf("unexpected fake sandbox: %+v, %v", fake, err)
	}
	if _, err := newSandbox(context.Background(), Config{Executor: "local"}, base); err == nil {
		t.Fatal("local fallback was accepted without opt-in")
	}
	local, err := newSandbox(context.Background(), Config{Executor: "local", AllowLocal: true, Timeout: time.Second, OutputLimit: 128}, base)
	if err != nil || local.engine == nil || local.executor != "local-dev-fallback" {
		t.Fatalf("unexpected local sandbox: %+v, %v", local, err)
	}
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := newSandbox(context.Background(), Config{Executor: "unknown"}, base); err == nil {
		t.Fatal("unknown executor was accepted")
	}
}

func TestRemoteSandboxFactoriesAndCapabilityGate(t *testing.T) {
	originalContainer, originalE2B := containerFactory, e2bFactory
	t.Cleanup(func() { containerFactory, e2bFactory = originalContainer, originalE2B })
	containerFactory = func(context.Context, Config, string) (codeexecutor.Engine, func() error, error) {
		return nil, nil, errors.New("docker unavailable")
	}
	e2bFactory = func(context.Context, Config, string) (codeexecutor.Engine, func() error, error) {
		return stubEngine{runner: stubRunner{}, cleanEnv: true}, func() error { return nil }, nil
	}
	base, _ := exampleDir()
	container, err := newSandbox(context.Background(), Config{Executor: "container"}, base)
	if err != nil || container.initErr == nil {
		t.Fatalf("container initialization failure was not retained: %+v, %v", container, err)
	}
	e2b, err := newSandbox(context.Background(), Config{Executor: "e2b", Timeout: time.Second}, base)
	if err != nil || e2b.engine == nil || e2b.executor != "e2b" {
		t.Fatalf("unexpected e2b sandbox: %+v, %v", e2b, err)
	}
	if err := e2b.Close(); err != nil {
		t.Fatal(err)
	}
	containerFactory = func(context.Context, Config, string) (codeexecutor.Engine, func() error, error) {
		return stubEngine{runner: stubRunner{}, cleanEnv: false}, func() error { return nil }, nil
	}
	container, err = newSandbox(context.Background(), Config{Executor: "container"}, base)
	if err != nil || container.initErr == nil || container.engine != nil {
		t.Fatalf("clean-environment capability was not enforced: %+v, %v", container, err)
	}
}

func TestLocalSandboxRunsAuditedChecks(t *testing.T) {
	base, _ := exampleDir()
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module example.com/smoke\n\ngo 1.23\n")
	writeFile(t, filepath.Join(repo, "smoke.go"), "package smoke\n\nfunc Add(a, b int) int { return a+b }\n")
	writeFile(t, filepath.Join(repo, "smoke_test.go"), "package smoke\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1,2) != 3 { t.Fatal(\"sum\") } }\n")
	runner, err := newSandbox(context.Background(), Config{Executor: "local", AllowLocal: true, Timeout: 20 * time.Second, OutputLimit: 4096, OutputDir: t.TempDir()}, base)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	input, err := ParseUnifiedDiff("diff --git a/smoke.go b/smoke.go\n--- a/smoke.go\n+++ b/smoke.go\n@@ -1 +1,2 @@\n package smoke\n+const changed = true\n")
	if err != nil {
		t.Fatal(err)
	}
	runs, decisions, artifacts, err := runner.run(context.Background(), "local-smoke", repo, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 4 || len(decisions) != 4 || len(artifacts) != 1 {
		t.Fatalf("incomplete sandbox evidence: runs=%+v decisions=%+v artifacts=%+v", runs, decisions, artifacts)
	}
	for _, run := range runs {
		if run.Command == "go" && run.Status != "success" {
			t.Fatalf("Go check failed: %+v", run)
		}
	}
}

func TestExecuteClassifiesResults(t *testing.T) {
	cases := []struct {
		name      string
		command   string
		result    codeexecutor.RunResult
		err       error
		status    string
		errorType string
	}{
		{name: "success", command: "go", result: codeexecutor.RunResult{Stdout: "token=\"ghp_abcdefghijklmnopqrstuvwxyz123456\" and more output"}, status: "success"},
		{name: "non-zero", command: "go", result: codeexecutor.RunResult{ExitCode: 2}, status: "failed", errorType: "non_zero_exit"},
		{name: "timeout result", command: "go", result: codeexecutor.RunResult{TimedOut: true}, status: "failed", errorType: "timeout"},
		{name: "deadline error", command: "go", err: context.DeadlineExceeded, status: "failed", errorType: "timeout"},
		{name: "staticcheck unavailable", command: "staticcheck", result: codeexecutor.RunResult{ExitCode: -1, Stderr: "not found"}, status: "skipped", errorType: "tool_unavailable"},
		{name: "dependency unavailable", command: "go", result: codeexecutor.RunResult{ExitCode: 1, Stderr: "missing go.sum entry for module"}, status: "skipped", errorType: "dependency_unavailable"},
		{name: "staticcheck timeout is not unavailable", command: "staticcheck", result: codeexecutor.RunResult{TimedOut: true, Stderr: "not found"}, status: "failed", errorType: "timeout"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			runner := &sandbox{engine: stubEngine{runner: stubRunner{result: test.result, err: test.err}}, executor: "stub", timeout: time.Second, outputLimit: 24}
			got := runner.execute(context.Background(), codeexecutor.Workspace{}, test.command, []string{"./..."}, ".")
			if string(got.Status) != test.status || string(got.ErrorType) != test.errorType {
				t.Fatalf("unexpected result: %+v", got)
			}
			if strings.Contains(got.Stdout, "ghp_") {
				t.Fatalf("stdout was not redacted: %q", got.Stdout)
			}
		})
	}
}

func TestSandboxSetupFailuresRemainAuditable(t *testing.T) {
	base, _ := exampleDir()
	input, err := ParseUnifiedDiff("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1,2 @@\n package a\n+const value = 1\n")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		repo string
		eng  codeexecutor.Engine
		op   string
	}{
		{name: "create workspace", eng: stubEngine{manager: stubManager{createErr: errors.New("create")}, cleanEnv: true}, op: "create_workspace"},
		{name: "stage skill", eng: stubEngine{manager: stubManager{}, fs: &stubFS{failStageCall: 1}, cleanEnv: true}, op: "stage_skill"},
		{name: "stage diff", eng: stubEngine{manager: stubManager{}, fs: &stubFS{putErr: errors.New("put")}, cleanEnv: true}, op: "stage_diff"},
		{name: "snapshot repo", repo: filepath.Join(t.TempDir(), "missing"), eng: stubEngine{manager: stubManager{}, fs: &stubFS{}, cleanEnv: true}, op: "snapshot_repo"},
		{name: "stage repo", repo: makeTinyRepo(t), eng: stubEngine{manager: stubManager{}, fs: &stubFS{failStageCall: 2}, cleanEnv: true}, op: "stage_repo"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			runner := &sandbox{engine: test.eng, executor: "stub", timeout: time.Second, outputLimit: 128, outputDir: t.TempDir(), skillDir: filepath.Join(base, "skills", "code-review")}
			runs, decisions, artifacts, err := runner.run(context.Background(), "setup-failure", test.repo, input)
			if err != nil {
				t.Fatal(err)
			}
			if len(runs) != 1 || runs[0].Command != test.op || runs[0].ErrorType != "setup_error" {
				t.Fatalf("unexpected setup failure: %+v", runs)
			}
			if len(decisions) == 0 || len(artifacts) != 1 {
				t.Fatalf("audit evidence missing: decisions=%+v artifacts=%+v", decisions, artifacts)
			}
		})
	}
}

func TestDiffStatsWriteFailureIsPropagated(t *testing.T) {
	outputFile := filepath.Join(t.TempDir(), "output-file")
	writeFile(t, outputFile, "not a directory")
	runner := &sandbox{executor: ExecutorFake, outputDir: outputFile}
	if _, _, _, err := runner.run(context.Background(), "task", "", ParsedInput{}); err == nil {
		t.Fatal("diff statistics write failure was ignored")
	}
}

func TestWorkspaceCleanupFailureIsAudited(t *testing.T) {
	base, err := exampleDir()
	if err != nil {
		t.Fatal(err)
	}
	runner := &sandbox{
		engine:   stubEngine{manager: stubManager{cleanupErr: errors.New("cleanup failed")}, fs: &stubFS{}, runner: stubRunner{}, cleanEnv: true},
		executor: ExecutorContainer, timeout: time.Second, outputLimit: 128, outputDir: t.TempDir(), skillDir: filepath.Join(base, "skills", "code-review"),
	}
	runs, _, _, err := runner.run(context.Background(), "cleanup-failure", "", ParsedInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[1].Command != "cleanup_workspace" || runs[1].ErrorType != "setup_error" {
		t.Fatalf("cleanup failure was not audited: %+v", runs)
	}
}

func TestReviewEnvironmentAndErrorClassification(t *testing.T) {
	if reviewEnvironment("container")["GOPROXY"] != "off" {
		t.Fatal("container environment permits dependency network access")
	}
	if reviewEnvironment("local-dev-fallback")["PATH"] == "" {
		t.Fatal("local environment omitted PATH")
	}
	if classifyExecutionError(errors.New("executor broke")) != "executor_error" || classifyExecutionError(context.DeadlineExceeded) != "timeout" {
		t.Fatal("execution errors were misclassified")
	}
}

func TestLoadInputModesAndConfigurationDefaults(t *testing.T) {
	base, _ := exampleDir()
	if _, _, err := loadInput(context.Background(), Config{}, base); err == nil {
		t.Fatal("missing input mode was accepted")
	}
	fixture, mode, err := loadInput(context.Background(), Config{Fixture: "clean"}, base)
	if err != nil || mode != "fixture:clean" || fixture.Summary.FilesChanged != 2 {
		t.Fatalf("unexpected fixture input: %+v %q %v", fixture, mode, err)
	}
	diffFile := filepath.Join(t.TempDir(), "change.diff")
	writeFile(t, diffFile, "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1,2 @@\n package a\n+const value = 1\n")
	if _, mode, err = loadInput(context.Background(), Config{DiffFile: diffFile}, base); err != nil || mode != "diff_file" {
		t.Fatalf("unexpected diff-file input: %q %v", mode, err)
	}
	cfg := Config{}
	if err := normalizeConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Executor != "container" || cfg.Timeout <= 0 || cfg.OutputLimit <= 0 || cfg.DatabasePath == "" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	for _, invalid := range []Config{{OutputLimit: 2 << 20}, {TaskID: "bad/id"}, {TaskID: strings.Repeat("a", 81)}} {
		if err := normalizeConfig(&invalid); err == nil {
			t.Fatalf("invalid configuration accepted: %+v", invalid)
		}
	}
}

func TestConclusionAndMetricsVariants(t *testing.T) {
	critical := Report{Findings: []Finding{{Severity: "critical"}}}
	actionable := Report{Findings: []Finding{{Severity: "high"}}}
	human := Report{NeedsHumanReview: []Finding{{Severity: "low"}}}
	clean := Report{}
	for report, phrase := range map[*Report]string{&critical: "Critical", &actionable: "actionable", &human: "human", &clean: "No actionable"} {
		if !strings.Contains(conclusion(*report), phrase) {
			t.Fatalf("conclusion %q missing %q", conclusion(*report), phrase)
		}
	}
	report := Report{
		Findings: []Finding{{Severity: "high"}}, Warnings: []Finding{{Severity: "low"}}, NeedsHumanReview: []Finding{{Severity: "medium"}},
		SandboxRuns:         []SandboxRun{{DurationMS: 5, ErrorType: "timeout"}},
		PermissionDecisions: []PermissionDecision{{Action: "deny"}, {Action: "ask"}},
	}
	metrics := collectMetrics(time.Now(), report)
	if metrics.PermissionDenyCount != 1 || metrics.PermissionAskCount != 1 || metrics.SandboxDurationMS != 5 || metrics.SeverityDistribution["high"] != 1 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
	if firstNonEmpty("", "fallback") != "fallback" || firstNonEmpty() != "unknown" {
		t.Fatal("firstNonEmpty fallback behavior changed")
	}
	if firstProductionGo([]string{"only_test.go"}) != "" || missingTests([]string{"README.md"}) {
		t.Fatal("non-production inputs were classified as production Go changes")
	}
}

func TestReportAndStoreErrorPaths(t *testing.T) {
	if err := atomicWrite(filepath.Join(t.TempDir(), "missing", "report"), []byte("x")); err == nil {
		t.Fatal("atomic write unexpectedly created a missing parent")
	}
	huge := Report{Task: Task{ID: "huge"}, Findings: []Finding{{Title: strings.Repeat("x", maxArtifactBytes+1)}}, Metrics: Metrics{SeverityDistribution: map[string]int{}}}
	if _, _, err := publish(huge, t.TempDir()); err == nil {
		t.Fatal("oversized report was published")
	}
	parentFile := filepath.Join(t.TempDir(), "parent")
	writeFile(t, parentFile, "not a directory")
	if _, err := openStore(filepath.Join(parentFile, "reviews.sqlite")); err == nil {
		t.Fatal("database under a regular file was opened")
	}
	dbPath := filepath.Join(t.TempDir(), "reviews.sqlite")
	store, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Load(context.Background(), "missing"); err == nil {
		t.Fatal("missing report was returned")
	}
	if _, err := store.LoadTask(context.Background(), "missing"); err == nil {
		t.Fatal("missing task was returned")
	}
	if _, err := store.LoadMetrics(context.Background(), "missing"); err == nil {
		t.Fatal("missing metrics were returned")
	}
}

func TestStageReportAndSkillLoadingRejectInvalidRoots(t *testing.T) {
	parentFile := filepath.Join(t.TempDir(), "parent")
	writeFile(t, parentFile, "not a directory")
	report := Report{Task: Task{ID: "task"}, Metrics: Metrics{SeverityDistribution: map[string]int{}}}
	if _, _, _, err := stageReport(report, parentFile); err == nil {
		t.Fatal("report was staged below a regular file")
	}
	if err := loadReviewSkill(t.TempDir()); err == nil {
		t.Fatal("missing review skill was accepted")
	}
}

func TestSnapshotRejectsOversizeGoFile(t *testing.T) {
	repo := t.TempDir()
	file := filepath.Join(repo, "large.go")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxSnapshotBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, cleanup, err := safeSnapshot(repo); err == nil {
		cleanup()
		t.Fatal("oversized snapshot was accepted")
	}
}

type stubEngine struct {
	runner   codeexecutor.ProgramRunner
	cleanEnv bool
	manager  codeexecutor.WorkspaceManager
	fs       codeexecutor.WorkspaceFS
}

func (s stubEngine) Manager() codeexecutor.WorkspaceManager { return s.manager }
func (s stubEngine) FS() codeexecutor.WorkspaceFS           { return s.fs }
func (s stubEngine) Runner() codeexecutor.ProgramRunner     { return s.runner }
func (s stubEngine) Describe() codeexecutor.Capabilities {
	return codeexecutor.Capabilities{SupportsCleanEnv: s.cleanEnv}
}

type stubRunner struct {
	result codeexecutor.RunResult
	err    error
}

type stubManager struct {
	createErr  error
	cleanupErr error
}

func (s stubManager) CreateWorkspace(context.Context, string, codeexecutor.WorkspacePolicy) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{Path: "stub"}, s.createErr
}
func (s stubManager) Cleanup(context.Context, codeexecutor.Workspace) error { return s.cleanupErr }

type stubFS struct {
	stageCalls    int
	failStageCall int
	putErr        error
}

func (s *stubFS) PutFiles(context.Context, codeexecutor.Workspace, []codeexecutor.PutFile) error {
	return s.putErr
}
func (s *stubFS) StageDirectory(context.Context, codeexecutor.Workspace, string, string, codeexecutor.StageOptions) error {
	s.stageCalls++
	if s.stageCalls == s.failStageCall {
		return errors.New("stage")
	}
	return nil
}
func (*stubFS) Collect(context.Context, codeexecutor.Workspace, []string) ([]codeexecutor.File, error) {
	return nil, nil
}
func (*stubFS) StageInputs(context.Context, codeexecutor.Workspace, []codeexecutor.InputSpec) error {
	return nil
}
func (*stubFS) CollectOutputs(context.Context, codeexecutor.Workspace, codeexecutor.OutputSpec) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}

func (s stubRunner) RunProgram(context.Context, codeexecutor.Workspace, codeexecutor.RunProgramSpec) (codeexecutor.RunResult, error) {
	return s.result, s.err
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func makeTinyRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module example.com/tiny\n\ngo 1.23\n")
	writeFile(t, filepath.Join(repo, "tiny.go"), "package tiny\n")
	return repo
}
