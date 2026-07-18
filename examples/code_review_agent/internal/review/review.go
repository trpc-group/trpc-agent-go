//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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
	if err := loadReviewSkill(baseDir); err != nil {
		return Report{}, ReportPaths{}, err
	}
	input, mode, err := loadInput(ctx, cfg, baseDir)
	if err != nil {
		return Report{}, ReportPaths{}, err
	}
	taskID := cfg.TaskID
	if taskID == "" {
		taskID = newTaskID()
	}
	task := Task{ID: taskID, Status: TaskRunning, InputMode: mode, StartedAt: started}
	findings, warnings, needsHuman, filterAudit := analyzeWithDecisions(input)
	sandboxRunner, err := newSandbox(ctx, cfg, baseDir)
	if err != nil {
		return Report{}, ReportPaths{}, fmt.Errorf("initialize sandbox: %w", err)
	}
	defer sandboxRunner.Close()
	runs, decisions, sandboxArtifacts := sandboxRunner.run(ctx, task.ID, cfg.RepoPath, input)
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
	task.Status, task.EndedAt = TaskCompleted, time.Now()
	report := Report{
		Task: task, Input: input.Summary, Findings: findings, Warnings: warnings,
		NeedsHumanReview: needsHuman, SandboxRuns: runs, PermissionDecisions: decisions,
		Artifacts: sandboxArtifacts, Mode: executionMode(cfg), FilterDecisions: filterAudit,
	}
	report.Metrics = collectMetrics(started, report)
	report.Conclusion = conclusion(report)
	report, paths, err := publish(report, cfg.OutputDir)
	if err != nil {
		return Report{}, ReportPaths{}, err
	}
	store, err := openStore(cfg.DatabasePath)
	if err != nil {
		return Report{}, ReportPaths{}, fmt.Errorf("open review database: %w", err)
	}
	defer store.Close()
	if err := store.Save(ctx, report); err != nil {
		return Report{}, ReportPaths{}, fmt.Errorf("store review: %w", err)
	}
	stored, err := store.Load(ctx, task.ID)
	if err != nil || stored.Task.ID != task.ID {
		return Report{}, ReportPaths{}, errors.New("stored review verification failed")
	}
	return report, paths, nil
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

func loadReviewSkill(baseDir string) error {
	repository, err := skill.NewFSRepository(filepath.Join(baseDir, "skills"))
	if err != nil {
		return err
	}
	loaded, err := repository.Get("code-review")
	if err != nil {
		return err
	}
	if strings.TrimSpace(loaded.Body) == "" || len(loaded.Docs) == 0 {
		return errors.New("code-review skill must include a body and rule documentation")
	}
	path, err := repository.Path("code-review")
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(path, "scripts", "diff_stats.sh")); err != nil {
		return err
	}
	return nil
}

func sandboxReviewItems(runs []SandboxRun) []Finding {
	var result []Finding
	for _, run := range runs {
		if run.Status == RunSuccess || run.Status == RunSkipped && run.ErrorType == "tool_unavailable" || run.ErrorType == "dry_run" {
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
	metrics := Metrics{TotalDurationMS: time.Since(started).Milliseconds(), ToolCallCount: len(report.SandboxRuns), FindingCount: len(report.Findings), WarningCount: len(report.Warnings), NeedsHumanCount: len(report.NeedsHumanReview), SeverityDistribution: map[string]int{}, ErrorDistribution: map[string]int{}}
	for _, run := range report.SandboxRuns {
		metrics.SandboxDurationMS += run.DurationMS
		if run.ErrorType != "" {
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
		if finding.Severity == SeverityCritical {
			return "Critical findings block merge until remediation and credential rotation are complete."
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
