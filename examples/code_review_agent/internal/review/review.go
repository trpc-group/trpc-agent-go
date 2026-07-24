//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// Run executes one governed review pipeline and persists its report.
func Run(ctx context.Context, cfg Config) (Report, ReportPaths, error) {
	started := time.Now()
	if err := normalizeConfig(&cfg); err != nil {
		return Report{}, ReportPaths{}, err
	}
	baseDir, err := exampleDir()
	if err != nil {
		return Report{}, ReportPaths{}, err
	}
	reviewSkill, err := loadReviewSkill(baseDir)
	if err != nil {
		return Report{}, ReportPaths{}, err
	}
	taskID := cfg.TaskID
	if taskID == "" {
		taskID = newTaskID()
	}
	store, err := cfg.StoreFactory(ctx, cfg)
	if err != nil {
		return Report{}, ReportPaths{}, fmt.Errorf("open review store: %w", err)
	}
	defer store.Close()
	input, mode, inputDecisions, err := loadInput(ctx, cfg, baseDir)
	if err != nil {
		return persistFailure(ctx, store, cfg, Task{ID: taskID, Status: TaskRunning, InputMode: firstNonEmpty(mode, "input_error"), StartedAt: started}, DiffSummary{}, inputDecisions, "load_input", err)
	}
	task := Task{ID: taskID, Status: TaskRunning, InputMode: mode, StartedAt: started}
	findings, warnings, needsHuman, filterAudit := analyzeWithRuleIDs(input, reviewSkill.RuleIDs)
	sandboxRunner, err := newSandbox(ctx, cfg, baseDir)
	if err != nil {
		return persistFailure(ctx, store, cfg, task, input.Summary, inputDecisions, "initialize_sandbox", err)
	}
	defer sandboxRunner.Close()
	runs, sandboxDecisions, sandboxArtifacts, err := sandboxRunner.run(ctx, task.ID, cfg.RepoPath, input)
	if err != nil {
		return Report{}, ReportPaths{}, fmt.Errorf("run sandbox checks: %w", err)
	}
	sandboxItems := sandboxReviewItems(runs)
	needsHuman = append(needsHuman, sandboxItems...)
	retainedAudit := filterAudit[:0]
	for _, decision := range filterAudit {
		if decision.TargetBucket != "needs_human_review" {
			retainedAudit = append(retainedAudit, decision)
		}
	}
	filterAudit = append(retainedAudit, filterDecisions(needsHuman, "needs_human_review", FilterRouteHuman)...)
	needsHuman = dedupe(needsHuman)
	report := Report{
		Task: task, Input: input.Summary, Findings: findings, Warnings: warnings,
		NeedsHumanReview: needsHuman, SandboxRuns: runs, PermissionDecisions: append(inputDecisions, sandboxDecisions...),
		Artifacts: sandboxArtifacts, Mode: executionMode(cfg), FilterDecisions: filterAudit,
	}
	report.Conclusion = conclusion(report)
	report = redactReport(report)
	if err := store.Save(ctx, report); err != nil {
		return Report{}, ReportPaths{}, fmt.Errorf("store review: %w", err)
	}
	failStoredReview := func(operation string, cause error) (Report, ReportPaths, error) {
		if failureErr := markStoredFailure(ctx, store, cfg, report, operation, cause); failureErr != nil {
			return Report{}, ReportPaths{}, fmt.Errorf("%s: %w; persist failure record: %v", operation, cause, failureErr)
		}
		return Report{}, ReportPaths{}, fmt.Errorf("%s for task %s: %w", operation, report.Task.ID, cause)
	}
	stored, err := store.Load(ctx, report.Task.ID)
	if err != nil {
		return failStoredReview("verify_initial_store", err)
	}
	if stored.Task.ID != report.Task.ID {
		return failStoredReview("verify_initial_store", errors.New("stored review task ID mismatch"))
	}
	report.Task.Status, report.Task.EndedAt = TaskCompleted, time.Now()
	report.Metrics = collectMetrics(started, report)
	stagedReport, paths, staged, err := stageReport(report, cfg.OutputDir)
	if err != nil {
		return failStoredReview("stage_report", err)
	}
	report = stagedReport
	defer staged.cleanup()
	if err := store.Finalize(ctx, report); err != nil {
		return failStoredReview("finalize_report", err)
	}
	stored, err = store.Load(ctx, report.Task.ID)
	if err != nil {
		return failStoredReview("verify_final_store", err)
	}
	if stored.Task.Status != TaskCompleted || stored.Metrics.PreparationDurationMS != report.Metrics.PreparationDurationMS {
		return failStoredReview("verify_final_store", errors.New("finalized review verification mismatch"))
	}
	if err := staged.commit(); err != nil {
		return failStoredReview("publish_report", err)
	}
	return report, paths, nil
}

func markStoredFailure(ctx context.Context, store Store, cfg Config, report Report, operation string, cause error) error {
	report.Task.Status, report.Task.EndedAt = TaskFailed, time.Now()
	report.Artifacts = nil
	report.SandboxRuns = append(report.SandboxRuns, setupFailure(cfg.Executor, operation, cause, cfg.OutputLimit))
	report.Conclusion = "Review failed after persistence; inspect the audit record before retrying."
	report.Metrics = collectMetrics(report.Task.StartedAt, report)
	return store.Finalize(ctx, redactReport(report))
}

func persistFailure(ctx context.Context, store Store, cfg Config, task Task, input DiffSummary, decisions []PermissionDecision, operation string, cause error) (Report, ReportPaths, error) {
	task.Status, task.EndedAt = TaskFailed, time.Now()
	report := Report{
		Task: task, Input: input, PermissionDecisions: decisions, Mode: executionMode(cfg),
		SandboxRuns: []SandboxRun{setupFailure(cfg.Executor, operation, cause, cfg.OutputLimit)},
		Conclusion:  "Review setup failed; inspect the persisted failure record before retrying.",
	}
	report.Metrics = collectMetrics(task.StartedAt, report)
	report = redactReport(report)
	if err := store.Save(ctx, report); err != nil {
		return Report{}, ReportPaths{}, fmt.Errorf("persist %s failure: %w", operation, err)
	}
	if err := store.Finalize(ctx, report); err != nil {
		return Report{}, ReportPaths{}, fmt.Errorf("finalize %s failure: %w", operation, err)
	}
	return Report{}, ReportPaths{}, fmt.Errorf("%s for task %s: %w", operation, task.ID, cause)
}

func normalizeConfig(cfg *Config) error {
	if cfg.TaskID != "" {
		for _, r := range cfg.TaskID {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return errors.New("task id may contain only letters, digits, hyphen, and underscore")
			}
		}
		if len(cfg.TaskID) > 80 {
			return errors.New("task id must not exceed 80 characters")
		}
		if looksSecret(cfg.TaskID) {
			return errors.New("task id must not contain credential-like material")
		}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 45 * time.Second
	}
	if cfg.OutputLimit <= 0 {
		cfg.OutputLimit = 64 << 10
	}
	if cfg.OutputLimit > 1<<20 {
		return errors.New("output limit must not exceed 1 MiB")
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "output"
	}
	if cfg.DatabasePath == "" {
		cfg.DatabasePath = filepath.Join(cfg.OutputDir, "reviews.sqlite")
	}
	if cfg.Executor == "" {
		cfg.Executor = ExecutorContainer
	}
	if cfg.DryRun {
		cfg.Executor = ExecutorFake
	}
	if cfg.StoreFactory == nil {
		cfg.StoreFactory = defaultStoreFactory
	}
	return nil
}

func exampleDir() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot locate example directory")
	}
	dir := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(dir, "skills", "code-review", "SKILL.md")); err != nil {
		return "", err
	}
	return dir, nil
}

type loadedReviewSkill struct {
	RuleIDs map[string]bool
}

func loadReviewSkill(baseDir string) (loadedReviewSkill, error) {
	repository, err := skill.NewFSRepository(filepath.Join(baseDir, "skills"))
	if err != nil {
		return loadedReviewSkill{}, err
	}
	loaded, err := repository.Get("code-review")
	if err != nil {
		return loadedReviewSkill{}, err
	}
	if strings.TrimSpace(loaded.Body) == "" || len(loaded.Docs) == 0 {
		return loadedReviewSkill{}, errors.New("code-review skill must include a body and rule documentation")
	}
	path, err := repository.Path("code-review")
	if err != nil {
		return loadedReviewSkill{}, err
	}
	if _, err := os.Stat(filepath.Join(path, "scripts", "diff_stats.sh")); err != nil {
		return loadedReviewSkill{}, err
	}
	doc, err := os.ReadFile(filepath.Join(path, "docs", "go-review-rules.md"))
	if err != nil {
		return loadedReviewSkill{}, err
	}
	ruleIDs := map[string]bool{}
	for _, match := range regexpRuleID.FindAllString(string(doc), -1) {
		ruleIDs[strings.Trim(match, "`")] = true
	}
	if len(ruleIDs) == 0 {
		return loadedReviewSkill{}, errors.New("code-review skill declares no rule IDs")
	}
	return loadedReviewSkill{RuleIDs: ruleIDs}, nil
}

var regexpRuleID = regexp.MustCompile("`go/[a-z0-9_/-]+`")

func sandboxReviewItems(runs []SandboxRun) []Finding {
	var result []Finding
	for _, run := range runs {
		if run.Status == RunSuccess || run.Status == RunSkipped && run.ErrorType == ErrorToolUnavailable || run.ErrorType == ErrorDryRun {
			continue
		}
		result = append(result, fingerprint(Finding{
			Severity: SeverityMedium, Category: "sandbox", Title: "Sandbox check requires human review",
			Evidence:       redact(strings.TrimSpace(run.Command + " " + stringsJoin(run.Args) + ": " + string(run.ErrorType) + " " + run.Stderr)),
			Recommendation: "Inspect the recorded failure or governance decision and rerun the check in a healthy sandbox.",
			Confidence:     .99, Source: "sandbox", RuleID: "sandbox/" + firstNonEmpty(string(run.ErrorType), string(run.Status)),
		}))
	}
	return result
}

func collectMetrics(started time.Time, report Report) Metrics {
	metrics := Metrics{PreparationDurationMS: time.Since(started).Milliseconds(), ToolCallCount: len(report.SandboxRuns), FindingCount: len(report.Findings), WarningCount: len(report.Warnings), NeedsHumanCount: len(report.NeedsHumanReview), SeverityDistribution: map[string]int{}, ErrorDistribution: map[string]int{}}
	for _, run := range report.SandboxRuns {
		metrics.SandboxDurationMS += run.DurationMS
		if run.ErrorType != "" && run.ErrorType != ErrorDryRun {
			metrics.ErrorDistribution[string(run.ErrorType)]++
		}
	}
	for _, decision := range report.PermissionDecisions {
		if decision.Action == PermissionDeny {
			metrics.PermissionDenyCount++
		}
		if decision.Action == PermissionAsk {
			metrics.PermissionAskCount++
		}
	}
	for _, bucket := range [][]Finding{report.Findings, report.Warnings, report.NeedsHumanReview} {
		for _, finding := range bucket {
			metrics.SeverityDistribution[string(finding.Severity)]++
		}
	}
	return metrics
}

func conclusion(report Report) string {
	for _, finding := range report.Findings {
		if finding.Severity == SeverityCritical && finding.RuleID == "go/security/hardcoded-secret" {
			return "Critical findings block merge until remediation and credential rotation are complete."
		}
	}
	for _, finding := range report.Findings {
		if finding.Severity == SeverityCritical {
			return "Critical findings block merge until the reported issues are remediated."
		}
	}
	if len(report.Findings) > 0 {
		return "Review found actionable issues that should be fixed before merge."
	}
	if len(report.NeedsHumanReview) > 0 {
		return "No high-confidence issue was confirmed, but listed items require human review."
	}
	return "No actionable issue was detected by the configured deterministic checks."
}

func executionMode(cfg Config) ExecutionMode {
	if cfg.FakeModel {
		return "deterministic-rule-only+fake-model"
	}
	return "deterministic-rule-only"
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "unknown"
}
func newTaskID() string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("review-%d", time.Now().UnixNano())
	}
	return "review-" + hex.EncodeToString(raw)
}
