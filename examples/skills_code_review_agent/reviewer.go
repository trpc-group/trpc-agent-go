//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	rootskill "trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const inputPreviewBytes = 4096

var diagnosticPattern = regexp.MustCompile(
	`(?m)([A-Za-z0-9_./\\-]+\.go):([0-9]+)(?::[0-9]+)?:\s*([^\r\n]+)`,
)

// Reviewer orchestrates skill loading, rules, sandbox checks, persistence,
// and report generation.
type Reviewer struct {
	store     ReviewStore
	sandbox   ReviewSandbox
	skillRepo rootskill.Repository
	skillPath string
	skillName string
}

// NewReviewer validates and loads the code-review skill.
func NewReviewer(
	store ReviewStore,
	sandbox ReviewSandbox,
	skillsRoot string,
) (*Reviewer, error) {
	if store == nil {
		return nil, errors.New("review store is required")
	}
	if sandbox == nil {
		return nil, errors.New("review sandbox is required")
	}
	repository, err := rootskill.NewFSRepository(skillsRoot)
	if err != nil {
		return nil, fmt.Errorf("open skills repository: %w", err)
	}
	loaded, err := repository.Get("code-review")
	if err != nil {
		return nil, fmt.Errorf("load code-review skill: %w", err)
	}
	if err := validateReviewSkill(loaded); err != nil {
		return nil, err
	}
	skillPath, err := repository.Path("code-review")
	if err != nil {
		return nil, fmt.Errorf("resolve code-review skill path: %w", err)
	}
	return &Reviewer{
		store: store, sandbox: sandbox, skillRepo: repository,
		skillPath: skillPath, skillName: loaded.Summary.Name,
	}, nil
}

// Review executes the complete deterministic review pipeline.
func (r *Reviewer) Review(
	ctx context.Context,
	request ReviewRequest,
) (ReviewReport, error) {
	started := time.Now().UTC()
	parsed, err := ParseUnifiedDiff(request.Diff)
	if err != nil {
		return ReviewReport{}, err
	}
	taskID := newTaskID(started, request.Diff)
	findings, warnings := AnalyzeDiff(parsed)

	sandboxStarted := time.Now()
	sandboxResult, sandboxErr := r.sandbox.RunChecks(
		ctx, taskID, request.Diff, request.RepoPath, r.skillPath,
		request.RunStaticcheck,
	)
	sandboxDuration := time.Since(sandboxStarted)
	if sandboxErr != nil {
		sandboxResult.Runs = append(sandboxResult.Runs, SandboxRun{
			Command:      "sandbox_setup",
			Status:       "failed",
			DurationMS:   sandboxDuration.Milliseconds(),
			ErrorType:    "sandbox_setup",
			ErrorMessage: Redact(sandboxErr.Error()),
		})
	}
	findings = append(
		findings, findingsFromSandbox(sandboxResult.Runs)...,
	)
	findings = DeduplicateFindings(findings)
	warnings = DeduplicateFindings(warnings)
	humanReview := append([]Finding(nil), warnings...)
	humanReview = append(
		humanReview,
		governanceHumanReview(sandboxResult.Decisions)...,
	)
	humanReview = DeduplicateFindings(humanReview)

	completed := time.Now().UTC()
	report := ReviewReport{
		TaskID:           taskID,
		Status:           reviewStatus(sandboxResult.Runs),
		Conclusion:       reviewConclusion(findings, humanReview, sandboxResult.Runs),
		Mode:             reviewMode(request),
		Runtime:          request.Runtime,
		Skill:            r.skillName,
		StartedAt:        started,
		CompletedAt:      completed,
		Input:            summarizeInput(request, parsed),
		Findings:         findings,
		Warnings:         warnings,
		NeedsHumanReview: humanReview,
		Decisions:        sandboxResult.Decisions,
		SandboxRuns:      sandboxResult.Runs,
	}
	report.Metrics = buildMetrics(
		report, time.Since(started), sandboxDuration,
	)
	jsonReport, markdownReport, err := RenderReports(report)
	if err != nil {
		return ReviewReport{}, err
	}
	artifacts, err := WriteReportFiles(
		request.OutputDir, jsonReport, markdownReport,
	)
	if err != nil {
		return ReviewReport{}, err
	}
	report.Artifacts = artifacts
	if err := r.store.SaveReview(
		ctx, report, jsonReport, markdownReport,
	); err != nil {
		return ReviewReport{}, err
	}
	return report, nil
}

func validateReviewSkill(skill *rootskill.Skill) error {
	if skill == nil || skill.Summary.Name != "code-review" {
		return errors.New("code-review skill metadata is invalid")
	}
	if strings.TrimSpace(skill.Body) == "" {
		return errors.New("code-review SKILL.md body is empty")
	}
	for _, document := range skill.Docs {
		if filepath.ToSlash(document.Path) == "docs/rules.md" {
			return nil
		}
	}
	return errors.New("code-review skill is missing docs/rules.md")
}

func summarizeInput(request ReviewRequest, parsed ParsedDiff) InputSummary {
	digest := sha256.Sum256(request.Diff)
	files := make([]string, 0, len(parsed.Files))
	packageSet := make(map[string]bool)
	for _, file := range parsed.Files {
		files = append(files, file.Path)
		if file.Package != "" {
			packageSet[file.Package] = true
		}
	}
	sort.Strings(files)
	packages := make([]string, 0, len(packageSet))
	for packageName := range packageSet {
		packages = append(packages, packageName)
	}
	sort.Strings(packages)
	return InputSummary{
		Kind:            request.InputKind,
		SHA256:          hex.EncodeToString(digest[:]),
		Bytes:           len(request.Diff),
		ChangedFiles:    files,
		GoPackages:      packages,
		RedactedPreview: boundedText(Redact(string(request.Diff)), inputPreviewBytes),
	}
}

func findingsFromSandbox(runs []SandboxRun) []Finding {
	var findings []Finding
	for _, run := range runs {
		if run.Status != "failed" || run.Output == "" {
			continue
		}
		source, category, severity := sandboxFindingMetadata(run.Command)
		matches := diagnosticPattern.FindAllStringSubmatch(run.Output, -1)
		for _, match := range matches {
			line, err := strconv.Atoi(match[2])
			if err != nil {
				continue
			}
			file := strings.TrimPrefix(
				filepath.ToSlash(match[1]), "./",
			)
			if filepath.IsAbs(file) {
				file = filepath.Base(file)
			}
			findings = append(findings, Finding{
				Severity: severity, Category: category, File: file,
				Line: line, Title: "Sandbox check reported a diagnostic",
				Evidence:       boundedText(Redact(match[3]), 2048),
				Recommendation: "Resolve the diagnostic and rerun the isolated check.",
				Confidence:     0.99, Source: source,
				RuleID: strings.ToUpper(source),
			})
		}
	}
	return findings
}

func sandboxFindingMetadata(command string) (string, string, string) {
	switch {
	case strings.Contains(command, `"go" "test"`):
		return "go_test", "test_failure", severityHigh
	case strings.Contains(command, `"go" "vet"`):
		return "go_vet", "static_analysis", severityMedium
	case strings.Contains(command, `"staticcheck"`):
		return "staticcheck", "static_analysis", severityMedium
	default:
		return "sandbox", "sandbox_execution", severityMedium
	}
}

func governanceHumanReview(
	decisions []PermissionDecision,
) []Finding {
	var findings []Finding
	for _, decision := range decisions {
		if decision.Action != string(tool.PermissionActionAsk) {
			continue
		}
		findings = append(findings, Finding{
			Severity: severityLow, Category: "governance",
			File: "", Line: 0,
			Title:          "Command requires human approval",
			Evidence:       Redact(decision.Command),
			Recommendation: "Review the command and grant a narrowly scoped one-time approval if appropriate.",
			Confidence:     0.70, Source: sourcePermission,
			RuleID: "GOV001",
		})
	}
	return findings
}

func reviewStatus(runs []SandboxRun) string {
	for _, run := range runs {
		if run.Status == "failed" {
			return "completed_with_errors"
		}
	}
	return "completed"
}

func reviewConclusion(
	findings []Finding,
	humanReview []Finding,
	runs []SandboxRun,
) string {
	for _, finding := range findings {
		if severityRank(finding.Severity) >= severityRank(severityHigh) {
			return "changes_requested"
		}
	}
	if len(findings) > 0 || len(humanReview) > 0 ||
		reviewStatus(runs) != "completed" {
		return "needs_human_review"
	}
	return "approved"
}

func reviewMode(request ReviewRequest) string {
	if request.DryRun {
		return "dry-run"
	}
	if request.FakeModel {
		return "fake-model"
	}
	return "deterministic-rule-only"
}

func buildMetrics(
	report ReviewReport,
	totalDuration time.Duration,
	sandboxDuration time.Duration,
) Metrics {
	metrics := Metrics{
		TotalDurationMS:   totalDuration.Milliseconds(),
		SandboxDurationMS: sandboxDuration.Milliseconds(),
		ToolCalls:         len(report.Decisions),
		FindingCount:      len(report.Findings),
		WarningCount:      len(report.Warnings),
		Severity: map[string]int{
			severityCritical: 0,
			severityHigh:     0,
			severityMedium:   0,
			severityLow:      0,
		},
		Errors: make(map[string]int),
	}
	for _, finding := range append(
		append([]Finding(nil), report.Findings...), report.Warnings...,
	) {
		metrics.Severity[finding.Severity]++
	}
	for _, decision := range report.Decisions {
		if decision.Action != string(tool.PermissionActionAllow) {
			metrics.PermissionBlocked++
		}
	}
	for _, run := range report.SandboxRuns {
		if run.ErrorType != "" {
			metrics.Errors[run.ErrorType]++
		}
	}
	return metrics
}

func newTaskID(started time.Time, diff []byte) string {
	digest := sha256.Sum256(diff)
	return fmt.Sprintf(
		"review-%d-%s", started.UnixNano(),
		hex.EncodeToString(digest[:6]),
	)
}
