//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression/report"
)

func TestRegressionLoopScenariosUseEvaluationAndPromptIter(t *testing.T) {
	tests := []struct {
		scenario string
		decision regression.Decision
	}{
		{"success", regression.DecisionAccepted},
		{"no-effect", regression.DecisionRejected},
		{"overfit", regression.DecisionRejected},
	}
	for _, test := range tests {
		t.Run(test.scenario, func(t *testing.T) {
			result, files, err := run(
				context.Background(), test.scenario, "test-"+test.scenario,
				t.TempDir(), "data",
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision != test.decision {
				t.Fatalf("decision = %q, want %q; candidates=%+v", result.Decision, test.decision, result.Candidates)
			}
			expectedRounds := 2
			if test.scenario == "success" {
				expectedRounds = 4
			}
			if len(files) != 2 || len(result.Candidates) != expectedRounds {
				t.Fatalf("missing artifacts or round evidence: files=%d candidates=%d want=%d",
					len(files), len(result.Candidates), expectedRounds)
			}
			if len(result.BaselineTrain.Cases) != 4 || len(result.BaselineValidation.Cases) != 5 {
				t.Fatalf("standard Evaluation assets were not executed: train=%d validation=%d",
					len(result.BaselineTrain.Cases), len(result.BaselineValidation.Cases))
			}
			if attributionCount(result.AttributionCounts) != 4 {
				t.Fatalf("failure attribution statistics are incomplete: %v", result.AttributionCounts)
			}
			if !result.Usage.Complete || result.Usage.Source != "deterministic_example" || result.Usage.Calls == 0 {
				t.Fatalf("full-pipeline usage is incomplete: %+v", result.Usage)
			}
			if result.PromptIter == nil || result.PromptIter.NumRuns != result.Spec.Runtime.NumRuns {
				t.Fatalf("effective PromptIter configuration is missing: %+v", result.PromptIter)
			}
			if result.Spec.Runtime.SeedApplied {
				t.Fatal("deterministic no-randomness example claimed that seed was applied")
			}
			candidate := result.Candidates[0]
			if !candidate.PromptIterAccepted || candidate.Train == nil || candidate.TrainDelta == nil {
				t.Fatalf("PromptIter round or reused candidate-train evidence missing: %+v", candidate)
			}
			assertCompleteSnapshot(t, "baseline train", result.BaselineTrain, result.Spec.Runtime.NumRuns)
			assertCompleteSnapshot(t, "baseline validation", result.BaselineValidation, result.Spec.Runtime.NumRuns)
			assertCompleteSnapshot(t, "candidate train", candidate.Train, result.Spec.Runtime.NumRuns)
			assertCompleteSnapshot(t, "candidate validation", candidate.Validation, result.Spec.Runtime.NumRuns)
			assertCompleteDelta(t, "train", candidate.TrainDelta)
			assertCompleteDelta(t, "validation", candidate.ValidationDelta)
			switch test.scenario {
			case "success":
				assertProgressiveSuccess(t, result)
				selected := selectedCandidate(t, result)
				if selected.Validation.OverallScore != 1 || selected.ValidationDelta.NewPasses != 4 {
					t.Fatalf("success did not reach the target: %+v", selected.ValidationDelta)
				}
			case "no-effect":
				if candidate.ValidationDelta.WeightedScoreDelta != 0 {
					t.Fatalf("no-effect changed validation behavior: %+v", candidate.ValidationDelta)
				}
			case "overfit":
				if candidate.TrainDelta.WeightedScoreDelta <= 0 ||
					candidate.ValidationDelta.WeightedScoreDelta >= 0 ||
					candidate.ValidationDelta.NewHardFailures != 1 {
					t.Fatalf("overfit scenario was not detected: train=%+v validation=%+v",
						candidate.TrainDelta, candidate.ValidationDelta)
				}
			}
		})
	}
}

func TestSampleReportMatchesCurrentSuccessScenario(t *testing.T) {
	generated, _, err := run(context.Background(), "success", "sample-success", t.TempDir(), "data")
	if err != nil {
		t.Fatal(err)
	}
	jsonReport, err := report.JSON(generated)
	if err != nil {
		t.Fatal(err)
	}
	var decoded regression.RunResult
	if err := json.Unmarshal(jsonReport, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != "1" || decoded.Decision != regression.DecisionAccepted ||
		len(decoded.Candidates) != 4 {
		t.Fatalf("generated JSON report is incomplete: %+v", decoded)
	}
	sampleJSON, err := os.ReadFile("sample_output/optimization_report.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(sampleJSON) > 100*1024 {
		t.Fatalf("sample JSON is too large for review: %d bytes", len(sampleJSON))
	}
	if bytes.Count(sampleJSON, []byte{'\n'}) > 1 {
		t.Fatalf("sample JSON should remain compact, got %d lines",
			bytes.Count(sampleJSON, []byte{'\n'})+1)
	}
	var sample regression.RunResult
	if err := json.Unmarshal(sampleJSON, &sample); err != nil {
		t.Fatal(err)
	}
	if sample.SchemaVersion != "1" || sample.Decision != generated.Decision ||
		len(sample.Candidates) != len(generated.Candidates) ||
		sample.SelectedCandidateID != generated.SelectedCandidateID {
		t.Fatalf("checked-in JSON sample is stale: sample=%+v generated=%+v",
			sample, generated)
	}
	if sample.Candidates[3].Train == nil || sample.Candidates[3].TrainDelta == nil {
		t.Fatal("checked-in JSON sample omits final candidate train evidence")
	}
	markdown, err := os.ReadFile("sample_output/optimization_report.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(markdown) > 40*1024 {
		t.Fatalf("sample markdown is too large for review: %d bytes", len(markdown))
	}
	value := string(markdown)
	for _, expected := range []string{
		"## Optimization progress",
		"| 4 | 1 | 0.25 | true | target score reached | accepted |",
		"| train | 0.666667 | 1.000000 | 0.312500 | 4 | 0 |",
	} {
		if !strings.Contains(value, expected) {
			t.Fatalf("sample markdown omitted %q", expected)
		}
	}
	if strings.Contains(value, "Candidate training evidence is unavailable") {
		t.Fatal("final candidate still lacks train evidence")
	}
}

func assertProgressiveSuccess(t *testing.T, result *regression.RunResult) {
	t.Helper()
	if result.SchemaVersion != "1" {
		t.Fatalf("schema version = %q, want 1", result.SchemaVersion)
	}
	if result.PromptIter == nil || result.PromptIter.TargetScore == nil ||
		*result.PromptIter.TargetScore != .95 {
		t.Fatalf("progressive target score is missing: %+v", result.PromptIter)
	}
	if result.Spec.Gate.MaxGeneralizationGap <= 0 {
		t.Fatalf("success scenario disabled the generalization gate: %+v", result.Spec.Gate)
	}
	previousScore := result.BaselineValidation.OverallScore
	for index, candidate := range result.Candidates {
		if !candidate.ProfileChanged {
			t.Fatalf("round %d did not change the effective profile: %+v", index+1, candidate)
		}
		if candidate.Validation == nil || candidate.Validation.OverallScore <= previousScore {
			t.Fatalf("round %d did not improve validation: previous=%f candidate=%+v",
				index+1, previousScore, candidate.Validation)
		}
		previousScore = candidate.Validation.OverallScore
	}
	final := result.Candidates[len(result.Candidates)-1]
	if !final.PromptIterShouldStop || final.PromptIterStopReason != "target score reached" {
		t.Fatalf("final round did not stop on target score: %+v", final)
	}
	if result.SelectedCandidateID != final.Candidate.ID {
		t.Fatalf("selected candidate = %q, want final target candidate %q",
			result.SelectedCandidateID, final.Candidate.ID)
	}
	if final.Train == nil || final.TrainDelta == nil {
		t.Fatalf("final candidate train evidence is missing: %+v", final)
	}
	assertCompleteSnapshot(t, "final candidate train", final.Train, result.Spec.Runtime.NumRuns)
	assertCompleteDelta(t, "final candidate train", final.TrainDelta)
	for _, rule := range []string{"train_delta_available", "generalization_gap"} {
		if !passedGateRule(final.Gate, rule) {
			t.Fatalf("final candidate did not pass %q: %+v", rule, final.Gate)
		}
	}
}

func passedGateRule(decision *regression.GateDecision, name string) bool {
	if decision == nil {
		return false
	}
	for _, rule := range decision.Rules {
		if rule.Rule == name {
			return rule.Passed
		}
	}
	return false
}

func selectedCandidate(t *testing.T, result *regression.RunResult) regression.CandidateResult {
	t.Helper()
	for _, candidate := range result.Candidates {
		if candidate.Candidate.ID == result.SelectedCandidateID {
			return candidate
		}
	}
	t.Fatalf("selected candidate %q was not found", result.SelectedCandidateID)
	return regression.CandidateResult{}
}

func assertCompleteSnapshot(
	t *testing.T,
	name string,
	snapshot *regression.EvaluationSnapshot,
	numRuns int,
) {
	t.Helper()
	if snapshot == nil || len(snapshot.Cases) == 0 {
		t.Fatalf("%s snapshot is missing", name)
	}
	for _, result := range snapshot.Cases {
		if len(result.Metrics) == 0 || len(result.Runs) != numRuns {
			t.Fatalf("%s case %q omitted metrics or run observations: %+v", name, result.CaseID, result)
		}
		for _, run := range result.Runs {
			if len(run.Trace) == 0 {
				t.Fatalf("%s case %q omitted execution trace: %+v", name, result.CaseID, run)
			}
		}
	}
}

func assertCompleteDelta(t *testing.T, name string, delta *regression.DeltaReport) {
	t.Helper()
	if delta == nil || len(delta.Cases) == 0 {
		t.Fatalf("%s delta is missing", name)
	}
	for _, result := range delta.Cases {
		if len(result.Metrics) == 0 {
			t.Fatalf("%s delta case %q omitted metric changes: %+v", name, result.CaseID, result)
		}
	}
}

func attributionCount(counts map[regression.FailureCategory]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}
