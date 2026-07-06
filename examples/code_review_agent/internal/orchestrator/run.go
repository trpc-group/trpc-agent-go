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
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
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
	defaultSandboxCommand   = "go test ./..."
	defaultSkillName        = "code-review"
)

// Options configures one review run.
type Options struct {
	FixtureDir string
	OutDir     string
	DBPath     string
	Model      string
	Runtime    string
	RepoPath   string
	Now        time.Time
	Planner    Planner
}

// Result is returned by the orchestrator after reports are written.
type Result struct {
	TaskID       string
	Report       review.Report
	JSONPath     string
	MarkdownPath string
	DBPath       string
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
	Files   []review.DiffFile
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
		return reviewPlan(modelName, "mock", "mock_planner", req.Skill, runtimeName), nil
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
	plan := reviewPlan(modelName, "openai_compatible", "model_response", req.Skill, runtimeName)
	if len(modelPlan.Commands) > 0 {
		plan.Commands = redactStrings(modelPlan.Commands)
	}
	if len(modelPlan.RuleSources) > 0 {
		plan.RuleSources = redactStrings(modelPlan.RuleSources)
	}
	return plan, nil
}

func reviewPlan(modelName string, provider string, source string, skill string, runtimeName string) review.ReviewPlan {
	if skill == "" {
		skill = defaultSkillName
	}
	return review.ReviewPlan{
		Model:    redact.Text(modelName).Text,
		Provider: provider,
		Source:   source,
		Skill:    skill,
		Runtime:  runtimeName,
		Commands: []string{
			defaultSandboxCommand,
		},
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
		"skill":         req.Skill,
		"runtime":       req.Runtime,
		"changed_files": files,
		"allowed_commands": []string{
			defaultSandboxCommand,
		},
		"rule_sources": []string{
			"skills/code-review/SKILL.md",
			"skills/code-review/docs/rules.md",
		},
		"response_schema": map[string]any{
			"commands":     []string{"go test ./..."},
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

func defaultPlanner() Planner {
	return EnvPlanner{
		APIKey:     os.Getenv("OPENAI_API_KEY"),
		BaseURL:    os.Getenv("OPENAI_BASE_URL"),
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Run executes a model-planned review over fixture diffs.
func Run(ctx context.Context, opts Options) (Result, error) {
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
	fixedNow := !opts.Now.IsZero()
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	rawDiff, fixtureNames, err := readFixtures(opts.FixtureDir)
	if err != nil {
		return Result{}, err
	}
	taskID := stableTaskID(rawDiff, now)
	task := review.ReviewTask{
		ID:        taskID,
		Status:    review.TaskStatusRunning,
		InputType: review.InputTypeFixture,
		RepoPath:  opts.RepoPath,
		DiffHash:  hashText(rawDiff),
		StartedAt: now.UTC(),
	}

	st, err := store.NewSQLite(ctx, opts.DBPath)
	if err != nil {
		return Result{}, err
	}
	defer st.Close()
	if err := st.CreateTask(ctx, task); err != nil {
		return Result{}, err
	}

	files, err := diffparse.Parse(rawDiff)
	if err != nil {
		_ = st.FinishTask(ctx, task.ID, review.TaskStatusFailed, err.Error())
		return Result{}, err
	}
	changedFilesJSON, err := json.Marshal(files)
	if err != nil {
		_ = st.FinishTask(ctx, task.ID, review.TaskStatusFailed, err.Error())
		return Result{}, fmt.Errorf("marshal changed files: %w", err)
	}
	redactedDiff := redact.Text(rawDiff)
	if err := st.RecordInput(ctx, store.InputRecord{
		TaskID:           task.ID,
		DiffSummary:      summarizeDiff(files, fixtureNames),
		ChangedFilesJSON: string(changedFilesJSON),
		RedactedDiff:     redactedDiff.Text,
	}); err != nil {
		return Result{}, err
	}

	planner := opts.Planner
	if planner == nil {
		planner = defaultPlanner()
	}
	plan, err := planner.PlanReview(ctx, PlanRequest{
		Model:   opts.Model,
		Runtime: opts.Runtime,
		Skill:   defaultSkillName,
		Files:   files,
	})
	if err != nil {
		_ = st.FinishTask(ctx, task.ID, review.TaskStatusFailed, err.Error())
		return Result{}, err
	}

	findings := rules.Evaluate(files)
	if err := st.SaveFindings(ctx, task.ID, findings); err != nil {
		return Result{}, err
	}

	decisions, runs, err := executePlannedCommands(ctx, st, task.ID, opts.Runtime, plan.Commands, now)
	if err != nil {
		return Result{}, err
	}
	for _, run := range runs {
		if err := st.RecordSandboxRun(ctx, run); err != nil {
			return Result{}, err
		}
	}

	metrics := report.BuildMetrics(task.ID, task.StartedAt, findings, runs, decisions, redactedDiff.Count+countFindingRedactions(findings))
	if fixedNow {
		metrics.TotalDurationMillis = 0
	}
	task.Status = statusFor(findings, runs)
	task.FinishedAt = now.UTC()
	conclusion := conclusionFor(task.Status, findings, runs)
	r := review.Report{
		Task:                task,
		Summary:             summarizeOutcome(files, findings, runs, plan),
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
		_ = st.FinishTask(ctx, task.ID, review.TaskStatusFailed, err.Error())
		return Result{}, err
	}
	r.Artifacts = artifacts
	if err := st.SaveArtifacts(ctx, artifacts); err != nil {
		return Result{}, err
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
		return Result{}, err
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

func readFixtures(dir string) (string, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, fmt.Errorf("read fixture dir: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".diff") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", nil, fmt.Errorf("read fixture %s: %w", name, err)
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.Write(raw)
		if !strings.HasSuffix(string(raw), "\n") {
			b.WriteString("\n")
		}
	}
	return b.String(), names, nil
}

func stableTaskID(diff string, now time.Time) string {
	sum := sha256.Sum256([]byte(diff + now.UTC().Format("20060102")))
	return "review-" + hex.EncodeToString(sum[:])[:12]
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func summarizeDiff(files []review.DiffFile, fixtureNames []string) string {
	return fmt.Sprintf("Reviewed %d diff fixtures across %d changed files.", len(fixtureNames), len(files))
}

func summarizeOutcome(files []review.DiffFile, findings []review.Finding, runs []review.SandboxRun, plan review.ReviewPlan) string {
	return fmt.Sprintf("Model plan %q coordinated skill %q for %d changed files, produced %d findings, and recorded %d sandbox runs.", plan.Model, plan.Skill, len(files), len(findings), len(runs))
}

func executePlannedCommands(ctx context.Context, st store.Store, taskID string, runtimeName string, commands []string, now time.Time) ([]review.PermissionDecisionRecord, []review.SandboxRun, error) {
	if len(commands) == 0 {
		commands = []string{defaultSandboxCommand}
	}
	var decisions []review.PermissionDecisionRecord
	var runs []review.SandboxRun
	runtime := runtimeForName(runtimeName)
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
		runs = append(runs, sandboxrun.Run(ctx, runtime, taskID, runID, command, defaultMaxSandboxOutput))
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

func runtimeForName(name string) sandboxrun.Runtime {
	return sandboxrun.FakeRuntime{RuntimeName: name}
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
