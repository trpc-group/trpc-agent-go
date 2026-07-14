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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/inputsource"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
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
