//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunPipelineDeterministicExample(t *testing.T) {
	outputDir := t.TempDir()
	start := time.Now()
	report, err := runPipeline(context.Background(), pipelineOptions{
		ConfigPath: "data/promptiter.json",
		OutputDir:  outputDir,
	})
	if err != nil {
		t.Fatalf("runPipeline returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 3*time.Minute {
		t.Fatalf("pipeline took %s, want less than 3 minutes", elapsed)
	}
	if len(report.Baseline.Train.Cases) != 3 || len(report.Baseline.Validation.Cases) != 3 {
		t.Fatalf(
			"baseline case counts = train %d validation %d, want 3 and 3",
			len(report.Baseline.Train.Cases),
			len(report.Baseline.Validation.Cases),
		)
	}
	if report.Baseline.Validation.Score != 0.5 {
		t.Fatalf("baseline validation score = %.4f, want 0.5000", report.Baseline.Validation.Score)
	}
	assertFailedMetricsHaveReasons(t, report.Baseline.Train)
	assertFailedMetricsHaveReasons(t, report.Baseline.Validation)
	if report.Candidate.Validation.Score != 1 {
		t.Fatalf("candidate validation score = %.4f, want 1.0000", report.Candidate.Validation.Score)
	}
	if !report.GateDecision.Accepted {
		t.Fatalf("selected candidate was rejected: %v", report.GateDecision.Reasons)
	}
	if len(report.Rounds) != 2 {
		t.Fatalf("round count = %d, want 2", len(report.Rounds))
	}
	if !report.Rounds[0].Decision.Accepted {
		t.Fatalf("round 1 was rejected: %v", report.Rounds[0].Decision.Reasons)
	}
	if report.Rounds[1].Decision.Accepted {
		t.Fatal("overfit round was accepted")
	}
	if report.Rounds[1].Train.Score <= report.Rounds[0].Train.Score {
		t.Fatalf(
			"overfit train score %.4f did not improve over %.4f",
			report.Rounds[1].Train.Score,
			report.Rounds[0].Train.Score,
		)
	}
	if report.Rounds[1].Validation.Score >= report.Rounds[0].Validation.Score {
		t.Fatalf(
			"overfit validation score %.4f did not regress from %.4f",
			report.Rounds[1].Validation.Score,
			report.Rounds[0].Validation.Score,
		)
	}
	for _, name := range []string{reportJSONFile, reportMDFile} {
		if _, err := os.Stat(filepath.Join(outputDir, name)); err != nil {
			t.Fatalf("stat generated report %q: %v", name, err)
		}
	}
}

func TestRunPipelineKeepsBaselineWhenAllCandidatesAreRejected(t *testing.T) {
	sourceConfigPath, err := filepath.Abs("data/promptiter.json")
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	config, err := loadConfig(sourceConfigPath)
	if err != nil {
		t.Fatalf("loadConfig returned error: %v", err)
	}
	config.MaxRounds = 1
	config.Candidates = []candidateConfig{{
		ID:             "rejected-overfit",
		Append:         directiveKnowledgeAll,
		Reason:         "Intentionally over-broad candidate.",
		TargetFailures: []failureCategory{failureRoute},
	}}
	configData, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "promptiter.json")
	if err := os.WriteFile(configPath, configData, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	report, err := runPipeline(context.Background(), pipelineOptions{
		ConfigPath: configPath,
		OutputDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("runPipeline returned error: %v", err)
	}
	if report.GateDecision.Accepted {
		t.Fatal("all-rejected pipeline returned an accepted decision")
	}
	if report.Candidate.CandidateID != "baseline" {
		t.Fatalf("selected candidate = %q, want baseline", report.Candidate.CandidateID)
	}
	if report.Candidate.Prompt != report.Baseline.Prompt {
		t.Fatal("all-rejected pipeline did not preserve the baseline prompt")
	}
	if report.Delta.ScoreDelta != 0 {
		t.Fatalf("all-rejected score delta = %.4f, want 0", report.Delta.ScoreDelta)
	}
}

func TestRunPipelineTopLevelGateUsesBaselineForChainedRounds(t *testing.T) {
	sourceConfigPath, err := filepath.Abs("data/promptiter.json")
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	config, err := loadConfig(sourceConfigPath)
	if err != nil {
		t.Fatalf("loadConfig returned error: %v", err)
	}
	config.MaxRounds = 2
	config.Candidates = []candidateConfig{
		{
			ID:             "lookup-only",
			Append:         directiveLookup,
			Reason:         "Fix lookup routing first.",
			TargetFailures: []failureCategory{failureRoute},
		},
		{
			ID:             "format-only",
			Append:         directiveJSON,
			Reason:         "Fix structured output second.",
			TargetFailures: []failureCategory{failureFormat},
		},
	}
	configData, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "promptiter.json")
	if err := os.WriteFile(configPath, configData, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	report, err := runPipeline(context.Background(), pipelineOptions{
		ConfigPath: configPath,
		OutputDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("runPipeline returned error: %v", err)
	}
	if len(report.Rounds) != 2 {
		t.Fatalf("round count = %d, want 2", len(report.Rounds))
	}
	for _, round := range report.Rounds {
		if !round.Decision.Accepted {
			t.Fatalf("round %d was rejected: %v", round.Round, round.Decision.Reasons)
		}
	}
	if !report.GateDecision.Accepted {
		t.Fatalf("top-level gate rejected: %v", report.GateDecision.Reasons)
	}
	if report.Delta.ScoreDelta != 0.5 {
		t.Fatalf("top-level score delta = %.4f, want 0.5000", report.Delta.ScoreDelta)
	}
	for _, check := range report.GateDecision.Checks {
		if check.Name == "minimum_validation_gain" {
			if !strings.Contains(check.Detail, "gain 0.5000") {
				t.Fatalf("top-level gate detail = %q, want baseline gain 0.5000", check.Detail)
			}
			return
		}
	}
	t.Fatal("top-level decision has no minimum_validation_gain check")
}

func TestSummarizeFailuresCountsAllAttributions(t *testing.T) {
	summary := evaluationSummary{
		Cases: []caseEvaluation{{
			FailureAttributions: []failureAttribution{
				{Category: failureRoute},
				{Category: failureToolCall},
			},
		}},
	}
	counts := summarizeFailures(summary)
	if counts[failureRoute] != 1 {
		t.Fatalf("route failure count = %d, want 1", counts[failureRoute])
	}
	if counts[failureToolCall] != 1 {
		t.Fatalf("tool-call failure count = %d, want 1", counts[failureToolCall])
	}
}

func TestReplaceFileAtomicallyPreservesSourceOnRenameFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt.txt")
	if err := os.WriteFile(path, []byte("baseline\n"), 0o600); err != nil {
		t.Fatalf("write baseline prompt: %v", err)
	}
	renameError := errors.New("rename failed")
	err := replaceFileAtomically(
		path,
		[]byte("candidate\n"),
		func(_, _ string) error {
			return renameError
		},
	)
	if !errors.Is(err, renameError) {
		t.Fatalf("replaceFileAtomically error = %v, want %v", err, renameError)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read baseline prompt: %v", err)
	}
	if string(content) != "baseline\n" {
		t.Fatalf("prompt content = %q, want original baseline", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat baseline prompt: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("prompt mode = %o, want 600", info.Mode().Perm())
	}
	temporary, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".prompt.txt.tmp-*"))
	if err != nil {
		t.Fatalf("find temporary prompt files: %v", err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary prompt files were not removed: %v", temporary)
	}
}

func TestReplaceFileAtomicallyPreservesSourceMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt.txt")
	if err := os.WriteFile(path, []byte("baseline\n"), 0o600); err != nil {
		t.Fatalf("write baseline prompt: %v", err)
	}
	if err := replaceFileAtomically(path, []byte("candidate\n"), os.Rename); err != nil {
		t.Fatalf("replaceFileAtomically returned error: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read replaced prompt: %v", err)
	}
	if string(content) != "candidate\n" {
		t.Fatalf("prompt content = %q, want candidate", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat replaced prompt: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("prompt mode = %o, want 600", info.Mode().Perm())
	}
}

func assertFailedMetricsHaveReasons(t *testing.T, summary evaluationSummary) {
	t.Helper()
	for _, evalCase := range summary.Cases {
		for _, metric := range evalCase.Metrics {
			if !metric.Passed && metric.Reason == "" {
				t.Fatalf(
					"failed metric %q for case %q has no reason",
					metric.Name,
					evalCase.CaseID,
				)
			}
		}
	}
}
