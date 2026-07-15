//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cragent "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/agent"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage/sqlite"
)

type reportFixture struct {
	Findings []findingExpectation
	Warnings []findingExpectation
	Secrets  []string
}

type findingExpectation struct {
	RuleID   string
	Severity string
	Status   string
}

func TestAllFixturesMatchExpectedReviewResults(t *testing.T) {
	if os.Getenv("CR_AGENT_RUN_FIXTURE_MATRIX_TEST") != "1" {
		t.Skip("set CR_AGENT_RUN_FIXTURE_MATRIX_TEST=1 to run the full fixture matrix")
	}
	root := filepath.Join("..", "..", "testdata", "fixtures")
	cases := map[string]reportFixture{
		"safe.diff": {},
		"secret.diff": {
			Findings: []findingExpectation{{RuleID: "secret-leak", Severity: "critical", Status: "finding"}},
			Secrets:  []string{"sk-1234567890abcdef"},
		},
		"secret-shapes.diff": {
			Findings: []findingExpectation{
				{RuleID: "secret-leak", Severity: "critical", Status: "finding"},
				{RuleID: "secret-leak", Severity: "critical", Status: "finding"},
				{RuleID: "secret-leak", Severity: "critical", Status: "finding"},
				{RuleID: "secret-leak", Severity: "critical", Status: "finding"},
				{RuleID: "secret-leak", Severity: "critical", Status: "finding"},
				{RuleID: "secret-leak", Severity: "critical", Status: "finding"},
			},
			Secrets: []string{
				"sk-proj-1234567890abcdef",
				"llm-live-1234567890abcdef",
				"sk-1234567890abcdef",
				"github_pat_1234567890abcdef1234567890abcdef",
				"abc.def.ghi",
				"plain-password",
			},
		},
		"panic.diff": {
			Findings: []findingExpectation{{RuleID: "panic-direct", Severity: "high", Status: "finding"}},
		},
		"todo.diff": {
			Findings: []findingExpectation{{RuleID: "todo-marker", Severity: "medium", Status: "finding"}},
		},
		"test-missing.diff": {
			Warnings: []findingExpectation{{RuleID: "missing-test-hint", Severity: "low", Status: "warning"}},
		},
		"missing-test.diff": {
			Warnings: []findingExpectation{{RuleID: "missing-test-hint", Severity: "low", Status: "warning"}},
		},
		"goroutine.diff": {
			Findings: []findingExpectation{{RuleID: "goroutine-leak", Severity: "high", Status: "finding"}},
		},
		"context.diff": {
			Findings: []findingExpectation{{RuleID: "context-leak", Severity: "high", Status: "finding"}},
		},
		"resource.diff": {
			Findings: []findingExpectation{{RuleID: "resource-leak", Severity: "high", Status: "finding"}},
		},
		"db-lifecycle.diff": {
			Findings: []findingExpectation{{RuleID: "db-lifecycle", Severity: "high", Status: "finding"}},
		},
		"http-body.diff": {
			Findings: []findingExpectation{{RuleID: "http-body-close", Severity: "high", Status: "finding"}},
		},
		"sql-string-concat.diff": {
			Findings: []findingExpectation{{RuleID: "sql-string-concat", Severity: "critical", Status: "finding"}},
		},
		"command-injection.diff": {
			Findings: []findingExpectation{{RuleID: "command-injection", Severity: "critical", Status: "finding"}},
		},
		"context-background.diff": {
			Findings: []findingExpectation{{RuleID: "context-background-misuse", Severity: "medium", Status: "finding"}},
		},
		"mutex-unlock.diff": {
			Findings: []findingExpectation{{RuleID: "mutex-unlock-missing", Severity: "high", Status: "finding"}},
		},
		"defer-in-loop.diff": {
			Findings: []findingExpectation{{RuleID: "defer-in-loop", Severity: "medium", Status: "finding"}},
		},
		"bare-return-err.diff": {
			Findings: []findingExpectation{{RuleID: "bare-return-err", Severity: "medium", Status: "finding"}},
		},
		"string-concat-loop.diff": {
			Warnings: []findingExpectation{{RuleID: "string-concat-loop", Severity: "low", Status: "needs_human_review"}},
		},
		"dedupe.diff": {
			Findings: []findingExpectation{
				{RuleID: "panic-direct", Severity: "high", Status: "finding"},
				{RuleID: "panic-direct", Severity: "high", Status: "finding"},
			},
		},
		"realistic-service-risk.diff": {
			Findings: []findingExpectation{
				{RuleID: "secret-leak", Severity: "critical", Status: "finding"},
				{RuleID: "context-leak", Severity: "high", Status: "finding"},
				{RuleID: "resource-leak", Severity: "high", Status: "finding"},
				{RuleID: "panic-direct", Severity: "high", Status: "finding"},
				{RuleID: "db-lifecycle", Severity: "high", Status: "finding"},
				{RuleID: "panic-direct", Severity: "high", Status: "finding"},
				{RuleID: "goroutine-leak", Severity: "high", Status: "finding"},
				{RuleID: "todo-marker", Severity: "medium", Status: "finding"},
			},
			Warnings: []findingExpectation{
				{RuleID: "missing-test-hint", Severity: "low", Status: "warning"},
				{RuleID: "string-concat-loop", Severity: "low", Status: "needs_human_review"},
			},
			Secrets: []string{"sk-live-realistic1234567890abcdef"},
		},
		"sandbox-fail.diff":    {},
		"sandbox-timeout.diff": {},
	}

	for fileName, fixture := range cases {
		t.Run(fileName, func(t *testing.T) {
			result := runFixture(t, root, fileName)
			assertExpectedFindings(t, result.Findings, fixture.Findings)
			assertExpectedFindings(t, result.Warnings, fixture.Warnings)
			assertSecretsRedacted(t, result, fixture.Secrets)
			if fileName == "safe.diff" && len(result.Findings) != 0 {
				t.Fatalf("safe fixture should not produce findings, got %d", len(result.Findings))
			}
			if fileName == "dedupe.diff" && len(result.Findings) != 2 {
				t.Fatalf("different lines must keep two findings, got %d", len(result.Findings))
			}
		})
	}
}

func TestRunCanUseFixtureName(t *testing.T) {
	out := t.TempDir()
	if err := Run(Options{
		Fixture:      "secret.diff",
		FixturesRoot: filepath.Join("..", "..", "testdata", "fixtures"),
		OutputDir:    out,
		Mode:         cragent.ModeRuleOnly,
		Runtime:      cragent.RuntimeLocalFallback,
		SkillsRoot:   filepath.Join("..", "..", "skills"),
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	jsonPath := filepath.Join(out, "review_report.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read report json: %v", err)
	}
	if !strings.Contains(string(data), "secret-leak") {
		t.Fatalf("expected fixture report to include secret finding, got %s", data)
	}
}

func TestAcceptanceEvidenceReportsAndSQLiteReplay(t *testing.T) {
	out := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	if err := Run(Options{
		Fixture:      "secret-shapes.diff",
		FixturesRoot: filepath.Join("..", "..", "testdata", "fixtures"),
		OutputDir:    out,
		SQLitePath:   dbPath,
		Mode:         cragent.ModeRuleOnly,
		Runtime:      cragent.RuntimeLocalFallback,
		SkillsRoot:   filepath.Join("..", "..", "skills"),
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	reportPath := filepath.Join(out, "review_report.json")
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read json report: %v", err)
	}
	var result reportData
	if err := json.Unmarshal(reportBytes, &result); err != nil {
		t.Fatalf("unmarshal report json: %v", err)
	}
	assertReportAcceptanceContract(t, string(reportBytes), result)

	if _, err := os.Stat(filepath.Join(out, "review_report.md")); err != nil {
		t.Fatalf("expected markdown report: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "review_diagnostics.json")); err != nil {
		t.Fatalf("expected diagnostics report: %v", err)
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	task, err := store.TaskByID(ctx, result.TaskID)
	if err != nil {
		t.Fatalf("query task by id: %v", err)
	}
	if task.ID != result.TaskID || task.Status != "done" {
		t.Fatalf("unexpected task replay record: %+v", task)
	}
	if findings, err := store.FindingsByTaskID(ctx, result.TaskID); err != nil || len(findings) == 0 {
		t.Fatalf("expected replayable findings, findings=%+v err=%v", findings, err)
	}
	if decisions, err := store.DecisionsByTaskID(ctx, result.TaskID); err != nil || len(decisions) == 0 {
		t.Fatalf("expected permission decisions, decisions=%+v err=%v", decisions, err)
	}
	if runs, err := store.SandboxRunsByTaskID(ctx, result.TaskID); err != nil || len(runs) == 0 {
		t.Fatalf("expected sandbox runs, runs=%+v err=%v", runs, err)
	}
	if filters, err := store.FilterDecisionsByTaskID(ctx, result.TaskID); err != nil || len(filters) == 0 {
		t.Fatalf("expected filter decisions, filters=%+v err=%v", filters, err)
	}
	if artifacts, err := store.ArtifactsByTaskID(ctx, result.TaskID); err != nil || len(artifacts) < 3 {
		t.Fatalf("expected report artifacts, artifacts=%+v err=%v", artifacts, err)
	}
	if metrics, err := store.MetricsByTaskID(ctx, result.TaskID); err != nil || metrics.FindingCount == 0 {
		t.Fatalf("expected metrics replay, metrics=%+v err=%v", metrics, err)
	}
	if report, err := store.ReportByTaskID(ctx, result.TaskID); err != nil || len(report.JSON) == 0 || len(report.Markdown) == 0 {
		t.Fatalf("expected stored reports, report=%+v err=%v", report, err)
	}
}

func runFixture(t *testing.T, root, name string) reportData {
	t.Helper()
	out := t.TempDir()
	if err := Run(Options{
		DiffFile:   filepath.Join(root, name),
		OutputDir:  out,
		Mode:       cragent.ModeRuleOnly,
		Runtime:    cragent.RuntimeLocalFallback,
		SkillsRoot: filepath.Join("..", "..", "skills"),
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	jsonPath := filepath.Join(out, "review_report.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read report json: %v", err)
	}
	var result reportData
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal report json: %v", err)
	}
	return result
}

type reportData struct {
	TaskID             string              `json:"task_id"`
	Summary            string              `json:"summary"`
	Findings           []findingData       `json:"findings"`
	Warnings           []findingData       `json:"warnings"`
	HumanReviewItems   []findingData       `json:"human_review_items"`
	GovernanceSummary  json.RawMessage     `json:"governance_summary"`
	SandboxSummary     json.RawMessage     `json:"sandbox_summary"`
	Metrics            metricsData         `json:"metrics"`
	Artifacts          []map[string]string `json:"artifacts"`
	Conclusion         json.RawMessage     `json:"conclusion"`
	InputMetadata      json.RawMessage     `json:"input_metadata"`
	DiagnosticsVersion string              `json:"diagnostics_version,omitempty"`
}

type findingData struct {
	RuleID   string `json:"rule_id"`
	Severity string `json:"severity"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
	Source   string `json:"source"`
}

func assertExpectedFindings(t *testing.T, got []findingData, want []findingExpectation) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %d results, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i].RuleID != want[i].RuleID {
			t.Fatalf("result[%d] rule_id = %q, want %q", i, got[i].RuleID, want[i].RuleID)
		}
		if got[i].Severity != want[i].Severity {
			t.Fatalf("result[%d] severity = %q, want %q", i, got[i].Severity, want[i].Severity)
		}
		if got[i].Status != want[i].Status {
			t.Fatalf("result[%d] status = %q, want %q", i, got[i].Status, want[i].Status)
		}
	}
}

func assertSecretsRedacted(t *testing.T, result reportData, secrets []string) {
	t.Helper()
	if len(secrets) == 0 {
		return
	}
	for _, item := range append(result.Findings, result.Warnings...) {
		for _, secret := range secrets {
			if strings.Contains(item.Evidence, secret) {
				t.Fatalf("evidence still contains secret %q: %q", secret, item.Evidence)
			}
		}
	}
}

func TestFakeModelHoldoutFixtureProducesIncrementalFinding(t *testing.T) {
	result := runHoldoutFixture(t, "model-semantic.diff")
	if result.Metrics.ModelCallCount != 1 {
		t.Fatalf("expected one model call, got %+v", result.Metrics)
	}
	if !hasReportFinding(result.Findings, "fake-model-semantic-risk", "fake_model") {
		t.Fatalf("expected incremental fake model finding, got %+v", result.Findings)
	}
}

func TestFakeModelSemanticHoldoutFixturesProduceDistinctIncrementalFindings(t *testing.T) {
	tests := []struct {
		fixture string
		ruleID  string
	}{
		{fixture: "model-authz-bypass.diff", ruleID: "fake-model-authz-bypass"},
		{fixture: "model-nil-boundary.diff", ruleID: "fake-model-nil-boundary"},
		{fixture: "model-state-inconsistency.diff", ruleID: "fake-model-state-inconsistency"},
		{fixture: "model-transaction-semantic.diff", ruleID: "fake-model-transaction-semantic"},
		{fixture: "model-error-swallow.diff", ruleID: "fake-model-error-swallow"},
	}
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			result := runHoldoutFixture(t, tc.fixture)
			if result.Metrics.ModelCallCount != 1 {
				t.Fatalf("expected one model call, got %+v", result.Metrics)
			}
			if len(result.Warnings) != 0 {
				t.Fatalf("expected semantic holdout to route high-confidence model output to findings only, warnings=%+v", result.Warnings)
			}
			if !hasReportFinding(result.Findings, tc.ruleID, "fake_model") {
				t.Fatalf("expected %s from fake model, findings=%+v", tc.ruleID, result.Findings)
			}
			for _, finding := range result.Findings {
				if finding.Source != "fake_model" {
					t.Fatalf("semantic holdout should not depend on deterministic findings, got %+v", result.Findings)
				}
			}
		})
	}
}

func TestFakeModelSafeSemanticHoldoutDoesNotProduceFindings(t *testing.T) {
	result := runHoldoutFixture(t, "model-safe-semantic.diff")
	if result.Metrics.ModelCallCount != 1 {
		t.Fatalf("expected one model call, got %+v", result.Metrics)
	}
	if len(result.Findings) != 0 || len(result.Warnings) != 0 {
		t.Fatalf("expected safe semantic holdout to stay clean, findings=%+v warnings=%+v", result.Findings, result.Warnings)
	}
}

func TestPublicFixtureRuleOnlyDoesNotRunModelReview(t *testing.T) {
	result := runFixtureWithMode(t, filepath.Join("..", "..", "testdata", "fixtures", "safe.diff"), cragent.ModeRuleOnly)
	if result.Metrics.ModelCallCount != 0 {
		t.Fatalf("rule-only public fixture should not call model review, got %+v", result.Metrics)
	}
	for _, finding := range append(result.Findings, result.Warnings...) {
		if finding.Source == "fake_model" {
			t.Fatalf("rule-only public fixture should not contain fake model output, got %+v", result)
		}
	}
}

func runHoldoutFixture(t *testing.T, fixture string) reportData {
	t.Helper()
	return runFixtureWithMode(t, filepath.Join("..", "..", "testdata", "holdout", fixture), cragent.ModeFakeModel)
}

func runFixtureWithMode(t *testing.T, diffFile string, mode string) reportData {
	t.Helper()
	out := t.TempDir()
	if err := Run(Options{
		DiffFile:   diffFile,
		OutputDir:  out,
		Mode:       mode,
		Runtime:    cragent.RuntimeLocalFallback,
		SkillsRoot: filepath.Join("..", "..", "skills"),
	}); err != nil {
		t.Fatalf("Run returned error for %s: %v", diffFile, err)
	}
	data, err := os.ReadFile(filepath.Join(out, "review_report.json"))
	if err != nil {
		t.Fatalf("read report json for %s: %v", diffFile, err)
	}
	var result reportData
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal report json for %s: %v", diffFile, err)
	}
	return result
}

func hasReportFinding(findings []findingData, ruleID, source string) bool {
	for _, finding := range findings {
		if finding.RuleID == ruleID && finding.Source == source {
			return true
		}
	}
	return false
}

func assertReportAcceptanceContract(t *testing.T, jsonText string, result reportData) {
	t.Helper()
	if result.TaskID == "" || result.Summary == "" {
		t.Fatalf("report missing task id or summary: %+v", result)
	}
	for _, want := range []string{
		`"findings"`,
		`"severity_counts"`,
		`"human_review_items"`,
		`"governance_summary"`,
		`"metrics"`,
		`"sandbox_summary"`,
		`"recommendation"`,
		`"artifacts"`,
		`"conclusion"`,
	} {
		if !strings.Contains(jsonText, want) {
			t.Fatalf("report missing acceptance field %s: %s", want, jsonText)
		}
	}
	if len(result.Findings) == 0 || result.Metrics.FindingCount == 0 || len(result.Artifacts) < 3 {
		t.Fatalf("report missing replayable findings, metrics, or artifacts: %+v", result)
	}
}

type metricsData struct {
	FindingCount   int            `json:"finding_count"`
	ModelCallCount int            `json:"model_call_count"`
	SeverityCounts map[string]int `json:"severity_counts"`
}
