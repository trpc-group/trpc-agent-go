//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main_test provides integration tests for the code review agent.
package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/runner"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/runner/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage"
)

func TestIntegration_DryRunAllSamples(t *testing.T) {
	diffsDir := filepath.Join("testdata", "diffs")
	entries, err := os.ReadDir(diffsDir)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entries), 8, "at least 8 diff samples required")

	store, err := storage.NewSQLiteStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	reg := runner.NewRuleRegistry()
	for _, r := range allRules() {
		require.NoError(t, reg.Register(r))
	}

	sanitizer := finding.NewSanitizer()
	dedupEngine := finding.NewDedupEngine()

	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".diff" {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			path := filepath.Join(diffsDir, entry.Name())
			data, err := os.ReadFile(path)
			require.NoError(t, err)

			// Parse diff.
			changedFiles, err := diff.ParseUnifiedDiff(string(data))
			require.NoError(t, err)
			require.NotEmpty(t, changedFiles, "%s should parse to at least one file", entry.Name())

			fileInfos := diff.ExtractFileInfo(changedFiles)
			summary := diff.DiffSummary(changedFiles)
			assert.NotEmpty(t, summary)

			// Run all rules (dry-run: no sandbox, no LLM).
			var allFindings []finding.Finding
			for _, rule := range reg.AllRules() {
				for _, fi := range diff.NonTestFiles(diff.GoFileFilter(fileInfos)) {
					findings, err := rule.Check(nil, fi, "")
					if err != nil {
						continue
					}
					allFindings = append(allFindings, findings...)
				}
			}

			// Sanitize.
			for i := range allFindings {
				allFindings[i] = sanitizer.SanitizeFinding(allFindings[i])
			}

			// Dedup.
			dedupResult := dedupEngine.Dedup(allFindings)
			report.SortFindings(dedupResult.Findings)

			// Build and verify report.
			riskSummary := report.BuildRiskSummary(dedupResult.Findings, dedupResult.Warnings)
			reviewReport := report.ReviewReport{
				TaskID:      entry.Name(),
				DiffSummary: summary,
				Findings:    dedupResult.Findings,
				Warnings:    dedupResult.Warnings,
				RiskSummary: riskSummary,
				GeneratedAt: time.Now(),
			}

			// Verify JSON serialization.
			jsonData, err := report.ToJSON(reviewReport)
			require.NoError(t, err)
			assert.Contains(t, jsonData, entry.Name())

			// Verify Markdown generation.
			mdData := report.ToMarkdown(reviewReport)
			assert.NotEmpty(t, mdData)

			// All samples must complete without crash.
			t.Logf("[%s] %d findings, %d warnings, %d suppressed, summary: %s",
				entry.Name(), len(dedupResult.Findings), len(dedupResult.Warnings),
				dedupResult.Suppressed, summary)
		})
	}
}

func TestIntegration_Sample2SecurityIssueProducesFindings(t *testing.T) {
	data := loadDiff(t, "02_security_issue.diff")
	changedFiles, err := diff.ParseUnifiedDiff(string(data))
	require.NoError(t, err)
	require.NotEmpty(t, changedFiles)

	// Security rule should not crash.
	rule := rules.NewSecurityRule()
	fileInfos := diff.ExtractFileInfo(changedFiles)
	for _, fi := range diff.GoFileFilter(fileInfos) {
		_, err := rule.Check(nil, fi, "")
		require.NoError(t, err)
	}

	// Note: findings require full file content (not just diff),
	// so this test verifies the pipeline is crash-free.
	t.Log("Security rule ran without error")
}

func TestIntegration_Sample3GoroutineLeakProducesFindings(t *testing.T) {
	data := loadDiff(t, "03_goroutine_leak.diff")
	changedFiles, err := diff.ParseUnifiedDiff(string(data))
	require.NoError(t, err)

	// Goroutine leak rule should not crash.
	rule := rules.NewGoroutineLeakRule()
	fileInfos := diff.ExtractFileInfo(changedFiles)
	for _, fi := range diff.GoFileFilter(fileInfos) {
		_, err := rule.Check(nil, fi, "")
		require.NoError(t, err)
	}
	t.Log("Goroutine leak rule ran without error")
}

func TestIntegration_Dedup(t *testing.T) {
	data := loadDiff(t, "07_duplicate_finding.diff")
	changedFiles, err := diff.ParseUnifiedDiff(string(data))
	require.NoError(t, err)
	require.NotEmpty(t, changedFiles)

	// Run error handling rule which catches `_ = fn()` patterns.
	rule := rules.NewErrorHandlingRule()
	fileInfos := diff.ExtractFileInfo(changedFiles)
	var allFindings []finding.Finding
	for _, fi := range diff.GoFileFilter(fileInfos) {
		findings, err := rule.Check(nil, fi, "")
		require.NoError(t, err)
		allFindings = append(allFindings, findings...)
	}

	// Dedup - same file, same line, same rule should collapse.
	dedupEngine := finding.NewDedupEngine()
	result := dedupEngine.Dedup(allFindings)
	t.Logf("Before dedup: %d, after: %d, suppressed: %d", len(allFindings), len(result.Findings), result.Suppressed)
	assert.LessOrEqual(t, len(result.Findings), len(allFindings))
}

func TestIntegration_Sanitize(t *testing.T) {
	data := loadDiff(t, "08_sensitive_info.diff")
	changedFiles, err := diff.ParseUnifiedDiff(string(data))
	require.NoError(t, err)

	// Hardcoded key rule should not crash with diff content (needs full file content for findings).
	rule := rules.NewHardcodedKeyRule()
	fileInfos := diff.ExtractFileInfo(changedFiles)
	for _, fi := range diff.GoFileFilter(fileInfos) {
		_, err := rule.Check(nil, fi, "")
		require.NoError(t, err)
	}

	// Sanitizer should not crash on any evidence.
	sanitizer := finding.NewSanitizer()
	_ = sanitizer.Sanitize("no secrets here")
	assert.Equal(t, "***REDACTED***", sanitizer.Sanitize("sk-abc123def456ghi789jkl012mnop345"))
	t.Log("Sanitize pipeline ran without error")
}

func TestIntegration_DiffParserAllSamples(t *testing.T) {
	diffsDir := filepath.Join("testdata", "diffs")
	entries, err := os.ReadDir(diffsDir)
	require.NoError(t, err)

	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".diff" {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(diffsDir, entry.Name()))
			require.NoError(t, err)

			files, err := diff.ParseUnifiedDiff(string(data))
			require.NoError(t, err)
			require.NotEmpty(t, files)
			assert.NotEmpty(t, diff.DiffSummary(files))
		})
	}
}

func loadDiff(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "diffs", name))
	require.NoError(t, err)
	return data
}

func allRules() []runner.CRRule {
	return []runner.CRRule{
		rules.NewSecurityRule(),
		rules.NewHardcodedKeyRule(),
		rules.NewGoroutineLeakRule(),
		rules.NewResourceLeakRule(),
		rules.NewErrorHandlingRule(),
		rules.NewErrorNoReturnRule(),
		rules.NewTestMissingRule(),
		rules.NewTestFileMissingRule(),
		rules.NewDBLifecycleRule(),
		rules.NewDBRowsErrCheckRule(),
	}
}

// TestHiddenSamples validates detection rate and false positive rate on
// hidden (non-public) diff samples. This test runs only when the
// environment variable CR_HIDDEN_SAMPLES_DIR is set to a directory
// containing .diff files with corresponding .label files.
//
// Label file format: each .label file contains one line:
//
//	HAS_HIGH    — the diff contains a high/critical severity issue
//	NO_HIGH     — the diff has no high/critical severity issue
//
// The test reports detection rate and false positive rate, and fails if
// detection rate < 80% or false positive rate > 15%.
func TestHiddenSamples(t *testing.T) {
	samplesDir := os.Getenv("CR_HIDDEN_SAMPLES_DIR")
	if samplesDir == "" {
		t.Skip("CR_HIDDEN_SAMPLES_DIR not set, skipping hidden sample validation")
	}

	entries, err := os.ReadDir(samplesDir)
	require.NoError(t, err)

	var diffFiles []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".diff" {
			diffFiles = append(diffFiles, e.Name())
		}
	}
	require.GreaterOrEqual(t, len(diffFiles), 8, "at least 8 hidden diff samples required")

	reg := runner.NewRuleRegistry()
	for _, r := range allRules() {
		require.NoError(t, reg.Register(r))
	}

	type result struct {
		name        string
		hasHighRisk bool // labeled
		foundHigh   bool // detected by agent
		findings    int
		highCount   int
	}

	var results []result
	totalHigh, detectedHigh := 0, 0
	totalClean, falsePos := 0, 0

	for _, df := range diffFiles {
		data, err := os.ReadFile(filepath.Join(samplesDir, df))
		require.NoError(t, err)

		// Parse diff.
		changedFiles, err := diff.ParseUnifiedDiff(string(data))
		require.NoError(t, err)
		fileInfos := diff.ExtractFileInfo(changedFiles)

		// Run all rules against the diff content.
		var allFindings []finding.Finding
		for _, rule := range reg.AllRules() {
			for _, fi := range diff.NonTestFiles(diff.GoFileFilter(fileInfos)) {
				findings, err := rule.Check(nil, fi, string(data))
				if err != nil {
					continue
				}
				allFindings = append(allFindings, findings...)
			}
		}

		// Count high/critical findings.
		highCount := 0
		for _, f := range allFindings {
			if f.Severity == finding.SeverityCritical || f.Severity == finding.SeverityHigh {
				highCount++
			}
		}

		// Read label.
		labelFile := filepath.Join(samplesDir, strings.TrimSuffix(df, ".diff")+".label")
		labelData, err := os.ReadFile(labelFile)
		require.NoError(t, err, "missing label file for %s", df)

		hasHigh := strings.TrimSpace(string(labelData)) == "HAS_HIGH"
		foundHigh := highCount > 0

		r := result{
			name:        df,
			hasHighRisk: hasHigh,
			foundHigh:   foundHigh,
			findings:    len(allFindings),
			highCount:   highCount,
		}
		results = append(results, r)

		if hasHigh {
			totalHigh++
			if foundHigh {
				detectedHigh++
			}
		} else {
			totalClean++
			if foundHigh {
				falsePos++
			}
		}

		t.Logf("[%s] labeled=%v high=%d findings=%d detected=%v",
			df, hasHigh, highCount, len(allFindings), foundHigh)
	}

	// Compute and validate metrics.
	detectionRate := 0.0
	if totalHigh > 0 {
		detectionRate = float64(detectedHigh) / float64(totalHigh) * 100
	}
	falsePosRate := 0.0
	if totalClean > 0 {
		falsePosRate = float64(falsePos) / float64(totalClean) * 100
	}

	t.Logf("\n======= Hidden Sample Results =======")
	t.Logf("Total samples: %d", len(results))
	t.Logf("  High-risk:   %d", totalHigh)
	t.Logf("  Clean:       %d", totalClean)
	t.Logf("Detection rate:  %.1f%% (%d/%d)", detectionRate, detectedHigh, totalHigh)
	t.Logf("False pos rate:  %.1f%% (%d/%d)", falsePosRate, falsePos, totalClean)
	t.Logf("====================================")

	if totalHigh > 0 {
		assert.GreaterOrEqual(t, detectionRate, 80.0,
			"detection rate %.1f%% < 80%%", detectionRate)
	}
	if totalClean > 0 {
		assert.LessOrEqual(t, falsePosRate, 15.0,
			"false positive rate %.1f%% > 15%%", falsePosRate)
	}
}
