//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regression

import (
	"context"
	"testing"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestRunSeparatesPromptIterAcceptanceFromReleaseGate(t *testing.T) {
	baseline := pipelineEvaluation(0.8, status.EvalStatusPassed)
	candidate := pipelineEvaluation(0.7, status.EvalStatusPassed)
	text := "candidate"
	profile := &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{SurfaceID: "agent#instruction", Value: astructure.SurfaceValue{Text: &text}}}}
	engineStub := &pipelineEngine{result: &engine.RunResult{
		BaselineValidation: baseline,
		Rounds: []engine.RoundResult{{
			Train: pipelineEvaluation(0.6, status.EvalStatusPassed), OutputProfile: profile, Validation: candidate,
			Acceptance: &engine.AcceptanceDecision{Accepted: true, ScoreDelta: -0.1, Reason: "search accepted"},
		}},
	}}
	artifacts := &memoryArtifacts{files: map[string][]byte{}}
	start := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	times := []time.Time{start, start.Add(time.Second)}
	report, err := Run(context.Background(), Options{
		Config: Config{
			TrainEvalSetID: "train", ValidationEvalSetID: "validation", TargetSurfaceIDs: []string{"agent#instruction"},
			MaxRounds: 1, MaxRoundsWithoutRelease: 1, PromptIterMinScoreGain: CandidatePassThroughGain,
			ReleaseGate:        GatePolicy{MinValidationScoreGain: 0.01, RejectValidationRegression: true, RequireCompleteEvaluation: true},
			BaselineProfileRef: "baseline/input_profile.json", SaveArtifacts: true,
		},
		Engine: engineStub, Evaluator: pipelineEvaluator{result: pipelineEvaluation(0.7, status.EvalStatusPassed)},
		Meter: pipelineMeter{}, InitialProfile: profile, Artifacts: artifacts,
		Now: func() time.Time { value := times[0]; times = times[1:]; return value },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Rounds[0].PromptIterAccepted || report.Rounds[0].ReleaseGate.Accepted {
		t.Fatalf("acceptance separation failed: %#v", report.Rounds[0])
	}
	if report.WriteBack.RecommendedForWriteBack || report.WriteBack.AcceptedProfileRef != "baseline/input_profile.json" {
		t.Fatalf("release profile changed after rejected candidate: %#v", report.WriteBack)
	}
	for _, path := range []string{"baseline/train_evaluation.json", "round_1/delta.json", "round_1/gate.json", "optimization_report.json", "optimization_report.md"} {
		if _, ok := artifacts.files[path]; !ok {
			t.Errorf("missing artifact %s", path)
		}
	}
}

func TestReleaseGateUsesLastReleasedBaseline(t *testing.T) {
	report, requests := runLastReleasedBaselineScenario(t)
	if len(report.Rounds) != 3 {
		t.Fatalf("round count = %d, want 3", len(report.Rounds))
	}
	if !report.Rounds[0].ReleaseGate.Accepted || report.Rounds[1].ReleaseGate.Accepted || report.Rounds[2].ReleaseGate.Accepted {
		t.Fatalf("unexpected release decisions: %#v", report.Rounds)
	}
	roundThree := report.Rounds[2]
	if got := roundThree.Delta.AgainstRoundInput.ScoreDelta; got < 0.099 || got > 0.101 {
		t.Fatalf("round 3 search delta = %v, want 0.1", got)
	}
	if got := roundThree.Delta.AgainstLastReleased.ScoreDelta; got < -0.301 || got > -0.299 {
		t.Fatalf("round 3 release delta = %v, want -0.3", got)
	}
	if check := roundThree.ReleaseGate.Checks["minValidationGain"]; check.Passed || check.Observed != "validation delta -0.3000, required 0.0500" {
		t.Fatalf("round 3 release Gate used wrong baseline: %#v", check)
	}
	if len(requests) != 3 || profileTextForTest(requests[2].InitialProfile) != "search-0.6" {
		t.Fatalf("PromptIter search did not advance through rejected profile: %#v", requests)
	}
}

func TestRejectedSearchProfileDoesNotBecomeReleaseBaseline(t *testing.T) {
	report, _ := runLastReleasedBaselineScenario(t)
	if report.WriteBack.AcceptedProfileRef != "round_1/candidate_profile.json" {
		t.Fatalf("released profile = %q, want round 1", report.WriteBack.AcceptedProfileRef)
	}
	if got := report.Rounds[2].Resources.Validation.LastReleased.Usage.ModelCalls; got != 10 {
		t.Fatalf("round 3 released resource baseline model calls = %d, want 10", got)
	}
}

func runLastReleasedBaselineScenario(t *testing.T) (*Report, []*engine.RunRequest) {
	t.Helper()
	initial := testProfile("initial")
	roundOne := testProfile("released-1.0")
	roundTwo := testProfile("search-0.6")
	roundThree := testProfile("candidate-0.7")
	engineStub := &sequencePipelineEngine{results: []*engine.RunResult{
		pipelineRunResult(0.8, 0.8, 1.0, roundOne),
		pipelineRunResult(1.0, 1.0, 0.6, roundTwo),
		pipelineRunResult(0.6, 0.6, 0.7, roundThree),
	}}
	evaluator := profilePipelineEvaluator{results: map[string]*engine.EvaluationResult{
		"released-1.0":  pipelineEvaluation(1.0, status.EvalStatusPassed),
		"search-0.6":    pipelineEvaluation(0.6, status.EvalStatusPassed),
		"candidate-0.7": pipelineEvaluation(0.7, status.EvalStatusPassed),
	}}
	meter := profilePipelineMeter{measurements: map[string]ResourceMeasurement{
		"initial": {Usage: Usage{ModelCalls: 8}}, "released-1.0": {Usage: Usage{ModelCalls: 10}},
		"search-0.6": {Usage: Usage{ModelCalls: 99}}, "candidate-0.7": {Usage: Usage{ModelCalls: 10}},
	}}
	report, err := Run(context.Background(), Options{
		Config: Config{
			TrainEvalSetID: "train", ValidationEvalSetID: "validation", TargetSurfaceIDs: []string{"agent#instruction"},
			MaxRounds: 3, MaxRoundsWithoutRelease: 3, PromptIterMinScoreGain: CandidatePassThroughGain,
			ReleaseGate:        GatePolicy{MinValidationScoreGain: 0.05, MaxModelCallIncrease: 100, MaxToolCallIncrease: 100, MaxCostIncrease: 100, MaxLatencyIncrease: 100, RejectValidationRegression: true, RequireCompleteEvaluation: true},
			BaselineProfileRef: "baseline/input_profile.json",
		},
		Engine: engineStub, Evaluator: evaluator, Meter: meter, InitialProfile: initial,
		Artifacts: &memoryArtifacts{files: map[string][]byte{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return report, engineStub.requests
}

func pipelineRunResult(inputTrain, inputValidation, candidateValidation float64, output *promptiter.Profile) *engine.RunResult {
	return &engine.RunResult{BaselineValidation: pipelineEvaluation(inputValidation, status.EvalStatusPassed), Rounds: []engine.RoundResult{{
		Train: pipelineEvaluation(inputTrain, status.EvalStatusPassed), OutputProfile: output,
		Validation: pipelineEvaluation(candidateValidation, status.EvalStatusPassed),
		Acceptance: &engine.AcceptanceDecision{Accepted: true, Reason: "search accepted"},
	}}}
}

func testProfile(text string) *promptiter.Profile {
	return &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{SurfaceID: "agent#instruction", Value: astructure.SurfaceValue{Text: &text}}}}
}

func profileTextForTest(profile *promptiter.Profile) string {
	if profile == nil || len(profile.Overrides) == 0 || profile.Overrides[0].Value.Text == nil {
		return ""
	}
	return *profile.Overrides[0].Value.Text
}

type pipelineEngine struct{ result *engine.RunResult }

func (e *pipelineEngine) Run(context.Context, *engine.RunRequest, ...engine.Option) (*engine.RunResult, error) {
	return e.result, nil
}

type sequencePipelineEngine struct {
	results  []*engine.RunResult
	requests []*engine.RunRequest
}

func (e *sequencePipelineEngine) Run(_ context.Context, request *engine.RunRequest, _ ...engine.Option) (*engine.RunResult, error) {
	e.requests = append(e.requests, request)
	result := e.results[0]
	e.results = e.results[1:]
	return result, nil
}

type pipelineEvaluator struct{ result *engine.EvaluationResult }

func (e pipelineEvaluator) EvaluateProfile(context.Context, string, *promptiter.Profile) (*engine.EvaluationResult, error) {
	return e.result, nil
}

type profilePipelineEvaluator struct {
	results map[string]*engine.EvaluationResult
}

func (e profilePipelineEvaluator) EvaluateProfile(_ context.Context, _ string, profile *promptiter.Profile) (*engine.EvaluationResult, error) {
	return e.results[profileTextForTest(profile)], nil
}

type pipelineMeter struct{}

func (pipelineMeter) Measure(string, *promptiter.Profile) ResourceMeasurement {
	return ResourceMeasurement{}
}
func (pipelineMeter) TotalModelCalls() int { return 4 }

type profilePipelineMeter struct {
	measurements map[string]ResourceMeasurement
}

func (m profilePipelineMeter) Measure(_ string, profile *promptiter.Profile) ResourceMeasurement {
	return m.measurements[profileTextForTest(profile)]
}
func (profilePipelineMeter) TotalModelCalls() int { return 37 }

type memoryArtifacts struct{ files map[string][]byte }

func (m *memoryArtifacts) Write(path string, payload []byte) error {
	m.files[path] = append([]byte(nil), payload...)
	return nil
}

func pipelineEvaluation(score float64, metricStatus status.EvalStatus) *engine.EvaluationResult {
	reason := ""
	if metricStatus == status.EvalStatusFailed {
		reason = "failed"
	}
	return &engine.EvaluationResult{OverallScore: score, EvalSets: []engine.EvalSetResult{{EvalSetID: "set", OverallScore: score, Cases: []engine.CaseResult{{EvalCaseID: "case", Metrics: []engine.MetricResult{{MetricName: "quality", Score: score, Status: metricStatus, Reason: reason}}}}}}}
}
