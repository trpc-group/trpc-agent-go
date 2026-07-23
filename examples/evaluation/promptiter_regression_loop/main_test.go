//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

func TestSamplePipelineRunsAllSixCasesWithoutAPIKey(t *testing.T) {
	outputDir := t.TempDir()
	promptBefore, err := os.ReadFile("baseline_prompt.txt")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := run(ctx, "data", "promptiter.json", "baseline_prompt.txt", outputDir); err != nil {
		t.Fatalf("run sample pipeline: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 3*time.Minute {
		t.Fatalf("pipeline exceeded three-minute requirement: %s", elapsed)
	}

	data, err := os.ReadFile(filepath.Join(outputDir, regression.JSONReportName))
	if err != nil {
		t.Fatal(err)
	}
	var report regression.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("decode JSON report: %v", err)
	}
	if report.SchemaVersion != "1.1" || report.ModelConfig.Version != "1.1.0" {
		t.Fatalf("unexpected report/engine schema versions: %q/%q", report.SchemaVersion, report.ModelConfig.Version)
	}
	if len(report.Baseline.Train.Cases) != 3 || len(report.Baseline.Validation.Cases) != 3 {
		t.Fatalf("expected three train and three validation cases, got %d and %d",
			len(report.Baseline.Train.Cases), len(report.Baseline.Validation.Cases))
	}
	if len(report.Rounds) != 2 {
		t.Fatalf("expected two optimization rounds, got %d", len(report.Rounds))
	}
	if report.Rounds[0].GateDecision.Accepted {
		t.Fatal("overfit round must be rejected")
	}
	if !report.Rounds[1].GateDecision.Accepted || !report.GateDecision.Accepted {
		t.Fatal("balanced round must be accepted")
	}
	if report.CandidatePrompt.ID != "candidate-balanced" {
		t.Fatalf("unexpected selected candidate %q", report.CandidatePrompt.ID)
	}
	for _, summary := range []regression.EvaluationSummary{
		report.Baseline.Train,
		report.Baseline.Validation,
		report.Rounds[0].Evaluation.Train,
		report.Rounds[0].Evaluation.Validation,
		report.Rounds[1].Evaluation.Train,
		report.Rounds[1].Evaluation.Validation,
	} {
		for _, evalCase := range summary.Cases {
			if evalCase.ResponseVariantID == "" || len(evalCase.ResponsePromptSHA256) != 64 {
				t.Fatalf("case %q has incomplete response provenance: %+v", evalCase.CaseID, evalCase)
			}
			if evalCase.UsedFallback || evalCase.ResponseVariantID != summary.VariantID {
				t.Fatalf("case %q was not explicitly rerun for %q: %+v", evalCase.CaseID, summary.VariantID, evalCase)
			}
			if !evalCase.Passed && len(evalCase.FailureAttributions) == 0 {
				t.Fatalf("failed case %q has no attribution", evalCase.CaseID)
			}
		}
	}
	noGainOutcomes := make(map[string]regression.DeltaOutcome)
	for _, caseDelta := range append(report.Delta.Train.Cases, report.Delta.Validation.Cases...) {
		noGainOutcomes[caseDelta.CaseID] = caseDelta.Outcome
	}
	for _, caseID := range []string{"train_knowledge_no_gain", "validation_coupon_no_gain"} {
		if noGainOutcomes[caseID] != regression.DeltaUnchangedFailure {
			t.Fatalf("no-gain case %q outcome = %q, want unchanged_failure", caseID, noGainOutcomes[caseID])
		}
	}
	if _, err := os.Stat(filepath.Join(outputDir, regression.MarkdownReportName)); err != nil {
		t.Fatal(err)
	}
	promptAfter, err := os.ReadFile("baseline_prompt.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(promptBefore) != string(promptAfter) {
		t.Fatal("pipeline unexpectedly rewrote source prompt")
	}
}

func TestSamplePipelineRunsInTraceReplayMode(t *testing.T) {
	configData, err := os.ReadFile("promptiter.json")
	if err != nil {
		t.Fatal(err)
	}
	traceConfig := strings.Replace(string(configData), `"mode": "fake"`, `"mode": "trace"`, 1)
	configPath := filepath.Join(t.TempDir(), "promptiter-trace.json")
	if err := os.WriteFile(configPath, []byte(traceConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	outputDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := run(ctx, "data", configPath, "baseline_prompt.txt", outputDir); err != nil {
		t.Fatalf("run trace replay pipeline: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(outputDir, regression.JSONReportName))
	if err != nil {
		t.Fatal(err)
	}
	var report regression.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Mode != "trace" || !report.GateDecision.Accepted {
		t.Fatalf("unexpected trace report mode/decision: %q/%v", report.Mode, report.GateDecision.Accepted)
	}
}

func TestCommittedSampleReportsMatchCurrentPipeline(t *testing.T) {
	loadReport := func(path string) regression.Report {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var report regression.Report
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return report
	}

	committedJSONPath := filepath.Join("sample_output", regression.JSONReportName)
	committedMarkdownPath := filepath.Join("sample_output", regression.MarkdownReportName)
	committed := loadReport(committedJSONPath)
	committedMarkdown, err := os.ReadFile(committedMarkdownPath)
	if err != nil {
		t.Fatal(err)
	}
	renderedCommitted, err := regression.RenderMarkdown(&committed)
	if err != nil {
		t.Fatal(err)
	}
	if string(committedMarkdown) != renderedCommitted {
		t.Fatal("committed Markdown report is not rendered from the committed JSON report")
	}

	outputDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := run(ctx, "data", "promptiter.json", "baseline_prompt.txt", outputDir); err != nil {
		t.Fatalf("run sample pipeline: %v", err)
	}
	current := loadReport(filepath.Join(outputDir, regression.JSONReportName))
	currentMarkdown, err := os.ReadFile(filepath.Join(outputDir, regression.MarkdownReportName))
	if err != nil {
		t.Fatal(err)
	}
	renderedCurrent, err := regression.RenderMarkdown(&current)
	if err != nil {
		t.Fatal(err)
	}
	if string(currentMarkdown) != renderedCurrent {
		t.Fatal("generated Markdown report is not rendered from the generated JSON report")
	}

	normalizeReportTimes(&committed)
	normalizeReportTimes(&current)
	if !reflect.DeepEqual(committed, current) {
		t.Fatal("committed sample report differs from a fresh deterministic run after normalizing wall-clock fields")
	}
}

func normalizeReportTimes(report *regression.Report) {
	report.StartedAt = time.Time{}
	report.CompletedAt = time.Time{}
	report.WallTimeMS = 0
}
