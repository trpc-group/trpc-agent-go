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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// EvalManifest labels fixtures so quality gates come from measured runs.
type EvalManifest struct {
	Fixtures     []EvalFixtureLabel `json:"fixtures"`
	SecretValues []string           `json:"secret_values"`
}

// EvalFixtureLabel is the expected rule set for one fixture.
type EvalFixtureLabel struct {
	Name              string   `json:"name"`
	ExpectedRules     []string `json:"expected_rules"`
	ExpectedHighRules []string `json:"expected_high_rules"`
	AllowedExtraRules []string `json:"allowed_extra_rules"`
}

// EvalReport records measurable fixture review quality and redaction metrics.
type EvalReport struct {
	StartedAt             time.Time           `json:"started_at"`
	CompletedAt           time.Time           `json:"completed_at"`
	DurationMS            int64               `json:"duration_ms"`
	FixtureCount          int                 `json:"fixture_count"`
	ExpectedRules         int                 `json:"expected_rules"`
	MatchedRules          int                 `json:"matched_rules"`
	ExpectedHighRules     int                 `json:"expected_high_rules"`
	MatchedHighRules      int                 `json:"matched_high_rules"`
	ReportedRules         int                 `json:"reported_rules"`
	FalsePositiveRules    int                 `json:"false_positive_rules"`
	SecretValueChecks     int                 `json:"secret_value_checks"`
	SecretValueLeaks      int                 `json:"secret_value_leaks"`
	Recall                float64             `json:"recall"`
	HighRiskRecall        float64             `json:"high_risk_recall"`
	FalsePositiveRate     float64             `json:"false_positive_rate"`
	RedactionRate         float64             `json:"redaction_rate"`
	PassedHiddenThreshold bool                `json:"passed_hidden_threshold"`
	Results               []EvalFixtureResult `json:"results"`
}

// EvalFixtureResult records measured rule matches for one fixture.
type EvalFixtureResult struct {
	Name           string   `json:"name"`
	TaskID         string   `json:"task_id"`
	MatchedRules   []string `json:"matched_rules"`
	MissingRules   []string `json:"missing_rules"`
	ReportedRules  []string `json:"reported_rules"`
	FalsePositives []string `json:"false_positives"`
	SecretLeaks    []string `json:"secret_leaks"`
}

// RunEvaluation runs labeled fixtures and writes eval_report.json/md.
func RunEvaluation(ctx context.Context, opts ReviewOptions, labelsPath string) (EvalReport, string, string, error) {
	start := time.Now().UTC()
	manifest, err := loadEvalManifest(labelsPath)
	if err != nil {
		return EvalReport{}, "", "", err
	}
	if opts.OutDir == "" {
		opts.OutDir = "code_review_agent_out"
	}
	report := EvalReport{StartedAt: start, Results: []EvalFixtureResult{}}
	for _, label := range manifest.Fixtures {
		next := opts
		next.Fixture = label.Name
		next.OutDir = filepath.Join(opts.OutDir, label.Name)
		next.DBPath = filepath.Join(next.OutDir, "review_agent.db")
		review, jsonPath, mdPath, err := RunReview(ctx, next)
		if err != nil {
			return EvalReport{}, "", "", fmt.Errorf("%s: %w", label.Name, err)
		}
		result, counts, err := evaluateFixture(label, review, []string{jsonPath, mdPath, next.DBPath}, manifest.SecretValues)
		if err != nil {
			return EvalReport{}, "", "", err
		}
		report.Results = append(report.Results, result)
		report.ExpectedRules += counts.expectedRules
		report.MatchedRules += counts.matchedRules
		report.ExpectedHighRules += counts.expectedHighRules
		report.MatchedHighRules += counts.matchedHighRules
		report.ReportedRules += counts.reportedRules
		report.FalsePositiveRules += counts.falsePositiveRules
		report.SecretValueChecks += counts.secretValueChecks
		report.SecretValueLeaks += counts.secretValueLeaks
	}
	report.FixtureCount = len(manifest.Fixtures)
	report.CompletedAt = time.Now().UTC()
	report.DurationMS = report.CompletedAt.Sub(start).Milliseconds()
	report.Recall = ratio(report.MatchedRules, report.ExpectedRules)
	report.HighRiskRecall = ratio(report.MatchedHighRules, report.ExpectedHighRules)
	report.FalsePositiveRate = ratio(report.FalsePositiveRules, report.ReportedRules)
	report.RedactionRate = ratio(report.SecretValueChecks-report.SecretValueLeaks, report.SecretValueChecks)
	report.PassedHiddenThreshold = report.HighRiskRecall >= 0.80 && report.FalsePositiveRate <= 0.15 && report.RedactionRate >= 0.95
	jsonPath, mdPath, err := writeEvalReport(report, opts.OutDir)
	if err != nil {
		return EvalReport{}, "", "", err
	}
	return report, jsonPath, mdPath, nil
}

type evalCounts struct {
	expectedRules      int
	matchedRules       int
	expectedHighRules  int
	matchedHighRules   int
	reportedRules      int
	falsePositiveRules int
	secretValueChecks  int
	secretValueLeaks   int
}

func loadEvalManifest(path string) (EvalManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return EvalManifest{}, err
	}
	var manifest EvalManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return EvalManifest{}, err
	}
	if len(manifest.Fixtures) == 0 {
		return EvalManifest{}, fmt.Errorf("eval manifest has no fixtures")
	}
	return manifest, nil
}

func evaluateFixture(label EvalFixtureLabel, report ReviewReport, artifactPaths []string, secrets []string) (EvalFixtureResult, evalCounts, error) {
	reported := collectRuleIDs(report)
	expected := stringSet(label.ExpectedRules)
	allowed := stringSet(label.AllowedExtraRules)
	allowed["go.testing.missing"] = struct{}{}
	result := EvalFixtureResult{
		Name:           label.Name,
		TaskID:         report.Task.ID,
		MatchedRules:   intersection(label.ExpectedRules, reported),
		MissingRules:   missing(label.ExpectedRules, reported),
		ReportedRules:  sortedKeys(reported),
		FalsePositives: []string{},
		SecretLeaks:    []string{},
	}
	for rule := range reported {
		if _, ok := expected[rule]; ok {
			continue
		}
		if _, ok := allowed[rule]; ok {
			continue
		}
		result.FalsePositives = append(result.FalsePositives, rule)
	}
	sort.Strings(result.FalsePositives)
	leaks, checks, err := scanArtifactsForSecrets(artifactPaths, secrets)
	if err != nil {
		return EvalFixtureResult{}, evalCounts{}, err
	}
	result.SecretLeaks = leaks
	counts := evalCounts{
		expectedRules:      len(label.ExpectedRules),
		matchedRules:       len(result.MatchedRules),
		expectedHighRules:  len(label.ExpectedHighRules),
		matchedHighRules:   len(intersection(label.ExpectedHighRules, reported)),
		reportedRules:      len(reported),
		falsePositiveRules: len(result.FalsePositives),
		secretValueChecks:  checks,
		secretValueLeaks:   len(leaks),
	}
	return result, counts, nil
}

func collectRuleIDs(report ReviewReport) map[string]struct{} {
	out := map[string]struct{}{}
	for _, bucket := range [][]Finding{report.Findings, report.Warnings, report.NeedsHumanReview} {
		for _, f := range bucket {
			if f.RuleID != "" {
				out[f.RuleID] = struct{}{}
			}
		}
	}
	return out
}

func scanArtifactsForSecrets(paths []string, secrets []string) ([]string, int, error) {
	if len(secrets) == 0 {
		return []string{}, 0, nil
	}
	leaks := map[string]struct{}{}
	checks := 0
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, 0, err
		}
		content := string(raw)
		for _, secret := range secrets {
			if secret == "" {
				continue
			}
			checks++
			if strings.Contains(content, secret) {
				leaks[secret] = struct{}{}
			}
		}
	}
	return sortedKeys(leaks), checks, nil
}

func writeEvalReport(report EvalReport, outDir string) (string, string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", "", err
	}
	jsonPath := filepath.Join(outDir, "eval_report.json")
	mdPath := filepath.Join(outDir, "eval_report.md")
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(jsonPath, []byte(RedactSecrets(string(raw))), 0o600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(mdPath, []byte(RedactSecrets(renderEvalMarkdown(report))), 0o600); err != nil {
		return "", "", err
	}
	return jsonPath, mdPath, nil
}

func renderEvalMarkdown(report EvalReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Review Evaluation\n\n")
	fmt.Fprintf(&b, "- Fixtures: %d\n", report.FixtureCount)
	fmt.Fprintf(&b, "- Recall: %.4f (%d/%d)\n", report.Recall, report.MatchedRules, report.ExpectedRules)
	fmt.Fprintf(&b, "- High-risk recall: %.4f (%d/%d)\n", report.HighRiskRecall, report.MatchedHighRules, report.ExpectedHighRules)
	fmt.Fprintf(&b, "- False positive rate: %.4f (%d/%d)\n", report.FalsePositiveRate, report.FalsePositiveRules, report.ReportedRules)
	fmt.Fprintf(&b, "- Redaction rate: %.4f (%d/%d checks clean)\n", report.RedactionRate, report.SecretValueChecks-report.SecretValueLeaks, report.SecretValueChecks)
	fmt.Fprintf(&b, "- Duration ms: %d\n", report.DurationMS)
	fmt.Fprintf(&b, "- Threshold pass: %t\n\n", report.PassedHiddenThreshold)
	fmt.Fprintf(&b, "## Fixtures\n\n")
	for _, result := range report.Results {
		fmt.Fprintf(&b, "- `%s`: matched=%s missing=%s false_positives=%s leaks=%d\n",
			result.Name,
			strings.Join(result.MatchedRules, ","),
			strings.Join(result.MissingRules, ","),
			strings.Join(result.FalsePositives, ","),
			len(result.SecretLeaks),
		)
	}
	return b.String()
}

func ratio(n int, d int) float64 {
	if d == 0 {
		return 1
	}
	return float64(n) / float64(d)
}

func intersection(expected []string, actual map[string]struct{}) []string {
	out := []string{}
	for _, rule := range expected {
		if _, ok := actual[rule]; ok {
			out = append(out, rule)
		}
	}
	sort.Strings(out)
	return out
}

func missing(expected []string, actual map[string]struct{}) []string {
	out := []string{}
	for _, rule := range expected {
		if _, ok := actual[rule]; !ok {
			out = append(out, rule)
		}
	}
	sort.Strings(out)
	return out
}

func stringSet(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, s := range in {
		out[s] = struct{}{}
	}
	return out
}

func sortedKeys(in map[string]struct{}) []string {
	out := make([]string, 0, len(in))
	for k := range in {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
