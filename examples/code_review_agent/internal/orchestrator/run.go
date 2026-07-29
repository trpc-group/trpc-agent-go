//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package orchestrator

import (
	"bufio"
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
	containerUser           = "65532:65532"
	containerSandboxImage   = "golang:1.24"
	containerGoBuildCache   = "/tmp/go-build"
	containerGoModCache     = "/go/pkg/mod"
	dependencyPrepTimeout   = 2 * time.Minute
	dependencyPrepMaxBytes  = int64(512 << 20)
	dependencyPrepMaxOutput = 32 << 10
	reviewSnapshotMaxBytes  = int64(512 << 20)
	maxReviewSnapshotFiles  = 100_000
	reviewAgentModuleDir    = "examples/code_review_agent"
	rootModuleDecl          = "module trpc.group/trpc-go/trpc-agent-go"
)

var workspaceCleanupTimeout = 2 * time.Second

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

type reviewSnapshotFile struct {
	Path    string
	Tracked bool
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
		plan.Commands = redactStrings(uniqueModelCommands(modelPlan.Commands))
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
		files = append(files, redact.Text(file.NewPath).Text)
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
	allowed := allowedCommandsByCanonical(workDir)
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

func uniqueModelCommands(commands []string) []string {
	seen := make(map[string]struct{}, len(commands))
	out := make([]string, 0, len(commands))
	for _, command := range commands {
		canonical := canonicalCommand(command)
		if canonical == "" {
			continue
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	return out
}

func allowedCommandsByCanonical(workDir string) map[string]string {
	allowlist := newSandboxWorkspace(workDir).commandAllowlist()
	allowed := make(map[string]string, len(allowlist))
	for _, command := range allowlist {
		allowed[canonicalCommand(command)] = command
	}
	return allowed
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

func (ws sandboxWorkspace) dependencySubdir() string {
	if ws.hasSelectedRepo() {
		return ""
	}
	return reviewAgentModuleDir
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
	allowedCommands := allowedCommandsByCanonical(workDir)
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
		canonical := canonicalCommand(command)
		allowedCommand, allowed := allowedCommands[canonical]
		if !allowed {
			decision := allowlistRejectedDecision(taskID, suffix, command, now)
			if err := st.RecordPermissionDecision(ctx, decision); err != nil {
				return nil, nil, err
			}
			decisions = append(decisions, decision)
			runs = append(runs, review.SandboxRun{
				ID:             taskID + "-sandbox-" + suffix,
				TaskID:         taskID,
				Runtime:        runtimeName,
				Command:        redact.Text(command).Text,
				Status:         sandboxrun.StatusSkipped,
				DurationMillis: 0,
				ErrorType:      sandboxrun.ErrorPermissionBlocked,
			})
			continue
		}
		command = allowedCommand
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

func allowlistRejectedDecision(taskID string, suffix string, command string, now time.Time) review.PermissionDecisionRecord {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return review.PermissionDecisionRecord{
		ID:              taskID + "-permission-" + suffix,
		TaskID:          taskID,
		ToolName:        "workspace_exec",
		Command:         redact.Text(command).Text,
		FrameworkAction: safetywrap.ActionDeny,
		SafetyDecision:  safetywrap.DecisionDeny,
		RiskLevel:       safetywrap.RiskHigh,
		RuleID:          "sandbox.command_not_allowlisted",
		Reason:          "Command is not in the sandbox allowlist and was not executed.",
		Blocked:         true,
		CreatedAt:       now.UTC(),
	}
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
	snapshotCleanup, err := stageReviewWorkspace(ctx, eng.FS(), ws, runtimeName, repoRoot, workspace.dependencySubdir())
	if err != nil {
		_ = eng.Manager().Cleanup(context.Background(), ws)
		if closeFn != nil {
			_ = closeFn()
		}
		return nil, nil, err
	}
	cleanup := newWorkspaceCleanup(eng.Manager(), ws, closeFn, snapshotCleanup)
	return sandboxrun.WorkspaceRuntime{
		RuntimeName: runtimeName,
		Engine:      eng,
		Workspace:   ws,
		Cwd:         workspace.runtimeCwd(runtimeName),
		Timeout:     timeout,
		OutputLimit: defaultMaxSandboxOutput,
		Env:         workspaceRuntimeEnv(runtimeName),
		TerminateFn: cleanup,
	}, func() { cleanup(context.Background()) }, nil
}

func newWorkspaceCleanup(manager codeexecutor.WorkspaceManager, ws codeexecutor.Workspace, closeFn func() error, snapshotCleanup func()) func(context.Context) {
	var cleanupOnce sync.Once
	cleanupDone := make(chan struct{})
	return func(ctx context.Context) {
		cleanupOnce.Do(func() {
			go func() {
				defer close(cleanupDone)
				cleanupWorkspaceResources(ctx, manager, ws, closeFn, snapshotCleanup)
			}()
		})
		waitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workspaceCleanupTimeout)
		defer cancel()
		select {
		case <-cleanupDone:
		case <-waitCtx.Done():
		}
	}
}

func cleanupWorkspaceResources(ctx context.Context, manager codeexecutor.WorkspaceManager, ws codeexecutor.Workspace, closeFn func() error, snapshotCleanup func()) {
	cleanupStep := func(fn func(context.Context)) bool {
		stepCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workspaceCleanupTimeout)
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			fn(stepCtx)
		}()
		select {
		case <-done:
			return true
		case <-stepCtx.Done():
			return false
		}
	}
	if manager != nil {
		// Continue best-effort cleanup if a backend ignores its context; otherwise
		// one stalled manager would retain every later resource indefinitely.
		_ = cleanupStep(func(stepCtx context.Context) { _ = manager.Cleanup(stepCtx, ws) })
	}
	if closeFn != nil {
		_ = cleanupStep(func(context.Context) { _ = closeFn() })
	}
	if snapshotCleanup != nil {
		_ = cleanupStep(func(context.Context) { snapshotCleanup() })
	}
}

func stageReviewWorkspace(ctx context.Context, fs codeexecutor.WorkspaceFS, ws codeexecutor.Workspace, runtimeName string, repoRoot string, dependencySubdir string) (func(), error) {
	if runtimeName == "local" {
		return nil, nil
	}
	stageRoot, snapshotCleanup, err := buildReviewSnapshot(ctx, repoRoot)
	if err != nil {
		return nil, err
	}
	if err := prepareSandboxDependencies(ctx, stageRoot, dependencySubdir); err != nil {
		snapshotCleanup()
		return nil, err
	}
	if err := lockReviewSnapshotSource(stageRoot, []string{
		filepath.Join(stageRoot, ".gopath"),
		filepath.Join(stageRoot, ".gomodcache"),
		filepath.Join(stageRoot, ".gocache"),
	}); err != nil {
		snapshotCleanup()
		return nil, err
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
	return buildReviewSnapshotWithLimit(ctx, repoRoot, reviewSnapshotMaxBytes)
}

func buildReviewSnapshotWithLimit(ctx context.Context, repoRoot string, maxBytes int64) (string, func(), error) {
	return buildReviewSnapshotWithLimits(ctx, repoRoot, maxBytes, maxReviewSnapshotFiles)
}

func buildReviewSnapshotWithLimits(ctx context.Context, repoRoot string, maxBytes int64, maxFiles int) (string, func(), error) {
	files, err := trackedReviewFiles(ctx, repoRoot, maxFiles)
	if err != nil {
		return "", nil, err
	}
	snapshot, err := os.MkdirTemp("", "review-agent-snapshot-")
	if err != nil {
		return "", nil, fmt.Errorf("create review snapshot: %w", err)
	}
	cleanup := func() { cleanupReviewSnapshot(snapshot) }
	var snapshotBytes int64
	for _, file := range files {
		if excludedReviewSnapshotPath(file.Path) && (!file.Tracked || sensitiveReviewSnapshotPath(file.Path)) {
			continue
		}
		rel := filepath.FromSlash(file.Path)
		clean := filepath.Clean(rel)
		if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			cleanup()
			return "", nil, fmt.Errorf("unsafe review snapshot path %q", file.Path)
		}
		src := filepath.Join(repoRoot, clean)
		info, err := os.Lstat(src)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			cleanup()
			return "", nil, fmt.Errorf("stat review snapshot file %s: %w", file.Path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(src)
			cleanup()
			if readErr != nil {
				return "", nil, fmt.Errorf("read review snapshot symlink %s: %w", file.Path, readErr)
			}
			return "", nil, fmt.Errorf("unsupported review snapshot symlink %q -> %q", file.Path, target)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if maxBytes >= 0 && info.Size() > maxBytes-snapshotBytes {
			cleanup()
			return "", nil, fmt.Errorf("review snapshot exceeds %d bytes at %s", maxBytes, file.Path)
		}
		dest := filepath.Join(snapshot, clean)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("create review snapshot directory: %w", err)
		}
		written, err := copyReviewSnapshotFile(src, dest, info.Mode().Perm(), maxBytes-snapshotBytes)
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("copy review snapshot file %s: %w", file.Path, err)
		}
		snapshotBytes += written
	}
	return snapshot, cleanup, nil
}

func copyReviewSnapshotFile(src string, dest string, mode os.FileMode, maxBytes int64) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	limited := io.Reader(in)
	if maxBytes >= 0 {
		limited = io.LimitReader(in, maxBytes+1)
	}
	written, err := io.Copy(out, limited)
	if err != nil {
		return written, err
	}
	if maxBytes >= 0 && written > maxBytes {
		_ = os.Remove(dest)
		return written, fmt.Errorf("review snapshot exceeds %d bytes", maxBytes)
	}
	return written, nil
}

func cleanupReviewSnapshot(root string) {
	if root == "" {
		return
	}
	_ = restoreReviewSnapshotPermissions(root)
	_ = os.RemoveAll(root)
}

func restoreReviewSnapshotPermissions(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if entry.IsDir() {
			mode |= 0o700
		} else {
			mode |= 0o600
		}
		return os.Chmod(path, mode)
	})
}

func prepareSandboxDependencies(ctx context.Context, snapshotRoot string, dependencySubdir string) error {
	moduleDir := filepath.Join(snapshotRoot, filepath.FromSlash(dependencySubdir))
	cacheRoot := filepath.Join(snapshotRoot, ".gopath")
	modCache := filepath.Join(snapshotRoot, ".gomodcache")
	buildCache := filepath.Join(snapshotRoot, ".gocache")
	homeDir := filepath.Join(snapshotRoot, ".home")
	tmpDir := filepath.Join(snapshotRoot, ".tmp")
	for _, dir := range []string{cacheRoot, modCache, buildCache, homeDir, tmpDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create sandbox dependency cache: %w", err)
		}
	}
	_, moduleErr := os.Stat(filepath.Join(moduleDir, "go.mod"))
	if errors.Is(moduleErr, os.ErrNotExist) {
		return makeSandboxCachesWritable(snapshotRoot, []string{cacheRoot, modCache, buildCache})
	}
	if moduleErr != nil {
		return fmt.Errorf("stat sandbox go.mod: %w", moduleErr)
	}
	prepCtx, cancel := context.WithTimeout(ctx, dependencyPrepTimeout)
	defer cancel()
	cmd := exec.CommandContext(prepCtx, "go", "mod", "download")
	cmd.Dir = moduleDir
	cmd.Env = sandboxDependencyEnv(snapshotRoot, cacheRoot, modCache, buildCache, homeDir, tmpDir)
	var output limitedBuffer
	output.limit = dependencyPrepMaxOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if errors.Is(prepCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("prepare sandbox dependencies timed out after %s", dependencyPrepTimeout)
		}
		return fmt.Errorf("prepare sandbox dependencies: %w: %s", err, redact.Text(strings.TrimSpace(output.String())).Text)
	}
	if err := ensureDependencyCacheWithinLimit([]string{cacheRoot, modCache, buildCache}, dependencyPrepMaxBytes); err != nil {
		return err
	}
	if err := makeSandboxCachesWritable(snapshotRoot, []string{cacheRoot, modCache, buildCache}); err != nil {
		return err
	}
	return nil
}

func makeSandboxCachesWritable(snapshotRoot string, roots []string) error {
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			mode := os.FileMode(0o666)
			if entry.IsDir() {
				mode = 0o777
			}
			if err := os.Chmod(path, mode); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("make sandbox cache writable %s: %w", filepath.Join(snapshotRoot, filepath.Base(root)), err)
		}
	}
	return nil
}

func lockReviewSnapshotSource(root string, writableRoots []string) error {
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		for _, writableRoot := range writableRoots {
			if path == writableRoot || strings.HasPrefix(path, writableRoot+string(os.PathSeparator)) {
				return nil
			}
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, info.Mode().Perm()&^0o222)
		}
		return os.Chmod(path, info.Mode().Perm()&^0o222)
	})
	if err != nil {
		return fmt.Errorf("make review snapshot source read-only: %w", err)
	}
	return nil
}

func sandboxDependencyEnv(snapshotRoot string, cacheRoot string, modCache string, buildCache string, homeDir string, tmpDir string) []string {
	env := make([]string, 0, 18)
	addHostEnv := func(key string) {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	for _, key := range []string{"PATH", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT"} {
		addHostEnv(key)
	}
	env = append(env,
		"HOME="+homeDir,
		"USERPROFILE="+homeDir,
		"TMPDIR="+tmpDir,
		"TEMP="+tmpDir,
		"TMP="+tmpDir,
		"GOPATH="+cacheRoot,
		"GOMODCACHE="+modCache,
		"GOCACHE="+buildCache,
		"GOPROXY=https://proxy.golang.org,direct",
		"GOSUMDB=sum.golang.org",
		"GONOSUMDB=",
		"GOPRIVATE=",
		"GOTOOLCHAIN=local",
		"GOFLAGS=-mod=mod",
	)
	_ = snapshotRoot
	return env
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) <= remaining {
			_, _ = b.buf.Write(p)
		} else {
			_, _ = b.buf.Write(p[:remaining])
			b.truncated = true
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	if !b.truncated {
		return b.buf.String()
	}
	return b.buf.String() + "\n[TRUNCATED]"
}

func ensureDependencyCacheWithinLimit(roots []string, limit int64) error {
	var total int64
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
			if limit > 0 && total > limit {
				return fmt.Errorf("sandbox dependency cache exceeded %d bytes", limit)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("measure sandbox dependency cache: %w", err)
		}
	}
	return nil
}

func trackedReviewFiles(ctx context.Context, repoRoot string, maxFiles int) ([]reviewSnapshotFile, error) {
	files := make([]reviewSnapshotFile, 0, minInt(maxFiles, 1024))
	if err := appendReviewFiles(ctx, repoRoot, maxFiles, true, &files, "ls-files", "-z", "--cached"); err != nil {
		return nil, fmt.Errorf("list tracked review files: %w", err)
	}
	if err := appendReviewFiles(ctx, repoRoot, maxFiles, false, &files, "ls-files", "-z", "--others", "--exclude-standard"); err != nil {
		return nil, fmt.Errorf("list untracked review files: %w", err)
	}
	return files, nil
}

func appendReviewFiles(ctx context.Context, repoRoot string, maxFiles int, tracked bool, files *[]reviewSnapshotFile, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repoRoot}, args...)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	reader := bufio.NewReader(stdout)
	for {
		part, readErr := reader.ReadBytes(0)
		if len(part) > 0 {
			part = bytes.TrimSuffix(part, []byte{0})
			if len(part) > 0 {
				if maxFiles >= 0 && len(*files) >= maxFiles {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					return fmt.Errorf("review snapshot file count exceeded %d", maxFiles)
				}
				*files = append(*files, reviewSnapshotFile{Path: filepath.ToSlash(string(part)), Tracked: tracked})
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return readErr
		}
	}
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

func minInt(a int, b int) int {
	if a >= 0 && a < b {
		return a
	}
	return b
}

func excludedReviewSnapshotPath(file string) bool {
	for _, part := range strings.Split(filepath.ToSlash(file), "/") {
		if part == ".git" {
			return true
		}
	}
	base := strings.ToLower(filepath.Base(file))
	return sensitiveReviewSnapshotPath(file) ||
		base == "review_agent.db" || base == "review_agent.db.lock" ||
		strings.HasPrefix(base, "review_report_")
}

func sensitiveReviewSnapshotPath(file string) bool {
	base := strings.ToLower(filepath.Base(file))
	return base == ".env" || strings.HasPrefix(base, ".env.")
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
		User:       containerUser,
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
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges:true"},
		Resources: tcontainer.Resources{
			Memory:    int64(512 << 20),
			NanoCPUs:  containerCPULimit,
			PidsLimit: &pidsLimit,
		},
		StorageOpt: map[string]string{"size": containerStorageLimit},
	}
}

func containerBindMounts(repoRoot string) []bindMount {
	_ = repoRoot
	return nil
}

func workspaceRuntimeEnv(runtimeName string) map[string]string {
	if runtimeName == "local" {
		env := map[string]string{
			"GOPROXY":     os.Getenv("GOPROXY"),
			"GOSUMDB":     os.Getenv("GOSUMDB"),
			"GOTOOLCHAIN": os.Getenv("GOTOOLCHAIN"),
			"GOFLAGS":     os.Getenv("GOFLAGS"),
			"CGO_ENABLED": os.Getenv("CGO_ENABLED"),
		}
		env["HOME"] = os.Getenv("HOME")
		env["GOCACHE"] = os.Getenv("GOCACHE")
		env["GOMODCACHE"] = os.Getenv("GOMODCACHE")
		env["GOPATH"] = os.Getenv("GOPATH")
		return env
	}
	return map[string]string{
		"HOME":        "/tmp",
		"GOPATH":      "/go",
		"GOMODCACHE":  containerGoModCache,
		"GOCACHE":     containerGoBuildCache,
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
		"GOFLAGS":     "-mod=mod",
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
	executedRuns := 0
	for _, run := range runs {
		if run.Status == sandboxrun.StatusFailed || run.Status == sandboxrun.StatusUnavailable {
			return review.TaskStatusFailed
		}
		if run.Status != sandboxrun.StatusSkipped {
			executedRuns++
		}
	}
	if len(runs) > 0 && executedRuns == 0 {
		return review.TaskStatusFailed
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
	if executedSandboxRunCount(runs) == 0 {
		return "no_sandbox_run"
	}
	return "passed"
}

func executedSandboxRunCount(runs []review.SandboxRun) int {
	count := 0
	for _, run := range runs {
		if run.Status != sandboxrun.StatusSkipped && run.Status != sandboxrun.StatusUnavailable {
			count++
		}
	}
	return count
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
