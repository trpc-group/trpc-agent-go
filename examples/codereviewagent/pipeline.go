//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const timeFormat = time.RFC3339Nano

type pipelineConfig struct {
	DiffFile   string
	RepoPath   string
	SkillsRoot string
	OutputDir  string
	Database   string
	Mode       string
	Runner     sandboxRunner
}

func runReview(ctx context.Context, cfg pipelineConfig) (*reviewReport, error) {
	started := time.Now()
	createdAt := started.UTC()
	diffData, source, err := loadDiff(ctx, cfg.DiffFile, cfg.RepoPath)
	if err != nil {
		return nil, err
	}
	diffHash := sha256.Sum256(diffData)
	diffSHA := hex.EncodeToString(diffHash[:])
	taskID := "review-" + diffSHA[:12]
	lines, err := parseUnifiedDiff(diffData)
	if err != nil {
		return nil, err
	}
	skill, err := selectSkill(lines, cfg.SkillsRoot)
	if err != nil {
		return nil, err
	}
	findings := analyze(lines)
	command := []string{"go", "test", "."}
	permission := permissionRecordFor(command)
	runner := cfg.Runner
	if runner == nil {
		runner = drySandbox{}
	}
	var sandboxResult sandboxRun
	if permission.Action == string(tool.PermissionActionAllow) {
		sandboxResult = runner.Run(ctx, taskID, command)
	} else {
		sandboxResult = sandboxRun{Status: "blocked", Command: command, ExitCode: -1, Error: permission.Reason}
	}
	sandboxResult.Output, _ = redact(sandboxResult.Output)
	sandboxResult.Error, _ = redact(sandboxResult.Error)
	if sandboxResult.Status == "failed" {
		findings = dedupeFindings(append(findings, finding{
			File: "", StartLine: 0, EndLine: 0, Severity: "P2", Category: "sandbox_failure",
			Confidence: 1, Source: "sandbox", RuleID: "SAN001", Status: "needs_human_review",
			Message:    "sandbox validation failed without aborting the review task",
			Suggestion: "inspect the sandbox error and rerun validation after correcting the environment",
		}))
	}
	metrics := buildMetrics(findings, sandboxResult, time.Since(started))
	status := reviewStatus(findings)
	report := &reviewReport{
		TaskID: taskID, Status: status, Mode: cfg.Mode, InputSource: filepath.ToSlash(source),
		DiffSHA256: diffSHA, Skill: skill.Name, CreatedAt: createdAt, Findings: findings,
		Permission: permission, Sandbox: sandboxResult, Metrics: metrics,
		Summary: reviewSummary(status, findings),
	}
	artifacts, err := writeReviewReports(cfg.OutputDir, report)
	if err != nil {
		return nil, err
	}
	store, err := openReviewStore(cfg.Database)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	if err := store.Save(ctx, report, artifacts); err != nil {
		return nil, err
	}
	return report, nil
}

func buildMetrics(findings []finding, sandboxResult sandboxRun, duration time.Duration) reviewMetrics {
	metrics := reviewMetrics{
		DurationMS: duration.Milliseconds(), SandboxDuration: sandboxResult.DurationMS,
		ToolCalls: 1, PermissionChecks: 1, FindingCount: len(findings),
		BySeverity: make(map[string]int), ByCategory: make(map[string]int),
	}
	for _, finding := range findings {
		metrics.BySeverity[finding.Severity]++
		metrics.ByCategory[finding.Category]++
		if finding.Status == "needs_human_review" {
			metrics.Warnings++
		}
	}
	return metrics
}

func reviewStatus(findings []finding) string {
	for _, finding := range findings {
		if finding.Severity == "P0" || finding.Severity == "P1" {
			return "needs_attention"
		}
	}
	if len(findings) > 0 {
		return "needs_human_review"
	}
	return "passed"
}

func reviewSummary(status string, findings []finding) string {
	actionable := 0
	warnings := 0
	for _, finding := range findings {
		if finding.Status == "needs_human_review" {
			warnings++
		} else {
			actionable++
		}
	}
	return fmt.Sprintf("Review %s with %d actionable findings and %d human-review warnings.", status, actionable, warnings)
}

func normalizeOutputDir(path string) string {
	if strings.TrimSpace(path) == "" {
		return "./output"
	}
	return path
}
