//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
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
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type mockWorkspaceEngine struct {
	createErr           error
	stageErr            error
	staticcheckExitOnly bool
	runSpecs            []codeexecutor.RunProgramSpec
}

func (m *mockWorkspaceEngine) Manager() codeexecutor.WorkspaceManager { return m }
func (m *mockWorkspaceEngine) FS() codeexecutor.WorkspaceFS           { return m }
func (m *mockWorkspaceEngine) Runner() codeexecutor.ProgramRunner     { return m }
func (m *mockWorkspaceEngine) Describe() codeexecutor.Capabilities {
	return codeexecutor.Capabilities{Isolation: "mock", SupportsCleanEnv: true}
}
func (m *mockWorkspaceEngine) CreateWorkspace(context.Context, string, codeexecutor.WorkspacePolicy) (codeexecutor.Workspace, error) {
	if m.createErr != nil {
		return codeexecutor.Workspace{}, m.createErr
	}
	return codeexecutor.Workspace{ID: "ws", Path: "mock"}, nil
}
func (m *mockWorkspaceEngine) Cleanup(context.Context, codeexecutor.Workspace) error { return nil }
func (m *mockWorkspaceEngine) PutFiles(context.Context, codeexecutor.Workspace, []codeexecutor.PutFile) error {
	return nil
}
func (m *mockWorkspaceEngine) StageDirectory(context.Context, codeexecutor.Workspace, string, string, codeexecutor.StageOptions) error {
	return m.stageErr
}
func (m *mockWorkspaceEngine) Collect(context.Context, codeexecutor.Workspace, []string) ([]codeexecutor.File, error) {
	return []codeexecutor.File{{
		Name:      "out/diff_summary.json",
		Content:   `{"files_changed":1}`,
		MIMEType:  "application/json",
		SizeBytes: 19,
	}}, nil
}
func (m *mockWorkspaceEngine) StageInputs(context.Context, codeexecutor.Workspace, []codeexecutor.InputSpec) error {
	return nil
}
func (m *mockWorkspaceEngine) CollectOutputs(context.Context, codeexecutor.Workspace, codeexecutor.OutputSpec) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}
func (m *mockWorkspaceEngine) RunProgram(_ context.Context, _ codeexecutor.Workspace, spec codeexecutor.RunProgramSpec) (codeexecutor.RunResult, error) {
	m.runSpecs = append(m.runSpecs, spec)
	switch spec.Cmd {
	case "bash":
		return codeexecutor.RunResult{ExitCode: 0, Stdout: "ok"}, nil
	case "go":
		if len(spec.Args) > 0 && spec.Args[0] == "test" {
			stdout := "work/repo/pkg/a.go:3: test failed"
			if spec.MaxOutputBytes > 0 && len(stdout) > spec.MaxOutputBytes {
				stdout = stdout[:spec.MaxOutputBytes]
			}
			return codeexecutor.RunResult{ExitCode: 1, Stderr: stdout}, nil
		}
		return codeexecutor.RunResult{ExitCode: 0}, nil
	case "staticcheck":
		if m.staticcheckExitOnly {
			return codeexecutor.RunResult{
				ExitCode: 127,
				Stderr:   "env: 'staticcheck': No such file or directory",
			}, nil
		}
		return codeexecutor.RunResult{}, errors.New("executable file not found")
	default:
		return codeexecutor.RunResult{ExitCode: 127}, errors.New("unknown command")
	}
}

func TestLoadInputDiffSourcesAndValidation(t *testing.T) {
	diffPath := filepath.Join(t.TempDir(), "change.diff")
	raw := readFixture(t, "security_issue")
	if err := os.WriteFile(diffPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, mode, err := loadInputDiff(testContext(t), ReviewConfig{DiffFile: diffPath})
	if err != nil {
		t.Fatal(err)
	}
	if got != raw || mode != "diff-file" {
		t.Fatalf("diff-file mode got %q len=%d", mode, len(got))
	}

	listPath := filepath.Join(t.TempDir(), "files.txt")
	if err := os.WriteFile(listPath, []byte("a.go\nb_test.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, mode, err = loadInputDiff(testContext(t), ReviewConfig{FileList: listPath})
	if err != nil {
		t.Fatal(err)
	}
	if mode != "file-list" || !strings.Contains(got, "diff --git a/a.go b/a.go") {
		t.Fatalf("file-list mode=%q diff=%q", mode, got)
	}

	got, mode, err = loadInputDiff(testContext(t), ReviewConfig{Fixture: "no_issue"})
	if err != nil {
		t.Fatal(err)
	}
	if mode != "fixture:no_issue" || !strings.Contains(got, "handler.go") {
		t.Fatalf("fixture mode=%q diff=%q", mode, got)
	}

	if _, _, err := loadInputDiff(testContext(t), ReviewConfig{}); err == nil {
		t.Fatal("expected missing input error")
	}
	if _, _, err := loadInputDiff(testContext(t), ReviewConfig{DiffFile: diffPath, Fixture: "no_issue"}); err == nil {
		t.Fatal("expected multiple input error")
	}
	if _, _, err := loadInputDiff(testContext(t), ReviewConfig{DiffFile: filepath.Join(t.TempDir(), "missing.diff")}); err == nil {
		t.Fatal("expected missing diff file error")
	}
	if _, _, err := loadInputDiff(testContext(t), ReviewConfig{FileList: filepath.Join(t.TempDir(), "missing.txt")}); err == nil {
		t.Fatal("expected missing file list error")
	}
	if _, _, err := loadInputDiff(testContext(t), ReviewConfig{Fixture: "missing_fixture"}); err == nil {
		t.Fatal("expected missing fixture error")
	}
}

func TestLoadInputDiffRepoAndContainerSmokeModes(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	writeTestFile(t, filepath.Join(repo, "go.mod"), "module example.com/input\n\ngo 1.23\n")
	writeTestFile(t, filepath.Join(repo, "main.go"), "package main\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-qm", "init")
	writeTestFile(t, filepath.Join(repo, "main.go"), "package main\n\nfunc main() {}\n")

	got, mode, err := loadInputDiff(testContext(t), ReviewConfig{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	if mode != "repo-path" || !strings.Contains(got, "main.go") {
		t.Fatalf("repo mode=%q diff=%q", mode, got)
	}
	got, mode, err = loadInputDiff(testContext(t), ReviewConfig{RepoPath: repo, ContainerSmoke: true})
	if err != nil {
		t.Fatal(err)
	}
	if mode != "container-smoke" || !strings.Contains(got, "main.go") {
		t.Fatalf("container-smoke mode=%q diff=%q", mode, got)
	}
	if _, _, err := loadInputDiff(testContext(t), ReviewConfig{RepoPath: filepath.Join(repo, "missing")}); err == nil {
		t.Fatal("expected git diff error for missing repo")
	}
	cleanRepo := t.TempDir()
	runGit(t, cleanRepo, "init", "-q")
	writeTestFile(t, filepath.Join(cleanRepo, "go.mod"), "module example.com/clean\n\ngo 1.23\n")
	runGit(t, cleanRepo, "add", ".")
	runGit(t, cleanRepo, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-qm", "init")
	if diff, err := gitDiff(testContext(t), cleanRepo); err != nil || diff != "" {
		t.Fatalf("clean repo diff=%q err=%v", diff, err)
	}
}

func TestUntrackedFileDiffsSymlinkBinaryAndNoNewline(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, filepath.Join(repo, "plain.go"), "package plain")
	writeTestFile(t, filepath.Join(repo, "binary.dat"), string([]byte{0, 1, 2}))
	if err := os.Symlink("plain.go", filepath.Join(repo, "link.go")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	raw := []byte("plain.go\x00binary.dat\x00link.go\x00")
	diff, err := untrackedFileDiffs(repo, raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"+++ b/plain.go",
		"\\ No newline at end of file",
		"Binary files /dev/null and b/binary.dat differ",
		"+plain.go",
	} {
		if !strings.Contains(diff, want) {
			t.Fatalf("diff missing %q:\n%s", want, diff)
		}
	}
	if _, err := untrackedFileDiffs(repo, []byte("missing.go\x00")); err == nil {
		t.Fatal("expected missing untracked file error")
	}
}

func TestRunReviewErrorPaths(t *testing.T) {
	if _, _, _, err := RunReview(testContext(t), ReviewConfig{DryRun: true}); err == nil {
		t.Fatal("expected missing input error")
	}
	out := t.TempDir()
	report, _, _, err := RunReview(testContext(t), ReviewConfig{
		Fixture:   "no_issue",
		OutputDir: out,
		DBPath:    filepath.Join(out, "reviews.sqlite"),
		Executor:  "bad-executor",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.NeedsHumanReview) == 0 || report.SandboxRuns[0].ErrorType != "sandbox_setup" {
		t.Fatalf("expected sandbox setup item, got runs=%+v needs=%+v", report.SandboxRuns, report.NeedsHumanReview)
	}
	out = t.TempDir()
	report, _, _, err = RunReview(testContext(t), ReviewConfig{
		Fixture:   "no_issue",
		OutputDir: out,
		DBPath:    filepath.Join(out, "reviews.sqlite"),
		DryRun:    true,
		RuleOnly:  false,
		LLMReview: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.NeedsHumanReview) == 0 || !hasRule(report.NeedsHumanReview, "llm/review-failed") {
		t.Fatalf("expected llm failure human-review item, got %+v", report.NeedsHumanReview)
	}
	fileOutput := filepath.Join(t.TempDir(), "output-file")
	if err := os.WriteFile(fileOutput, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := RunReview(testContext(t), ReviewConfig{Fixture: "no_issue", OutputDir: fileOutput, DryRun: true}); err == nil {
		t.Fatal("expected output dir file error")
	}
	if _, _, _, err := RunReview(testContext(t), ReviewConfig{
		Fixture:   "no_issue",
		OutputDir: t.TempDir(),
		DBPath:    filepath.Join(t.TempDir(), "missing", "reviews.sqlite"),
		DryRun:    true,
	}); err == nil {
		t.Fatal("expected db open/init error")
	}
}

func TestSelectedInputsAndPackageEnrichment(t *testing.T) {
	selected := selectedInputs(ReviewConfig{
		DiffFile:       "x.diff",
		RepoPath:       "/repo",
		FileList:       "files.txt",
		Fixture:        "no_issue",
		ContainerSmoke: true,
	})
	want := []string{"--diff-file", "--file-list", "--fixture", "--container-smoke"}
	if strings.Join(selected, ",") != strings.Join(want, ",") {
		t.Fatalf("selected inputs = %v, want %v", selected, want)
	}

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pkg", "thing.go"), []byte("package custompkg\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pd := ParsedDiff{Files: []DiffFile{{NewPath: "pkg/thing.go", IsGo: true}}}
	enrichPackageInfoFromRepo(&pd, repo)
	if pd.Files[0].PackageName != "custompkg" || len(pd.Packages) != 1 {
		t.Fatalf("package info not enriched: %+v", pd)
	}
}

func TestSandboxRunnerFactoryDeterministicBranches(t *testing.T) {
	for _, executor := range []string{"fake", "none", "fake-fail"} {
		runner, err := NewSandboxRunner(ReviewConfig{Executor: executor})
		if err != nil {
			t.Fatalf("%s runner: %v", executor, err)
		}
		if err := runner.Close(); err != nil {
			t.Fatalf("%s close: %v", executor, err)
		}
	}
	if _, err := NewSandboxRunner(ReviewConfig{Executor: "local"}); err == nil {
		t.Fatal("expected local executor to require explicit fallback")
	}
	local, err := NewSandboxRunner(ReviewConfig{Executor: "local", AllowLocalFallback: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = local.Close()
	if _, err := NewSandboxRunner(ReviewConfig{Executor: "container", ContainerBaseImage: "bad image\nRUN x"}); err == nil {
		t.Fatal("expected unsafe container image to fail before Docker init")
	}
	t.Setenv("DOCKER_HOST", "unix:///tmp/trpc-agent-go-missing-docker.sock")
	if runner, err := NewSandboxRunner(ReviewConfig{Executor: "container"}); err == nil {
		_ = runner.Close()
		t.Fatal("expected container executor to fail with missing Docker socket")
	}
	t.Setenv("E2B_API_KEY", "")
	if _, err := NewSandboxRunner(ReviewConfig{Executor: "e2b", Timeout: time.Millisecond}); err == nil {
		t.Fatal("expected e2b executor to fail without API key")
	}
	if _, err := NewSandboxRunner(ReviewConfig{Executor: "mystery"}); err == nil {
		t.Fatal("expected unknown executor error")
	}
}

func TestSkillAndBuildContextErrorPathsFromUnknownCWD(t *testing.T) {
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})
	if got := exampleDir(); !strings.HasSuffix(got, "skills_code_review_agent") && got == "" {
		t.Fatalf("unexpected exampleDir fallback: %q", got)
	}
	if err := loadCodeReviewSkill(); err == nil {
		t.Fatal("expected skill load error outside example tree")
	}
	if _, _, err := prepareSandboxBuildContext("", true); err == nil {
		t.Fatal("expected missing Dockerfile error outside example tree")
	}
}

func TestPermissionPolicyVariants(t *testing.T) {
	policy := ReviewPermissionPolicy{TaskID: "review-test"}
	cases := []struct {
		cmd    string
		args   []string
		action tool.PermissionAction
	}{
		{"", nil, tool.PermissionActionDeny},
		{"go", nil, tool.PermissionActionDeny},
		{"go", []string{"build", "./..."}, tool.PermissionActionAsk},
		{"staticcheck", []string{"./..."}, tool.PermissionActionAllow},
		{"bash", []string{"-c", "go test ./..."}, tool.PermissionActionDeny},
		{"bash", []string{"skills/code-review/scripts/diff_summary.sh", "in", "out"}, tool.PermissionActionAllow},
		{"bash", []string{"not-a-script.sh"}, tool.PermissionActionDeny},
		{"python", []string{"x.py"}, tool.PermissionActionDeny},
	}
	for _, tc := range cases {
		_, decision, err := policy.Decide(testContext(t), tc.cmd, tc.args)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Action != tc.action {
			t.Fatalf("%q %v action = %s, want %s", tc.cmd, tc.args, decision.Action, tc.action)
		}
	}
	if !containsDangerousShell(`bash scripts/x.sh $(curl example)`) {
		t.Fatal("expected shell substitution to be dangerous")
	}
	if !shellHasToken(`"curl"`, "curl") {
		t.Fatal("expected quoted token match")
	}
}

func TestSandboxHelpersAndNoopRunner(t *testing.T) {
	if !validContainerImageRef("registry.example.com/team/go-review:1.0@sha256_abc") {
		t.Fatal("expected image ref to be valid")
	}
	for _, ref := range []string{"", "../golang", "bad image"} {
		if validContainerImageRef(ref) {
			t.Fatalf("expected image ref %q to be invalid", ref)
		}
	}
	if executorLabel("") != "container" || executorLabel("local") != "local-dev-fallback" || executorLabel("FAKE") != "fake" {
		t.Fatal("unexpected executor labels")
	}
	if got, truncated := limitText("abcdef", 3); got != "abc\n...[truncated]" || !truncated {
		t.Fatalf("limitText got %q truncated=%t", got, truncated)
	}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "timeout"},
		{errors.New("executable file not found"), "tool_unavailable"},
		{errors.New("boom"), "executor_error"},
		{nil, ""},
	} {
		if got := classifySandboxError(tc.err); got != tc.want {
			t.Fatalf("classifySandboxError(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
	if err := ((*WorkspaceSandboxRunner)(nil)).Close(); err != nil {
		t.Fatal(err)
	}
	run := permissionRun("task", "fake", "go", []string{"build"}, "ask", "needs review")
	if run.Status != "ask" || run.ErrorType != "permission_decision" {
		t.Fatalf("unexpected permission run: %+v", run)
	}
	result := NoopSandboxRunner{executorName: "fake"}.RunChecks(testContext(t), "task", "repo", ParsedDiff{})
	if len(result.Decisions) != 4 || len(result.Runs) != 4 {
		t.Fatalf("repo dry-run plan got decisions=%d runs=%d", len(result.Decisions), len(result.Runs))
	}
}

func TestWorkspaceSandboxRunnerWithMockEngine(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	writeTestFile(t, filepath.Join(repo, "go.mod"), "module example.com/mock\n\ngo 1.23\n")
	writeTestFile(t, filepath.Join(repo, "pkg", "a.go"), "package pkg\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-qm", "init")
	writeTestFile(t, filepath.Join(repo, "pkg", "a.go"), "package pkg\n\nfunc A() {}\n")
	raw, err := gitDiff(testContext(t), repo)
	if err != nil {
		t.Fatal(err)
	}
	pd, err := ParseUnifiedDiff(raw)
	if err != nil {
		t.Fatal(err)
	}
	engine := &mockWorkspaceEngine{}
	runner := &WorkspaceSandboxRunner{
		executorName:     "mock",
		engine:           engine,
		timeout:          time.Second,
		outputLimitBytes: 1024,
		outputDir:        t.TempDir(),
	}
	result := runner.RunChecks(testContext(t), "task-mock", repo, pd)
	if !result.SkillLoaded {
		t.Fatal("expected skill to be marked loaded")
	}
	if len(result.Decisions) != 4 || len(result.Runs) != 4 || len(engine.runSpecs) != 4 {
		t.Fatalf("unexpected result decisions=%d runs=%d specs=%d", len(result.Decisions), len(result.Runs), len(engine.runSpecs))
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].Name != "diff_summary.json" {
		t.Fatalf("unexpected artifacts: %+v", result.Artifacts)
	}
	if !strings.Contains(filepath.ToSlash(result.Artifacts[0].Path), "/task-mock/") {
		t.Fatalf("artifact path is not scoped by task id: %s", result.Artifacts[0].Path)
	}
	data, err := os.ReadFile(result.Artifacts[0].Path)
	if err != nil {
		t.Fatalf("expected durable artifact path %q: %v", result.Artifacts[0].Path, err)
	}
	if !strings.Contains(string(data), "files_changed") {
		t.Fatalf("unexpected artifact content: %s", data)
	}
	if len(result.Findings) != 1 || result.Findings[0].RuleID != "sandbox/go/diagnostic" {
		t.Fatalf("unexpected sandbox findings: %+v", result.Findings)
	}
	other, err := runner.materializeCollectedArtifact("task-other", codeexecutor.File{
		Name:    "out/diff_summary.json",
		Content: `{"files_changed":2}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if other.Path == result.Artifacts[0].Path {
		t.Fatalf("artifact paths should be task-scoped, both were %s", other.Path)
	}
	tooLarge := strings.Repeat("x", int(defaultArtifactPolicy().MaxBytesPerFile)+1)
	if _, err := runner.materializeCollectedArtifact("task-large", codeexecutor.File{Name: "out/diff_summary.json", Content: tooLarge}); err == nil {
		t.Fatal("expected oversized collected artifact to be rejected before writing")
	}
	var staticcheckSkipped bool
	for _, run := range result.Runs {
		if run.Command == "staticcheck" && run.Status == "skipped" && run.ErrorType == "tool_unavailable" {
			staticcheckSkipped = true
		}
	}
	if !staticcheckSkipped {
		t.Fatalf("expected staticcheck unavailable skip, runs=%+v", result.Runs)
	}
}

func TestWorkspaceSandboxRunnerSkipsStaticcheckExit127WithoutError(t *testing.T) {
	engine := &mockWorkspaceEngine{staticcheckExitOnly: true}
	runner := &WorkspaceSandboxRunner{
		executorName:     "mock",
		engine:           engine,
		timeout:          time.Second,
		outputLimitBytes: 1024,
		outputDir:        t.TempDir(),
	}
	run := runner.runProgram(testContext(t), codeexecutor.Workspace{ID: "ws"}, "task", "staticcheck", []string{"./..."}, ".")
	if run.Status != "skipped" || run.ErrorType != "tool_unavailable" {
		t.Fatalf("expected staticcheck exit 127 to be skipped tool_unavailable, got %+v", run)
	}
	if strings.Contains(run.Stderr, "No such file") {
		t.Fatalf("expected friendly unavailable message, got %q", run.Stderr)
	}
}

func TestWorkspaceSandboxRunnerDisablesRepoChecksForE2B(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, filepath.Join(repo, "go.mod"), "module example.com/e2b\n\ngo 1.23\n")
	engine := &mockWorkspaceEngine{}
	runner := &WorkspaceSandboxRunner{
		executorName:     "e2b",
		engine:           engine,
		timeout:          time.Second,
		outputLimitBytes: 1024,
		outputDir:        t.TempDir(),
	}
	result := runner.RunChecks(testContext(t), "task-e2b", repo, ParsedDiff{Raw: "diff"})
	if len(engine.runSpecs) != 1 || engine.runSpecs[0].Cmd != "bash" {
		t.Fatalf("expected only diff summary to run in E2B, specs=%+v", engine.runSpecs)
	}
	var unavailable int
	for _, run := range result.Runs {
		if run.ErrorType == "e2b_egress_not_enforced" && run.Status == "skipped" {
			unavailable++
		}
	}
	if unavailable != 3 {
		t.Fatalf("expected three unavailable repo checks, runs=%+v", result.Runs)
	}
}

func TestWorkspaceSandboxRunnerMarksExternalModulesUnavailableOffline(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, filepath.Join(repo, "go.mod"), "module example.com/deps\n\ngo 1.23\n\nrequire github.com/stretchr/testify v1.9.0\n")
	engine := &mockWorkspaceEngine{}
	runner := &WorkspaceSandboxRunner{
		executorName:     "container",
		engine:           engine,
		timeout:          time.Second,
		outputLimitBytes: 1024,
		outputDir:        t.TempDir(),
	}
	result := runner.RunChecks(testContext(t), "task-deps", repo, ParsedDiff{Raw: "diff"})
	if len(engine.runSpecs) != 1 || engine.runSpecs[0].Cmd != "bash" {
		t.Fatalf("expected dependency-unavailable path to skip repo commands, specs=%+v", engine.runSpecs)
	}
	var unavailable int
	for _, run := range result.Runs {
		if run.ErrorType == "dependency_unavailable" && run.Status == "skipped" {
			unavailable++
		}
	}
	if unavailable != 3 {
		t.Fatalf("expected three dependency unavailable runs, got %+v", result.Runs)
	}
	items := sandboxReviewItems(result.Runs, ParsedDiff{}, result.Findings)
	var incomplete int
	for _, item := range items {
		if item.RuleID == "sandbox/core-check-unavailable" &&
			item.Category == "incomplete_analysis" {
			incomplete++
		}
	}
	if incomplete != 2 {
		t.Fatalf("expected go test/go vet incomplete-analysis findings, got %+v", items)
	}
}

func TestWorkspaceSandboxRunnerAllowsLocalReplaceModulesOffline(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, filepath.Join(repo, "go.mod"), `module example.com/deps

go 1.23

require example.com/localmod v0.0.0

replace example.com/localmod => ./localmod
`)
	writeTestFile(t, filepath.Join(repo, "localmod", "go.mod"), "module example.com/localmod\n\ngo 1.23\n")
	if repoHasUnvendoredExternalModules(repo) {
		t.Fatal("local replace module should not require external module resolution")
	}
	engine := &mockWorkspaceEngine{}
	runner := &WorkspaceSandboxRunner{
		executorName:     "container",
		engine:           engine,
		timeout:          time.Second,
		outputLimitBytes: 1024,
		outputDir:        t.TempDir(),
	}
	result := runner.RunChecks(testContext(t), "task-local-replace", repo, ParsedDiff{Raw: "diff"})
	if len(engine.runSpecs) != 4 {
		t.Fatalf("expected repo checks to run with local replace, specs=%+v result=%+v", engine.runSpecs, result.Runs)
	}
	for _, run := range result.Runs {
		if run.ErrorType == "dependency_unavailable" {
			t.Fatalf("local replace was marked unavailable: %+v", result.Runs)
		}
	}
}

func TestWorkspaceSandboxRunnerRejectsLocalReplaceOutsideSnapshot(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	external := filepath.Join(root, "externalmod")
	writeTestFile(t, filepath.Join(repo, "go.mod"), `module example.com/deps

go 1.23

require example.com/externalmod v0.0.0

replace example.com/externalmod => ../externalmod
`)
	writeTestFile(t, filepath.Join(external, "go.mod"), "module example.com/externalmod\n\ngo 1.23\n")
	plan, err := buildSandboxSnapshotPlan(testContext(t), repo, sandboxSnapshotMaxFiles)
	if err != nil {
		t.Fatal(err)
	}
	if !repoHasUnvendoredExternalModulesForSnapshot(repo, plan) {
		t.Fatal("out-of-snapshot local replace should make offline repo checks unavailable")
	}
	engine := &mockWorkspaceEngine{}
	runner := &WorkspaceSandboxRunner{
		executorName:     "container",
		engine:           engine,
		timeout:          time.Second,
		outputLimitBytes: 1024,
		outputDir:        t.TempDir(),
	}
	result := runner.RunChecks(testContext(t), "task-external-replace", repo, ParsedDiff{Raw: "diff"})
	if len(engine.runSpecs) != 1 || engine.runSpecs[0].Cmd != "bash" {
		t.Fatalf("expected out-of-snapshot replace to skip repo commands, specs=%+v result=%+v", engine.runSpecs, result.Runs)
	}
	var unavailable int
	for _, run := range result.Runs {
		if run.ErrorType == "dependency_unavailable" && run.Status == "skipped" {
			unavailable++
		}
	}
	if unavailable != 3 {
		t.Fatalf("expected three dependency-unavailable runs, got %+v", result.Runs)
	}
}

func TestWorkspaceSandboxRunnerRejectsIgnoredLocalReplaceInsideRoot(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	writeTestFile(t, filepath.Join(repo, ".gitignore"), "localmod/\n")
	writeTestFile(t, filepath.Join(repo, "go.mod"), `module example.com/deps

go 1.23

require example.com/localmod v0.0.0

replace example.com/localmod => ./localmod
`)
	writeTestFile(t, filepath.Join(repo, "localmod", "go.mod"), "module example.com/localmod\n\ngo 1.23\n")
	runGit(t, repo, "add", ".gitignore", "go.mod")
	plan, err := buildSandboxSnapshotPlan(testContext(t), repo, sandboxSnapshotMaxFiles)
	if err != nil {
		t.Fatal(err)
	}
	if !repoHasUnvendoredExternalModulesForSnapshot(repo, plan) {
		t.Fatal("ignored local replace target should not be treated as available in the sandbox snapshot")
	}
}

func TestWorkspaceSandboxRunnerSetupFailures(t *testing.T) {
	pd := ParsedDiff{Raw: "diff --git a/a.go b/a.go\n"}
	runner := &WorkspaceSandboxRunner{executorName: "mock", engine: &mockWorkspaceEngine{createErr: errors.New("no workspace")}}
	result := runner.RunChecks(testContext(t), "task", "", pd)
	if len(result.Runs) != 1 || result.Runs[0].Command != "create_workspace" {
		t.Fatalf("unexpected create failure result: %+v", result.Runs)
	}
	runner = &WorkspaceSandboxRunner{executorName: "mock", engine: &mockWorkspaceEngine{stageErr: errors.New("stage denied")}}
	result = runner.RunChecks(testContext(t), "task", "", pd)
	if len(result.Runs) != 1 || result.Runs[0].Command != "stage_skill" {
		t.Fatalf("unexpected stage failure result: %+v", result.Runs)
	}
}

func TestRepoSnapshotHelpersEdgeCases(t *testing.T) {
	for _, path := range []string{"", ".", "/abs", "../escape"} {
		if got := normalizeSandboxRelPath(path); got != "" {
			t.Fatalf("normalizeSandboxRelPath(%q) = %q, want empty", path, got)
		}
	}
	for _, path := range []string{".git/config", ".env", "id_rsa", "config.pem", "my-secret/file.go"} {
		if !shouldSkipSandboxStagePath(path) {
			t.Fatalf("expected %q to be skipped", path)
		}
	}
	if shouldSkipSandboxStagePath("service/main.go") {
		t.Fatal("did not expect service/main.go to be skipped")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "file.go"), []byte("package nested\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := walkSandboxRepoFiles(dir, sandboxSnapshotMaxFiles)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "nested/file.go" {
		t.Fatalf("walk files = %+v", files)
	}
	dst := t.TempDir()
	budget := sandboxSnapshotBudget{maxFiles: 10, maxTotalBytes: 1024, maxFileBytes: 1024}
	if err := copySandboxFile(testContext(t), dir, dst, "nested/file.go", &budget); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "nested", "file.go")); err != nil {
		t.Fatal(err)
	}
	if err := copySandboxFile(testContext(t), dir, dst, "../escape", &budget); err != nil {
		t.Fatal(err)
	}
	if err := copySandboxFile(testContext(t), dir, dst, "missing.go", &budget); err == nil {
		t.Fatal("expected missing file copy error")
	}
	if files, err := sandboxRepoFileList(testContext(t), dir, sandboxSnapshotMaxFiles); err != nil || len(files) == 0 {
		t.Fatalf("sandboxRepoFileList files=%v err=%v", files, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "link-target"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("link-target", filepath.Join(dir, "link")); err == nil {
		if err := copySandboxFile(testContext(t), dir, dst, "link", &budget); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLLMParsingAndBucketingEdges(t *testing.T) {
	fenced := "```json\n[]\n```"
	if got := stripCodeFence(fenced); got != "[]" {
		t.Fatalf("stripCodeFence = %q", got)
	}
	if got := stripCodeFence("```broken"); got != "```broken" {
		t.Fatalf("short code fence changed to %q", got)
	}
	if findings, err := parseLLMFindings("prefix [] suffix"); err != nil || len(findings) != 0 {
		t.Fatalf("empty array findings=%+v err=%v", findings, err)
	}
	if _, err := parseLLMFindings("[bad json]"); err == nil {
		t.Fatal("expected invalid JSON error")
	}
	parsed, err := parseLLMFindings(`[{"severity":"high","category":"security","file":"a.go","line":1,"title":"x","evidence":"apiKey = \"sk-1234567890abcdefghijklmnop\"","recommendation":"fix","confidence":0.9,"rule_id":"r"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 1 || parsed[0].Source != "llm" || strings.Contains(parsed[0].Evidence, "sk-1234567890") {
		t.Fatalf("unexpected parsed LLM findings: %+v", parsed)
	}
	items := []Finding{
		{Severity: SeverityHigh, Confidence: 0.80},
		{Severity: SeverityMedium, Confidence: 0.60},
		{Severity: SeverityLow, Confidence: 0.80},
		{Severity: SeverityMedium, Confidence: 0.20},
	}
	findings, warnings, needs := bucketSupplementalFindings(items)
	if len(findings) != 1 || len(warnings) != 1 || len(needs) != 2 {
		t.Fatalf("bucketed findings=%d warnings=%d needs=%d", len(findings), len(warnings), len(needs))
	}
	if !strings.Contains(buildLLMReviewPrompt(LLMReviewConfig{
		InputSummary: DiffSummary{FilesChanged: 1, GoFiles: 1, AddedLines: 2},
		RuleFindings: []Finding{{
			Severity: SeverityHigh,
			File:     "a.go",
			Line:     3,
			Title:    "risk",
			RuleID:   "rule",
		}},
		DiffRaw: "+token := \"sk-1234567890abcdefghijklmnop\"",
	}, "+token := \"sk-1234567890abcdefghijklmnop\"", 1, 1, false), "[REDACTED]") {
		t.Fatal("expected LLM prompt to redact diff")
	}
	t.Setenv("OPENAI_API_KEY", "test-key")
	if modelName, _, err := openAICompatibleModelOptions("openai-compatible", "custom-review", "https://llm.example/v1"); err != nil || modelName != "custom-review" {
		t.Fatalf("openai-compatible options model=%q err=%v", modelName, err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "deepseek-key")
	if modelName, _, err := openAICompatibleModelOptions("deepseek", "", ""); err != nil || modelName != "deepseek-chat" {
		t.Fatalf("deepseek options model=%q err=%v", modelName, err)
	}
	if _, _, err := openAICompatibleModelOptions("http", "x", ""); err == nil {
		t.Fatal("expected http provider to require a base URL")
	}
	if _, _, err := openAICompatibleModelOptions("unknown", "x", ""); err == nil {
		t.Fatal("expected unsupported provider error")
	}
}

func TestSmallParserRuleAndRedactionBranches(t *testing.T) {
	if atoiDefault("bad", 9) != 9 || atoiDefault("", 7) != 7 || atoiDefault("3", 0) != 3 {
		t.Fatal("unexpected atoiDefault result")
	}
	values := []string{"c", "a", "b"}
	sortStrings(values)
	if strings.Join(values, "") != "abc" {
		t.Fatalf("sortStrings = %v", values)
	}
	if severityRank("unknown") != 0 || severityRank(SeverityLow) != 1 {
		t.Fatal("unexpected severity rank")
	}
	if !commandInvocationLooksDynamic(`exec.Command("go", arg)`, "") {
		t.Fatal("expected dynamic command")
	}
	if commandInvocationLooksDynamic(`exec.Command("go", "test")`, "") {
		t.Fatal("did not expect literal command to be dynamic")
	}
	for _, raw := range []string{
		"Authorization: Basic YWRtaW46cGFzc3dvcmQxMjM0NQ==",
		"X-API-Key: abcdefghijklmnopqrstuvwxyz",
		"Bearer abcdefghijklmnopqrstuvwxyz",
		"Basic YWRtaW46cGFzc3dvcmQxMjM0NQ==",
		`postgres://alice:S3cr3tPass@db.internal/app`,
	} {
		if got := redactSecrets(raw); strings.Contains(got, "abcdefghijklmnopqrstuvwxyz") || strings.Contains(got, "S3cr3tPass") {
			t.Fatalf("secret leaked after redaction: %q -> %q", raw, got)
		}
	}
}

func TestReportMetricsAndArtifactEdges(t *testing.T) {
	report := ReviewReport{
		Findings:         []Finding{{Severity: SeverityCritical}},
		Warnings:         []Finding{{Severity: SeverityLow}},
		NeedsHumanReview: []Finding{{Severity: SeverityMedium}},
		SandboxRuns: []SandboxRun{
			{Status: "failed", ErrorType: "timeout", DurationMS: 7},
			{Status: "skipped", ErrorType: "permission_decision"},
		},
		Permissions: []PermissionDecisionRecord{
			{Action: "deny"},
			{Action: "ask"},
		},
	}
	metrics := buildMetrics(report, 15*time.Millisecond)
	if metrics.ToolCallCount != 1 || metrics.PermissionDenyCount != 1 || metrics.PermissionAskCount != 1 ||
		metrics.ErrorTypeCounts["timeout"] != 1 || metrics.ErrorTypeCounts["permission_decision"] != 1 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
	permissionSummary := buildPermissionSummary(report.Permissions)
	if permissionSummary.DenyCount != 1 || permissionSummary.AskCount != 1 ||
		permissionSummary.NeedsHumanReviewCount != 1 {
		t.Fatalf("unexpected permission summary: %+v", permissionSummary)
	}
	if got := permissionDisposition("ask"); got != "needs_human_review" {
		t.Fatalf("ask disposition = %q", got)
	}
	if buildConclusion(nil, nil) == "" ||
		buildConclusion([]Finding{{Severity: SeverityCritical}}, nil) == "" ||
		buildConclusion([]Finding{{Severity: SeverityHigh}}, nil) == "" ||
		buildConclusion(nil, []Finding{{Severity: SeverityLow}}) == "" {
		t.Fatal("expected conclusions for all buckets")
	}
	artifacts, policy := reportArtifacts("task", []ArtifactRecord{
		{Name: "review_report.json", SizeBytes: 10},
		{Name: "review_report.md", SizeBytes: 10},
		{Name: "review_diagnostics.json", SizeBytes: 10},
		{Name: "review_report.zh.md", SizeBytes: 10},
		{Name: "diff_summary.json", SizeBytes: 10},
		{Name: "extra.txt", SizeBytes: 10},
		{Name: "review_report.json", SizeBytes: 2 << 20},
	})
	if len(artifacts) != 5 || policy.RejectedCount != 2 || policy.RetainedCount != 5 {
		t.Fatalf("artifacts=%+v policy=%+v", artifacts, policy)
	}
	if got := RenderMarkdown(ReviewReport{Metrics: AuditMetrics{SeverityCounts: map[string]int{}, ErrorTypeCounts: map[string]int{}}}); !strings.Contains(got, "No high-confidence findings") {
		t.Fatalf("unexpected markdown: %s", got)
	}
	filePath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := WriteReports(filePath, ReviewReport{}); err == nil {
		t.Fatal("expected WriteReports mkdir error when output path is file")
	}
	if artifacts := reportFileArtifacts("task", filepath.Join(t.TempDir(), "missing.json"), filepath.Join(t.TempDir(), "missing.md")); len(artifacts) != 0 {
		t.Fatalf("expected missing report artifacts to be ignored: %v", artifacts)
	}
}

func TestSandboxOutputAndStorageSmallHelpers(t *testing.T) {
	runs := []SandboxRun{
		{Command: "go", Status: "failed", Stderr: "a.go:8: bad"},
		{Command: "staticcheck", Status: "failed", Stderr: "b.go:9:1: warning (SA1000)"},
		{Command: "bash", Status: "failed", Stderr: "c.go:10: bad"},
	}
	pd, err := ParseUnifiedDiff(`diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -6,1 +7,2 @@
 package a
+func A() {}
diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -7,1 +8,2 @@
 package b
+func B() {}
diff --git a/c.go b/c.go
--- a/c.go
+++ b/c.go
@@ -8,1 +9,2 @@
 package c
+func C() {}
`)
	if err != nil {
		t.Fatal(err)
	}
	findings := ParseSandboxFindings(runs, pd)
	if len(findings) != 3 {
		t.Fatalf("sandbox findings = %+v", findings)
	}
	if artifactSize(codeexecutor.File{SizeBytes: 1}) != 1 ||
		artifactSize(codeexecutor.File{Content: "abc"}) != 3 {
		t.Fatal("bad artifactSize")
	}
	if firstNonEmpty("", "x") != "x" || firstNonEmpty("", " ") != "" {
		t.Fatal("bad firstNonEmpty")
	}
	if sandboxTitle("custom", strings.Repeat("x", 120)) == "" ||
		sandboxRuleID("custom", "message") != "sandbox/tool/diagnostic" {
		t.Fatal("bad sandbox defaults")
	}
	if boolInt(true) != 1 || boolInt(false) != 0 {
		t.Fatal("bad boolInt")
	}
	if truncate("abcdef", 3) != "abc...[truncated]" || truncate("abc", 3) != "abc" {
		t.Fatal("bad truncate")
	}
	if wrapStoreErr("op", nil) != nil || wrapStoreErr("op", errors.New("x")) == nil {
		t.Fatal("bad wrapStoreErr")
	}
	var nilStore *Store
	if err := nilStore.Close(); err != nil {
		t.Fatal(err)
	}
	fileRoot := filepath.Join(t.TempDir(), "file-root")
	if err := os.WriteFile(fileRoot, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeSmokeFiles(fileRoot, map[string]string{"a": "b"}); err == nil {
		t.Fatal("expected writeSmokeFiles error for missing parent")
	}
}

func TestStoreDirectBucketsAndMissingRows(t *testing.T) {
	storeIface, err := OpenStore(testContext(t), filepath.Join(t.TempDir(), "reviews.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer storeIface.Close()
	store := storeIface.(*Store)
	if err := store.loadInput(testContext(t), "missing", &ReviewReport{}); err != nil {
		t.Fatal(err)
	}
	if err := store.loadMetrics(testContext(t), "missing", &ReviewReport{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadTaskReport(testContext(t), "missing"); err == nil {
		t.Fatal("expected missing task error")
	}
	report := ReviewReport{
		Task: ReviewTask{
			ID:        "review-direct",
			Status:    StatusCompleted,
			StartedAt: time.Now(),
			EndedAt:   time.Now(),
			InputMode: "unit",
		},
		Input:    DiffSummary{FilesChanged: 1, GoFiles: 1},
		Packages: []GoPackageInfo{{PackagePath: "pkg", PackageName: "pkg", Files: []string{"pkg/a.go"}}},
		Findings: []Finding{{
			Severity:       SeverityHigh,
			Category:       "security",
			File:           "pkg/a.go",
			Line:           1,
			Title:          "finding",
			Evidence:       "x",
			Recommendation: "fix",
			Confidence:     0.9,
			Source:         "unit",
			RuleID:         "unit/finding",
		}},
		Warnings: []Finding{{
			Severity:       SeverityMedium,
			Category:       "warning",
			File:           "pkg/a.go",
			Line:           2,
			Title:          "warning",
			Evidence:       "x",
			Recommendation: "check",
			Confidence:     0.5,
			Source:         "unit",
			RuleID:         "unit/warning",
		}},
		NeedsHumanReview: []Finding{{
			Severity:       SeverityLow,
			Category:       "review",
			File:           "pkg/a.go",
			Line:           3,
			Title:          "review",
			Evidence:       "x",
			Recommendation: "review",
			Confidence:     0.6,
			Source:         "unit",
			RuleID:         "unit/review",
		}},
		SandboxRuns: []SandboxRun{{
			ID:        "run-direct",
			TaskID:    "review-direct",
			Command:   "go",
			Args:      []string{"test", "./..."},
			Executor:  "fake",
			Status:    "skipped",
			StartedAt: time.Now(),
		}},
		Permissions: []PermissionDecisionRecord{{
			ID:          "perm-direct",
			TaskID:      "review-direct",
			Tool:        "workspace_exec",
			Command:     "go test ./...",
			Action:      "allow",
			Disposition: "allow",
			CreatedAt:   time.Now(),
		}},
		Artifacts: []ArtifactRecord{{
			ID:        "artifact-direct",
			TaskID:    "review-direct",
			Name:      "review_report.json",
			Path:      "review_report.json",
			MimeType:  "application/json",
			CreatedAt: time.Now(),
		}},
		ArtifactPolicy: ArtifactPolicy{
			MaxArtifacts:     5,
			MaxBytesPerFile:  1024,
			AllowedFileNames: []string{"review_report.json"},
			RetainedCount:    1,
			RejectedCount:    2,
		},
		Metrics:    AuditMetrics{FindingCount: 1, SeverityCounts: map[string]int{"high": 1}, ErrorTypeCounts: map[string]int{}},
		Conclusion: "done",
	}
	pd := ParsedDiff{RawHash: "hash", Raw: "diff", Summary: report.Input, Packages: report.Packages}
	if err := store.SaveReport(testContext(t), report, pd, "review_report.json", "review_report.md"); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadTaskReport(testContext(t), report.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Findings) != 1 || len(loaded.Warnings) != 1 || len(loaded.NeedsHumanReview) != 1 ||
		len(loaded.SandboxRuns) != 1 || len(loaded.Permissions) != 1 || len(loaded.Artifacts) != 1 {
		t.Fatalf("unexpected loaded report: %+v", loaded)
	}
	if loaded.Permissions[0].Disposition != "allow" {
		t.Fatalf("loaded permission disposition = %q", loaded.Permissions[0].Disposition)
	}
	if loaded.PermissionSummary.AllowCount != 1 {
		t.Fatalf("loaded permission summary = %+v", loaded.PermissionSummary)
	}
	if loaded.ArtifactPolicy.MaxArtifacts != report.ArtifactPolicy.MaxArtifacts ||
		loaded.ArtifactPolicy.MaxBytesPerFile != report.ArtifactPolicy.MaxBytesPerFile ||
		strings.Join(loaded.ArtifactPolicy.AllowedFileNames, "\x00") != strings.Join(report.ArtifactPolicy.AllowedFileNames, "\x00") ||
		loaded.ArtifactPolicy.RejectedCount != report.ArtifactPolicy.RejectedCount ||
		loaded.ArtifactPolicy.RetainedCount != report.ArtifactPolicy.RetainedCount {
		t.Fatalf("loaded artifact policy = %+v", loaded.ArtifactPolicy)
	}
	if err := store.SaveReport(testContext(t), report, pd, "review_report.json", "review_report.md"); err == nil {
		t.Fatal("expected duplicate task save error")
	}
	if err := ensureColumn(testContext(t), store.db, "review_tasks", "status", "TEXT"); err != nil {
		t.Fatal(err)
	}
	if err := ensureColumn(testContext(t), store.db, "review_tasks", "coverage_extra", "TEXT DEFAULT ''"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(testContext(t),
		`UPDATE review_inputs SET summary_json = ? WHERE task_id = ?`, "{bad", report.Task.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.loadInput(testContext(t), report.Task.ID, &ReviewReport{}); err == nil {
		t.Fatal("expected bad input JSON error")
	}
	if _, err := store.db.ExecContext(testContext(t),
		`UPDATE review_inputs SET summary_json = ?, packages_json = ? WHERE task_id = ?`, "{}", "{bad", report.Task.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.loadInput(testContext(t), report.Task.ID, &ReviewReport{}); err == nil {
		t.Fatal("expected bad packages JSON error")
	}
	if _, err := store.db.ExecContext(testContext(t),
		`UPDATE audit_metrics SET metrics_json = ? WHERE task_id = ?`, "{bad", report.Task.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.loadMetrics(testContext(t), report.Task.ID, &ReviewReport{}); err == nil {
		t.Fatal("expected bad metrics JSON error")
	}
}

func TestStoreClosedDatabaseErrors(t *testing.T) {
	storeIface, err := OpenStore(testContext(t), filepath.Join(t.TempDir(), "reviews.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := storeIface.(*Store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Init(testContext(t)); err == nil {
		t.Fatal("expected init on closed db to fail")
	}
	if _, err := store.SchemaVersion(testContext(t)); err == nil {
		t.Fatal("expected schema version on closed db to fail")
	}
	if err := store.SaveReport(testContext(t), ReviewReport{}, ParsedDiff{}, "", ""); err == nil {
		t.Fatal("expected save on closed db to fail")
	}
	if _, err := store.LoadTaskReport(testContext(t), "x"); err == nil {
		t.Fatal("expected load on closed db to fail")
	}
	if err := store.loadFindings(testContext(t), "x", &ReviewReport{}); err == nil {
		t.Fatal("expected loadFindings on closed db to fail")
	}
	if err := store.loadSandboxRuns(testContext(t), "x", &ReviewReport{}); err == nil {
		t.Fatal("expected loadSandboxRuns on closed db to fail")
	}
	if err := store.loadPermissionDecisions(testContext(t), "x", &ReviewReport{}); err == nil {
		t.Fatal("expected loadPermissionDecisions on closed db to fail")
	}
	if err := store.loadArtifacts(testContext(t), "x", &ReviewReport{}); err == nil {
		t.Fatal("expected loadArtifacts on closed db to fail")
	}
}

func TestContainerSmokeRepoCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := prepareContainerSmokeRepo(ctx); err == nil {
		t.Fatal("expected canceled context to fail smoke repo preparation")
	}
	if err := runSmokeGit(ctx, t.TempDir(), "status"); err == nil {
		t.Fatal("expected canceled context to fail git command")
	}
}

func TestRunReviewStoresTaskScopedFinalReportArtifacts(t *testing.T) {
	out := t.TempDir()
	first, _, _, err := RunReview(testContext(t), ReviewConfig{
		Fixture:   "security_issue",
		OutputDir: out,
		DBPath:    filepath.Join(out, "reviews.sqlite"),
		DryRun:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, _, _, err := RunReview(testContext(t), ReviewConfig{
		Fixture:   "no_issue",
		OutputDir: out,
		DBPath:    filepath.Join(out, "reviews2.sqlite"),
		DryRun:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Task.ID == second.Task.ID {
		t.Fatal("expected distinct tasks")
	}
	for _, report := range []ReviewReport{first, second} {
		for _, artifact := range report.Artifacts {
			if artifact.Name != "review_report.json" && artifact.Name != "review_report.md" &&
				artifact.Name != "review_diagnostics.json" && artifact.Name != "review_report.zh.md" {
				continue
			}
			if !strings.Contains(filepath.ToSlash(artifact.Path), "/"+report.Task.ID+"/") {
				t.Fatalf("report artifact path is not task-scoped: %+v", artifact)
			}
			st, err := os.Stat(artifact.Path)
			if err != nil {
				t.Fatalf("artifact path missing after later task: %+v err=%v", artifact, err)
			}
			if st.Size() != artifact.SizeBytes {
				t.Fatalf("artifact size = %d, stat = %d for %+v", artifact.SizeBytes, st.Size(), artifact)
			}
			if artifact.Name == "review_report.json" {
				data, err := os.ReadFile(artifact.Path)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(data), report.Task.ID) {
					t.Fatalf("task-scoped report does not contain its task id: %s", artifact.Path)
				}
			}
		}
	}
}

func TestPrepareSandboxRepoSnapshotRejectsOversizedFileAndCleansPartialSnapshot(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	writeTestFile(t, filepath.Join(repo, "go.mod"), "module example.com/large\n\ngo 1.23\n")
	if err := os.WriteFile(filepath.Join(repo, "large.bin"), []byte(strings.Repeat("x", int(sandboxSnapshotMaxFileBytes)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	var budgetErr *sandboxSnapshotBudgetError
	snapshot, cleanup, err := prepareSandboxRepoSnapshot(testContext(t), repo)
	if err == nil {
		_ = cleanup()
		t.Fatalf("expected oversized snapshot to fail, got %s", snapshot)
	}
	if !errors.As(err, &budgetErr) {
		t.Fatalf("expected sandboxSnapshotBudgetError, got %T %v", err, err)
	}
	engine := &mockWorkspaceEngine{}
	runner := &WorkspaceSandboxRunner{
		executorName:     "container",
		engine:           engine,
		timeout:          time.Second,
		outputLimitBytes: 1024,
		outputDir:        t.TempDir(),
	}
	result := runner.RunChecks(testContext(t), "task-large-snapshot", repo, ParsedDiff{Raw: "diff"})
	var unavailable int
	for _, run := range result.Runs {
		if run.ErrorType == "snapshot_budget_exceeded" && run.Status == "skipped" {
			unavailable++
		}
	}
	if unavailable != 3 {
		t.Fatalf("expected repo checks unavailable after budget exceed, got %+v", result.Runs)
	}
}

func TestSplitDiffForLLMBoundsOversizedHunk(t *testing.T) {
	raw := `diff --git a/big.go b/big.go
--- a/big.go
+++ b/big.go
@@ -1,1 +1,2 @@
 package big
+` + strings.Repeat("x", llmMaxDiffBytes*2)
	chunks, truncated := splitDiffForLLM(raw, llmMaxDiffBytes)
	if !truncated {
		t.Fatal("expected oversized hunk to be marked truncated")
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one bounded chunk")
	}
	for _, chunk := range chunks {
		if len(chunk) > llmMaxDiffBytes+len("...[truncated]") {
			t.Fatalf("chunk exceeds budget: %d", len(chunk))
		}
	}
	prompt := buildLLMReviewPrompt(LLMReviewConfig{
		InputSummary: DiffSummary{FilesChanged: 1, GoFiles: 1},
	}, chunks[0], 1, len(chunks), truncated)
	if !strings.Contains(prompt, "omitted") {
		t.Fatalf("expected truncation to be visible in prompt: %s", prompt)
	}
}

func TestSplitDiffForLLMEnforcesTotalChunkLimit(t *testing.T) {
	var raw strings.Builder
	for i := 0; i < llmMaxDiffChunks+3; i++ {
		raw.WriteString("diff --git a/file")
		raw.WriteString(string(rune('a' + i)))
		raw.WriteString(".go b/file")
		raw.WriteString(string(rune('a' + i)))
		raw.WriteString(".go\n--- a/file.go\n+++ b/file.go\n@@ -1,1 +1,2 @@\n package p\n+")
		raw.WriteString(strings.Repeat("x", llmMaxDiffBytes/2))
		raw.WriteString("\n")
	}
	chunks, truncated := splitDiffForLLM(raw.String(), llmMaxDiffBytes)
	if !truncated {
		t.Fatal("expected total chunk cap to mark LLM input truncated")
	}
	if len(chunks) != llmMaxDiffChunks {
		t.Fatalf("chunks = %d, want %d", len(chunks), llmMaxDiffChunks)
	}
}

func TestBuildLLMReviewPromptBoundsRuleFindings(t *testing.T) {
	findings := make([]Finding, llmMaxRuleFindings+2)
	for i := range findings {
		findings[i] = Finding{
			Severity: SeverityHigh,
			File:     strings.Repeat("p", 200) + ".go",
			Line:     i + 1,
			Title:    strings.Repeat("t", 220),
			RuleID:   "rule/test",
		}
	}
	prompt := buildLLMReviewPrompt(LLMReviewConfig{
		InputSummary: DiffSummary{FilesChanged: 1, GoFiles: 1},
		RuleFindings: findings,
	}, "diff --git a/a.go b/a.go", 1, 1, false)
	if !strings.Contains(prompt, "additional rule finding(s) omitted") {
		t.Fatalf("expected prompt to disclose omitted rule findings: %s", prompt)
	}
	if strings.Contains(prompt, strings.Repeat("t", 220)) {
		t.Fatalf("expected long rule titles to be truncated: %s", prompt)
	}
}

func TestSplitNULFileListRejectsTooManyFiles(t *testing.T) {
	raw := []byte("a.go\x00b.go\x00")
	var budgetErr *sandboxSnapshotBudgetError
	if _, err := splitNULFileList(raw, 1); !errors.As(err, &budgetErr) {
		t.Fatalf("expected file-count budget error, got %v", err)
	}
}

func TestSandboxSnapshotBudgetCountsCopiedBytes(t *testing.T) {
	budget := sandboxSnapshotBudget{maxFiles: 1, maxTotalBytes: 6, maxFileBytes: 5}
	if err := budget.reserveFile("grow.txt", 1); err != nil {
		t.Fatal(err)
	}
	if err := budget.reserveBytes("grow.txt", 3); err != nil {
		t.Fatal(err)
	}
	var budgetErr *sandboxSnapshotBudgetError
	if err := budget.reserveBytes("grow.txt", 3); !errors.As(err, &budgetErr) {
		t.Fatalf("expected copied byte budget error, got %v", err)
	}
}

func TestRunAllFixturesRejectsExternalOutputRoot(t *testing.T) {
	root := t.TempDir()
	sentinelDir := filepath.Join(root, "security_issue")
	writeTestFile(t, filepath.Join(sentinelDir, "sentinel.txt"), "keep")
	cmd := exec.Command("bash", filepath.Join(exampleDir(), "scripts", "run_all_fixtures.sh"), root)
	cmd.Dir = exampleDir()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected external output root to be rejected:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(sentinelDir, "sentinel.txt")); statErr != nil {
		t.Fatalf("sentinel should not be deleted: %v", statErr)
	}
}

// -- Fix #1: MaxOutputBytes propagated to RunProgramSpec -------------------

func TestRunProgramPassesMaxOutputBytesToSpec(t *testing.T) {
	// Verify that runProgram forwards outputLimitBytes as MaxOutputBytes so
	// executors that support streaming can enforce the cap at the source rather
	// than after the full output is already in memory.
	engine := &mockWorkspaceEngine{}
	runner := &WorkspaceSandboxRunner{
		executorName:     "mock",
		engine:           engine,
		timeout:          time.Second,
		outputLimitBytes: 8,
		outputDir:        t.TempDir(),
	}
	runner.runProgram(testContext(t), codeexecutor.Workspace{ID: "ws"}, "task", "go", []string{"test", "./..."}, ".")
	if len(engine.runSpecs) == 0 {
		t.Fatal("expected at least one RunProgram call")
	}
	spec := engine.runSpecs[len(engine.runSpecs)-1]
	if spec.MaxOutputBytes != 8 {
		t.Fatalf("MaxOutputBytes = %d, want 8", spec.MaxOutputBytes)
	}
}

func TestRunProgramBoundsRetainedBytesAtOutputLimit(t *testing.T) {
	// When a mock executor honours MaxOutputBytes, the retained output must
	// not exceed the configured cap.  limitText acts as a safety net for
	// executors that do not yet honour MaxOutputBytes at the source; when the
	// executor already bounded the output, limitText does not truncate a second
	// time, so OutputTruncated is not necessarily set in that path.
	const limit = 16
	engine := &largeOutputEngine{outputBytes: limit * 10}
	runner := &WorkspaceSandboxRunner{
		executorName:     "mock",
		engine:           engine,
		timeout:          time.Second,
		outputLimitBytes: limit,
		outputDir:        t.TempDir(),
	}
	run := runner.runProgram(testContext(t), codeexecutor.Workspace{ID: "ws"}, "task", "go", []string{"test", "./..."}, ".")
	// Primary invariant: retained bytes must not exceed the cap regardless of
	// whether the executor or limitText applied the bound.
	retained := len(run.Stdout) + len(run.Stderr)
	maxAllowed := limit + len("\n...[truncated]")
	if retained > maxAllowed {
		t.Fatalf("retained output too large: stdout=%d stderr=%d total=%d limit=%d",
			len(run.Stdout), len(run.Stderr), retained, limit)
	}
}

// largeOutputEngine returns output of exactly outputBytes characters.
type largeOutputEngine struct {
	outputBytes int
}

func (e *largeOutputEngine) Manager() codeexecutor.WorkspaceManager { return e }
func (e *largeOutputEngine) FS() codeexecutor.WorkspaceFS           { return e }
func (e *largeOutputEngine) Runner() codeexecutor.ProgramRunner     { return e }
func (e *largeOutputEngine) Describe() codeexecutor.Capabilities {
	return codeexecutor.Capabilities{Isolation: "large-output-mock"}
}
func (e *largeOutputEngine) CreateWorkspace(_ context.Context, _ string, _ codeexecutor.WorkspacePolicy) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{ID: "ws"}, nil
}
func (e *largeOutputEngine) Cleanup(_ context.Context, _ codeexecutor.Workspace) error { return nil }
func (e *largeOutputEngine) PutFiles(_ context.Context, _ codeexecutor.Workspace, _ []codeexecutor.PutFile) error {
	return nil
}
func (e *largeOutputEngine) StageDirectory(_ context.Context, _ codeexecutor.Workspace, _, _ string, _ codeexecutor.StageOptions) error {
	return nil
}
func (e *largeOutputEngine) Collect(_ context.Context, _ codeexecutor.Workspace, _ []string) ([]codeexecutor.File, error) {
	return nil, nil
}
func (e *largeOutputEngine) StageInputs(_ context.Context, _ codeexecutor.Workspace, _ []codeexecutor.InputSpec) error {
	return nil
}
func (e *largeOutputEngine) CollectOutputs(_ context.Context, _ codeexecutor.Workspace, _ codeexecutor.OutputSpec) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}
func (e *largeOutputEngine) RunProgram(_ context.Context, _ codeexecutor.Workspace, spec codeexecutor.RunProgramSpec) (codeexecutor.RunResult, error) {
	stdout := strings.Repeat("x", e.outputBytes)
	// Honour MaxOutputBytes if set, simulating a streaming executor.
	if spec.MaxOutputBytes > 0 && len(stdout) > spec.MaxOutputBytes {
		stdout = stdout[:spec.MaxOutputBytes]
	}
	return codeexecutor.RunResult{ExitCode: 1, Stdout: stdout}, nil
}

// -- Fix #2: skipped core checks surfaced as incomplete-analysis -----------

func TestSandboxReviewItemsSurfacesSkippedCoreChecks(t *testing.T) {
	// Runs for go test and go vet that are skipped due to infrastructure
	// constraints must produce a needs_human_review incomplete_analysis finding
	// so the caller cannot conclude "no issues found" for an unreviewed module.
	for _, reason := range []string{"dependency_unavailable", "snapshot_budget_exceeded", "e2b_egress_not_enforced"} {
		t.Run(reason, func(t *testing.T) {
			runs := []SandboxRun{
				{Command: "go", Args: []string{"test", "./..."}, Status: "skipped", ErrorType: reason, Stderr: "repo checks unavailable"},
				{Command: "go", Args: []string{"vet", "./..."}, Status: "skipped", ErrorType: reason, Stderr: "repo checks unavailable"},
				{Command: "staticcheck", Args: []string{"./..."}, Status: "skipped", ErrorType: "tool_unavailable"},
			}
			items := sandboxReviewItems(runs, ParsedDiff{}, nil)
			// staticcheck with tool_unavailable must NOT produce a finding.
			for _, f := range items {
				if strings.Contains(f.Evidence, "staticcheck") && f.RuleID == "sandbox/core-check-unavailable" {
					t.Fatalf("staticcheck tool_unavailable must not produce incomplete-analysis finding: %+v", f)
				}
			}
			var coreItems []Finding
			for _, f := range items {
				if f.RuleID == "sandbox/core-check-unavailable" {
					coreItems = append(coreItems, f)
				}
			}
			if len(coreItems) != 2 {
				t.Fatalf("reason=%s: want 2 incomplete-analysis findings (go test + go vet), got %d: %+v", reason, len(coreItems), items)
			}
			for _, f := range coreItems {
				if f.Category != "incomplete_analysis" {
					t.Fatalf("expected category=incomplete_analysis, got %q", f.Category)
				}
				if !strings.Contains(f.Evidence, reason) {
					t.Fatalf("expected evidence to contain reason %q, got %q", reason, f.Evidence)
				}
			}
		})
	}
}

func TestSandboxReviewItemsDoesNotSurfaceOptionalToolSkip(t *testing.T) {
	// A staticcheck skip with tool_unavailable is an optional tool; it must
	// not produce an incomplete-analysis finding.
	runs := []SandboxRun{
		{Command: "staticcheck", Args: []string{"./..."}, Status: "skipped", ErrorType: "tool_unavailable"},
	}
	items := sandboxReviewItems(runs, ParsedDiff{}, nil)
	for _, f := range items {
		if f.RuleID == "sandbox/core-check-unavailable" {
			t.Fatalf("staticcheck skip must not produce core-unavailable finding: %+v", f)
		}
	}
}

// -- Fix #3: preprocessing failures persist a StatusFailed audit record ----

func TestRunReviewPersistsFailedTaskOnMalformedDiff(t *testing.T) {
	// Before the fix, any preprocessing error (skill load, input read, diff
	// parse) returned without leaving a DB record; a failed attempt was
	// indistinguishable from no attempt.  The task must now be persisted as
	// pending before preprocessing and transition to failed on error.
	out := t.TempDir()
	dbPath := filepath.Join(out, "reviews.sqlite")
	diffPath := filepath.Join(out, "bad.diff")
	if err := os.WriteFile(diffPath, []byte("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ malformed @@\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Install a trigger before the run so the test observes the state
	// transition itself, rather than only the final row.
	seed, err := OpenStore(testContext(t), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	seedStore := seed.(*Store)
	if _, err := seedStore.db.ExecContext(testContext(t), `
		CREATE TABLE task_status_transitions (
			task_id TEXT NOT NULL,
			old_status TEXT NOT NULL,
			new_status TEXT NOT NULL
		);
		CREATE TRIGGER task_status_transition_audit
		AFTER UPDATE OF status ON review_tasks
		BEGIN
			INSERT INTO task_status_transitions(task_id, old_status, new_status)
			VALUES (OLD.id, OLD.status, NEW.status);
		END;
	`); err != nil {
		_ = seed.Close()
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	// Trigger a parse_diff failure with an invalid hunk header.
	_, _, _, runErr := RunReview(testContext(t), ReviewConfig{
		DiffFile:  diffPath,
		OutputDir: out,
		DBPath:    dbPath,
	})
	if runErr == nil {
		t.Fatal("expected error for malformed diff file")
	}

	// The failed attempt must be visible in the DB.
	store, openErr := OpenStore(testContext(t), dbPath)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer store.Close()

	rows, queryErr := store.(*Store).db.QueryContext(testContext(t),
		`SELECT id, status FROM review_tasks WHERE status = ?`, string(StatusFailed))
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		count++
		var id, status string
		if scanErr := rows.Scan(&id, &status); scanErr != nil {
			t.Fatal(scanErr)
		}
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if count == 0 {
		t.Fatal("expected at least one failed review_tasks row after preprocessing failure")
	}

	auditStore, err := OpenStore(testContext(t), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer auditStore.Close()
	var oldStatus, newStatus string
	if err := auditStore.(*Store).db.QueryRowContext(testContext(t),
		`SELECT old_status, new_status FROM task_status_transitions LIMIT 1`,
	).Scan(&oldStatus, &newStatus); err != nil {
		t.Fatalf("expected pending-to-failed transition: %v", err)
	}
	if oldStatus != string(StatusPending) || newStatus != string(StatusFailed) {
		t.Fatalf("status transition = %q -> %q, want %q -> %q",
			oldStatus, newStatus, StatusPending, StatusFailed)
	}

	var metricsJSON string
	if err := store.(*Store).db.QueryRowContext(testContext(t),
		`SELECT metrics_json FROM audit_metrics WHERE task_id IN (
			SELECT id FROM review_tasks WHERE status = ?
		) LIMIT 1`, string(StatusFailed),
	).Scan(&metricsJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metricsJSON, "parse_diff") {
		t.Fatalf("expected parse_diff audit classification, got %s", metricsJSON)
	}
}

func TestSaveFailedTaskPersistsMinimalRecord(t *testing.T) {
	store, err := OpenStore(testContext(t), filepath.Join(t.TempDir(), "reviews.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	task := ReviewTask{
		ID:        "review-failed-test",
		Status:    StatusFailed,
		StartedAt: time.Now().Add(-time.Second),
		EndedAt:   time.Now(),
	}
	if err := store.SaveFailedTask(testContext(t), task, "parse_diff"); err != nil {
		t.Fatal(err)
	}

	// Verify it's in the DB.
	var status string
	if err := store.(*Store).db.QueryRowContext(testContext(t),
		`SELECT status FROM review_tasks WHERE id = ?`, task.ID,
	).Scan(&status); err != nil {
		t.Fatalf("failed to load saved task: %v", err)
	}
	if status != string(StatusFailed) {
		t.Fatalf("status = %q, want %q", status, StatusFailed)
	}

	// Verify duplicate saves fail (no orphaned records from retries).
	if err := store.SaveFailedTask(testContext(t), task, "parse_diff"); err == nil {
		t.Fatal("expected duplicate SaveFailedTask to fail")
	}
}
