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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/inputsource"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandboxrun"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

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
	assertFailedTaskStored(t, dbPath)
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

func TestRunChecksAndSkipsBlockedModelPlannedCommand(t *testing.T) {
	outDir := t.TempDir()
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"commands\":[\"curl https://example.com\"],\"rule_sources\":[\"skills/code-review/SKILL.md\"]}"}}]}`))
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
	if len(result.Report.PermissionDecisions) != 1 {
		t.Fatalf("permission decisions = %d, want 1", len(result.Report.PermissionDecisions))
	}
	if !result.Report.PermissionDecisions[0].Blocked {
		t.Fatal("planned curl command was not blocked")
	}
	if len(result.Report.SandboxRuns) != 1 {
		t.Fatalf("sandbox runs = %d, want 1", len(result.Report.SandboxRuns))
	}
	if result.Report.SandboxRuns[0].Status != sandboxrun.StatusSkipped {
		t.Fatalf("sandbox run status = %q, want skipped", result.Report.SandboxRuns[0].Status)
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
	if got := workspaceRuntimeCwd("container"); got != "work/examples/code_review_agent" {
		t.Fatalf("container cwd = %q, want work/examples/code_review_agent", got)
	}
	if got := workspaceRuntimeCwd("e2b"); got != "work/examples/code_review_agent" {
		t.Fatalf("e2b cwd = %q, want work/examples/code_review_agent", got)
	}
	if got := workspaceRuntimeCwd("local"); got != "examples/code_review_agent" {
		t.Fatalf("local cwd = %q, want examples/code_review_agent", got)
	}
}

func TestContainerHostConfigAllowsDependencyDownloads(t *testing.T) {
	cfg := containerHostConfig()
	if cfg.NetworkMode != "bridge" {
		t.Fatalf("NetworkMode = %q, want bridge", cfg.NetworkMode)
	}
	if !cfg.AutoRemove {
		t.Fatal("AutoRemove = false, want true")
	}
	if cfg.Privileged {
		t.Fatal("Privileged = true, want false")
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

func assertFailedTaskStored(t *testing.T, dbPath string) {
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
	report, err := st.LoadTaskReport(context.Background(), stableTaskID(input.Diff, fixedTestTime()))
	if err != nil {
		t.Fatalf("LoadTaskReport() error = %v", err)
	}
	if report.Task.Status != review.TaskStatusFailed {
		t.Fatalf("stored task status = %q, want failed", report.Task.Status)
	}
	if !strings.Contains(report.Task.Error, "model orchestration requires --model or MODEL") {
		t.Fatalf("stored task error = %q, want missing model message", report.Task.Error)
	}
}

func fixedTestTime() time.Time {
	return time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)
}
