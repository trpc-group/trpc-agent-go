//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

	raw, decisions, err := gitWorkingDiff(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 2 || decisions[0].Action != PermissionAllow || decisions[1].Action != PermissionAllow {
		t.Fatalf("git input operations were not audited: %+v", decisions)
	}
	parsed, err := ParseUnifiedDiff(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Summary.FilesChanged != 2 || !containsString(parsed.Files, "tracked.go") || !containsString(parsed.Files, "untracked.go") {
		t.Fatalf("working diff missed files: %+v", parsed)
	}
}

func TestGitWorkingDiffRejectsNonRepository(t *testing.T) {
	_, decisions, err := gitWorkingDiff(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("non-repository was accepted as git input")
	}
	if len(decisions) != 2 || decisions[0].Action != PermissionAllow || decisions[1].Action != PermissionAllow {
		t.Fatalf("failed git input did not retain audited decisions: %+v", decisions)
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

func TestLoadInputExercisesSupportedModesAndRejectsAmbiguity(t *testing.T) {
	base, err := exampleDir()
	if err != nil {
		t.Fatal(err)
	}
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1,2 @@\n package a\n+const changed = true\n"
	diffPath := filepath.Join(t.TempDir(), "change.diff")
	writeFile(t, diffPath, diff)
	parsed, mode, _, err := loadInput(context.Background(), Config{DiffFile: "  " + diffPath + "  "}, base)
	if err != nil || mode != "diff_file" || parsed.Summary.GoFiles != 1 {
		t.Fatalf("diff-file mode = parsed=%+v mode=%q err=%v", parsed, mode, err)
	}

	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "nested", "file.go"), "package nested\n\nconst Value = 1\n")
	list := filepath.Join(repo, "files.txt")
	writeFile(t, list, "nested/file.go\n")
	parsed, mode, _, err = loadInput(context.Background(), Config{RepoPath: repo, FileList: list}, base)
	if err != nil || mode != "file_list" || len(parsed.Hunks) != 1 || parsed.Hunks[0].Package != "nested" {
		t.Fatalf("file-list mode = parsed=%+v mode=%q err=%v", parsed, mode, err)
	}

	if _, _, _, err := loadInput(context.Background(), Config{Fixture: "../clean"}, base); err == nil {
		t.Fatal("fixture traversal was accepted")
	}
	if _, _, _, err := loadInput(context.Background(), Config{DiffFile: diffPath, Fixture: "clean"}, base); err == nil {
		t.Fatal("ambiguous input modes were accepted")
	}
	fixture, mode, _, err := loadInput(context.Background(), Config{Fixture: "clean"}, base)
	if err != nil || mode != "fixture:clean" || fixture.Summary.FilesChanged == 0 {
		t.Fatalf("fixture mode = parsed=%+v mode=%q err=%v", fixture, mode, err)
	}

	gitRepo := t.TempDir()
	runGit(t, gitRepo, "init")
	runGit(t, gitRepo, "config", "user.email", "review@example.com")
	runGit(t, gitRepo, "config", "user.name", "Review Test")
	writeFile(t, filepath.Join(gitRepo, "tracked.go"), "package tracked\n\nconst Value = 1\n")
	runGit(t, gitRepo, "add", ".")
	runGit(t, gitRepo, "commit", "-m", "baseline")
	writeFile(t, filepath.Join(gitRepo, "tracked.go"), "package tracked\n\nconst Value = 2\n")
	fromRepo, mode, decisions, err := loadInput(context.Background(), Config{RepoPath: gitRepo}, base)
	if err != nil || mode != "repo_path" || fromRepo.Summary.FilesChanged != 1 || len(decisions) != 2 {
		t.Fatalf("repo-path mode = parsed=%+v mode=%q decisions=%+v err=%v", fromRepo, mode, decisions, err)
	}
}

func TestEnrichPackagesFromRepoUsesPackageOutsideHunk(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "service.go"), "package service\n\nfunc Add(a, b int) int { return a + b }\n")
	parsed, err := ParseUnifiedDiff("diff --git a/service.go b/service.go\n--- a/service.go\n+++ b/service.go\n@@ -3 +3 @@\n-func Add(a, b int) int { return a - b }\n+func Add(a, b int) int { return a + b }\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := enrichPackagesFromRepo(&parsed, repo); err != nil {
		t.Fatal(err)
	}
	if parsed.Lines[0].Package != "service" || parsed.Hunks[0].Package != "service" {
		t.Fatalf("package outside hunk was not resolved: %+v", parsed)
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

func TestInputHelpersFailClosedOnReadAndPathErrors(t *testing.T) {
	if _, err := readBoundedReader(failingReader{}, "broken input"); err == nil {
		t.Fatal("read failure was accepted")
	}
	if _, err := diffFromFileList("", filepath.Join(t.TempDir(), "files.txt")); err == nil {
		t.Fatal("file list without repository was accepted")
	}
	root := t.TempDir()
	if _, err := synthesizeFiles(root, []string{"../escape.go"}); err == nil {
		t.Fatal("escaping file-list path was accepted")
	}
	if _, err := synthesizeFiles(root, []string{"missing.go"}); err == nil {
		t.Fatal("missing file-list path was accepted")
	}
	parsed, err := ParseUnifiedDiff("diff --git a/broken.go b/broken.go\n--- a/broken.go\n+++ b/broken.go\n@@ -1 +1 @@\n+package (\n")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "broken.go"), "package (\n")
	if err := enrichPackagesFromRepo(&parsed, root); err == nil {
		t.Fatal("invalid Go package source was accepted")
	}
}

func TestCommandOutputBoundedRejectsStartFailureNonZeroExitAndOversizeOutput(t *testing.T) {
	if _, err := commandOutputBounded(exec.Command(filepath.Join(t.TempDir(), "missing-command")), "missing command"); err == nil {
		t.Fatal("missing command was started")
	}
	for _, mode := range []string{"fail", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestCommandOutputBoundedHelper")
			cmd.Env = append(os.Environ(), "GO_WANT_REVIEW_HELPER="+mode)
			if _, err := commandOutputBounded(cmd, "helper output"); err == nil {
				t.Fatalf("helper mode %q was accepted", mode)
			}
		})
	}
}

func TestCommandOutputBoundedHelper(t *testing.T) {
	mode := os.Getenv("GO_WANT_REVIEW_HELPER")
	if mode == "" {
		return
	}
	if mode == "oversize" {
		_, _ = os.Stdout.WriteString(strings.Repeat("x", maxInputBytes+1))
		os.Exit(0)
	}
	os.Exit(3)
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

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
	fakeFailure, err := newSandbox(context.Background(), Config{Executor: ExecutorFakeFailure}, base)
	if err != nil {
		t.Fatal(err)
	}
	runs, _, artifacts, err := fakeFailure.run(context.Background(), "fake-failure", "", ParsedInput{})
	if err != nil || len(runs) != 1 || runs[0].Status != RunFailed || len(artifacts) != 0 {
		t.Fatalf("fake failure did not preserve failed-script semantics: runs=%+v artifacts=%+v err=%v", runs, artifacts, err)
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
	e2bFactory = func(context.Context, Config, string) (codeexecutor.Engine, func() error, error) {
		return nil, nil, errors.New("remote unavailable")
	}
	e2b, err = newSandbox(context.Background(), Config{Executor: "e2b", Timeout: time.Second}, base)
	if err != nil || e2b.initErr == nil || e2b.engine != nil {
		t.Fatalf("e2b initialization failure was not retained: %+v, %v", e2b, err)
	}
}

func TestSafeSnapshotCleansUpOnInvalidRepository(t *testing.T) {
	_, cleanup, err := safeSnapshot(filepath.Join(t.TempDir(), "missing"))
	defer cleanup()
	if err == nil {
		t.Fatal("missing repository was snapshotted")
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
		{name: "executor truncation", command: "go", result: codeexecutor.RunResult{Stdout: "bounded", StdoutTruncated: true}, status: "success"},
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
			if test.name == "executor truncation" && !got.OutputTruncated {
				t.Fatal("executor truncation was not propagated")
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
			if len(decisions) == 0 || len(artifacts) != 0 {
				t.Fatalf("failed setup must not publish a sandbox artifact: decisions=%+v artifacts=%+v", decisions, artifacts)
			}
		})
	}
}

func TestDiffStatsWriteFailureIsPropagated(t *testing.T) {
	outputFile := filepath.Join(t.TempDir(), "output-file")
	writeFile(t, outputFile, "not a directory")
	runner := &sandbox{executor: ExecutorFake, outputDir: outputFile}
	_, _, artifacts, err := runner.run(context.Background(), "task", "", ParsedInput{})
	if err != nil || len(artifacts) != 1 {
		t.Fatalf("diff statistics were not deferred for atomic report staging: artifacts=%+v err=%v", artifacts, err)
	}
	if _, err := os.Stat(filepath.Join(outputFile, "task", "diff_stats.json")); !os.IsNotExist(err) {
		t.Fatalf("sandbox wrote an artifact before report staging: %v", err)
	}
}

func TestWorkspaceCleanupFailureIsAudited(t *testing.T) {
	base, err := exampleDir()
	if err != nil {
		t.Fatal(err)
	}
	runner := &sandbox{
		engine: stubEngine{manager: stubManager{cleanupErr: errors.New("cleanup failed")}, fs: &stubFS{collectFiles: []codeexecutor.File{{
			Name: "out/diff_stats.json", Content: `{"files_changed":0,"added_lines":0,"deleted_lines":0}`,
		}}}, runner: stubRunner{}, cleanEnv: true},
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
	if _, _, _, err := loadInput(context.Background(), Config{}, base); err == nil {
		t.Fatal("missing input mode was accepted")
	}
	fixture, mode, _, err := loadInput(context.Background(), Config{Fixture: "clean"}, base)
	if err != nil || mode != "fixture:clean" || fixture.Summary.FilesChanged != 2 {
		t.Fatalf("unexpected fixture input: %+v %q %v", fixture, mode, err)
	}
	diffFile := filepath.Join(t.TempDir(), "change.diff")
	writeFile(t, diffFile, "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1,2 @@\n package a\n+const value = 1\n")
	if _, mode, _, err = loadInput(context.Background(), Config{DiffFile: diffFile}, base); err != nil || mode != "diff_file" {
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
	if err := store.Finalize(context.Background(), Report{Task: Task{ID: "missing", Status: TaskCompleted}}); err == nil {
		t.Fatal("finalized a task that was never saved")
	}
}

func TestStagedReportCommitRollbackAndAtomicReplace(t *testing.T) {
	root := t.TempDir()
	finalDir := filepath.Join(root, "published")
	firstStage := filepath.Join(root, "first-stage")
	if err := os.Mkdir(firstStage, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(firstStage, "report.txt"), "first")
	first := stagedReport{tempDir: firstStage, finalDir: finalDir}
	if err := first.commit(); err != nil {
		t.Fatalf("commit staged report: %v", err)
	}
	if err := first.rollback(); err != nil {
		t.Fatalf("rollback published report: %v", err)
	}
	if _, err := os.Stat(finalDir); !os.IsNotExist(err) {
		t.Fatalf("rollback left final report directory behind: %v", err)
	}
	if err := (stagedReport{}).rollback(); err != nil {
		t.Fatalf("empty rollback returned an error: %v", err)
	}

	writeFile(t, finalDir, "existing")
	secondStage := filepath.Join(root, "second-stage")
	if err := os.Mkdir(secondStage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (stagedReport{tempDir: secondStage, finalDir: finalDir}).commit(); err == nil {
		t.Fatal("commit replaced an existing report directory")
	}

	target := filepath.Join(root, "report.json")
	writeFile(t, target, "old")
	if err := atomicWrite(target, []byte("new")); err != nil {
		t.Fatalf("replace report atomically: %v", err)
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "new" {
		t.Fatalf("atomic replacement result = %q, %v", contents, err)
	}
}

func TestSnapshotCopyRejectsChangedOrExistingTargets(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.go")
	writeFile(t, source, "a")
	original, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, source, "changed")
	if err := copyBoundedFile(source, filepath.Join(root, "changed.go"), original); err == nil {
		t.Fatal("snapshot copied a source whose size changed after inspection")
	}

	stable, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "existing.go")
	writeFile(t, target, "existing")
	if err := copyBoundedFile(source, target, stable); err == nil {
		t.Fatal("snapshot overwrote an existing target")
	}
	if _, err := diffStatsArtifact(strings.Repeat("x", maxArtifactBytes+1), "synthetic_dry_run"); err == nil {
		t.Fatal("oversized diff statistics artifact was accepted")
	}
}

func TestStoreRoundTripsCompleteAuditRecords(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "reviews.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Round(0)
	report := Report{
		Task:                Task{ID: "complete-audit", Status: TaskRunning, InputMode: "diff_file", StartedAt: now, EndedAt: now},
		Input:               DiffSummary{Digest: "digest", FilesChanged: 1, GoFiles: 1, AddedLines: 2, DeletedLines: 1},
		SandboxRuns:         []SandboxRun{{Command: "diff_stats.sh", Executor: ExecutorContainer, Status: RunSuccess, DurationMS: 3}},
		PermissionDecisions: []PermissionDecision{{Command: "bash", Action: PermissionAllow, Reason: "approved", CreatedAt: now}},
		FilterDecisions:     []FilterDecision{{Fingerprint: "finding", Action: "keep", Reason: "actionable", TargetBucket: "finding"}},
		Findings:            []Finding{{Fingerprint: "finding", Severity: "high", Category: "security", File: "main.go", Line: 7, Title: "unsafe", Evidence: "evidence", Recommendation: "fix"}},
		Warnings:            []Finding{{Fingerprint: "warning", Severity: "low", Category: "quality", File: "main.go", Line: 9}},
		NeedsHumanReview:    []Finding{{Fingerprint: "human", Severity: "medium", Category: "review", File: "main.go", Line: 11}},
		Artifacts:           []Artifact{{Name: "diff_stats.json", Path: "diff_stats.json", MIMEType: "application/json", SizeBytes: 3, Provenance: "validated_sandbox_script"}},
		Metrics:             Metrics{SeverityDistribution: map[string]int{"high": 1}},
		Conclusion:          "review in progress",
	}
	if err := store.Save(context.Background(), report); err != nil {
		t.Fatalf("save complete audit report: %v", err)
	}
	if runs, err := store.LoadRuns(context.Background(), report.Task.ID); err != nil || len(runs) != 1 || runs[0].Command != "diff_stats.sh" {
		t.Fatalf("sandbox run round trip failed: runs=%+v err=%v", runs, err)
	}
	if decisions, err := store.LoadDecisions(context.Background(), report.Task.ID); err != nil || len(decisions) != 1 || decisions[0].Reason != "approved" {
		t.Fatalf("permission decision round trip failed: decisions=%+v err=%v", decisions, err)
	}
	if decisions, err := store.LoadFilterDecisions(context.Background(), report.Task.ID); err != nil || len(decisions) != 1 || decisions[0].TargetBucket != "finding" {
		t.Fatalf("filter decision round trip failed: decisions=%+v err=%v", decisions, err)
	}
	for _, bucket := range []string{"finding", "warning", "needs_human_review"} {
		findings, err := store.LoadFindings(context.Background(), report.Task.ID, bucket)
		if err != nil || len(findings) != 1 {
			t.Fatalf("%s findings round trip failed: findings=%+v err=%v", bucket, findings, err)
		}
	}

	report.Task.Status = TaskCompleted
	report.Task.EndedAt = now.Add(time.Second)
	report.SandboxRuns = append(report.SandboxRuns, SandboxRun{Command: "finalize_report", Executor: ExecutorContainer, Status: RunFailed, ErrorType: "setup_error"})
	report.Artifacts = append(report.Artifacts, Artifact{Name: "review_report.json", Path: "report/review_report.json", MIMEType: "application/json", SizeBytes: 5})
	report.Conclusion = "review complete"
	if err := store.Finalize(context.Background(), report); err != nil {
		t.Fatalf("finalize complete audit report: %v", err)
	}
	if runs, err := store.LoadRuns(context.Background(), report.Task.ID); err != nil || len(runs) != len(report.SandboxRuns) || runs[1].Command != "finalize_report" {
		t.Fatalf("finalized sandbox runs are stale: runs=%+v report=%+v err=%v", runs, report.SandboxRuns, err)
	}
	loaded, err := store.Load(context.Background(), report.Task.ID)
	if err != nil || loaded.Task.Status != TaskCompleted || loaded.Conclusion != "review complete" {
		t.Fatalf("final report round trip failed: report=%+v err=%v", loaded, err)
	}
	artifacts, err := store.LoadArtifacts(context.Background(), report.Task.ID)
	if err != nil || len(artifacts) != 2 || !hasArtifactProvenance(artifacts, "diff_stats.json", "validated_sandbox_script") {
		t.Fatalf("final artifacts round trip failed: artifacts=%+v err=%v", artifacts, err)
	}
}

func TestStoreRejectsCorruptedSerializedRecords(t *testing.T) {
	storeValue, err := openStore(filepath.Join(t.TempDir(), "reviews.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer storeValue.Close()
	store := storeValue.(*sqliteStore)
	now := time.Now().UTC().Round(0)
	report := Report{Task: Task{ID: "corrupt", Status: TaskRunning, InputMode: "fixture", StartedAt: now, EndedAt: now}, Input: DiffSummary{Digest: "digest"}, SandboxRuns: []SandboxRun{{Command: "check", Status: RunSuccess}}, PermissionDecisions: []PermissionDecision{{Command: "bash", Action: PermissionAllow, CreatedAt: now}}, Metrics: Metrics{SeverityDistribution: map[string]int{}}}
	if err := store.Save(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE review_reports SET payload_json='{' WHERE task_id=?`, report.Task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background(), report.Task.ID); err == nil {
		t.Fatal("corrupted report payload was accepted")
	}
	if _, err := store.db.Exec(`UPDATE review_metrics SET payload_json='{' WHERE task_id=?`, report.Task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadMetrics(context.Background(), report.Task.ID); err == nil {
		t.Fatal("corrupted metrics payload was accepted")
	}
	if _, err := store.db.Exec(`UPDATE sandbox_runs SET payload_json='{' WHERE task_id=?`, report.Task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadRuns(context.Background(), report.Task.ID); err == nil {
		t.Fatal("corrupted sandbox run payload was accepted")
	}
	if _, err := store.db.Exec(`UPDATE review_tasks SET started_at='invalid' WHERE id=?`, report.Task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadTask(context.Background(), report.Task.ID); err == nil {
		t.Fatal("corrupted task timestamp was accepted")
	}
	if artifacts, err := store.LoadArtifacts(context.Background(), report.Task.ID); err != nil || len(artifacts) != 0 {
		t.Fatalf("empty artifacts query = %+v, %v", artifacts, err)
	}
	if _, err := store.db.Exec(`UPDATE permission_decisions SET created_at='invalid' WHERE task_id=?`, report.Task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadDecisions(context.Background(), report.Task.ID); err == nil {
		t.Fatal("corrupted decision timestamp was accepted")
	}
}

func TestClosedStoreOperationsReturnErrors(t *testing.T) {
	storeValue, err := openStore(filepath.Join(t.TempDir(), "reviews.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := storeValue.(*sqliteStore)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Round(0)
	report := Report{Task: Task{ID: "closed", Status: TaskRunning, InputMode: "fixture", StartedAt: now, EndedAt: now}, Metrics: Metrics{SeverityDistribution: map[string]int{}}}
	checks := []struct {
		name string
		run  func() error
	}{
		{name: "save", run: func() error { return store.Save(context.Background(), report) }},
		{name: "finalize", run: func() error { return store.Finalize(context.Background(), report) }},
		{name: "load", run: func() error { _, err := store.Load(context.Background(), report.Task.ID); return err }},
		{name: "load task", run: func() error { _, err := store.LoadTask(context.Background(), report.Task.ID); return err }},
		{name: "load runs", run: func() error { _, err := store.LoadRuns(context.Background(), report.Task.ID); return err }},
		{name: "load decisions", run: func() error { _, err := store.LoadDecisions(context.Background(), report.Task.ID); return err }},
		{name: "load filters", run: func() error { _, err := store.LoadFilterDecisions(context.Background(), report.Task.ID); return err }},
		{name: "load metrics", run: func() error { _, err := store.LoadMetrics(context.Background(), report.Task.ID); return err }},
		{name: "load findings", run: func() error {
			_, err := store.LoadFindings(context.Background(), report.Task.ID, "finding")
			return err
		}},
		{name: "load artifacts", run: func() error { _, err := store.LoadArtifacts(context.Background(), report.Task.ID); return err }},
		{name: "delete", run: func() error { return store.Delete(context.Background(), report.Task.ID) }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.run(); err == nil {
				t.Fatal("closed store operation unexpectedly succeeded")
			}
		})
	}
}

func TestOpenStorePreservesExistingDirectoryMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX directory modes")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := openStore(filepath.Join(dir, "reviews.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("existing directory mode changed to %o", got)
	}
}

func TestOpenStoreMigratesLegacyArtifactSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE artifacts (task_id TEXT NOT NULL, name TEXT NOT NULL, path TEXT NOT NULL, mime_type TEXT NOT NULL, size_bytes INTEGER NOT NULL, PRIMARY KEY(task_id, name))`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rows, err := store.(*sqliteStore).db.Query(`PRAGMA table_info(artifacts)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var hasProvenance bool
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, fieldType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &fieldType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		hasProvenance = hasProvenance || name == "provenance"
	}
	if err := rows.Err(); err != nil || !hasProvenance {
		t.Fatalf("legacy provenance migration failed: present=%t err=%v", hasProvenance, err)
	}
}

func TestAtomicWriteRejectsDirectoryTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "report")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(target, []byte("data")); err == nil {
		t.Fatal("atomic write replaced a directory target")
	}
}

func TestStageReportPublishesDeferredDiffStatisticsWithReportPair(t *testing.T) {
	dir := t.TempDir()
	report := Report{
		Task:      Task{ID: "deferred-stats", Status: TaskCompleted},
		Artifacts: []Artifact{{Name: "diff_stats.json", Path: "diff_stats.json", MIMEType: "application/json", content: `{"files_changed":1}`}},
		Metrics:   Metrics{SeverityDistribution: map[string]int{}},
	}
	stagedReportValue, paths, staged, err := stageReport(report, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer staged.cleanup()
	if err := staged.commit(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, report.Task.ID, "diff_stats.json"))
	if err != nil || string(data) != `{"files_changed":1}` {
		t.Fatalf("deferred statistics were not published: %q, %v", data, err)
	}
	for _, path := range []string{paths.JSON, paths.Markdown} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("report pair is incomplete: %v", err)
		}
	}
	if len(stagedReportValue.Artifacts) != 3 || stagedReportValue.Artifacts[1].SizeBytes == 0 || stagedReportValue.Artifacts[2].SizeBytes == 0 {
		t.Fatalf("report artifact sizes did not converge: %+v", stagedReportValue.Artifacts)
	}
}

func TestSafeSnapshotCopiesEligibleFilesAndSkipsIgnoredTrees(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module example.com/snapshot\n")
	writeFile(t, filepath.Join(repo, "nested", "service.go"), "package nested\n")
	writeFile(t, filepath.Join(repo, "vendor", "ignored.go"), "package ignored\n")
	writeFile(t, filepath.Join(repo, "notes.txt"), "not staged\n")
	snapshot, cleanup, err := safeSnapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, rel := range []string{"go.mod", filepath.Join("nested", "service.go")} {
		if _, err := os.Stat(filepath.Join(snapshot, rel)); err != nil {
			t.Fatalf("eligible file %q was not copied: %v", rel, err)
		}
	}
	for _, rel := range []string{filepath.Join("vendor", "ignored.go"), "notes.txt"} {
		if _, err := os.Stat(filepath.Join(snapshot, rel)); !os.IsNotExist(err) {
			t.Fatalf("ignored file %q was copied: %v", rel, err)
		}
	}
}

func TestSnapshotCopyFailsClosedForOversizeAndUnstableSources(t *testing.T) {
	root := t.TempDir()
	large := filepath.Join(root, "large.go")
	file, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxSnapshotBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, cleanup, err := safeSnapshot(root); err == nil {
		cleanup()
		t.Fatal("oversized source tree was snapshotted")
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyBoundedFile(root, filepath.Join(t.TempDir(), "copy"), info); err == nil {
		t.Fatal("directory source was copied as a regular file")
	}
	if err := copyBoundedFile(filepath.Join(root, "missing.go"), filepath.Join(t.TempDir(), "copy"), info); err == nil {
		t.Fatal("missing source was copied")
	}
}

func TestReviewContainerSecurityConfiguration(t *testing.T) {
	containerCfg := reviewContainerConfig()
	if containerCfg.WorkingDir != "/home/reviewer" {
		t.Fatalf("unexpected working directory %q", containerCfg.WorkingDir)
	}
	hostCfg := reviewHostConfig()
	if !hostCfg.AutoRemove || hostCfg.Privileged || hostCfg.NetworkMode != "none" {
		t.Fatalf("unsafe host configuration: %+v", hostCfg)
	}
	if !hostCfg.ReadonlyRootfs || len(hostCfg.CapDrop) != 1 || hostCfg.CapDrop[0] != "ALL" || hostCfg.Tmpfs["/tmp"] == "" {
		t.Fatalf("filesystem or capability isolation missing: %+v", hostCfg)
	}
	if hostCfg.Memory != containerMemory || hostCfg.NanoCPUs != containerNanoCPUs || hostCfg.PidsLimit == nil || *hostCfg.PidsLimit != containerPIDs {
		t.Fatalf("resource limits missing: %+v", hostCfg.Resources)
	}
}

func TestStageReportAndSkillLoadingRejectInvalidRoots(t *testing.T) {
	parentFile := filepath.Join(t.TempDir(), "parent")
	writeFile(t, parentFile, "not a directory")
	report := Report{Task: Task{ID: "task"}, Metrics: Metrics{SeverityDistribution: map[string]int{}}}
	if _, _, _, err := stageReport(report, parentFile); err == nil {
		t.Fatal("report was staged below a regular file")
	}
	if _, err := loadReviewSkill(t.TempDir()); err == nil {
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
	collectFiles  []codeexecutor.File
	collectErr    error
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
func (s *stubFS) Collect(context.Context, codeexecutor.Workspace, []string) ([]codeexecutor.File, error) {
	return s.collectFiles, s.collectErr
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
