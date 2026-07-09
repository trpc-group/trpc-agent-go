//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package orchestrator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tcontainer "github.com/docker/docker/api/types/container"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/inputsource"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/safetywrap"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandboxrun"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

const (
	defaultMaxSandboxOutput = 4096
	defaultSkillName        = "code-review"
	defaultSandboxTimeout   = 30 * time.Second
	containerSandboxImage   = "golang:1.24"
	containerGoBuildCache   = "/tmp/go-build"
	containerGoModCache     = "/go/pkg/mod"
	reviewAgentModuleDir    = "examples/code_review_agent"
	rootModuleDecl          = "module trpc.group/trpc-go/trpc-agent-go"
)

var defaultSandboxCommands = []string{
	"go test ./...",
	"go vet ./...",
	"go test ./skills/code-review/scripts",
	"go test ./internal/rules",
}

// Options configures one review run.
type Options struct {
	FixtureDir     string
	DiffFile       string
	FileList       string
	OutDir         string
	DBPath         string
	Model          string
	Runtime        string
	RepoPath       string
	SandboxTimeout time.Duration
	Now            time.Time
	Planner        Planner
}

// Result is returned by the orchestrator after reports are written.
type Result struct {
	TaskID       string
	Report       review.Report
	JSONPath     string
	MarkdownPath string
	DBPath       string
}

type bindMount struct {
	HostPath      string
	ContainerPath string
	Mode          string
}

// Planner produces the model-coordinated review plan.
type Planner interface {
	PlanReview(ctx context.Context, req PlanRequest) (review.ReviewPlan, error)
}

// PlanRequest contains non-secret context for model planning.
type PlanRequest struct {
	Model   string
	Runtime string
	Skill   string
	WorkDir string
	Files   []review.DiffFile
}

type sandboxWorkspace struct {
	workDir string
}

// EnvPlanner validates OpenAI-compatible model configuration for real runs.
type EnvPlanner struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

type modelPlanEnvelope struct {
	Commands    []string `json:"commands"`
	RuleSources []string `json:"rule_sources"`
}

// PlanReview asks an OpenAI-compatible model for the orchestration plan. Unit
// tests can use the fake runtime without model keys.
func (p EnvPlanner) PlanReview(ctx context.Context, req PlanRequest) (review.ReviewPlan, error) {
	if err := ctx.Err(); err != nil {
		return review.ReviewPlan{}, err
	}
	runtimeName := strings.TrimSpace(req.Runtime)
	if runtimeName == "" {
		runtimeName = "container"
	}
	modelName := strings.TrimSpace(req.Model)
	if strings.EqualFold(runtimeName, "fake") {
		if modelName == "" {
			modelName = "mock-model"
		}
		return reviewPlan(modelName, "mock", "mock_planner", req.Skill, runtimeName, req.WorkDir), nil
	}
	if modelName == "" {
		return review.ReviewPlan{}, fmt.Errorf("model orchestration requires --model or MODEL for runtime %q; use --runtime fake for unit tests", runtimeName)
	}
	if strings.TrimSpace(p.APIKey) == "" {
		return review.ReviewPlan{}, fmt.Errorf("model orchestration requires OPENAI_API_KEY for runtime %q; use --runtime fake for unit tests", runtimeName)
	}
	modelPlan, err := p.requestModelPlan(ctx, modelName, req)
	if err != nil {
		return review.ReviewPlan{}, err
	}
	plan := reviewPlan(modelName, "openai_compatible", "model_response", req.Skill, runtimeName, req.WorkDir)
	if len(modelPlan.Commands) > 0 {
		if allowedCommands := allowlistedModelCommands(modelPlan.Commands, req.WorkDir); len(allowedCommands) > 0 {
			plan.Commands = redactStrings(allowedCommands)
		}
	}
	if len(modelPlan.RuleSources) > 0 {
		plan.RuleSources = redactStrings(modelPlan.RuleSources)
	}
	return plan, nil
}

func reviewPlan(modelName string, provider string, source string, skill string, runtimeName string, workDir string) review.ReviewPlan {
	if skill == "" {
		skill = defaultSkillName
	}
	return review.ReviewPlan{
		Model:    redact.Text(modelName).Text,
		Provider: provider,
		Source:   source,
		Skill:    skill,
		Runtime:  runtimeName,
		Commands: newSandboxWorkspace(workDir).commandAllowlist(),
		RuleSources: []string{
			"skills/code-review/SKILL.md",
			"skills/code-review/docs/rules.md",
		},
	}
}

func (p EnvPlanner) requestModelPlan(ctx context.Context, modelName string, req PlanRequest) (modelPlanEnvelope, error) {
	body, err := json.Marshal(chatCompletionRequest{
		Model:       modelName,
		Temperature: 0,
		Messages: []chatMessage{
			{Role: "system", Content: "You plan safe code-review agent execution. Return compact JSON only."},
			{Role: "user", Content: buildPlanningPrompt(req)},
		},
	})
	if err != nil {
		return modelPlanEnvelope{}, fmt.Errorf("encode model request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, chatCompletionsURL(p.BaseURL), bytes.NewReader(body))
	if err != nil {
		return modelPlanEnvelope{}, fmt.Errorf("build model request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	client := p.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(httpReq)
	if err != nil {
		return modelPlanEnvelope{}, fmt.Errorf("call model planner: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return modelPlanEnvelope{}, fmt.Errorf("read model planner response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return modelPlanEnvelope{}, fmt.Errorf("model planner returned status %d: %s", res.StatusCode, redact.Text(string(raw)).Text)
	}
	var completion chatCompletionResponse
	if err := json.Unmarshal(raw, &completion); err != nil {
		return modelPlanEnvelope{}, fmt.Errorf("decode model planner response: %w", err)
	}
	if len(completion.Choices) == 0 {
		return modelPlanEnvelope{}, fmt.Errorf("model planner returned no choices")
	}
	var plan modelPlanEnvelope
	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return modelPlanEnvelope{}, fmt.Errorf("decode model planner content: %w", err)
	}
	return plan, nil
}

func chatCompletionsURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return base + "/chat/completions"
}

func buildPlanningPrompt(req PlanRequest) string {
	var files []string
	for _, file := range req.Files {
		files = append(files, file.NewPath)
	}
	sort.Strings(files)
	payload := map[string]any{
		"skill":            req.Skill,
		"runtime":          req.Runtime,
		"changed_files":    files,
		"allowed_commands": newSandboxWorkspace(req.WorkDir).commandAllowlist(),
		"rule_sources": []string{
			"skills/code-review/SKILL.md",
			"skills/code-review/docs/rules.md",
		},
		"response_schema": map[string]any{
			"commands":     []string{"go test ./...", "go vet ./..."},
			"rule_sources": []string{"skills/code-review/SKILL.md"},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func redactStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		out = append(out, redact.Text(item).Text)
	}
	return out
}

func allowlistedModelCommands(commands []string, workDir string) []string {
	allowlist := newSandboxWorkspace(workDir).commandAllowlist()
	allowed := make(map[string]string, len(allowlist))
	for _, command := range allowlist {
		allowed[canonicalCommand(command)] = command
	}
	seen := make(map[string]struct{}, len(commands))
	out := make([]string, 0, len(commands))
	for _, command := range commands {
		canonical := canonicalCommand(command)
		allowedCommand, ok := allowed[canonical]
		if !ok {
			continue
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, allowedCommand)
	}
	return out
}

func canonicalCommand(command string) string {
	return strings.Join(strings.Fields(command), " ")
}

func defaultPlanner() Planner {
	return EnvPlanner{
		APIKey:     os.Getenv("OPENAI_API_KEY"),
		BaseURL:    os.Getenv("OPENAI_BASE_URL"),
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func newSandboxWorkspace(workDir string) sandboxWorkspace {
	return sandboxWorkspace{workDir: strings.TrimSpace(workDir)}
}

func (ws sandboxWorkspace) commandAllowlist() []string {
	if ws.hasSelectedRepo() {
		return []string{
			"go test ./...",
			"go vet ./...",
		}
	}
	return append([]string(nil), defaultSandboxCommands...)
}

func (ws sandboxWorkspace) root() (string, error) {
	if ws.hasSelectedRepo() {
		abs, err := filepath.Abs(ws.workDir)
		if err != nil {
			return "", fmt.Errorf("resolve sandbox workdir: %w", err)
		}
		return abs, nil
	}
	return repositoryRoot()
}

func (ws sandboxWorkspace) runtimeCwd(runtimeName string) string {
	if runtimeName == "local" {
		if ws.hasSelectedRepo() {
			return "."
		}
		return filepath.ToSlash(reviewAgentModuleDir)
	}
	if ws.hasSelectedRepo() {
		return codeexecutor.DirWork
	}
	return path.Join(codeexecutor.DirWork, reviewAgentModuleDir)
}

func (ws sandboxWorkspace) hasSelectedRepo() bool {
	return ws.workDir != ""
}

// Run executes a model-planned review over fixture diffs.
func Run(ctx context.Context, opts Options) (result Result, err error) {
	if opts.FixtureDir == "" {
		opts.FixtureDir = "testdata/fixtures"
	}
	if opts.OutDir == "" {
		opts.OutDir = "./out"
	}
	if opts.DBPath == "" {
		opts.DBPath = filepath.Join(opts.OutDir, "review_agent.db")
	}
	if opts.Runtime == "" {
		opts.Runtime = "container"
	}
	if opts.SandboxTimeout == 0 {
		opts.SandboxTimeout = defaultSandboxTimeout
	}
	fixedNow := !opts.Now.IsZero()
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	input, err := inputsource.Read(ctx, inputsource.Options{
		FixtureDir: opts.FixtureDir,
		DiffFile:   opts.DiffFile,
		RepoPath:   opts.RepoPath,
		FileList:   opts.FileList,
	})
	if err != nil {
		return Result{}, err
	}
	rawDiff := input.Diff
	taskID := runTaskID(rawDiff, now)
	task := review.ReviewTask{
		ID:        taskID,
		Status:    review.TaskStatusRunning,
		InputType: input.Type,
		RepoPath:  input.RepoPath,
		DiffHash:  hashText(rawDiff),
		StartedAt: now.UTC(),
	}

	st, err := store.NewSQLite(ctx, opts.DBPath)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if closeErr := st.Close(); closeErr != nil {
			if err != nil {
				err = errors.Join(err, fmt.Errorf("close store: %w", closeErr))
				return
			}
			err = fmt.Errorf("close store: %w", closeErr)
		}
	}()
	if err := st.CreateTask(ctx, task); err != nil {
		return Result{}, err
	}
	failTask := func(runErr error) error {
		if runErr == nil {
			return nil
		}
		if finishErr := st.FinishTask(ctx, task.ID, review.TaskStatusFailed, runErr.Error()); finishErr != nil {
			return errors.Join(runErr, fmt.Errorf("finish failed task: %w", finishErr))
		}
		return runErr
	}

	files, err := parseInputFiles(rawDiff, input.FileList)
	if err != nil {
		return Result{}, failTask(err)
	}
	changedFilesJSON, err := json.Marshal(files)
	if err != nil {
		return Result{}, failTask(fmt.Errorf("marshal changed files: %w", err))
	}
	redactedDiff := redact.Text(rawDiff)
	if err := st.RecordInput(ctx, store.InputRecord{
		TaskID:           task.ID,
		DiffSummary:      summarizeDiff(input, files),
		ChangedFilesJSON: string(changedFilesJSON),
		RedactedDiff:     redactedDiff.Text,
	}); err != nil {
		return Result{}, failTask(err)
	}

	planner := opts.Planner
	if planner == nil {
		planner = defaultPlanner()
	}
	plan, err := planner.PlanReview(ctx, PlanRequest{
		Model:   opts.Model,
		Runtime: opts.Runtime,
		Skill:   defaultSkillName,
		WorkDir: input.WorkDir,
		Files:   files,
	})
	if err != nil {
		return Result{}, failTask(err)
	}

	findings := rules.Evaluate(files)
	if err := st.SaveFindings(ctx, task.ID, findings); err != nil {
		return Result{}, failTask(err)
	}

	decisions, runs, err := executePlannedCommands(ctx, st, task.ID, opts.Runtime, plan.Commands, now, opts.SandboxTimeout, input.WorkDir)
	if err != nil {
		return Result{}, failTask(err)
	}
	for _, run := range runs {
		if err := st.RecordSandboxRun(ctx, run); err != nil {
			return Result{}, failTask(err)
		}
	}

	metrics := report.BuildMetrics(task.ID, task.StartedAt, findings, runs, decisions, redactedDiff.Count+countFindingRedactions(findings))
	if fixedNow {
		metrics.TotalDurationMillis = 0
	}
	task.Status = statusFor(findings, runs)
	finishedAt := now.UTC()
	task.FinishedAt = &finishedAt
	conclusion := conclusionFor(task.Status, findings, runs)
	r := review.Report{
		Task:                task,
		Summary:             summarizeOutcome(input, files, findings, runs, plan),
		Plan:                plan,
		ChangedFiles:        files,
		Findings:            findings,
		SandboxRuns:         runs,
		PermissionDecisions: decisions,
		Metrics:             metrics,
		Conclusion:          conclusion,
	}
	artifacts, err := report.Write(opts.OutDir, r, now)
	if err != nil {
		return Result{}, failTask(err)
	}
	r.Artifacts = artifacts
	if err := st.SaveArtifacts(ctx, artifacts); err != nil {
		return Result{}, failTask(err)
	}
	jsonPath, mdPath := artifactPaths(artifacts)
	metricsJSON, _ := json.Marshal(metrics)
	if err := st.SaveReport(ctx, store.ReportRecord{
		TaskID:       task.ID,
		JSONPath:     jsonPath,
		MarkdownPath: mdPath,
		Conclusion:   conclusion,
		MetricsJSON:  string(metricsJSON),
	}); err != nil {
		return Result{}, failTask(err)
	}
	if err := st.FinishTask(ctx, task.ID, task.Status, ""); err != nil {
		return Result{}, err
	}
	return Result{
		TaskID:       task.ID,
		Report:       r,
		JSONPath:     jsonPath,
		MarkdownPath: mdPath,
		DBPath:       opts.DBPath,
	}, nil
}

func runTaskID(diff string, now time.Time) string {
	sum := sha256.Sum256([]byte(diff + now.UTC().Format(time.RFC3339Nano)))
	return "review-" + hex.EncodeToString(sum[:])[:12]
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func parseInputFiles(rawDiff string, fileList []string) ([]review.DiffFile, error) {
	if strings.TrimSpace(rawDiff) != "" {
		return diffparse.Parse(rawDiff)
	}
	files := make([]review.DiffFile, 0, len(fileList))
	for _, file := range fileList {
		files = append(files, review.DiffFile{
			OldPath:    file,
			NewPath:    file,
			PackageDir: inferPackageDir(file),
		})
	}
	return files, nil
}

func inferPackageDir(path string) string {
	path = filepath.ToSlash(path)
	if path == "" || !strings.HasSuffix(path, ".go") {
		return ""
	}
	dir := filepath.ToSlash(filepath.Dir(path))
	if dir == "." {
		return ""
	}
	return dir
}

func summarizeDiff(input inputsource.Source, files []review.DiffFile) string {
	if input.Summary != "" {
		return fmt.Sprintf("%s Parsed %d changed files.", input.Summary, len(files))
	}
	return fmt.Sprintf("Reviewed %d changed files.", len(files))
}

func summarizeOutcome(input inputsource.Source, files []review.DiffFile, findings []review.Finding, runs []review.SandboxRun, plan review.ReviewPlan) string {
	summary := fmt.Sprintf("Model plan %q coordinated skill %q for %d changed files, produced %d findings, and recorded %d sandbox runs.", plan.Model, plan.Skill, len(files), len(findings), len(runs))
	if input.Type == review.InputTypeFileList {
		return summary + " File-list input supplies path context only; content-based deterministic rules require diff input."
	}
	return summary
}

func executePlannedCommands(ctx context.Context, st store.Store, taskID string, runtimeName string, commands []string, now time.Time, timeout time.Duration, workDir string) ([]review.PermissionDecisionRecord, []review.SandboxRun, error) {
	if len(commands) == 0 {
		commands = newSandboxWorkspace(workDir).commandAllowlist()
	}
	var decisions []review.PermissionDecisionRecord
	var runs []review.SandboxRun
	var runtime sandboxrun.Runtime
	var cleanup func()
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()
	for index, command := range commands {
		suffix := fmt.Sprintf("%03d", index+1)
		decision := safetywrap.Decide(safetywrap.PlannedCommand{
			ID:       taskID + "-permission-" + suffix,
			TaskID:   taskID,
			ToolName: "workspace_exec",
			Command:  command,
			Now:      now,
		})
		if err := st.RecordPermissionDecision(ctx, decision); err != nil {
			return nil, nil, err
		}
		decisions = append(decisions, decision)
		runID := taskID + "-sandbox-" + suffix
		if decision.Blocked {
			runs = append(runs, review.SandboxRun{
				ID:             runID,
				TaskID:         taskID,
				Runtime:        runtimeName,
				Command:        command,
				Status:         sandboxrun.StatusSkipped,
				DurationMillis: 0,
				ErrorType:      sandboxrun.ErrorPermissionBlocked,
			})
			continue
		}
		if runtime == nil {
			var initRun *review.SandboxRun
			runtime, cleanup, initRun = runtimeForName(ctx, runtimeName, taskID, suffix, timeout, workDir)
			if initRun != nil {
				runs = append(runs, *initRun)
			}
		}
		runCtx := ctx
		cancel := func() {}
		if timeout > 0 {
			runCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		runs = append(runs, sandboxrun.Run(runCtx, runtime, taskID, runID, command, defaultMaxSandboxOutput))
		cancel()
	}
	return decisions, runs, nil
}

func countFindingRedactions(findings []review.Finding) int {
	count := 0
	for _, finding := range findings {
		count += redact.Text(finding.Evidence).Count
		count += redact.Text(finding.Recommendation).Count
	}
	return count
}

func runtimeForName(ctx context.Context, name string, taskID string, suffix string, timeout time.Duration, workDir string) (sandboxrun.Runtime, func(), *review.SandboxRun) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		normalized = "container"
	}
	if normalized == "fake" {
		return sandboxrun.FakeRuntime{RuntimeName: normalized}, nil, nil
	}
	rt, cleanup, err := newWorkspaceRuntime(ctx, normalized, taskID, timeout, workDir)
	if err != nil {
		run := review.SandboxRun{
			ID:             taskID + "-sandbox-init-" + suffix,
			TaskID:         taskID,
			Runtime:        normalized,
			Command:        "initialize workspace runtime",
			Status:         sandboxrun.StatusUnavailable,
			ErrorType:      sandboxrun.ErrorRuntimeUnavailable,
			StderrRedacted: redact.Text(err.Error()).Text,
		}
		return nil, cleanup, &run
	}
	return rt, cleanup, nil
}

func newWorkspaceRuntime(ctx context.Context, runtimeName string, taskID string, timeout time.Duration, workDir string) (sandboxrun.Runtime, func(), error) {
	workspace := newSandboxWorkspace(workDir)
	repoRoot, err := workspace.root()
	if err != nil {
		return nil, nil, err
	}
	var eng codeexecutor.Engine
	var closeFn func() error
	switch runtimeName {
	case "local":
		exec := localexec.New(
			localexec.WithWorkDir(repoRoot),
			localexec.WithTimeout(timeout),
			localexec.WithWorkspaceMode(localexec.WorkspaceModeTrustedLocal),
		)
		eng = exec.Engine()
	case "container":
		opts := []containerexec.Option{
			containerexec.WithContainerConfig(containerConfig()),
			containerexec.WithHostConfig(containerHostConfig()),
		}
		for _, mount := range containerBindMounts(repoRoot) {
			opts = append(opts, containerexec.WithBindMount(mount.HostPath, mount.ContainerPath, mount.Mode))
		}
		exec, err := containerexec.New(
			opts...,
		)
		if err != nil {
			return nil, nil, err
		}
		eng = exec.Engine()
		closeFn = exec.Close
	case "e2b":
		exec, err := e2b.NewWithContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		eng = exec.Engine()
		closeFn = exec.Close
	default:
		return nil, nil, fmt.Errorf("unsupported runtime %q", runtimeName)
	}
	if eng == nil || eng.Manager() == nil || eng.Runner() == nil {
		if closeFn != nil {
			_ = closeFn()
		}
		return nil, nil, fmt.Errorf("runtime %q did not expose a workspace engine", runtimeName)
	}
	ws, err := eng.Manager().CreateWorkspace(ctx, taskID, codeexecutor.WorkspacePolicy{
		Isolated:     runtimeName != "local",
		MaxDiskBytes: 512 << 20,
	})
	if err != nil {
		if closeFn != nil {
			_ = closeFn()
		}
		return nil, nil, err
	}
	cleanup := func() {
		_ = eng.Manager().Cleanup(context.Background(), ws)
		if closeFn != nil {
			_ = closeFn()
		}
	}
	if runtimeName != "local" {
		if err := eng.FS().StageDirectory(ctx, ws, repoRoot, codeexecutor.DirWork, codeexecutor.StageOptions{AllowMount: true}); err != nil {
			cleanup()
			return nil, nil, err
		}
	}
	return sandboxrun.WorkspaceRuntime{
		RuntimeName: runtimeName,
		Engine:      eng,
		Workspace:   ws,
		Cwd:         workspace.runtimeCwd(runtimeName),
		Timeout:     timeout,
		Env:         workspaceRuntimeEnv(runtimeName),
	}, cleanup, nil
}

func containerConfig() tcontainer.Config {
	return tcontainer.Config{
		Image:      containerSandboxImage,
		WorkingDir: "/",
		Cmd:        []string{"tail", "-f", "/dev/null"},
		Tty:        true,
		OpenStdin:  true,
	}
}

func containerHostConfig() tcontainer.HostConfig {
	return tcontainer.HostConfig{
		AutoRemove:  true,
		Privileged:  false,
		NetworkMode: "none",
	}
}

func containerBindMounts(repoRoot string) []bindMount {
	mounts := []bindMount{{
		HostPath:      repoRoot,
		ContainerPath: "/workspace",
		Mode:          "ro",
	}}
	if hostModCache := hostGoEnv("GOMODCACHE"); hostModCache != "" {
		mounts = append(mounts, bindMount{
			HostPath:      hostModCache,
			ContainerPath: containerGoModCache,
			Mode:          "ro",
		})
	}
	return mounts
}

func workspaceRuntimeEnv(runtimeName string) map[string]string {
	env := map[string]string{
		"GOPROXY":     os.Getenv("GOPROXY"),
		"GOSUMDB":     os.Getenv("GOSUMDB"),
		"GOTOOLCHAIN": os.Getenv("GOTOOLCHAIN"),
		"GOFLAGS":     os.Getenv("GOFLAGS"),
		"CGO_ENABLED": os.Getenv("CGO_ENABLED"),
	}
	if runtimeName == "local" {
		env["HOME"] = os.Getenv("HOME")
		env["GOCACHE"] = os.Getenv("GOCACHE")
		env["GOMODCACHE"] = os.Getenv("GOMODCACHE")
		env["GOPATH"] = os.Getenv("GOPATH")
	} else {
		env["HOME"] = "/tmp"
		env["GOPATH"] = "/go"
		env["GOMODCACHE"] = containerGoModCache
		env["GOCACHE"] = containerGoBuildCache
		setDefaultEnv(env, "GOTOOLCHAIN", "local")
	}
	return env
}

func hostGoEnv(key string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	output, err := exec.Command("go", "env", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func setDefaultEnv(env map[string]string, key string, value string) {
	if env[key] == "" {
		env[key] = value
	}
}

func repositoryRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			raw, err := os.ReadFile(filepath.Join(wd, "go.mod"))
			if err != nil {
				return "", err
			}
			if hasExactModuleDecl(string(raw), rootModuleDecl) {
				return wd, nil
			}
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			return "", fmt.Errorf("repository root not found from %s", wd)
		}
		wd = parent
	}
}

func hasExactModuleDecl(raw string, moduleDecl string) bool {
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == moduleDecl {
			return true
		}
	}
	return false
}

func statusFor(findings []review.Finding, runs []review.SandboxRun) string {
	for _, run := range runs {
		if run.Status == sandboxrun.StatusFailed || run.Status == sandboxrun.StatusUnavailable {
			return review.TaskStatusFailed
		}
	}
	for _, finding := range findings {
		if finding.Status == review.FindingStatusNeedsHumanReview {
			return review.TaskStatusFailed
		}
	}
	return review.TaskStatusPassed
}

func conclusionFor(status string, findings []review.Finding, runs []review.SandboxRun) string {
	if status == review.TaskStatusFailed {
		return "needs_human_review"
	}
	if len(findings) > 0 {
		return "findings_recorded"
	}
	if len(runs) == 0 {
		return "no_sandbox_run"
	}
	return "passed"
}

func artifactPaths(artifacts []review.ArtifactRecord) (string, string) {
	var jsonPath, mdPath string
	for _, artifact := range artifacts {
		switch artifact.Kind {
		case "json_report":
			jsonPath = artifact.Path
		case "markdown_report":
			mdPath = artifact.Path
		}
	}
	return jsonPath, mdPath
}
