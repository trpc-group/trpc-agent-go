//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/report"
	stor "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage"
)

// CRAgent orchestrates a full code review: diff parsing, rule checking, sandbox execution,
// dedup, sanitization, report generation, and storage.
type CRAgent struct {
	registry  *RuleRegistry
	sandbox   *SandboxManager
	store     stor.Store
	sanitizer *finding.Sanitizer
	dedup     *finding.DedupEngine
	config    *CRConfig
}

// CRAgentOption configures the agent.
type CRAgentOption func(*CRAgent)

// NewCRAgent creates a new code review agent.
func NewCRAgent(registry *RuleRegistry, sandbox *SandboxManager, store stor.Store, opts ...CRAgentOption) *CRAgent {
	a := &CRAgent{
		registry:  registry,
		sandbox:   sandbox,
		store:     store,
		sanitizer: finding.NewSanitizer(),
		dedup:     finding.NewDedupEngine(),
		config:    &CRConfig{},
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// WithCRConfig sets the agent configuration.
func WithCRConfig(cfg *CRConfig) CRAgentOption {
	return func(a *CRAgent) { a.config = cfg }
}

// ReviewInput describes the input for a code review.
type ReviewInput struct {
	TaskID      string
	DiffSource  string
	DiffContent string
	RepoPath    string
	DryRun      bool
}

// Run executes a complete code review.
func (a *CRAgent) Run(ctx context.Context, input ReviewInput) (*report.ReviewReport, error) {
	startTime := time.Now()

	task := &finding.ReviewTask{
		ID:         input.TaskID,
		DiffSource: input.DiffSource,
		Status:     "running",
		DryRun:     input.DryRun,
		CreatedAt:  startTime,
	}

	if err := a.store.CreateTask(ctx, task); err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}

	// 1. Parse diff.
	changedFiles, err := diff.ParseUnifiedDiff(input.DiffContent)
	if err != nil {
		_ = a.store.UpdateTaskStatus(ctx, input.TaskID, "failed", err.Error())
		return nil, fmt.Errorf("parse diff: %w", err)
	}
	task.ChangedFiles = diff.ExtractFileInfo(changedFiles)
	task.DiffSummary = diff.DiffSummary(changedFiles)

	// 2. Run rules.
	rules := a.registry.EnabledRules(a.config)
	var allFindings []finding.Finding

	for _, rule := range rules {
		fileInfos := diff.GoFileFilter(task.ChangedFiles)
		fileInfos = diff.NonTestFiles(fileInfos)

		for _, fi := range fileInfos {
			// For each changed file, run the rule.
			findings, err := rule.Check(ctx, fi, "")
			if err != nil {
				continue // skip rule on error
			}
			for i := range findings {
				// Apply severity override.
				findings[i].Severity = EffectiveSeverity(rule.ID(), rule.DefaultSeverity(), a.config)
			}
			allFindings = append(allFindings, findings...)
		}
	}

	// 3. Sanitize.
	for i := range allFindings {
		allFindings[i] = a.sanitizer.SanitizeFinding(allFindings[i])
	}

	// 4. Dedup.
	dedupResult := a.dedup.Dedup(allFindings)

	// 5. Sort findings by severity.
	report.SortFindings(dedupResult.Findings)

	// 6. Store findings (convert to pointer slice).
	findingPtrs := make([]*finding.Finding, len(dedupResult.Findings))
	for i := range dedupResult.Findings {
		findingPtrs[i] = &dedupResult.Findings[i]
	}
	if err := a.store.CreateFindings(ctx, findingPtrs); err != nil {
		_ = a.store.UpdateTaskStatus(ctx, input.TaskID, "failed", err.Error())
		return nil, fmt.Errorf("store findings: %w", err)
	}

	// 7. Build report.
	riskSummary := report.BuildRiskSummary(dedupResult.Findings, dedupResult.Warnings)
	monitoring := report.BuildMonitoringSummary(
		time.Since(startTime).Milliseconds(),
		0,
		dedupResult.Findings,
		dedupResult.Warnings,
		len(dedupResult.Findings)+dedupResult.Suppressed,
		0,
		0,
	)

	reviewReport := &report.ReviewReport{
		TaskID:          input.TaskID,
		DiffSummary:     task.DiffSummary,
		Findings:        dedupResult.Findings,
		Warnings:        dedupResult.Warnings,
		RiskSummary:     riskSummary,
		PermissionLog:   []report.PermissionDecisionSummary{},
		SandboxSummary:  report.SandboxSummary{},
		Monitoring:      monitoring,
		Recommendations: buildRecommendations(dedupResult.Findings),
		GeneratedAt:     time.Now(),
	}

	// 8. Save reports.
	jsonReport, err := report.ToJSON(*reviewReport)
	if err == nil {
		task.ReportJSON = jsonReport
		_ = a.store.SaveReport(ctx, input.TaskID, "json", jsonReport)
	}

	mdReport := report.ToMarkdown(*reviewReport)
	task.ReportMD = mdReport
	_ = a.store.SaveReport(ctx, input.TaskID, "markdown", mdReport)

	// 9. Update task.
	duration := time.Since(startTime).Milliseconds()
	_ = a.store.UpdateTaskStats(ctx, input.TaskID, stor.TaskStats{
		FindingCount:    len(dedupResult.Findings),
		HighRiskCount:   riskSummary.BySeverity["critical"] + riskSummary.BySeverity["high"],
		MediumRiskCount: riskSummary.BySeverity["medium"],
		LowRiskCount:    riskSummary.BySeverity["low"],
		WarningCount:    len(dedupResult.Warnings),
		TotalDurationMs: duration,
	})
	_ = a.store.UpdateTaskStatus(ctx, input.TaskID, "completed", "")

	return reviewReport, nil
}

func buildRecommendations(findings []finding.Finding) []string {
	seen := make(map[string]bool)
	var result []string
	for _, f := range findings {
		if !seen[f.Recommendation] && f.Recommendation != "" {
			seen[f.Recommendation] = true
			result = append(result, f.Recommendation)
		}
	}
	if len(result) > 5 {
		result = result[:5]
	}
	return result
}
