//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
	storemodel "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

type expectedFinding struct {
	Fixture  string `json:"fixture"`
	File     string `json:"file"`
	Category string `json:"category"`
	LineMin  int    `json:"line_min"`
	LineMax  int    `json:"line_max"`
	Positive bool   `json:"positive"`
	HighRisk bool   `json:"high_risk"`
}

type qualityCounts struct {
	TP, FP, TN, FN, HighTP, HighFN int
}

const (
	cliPathSecret             = "token=cli-secret-value"
	mainTestFileMode          = 0o600
	maximumFakeReviewDuration = 2 * time.Minute
)

func TestRunFixture(t *testing.T) {
	root := mainExampleRoot(t)
	outputDir := t.TempDir()
	args := []string{"--fixture", "composite", "--runtime", "fake", "--fake-model",
		"--skills-root", filepath.Join(root, "skills"), "--db", filepath.Join(t.TempDir(), "review.db"),
		"--output-dir", outputDir}
	enterMainRoot(t, root)
	started := time.Now()
	if err := run(context.Background(), args); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > maximumFakeReviewDuration {
		t.Fatalf("fake-model review took %s; maximum is %s", elapsed, maximumFakeReviewDuration)
	}
	for _, name := range []string{"review_report.json", "review_report.md"} {
		if _, err := os.Stat(filepath.Join(outputDir, name)); err != nil {
			t.Fatalf("report %q: %v", name, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(outputDir, "review_report.json"))
	if err != nil {
		t.Fatalf("read fixture report: %v", err)
	}
	var snapshot report.Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatalf("decode fixture report: %v", err)
	}
	findings := append(snapshot.Findings, snapshot.Warnings...)
	findings = append(findings, snapshot.NeedsHumanReview...)
	assertCompositeFindings(t, findings)
}

func TestRunPublicFixturesFakeReports(t *testing.T) {
	root := mainExampleRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "fixtures", "expectations.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Scenarios []struct {
			ID, Fixture string
		} `json:"scenarios"`
		Units []expectedFinding `json:"units"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	enterMainRoot(t, root)
	predictions := make(map[string][]reviewmodel.Finding, len(manifest.Scenarios))
	for _, scenario := range manifest.Scenarios {
		t.Run(scenario.ID, func(t *testing.T) {
			outputDir := t.TempDir()
			args := []string{"--diff-file", filepath.Join(root, "fixtures", "diffs", scenario.Fixture+".diff"),
				"--runtime", "fake", "--rule-only",
				"--skills-root", filepath.Join(root, "skills"),
				"--db", filepath.Join(t.TempDir(), "review.db"), "--output-dir", outputDir}
			if err := run(context.Background(), args); err != nil {
				t.Fatalf("run(%q) error = %v", scenario.Fixture, err)
			}
			for _, name := range []string{"review_report.json", "review_report.md"} {
				if info, err := os.Stat(filepath.Join(outputDir, name)); err != nil || info.Size() == 0 {
					t.Fatalf("report %q info=%v error=%v", name, info, err)
				}
			}
			reportData, err := os.ReadFile(filepath.Join(outputDir, "review_report.json"))
			if err != nil {
				t.Fatal(err)
			}
			var snapshot report.Snapshot
			if err := json.Unmarshal(reportData, &snapshot); err != nil {
				t.Fatal(err)
			}
			findings := append(append([]reviewmodel.Finding{}, snapshot.Findings...), snapshot.Warnings...)
			findings = append(findings, snapshot.NeedsHumanReview...)
			hasQualityUnit := false
			for _, unit := range manifest.Units {
				if unit.Fixture != scenario.Fixture {
					continue
				}
				hasQualityUnit = true
				if unit.Positive && countExpected(findings, unit) == 0 {
					t.Fatalf("fixture %q missed unit %#v", scenario.Fixture, unit)
				}
			}
			if hasQualityUnit {
				predictions[scenario.Fixture] = findings
			}
			if scenario.ID == "duplicate_finding" {
				for _, unit := range manifest.Units {
					if unit.Fixture == scenario.Fixture && countExpected(findings, unit) != 1 {
						t.Fatal("duplicate fixture did not normalize to one finding")
					}
				}
			}
			if scenario.ID == "secret_redaction" && redact.ContainsSecret(string(reportData)) {
				t.Fatal("secret fixture report contains plaintext secret")
			}
		})
	}
	assertQuality(t, manifest.Units, predictions)
}

func countExpected(findings []reviewmodel.Finding, unit expectedFinding) int {
	count := 0
	for _, finding := range findings {
		if finding.File == unit.File && finding.Category == unit.Category &&
			finding.Line >= unit.LineMin && finding.Line <= unit.LineMax {
			count++
		}
	}
	return count
}

func assertQuality(t *testing.T, units []expectedFinding, predictions map[string][]reviewmodel.Finding) {
	t.Helper()
	matched := make([]bool, len(units))
	counts := qualityCounts{}
	for fixture, findings := range predictions {
		for _, finding := range findings {
			index := -1
			for candidate, unit := range units {
				if !matched[candidate] && unit.Fixture == fixture && unit.File == finding.File &&
					unit.Category == finding.Category && finding.Line >= unit.LineMin && finding.Line <= unit.LineMax {
					index = candidate
					break
				}
			}
			if index < 0 {
				counts.FP++
				continue
			}
			matched[index] = true
			if !units[index].Positive {
				counts.FP++
				continue
			}
			counts.TP++
			if units[index].HighRisk {
				counts.HighTP++
			}
		}
	}
	for index, unit := range units {
		if matched[index] {
			continue
		}
		if unit.Positive {
			counts.FN++
			if unit.HighRisk {
				counts.HighFN++
			}
		} else {
			counts.TN++
		}
	}
	recall := ratio(counts.HighTP, counts.HighTP+counts.HighFN)
	fpr, fdr := ratio(counts.FP, counts.FP+counts.TN), ratio(counts.FP, counts.TP+counts.FP)
	if recall < 0.80 || fpr > 0.15 || fdr > 0.15 {
		t.Fatalf("quality gate failed: counts=%#v recall=%.3f FPR=%.3f FDR=%.3f", counts, recall, fpr, fdr)
	}
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func reviewArgs(root, runtimeName, databasePath, outputDir string, input ...string) []string {
	return append(input, "--runtime", runtimeName, "--skills-root", filepath.Join(root, "skills"),
		"--db", databasePath, "--output-dir", outputDir)
}

func TestRunRejectsMissingSkill(t *testing.T) {
	root := mainExampleRoot(t)
	enterMainRoot(t, root)
	args := []string{"--fixture", "composite", "--runtime", "fake", "--skills-root", t.TempDir(),
		"--db", filepath.Join(t.TempDir(), "review.db"), "--output-dir", t.TempDir()}
	if err := run(context.Background(), args); err == nil {
		t.Fatal("run() error = nil")
	}
}

func TestRunDiffFilePersistsQueryableReview(t *testing.T) {
	root := mainExampleRoot(t)
	databasePath := filepath.Join(t.TempDir(), "review.db")
	outputDir := t.TempDir()
	args := reviewArgs(root, "fake", databasePath, outputDir,
		"--diff-file", filepath.Join(root, "fixtures", "composite", "input.diff"))
	var output bytes.Buffer
	if err := runWithOutput(context.Background(), args, &output); err != nil {
		t.Fatalf("runWithOutput() error = %v", err)
	}
	review := loadCLIReview(t, databasePath, output.Bytes())
	if review.Task.Status != storemodel.StatusCompleted || len(review.Findings) == 0 ||
		review.Report.JSON == "" || review.Report.Markdown == "" {
		t.Fatalf("persisted review = %#v", review)
	}
}

func TestRunCompositeRealDocker(t *testing.T) {
	if os.Getenv("CODE_REVIEW_DOCKER_TEST") != "1" {
		t.Skip("set CODE_REVIEW_DOCKER_TEST=1 for real Docker acceptance")
	}
	root := mainExampleRoot(t)
	review := runCompositeContainer(t, root)
	if review.Task.Status != storemodel.StatusCompleted || len(review.Runs) != 2 ||
		len(review.Decisions) != 4 || review.Report.JSON == "" || review.Report.Markdown == "" {
		t.Fatalf("container review = %#v", review)
	}
	assertCompositeFindings(t, review.Findings)
	for _, run := range review.Runs {
		if run.Status == "failed" || run.Status == "timeout" {
			t.Fatalf("container fixture run = %#v", run)
		}
	}
}

func runCompositeContainer(t *testing.T, root string) storemodel.Review {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "review.db")
	var output bytes.Buffer
	args := reviewArgs(root, "container", databasePath, t.TempDir(), "--fixture", "composite")
	enterMainRoot(t, root)
	if err := runWithOutput(context.Background(), args, &output); err != nil {
		t.Fatalf("runWithOutput(container) error = %v", err)
	}
	return loadCLIReview(t, databasePath, output.Bytes())
}

func loadCLIReview(t *testing.T, databasePath string, output []byte) storemodel.Review {
	t.Helper()
	var summary struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(output, &summary); err != nil {
		t.Fatalf("decode CLI output: %v", err)
	}
	database, err := storemodel.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open persisted database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close persisted database: %v", err)
		}
	}()
	review, err := database.GetReview(context.Background(), summary.TaskID)
	if err != nil {
		t.Fatalf("GetReview() error = %v", err)
	}
	return review
}

func assertCompositeFindings(t *testing.T, findings []reviewmodel.Finding) {
	t.Helper()
	required := map[string]bool{"security": true, "sensitive_information": true, "context_leak": true,
		"goroutine_leak": true, "resource_lifecycle": true, "error_handling": true,
		"database_lifecycle": true}
	for _, finding := range findings {
		delete(required, finding.Category)
	}
	if len(required) != 0 {
		t.Fatalf("composite fixture missed categories: %v", required)
	}
}

func TestRunRedactsSuccessfulOutputPaths(t *testing.T) {
	root := mainExampleRoot(t)
	outputDir := filepath.Join(t.TempDir(), cliPathSecret)
	args := []string{"--fixture", "composite", "--runtime", "fake", "--skills-root",
		filepath.Join(root, "skills"), "--db", filepath.Join(t.TempDir(), "review.db"),
		"--output-dir", outputDir}
	enterMainRoot(t, root)
	var output bytes.Buffer
	if err := runWithOutput(context.Background(), args, &output); err != nil {
		t.Fatalf("runWithOutput() error = %v", err)
	}
	if strings.Contains(output.String(), cliPathSecret) || !strings.Contains(output.String(), "[REDACTED:") {
		t.Fatalf("CLI output was not redacted: %s", output.String())
	}
}

func TestRunRejectsInvalidCLIAndOutputDirectory(t *testing.T) {
	if err := runWithOutput(context.Background(), nil, &bytes.Buffer{}); err == nil {
		t.Fatal("runWithOutput(no input) error = nil")
	}
	root := mainExampleRoot(t)
	outputFile := filepath.Join(t.TempDir(), "output-file")
	if err := os.WriteFile(outputFile, []byte("not a directory"), mainTestFileMode); err != nil {
		t.Fatalf("write output file: %v", err)
	}
	args := []string{"--fixture", "composite", "--runtime", "fake",
		"--skills-root", filepath.Join(root, "skills"), "--db", filepath.Join(t.TempDir(), "review.db"),
		"--output-dir", outputFile}
	if err := runWithOutput(context.Background(), args, &bytes.Buffer{}); err == nil {
		t.Fatal("runWithOutput(file output directory) error = nil")
	}
}

func mainExampleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(file)
}

func enterMainRoot(t *testing.T, root string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
}
