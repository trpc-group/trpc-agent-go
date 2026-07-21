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
	"sync"
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
	failedTaskFinishTimeout = 3 * time.Second
	containerCPULimit       = int64(1_000_000_000)
	containerPIDsLimit      = int64(128)
	containerStorageLimit   = "512m"
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
	FixtureDir        string
	DiffFile          string
	FileList          string
	OutDir            string
	DBPath            string
	Model             string
	Runtime           string
	RepoPath          string
	AllowTrustedLocal bool
	SandboxTimeout    time.Duration
	Now               time.Time
	FinishedAt        time.Time
	Planner           Planner
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
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	var resolvedFinishedAt time.Time
	resolveFinishedAt := func() time.Time {
		if !resolvedFinishedAt.IsZero() {
			return resolvedFinishedAt
		}
		switch {
		case !opts.FinishedAt.IsZero():
			resolvedFinishedAt = opts.FinishedAt.UTC()
		case !opts.Now.IsZero():
			resolvedFinishedAt = opts.Now.UTC()
		default:
			resolvedFinishedAt = time.Now().UTC()
		}
		return resolvedFinishedAt
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
		finishCtx, cancel := failedTaskContext(ctx)
		defer cancel()
		if finishErr := st.FinishTask(finishCtx, task.ID, review.TaskStatusFailed, runErr.Error(), resolveFinishedAt()); finishErr != nil {
			return errors.Join(runErr, fmt.Errorf("finish failed task: %w", finishErr))
		}
		return runErr
	}

	files, err := parseInputFiles(rawDiff, input.FileList)
	if err != nil {
		return Result{}, failTask(err)
	}
	changedFilesJSON, err := json.Marshal(redact.DiffFiles(files))
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
	if err := validateRuntimePolicy(opts.Runtime, opts.AllowTrustedLocal); err != nil {
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

	var decisions []review.PermissionDecisionRecord
	var runs []review.SandboxRun
	if sandboxValidationAvailable(input) {
		decisions, runs, err = executePlannedCommands(ctx, st, task.ID, opts.Runtime, opts.AllowTrustedLocal, plan.Commands, now, opts.SandboxTimeout, input.WorkDir)
		if err != nil {
			return Result{}, failTask(err)
		}
		if err := recordSandboxRuns(ctx, st, runs); err != nil {
			return Result{}, failTask(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, failTask(err)
	}

	finishedAt := resolveFinishedAt()
	metrics := report.BuildMetrics(task.ID, task.StartedAt, findings, runs, decisions, redactedDiff.Count+countFindingRedactions(findings))
	metrics.TotalDurationMillis = finishedAt.Sub(task.StartedAt).Milliseconds()
	if metrics.TotalDurationMillis < 0 {
		metrics.TotalDurationMillis = 0
	}
	task.Status = statusFor(findings, runs)
	task.FinishedAt = &finishedAt
	conclusion := conclusionFor(task.Status, findings, runs)
	if !sandboxValidationAvailable(input) {
		conclusion = "no_sandbox_run"
	}
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
	artifacts, err := report.Write(opts.OutDir, r, finishedAt)
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
	if err := st.FinishTask(ctx, task.ID, task.Status, "", finishedAt); err != nil {
		return Result{}, failTask(err)
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
	if !sandboxValidationAvailable(input) {
		summary += " Sandbox validation was skipped because this input has no reviewed workspace."
	}
	if input.Type == review.InputTypeFileList {
		if input.RepoPath != "" {
			return summary + fmt.Sprintf(" File-list input supplies path context only for repository %s; content-based deterministic rules require diff input.", input.RepoPath)
		}
		return summary + " File-list input supplies path context only; content-based deterministic rules require diff input."
	}
	return summary
}

func sandboxValidationAvailable(input inputsource.Source) bool {
	switch input.Type {
	case review.InputTypeDiffFile, review.InputTypeFileList:
		return strings.TrimSpace(input.WorkDir) != ""
	default:
		return true
	}
}

func recordSandboxRuns(ctx context.Context, st store.Store, runs []review.SandboxRun) error {
	for _, run := range runs {
		if err := st.RecordSandboxRun(ctx, run); err != nil {
			if ctx.Err() == nil {
				return err
			}
			persistCtx, cancel := failedTaskContext(ctx)
			retryErr := st.RecordSandboxRun(persistCtx, run)
			cancel()
			if retryErr != nil {
				return retryErr
			}
		}
	}
	return nil
}

type runtimeFactory func(context.Context, string, string, string, time.Duration, string, bool) (sandboxrun.Runtime, func(), *review.SandboxRun)

func executePlannedCommands(ctx context.Context, st store.Store, taskID string, runtimeName string, allowTrustedLocal bool, commands []string, now time.Time, timeout time.Duration, workDir string) ([]review.PermissionDecisionRecord, []review.SandboxRun, error) {
	return executePlannedCommandsWithFactory(ctx, st, taskID, runtimeName, allowTrustedLocal, commands, now, timeout, workDir, runtimeForName)
}

func executePlannedCommandsWithFactory(ctx context.Context, st store.Store, taskID string, runtimeName string, allowTrustedLocal bool, commands []string, now time.Time, timeout time.Duration, workDir string, factory runtimeFactory) ([]review.PermissionDecisionRecord, []review.SandboxRun, error) {
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
			runtime, cleanup, initRun = factory(ctx, runtimeName, taskID, suffix, timeout, workDir, allowTrustedLocal)
			if initRun != nil {
				runs = append(runs, *initRun)
			}
		}
		runCtx := ctx
		cancel := func() {}
		if timeout > 0 {
			runCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		run := sandboxrun.Run(runCtx, runtime, taskID, runID, command, defaultMaxSandboxOutput)
		runs = append(runs, run)
		cancel()
		if run.ErrorType == sandboxrun.ErrorTimeout || run.ErrorType == sandboxrun.ErrorCanceled || ctx.Err() != nil {
			break
		}
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

func runtimeForName(ctx context.Context, name string, taskID string, suffix string, timeout time.Duration, workDir string, allowTrustedLocal bool) (sandboxrun.Runtime, func(), *review.SandboxRun) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		normalized = "container"
	}
	if normalized == "fake" {
		return sandboxrun.FakeRuntime{RuntimeName: normalized}, nil, nil
	}
	rt, cleanup, err := newWorkspaceRuntime(ctx, normalized, taskID, timeout, workDir, allowTrustedLocal)
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

func newWorkspaceRuntime(ctx context.Context, runtimeName string, taskID string, timeout time.Duration, workDir string, allowTrustedLocal bool) (sandboxrun.Runtime, func(), error) {
	workspace := newSandboxWorkspace(workDir)
	repoRoot, err := workspace.root()
	if err != nil {
		return nil, nil, err
	}
	var eng codeexecutor.Engine
	var closeFn func() error
	switch runtimeName {
	case "local":
		if err := validateRuntimePolicy(runtimeName, allowTrustedLocal); err != nil {
			return nil, nil, err
		}
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
	snapshotCleanup, err := stageReviewWorkspace(ctx, eng.FS(), ws, runtimeName, repoRoot)
	if err != nil {
		_ = eng.Manager().Cleanup(context.Background(), ws)
		if closeFn != nil {
			_ = closeFn()
		}
		return nil, nil, err
	}
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			_ = eng.Manager().Cleanup(context.Background(), ws)
			if closeFn != nil {
				_ = closeFn()
			}
			if snapshotCleanup != nil {
				snapshotCleanup()
			}
		})
	}
	return sandboxrun.WorkspaceRuntime{
		RuntimeName: runtimeName,
		Engine:      eng,
		Workspace:   ws,
		Cwd:         workspace.runtimeCwd(runtimeName),
		Timeout:     timeout,
		Env:         workspaceRuntimeEnv(runtimeName),
		TerminateFn: func(context.Context) { cleanup() },
	}, cleanup, nil
}

func stageReviewWorkspace(ctx context.Context, fs codeexecutor.WorkspaceFS, ws codeexecutor.Workspace, runtimeName string, repoRoot string) (func(), error) {
	if runtimeName == "local" {
		return nil, nil
	}
	stageRoot := repoRoot
	var snapshotCleanup func()
	if runtimeName == "e2b" {
		var err error
		stageRoot, snapshotCleanup, err = buildReviewSnapshot(ctx, repoRoot)
		if err != nil {
			return nil, err
		}
	}
	if err := fs.StageDirectory(ctx, ws, stageRoot, codeexecutor.DirWork, codeexecutor.StageOptions{AllowMount: true}); err != nil {
		if snapshotCleanup != nil {
			snapshotCleanup()
		}
		return nil, err
	}
	return snapshotCleanup, nil
}

func buildReviewSnapshot(ctx context.Context, repoRoot string) (string, func(), error) {
	files, err := trackedReviewFiles(ctx, repoRoot)
	if err != nil {
		return "", nil, err
	}
	snapshot, err := os.MkdirTemp("", "review-agent-snapshot-")
	if err != nil {
		return "", nil, fmt.Errorf("create review snapshot: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(snapshot) }
	for _, file := range files {
		if excludedReviewSnapshotPath(file) {
			continue
		}
		rel := filepath.FromSlash(file)
		clean := filepath.Clean(rel)
		if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			cleanup()
			return "", nil, fmt.Errorf("unsafe review snapshot path %q", file)
		}
		src := filepath.Join(repoRoot, clean)
		info, err := os.Lstat(src)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			cleanup()
			return "", nil, fmt.Errorf("stat review snapshot file %s: %w", file, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("read review snapshot file %s: %w", file, err)
		}
		dest := filepath.Join(snapshot, clean)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("create review snapshot directory: %w", err)
		}
		if err := os.WriteFile(dest, data, info.Mode().Perm()); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("write review snapshot file %s: %w", file, err)
		}
	}
	return snapshot, cleanup, nil
}

func trackedReviewFiles(ctx context.Context, repoRoot string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list tracked review files: %w", err)
	}
	parts := bytes.Split(raw, []byte{0})
	files := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		files = append(files, filepath.ToSlash(string(part)))
	}
	sort.Strings(files)
	return files, nil
}

func excludedReviewSnapshotPath(file string) bool {
	for _, part := range strings.Split(filepath.ToSlash(file), "/") {
		if part == ".git" {
			return true
		}
	}
	base := strings.ToLower(filepath.Base(file))
	return base == ".env" || strings.HasPrefix(base, ".env.") ||
		base == "review_agent.db" || base == "review_agent.db.lock" ||
		strings.HasPrefix(base, "review_report_")
}

func validateRuntimePolicy(runtimeName string, allowTrustedLocal bool) error {
	normalized := strings.ToLower(strings.TrimSpace(runtimeName))
	if normalized != "local" || allowTrustedLocal {
		return nil
	}
	return fmt.Errorf("runtime %q is disabled for untrusted review input; rerun only for explicitly trusted input with AllowTrustedLocal or --allow-trusted-local", normalized)
}

func failedTaskContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), failedTaskFinishTimeout)
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
	pidsLimit := containerPIDsLimit
	return tcontainer.HostConfig{
		AutoRemove:  true,
		Privileged:  false,
		NetworkMode: "none",
		Resources: tcontainer.Resources{
			Memory:    int64(512 << 20),
			NanoCPUs:  containerCPULimit,
			PidsLimit: &pidsLimit,
		},
		StorageOpt: map[string]string{"size": containerStorageLimit},
	}
}

func containerBindMounts(repoRoot string) []bindMount {
	return []bindMount{{
		HostPath:      repoRoot,
		ContainerPath: "/workspace",
		Mode:          "ro",
	}}
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
