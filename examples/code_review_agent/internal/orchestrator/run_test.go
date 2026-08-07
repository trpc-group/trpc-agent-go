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
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/inputsource"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
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
		FixtureDir:        filepath.Join("..", "..", "testdata", "fixtures"),
		OutDir:            outDir,
		DBPath:            dbPath,
		Runtime:           "local",
		AllowTrustedLocal: true,
		Now:               fixedTestTime(),
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
		if len(body.Messages) < 2 {
			t.Fatalf("model planning request missing user message: %#v", body.Messages)
		}
		if strings.Contains(body.Messages[1].Content, "supersecretvalue") {
			t.Fatalf("model planning request leaked secret-bearing path: %s", body.Messages[1].Content)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"commands\":[\"go test ./...\"],\"rule_sources\":[\"skills/code-review/SKILL.md\",\"skills/code-review/docs/rules.md\"]}"}}]}`))
	}))
	defer modelServer.Close()
	planner := EnvPlanner{APIKey: "test-key", BaseURL: modelServer.URL, HTTPClient: modelServer.Client()}
	result, err := Run(context.Background(), Options{
		FixtureDir: filepath.Join("..", "..", "testdata", "fixtures"),
		OutDir:     outDir,
		DBPath:     filepath.Join(outDir, "review_agent.db"),
		Model:      "gpt-review",
		Runtime:    "fake",
		Now:        fixedTestTime(),
		Planner: plannerFunc(func(ctx context.Context, req PlanRequest) (review.ReviewPlan, error) {
			req.Runtime = "container"
			return planner.PlanReview(ctx, req)
		}),
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
	if !strings.Contains(string(raw), "## Model Plan") || !strings.Contains(string(raw), "- model: `gpt-review`") {
		t.Fatalf("markdown report does not contain configured model plan:\n%s", raw)
	}
}

func TestPlanReviewBoundsChangedFilePayload(t *testing.T) {
	type observedRequest struct {
		body       chatCompletionRequest
		contentLen int64
		err        error
	}
	observed := make(chan observedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body chatCompletionRequest
		err := json.NewDecoder(r.Body).Decode(&body)
		observed <- observedRequest{body: body, contentLen: r.ContentLength, err: err}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"commands\":[\"go test ./...\"]}"}}]}`))
	}))
	defer server.Close()

	files := make([]review.DiffFile, maxModelPlanningFiles+10)
	for index := range files {
		files[index].NewPath = strings.Repeat("p", 512) + ".go"
	}
	_, err := (EnvPlanner{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}).PlanReview(context.Background(), PlanRequest{Model: "gpt-review", Runtime: "container", Files: files})
	if err != nil {
		t.Fatalf("PlanReview() error = %v", err)
	}
	request := <-observed
	if request.err != nil {
		t.Fatalf("decode model request: %v", request.err)
	}
	if request.contentLen <= 0 || request.contentLen > maxModelPlanningRequestBytes {
		t.Fatalf("model request content length = %d, want 0 < length <= %d", request.contentLen, maxModelPlanningRequestBytes)
	}
	if len(request.body.Messages) < 2 {
		t.Fatalf("model planning request missing user message: %#v", request.body.Messages)
	}
	var prompt struct {
		ChangedFiles          []string `json:"changed_files"`
		ChangedFileCount      int      `json:"changed_file_count"`
		ChangedFilesTruncated bool     `json:"changed_files_truncated"`
	}
	if err := json.Unmarshal([]byte(request.body.Messages[1].Content), &prompt); err != nil {
		t.Fatalf("decode bounded planning prompt: %v", err)
	}
	if prompt.ChangedFileCount != len(files) {
		t.Fatalf("changed file count = %d, want %d", prompt.ChangedFileCount, len(files))
	}
	if len(prompt.ChangedFiles) > maxModelPlanningFiles {
		t.Fatalf("changed file sample count = %d, want <= %d", len(prompt.ChangedFiles), maxModelPlanningFiles)
	}
	encodedFiles, err := json.Marshal(prompt.ChangedFiles)
	if err != nil {
		t.Fatalf("encode changed file sample: %v", err)
	}
	if len(encodedFiles) > maxModelPlanningFileBytes {
		t.Fatalf("encoded changed file sample = %d, want <= %d", len(encodedFiles), maxModelPlanningFileBytes)
	}
	if !prompt.ChangedFilesTruncated {
		t.Fatal("changed_files_truncated = false, want true")
	}
}

func TestPlanReviewPreservesModelPlannedCommandsForAudit(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode model request: %v", err)
		}
		if len(body.Messages) < 2 || strings.Contains(body.Messages[1].Content, "supersecretvalue") {
			t.Fatalf("model planning request leaked secret-bearing path: %#v", body.Messages)
		}
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
		Files:   []review.DiffFile{{NewPath: "token=supersecretvalue.go"}},
	})
	if err != nil {
		t.Fatalf("PlanReview() error = %v", err)
	}
	want := []string{"go test ./...", "go env", "curl https://example.com", "go vet ./..."}
	if !reflect.DeepEqual(plan.Commands, want) {
		t.Fatalf("plan commands = %#v, want %#v", plan.Commands, want)
	}
}

func TestPlanReviewRejectsUnboundedModelCommands(t *testing.T) {
	tests := []struct {
		name     string
		commands []string
		wantErr  string
	}{
		{
			name:     "command count",
			commands: make([]string, maxModelPlanCommands+1),
			wantErr:  "model command count exceeded",
		},
		{
			name:     "command length",
			commands: []string{strings.Repeat("x", maxModelCommandBytes+1)},
			wantErr:  "model command exceeded",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for index := range test.commands {
				if test.commands[index] == "" {
					test.commands[index] = "go test ./... " + strings.Repeat("x", index)
				}
			}
			content, err := json.Marshal(map[string]any{"commands": test.commands})
			if err != nil {
				t.Fatalf("Marshal(model content) error = %v", err)
			}
			response, err := json.Marshal(map[string]any{
				"choices": []any{map[string]any{
					"message": map[string]string{"content": string(content)},
				}},
			})
			if err != nil {
				t.Fatalf("Marshal(model response) error = %v", err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(response)
			}))
			defer server.Close()
			_, err = (EnvPlanner{
				APIKey:     "test-key",
				BaseURL:    server.URL,
				HTTPClient: server.Client(),
			}).PlanReview(context.Background(), PlanRequest{Model: "gpt-review", Runtime: "container"})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("PlanReview() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestExecutePlannedCommandsAuditsRejectedModelCommands(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "review_agent.db")
	st, err := store.NewSQLite(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	defer st.Close()

	decisions, runs, err := executePlannedCommandsWithFactory(
		context.Background(),
		st,
		"task-audit",
		"fake",
		false,
		false,
		[]string{"curl https://example.com", "go test ./..."},
		fixedTestTime(),
		time.Second,
		"",
		func(context.Context, string, string, string, time.Duration, string, bool, bool) (sandboxrun.Runtime, func(), *review.SandboxRun) {
			return sandboxrun.FakeRuntime{}, nil, nil
		},
	)
	if err != nil {
		t.Fatalf("executePlannedCommandsWithFactory() error = %v", err)
	}
	if len(decisions) != 2 || len(runs) != 2 {
		t.Fatalf("decisions/runs = %d/%d, want 2/2", len(decisions), len(runs))
	}
	if !decisions[0].Blocked || decisions[0].RuleID != "sandbox.command_not_allowlisted" {
		t.Fatalf("first decision = %#v, want allowlist rejection", decisions[0])
	}
	if runs[0].Status != sandboxrun.StatusSkipped || runs[0].Command != "curl https://example.com" {
		t.Fatalf("first run = %#v, want skipped rejected command", runs[0])
	}
	if decisions[1].Blocked || runs[1].Status != sandboxrun.StatusPassed {
		t.Fatalf("allowed command decision/run = %#v/%#v, want executed pass", decisions[1], runs[1])
	}
}

func TestExecutePlannedCommandsInitializesFailedRuntimeOnce(t *testing.T) {
	st, err := store.NewSQLite(context.Background(), filepath.Join(t.TempDir(), "review_agent.db"))
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	defer st.Close()
	attempts := 0
	_, runs, err := executePlannedCommandsWithFactory(
		context.Background(), st, "task-init-once", "container", false, false,
		[]string{"go test ./...", "go vet ./..."}, fixedTestTime(), time.Second, "",
		func(_ context.Context, runtimeName string, taskID string, suffix string, _ time.Duration, _ string, _ bool, _ bool) (sandboxrun.Runtime, func(), *review.SandboxRun) {
			attempts++
			return nil, nil, &review.SandboxRun{
				ID:        taskID + "-sandbox-init-" + suffix,
				TaskID:    taskID,
				Runtime:   runtimeName,
				Status:    sandboxrun.StatusUnavailable,
				ErrorType: sandboxrun.ErrorRuntimeUnavailable,
			}
		},
	)
	if err != nil {
		t.Fatalf("executePlannedCommandsWithFactory() error = %v", err)
	}
	if attempts != 1 {
		t.Fatalf("runtime initialization attempts = %d, want 1", attempts)
	}
	if len(runs) != 3 || runs[0].Status != sandboxrun.StatusUnavailable || runs[1].Status != sandboxrun.StatusUnavailable || runs[2].Status != sandboxrun.StatusUnavailable {
		t.Fatalf("runs = %#v, want one initialization record and two unavailable commands", runs)
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
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
		"GOFLAGS":     "",
	}
	for key, value := range want {
		if env[key] != value {
			t.Fatalf("%s = %q, want %q", key, env[key], value)
		}
	}
	if _, ok := env["CGO_ENABLED"]; ok {
		t.Fatalf("container env leaked CGO_ENABLED: %#v", env)
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
		"GOTOOLCHAIN": "local",
	}
	for key, value := range want {
		if env[key] != value {
			t.Fatalf("%s = %q, want %q", key, env[key], value)
		}
	}
}

func TestValidateContainerToolchainRejectsNewerModule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	if err := validateContainerToolchain(root, ""); err == nil || !strings.Contains(err.Error(), "unsupported-toolchain") {
		t.Fatalf("validateContainerToolchain() error = %v, want unsupported-toolchain rejection", err)
	}
}

func TestValidateContainerToolchainAcceptsSupportedModule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test\n\ngo 1.13\ntoolchain go1.25.7\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	if err := validateContainerToolchain(root, ""); err != nil {
		t.Fatalf("validateContainerToolchain() error = %v, want supported module accepted", err)
	}
}

func TestValidateContainerToolchainRejectsWorkspaceRequirement(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "module")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatalf("MkdirAll(module) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module root.example\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(root go.mod) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module module.example\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(module go.mod) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.24\n\nuse ./module\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.work) error = %v", err)
	}
	if err := validateContainerToolchain(root, ""); err == nil || !strings.Contains(err.Error(), "unsupported-toolchain") {
		t.Fatalf("validateContainerToolchain() error = %v, want workspace toolchain rejection", err)
	}
}

func TestValidateContainerToolchainParsesQuotedWorkspaceModule(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "module with spaces")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatalf("MkdirAll(module) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module root.example\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(root go.mod) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module module.example\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(module go.mod) error = %v", err)
	}
	work := "go 1.24\n\nuse (\n\t\"./module with spaces\"\n)\n"
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte(work), 0o600); err != nil {
		t.Fatalf("WriteFile(go.work) error = %v", err)
	}
	if err := validateContainerToolchain(root, ""); err == nil || !strings.Contains(err.Error(), "unsupported-toolchain") {
		t.Fatalf("validateContainerToolchain() error = %v, want quoted workspace module rejection", err)
	}
}

func TestWorkspaceUsePathsSupportsRawStringsAndWhitespace(t *testing.T) {
	raw := "use\t( // block comment\n\t`./module with spaces` // path comment\n) // block comment\nuse   \"./quoted\"\n"
	want := []string{"./module with spaces", "./quoted"}
	if got := workspaceUsePaths(raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("workspaceUsePaths() = %#v, want %#v", got, want)
	}
}

func TestValidateContainerToolchainUsesNearestWorkspace(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "module")
	higher := filepath.Join(root, "higher")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatalf("MkdirAll(module) error = %v", err)
	}
	if err := os.MkdirAll(higher, 0o700); err != nil {
		t.Fatalf("MkdirAll(higher) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module module.example\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(module go.mod) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(higher, "go.mod"), []byte("module higher.example\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(higher go.mod) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.24\n\nuse ./higher\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(root go.work) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(module, "go.work"), []byte("go 1.24\n\nuse .\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(module go.work) error = %v", err)
	}
	if err := validateContainerToolchain(root, "module"); err != nil {
		t.Fatalf("validateContainerToolchain() error = %v, want nearest workspace accepted", err)
	}
}

func TestValidateContainerToolchainRejectsOversizedMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(strings.Repeat("x", int(maxGoMetadataBytes+1))), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	if err := validateContainerToolchain(root, ""); err == nil || !strings.Contains(err.Error(), "file exceeds") {
		t.Fatalf("validateContainerToolchain() error = %v, want bounded metadata rejection", err)
	}
}

func TestGoVersionPreservesPatchAndRejectsUnpinnedImageTag(t *testing.T) {
	required, err := parseGoVersion("1.24.7")
	if err != nil {
		t.Fatalf("parseGoVersion(required) error = %v", err)
	}
	image, err := parseGoVersion("1.24")
	if err != nil {
		t.Fatalf("parseGoVersion(image) error = %v", err)
	}
	if required.String() != "1.24.7" || image.String() != "1.24" {
		t.Fatalf("versions = %q/%q, want patch-preserving required and minor-only image", required, image)
	}
	if !required.exceeds(image) {
		t.Fatal("explicit patch requirement should fail closed for a minor-only image tag")
	}
	baseline, err := parseGoVersion("1.24")
	if err != nil {
		t.Fatalf("parseGoVersion(baseline) error = %v", err)
	}
	if baseline.exceeds(image) {
		t.Fatal("minor-only requirement should match the same minor image tag")
	}
	pinned, err := parseGoVersion("1.24.6")
	if err != nil {
		t.Fatalf("parseGoVersion(pinned) error = %v", err)
	}
	if !required.exceeds(pinned) {
		t.Fatal("1.24.7 should exceed a pinned 1.24.6 image")
	}
	if !required.after(image) {
		t.Fatal("1.24.7 should remain the greater required version for aggregation")
	}
}

func TestWorkspaceRuntimeEnvUsesVendorMode(t *testing.T) {
	for _, runtimeName := range []string{"container", "e2b"} {
		env := workspaceRuntimeEnvForDependencyMode(runtimeName, dependencyModeVendor)
		if env["GOFLAGS"] != "-mod=vendor" {
			t.Fatalf("%s GOFLAGS = %q, want -mod=vendor", runtimeName, env["GOFLAGS"])
		}
	}
}

func TestDependencyModeDetectsLegacyVendorModule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test\n\ngo 1.13\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "vendor"), 0o700); err != nil {
		t.Fatalf("MkdirAll(vendor) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "vendor", "modules.txt"), []byte("# example.test/dep v1.0.0\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(vendor/modules.txt) error = %v", err)
	}
	mode, err := dependencyModeForModule(root, filepath.Join(root, ".gomodcache"))
	if err != nil {
		t.Fatalf("dependencyModeForModule() error = %v", err)
	}
	if mode != dependencyModeVendor {
		t.Fatalf("dependency mode = %q, want vendor", mode)
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

func TestValidateContainerRuntimePolicyFailsClosed(t *testing.T) {
	if err := validateContainerRuntimePolicy("container"); err == nil {
		t.Fatal("validateContainerRuntimePolicy(container) error = nil, want fail-closed rejection")
	} else if !strings.Contains(err.Error(), "context-aware constructor") {
		t.Fatalf("validateContainerRuntimePolicy() error = %q, want upstream constructor guidance", err)
	}
	if err := validateContainerRuntimePolicy("fake"); err != nil {
		t.Fatalf("validateContainerRuntimePolicy(fake) error = %v, want nil", err)
	}
}

func TestRunFailsClosedForContainerInitialization(t *testing.T) {
	outDir := t.TempDir()
	calledPlanner := false
	_, err := Run(context.Background(), Options{
		FixtureDir: filepath.Join("..", "..", "testdata", "fixtures"),
		OutDir:     outDir,
		DBPath:     filepath.Join(outDir, "review_agent.db"),
		Runtime:    "container",
		Now:        fixedTestTime(),
		Planner: plannerFunc(func(ctx context.Context, req PlanRequest) (review.ReviewPlan, error) {
			calledPlanner = true
			return review.ReviewPlan{}, errors.New("planner should not be called")
		}),
	})
	if err == nil {
		t.Fatal("Run() error = nil, want container fail-closed rejection")
	}
	if !strings.Contains(err.Error(), "fail-closed") {
		t.Fatalf("Run() error = %q, want fail-closed message", err)
	}
	if calledPlanner {
		t.Fatal("planner was called before container policy rejection")
	}
	assertStoredTask(t, filepath.Join(outDir, "review_agent.db"), fixedTestTime(), func(report store.TaskReport) {
		if report.Task.Status != review.TaskStatusFailed {
			t.Fatalf("stored task status = %q, want failed", report.Task.Status)
		}
		if !strings.Contains(report.Task.Error, "fail-closed") {
			t.Fatalf("stored task error = %q, want fail-closed message", report.Task.Error)
		}
	})
}

func TestValidateRemoteRuntimePolicyRejectsUntrustedE2B(t *testing.T) {
	if err := validateRemoteRuntimePolicy("e2b", false); err == nil {
		t.Fatal("validateRemoteRuntimePolicy(e2b, false) error = nil, want rejection")
	} else if !strings.Contains(err.Error(), "--allow-trusted-remote") {
		t.Fatalf("validateRemoteRuntimePolicy() error = %q, want trusted remote guidance", err)
	}
	if err := validateRemoteRuntimePolicy("E2B", true); err != nil {
		t.Fatalf("validateRemoteRuntimePolicy(E2B, true) error = %v, want nil", err)
	}
	if err := validateRemoteRuntimePolicy("container", false); err != nil {
		t.Fatalf("validateRemoteRuntimePolicy(container, false) error = %v, want nil", err)
	}
}

func TestRunRejectsE2BWithoutTrustedRemoteOptIn(t *testing.T) {
	outDir := t.TempDir()
	calledPlanner := false
	_, err := Run(context.Background(), Options{
		FixtureDir: filepath.Join("..", "..", "testdata", "fixtures"),
		OutDir:     outDir,
		DBPath:     filepath.Join(outDir, "review_agent.db"),
		Runtime:    "e2b",
		Now:        fixedTestTime(),
		Planner: plannerFunc(func(ctx context.Context, req PlanRequest) (review.ReviewPlan, error) {
			calledPlanner = true
			return review.ReviewPlan{}, errors.New("planner should not be called")
		}),
	})
	if err == nil {
		t.Fatal("Run() error = nil, want E2B egress policy rejection")
	}
	if !strings.Contains(err.Error(), "--allow-trusted-remote") {
		t.Fatalf("Run() error = %q, want trusted remote guidance", err)
	}
	if calledPlanner {
		t.Fatal("planner was called before E2B policy rejection")
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

func TestRunPersistsDeterministicFindingsBeforePlannerFailure(t *testing.T) {
	outDir := t.TempDir()
	dbPath := filepath.Join(outDir, "review_agent.db")
	plannerErr := errors.New("planner unavailable")
	_, err := Run(context.Background(), Options{
		FixtureDir: filepath.Join("..", "..", "testdata", "fixtures"),
		OutDir:     outDir,
		DBPath:     dbPath,
		Runtime:    "fake",
		Now:        fixedTestTime(),
		Planner: plannerFunc(func(context.Context, PlanRequest) (review.ReviewPlan, error) {
			return review.ReviewPlan{}, plannerErr
		}),
	})
	if !errors.Is(err, plannerErr) {
		t.Fatalf("Run() error = %v, want planner error", err)
	}
	assertStoredTask(t, dbPath, fixedTestTime(), func(report store.TaskReport) {
		if report.Task.Status != review.TaskStatusFailed {
			t.Fatalf("stored task status = %q, want failed", report.Task.Status)
		}
		if len(report.Findings) == 0 {
			t.Fatal("stored findings = 0, want deterministic findings preserved after planner failure")
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

func TestStandaloneDiffFindingConclusionIsNotOverwrittenByMissingSandbox(t *testing.T) {
	diffPath := filepath.Join(t.TempDir(), "goroutine.diff")
	diff := "diff --git a/pkg/a.go b/pkg/a.go\n--- a/pkg/a.go\n+++ b/pkg/a.go\n@@ -1,0 +1,3 @@\n+package pkg\n+func f() {\n+\tgo func() { println(\"leak\") }()\n"
	if err := os.WriteFile(diffPath, []byte(diff), 0o600); err != nil {
		t.Fatalf("WriteFile(diff) error = %v", err)
	}
	outDir := t.TempDir()
	result, err := Run(context.Background(), Options{
		DiffFile: diffPath,
		OutDir:   outDir,
		DBPath:   filepath.Join(outDir, "review_agent.db"),
		Runtime:  "fake",
		Now:      fixedTestTime(),
		Planner: plannerFunc(func(ctx context.Context, req PlanRequest) (review.ReviewPlan, error) {
			return review.ReviewPlan{Model: "test", Provider: "test", Source: "test", Skill: defaultSkillName, Runtime: "fake", Commands: []string{"go test ./..."}}, nil
		}),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Report.Task.Status != review.TaskStatusFailed {
		t.Fatalf("status = %q, want failed", result.Report.Task.Status)
	}
	if result.Report.Conclusion != "needs_human_review" {
		t.Fatalf("conclusion = %q, want needs_human_review", result.Report.Conclusion)
	}
	if len(result.Report.SandboxRuns) != 0 {
		t.Fatalf("sandbox runs = %#v, want none for standalone diff", result.Report.SandboxRuns)
	}
}

func TestRunRedactsMultilinePEMInReportAndStore(t *testing.T) {
	diffPath := filepath.Join(t.TempDir(), "key.diff")
	diff := "diff --git a/pkg/key.go b/pkg/key.go\nnew file mode 100644\n--- /dev/null\n+++ b/pkg/key.go\n@@ -0,0 +1,3 @@\n+-----BEGIN PRIVATE KEY-----\n+MIIEvQIBADANBgkqhkiG9w0BAQEFAASC\n+-----END PRIVATE KEY-----\n"
	if err := os.WriteFile(diffPath, []byte(diff), 0o600); err != nil {
		t.Fatalf("WriteFile(diff) error = %v", err)
	}
	outDir := t.TempDir()
	dbPath := filepath.Join(outDir, "review_agent.db")
	result, err := Run(context.Background(), Options{
		DiffFile: diffPath,
		OutDir:   outDir,
		DBPath:   dbPath,
		Runtime:  "fake",
		Now:      fixedTestTime(),
		Planner: plannerFunc(func(ctx context.Context, req PlanRequest) (review.ReviewPlan, error) {
			return review.ReviewPlan{Model: "test", Provider: "test", Source: "test", Skill: defaultSkillName, Runtime: "fake", Commands: []string{"go test ./..."}}, nil
		}),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, path := range []string{result.JSONPath, dbPath} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		if strings.Contains(string(raw), "PRIVATE KEY") || strings.Contains(string(raw), "MIIEvQ") {
			t.Fatalf("%s leaked private key material:\n%s", path, raw)
		}
	}
}

func TestRunReturnsRedactedFindingsBeforeReportWriting(t *testing.T) {
	diffPath := filepath.Join(t.TempDir(), "secret-resource.diff")
	diff := "diff --git a/pkg/file.go b/pkg/file.go\n--- a/pkg/file.go\n+++ b/pkg/file.go\n@@ -1,0 +1,6 @@\n+package pkg\n+import \"os\"\n+func Load() error {\n+\tf, _ := os.Open(\"password=supersecretvalue\")\n+\treturn nil\n+}\n"
	if err := os.WriteFile(diffPath, []byte(diff), 0o600); err != nil {
		t.Fatalf("WriteFile(diff) error = %v", err)
	}
	outDir := t.TempDir()
	var plannedFiles []review.DiffFile
	result, err := Run(context.Background(), Options{
		DiffFile: diffPath,
		OutDir:   outDir,
		DBPath:   filepath.Join(outDir, "review_agent.db"),
		Runtime:  "fake",
		Now:      fixedTestTime(),
		Planner: plannerFunc(func(ctx context.Context, req PlanRequest) (review.ReviewPlan, error) {
			plannedFiles = req.Files
			return review.ReviewPlan{Model: "test", Provider: "test", Source: "test", Skill: defaultSkillName, Runtime: "fake"}, nil
		}),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for name, files := range map[string][]review.DiffFile{
		"planner request": plannedFiles,
		"caller report":   result.Report.ChangedFiles,
	} {
		raw, marshalErr := json.Marshal(files)
		if marshalErr != nil {
			t.Fatalf("Marshal(%s) error = %v", name, marshalErr)
		}
		if strings.Contains(string(raw), "supersecretvalue") {
			t.Fatalf("%s leaked raw diff secret: %s", name, raw)
		}
	}
	var sawResource bool
	for _, finding := range result.Report.Findings {
		raw, marshalErr := json.Marshal(finding)
		if marshalErr != nil {
			t.Fatalf("Marshal(finding) error = %v", marshalErr)
		}
		if strings.Contains(string(raw), "supersecretvalue") {
			t.Fatalf("Run() returned raw secret finding: %s", raw)
		}
		if finding.RuleID == "resource.close_missing" {
			sawResource = true
		}
	}
	if !sawResource {
		t.Fatalf("Run() findings = %#v, want resource.close_missing", result.Report.Findings)
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

func TestPlannerReceivesRedactedWorkspacePath(t *testing.T) {
	diffPath := filepath.Join(t.TempDir(), "change.diff")
	if err := os.WriteFile(diffPath, []byte("diff --git a/pkg/a.go b/pkg/a.go\n--- a/pkg/a.go\n+++ b/pkg/a.go\n@@ -1 +1 @@\n-package pkg\n+package pkg\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(diff) error = %v", err)
	}
	repo := filepath.Join(t.TempDir(), "password=supersecretvalue")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatalf("MkdirAll(repo) error = %v", err)
	}
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
	if result.Report.Task.RepoPath != wantRepo {
		t.Fatalf("task RepoPath = %q, want %q", result.Report.Task.RepoPath, wantRepo)
	}
	wantPlannerWorkDir := redact.Text(wantRepo).Text
	if plannedWorkDir != wantPlannerWorkDir {
		t.Fatalf("planner WorkDir = %q, want redacted %q", plannedWorkDir, wantPlannerWorkDir)
	}
	if strings.Contains(plannedWorkDir, "supersecretvalue") {
		t.Fatalf("planner WorkDir leaked secret: %q", plannedWorkDir)
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
	if err := os.WriteFile(filepath.Join(repo, "review_report_fixture.json"), []byte("tracked report fixture\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(tracked report) error = %v", err)
	}
	runGitCommand(t, repo, "add", ".gitignore", ".env", "tracked.go", "review_agent.db", "review_report_fixture.json")
	snapshot, cleanup, err := buildReviewSnapshot(context.Background(), repo)
	if err != nil {
		t.Fatalf("buildReviewSnapshot() error = %v", err)
	}
	defer cleanup()
	if _, err := os.Stat(filepath.Join(snapshot, "tracked.go")); err != nil {
		t.Fatalf("tracked.go missing from snapshot: %v", err)
	}
	for _, excluded := range []string{".git", ".env", "ignored.txt"} {
		if _, err := os.Stat(filepath.Join(snapshot, excluded)); !os.IsNotExist(err) {
			t.Fatalf("excluded %s present in snapshot, stat err=%v", excluded, err)
		}
	}
	for _, included := range []string{"review_agent.db", "review_report_fixture.json"} {
		if _, err := os.Stat(filepath.Join(snapshot, included)); err != nil {
			t.Fatalf("tracked %s missing from snapshot: %v", included, err)
		}
	}
	fs := &recordingStageFS{}
	stagedCleanup, err := stageReviewWorkspace(context.Background(), fs, codeexecutor.Workspace{Path: "/work"}, "e2b", repo, "", false)
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
	if _, err := os.Stat(filepath.Join(fs.src, "review_agent.db")); err != nil {
		t.Fatalf("staged snapshot missing tracked review_agent.db: %v", err)
	}
	containerFS := &recordingStageFS{}
	containerCleanup, err := stageReviewWorkspace(context.Background(), containerFS, codeexecutor.Workspace{Path: "/work"}, "container", repo, "", false)
	if err != nil {
		t.Fatalf("container stageReviewWorkspace() error = %v", err)
	}
	defer containerCleanup()
	if containerFS.src == repo || containerFS.src == "" {
		t.Fatalf("container staged source = %q, want filtered snapshot", containerFS.src)
	}
	if _, err := os.Stat(filepath.Join(containerFS.src, ".git")); !os.IsNotExist(err) {
		t.Fatalf("container staged snapshot contains .git, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(containerFS.src, ".env")); !os.IsNotExist(err) {
		t.Fatalf("container staged snapshot contains .env, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(containerFS.src, "review_report_fixture.json")); err != nil {
		t.Fatalf("container staged snapshot missing tracked report fixture: %v", err)
	}
}

func TestBuildReviewSnapshotExcludesConfiguredStoreAndOutputPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	runGitCommand(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "tracked.go"), []byte("package tracked\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(tracked.go) error = %v", err)
	}
	runGitCommand(t, repo, "add", "tracked.go")
	customDB := filepath.Join(repo, "review-state", "audit-store.data")
	customOut := filepath.Join(repo, "review-output")
	customReport := filepath.Join(customOut, "nested", "report.json")
	if err := os.MkdirAll(filepath.Dir(customDB), 0o700); err != nil {
		t.Fatalf("MkdirAll(custom db) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(customReport), 0o700); err != nil {
		t.Fatalf("MkdirAll(custom report) error = %v", err)
	}
	customStore, err := store.NewSQLite(context.Background(), customDB)
	if err != nil {
		t.Fatalf("NewSQLite(custom db) error = %v", err)
	}
	defer customStore.Close()
	if err := os.WriteFile(customReport, []byte("private report\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(custom report) error = %v", err)
	}
	if _, err := os.Stat(customDB + ".lock"); err != nil {
		t.Fatalf("custom store lock missing: %v", err)
	}

	exclusions, err := reviewSnapshotArtifactPaths(repo, customDB, customOut)
	if err != nil {
		t.Fatalf("reviewSnapshotArtifactPaths() error = %v", err)
	}
	snapshot, cleanup, err := buildReviewSnapshotWithSnapshotPathsAndExclusions(context.Background(), repo, nil, exclusions)
	if err != nil {
		t.Fatalf("buildReviewSnapshotWithSnapshotPathsAndExclusions() error = %v", err)
	}
	defer cleanup()
	if _, err := os.Stat(filepath.Join(snapshot, "tracked.go")); err != nil {
		t.Fatalf("snapshot missing tracked.go: %v", err)
	}
	for _, excluded := range []string{"review-state", "review-output"} {
		if _, err := os.Stat(filepath.Join(snapshot, excluded)); !os.IsNotExist(err) {
			t.Fatalf("configured artifact %s present in snapshot, stat err=%v", excluded, err)
		}
	}
}

func TestReviewSnapshotArtifactPathsResolveSymlinkAliases(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	aliasParent := t.TempDir()
	aliasRoot := filepath.Join(aliasParent, "repo-alias")
	if err := os.Symlink(repo, aliasRoot); err != nil {
		t.Skipf("symlink creation is not supported in this environment: %v", err)
	}
	runGitCommand(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "tracked.go"), []byte("package tracked\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(tracked.go) error = %v", err)
	}
	runGitCommand(t, repo, "add", "tracked.go")
	customDB := filepath.Join(aliasRoot, "review-state", "audit-store.data")
	customOut := filepath.Join(aliasRoot, "review-output")
	customReport := filepath.Join(customOut, "nested", "report.json")
	if err := os.MkdirAll(filepath.Dir(customDB), 0o700); err != nil {
		t.Fatalf("MkdirAll(custom db) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(customReport), 0o700); err != nil {
		t.Fatalf("MkdirAll(custom report) error = %v", err)
	}
	for _, path := range []string{customDB, customDB + ".lock", customReport} {
		if err := os.WriteFile(path, []byte("private artifact\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}

	exclusions, err := reviewSnapshotArtifactPaths(repo, customDB, customOut)
	if err != nil {
		t.Fatalf("reviewSnapshotArtifactPaths() error = %v", err)
	}
	snapshot, cleanup, err := buildReviewSnapshotWithSnapshotPathsAndExclusions(context.Background(), repo, nil, exclusions)
	if err != nil {
		t.Fatalf("buildReviewSnapshotWithSnapshotPathsAndExclusions() error = %v", err)
	}
	defer cleanup()
	for _, excluded := range []string{"review-state", "review-output"} {
		if _, err := os.Stat(filepath.Join(snapshot, excluded)); !os.IsNotExist(err) {
			t.Fatalf("symlink-aliased artifact %s present in snapshot, stat err=%v", excluded, err)
		}
	}
}

func TestBuildReviewSnapshotRestrictsUntrackedFilesToSubmittedPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	runGitCommand(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "tracked.go"), []byte("package tracked\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(tracked.go) error = %v", err)
	}
	runGitCommand(t, repo, "add", "tracked.go")
	for _, name := range []string{"selected.go", "private.txt"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(name+"\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	snapshot, cleanup, err := buildReviewSnapshotWithSnapshotPaths(context.Background(), repo, []string{"selected.go"})
	if err != nil {
		t.Fatalf("buildReviewSnapshotWithSnapshotPaths() error = %v", err)
	}
	defer cleanup()
	for _, included := range []string{"tracked.go", "selected.go"} {
		if _, err := os.Stat(filepath.Join(snapshot, included)); err != nil {
			t.Fatalf("selected snapshot file %s missing: %v", included, err)
		}
	}
	if _, err := os.Stat(filepath.Join(snapshot, "private.txt")); !os.IsNotExist(err) {
		t.Fatalf("non-selected untracked file was staged, stat err=%v", err)
	}
}

func TestFileListPreservesHashPathsInUntrackedSnapshot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	runGitCommand(t, repo, "init")
	selected := []string{"#config.go", "# config.go"}
	if runtime.GOOS != "windows" {
		selected = append(selected, "#\tconfig.go")
	}
	for _, name := range selected {
		if err := os.WriteFile(filepath.Join(repo, name), []byte("package config\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", name, err)
		}
	}
	fileList := filepath.Join(t.TempDir(), "files.txt")
	fileListContent := "# comment\n#\tcomment\n#config.go\n\\# config.go\n"
	if runtime.GOOS != "windows" {
		fileListContent += "\\#\tconfig.go\n"
	}
	if err := os.WriteFile(fileList, []byte(fileListContent), 0o600); err != nil {
		t.Fatalf("WriteFile(file list) error = %v", err)
	}
	source, err := inputsource.Read(context.Background(), inputsource.Options{FileList: fileList, RepoPath: repo})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	wantFiles := append([]string(nil), selected...)
	sort.Strings(wantFiles)
	if !reflect.DeepEqual(source.FileList, wantFiles) {
		t.Fatalf("FileList = %#v, want %#v", source.FileList, wantFiles)
	}
	snapshot, cleanup, err := buildReviewSnapshotWithSnapshotPaths(context.Background(), repo, snapshotUntrackedPaths(source, nil))
	if err != nil {
		t.Fatalf("buildReviewSnapshotWithSnapshotPaths() error = %v", err)
	}
	defer cleanup()
	for _, name := range selected {
		if _, err := os.Stat(filepath.Join(snapshot, name)); err != nil {
			t.Fatalf("hash-prefixed untracked path %q missing from snapshot: %v", name, err)
		}
	}
}

func TestStagedSnapshotDerivesDependencyModeFromFilteredSnapshot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	runGitCommand(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("vendor/\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.gitignore) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.test\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "vendor"), 0o700); err != nil {
		t.Fatalf("MkdirAll(vendor) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "vendor", "modules.txt"), []byte("# ignored vendor\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(vendor/modules.txt) error = %v", err)
	}
	runGitCommand(t, repo, "add", ".gitignore", "go.mod")

	fs := &recordingStageFS{}
	cleanup, mode, err := stageReviewWorkspaceWithSnapshotPathsAndMode(context.Background(), fs, codeexecutor.Workspace{Path: "/work"}, "e2b", repo, "", true, nil)
	if err != nil {
		t.Fatalf("stageReviewWorkspaceWithSnapshotPathsAndMode() error = %v", err)
	}
	defer cleanup()
	if mode == dependencyModeVendor {
		t.Fatalf("dependency mode = %q, want mode derived without omitted vendor directory", mode)
	}
	if got := workspaceRuntimeEnvForDependencyMode("e2b", mode)["GOFLAGS"]; got == "-mod=vendor" {
		t.Fatalf("GOFLAGS = %q, want prepared-cache mode without vendor", got)
	}
	if _, err := os.Stat(filepath.Join(fs.src, "vendor")); !os.IsNotExist(err) {
		t.Fatalf("filtered snapshot contains ignored vendor directory, stat err=%v", err)
	}
}

func TestSelectedUntrackedReviewFilesUseBoundedPathspecBatches(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	runGitCommand(t, repo, "init")
	selected := []string{"selected-a.go", "selected-b.go", "selected-c.go"}
	for _, name := range selected {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(name+"\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	allowed := snapshotPathSet(selected)
	var files []reviewSnapshotFile
	if err := appendUntrackedReviewFilesInBatches(context.Background(), repo, len(selected), allowed, &files, selected, 1, int64(len(selected[0])+1)); err != nil {
		t.Fatalf("appendUntrackedReviewFilesInBatches() error = %v", err)
	}
	got := make([]string, 0, len(files))
	for _, file := range files {
		got = append(got, file.Path)
	}
	if !reflect.DeepEqual(got, selected) {
		t.Fatalf("selected untracked files = %#v, want %#v", got, selected)
	}
}

func TestSnapshotPathSetPreservesWhitespace(t *testing.T) {
	got := snapshotPathSet([]string{" leading.go", "trailing.go ", "tab\t.go", "  ", "/dev/null"})
	for _, path := range []string{" leading.go", "trailing.go ", "tab\t.go", "  "} {
		if _, ok := got[path]; !ok {
			t.Fatalf("snapshotPathSet() lost exact path %q: %#v", path, got)
		}
	}
	if len(got) != 4 {
		t.Fatalf("snapshotPathSet() = %#v, want four selected paths", got)
	}
}

func TestSnapshotUntrackedPathsOnlyIncludesAllForRepoWorkspace(t *testing.T) {
	files := []review.DiffFile{{OldPath: "old.go", NewPath: "new.go"}}
	if got := snapshotUntrackedPaths(inputsource.Source{Type: review.InputTypeRepo}, files); got != nil {
		t.Fatalf("repo snapshot paths = %#v, want unrestricted untracked inventory", got)
	}
	got := snapshotUntrackedPaths(inputsource.Source{Type: review.InputTypeFixture}, files)
	if want := []string{"new.go", "old.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fixture snapshot paths = %#v, want %#v", got, want)
	}
	got = snapshotUntrackedPaths(inputsource.Source{
		Type:     review.InputTypeFileList,
		FileList: []string{" selected.go ", "  "},
	}, nil)
	if want := []string{"  ", " selected.go "}; !reflect.DeepEqual(got, want) {
		t.Fatalf("file-list snapshot paths = %#v, want %#v", got, want)
	}
}

func TestBuildReviewSnapshotRejectsOversizeFileBeforeCopy(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	runGitCommand(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "large.txt"), []byte("0123456789"), 0o600); err != nil {
		t.Fatalf("WriteFile(large.txt) error = %v", err)
	}
	runGitCommand(t, repo, "add", "large.txt")

	snapshot, cleanup, err := buildReviewSnapshotWithLimit(context.Background(), repo, 4)
	if err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatal("buildReviewSnapshotWithLimit() error = nil, want snapshot size rejection")
	}
	if snapshot != "" || cleanup != nil {
		t.Fatalf("snapshot cleanup = %q/%t, want no materialized snapshot", snapshot, cleanup != nil)
	}
	if !strings.Contains(err.Error(), "exceeds 4 bytes") {
		t.Fatalf("error = %q, want snapshot size message", err)
	}
}

func TestBuildReviewSnapshotRejectsTooManyFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	runGitCommand(t, repo, "init")
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(name), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	runGitCommand(t, repo, "add", "a.txt", "b.txt", "c.txt")

	snapshot, cleanup, err := buildReviewSnapshotWithLimits(context.Background(), repo, -1, 2)
	if err == nil || !strings.Contains(err.Error(), "file count exceeded 2") {
		t.Fatalf("buildReviewSnapshotWithLimits() error = %v, want file-count rejection", err)
	}
	if snapshot != "" || cleanup != nil {
		t.Fatalf("snapshot cleanup = %q/%t, want no materialized snapshot", snapshot, cleanup != nil)
	}
}

func TestBuildReviewSnapshotRejectsTrackedSubmodule(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	runGitCommand(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "tracked.go"), []byte("package tracked\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(tracked.go) error = %v", err)
	}
	runGitCommand(t, repo, "add", "tracked.go")
	runGitCommand(t, repo, "commit", "-m", "base")
	hash := runGitOutput(t, repo, "rev-parse", "HEAD")
	runGitCommand(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+hash+",submodule")

	_, cleanup, err := buildReviewSnapshot(context.Background(), repo)
	if err == nil || !strings.Contains(err.Error(), "unsupported tracked git submodule") {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("buildReviewSnapshot() error = %v, want explicit submodule rejection", err)
	}
}

func TestPrepareSandboxDependenciesRejectsUntrustedHostPreparation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	if err := prepareSandboxDependencies(context.Background(), root, "", false); err == nil || !strings.Contains(err.Error(), "--allow-trusted-host-preparation") {
		t.Fatalf("prepareSandboxDependencies() error = %v, want trusted host preparation rejection", err)
	}
}

func TestPrepareSandboxDependenciesAcceptsVendoredDependencies(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "vendor"), 0o700); err != nil {
		t.Fatalf("MkdirAll(vendor) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "vendor", "modules.txt"), []byte("# vendored\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(vendor/modules.txt) error = %v", err)
	}
	if err := prepareSandboxDependencies(context.Background(), root, "", false); err != nil {
		t.Fatalf("prepareSandboxDependencies() error = %v, want vendored dependencies accepted", err)
	}
}

func TestPrepareSandboxDependenciesAcceptsPreProvisionedModuleCache(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	cache := filepath.Join(root, ".gomodcache", "cache", "download")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatalf("MkdirAll(cache) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(cache, "preprovisioned.mod"), []byte("module cache\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := prepareSandboxDependencies(context.Background(), root, "", false); err != nil {
		t.Fatalf("prepareSandboxDependencies() error = %v, want pre-provisioned cache accepted", err)
	}
}

func TestReviewSnapshotKeepsSourceReadOnlyAndCachesWritable(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "pkg")
	cache := filepath.Join(root, ".gocache")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("MkdirAll(source) error = %v", err)
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatalf("MkdirAll(cache) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "a.go"), []byte("package pkg\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}

	if err := makeSandboxCachesWritable(root, []string{cache}); err != nil {
		t.Fatalf("makeSandboxCachesWritable() error = %v", err)
	}
	if err := lockReviewSnapshotSource(root, []string{cache}); err != nil {
		t.Fatalf("lockReviewSnapshotSource() error = %v", err)
	}
	sourceInfo, err := os.Stat(filepath.Join(source, "a.go"))
	if err != nil {
		t.Fatalf("Stat(source) error = %v", err)
	}
	if sourceInfo.Mode().Perm()&0o222 != 0 {
		t.Fatalf("source mode = %o, want read-only", sourceInfo.Mode().Perm())
	}
	if sourceInfo.Mode().Perm()&0o444 != 0o444 {
		t.Fatalf("source mode = %o, want readable by non-root runtime user", sourceInfo.Mode().Perm())
	}
	cacheInfo, err := os.Stat(cache)
	if err != nil {
		t.Fatalf("Stat(cache) error = %v", err)
	}
	if cacheInfo.Mode().Perm()&0o222 == 0 {
		t.Fatalf("cache mode = %o, want writable", cacheInfo.Mode().Perm())
	}
	cleanupReviewSnapshot(root)
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("snapshot root still exists after cleanup, stat err=%v", err)
	}
}

func TestSkippedOnlySandboxPlanNeedsHumanReview(t *testing.T) {
	runs := []review.SandboxRun{{
		Status: sandboxrun.StatusSkipped,
	}}
	if got := statusFor(nil, runs); got != review.TaskStatusFailed {
		t.Fatalf("statusFor() = %q, want failed", got)
	}
	if got := conclusionFor(review.TaskStatusFailed, nil, runs); got != "needs_human_review" {
		t.Fatalf("conclusionFor() = %q, want needs_human_review", got)
	}
}

type recordingStageFS struct {
	src string
}

type blockingWorkspaceManager struct{}

func (blockingWorkspaceManager) CreateWorkspace(context.Context, string, codeexecutor.WorkspacePolicy) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{}, nil
}

func (blockingWorkspaceManager) Cleanup(context.Context, codeexecutor.Workspace) error {
	select {}
}

func TestWorkspaceCleanupBoundsBlockingManager(t *testing.T) {
	previous := workspaceCleanupTimeout
	workspaceCleanupTimeout = 20 * time.Millisecond
	defer func() { workspaceCleanupTimeout = previous }()

	cleanup := newWorkspaceCleanup(blockingWorkspaceManager{}, codeexecutor.Workspace{}, nil, nil)
	start := time.Now()
	cleanup(context.Background())
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Fatalf("cleanup returned after %s, want bounded wait around 20ms", elapsed)
	}

	done := make(chan struct{})
	go func() {
		cleanup(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("second cleanup call blocked indefinitely")
	}
}

func TestWorkspaceCleanupContinuesAfterBlockingManager(t *testing.T) {
	previous := workspaceCleanupTimeout
	workspaceCleanupTimeout = 20 * time.Millisecond
	defer func() { workspaceCleanupTimeout = previous }()

	snapshotCleaned := make(chan struct{})
	cleanup := newWorkspaceCleanup(blockingWorkspaceManager{}, codeexecutor.Workspace{}, nil, func() {
		close(snapshotCleaned)
	})
	cleanup(context.Background())
	select {
	case <-snapshotCleaned:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("snapshot cleanup was skipped after manager cleanup timed out")
	}
}

func TestWorkspaceCleanupBoundsBlockingBackendClose(t *testing.T) {
	previous := workspaceCleanupTimeout
	workspaceCleanupTimeout = 20 * time.Millisecond
	defer func() { workspaceCleanupTimeout = previous }()

	snapshotCleaned := make(chan struct{})
	cleanup := newWorkspaceCleanup(nil, codeexecutor.Workspace{}, func() error {
		select {}
	}, func() {
		close(snapshotCleaned)
	})
	start := time.Now()
	cleanup(context.Background())
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Fatalf("cleanup returned after %s, want bounded wait around 20ms", elapsed)
	}
	select {
	case <-snapshotCleaned:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("snapshot cleanup was skipped after backend close timed out")
	}
}

func TestCloseWorkspaceBackendBoundsBlockingClose(t *testing.T) {
	previous := workspaceCleanupTimeout
	workspaceCleanupTimeout = 20 * time.Millisecond
	defer func() { workspaceCleanupTimeout = previous }()

	start := time.Now()
	closeWorkspaceBackend(func() error {
		select {}
	})
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Fatalf("closeWorkspaceBackend() returned after %s, want bounded wait around 20ms", elapsed)
	}
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

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s failed: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output))
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

type timeoutRecordingRuntime struct {
	commands []string
}

func (r *timeoutRecordingRuntime) Name() string { return "timeout-test" }

func (r *timeoutRecordingRuntime) Run(_ context.Context, command string) (sandboxrun.Result, error) {
	r.commands = append(r.commands, command)
	return sandboxrun.Result{TimedOut: true}, nil
}

type cancelOnSecondPermissionStore struct {
	store.Store
	cancel          context.CancelFunc
	permissionCalls int
}

func (s *cancelOnSecondPermissionStore) RecordPermissionDecision(ctx context.Context, decision review.PermissionDecisionRecord) error {
	s.permissionCalls++
	if s.permissionCalls == 2 {
		s.cancel()
	}
	return s.Store.RecordPermissionDecision(ctx, decision)
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
		false,
		[]string{"go test ./..."},
		fixedTestTime(),
		time.Second,
		"",
		func(context.Context, string, string, string, time.Duration, string, bool, bool) (sandboxrun.Runtime, func(), *review.SandboxRun) {
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

func TestCompletedSandboxRunIsPersistedWhenNextPermissionIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "review_agent.db")
	st, err := store.NewSQLite(context.Background(), path)
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	task := review.ReviewTask{ID: "task-partial-cancellation", Status: review.TaskStatusRunning}
	if err := st.CreateTask(context.Background(), task); err != nil {
		st.Close()
		t.Fatalf("CreateTask() error = %v", err)
	}
	wrapped := &cancelOnSecondPermissionStore{Store: st, cancel: cancel}
	_, runs, err := executePlannedCommandsWithFactory(
		ctx,
		wrapped,
		task.ID,
		"fake",
		false,
		false,
		[]string{"go test ./...", "go vet ./..."},
		fixedTestTime(),
		time.Second,
		"",
		func(context.Context, string, string, string, time.Duration, string, bool, bool) (sandboxrun.Runtime, func(), *review.SandboxRun) {
			return sandboxrun.FakeRuntime{}, nil, nil
		},
	)
	if !errors.Is(err, context.Canceled) {
		st.Close()
		t.Fatalf("executePlannedCommandsWithFactory() error = %v, want context.Canceled", err)
	}
	if len(runs) != 1 || runs[0].Status != sandboxrun.StatusPassed {
		st.Close()
		t.Fatalf("runs = %#v, want one completed run", runs)
	}
	if err := recordSandboxRuns(ctx, wrapped, runs); err != nil {
		st.Close()
		t.Fatalf("recordSandboxRuns() error = %v", err)
	}
	finishCtx, finishCancel := failedTaskContext(ctx)
	if err := st.FinishTask(finishCtx, task.ID, review.TaskStatusFailed, context.Canceled.Error(), fixedTestTime()); err != nil {
		finishCancel()
		st.Close()
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
	if len(loaded.SandboxRuns) != 1 || loaded.SandboxRuns[0].Status != sandboxrun.StatusPassed {
		t.Fatalf("loaded sandbox runs = %#v, want completed run", loaded.SandboxRuns)
	}
}

func TestTimeoutAuditsRemainingPlannedCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review_agent.db")
	st, err := store.NewSQLite(context.Background(), path)
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	task := review.ReviewTask{ID: "task-timeout-audit", Status: review.TaskStatusRunning}
	if err := st.CreateTask(context.Background(), task); err != nil {
		st.Close()
		t.Fatalf("CreateTask() error = %v", err)
	}
	runtime := &timeoutRecordingRuntime{}
	commands := []string{"go test ./...", "go vet ./...", "go test ./skills/code-review/scripts"}
	decisions, runs, err := executePlannedCommandsWithFactory(
		context.Background(),
		st,
		task.ID,
		"fake",
		false,
		false,
		commands,
		fixedTestTime(),
		time.Second,
		"",
		func(context.Context, string, string, string, time.Duration, string, bool, bool) (sandboxrun.Runtime, func(), *review.SandboxRun) {
			return runtime, nil, nil
		},
	)
	if err != nil {
		st.Close()
		t.Fatalf("executePlannedCommandsWithFactory() error = %v", err)
	}
	if !reflect.DeepEqual(runtime.commands, []string{"go test ./..."}) {
		t.Fatalf("executed commands = %#v, want only first command", runtime.commands)
	}
	if len(decisions) != len(commands) || len(runs) != len(commands) {
		st.Close()
		t.Fatalf("records = decisions:%d runs:%d, want %d each", len(decisions), len(runs), len(commands))
	}
	if runs[0].ErrorType != sandboxrun.ErrorTimeout {
		st.Close()
		t.Fatalf("first run error type = %q, want timeout", runs[0].ErrorType)
	}
	for index := 1; index < len(commands); index++ {
		if runs[index].Status != sandboxrun.StatusSkipped || runs[index].ErrorType != sandboxrun.ErrorTimeout {
			st.Close()
			t.Fatalf("remaining run %d = %#v, want timeout skipped record", index, runs[index])
		}
		if !strings.Contains(decisions[index].Reason, "not executed") {
			st.Close()
			t.Fatalf("remaining decision %d = %#v, want skip reason", index, decisions[index])
		}
	}
	if err := recordSandboxRuns(context.Background(), st, runs); err != nil {
		st.Close()
		t.Fatalf("recordSandboxRuns() error = %v", err)
	}
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
	if len(loaded.PermissionDecisions) != len(commands) || len(loaded.SandboxRuns) != len(commands) {
		t.Fatalf("reopened audit records = decisions:%d runs:%d, want %d each", len(loaded.PermissionDecisions), len(loaded.SandboxRuns), len(commands))
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
	if len(cfg.CapDrop) != 1 || cfg.CapDrop[0] != "ALL" {
		t.Fatalf("CapDrop = %#v, want [ALL]", cfg.CapDrop)
	}
	if len(cfg.SecurityOpt) != 1 || cfg.SecurityOpt[0] != "no-new-privileges:true" {
		t.Fatalf("SecurityOpt = %#v, want no-new-privileges", cfg.SecurityOpt)
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
	if cfg.Image != "golang:1.24.6" {
		t.Fatalf("Image = %q, want golang:1.24.6", cfg.Image)
	}
	if cfg.WorkingDir != "/" {
		t.Fatalf("WorkingDir = %q, want /", cfg.WorkingDir)
	}
	if cfg.User != containerUser {
		t.Fatalf("User = %q, want %q", cfg.User, containerUser)
	}
}

func TestContainerBindMountsDoNotExposeHostModuleCache(t *testing.T) {
	t.Setenv("GOMODCACHE", "/host/go/pkg/mod")
	t.Setenv("GOCACHE", "/host/go-build")

	got := containerBindMounts("/repo")
	if len(got) != 0 {
		t.Fatalf("containerBindMounts() = %#v, want no host repository or module-cache mounts", got)
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
