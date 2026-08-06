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
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

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
	defaultMaxSandboxOutput       = 4096
	defaultSkillName              = "code-review"
	defaultSandboxTimeout         = 30 * time.Second
	failedTaskFinishTimeout       = 3 * time.Second
	containerCPULimit             = int64(1_000_000_000)
	containerPIDsLimit            = int64(128)
	containerStorageLimit         = "512m"
	containerUser                 = "65532:65532"
	containerSandboxImage         = "golang:1.24.6"
	containerGoBuildCache         = "/tmp/go-build"
	containerGoModCache           = "/go/pkg/mod"
	dependencyPrepTimeout         = 2 * time.Minute
	dependencyPrepMaxBytes        = int64(512 << 20)
	dependencyPrepMaxOutput       = 32 << 10
	reviewSnapshotMaxBytes        = int64(512 << 20)
	maxReviewSnapshotFiles        = 100_000
	maxModelPlanningFiles         = 256
	maxModelPlanningFileBytes     = 16 << 10
	maxModelPlanningRequestBytes  = 64 << 10
	maxModelPlanCommands          = 32
	maxModelCommandBytes          = 4 << 10
	maxGoMetadataBytes            = int64(1 << 20)
	maxSnapshotPathspecBatchPaths = 256
	maxSnapshotPathspecBatchBytes = 32 << 10
	reviewAgentModuleDir          = "examples/code_review_agent"
	rootModuleDecl                = "module trpc.group/trpc-go/trpc-agent-go"
)

var workspaceCleanupTimeout = 2 * time.Second

var defaultSandboxCommands = []string{
	"go test ./...",
	"go vet ./...",
	"go test ./skills/code-review/scripts",
	"go test ./internal/rules",
}

type sandboxDependencyMode string

const (
	dependencyModeNone            sandboxDependencyMode = "none"
	dependencyModeVendor          sandboxDependencyMode = "vendor"
	dependencyModePreProvisioned  sandboxDependencyMode = "pre-provisioned"
	dependencyModeHostPreparation sandboxDependencyMode = "host-preparation"
)

type goVersion struct {
	major          int
	minor          int
	patch          int
	patchSpecified bool
}

// Options configures one review run.
type Options struct {
	FixtureDir                  string
	DiffFile                    string
	FileList                    string
	OutDir                      string
	DBPath                      string
	Model                       string
	Runtime                     string
	RepoPath                    string
	AllowTrustedLocal           bool
	AllowTrustedHostPreparation bool
	AllowTrustedRemote          bool
	SandboxTimeout              time.Duration
	Now                         time.Time
	FinishedAt                  time.Time
	Planner                     Planner
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
	Gitlink bool
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
		commands, err := uniqueModelCommands(modelPlan.Commands)
		if err != nil {
			return review.ReviewPlan{}, fmt.Errorf("validate model commands: %w", err)
		}
		plan.Commands = redactStrings(commands)
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
	if len(body) > maxModelPlanningRequestBytes {
		return modelPlanEnvelope{}, fmt.Errorf("model planning request exceeded %d bytes", maxModelPlanningRequestBytes)
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
	files, fileCount, filesTruncated := boundedPlanningFiles(req.Files)
	payload := map[string]any{
		"skill":                   req.Skill,
		"runtime":                 req.Runtime,
		"changed_files":           files,
		"changed_file_count":      fileCount,
		"changed_files_truncated": filesTruncated,
		"allowed_commands":        newSandboxWorkspace(req.WorkDir).commandAllowlist(),
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

func boundedPlanningFiles(files []review.DiffFile) ([]string, int, bool) {
	sample := make([]string, 0, minInt(len(files), maxModelPlanningFiles))
	encodedBytes := 2 // The opening and closing brackets of the JSON array.
	truncated := false
	for _, file := range files {
		path := redact.Text(file.NewPath).Text
		encodedPath, err := json.Marshal(path)
		if err != nil {
			truncated = true
			continue
		}
		candidateBytes := encodedBytes + len(encodedPath)
		if len(sample) > 0 {
			candidateBytes++ // The comma between JSON array elements.
		}
		if len(sample) >= maxModelPlanningFiles || candidateBytes > maxModelPlanningFileBytes {
			truncated = true
			continue
		}
		sample = append(sample, path)
		encodedBytes = candidateBytes
	}
	sort.Strings(sample)
	return sample, len(files), truncated
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

func uniqueModelCommands(commands []string) ([]string, error) {
	if len(commands) > maxModelPlanCommands {
		return nil, fmt.Errorf("model command count exceeded %d", maxModelPlanCommands)
	}
	seen := make(map[string]struct{}, len(commands))
	out := make([]string, 0, len(commands))
	for _, command := range commands {
		canonical := canonicalCommand(command)
		if canonical == "" {
			continue
		}
		if len(canonical) > maxModelCommandBytes {
			return nil, fmt.Errorf("model command exceeded %d bytes", maxModelCommandBytes)
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	return out, nil
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
	if err := validateRemoteRuntimePolicy(opts.Runtime, opts.AllowTrustedRemote); err != nil {
		return Result{}, failTask(err)
	}
	findings := rules.Evaluate(files)
	if err := st.SaveFindings(ctx, task.ID, findings); err != nil {
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

	var decisions []review.PermissionDecisionRecord
	var runs []review.SandboxRun
	if sandboxValidationAvailable(input) {
		snapshotExclusionPaths, exclusionErr := reviewSnapshotArtifactPaths(input.WorkDir, opts.DBPath, opts.OutDir)
		if exclusionErr != nil {
			return Result{}, failTask(fmt.Errorf("resolve review snapshot artifact paths: %w", exclusionErr))
		}
		decisions, runs, err = executePlannedCommandsWithSnapshotPathsAndExclusions(ctx, st, task.ID, opts.Runtime, opts.AllowTrustedLocal, opts.AllowTrustedHostPreparation, opts.AllowTrustedRemote, plan.Commands, now, opts.SandboxTimeout, input.WorkDir, snapshotUntrackedPaths(input, files), snapshotExclusionPaths)
		if err != nil {
			if persistErr := recordSandboxRuns(ctx, st, runs); persistErr != nil {
				err = errors.Join(err, fmt.Errorf("record sandbox runs after execution failure: %w", persistErr))
			}
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
	jsonPath, mdPath := artifactPaths(artifacts)
	metricsJSON, _ := json.Marshal(metrics)
	// Persist report metadata and the terminal task state together; if this
	// mutation fails, remove the already-written files before recording failure.
	if err := st.FinalizeTask(ctx, task.ID, task.Status, finishedAt, artifacts, store.ReportRecord{
		TaskID:       task.ID,
		JSONPath:     jsonPath,
		MarkdownPath: mdPath,
		Conclusion:   conclusion,
		MetricsJSON:  string(metricsJSON),
	}); err != nil {
		cleanupErr := removeReportArtifacts(artifacts)
		return Result{}, failTask(errors.Join(err, cleanupErr))
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

func snapshotUntrackedPaths(input inputsource.Source, files []review.DiffFile) []string {
	if input.Type == review.InputTypeRepo {
		return nil
	}
	paths := make(map[string]struct{}, len(files)*2+len(input.FileList))
	add := func(path string) {
		path = filepath.ToSlash(path)
		if path == "" || path == "/dev/null" {
			return
		}
		paths[path] = struct{}{}
	}
	for _, path := range input.FileList {
		add(path)
	}
	for _, file := range files {
		add(file.OldPath)
		add(file.NewPath)
	}
	selected := make([]string, 0, len(paths))
	for path := range paths {
		selected = append(selected, path)
	}
	sort.Strings(selected)
	return selected
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

type runtimeFactory func(context.Context, string, string, string, time.Duration, string, bool, bool) (sandboxrun.Runtime, func(), *review.SandboxRun)

func executePlannedCommands(ctx context.Context, st store.Store, taskID string, runtimeName string, allowTrustedLocal bool, allowTrustedHostPreparation bool, commands []string, now time.Time, timeout time.Duration, workDir string) ([]review.PermissionDecisionRecord, []review.SandboxRun, error) {
	return executePlannedCommandsWithSnapshotPaths(ctx, st, taskID, runtimeName, allowTrustedLocal, allowTrustedHostPreparation, false, commands, now, timeout, workDir, nil)
}

func executePlannedCommandsWithSnapshotPaths(ctx context.Context, st store.Store, taskID string, runtimeName string, allowTrustedLocal bool, allowTrustedHostPreparation bool, allowTrustedRemote bool, commands []string, now time.Time, timeout time.Duration, workDir string, snapshotPaths []string) ([]review.PermissionDecisionRecord, []review.SandboxRun, error) {
	return executePlannedCommandsWithSnapshotPathsAndExclusions(ctx, st, taskID, runtimeName, allowTrustedLocal, allowTrustedHostPreparation, allowTrustedRemote, commands, now, timeout, workDir, snapshotPaths, nil)
}

func executePlannedCommandsWithSnapshotPathsAndExclusions(ctx context.Context, st store.Store, taskID string, runtimeName string, allowTrustedLocal bool, allowTrustedHostPreparation bool, allowTrustedRemote bool, commands []string, now time.Time, timeout time.Duration, workDir string, snapshotPaths []string, snapshotExclusionPaths []string) ([]review.PermissionDecisionRecord, []review.SandboxRun, error) {
	factory := func(factoryCtx context.Context, name string, factoryTaskID string, suffix string, factoryTimeout time.Duration, factoryWorkDir string, trustedLocal bool, trustedHostPreparation bool) (sandboxrun.Runtime, func(), *review.SandboxRun) {
		return runtimeForNameWithSnapshotPathsAndExclusions(factoryCtx, name, factoryTaskID, suffix, factoryTimeout, factoryWorkDir, trustedLocal, trustedHostPreparation, allowTrustedRemote, snapshotPaths, snapshotExclusionPaths)
	}
	return executePlannedCommandsWithFactory(ctx, st, taskID, runtimeName, allowTrustedLocal, allowTrustedHostPreparation, commands, now, timeout, workDir, factory)
}

func executePlannedCommandsWithFactory(ctx context.Context, st store.Store, taskID string, runtimeName string, allowTrustedLocal bool, allowTrustedHostPreparation bool, commands []string, now time.Time, timeout time.Duration, workDir string, factory runtimeFactory) ([]review.PermissionDecisionRecord, []review.SandboxRun, error) {
	if len(commands) == 0 {
		commands = newSandboxWorkspace(workDir).commandAllowlist()
	}
	allowedCommands := allowedCommandsByCanonical(workDir)
	var decisions []review.PermissionDecisionRecord
	var runs []review.SandboxRun
	var runtime sandboxrun.Runtime
	var cleanup func()
	runtimeInitAttempted := false
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
				return decisions, runs, err
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
			return decisions, runs, err
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
		if runtime == nil && !runtimeInitAttempted {
			runtimeInitAttempted = true
			var initRun *review.SandboxRun
			runtime, cleanup, initRun = factory(ctx, runtimeName, taskID, suffix, timeout, workDir, allowTrustedLocal, allowTrustedHostPreparation)
			if initRun != nil {
				runs = append(runs, *initRun)
			}
		}
		if runtime == nil {
			runs = append(runs, review.SandboxRun{
				ID:             runID,
				TaskID:         taskID,
				Runtime:        runtimeName,
				Command:        redact.Text(command).Text,
				Status:         sandboxrun.StatusUnavailable,
				ErrorType:      sandboxrun.ErrorRuntimeUnavailable,
				DurationMillis: 0,
			})
			continue
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

func runtimeForName(ctx context.Context, name string, taskID string, suffix string, timeout time.Duration, workDir string, allowTrustedLocal bool, allowTrustedHostPreparation bool, allowTrustedRemote bool) (sandboxrun.Runtime, func(), *review.SandboxRun) {
	return runtimeForNameWithSnapshotPaths(ctx, name, taskID, suffix, timeout, workDir, allowTrustedLocal, allowTrustedHostPreparation, allowTrustedRemote, nil)
}

func runtimeForNameWithSnapshotPaths(ctx context.Context, name string, taskID string, suffix string, timeout time.Duration, workDir string, allowTrustedLocal bool, allowTrustedHostPreparation bool, allowTrustedRemote bool, snapshotPaths []string) (sandboxrun.Runtime, func(), *review.SandboxRun) {
	return runtimeForNameWithSnapshotPathsAndExclusions(ctx, name, taskID, suffix, timeout, workDir, allowTrustedLocal, allowTrustedHostPreparation, allowTrustedRemote, snapshotPaths, nil)
}

func runtimeForNameWithSnapshotPathsAndExclusions(ctx context.Context, name string, taskID string, suffix string, timeout time.Duration, workDir string, allowTrustedLocal bool, allowTrustedHostPreparation bool, allowTrustedRemote bool, snapshotPaths []string, snapshotExclusionPaths []string) (sandboxrun.Runtime, func(), *review.SandboxRun) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		normalized = "container"
	}
	if normalized == "fake" {
		return sandboxrun.FakeRuntime{RuntimeName: normalized}, nil, nil
	}
	rt, cleanup, err := newWorkspaceRuntimeWithSnapshotPathsAndExclusions(ctx, normalized, taskID, timeout, workDir, allowTrustedLocal, allowTrustedHostPreparation, allowTrustedRemote, snapshotPaths, snapshotExclusionPaths)
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

func newWorkspaceRuntime(ctx context.Context, runtimeName string, taskID string, timeout time.Duration, workDir string, allowTrustedLocal bool, allowTrustedHostPreparation bool, allowTrustedRemote bool) (sandboxrun.Runtime, func(), error) {
	return newWorkspaceRuntimeWithSnapshotPaths(ctx, runtimeName, taskID, timeout, workDir, allowTrustedLocal, allowTrustedHostPreparation, allowTrustedRemote, nil)
}

func newWorkspaceRuntimeWithSnapshotPaths(ctx context.Context, runtimeName string, taskID string, timeout time.Duration, workDir string, allowTrustedLocal bool, allowTrustedHostPreparation bool, allowTrustedRemote bool, snapshotPaths []string) (sandboxrun.Runtime, func(), error) {
	return newWorkspaceRuntimeWithSnapshotPathsAndExclusions(ctx, runtimeName, taskID, timeout, workDir, allowTrustedLocal, allowTrustedHostPreparation, allowTrustedRemote, snapshotPaths, nil)
}

func newWorkspaceRuntimeWithSnapshotPathsAndExclusions(ctx context.Context, runtimeName string, taskID string, timeout time.Duration, workDir string, allowTrustedLocal bool, allowTrustedHostPreparation bool, allowTrustedRemote bool, snapshotPaths []string, snapshotExclusionPaths []string) (sandboxrun.Runtime, func(), error) {
	workspace := newSandboxWorkspace(workDir)
	repoRoot, err := workspace.root()
	if err != nil {
		return nil, nil, err
	}
	if err := validateRemoteRuntimePolicy(runtimeName, allowTrustedRemote); err != nil {
		return nil, nil, err
	}
	var eng codeexecutor.Engine
	var closeFn func() error
	dependencyMode := dependencyModeNone
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
		closeWorkspaceBackend(closeFn)
		return nil, nil, fmt.Errorf("runtime %q did not expose a workspace engine", runtimeName)
	}
	ws, err := eng.Manager().CreateWorkspace(ctx, taskID, codeexecutor.WorkspacePolicy{
		Isolated:     runtimeName != "local",
		MaxDiskBytes: 512 << 20,
	})
	if err != nil {
		closeWorkspaceBackend(closeFn)
		return nil, nil, err
	}
	var snapshotCleanup func()
	cleanup := newWorkspaceCleanup(eng.Manager(), ws, closeFn, func() {
		if snapshotCleanup != nil {
			snapshotCleanup()
		}
	})
	snapshotCleanup, dependencyMode, err = stageReviewWorkspaceWithSnapshotPathsAndModeAndExclusions(ctx, eng.FS(), ws, runtimeName, repoRoot, workspace.dependencySubdir(), allowTrustedHostPreparation, snapshotPaths, snapshotExclusionPaths)
	if err != nil {
		cleanup(context.Background())
		return nil, nil, err
	}
	return sandboxrun.WorkspaceRuntime{
		RuntimeName: runtimeName,
		Engine:      eng,
		Workspace:   ws,
		Cwd:         workspace.runtimeCwd(runtimeName),
		Timeout:     timeout,
		OutputLimit: defaultMaxSandboxOutput,
		Env:         workspaceRuntimeEnvForDependencyMode(runtimeName, dependencyMode),
		TerminateFn: cleanup,
	}, func() { cleanup(context.Background()) }, nil
}

func closeWorkspaceBackend(closeFn func() error) {
	if closeFn == nil {
		return
	}
	newWorkspaceCleanup(nil, codeexecutor.Workspace{}, closeFn, nil)(context.Background())
}

func newWorkspaceCleanup(manager codeexecutor.WorkspaceManager, ws codeexecutor.Workspace, closeFn func() error, snapshotCleanup func()) func(context.Context) {
	var cleanupOnce sync.Once
	cleanupDone := make(chan struct{})
	cleanupTimeout := workspaceCleanupTimeout
	return func(ctx context.Context) {
		cleanupOnce.Do(func() {
			go func() {
				defer close(cleanupDone)
				cleanupWorkspaceResources(ctx, manager, ws, closeFn, snapshotCleanup, cleanupTimeout)
			}()
		})
		waitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		select {
		case <-cleanupDone:
		case <-waitCtx.Done():
		}
	}
}

func cleanupWorkspaceResources(ctx context.Context, manager codeexecutor.WorkspaceManager, ws codeexecutor.Workspace, closeFn func() error, snapshotCleanup func(), cleanupTimeout time.Duration) {
	cleanupStep := func(fn func(context.Context)) bool {
		stepCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
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
	closeDone := true
	var closeErr error
	if closeFn != nil {
		// Container/E2B Close destroys the backend workspace; never race it with
		// manager cleanup. Fall back to manager cleanup only if Close fails.
		closeDone = cleanupStep(func(context.Context) { closeErr = closeFn() })
	}
	if manager != nil && (closeFn == nil || (closeDone && closeErr != nil)) {
		_ = cleanupStep(func(stepCtx context.Context) { _ = manager.Cleanup(stepCtx, ws) })
	}
	if snapshotCleanup != nil {
		_ = cleanupStep(func(context.Context) { snapshotCleanup() })
	}
}

func stageReviewWorkspace(ctx context.Context, fs codeexecutor.WorkspaceFS, ws codeexecutor.Workspace, runtimeName string, repoRoot string, dependencySubdir string, allowTrustedHostPreparation bool) (func(), error) {
	cleanup, _, err := stageReviewWorkspaceWithSnapshotPathsAndMode(ctx, fs, ws, runtimeName, repoRoot, dependencySubdir, allowTrustedHostPreparation, nil)
	return cleanup, err
}

func stageReviewWorkspaceWithSnapshotPaths(ctx context.Context, fs codeexecutor.WorkspaceFS, ws codeexecutor.Workspace, runtimeName string, repoRoot string, dependencySubdir string, allowTrustedHostPreparation bool, snapshotPaths []string) (func(), error) {
	cleanup, _, err := stageReviewWorkspaceWithSnapshotPathsAndModeAndExclusions(ctx, fs, ws, runtimeName, repoRoot, dependencySubdir, allowTrustedHostPreparation, snapshotPaths, nil)
	return cleanup, err
}

func stageReviewWorkspaceWithSnapshotPathsAndMode(ctx context.Context, fs codeexecutor.WorkspaceFS, ws codeexecutor.Workspace, runtimeName string, repoRoot string, dependencySubdir string, allowTrustedHostPreparation bool, snapshotPaths []string) (func(), sandboxDependencyMode, error) {
	return stageReviewWorkspaceWithSnapshotPathsAndModeAndExclusions(ctx, fs, ws, runtimeName, repoRoot, dependencySubdir, allowTrustedHostPreparation, snapshotPaths, nil)
}

func stageReviewWorkspaceWithSnapshotPathsAndModeAndExclusions(ctx context.Context, fs codeexecutor.WorkspaceFS, ws codeexecutor.Workspace, runtimeName string, repoRoot string, dependencySubdir string, allowTrustedHostPreparation bool, snapshotPaths []string, snapshotExclusionPaths []string) (func(), sandboxDependencyMode, error) {
	if runtimeName == "local" {
		return nil, dependencyModeNone, nil
	}
	stageRoot, snapshotCleanup, err := buildReviewSnapshotWithSnapshotPathsAndExclusions(ctx, repoRoot, snapshotPaths, snapshotExclusionPaths)
	if err != nil {
		return nil, dependencyModeNone, err
	}
	if runtimeName == "container" {
		if err := validateContainerToolchain(stageRoot, dependencySubdir); err != nil {
			snapshotCleanup()
			return nil, dependencyModeNone, err
		}
	}
	dependencyMode, err := prepareSandboxDependenciesWithMode(ctx, stageRoot, dependencySubdir, allowTrustedHostPreparation)
	if err != nil {
		snapshotCleanup()
		return nil, dependencyModeNone, err
	}
	if err := lockReviewSnapshotSource(stageRoot, []string{
		filepath.Join(stageRoot, ".gopath"),
		filepath.Join(stageRoot, ".gomodcache"),
		filepath.Join(stageRoot, ".gocache"),
	}); err != nil {
		snapshotCleanup()
		return nil, dependencyModeNone, err
	}
	if err := fs.StageDirectory(ctx, ws, stageRoot, codeexecutor.DirWork, codeexecutor.StageOptions{AllowMount: true}); err != nil {
		if snapshotCleanup != nil {
			snapshotCleanup()
		}
		return nil, dependencyModeNone, err
	}
	return snapshotCleanup, dependencyMode, nil
}

func buildReviewSnapshot(ctx context.Context, repoRoot string) (string, func(), error) {
	return buildReviewSnapshotWithLimit(ctx, repoRoot, reviewSnapshotMaxBytes)
}

func buildReviewSnapshotWithLimit(ctx context.Context, repoRoot string, maxBytes int64) (string, func(), error) {
	return buildReviewSnapshotWithLimits(ctx, repoRoot, maxBytes, maxReviewSnapshotFiles)
}

func buildReviewSnapshotWithLimits(ctx context.Context, repoRoot string, maxBytes int64, maxFiles int) (string, func(), error) {
	return buildReviewSnapshotWithLimitsAndPaths(ctx, repoRoot, maxBytes, maxFiles, nil)
}

func buildReviewSnapshotWithSnapshotPaths(ctx context.Context, repoRoot string, snapshotPaths []string) (string, func(), error) {
	return buildReviewSnapshotWithLimitsAndPaths(ctx, repoRoot, reviewSnapshotMaxBytes, maxReviewSnapshotFiles, snapshotPaths)
}

func buildReviewSnapshotWithLimitsAndPaths(ctx context.Context, repoRoot string, maxBytes int64, maxFiles int, snapshotPaths []string) (string, func(), error) {
	return buildReviewSnapshotWithLimitsAndPathsAndExclusions(ctx, repoRoot, maxBytes, maxFiles, snapshotPaths, nil)
}

func buildReviewSnapshotWithSnapshotPathsAndExclusions(ctx context.Context, repoRoot string, snapshotPaths []string, snapshotExclusionPaths []string) (string, func(), error) {
	return buildReviewSnapshotWithLimitsAndPathsAndExclusions(ctx, repoRoot, reviewSnapshotMaxBytes, maxReviewSnapshotFiles, snapshotPaths, snapshotExclusionPaths)
}

func buildReviewSnapshotWithLimitsAndPathsAndExclusions(ctx context.Context, repoRoot string, maxBytes int64, maxFiles int, snapshotPaths []string, snapshotExclusionPaths []string) (string, func(), error) {
	files, err := trackedReviewFilesWithPaths(ctx, repoRoot, maxFiles, snapshotPaths)
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
		if file.Gitlink {
			cleanup()
			return "", nil, fmt.Errorf("unsupported tracked git submodule %q", file.Path)
		}
		if configuredReviewSnapshotPath(file.Path, snapshotExclusionPaths) || (excludedReviewSnapshotPath(file.Path) && (!file.Tracked || sensitiveReviewSnapshotPath(file.Path))) {
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

func prepareSandboxDependencies(ctx context.Context, snapshotRoot string, dependencySubdir string, allowTrustedHostPreparation bool) error {
	_, err := prepareSandboxDependenciesWithMode(ctx, snapshotRoot, dependencySubdir, allowTrustedHostPreparation)
	return err
}

func prepareSandboxDependenciesWithMode(ctx context.Context, snapshotRoot string, dependencySubdir string, allowTrustedHostPreparation bool) (sandboxDependencyMode, error) {
	moduleDir := filepath.Join(snapshotRoot, filepath.FromSlash(dependencySubdir))
	cacheRoot := filepath.Join(snapshotRoot, ".gopath")
	modCache := filepath.Join(snapshotRoot, ".gomodcache")
	buildCache := filepath.Join(snapshotRoot, ".gocache")
	homeDir := filepath.Join(snapshotRoot, ".home")
	tmpDir := filepath.Join(snapshotRoot, ".tmp")
	for _, dir := range []string{cacheRoot, modCache, buildCache, homeDir, tmpDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return dependencyModeNone, fmt.Errorf("create sandbox dependency cache: %w", err)
		}
	}
	_, moduleErr := os.Stat(filepath.Join(moduleDir, "go.mod"))
	if errors.Is(moduleErr, os.ErrNotExist) {
		return dependencyModeNone, makeSandboxCachesWritable(snapshotRoot, []string{cacheRoot, modCache, buildCache})
	}
	if moduleErr != nil {
		return dependencyModeNone, fmt.Errorf("stat sandbox go.mod: %w", moduleErr)
	}
	dependencyMode, err := dependencyModeForModule(moduleDir, modCache)
	if err != nil {
		return dependencyModeNone, fmt.Errorf("inspect sandbox dependencies: %w", err)
	}
	if dependencyMode == dependencyModeHostPreparation && !allowTrustedHostPreparation {
		return dependencyModeNone, fmt.Errorf("host-side dependency preparation is disabled for untrusted review input; vendor dependencies or pre-provision .gomodcache, or rerun with --allow-trusted-host-preparation")
	}
	if dependencyMode == dependencyModeVendor || dependencyMode == dependencyModePreProvisioned {
		return dependencyMode, makeSandboxCachesWritable(snapshotRoot, []string{cacheRoot, modCache, buildCache})
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
			return dependencyModeNone, fmt.Errorf("prepare sandbox dependencies timed out after %s", dependencyPrepTimeout)
		}
		return dependencyModeNone, fmt.Errorf("prepare sandbox dependencies: %w: %s", err, redact.Text(strings.TrimSpace(output.String())).Text)
	}
	if err := ensureDependencyCacheWithinLimit([]string{cacheRoot, modCache, buildCache}, dependencyPrepMaxBytes); err != nil {
		return dependencyModeNone, err
	}
	if err := makeSandboxCachesWritable(snapshotRoot, []string{cacheRoot, modCache, buildCache}); err != nil {
		return dependencyModeNone, err
	}
	return dependencyModeHostPreparation, nil
}

func dependencyModeForModule(moduleDir string, modCache string) (sandboxDependencyMode, error) {
	if _, err := os.Stat(filepath.Join(moduleDir, "go.mod")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return dependencyModeNone, nil
		}
		return dependencyModeNone, err
	}
	if _, err := os.Stat(filepath.Join(moduleDir, "vendor", "modules.txt")); err == nil {
		return dependencyModeVendor, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return dependencyModeNone, err
	}
	if _, err := os.Stat(modCache); errors.Is(err, os.ErrNotExist) {
		return dependencyModeHostPreparation, nil
	} else if err != nil {
		return dependencyModeNone, err
	}
	found := false
	err := filepath.WalkDir(modCache, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			found = true
		}
		return nil
	})
	if err != nil {
		return dependencyModeNone, err
	}
	if found {
		return dependencyModePreProvisioned, nil
	}
	return dependencyModeHostPreparation, nil
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
		mode := info.Mode().Perm()
		if entry.IsDir() {
			return os.Chmod(path, (mode|0o555)&^0o222)
		}
		readOnly := mode | 0o444
		if mode&0o111 != 0 {
			readOnly |= 0o111
		}
		return os.Chmod(path, readOnly&^0o222)
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
	return trackedReviewFilesWithPaths(ctx, repoRoot, maxFiles, nil)
}

func trackedReviewFilesWithPaths(ctx context.Context, repoRoot string, maxFiles int, snapshotPaths []string) ([]reviewSnapshotFile, error) {
	files := make([]reviewSnapshotFile, 0, minInt(maxFiles, 1024))
	if err := appendReviewFiles(ctx, repoRoot, maxFiles, true, nil, &files, "ls-files", "-s", "-z", "--cached"); err != nil {
		return nil, fmt.Errorf("list tracked review files: %w", err)
	}
	allowed := snapshotPathSet(snapshotPaths)
	if allowed != nil {
		if len(allowed) == 0 {
			return files, nil
		}
		selected := make([]string, 0, len(allowed))
		for path := range allowed {
			selected = append(selected, path)
		}
		sort.Strings(selected)
		if err := appendUntrackedReviewFilesInBatches(ctx, repoRoot, maxFiles, allowed, &files, selected, maxSnapshotPathspecBatchPaths, maxSnapshotPathspecBatchBytes); err != nil {
			return nil, fmt.Errorf("list selected untracked review files: %w", err)
		}
		return files, nil
	}
	if err := appendReviewFiles(ctx, repoRoot, maxFiles, false, allowed, &files, "ls-files", "-z", "--others", "--exclude-standard"); err != nil {
		return nil, fmt.Errorf("list untracked review files: %w", err)
	}
	return files, nil
}

func appendUntrackedReviewFilesInBatches(ctx context.Context, repoRoot string, maxFiles int, allowed map[string]struct{}, files *[]reviewSnapshotFile, selected []string, maxBatchPaths int, maxBatchBytes int64) error {
	if len(selected) == 0 {
		return nil
	}
	if maxBatchPaths <= 0 || maxBatchBytes <= 0 {
		return fmt.Errorf("invalid untracked pathspec batch limits")
	}
	flush := func(batch []string) error {
		if len(batch) == 0 {
			return nil
		}
		args := []string{"--literal-pathspecs", "ls-files", "-z", "--others", "--exclude-standard", "--"}
		args = append(args, batch...)
		return appendReviewFiles(ctx, repoRoot, maxFiles, false, allowed, files, args...)
	}
	batch := make([]string, 0, maxBatchPaths)
	var batchBytes int64
	for _, path := range selected {
		pathBytes := int64(len(path) + 1)
		if pathBytes > maxBatchBytes {
			return fmt.Errorf("untracked pathspec %q exceeds %d bytes", path, maxBatchBytes)
		}
		if len(batch) > 0 && (len(batch) >= maxBatchPaths || batchBytes+pathBytes > maxBatchBytes) {
			if err := flush(batch); err != nil {
				return err
			}
			batch = batch[:0]
			batchBytes = 0
		}
		batch = append(batch, path)
		batchBytes += pathBytes
	}
	return flush(batch)
}

func snapshotPathSet(paths []string) map[string]struct{} {
	if paths == nil {
		return nil
	}
	selected := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path == "" || path == "/dev/null" {
			continue
		}
		selected[filepath.ToSlash(path)] = struct{}{}
	}
	return selected
}

func appendReviewFiles(ctx context.Context, repoRoot string, maxFiles int, tracked bool, allowed map[string]struct{}, files *[]reviewSnapshotFile, args ...string) error {
	cmdArgs := []string{"-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-C", repoRoot}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = snapshotGitCommandEnv()
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
				path, gitlink, parseErr := parseReviewFileRecord(part, tracked)
				if parseErr != nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					return parseErr
				}
				if !tracked && allowed != nil {
					if _, ok := allowed[path]; !ok {
						continue
					}
				}
				if maxFiles >= 0 && len(*files) >= maxFiles {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					return fmt.Errorf("review snapshot file count exceeded %d", maxFiles)
				}
				*files = append(*files, reviewSnapshotFile{Path: path, Tracked: tracked, Gitlink: gitlink})
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

func snapshotGitCommandEnv() []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env)+5)
	for _, item := range env {
		name, _, _ := strings.Cut(item, "=")
		switch {
		case name == "GIT_CONFIG_COUNT",
			strings.HasPrefix(name, "GIT_CONFIG_KEY_"),
			strings.HasPrefix(name, "GIT_CONFIG_VALUE_"),
			name == "GIT_CONFIG_PARAMETERS",
			name == "GIT_CONFIG_GLOBAL",
			name == "GIT_CONFIG_SYSTEM",
			name == "GIT_CONFIG_NOSYSTEM",
			name == "GIT_EXTERNAL_DIFF",
			name == "GIT_DIFF_OPTS",
			name == "GIT_PAGER",
			name == "GIT_PAGER_IN_USE",
			name == "GIT_DIR",
			name == "GIT_WORK_TREE",
			name == "GIT_INDEX_FILE",
			name == "GIT_OBJECT_DIRECTORY",
			name == "GIT_ALTERNATE_OBJECT_DIRECTORIES",
			name == "GIT_COMMON_DIR",
			name == "GIT_ASKPASS",
			name == "GIT_SSH",
			name == "GIT_SSH_COMMAND",
			name == "GIT_PROXY_COMMAND":
			continue
		default:
			filtered = append(filtered, item)
		}
	}
	filtered = append(filtered,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_PAGER=cat",
		"GIT_TERMINAL_PROMPT=0",
	)
	return filtered
}

func parseReviewFileRecord(record []byte, tracked bool) (string, bool, error) {
	if !tracked {
		return filepath.ToSlash(string(record)), false, nil
	}
	separator := bytes.IndexByte(record, '\t')
	if separator <= 0 || separator == len(record)-1 {
		return "", false, fmt.Errorf("invalid tracked review file record")
	}
	metadata := bytes.Fields(record[:separator])
	if len(metadata) == 0 {
		return "", false, fmt.Errorf("invalid tracked review file metadata")
	}
	return filepath.ToSlash(string(record[separator+1:])), string(metadata[0]) == "160000", nil
}

func minInt(a int, b int) int {
	if a >= 0 && a < b {
		return a
	}
	return b
}

func reviewSnapshotArtifactPaths(repoRoot string, dbPath string, outDir string) ([]string, error) {
	if strings.TrimSpace(repoRoot) == "" {
		return nil, nil
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root %q: %w", repoRoot, err)
	}
	root = filepath.Clean(root)
	paths := make([]string, 0, 2)
	add := func(configured string) error {
		if strings.TrimSpace(configured) == "" {
			return nil
		}
		absolute, err := filepath.Abs(configured)
		if err != nil {
			return fmt.Errorf("resolve configured path %q: %w", configured, err)
		}
		relative, err := filepath.Rel(root, absolute)
		if err != nil {
			if rootVolume, pathVolume := filepath.VolumeName(root), filepath.VolumeName(absolute); rootVolume != "" && pathVolume != "" && !strings.EqualFold(rootVolume, pathVolume) {
				return nil
			}
			return fmt.Errorf("relativize configured path %q: %w", configured, err)
		}
		relative = filepath.Clean(relative)
		if relative == "." {
			return fmt.Errorf("configured path %q resolves to the repository root", configured)
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return nil
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	}
	if err := add(dbPath); err != nil {
		return nil, err
	}
	if err := add(dbPath + ".lock"); err != nil {
		return nil, err
	}
	if err := add(outDir); err != nil {
		return nil, err
	}
	sort.Strings(paths)
	unique := paths[:0]
	for _, path := range paths {
		if len(unique) == 0 || unique[len(unique)-1] != path {
			unique = append(unique, path)
		}
	}
	return unique, nil
}

func configuredReviewSnapshotPath(file string, configured []string) bool {
	file = filepath.ToSlash(filepath.Clean(file))
	for _, excluded := range configured {
		excluded = filepath.ToSlash(filepath.Clean(excluded))
		if excluded == "." || excluded == "" {
			continue
		}
		if file == excluded || strings.HasPrefix(file, excluded+"/") {
			return true
		}
	}
	return false
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

func validateRemoteRuntimePolicy(runtimeName string, allowTrustedRemote bool) error {
	normalized := strings.ToLower(strings.TrimSpace(runtimeName))
	if normalized != "e2b" || allowTrustedRemote {
		return nil
	}
	return fmt.Errorf("runtime %q is disabled because this E2B path has no enforced egress boundary; rerun only for explicitly trusted/networked review input with AllowTrustedRemote or --allow-trusted-remote", normalized)
}

func failedTaskContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), failedTaskFinishTimeout)
}

func validateContainerToolchain(repoRoot string, dependencySubdir string) error {
	moduleDir := filepath.Join(repoRoot, filepath.FromSlash(dependencySubdir))
	required := goVersion{}
	metadataPaths := []string{filepath.Join(moduleDir, "go.mod")}
	workFiles := goWorkFiles(repoRoot, moduleDir)
	metadataPaths = append(metadataPaths, workFiles...)
	seen := make(map[string]struct{}, len(metadataPaths))
	for index := 0; index < len(metadataPaths); index++ {
		metadataPath := metadataPaths[index]
		metadataPath, err := filepath.Abs(metadataPath)
		if err != nil {
			return fmt.Errorf("resolve Go metadata path: %w", err)
		}
		if _, ok := seen[metadataPath]; ok {
			continue
		}
		seen[metadataPath] = struct{}{}
		raw, exists, err := readBoundedGoMetadata(metadataPath)
		if err != nil {
			return fmt.Errorf("read Go metadata %s: %w", metadataPath, err)
		}
		if !exists {
			continue
		}
		candidate, err := requiredModuleGoVersion(string(raw))
		if err != nil {
			return fmt.Errorf("inspect Go metadata %s: %w", metadataPath, err)
		}
		if candidate.after(required) {
			required = candidate
		}
		if filepath.Base(metadataPath) != "go.work" {
			continue
		}
		root, err := filepath.Abs(repoRoot)
		if err != nil {
			return fmt.Errorf("resolve workspace root: %w", err)
		}
		for _, usePath := range workspaceUsePaths(string(raw)) {
			usePath = filepath.FromSlash(usePath)
			candidatePath := usePath
			if !filepath.IsAbs(candidatePath) {
				candidatePath = filepath.Join(filepath.Dir(metadataPath), candidatePath)
			}
			candidatePath, err = filepath.Abs(candidatePath)
			if err != nil || !pathWithinRoot(root, candidatePath) {
				continue
			}
			metadataPaths = append(metadataPaths, filepath.Join(candidatePath, "go.mod"))
		}
	}
	if !required.valid() {
		return nil
	}
	imageVersion, err := parseGoVersion(strings.TrimPrefix(containerSandboxImage, "golang:"))
	if err != nil {
		return fmt.Errorf("inspect container toolchain: %w", err)
	}
	if required.exceeds(imageVersion) {
		return fmt.Errorf("unsupported-toolchain: reviewed module requires Go %s, but container image %s supports Go %s", required, containerSandboxImage, imageVersion)
	}
	return nil
}

func requiredModuleGoVersion(raw string) (goVersion, error) {
	var required goVersion
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[0] != "go" {
			continue
		}
		candidate, err := parseGoVersion(fields[1])
		if err != nil {
			return goVersion{}, err
		}
		if !required.valid() || candidate.after(required) {
			required = candidate
		}
	}
	return required, nil
}

func goWorkFiles(repoRoot string, moduleDir string) []string {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil
	}
	dir, err := filepath.Abs(moduleDir)
	if err != nil || !pathWithinRoot(root, dir) {
		return nil
	}
	for {
		candidate := filepath.Join(dir, "go.work")
		if _, err := os.Lstat(candidate); err == nil || !errors.Is(err, os.ErrNotExist) {
			return []string{candidate}
		}
		if dir == root {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil
}

func workspaceUsePaths(raw string) []string {
	var paths []string
	inBlock := false
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if inBlock {
			if workspaceUseBlockEnd(line) {
				inBlock = false
				continue
			}
			if path := firstWorkspacePathToken(line); path != "" {
				paths = append(paths, path)
			}
			continue
		}
		rest, ok := workspaceUseDirective(line)
		if !ok {
			continue
		}
		if workspaceUseBlockStart(rest) {
			inBlock = true
			continue
		}
		if path := firstWorkspacePathToken(rest); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func workspaceUseBlockStart(rest string) bool {
	rest = strings.TrimSpace(rest)
	if rest == "(" {
		return true
	}
	return strings.HasPrefix(rest, "(") && strings.HasPrefix(strings.TrimSpace(rest[1:]), "//")
}

func workspaceUseBlockEnd(line string) bool {
	line = strings.TrimSpace(line)
	if line == ")" {
		return true
	}
	return strings.HasPrefix(line, ")") && strings.HasPrefix(strings.TrimSpace(line[1:]), "//")
}

func workspaceUseDirective(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if len(line) <= len("use") || line[:len("use")] != "use" || !unicode.IsSpace(rune(line[len("use")])) {
		return "", false
	}
	return strings.TrimSpace(line[len("use"):]), true
}

func firstWorkspacePathToken(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	if line[0] == '`' {
		end := strings.IndexByte(line[1:], '`')
		if end < 0 {
			return ""
		}
		return line[1 : end+1]
	}
	if line[0] == '"' {
		for index := 1; index < len(line); index++ {
			if line[index] == '\\' {
				index++
				continue
			}
			if line[index] != '"' {
				continue
			}
			path, err := strconv.Unquote(line[:index+1])
			if err != nil {
				return ""
			}
			return path
		}
		return ""
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func pathWithinRoot(root string, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(rel) || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func readBoundedGoMetadata(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("symlinks are not supported")
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("expected a regular file, got %s", info.Mode().String())
	}
	in, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer in.Close()
	raw, err := io.ReadAll(io.LimitReader(in, maxGoMetadataBytes+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(raw)) > maxGoMetadataBytes {
		return nil, false, fmt.Errorf("file exceeds %d bytes", maxGoMetadataBytes)
	}
	return raw, true, nil
}

func parseGoVersion(raw string) (goVersion, error) {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return goVersion{}, fmt.Errorf("invalid Go version %q", raw)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return goVersion{}, fmt.Errorf("invalid Go version %q", raw)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return goVersion{}, fmt.Errorf("invalid Go version %q", raw)
	}
	version := goVersion{major: major, minor: minor}
	if len(parts) == 3 {
		patch, err := strconv.Atoi(parts[2])
		if err != nil || patch < 0 {
			return goVersion{}, fmt.Errorf("invalid Go version %q", raw)
		}
		version.patch = patch
		version.patchSpecified = true
	}
	return version, nil
}

func (v goVersion) valid() bool {
	return v.major != 0 || v.minor != 0
}

func (v goVersion) after(other goVersion) bool {
	if v.major != other.major {
		return v.major > other.major
	}
	if v.minor != other.minor {
		return v.minor > other.minor
	}
	if v.patchSpecified != other.patchSpecified {
		return v.patchSpecified && v.patch > 0
	}
	return v.patch > other.patch
}

func (v goVersion) exceeds(other goVersion) bool {
	if v.major != other.major {
		return v.major > other.major
	}
	if v.minor != other.minor {
		return v.minor > other.minor
	}
	if !v.patchSpecified {
		return false
	}
	// An unpinned image tag cannot prove support for an explicit patch level.
	if !other.patchSpecified {
		return true
	}
	return v.patch > other.patch
}

func (v goVersion) String() string {
	if v.patchSpecified {
		return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
	}
	return fmt.Sprintf("%d.%d", v.major, v.minor)
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
	return workspaceRuntimeEnvForDependencyMode(runtimeName, dependencyModeNone)
}

func workspaceRuntimeEnvForDependencyMode(runtimeName string, dependencyMode sandboxDependencyMode) map[string]string {
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
	env := map[string]string{
		"HOME":        "/tmp",
		"GOPATH":      "/go",
		"GOMODCACHE":  containerGoModCache,
		"GOCACHE":     containerGoBuildCache,
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
	}
	if dependencyMode == dependencyModeVendor {
		env["GOFLAGS"] = "-mod=vendor"
	} else {
		env["GOFLAGS"] = ""
	}
	return env
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

func removeReportArtifacts(artifacts []review.ArtifactRecord) error {
	var cleanupErrs []error
	for _, artifact := range artifacts {
		if err := os.Remove(artifact.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("remove report artifact %s: %w", filepath.Base(artifact.Path), err))
		}
	}
	return errors.Join(cleanupErrs...)
}
