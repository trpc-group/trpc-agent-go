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
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/regressionloop/internal/config"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/regressionloop/internal/fakeenv"
)

var updateGolden = flag.Bool("update", false, "update expected optimization reports")

func TestPipelineConfigUsesOptimizationMinScoreGain(t *testing.T) {
	cfg := &config.Config{}
	cfg.Optimization.MinScoreGain = 0.125
	cfg.Evaluation.ExpectedAgentName = "candidate"
	if got := pipelineConfig(cfg).PromptIterMinScoreGain; got != 0.125 {
		t.Fatalf("PromptIterMinScoreGain = %v, want 0.125", got)
	}
	if got := pipelineConfig(cfg).ExpectedAgentName; got != "candidate" {
		t.Fatalf("ExpectedAgentName = %q, want candidate", got)
	}
}

func TestDeterministicEndToEnd(t *testing.T) {
	wallStart := time.Now()
	configPath := filepath.Join("data", "promptiter-recap-app", "promptiter.json")
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile(filepath.Join(filepath.Dir(configPath), cfg.Prompt.SourceFile))
	if err != nil {
		t.Fatal(err)
	}
	environment, err := fakeenv.New(context.Background(), filepath.Dir(configPath), string(prompt), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer environment.Close()
	output := t.TempDir()
	writer, err := regression.NewFileArtifactWriterWithInputs(output,
		filepath.Join(filepath.Dir(configPath), cfg.Prompt.SourceFile), filepath.Join(filepath.Dir(configPath), cfg.Evaluation.TrainFile),
		filepath.Join(filepath.Dir(configPath), cfg.Evaluation.ValidationFile), filepath.Join(filepath.Dir(configPath), cfg.Evaluation.MetricsFile),
	)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	times := []time.Time{start, start.Add(250 * time.Millisecond)}
	report, err := regression.Run(context.Background(), regression.Options{
		Config: pipelineConfig(cfg), Engine: environment.Engine, Evaluator: environment.Evaluator,
		Meter: environment.Evaluator, InitialProfile: environment.InitialProfile, Artifacts: writer,
		Now: func() time.Time {
			value := times[0]
			times = times[1:]
			return value
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(wallStart) > 3*time.Minute {
		t.Fatal("pipeline exceeded three minutes")
	}
	if len(report.Rounds) != 3 || !report.Rounds[0].ReleaseGate.Accepted || report.Rounds[1].ReleaseGate.Accepted || report.Rounds[2].ReleaseGate.Accepted {
		t.Fatalf("unexpected release decisions: %#v", report.Rounds)
	}
	for _, round := range report.Rounds {
		if !round.PromptIterAccepted {
			t.Fatalf("PromptIter did not expose round %d candidate", round.Round)
		}
	}
	if !report.WriteBack.RecommendedForWriteBack || report.WriteBack.Performed || report.WriteBack.AcceptedProfileRef != "round_1/candidate_profile.json" {
		t.Fatalf("unexpected write-back decision: %#v", report.WriteBack)
	}
	if report.Baseline.Validation.Resources.Usage.ModelCalls == 0 || report.Rounds[0].Validation.Resources.Usage.ModelCalls == 0 {
		t.Fatalf("missing per-profile resource measurements: baseline=%#v candidate=%#v", report.Baseline.Validation.Resources, report.Rounds[0].Validation.Resources)
	}
	if len(report.FailureAttributionStats.Rounds) != 3 || len(report.FailureAttributionStats.BaselineTrain) == 0 {
		t.Fatalf("missing failure attribution statistics: %#v", report.FailureAttributionStats)
	}
	if got := report.Rounds[2].Delta.AgainstLastReleased.ScoreDelta; got >= 0 {
		t.Fatalf("round 3 was not compared with the last released profile: delta=%v", got)
	}
	var roundCaseRuns, roundModelCalls, roundToolCalls int
	var roundLatency, roundCost float64
	for _, round := range report.Rounds {
		if round.Usage.ModelCalls != 17 {
			t.Fatalf("round %d model calls = %d, want 16 evaluation calls plus 1 optimizer call", round.Round, round.Usage.ModelCalls)
		}
		roundCaseRuns += round.Usage.EvaluationCaseRuns
		roundModelCalls += round.Usage.ModelCalls
		roundToolCalls += round.Usage.ToolCalls
		roundLatency += round.LatencySeconds
		roundCost += round.EstimatedCost.Amount
	}
	if roundCaseRuns != report.Usage.EvaluationCaseRuns || roundModelCalls != report.Usage.ModelCalls || roundToolCalls != report.Usage.ToolCalls {
		t.Fatalf("round usage does not sum to report usage: rounds=(%d,%d,%d), report=%#v", roundCaseRuns, roundModelCalls, roundToolCalls, report.Usage)
	}
	if !closeEnough(roundLatency, report.LatencySeconds) || !closeEnough(roundCost, report.EstimatedCost.Amount) {
		t.Fatalf("round resources do not sum to report totals: latency=%v/%v cost=%v/%v", roundLatency, report.LatencySeconds, roundCost, report.EstimatedCost.Amount)
	}
	for _, path := range []string{
		"baseline/input_profile.json", "baseline/train_evaluation.json", "baseline/validation_evaluation.json",
		"round_1/input_profile.json", "round_1/candidate_profile.json", "round_1/train_evaluation.json",
		"round_1/validation_evaluation.json", "round_1/delta.json", "round_1/gate.json",
	} {
		if _, err := os.Stat(filepath.Join(output, filepath.FromSlash(path))); err != nil {
			t.Errorf("missing artifact %s: %v", path, err)
		}
	}
	baseline, err := os.ReadFile(filepath.Join(output, "baseline", "validation_evaluation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(baseline, []byte("tooltrajectory")) || !bytes.Contains(baseline, []byte("AppliedSurfaceIDs")) {
		t.Fatal("baseline artifact does not contain metric and trace evidence")
	}
	for _, name := range []string{"optimization_report.json", "optimization_report.md"} {
		actual, err := os.ReadFile(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		expectedPath := filepath.Join("expected", name)
		if *updateGolden {
			if err := os.WriteFile(expectedPath, actual, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		expected, err := os.ReadFile(expectedPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, expected) {
			t.Errorf("%s differs from golden; run go test . -update", name)
		}
	}
}

func closeEnough(left, right float64) bool {
	delta := left - right
	return delta > -1e-12 && delta < 1e-12
}

func TestAddedEvalCaseChangesReport(t *testing.T) {
	baseDir := copyExampleData(t)
	configPath := filepath.Join(baseDir, "promptiter.json")
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	validationPath := filepath.Join(baseDir, cfg.Evaluation.ValidationFile)
	payload, err := os.ReadFile(validationPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	cases := document["evalCases"].([]any)
	encoded, err := json.Marshal(cases[0])
	if err != nil {
		t.Fatal(err)
	}
	var added map[string]any
	if err := json.Unmarshal(encoded, &added); err != nil {
		t.Fatal(err)
	}
	added["evalId"] = "val-04"
	document["evalCases"] = append(cases, added)
	updated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(validationPath, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile(filepath.Join(baseDir, cfg.Prompt.SourceFile))
	if err != nil {
		t.Fatal(err)
	}
	environment, err := fakeenv.New(context.Background(), baseDir, string(prompt), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer environment.Close()
	writer, err := regression.NewFileArtifactWriter(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	report, err := regression.Run(context.Background(), regression.Options{
		Config: pipelineConfig(cfg), Engine: environment.Engine, Evaluator: environment.Evaluator,
		Meter: environment.Evaluator, InitialProfile: environment.InitialProfile, Artifacts: writer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Baseline.Validation.PerCase) != 4 {
		t.Fatalf("report contains %d validation cases, want 4", len(report.Baseline.Validation.PerCase))
	}
}

func copyExampleData(t *testing.T) string {
	t.Helper()
	source := filepath.Join("data", "promptiter-recap-app")
	target := filepath.Join(t.TempDir(), "promptiter-recap-app")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"baseline.prompt.txt", "promptiter.json", "recap.metrics.json", "train.evalset.json", "validation.evalset.json"} {
		payload, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, name), payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return target
}
