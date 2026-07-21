//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/inputsource"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandboxrun"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

type plannerFunc func(ctx context.Context, req PlanRequest) (review.ReviewPlan, error)

func (f plannerFunc) PlanReview(ctx context.Context, req PlanRequest) (review.ReviewPlan, error) {
	return f(ctx, req)
}

func TestRunAllowsFakeRuntimeWithoutModel(t *testing.T) {
	outDir := t.TempDir()
	result, err := Run(context.Background(), Options{
		FixtureDir: filepath.Join("..", "..", "testdata", "fixtures"),
		OutDir:     outDir,
		DBPath:     filepath.Join(outDir, "review_agent.db"),
		Runtime:    "fake",
		Now:        fixedTestTime(),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Report.Plan.Model != "mock-model" {
		t.Fatalf("plan model = %q, want mock-model", result.Report.Plan.Model)
	}
	if result.Report.Plan.Provider != "mock" {
		t.Fatalf("plan provider = %q, want mock", result.Report.Plan.Provider)
	}
}

func TestRunRequiresModelForNonFakeRuntime(t *testing.T) {
	outDir := t.TempDir()
	dbPath := filepath.Join(outDir, "review_agent.db")
	_, err := Run(context.Background(), Options{
		FixtureDir: filepath.Join("..", "..", "testdata", "fixtures"),
		OutDir:     outDir,
		DBPath:     dbPath,
		Runtime:    "container",
		Now:        fixedTestTime(),
	})
	if err == nil {
		t.Fatal("Run() error = nil, want model configuration error")
	}
	if !strings.Contains(err.Error(), "model orchestration requires --model or MODEL") {
		t.Fatalf("Run() error = %q, want missing model message", err)
	}
	assertStoredTask(t, dbPath, fixedTestTime(), func(report store.TaskReport) {
		if report.Task.Status != review.TaskStatusFailed {
			t.Fatalf("stored task status = %q, want failed", report.Task.Status)
		}
		if !strings.Contains(report.Task.Error, "model orchestration requires --model or MODEL") {
			t.Fatalf("stored task error = %q, want missing model message", report.Task.Error)
		}
		if report.Task.FinishedAt == nil || report.Task.FinishedAt.IsZero() {
			t.Fatal("stored task finished_at = nil/zero, want non-zero")
		}
	})
}

func TestRunRecordsConfiguredModelPlan(t *testing.T) {
	outDir := t.TempDir()
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("model path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q, want bearer test key", got)
		}
		var body chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode model request: %v", err)
		}
		if body.Model != "gpt-review" {
			t.Fatalf("request model = %q, want gpt-review", body.Model)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"commands\":[\"go test ./...\"],\"rule_sources\":[\"skills/code-review/SKILL.md\",\"skills/code-review/docs/rules.md\"]}"}}]}`))
	}))
	defer modelServer.Close()
	result, err := Run(context.Background(), Options{
		FixtureDir: filepath.Join("..", "..", "testdata", "fixtures"),
		OutDir:     outDir,
		DBPath:     filepath.Join(outDir, "review_agent.db"),
		Model:      "gpt-review",
		Runtime:    "container",
		Now:        fixedTestTime(),
		Planner:    EnvPlanner{APIKey: "test-key", BaseURL: modelServer.URL, HTTPClient: modelServer.Client()},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Report.Plan.Model != "gpt-review" {
		t.Fatalf("plan model = %q, want gpt-review", result.Report.Plan.Model)
	}
	if result.Report.Plan.Provider != "openai_compatible" {
		t.Fatalf("plan provider = %q, want openai_compatible", result.Report.Plan.Provider)
	}
	if result.Report.Plan.Source != "model_response" {
		t.Fatalf("plan source = %q, want model_response", result.Report.Plan.Source)
	}
	raw, err := os.ReadFile(result.MarkdownPath)
	if err != nil {
		t.Fatalf("ReadFile(markdown) error = %v", err)
	}
	if !strings.Contains(string(raw), "## Model Plan") || !strings.Contains(string(raw), "- model: gpt-review") {
		t.Fatalf("markdown report does not contain configured model plan:\n%s", raw)
	}
}

func TestPlanReviewFiltersModelPlannedCommandsToAllowlist(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"commands\":[\"go test ./...\",\"go env\",\"curl https://example.com\",\" go vet ./... \",\"go test ./...\"],\"rule_sources\":[\"skills/code-review/SKILL.md\"]}"}}]}`))
	}))
	defer modelServer.Close()
	plan, err := (EnvPlanner{
		APIKey:     "test-key",
		BaseURL:    modelServer.URL,
		HTTPClient: modelServer.Client(),
	}).PlanReview(context.Background(), PlanRequest{
		Model:   "gpt-review",
		Runtime: "container",
		Skill:   defaultSkillName,
	})
	if err != nil {
		t.Fatalf("PlanReview() error = %v", err)
	}
	want := []string{"go test ./...", "go vet ./..."}
	if !reflect.DeepEqual(plan.Commands, want) {
		t.Fatalf("plan commands = %#v, want %#v", plan.Commands, want)
	}
	for _, command := range plan.Commands {
		if command == "go env" || strings.HasPrefix(command, "curl ") {
			t.Fatalf("unlisted command was not filtered: %q", command)
		}
	}
}

func TestWorkspaceRuntimeEnvProvidesContainerGoCacheDefaults(t *testing.T) {
	for _, key := range []string{"HOME", "GOCACHE", "GOMODCACHE", "GOPATH"} {
		t.Setenv(key, "")
	}
	t.Setenv("GOPROXY", "https://proxy.example,direct")
	t.Setenv("GOSUMDB", "sum.example")
	t.Setenv("GOTOOLCHAIN", "")
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("CGO_ENABLED", "0")

	env := workspaceRuntimeEnv("container")

	want := map[string]string{
		"HOME":        "/tmp",
		"GOCACHE":     "/tmp/go-build",
		"GOMODCACHE":  "/go/pkg/mod",
		"GOPATH":      "/go",
		"GOPROXY":     "https://proxy.example,direct",
		"GOSUMDB":     "sum.example",
		"GOTOOLCHAIN": "local",
		"GOFLAGS":     "-mod=mod",
		"CGO_ENABLED": "0",
	}
	for key, value := range want {
		if env[key] != value {
			t.Fatalf("%s = %q, want %q", key, env[key], value)
		}
	}
}

func TestWorkspaceRuntimeEnvUsesContainerPathsForNonLocalRuntime(t *testing.T) {
	t.Setenv("HOME", "/custom-home")
	t.Setenv("GOCACHE", "/custom-cache")
	t.Setenv("GOMODCACHE", "/custom-mod-cache")
	t.Setenv("GOPATH", "/custom-go")
	t.Setenv("GOTOOLCHAIN", "auto")

	env := workspaceRuntimeEnv("container")

	want := map[string]string{
		"HOME":        "/tmp",
		"GOCACHE":     "/tmp/go-build",
		"GOMODCACHE":  "/go/pkg/mod",
		"GOPATH":      "/go",
		"GOTOOLCHAIN": "auto",
	}
	for key, value := range want {
		if env[key] != value {
			t.Fatalf("%s = %q, want %q", key, env[key], value)
		}
	}
}

func TestWorkspaceRuntimeEnvKeepsLocalGoCacheValues(t *testing.T) {
	t.Setenv("HOME", "/custom-home")
	t.Setenv("GOCACHE", "/custom-cache")
	t.Setenv("GOMODCACHE", "/custom-mod-cache")
	t.Setenv("GOPATH", "/custom-go")

	env := workspaceRuntimeEnv("local")

	want := map[string]string{
		"HOME":       "/custom-home",
		"GOCACHE":    "/custom-cache",
		"GOMODCACHE": "/custom-mod-cache",
		"GOPATH":     "/custom-go",
	}
	for key, value := range want {
		if env[key] != value {
			t.Fatalf("%s = %q, want %q", key, env[key], value)
		}
	}
}

func TestWorkspaceRuntimeCwdScopesCommandsToReviewAgentModule(t *testing.T) {
	if got := newSandboxWorkspace("").runtimeCwd("container"); got != "work/examples/code_review_agent" {
		t.Fatalf("container cwd = %q, want work/examples/code_review_agent", got)
	}
	if got := newSandboxWorkspace("").runtimeCwd("e2b"); got != "work/examples/code_review_agent" {
		t.Fatalf("e2b cwd = %q, want work/examples/code_review_agent", got)
	}
	if got := newSandboxWorkspace("").runtimeCwd("local"); got != "examples/code_review_agent" {
		t.Fatalf("local cwd = %q, want examples/code_review_agent", got)
	}
}

func TestWorkspaceRuntimeCwdUsesSelectedRepoPath(t *testing.T) {
	workspace := newSandboxWorkspace("/tmp/target-repo")
	if got := workspace.runtimeCwd("container"); got != "work" {
		t.Fatalf("container cwd = %q, want work", got)
	}
	if got := workspace.runtimeCwd("e2b"); got != "work" {
		t.Fatalf("e2b cwd = %q, want work", got)
	}
	if got := workspace.runtimeCwd("local"); got != "." {
		t.Fatalf("local cwd = %q, want .", got)
	}
}

func TestSandboxWorkDirUsesSelectedRepoPath(t *testing.T) {
	targetRepo := t.TempDir()
	got, err := newSandboxWorkspace(targetRepo).root()
	if err != nil {
		t.Fatalf("root() error = %v", err)
	}
	want, err := filepath.Abs(targetRepo)
	if err != nil {
		t.Fatalf("Abs() error = %v", err)
	}
	if got != want {
		t.Fatalf("root() = %q, want %q", got, want)
	}
}

func TestRunTaskIDIncludesFullRunTimestamp(t *testing.T) {
	diff := "diff --git a/a.go b/a.go\n"
	first := runTaskID(diff, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	second := runTaskID(diff, time.Date(2026, 7, 6, 12, 0, 0, 1, time.UTC))
	if first == second {
		t.Fatalf("runTaskID reused %q for repeated same-day runs", first)
	}
}

func TestValidateRuntimePolicyRejectsUntrustedLocalRuntime(t *testing.T) {
	if err := validateRuntimePolicy("local", false); err == nil {
		t.Fatal("validateRuntimePolicy(local, false) error = nil, want rejection")
	}
	if err := validateRuntimePolicy("LOCAL", true); err != nil {
		t.Fatalf("validateRuntimePolicy(LOCAL, true) error = %v, want nil", err)
	}
	if err := validateRuntimePolicy("fake", false); err != nil {
		t.Fatalf("validateRuntimePolicy(fake, false) error = %v, want nil", err)
	}
}

func TestRunRejectsLocalRuntimeWithoutTrustedOptIn(t *testing.T) {
	outDir := t.TempDir()
	calledPlanner := false
	_, err := Run(context.Background(), Options{
		FixtureDir: filepath.Join("..", "..", "testdata", "fixtures"),
		OutDir:     outDir,
		DBPath:     filepath.Join(outDir, "review_agent.db"),
		Runtime:    "local",
		Now:        fixedTestTime(),
		Planner: plannerFunc(func(ctx context.Context, req PlanRequest) (review.ReviewPlan, error) {
			calledPlanner = true
			return review.ReviewPlan{}, errors.New("planner should not be called")
		}),
	})
	if err == nil {
		t.Fatal("Run() error = nil, want local runtime rejection")
	}
	if !strings.Contains(err.Error(), "--allow-trusted-local") {
		t.Fatalf("Run() error = %q, want allow-trusted-local guidance", err)
	}
	if calledPlanner {
		t.Fatal("planner was called for rejected local runtime")
	}
}

func TestRunFinishesFailedTaskAfterCancellation(t *testing.T) {
	outDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := Run(ctx, Options{
		FixtureDir: filepath.Join("..", "..", "testdata", "fixtures"),
		OutDir:     outDir,
		DBPath:     filepath.Join(outDir, "review_agent.db"),
		Runtime:    "fake",
		Now:        fixedTestTime(),
		Planner: plannerFunc(func(ctx context.Context, req PlanRequest) (review.ReviewPlan, error) {
			cancel()
			<-ctx.Done()
			return review.ReviewPlan{}, ctx.Err()
		}),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	assertStoredTask(t, filepath.Join(outDir, "review_agent.db"), fixedTestTime(), func(report store.TaskReport) {
		if report.Task.Status != review.TaskStatusFailed {
			t.Fatalf("stored task status = %q, want failed", report.Task.Status)
		}
		if report.Task.FinishedAt == nil || report.Task.FinishedAt.IsZero() {
			t.Fatal("stored task finished_at = nil/zero, want non-zero")
		}
		if !strings.Contains(report.Task.Error, context.Canceled.Error()) {
			t.Fatalf("stored task error = %q, want context canceled", report.Task.Error)
		}
	})
}

func TestRunUsesSharedInjectedCompletionTimestamp(t *testing.T) {
	outDir := t.TempDir()
	startedAt := fixedTestTime()
	finishedAt := startedAt.Add(42 * time.Second)
	result, err := Run(context.Background(), Options{
		FixtureDir: filepath.Join("..", "..", "testdata", "fixtures"),
		OutDir:     outDir,
		DBPath:     filepath.Join(outDir, "review_agent.db"),
		Runtime:    "fake",
		Now:        startedAt,
		FinishedAt: finishedAt,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Report.Task.FinishedAt == nil || !result.Report.Task.FinishedAt.Equal(finishedAt) {
		t.Fatalf("report finished_at = %v, want %v", result.Report.Task.FinishedAt, finishedAt)
	}
	if result.Report.Metrics.TotalDurationMillis != finishedAt.Sub(startedAt).Milliseconds() {
		t.Fatalf("total duration ms = %d, want %d", result.Report.Metrics.TotalDurationMillis, finishedAt.Sub(startedAt).Milliseconds())
	}
	for _, artifact := range result.Report.Artifacts {
		if !artifact.CreatedAt.Equal(finishedAt) {
			t.Fatalf("artifact %s created_at = %v, want %v", artifact.Kind, artifact.CreatedAt, finishedAt)
		}
	}
	assertStoredTask(t, filepath.Join(outDir, "review_agent.db"), startedAt, func(report store.TaskReport) {
		if report.Task.FinishedAt == nil || !report.Task.FinishedAt.Equal(finishedAt) {
			t.Fatalf("stored task finished_at = %v, want %v", report.Task.FinishedAt, finishedAt)
		}
	})
}

func TestSummarizeOutcomeWarnsForFileListInput(t *testing.T) {
	summary := summarizeOutcome(
		inputsource.Source{Type: review.InputTypeFileList},
		[]review.DiffFile{{NewPath: "pkg/a.go"}},
		nil,
		nil,
		review.ReviewPlan{Model: "mock-model", Skill: defaultSkillName},
	)

	if !strings.Contains(summary, "File-list input supplies path context only") {
		t.Fatalf("summary = %q, want file-list caveat", summary)
	}
}

func TestFileListReviewUsesSelectedRepositoryWorkspace(t *testing.T) {
	targetRepo := t.TempDir()
	listDir := t.TempDir()
	fileList := filepath.Join(listDir, "files.txt")
	if err := os.WriteFile(fileList, []byte("pkg/a.go\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(file list) error = %v", err)
	}
	outDir := t.TempDir()
	var plannedWorkDir string
	result, err := Run(context.Background(), Options{
		FileList: fileList,
		RepoPath: targetRepo,
		OutDir:   outDir,
		DBPath:   filepath.Join(outDir, "review_agent.db"),
		Runtime:  "fake",
		Now:      fixedTestTime(),
		Planner: plannerFunc(func(ctx context.Context, req PlanRequest) (review.ReviewPlan, error) {
			plannedWorkDir = req.WorkDir
			return review.ReviewPlan{Model: "test", Provider: "test", Source: "test", Skill: defaultSkillName, Runtime: "fake"}, nil
		}),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantRepo, err := filepath.Abs(targetRepo)
	if err != nil {
		t.Fatalf("Abs(target repo) error = %v", err)
	}
	if plannedWorkDir != wantRepo {
		t.Fatalf("planner WorkDir = %q, want %q", plannedWorkDir, wantRepo)
	}
	if result.Report.Task.RepoPath != wantRepo {
		t.Fatalf("task RepoPath = %q, want %q", result.Report.Task.RepoPath, wantRepo)
	}
	if !strings.Contains(result.Report.Summary, wantRepo) {
		t.Fatalf("report summary = %q, want repository path", result.Report.Summary)
	}
	workspace := newSandboxWorkspace(plannedWorkDir)
	if got := workspace.runtimeCwd("container"); got != "work" {
		t.Fatalf("container CWD = %q, want work", got)
	}
}

func TestStandaloneFileListSkipsSandboxValidation(t *testing.T) {
	listDir := t.TempDir()
	fileList := filepath.Join(listDir, "files.txt")
	if err := os.WriteFile(fileList, []byte("pkg/a.go\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(file list) error = %v", err)
	}
	outDir := t.TempDir()
	result, err := Run(context.Background(), Options{
		FileList: fileList,
		OutDir:   outDir,
		DBPath:   filepath.Join(outDir, "review_agent.db"),
		Runtime:  "fake",
		Now:      fixedTestTime(),
		Planner: plannerFunc(func(ctx context.Context, req PlanRequest) (review.ReviewPlan, error) {
			if req.WorkDir != "" {
				t.Fatalf("planner WorkDir = %q, want empty", req.WorkDir)
			}
			return review.ReviewPlan{Model: "test", Provider: "test", Source: "test", Skill: defaultSkillName, Runtime: "fake", Commands: []string{"go test ./..."}}, nil
		}),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Report.SandboxRuns) != 0 || len(result.Report.PermissionDecisions) != 0 {
		t.Fatalf("standalone file-list executed sandbox: runs=%#v decisions=%#v", result.Report.SandboxRuns, result.Report.PermissionDecisions)
	}
	if result.Report.Conclusion != "no_sandbox_run" {
		t.Fatalf("conclusion = %q, want no_sandbox_run", result.Report.Conclusion)
	}
}

func TestStandaloneDiffFileCanUseSelectedRepositoryWorkspace(t *testing.T) {
	diffPath := filepath.Join(t.TempDir(), "change.diff")
	if err := os.WriteFile(diffPath, []byte("diff --git a/pkg/a.go b/pkg/a.go\n--- a/pkg/a.go\n+++ b/pkg/a.go\n@@ -1 +1 @@\n-package pkg\n+package pkg\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(diff) error = %v", err)
	}
	repo := t.TempDir()
	outDir := t.TempDir()
	var plannedWorkDir string
	result, err := Run(context.Background(), Options{
		DiffFile: diffPath,
		RepoPath: repo,
		OutDir:   outDir,
		DBPath:   filepath.Join(outDir, "review_agent.db"),
		Runtime:  "fake",
		Now:      fixedTestTime(),
		Planner: plannerFunc(func(ctx context.Context, req PlanRequest) (review.ReviewPlan, error) {
			plannedWorkDir = req.WorkDir
			return review.ReviewPlan{Model: "test", Provider: "test", Source: "test", Skill: defaultSkillName, Runtime: "fake", Commands: []string{"go test ./..."}}, nil
		}),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantRepo, err := filepath.Abs(repo)
	if err != nil {
		t.Fatalf("Abs(repo) error = %v", err)
	}
	if plannedWorkDir != wantRepo || result.Report.Task.RepoPath != wantRepo {
		t.Fatalf("workspace = %q/%q, want %q/%q", plannedWorkDir, result.Report.Task.RepoPath, wantRepo, wantRepo)
	}
	if len(result.Report.SandboxRuns) != 1 {
		t.Fatalf("sandbox runs = %d, want one for associated workspace", len(result.Report.SandboxRuns))
	}
}

func TestBuildReviewSnapshotExcludesGitIgnoredAndEnvironmentFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	runGitCommand(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored.txt\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.gitignore) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.go"), []byte("package tracked\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(tracked) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("TOKEN=local-secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.env) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "ignored.txt"), []byte("ignored\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(ignored) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "review_agent.db"), []byte("local store\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(store) error = %v", err)
	}
	runGitCommand(t, repo, "add", ".gitignore", "tracked.go", ".env")
	snapshot, cleanup, err := buildReviewSnapshot(context.Background(), repo)
	if err != nil {
		t.Fatalf("buildReviewSnapshot() error = %v", err)
	}
	defer cleanup()
	if _, err := os.Stat(filepath.Join(snapshot, "tracked.go")); err != nil {
		t.Fatalf("tracked.go missing from snapshot: %v", err)
	}
	for _, excluded := range []string{".git", ".env", "ignored.txt", "review_agent.db"} {
		if _, err := os.Stat(filepath.Join(snapshot, excluded)); !os.IsNotExist(err) {
			t.Fatalf("excluded %s present in snapshot, stat err=%v", excluded, err)
		}
	}
	fs := &recordingStageFS{}
	stagedCleanup, err := stageReviewWorkspace(context.Background(), fs, codeexecutor.Workspace{Path: "/work"}, "e2b", repo)
	if err != nil {
		t.Fatalf("stageReviewWorkspace() error = %v", err)
	}
	defer stagedCleanup()
	if fs.src == repo || fs.src == "" {
		t.Fatalf("E2B staged source = %q, want filtered snapshot", fs.src)
	}
	if _, err := os.Stat(filepath.Join(fs.src, "tracked.go")); err != nil {
		t.Fatalf("staged snapshot missing tracked.go: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fs.src, ".env")); !os.IsNotExist(err) {
		t.Fatalf("staged snapshot contains .env, stat err=%v", err)
	}
}

type recordingStageFS struct {
	src string
}

func (f *recordingStageFS) PutFiles(context.Context, codeexecutor.Workspace, []codeexecutor.PutFile) error {
	return nil
}

func (f *recordingStageFS) StageDirectory(_ context.Context, _ codeexecutor.Workspace, src string, _ string, _ codeexecutor.StageOptions) error {
	f.src = src
	return nil
}

func (f *recordingStageFS) Collect(context.Context, codeexecutor.Workspace, []string) ([]codeexecutor.File, error) {
	return nil, nil
}

func (f *recordingStageFS) StageInputs(context.Context, codeexecutor.Workspace, []codeexecutor.InputSpec) error {
	return nil
}

func (f *recordingStageFS) CollectOutputs(context.Context, codeexecutor.Workspace, codeexecutor.OutputSpec) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}

var _ codeexecutor.WorkspaceFS = (*recordingStageFS)(nil)

func runGitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}

type cancelingRuntime struct {
	cancel context.CancelFunc
}

func (r cancelingRuntime) Name() string { return "fake" }

func (r cancelingRuntime) Run(ctx context.Context, _ string) (sandboxrun.Result, error) {
	r.cancel()
	<-ctx.Done()
	return sandboxrun.Result{}, ctx.Err()
}

func TestCanceledSandboxRunIsPersistedWithFailedTask(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "review_agent.db")
	st, err := store.NewSQLite(context.Background(), path)
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	task := review.ReviewTask{ID: "task-canceled", Status: review.TaskStatusRunning}
	if err := st.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	_, runs, err := executePlannedCommandsWithFactory(
		ctx,
		st,
		task.ID,
		"fake",
		false,
		[]string{"go test ./..."},
		fixedTestTime(),
		time.Second,
		"",
		func(context.Context, string, string, string, time.Duration, string, bool) (sandboxrun.Runtime, func(), *review.SandboxRun) {
			return cancelingRuntime{cancel: cancel}, nil, nil
		},
	)
	if err != nil {
		t.Fatalf("executePlannedCommandsWithFactory() error = %v", err)
	}
	if len(runs) != 1 || runs[0].ErrorType != sandboxrun.ErrorCanceled {
		t.Fatalf("runs = %#v, want one canceled run", runs)
	}
	if err := recordSandboxRuns(ctx, st, runs); err != nil {
		t.Fatalf("recordSandboxRuns() error = %v", err)
	}
	finishCtx, finishCancel := failedTaskContext(ctx)
	if err := st.FinishTask(finishCtx, task.ID, review.TaskStatusFailed, context.Canceled.Error(), fixedTestTime()); err != nil {
		t.Fatalf("FinishTask() error = %v", err)
	}
	finishCancel()
	if err := st.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := store.NewSQLite(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen NewSQLite() error = %v", err)
	}
	defer reopened.Close()
	loaded, err := reopened.LoadTaskReport(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("LoadTaskReport() error = %v", err)
	}
	if loaded.Task.Status != review.TaskStatusFailed {
		t.Fatalf("loaded status = %q, want failed", loaded.Task.Status)
	}
	if len(loaded.SandboxRuns) != 1 || loaded.SandboxRuns[0].ErrorType != sandboxrun.ErrorCanceled {
		t.Fatalf("loaded sandbox runs = %#v, want canceled run", loaded.SandboxRuns)
	}
}

func TestRunKeepsTaskArtifactsUniqueAcrossRuns(t *testing.T) {
	outDir := t.TempDir()
	dbPath := filepath.Join(outDir, "review_agent.db")
	first, err := Run(context.Background(), Options{
		FixtureDir: filepath.Join("..", "..", "testdata", "fixtures"),
		OutDir:     outDir,
		DBPath:     dbPath,
		Runtime:    "fake",
		Now:        time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	second, err := Run(context.Background(), Options{
		FixtureDir: filepath.Join("..", "..", "testdata", "fixtures"),
		OutDir:     outDir,
		DBPath:     dbPath,
		Runtime:    "fake",
		Now:        time.Date(2026, 7, 21, 0, 0, 1, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if first.JSONPath == second.JSONPath || first.MarkdownPath == second.MarkdownPath {
		t.Fatalf("artifact paths were reused: first=%q/%q second=%q/%q", first.JSONPath, first.MarkdownPath, second.JSONPath, second.MarkdownPath)
	}
	reopened, err := store.NewSQLite(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("reopen NewSQLite() error = %v", err)
	}
	defer reopened.Close()
	for _, result := range []Result{first, second} {
		loaded, err := reopened.LoadTaskReport(context.Background(), result.TaskID)
		if err != nil {
			t.Fatalf("LoadTaskReport(%s) error = %v", result.TaskID, err)
		}
		if len(loaded.Artifacts) != 2 {
			t.Fatalf("task %s artifacts = %d, want 2", result.TaskID, len(loaded.Artifacts))
		}
		for _, artifact := range loaded.Artifacts {
			if !strings.Contains(filepath.Base(artifact.Path), result.TaskID) {
				t.Fatalf("task %s artifact path = %q, want task ID", result.TaskID, artifact.Path)
			}
			raw, err := os.ReadFile(artifact.Path)
			if err != nil {
				t.Fatalf("ReadFile(%s) error = %v", artifact.Path, err)
			}
			sum := sha256.Sum256(raw)
			if got := hex.EncodeToString(sum[:]); got != artifact.SHA256 {
				t.Fatalf("task %s artifact %s checksum = %q, want %q", result.TaskID, artifact.Path, got, artifact.SHA256)
			}
		}
	}
}

func TestContainerHostConfigDisablesNetworkEgress(t *testing.T) {
	cfg := containerHostConfig()
	if cfg.NetworkMode != "none" {
		t.Fatalf("NetworkMode = %q, want none", cfg.NetworkMode)
	}
	if !cfg.AutoRemove {
		t.Fatal("AutoRemove = false, want true")
	}
	if cfg.Privileged {
		t.Fatal("Privileged = true, want false")
	}
	if cfg.Resources.Memory != int64(512<<20) {
		t.Fatalf("Memory = %d, want %d", cfg.Resources.Memory, int64(512<<20))
	}
	if cfg.Resources.NanoCPUs != containerCPULimit {
		t.Fatalf("NanoCPUs = %d, want %d", cfg.Resources.NanoCPUs, containerCPULimit)
	}
	if cfg.Resources.PidsLimit == nil || *cfg.Resources.PidsLimit != containerPIDsLimit {
		t.Fatalf("PidsLimit = %v, want %d", cfg.Resources.PidsLimit, containerPIDsLimit)
	}
	if got := cfg.StorageOpt["size"]; got != containerStorageLimit {
		t.Fatalf("StorageOpt[size] = %q, want %q", got, containerStorageLimit)
	}
}

func TestContainerConfigUsesGo124Image(t *testing.T) {
	cfg := containerConfig()
	if cfg.Image != "golang:1.24" {
		t.Fatalf("Image = %q, want golang:1.24", cfg.Image)
	}
	if cfg.WorkingDir != "/" {
		t.Fatalf("WorkingDir = %q, want /", cfg.WorkingDir)
	}
}

func TestContainerBindMountsDoNotExposeHostModuleCache(t *testing.T) {
	t.Setenv("GOMODCACHE", "/host/go/pkg/mod")
	t.Setenv("GOCACHE", "/host/go-build")

	got := containerBindMounts("/repo")
	want := []bindMount{
		{HostPath: "/repo", ContainerPath: "/workspace", Mode: "ro"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("containerBindMounts() = %#v, want %#v", got, want)
	}
	for _, mount := range got {
		if mount.ContainerPath == containerGoModCache {
			t.Fatalf("containerBindMounts() exposed module cache: %#v", mount)
		}
	}
}

func TestHasExactModuleDeclRejectsNestedModulePrefixes(t *testing.T) {
	if !hasExactModuleDecl("module trpc.group/trpc-go/trpc-agent-go\n\ngo 1.21\n", rootModuleDecl) {
		t.Fatal("root module declaration was not matched")
	}
	for _, raw := range []string{
		"module trpc.group/trpc-go/trpc-agent-go/examples\n\ngo 1.24.4\n",
		"module trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent\n\ngo 1.23.0\n",
	} {
		if hasExactModuleDecl(raw, rootModuleDecl) {
			t.Fatalf("nested module was incorrectly matched:\n%s", raw)
		}
	}
}

func assertStoredTask(t *testing.T, dbPath string, startedAt time.Time, assert func(report store.TaskReport)) {
	t.Helper()
	st, err := store.NewSQLite(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	defer st.Close()
	input, err := inputsource.Read(context.Background(), inputsource.Options{
		FixtureDir: filepath.Join("..", "..", "testdata", "fixtures"),
	})
	if err != nil {
		t.Fatalf("inputsource.Read() error = %v", err)
	}
	report, err := st.LoadTaskReport(context.Background(), runTaskID(input.Diff, startedAt))
	if err != nil {
		t.Fatalf("LoadTaskReport() error = %v", err)
	}
	assert(report)
}

func fixedTestTime() time.Time {
	return time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)
}
