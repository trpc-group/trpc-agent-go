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
	"reflect"
	"strings"
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

func TestValidateOptionsRejectsInvalidConfiguration(t *testing.T) {
	valid := Options{
		Config: Config{TrainEvalSetID: "train", ValidationEvalSetID: "validation", MaxRounds: 1, MaxRoundsWithoutRelease: 1, TargetSurfaceIDs: []string{"agent#instruction"}},
		Engine: &pipelineEngine{}, Evaluator: pipelineEvaluator{}, Meter: pipelineMeter{}, InitialProfile: testProfile("initial"),
		Artifacts: &memoryArtifacts{files: map[string][]byte{}},
	}
	tests := []struct {
		name   string
		change func(*Options)
	}{
		{name: "missing dependency", change: func(options *Options) { options.Engine = nil }},
		{name: "missing eval set", change: func(options *Options) { options.Config.TrainEvalSetID = "" }},
		{name: "invalid rounds", change: func(options *Options) { options.Config.MaxRounds = 0 }},
		{name: "invalid release limit", change: func(options *Options) { options.Config.MaxRoundsWithoutRelease = 0 }},
		{name: "missing target", change: func(options *Options) { options.Config.TargetSurfaceIDs = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := valid
			test.change(&options)
			if err := validateOptions(options); err == nil {
				t.Fatal("validateOptions succeeded")
			}
		})
	}
}

func TestEvaluationCompleteRejectsIncompleteResults(t *testing.T) {
	complete := pipelineEvaluation(1, status.EvalStatusPassed)
	if !evaluationComplete(complete, complete) {
		t.Fatal("complete evaluation was rejected")
	}
	if evaluationComplete(nil, complete) {
		t.Fatal("missing expected evaluation was accepted")
	}
	missingCase := &engine.EvaluationResult{EvalSets: []engine.EvalSetResult{{EvalSetID: "set"}}}
	if evaluationComplete(complete, missingCase) {
		t.Fatal("missing case was accepted")
	}
	missingMetrics := pipelineEvaluation(1, status.EvalStatusPassed)
	missingMetrics.EvalSets[0].Cases[0].Metrics = nil
	if evaluationComplete(complete, missingMetrics) {
		t.Fatal("case without metrics was accepted")
	}
	notEvaluated := pipelineEvaluation(0, status.EvalStatusNotEvaluated)
	if evaluationComplete(complete, notEvaluated) {
		t.Fatal("not-evaluated metric was accepted")
	}
	twoMetrics := pipelineEvaluation(1, status.EvalStatusPassed)
	twoMetrics.EvalSets[0].Cases[0].Metrics = append(twoMetrics.EvalSets[0].Cases[0].Metrics,
		engine.MetricResult{MetricName: "safety", Score: 1, Status: status.EvalStatusPassed})
	if evaluationComplete(twoMetrics, complete) {
		t.Fatal("candidate with a missing metric was accepted")
	}
	unknown := pipelineEvaluation(1, status.EvalStatusUnknown)
	if evaluationComplete(complete, unknown) {
		t.Fatal("unknown metric status was accepted")
	}
	emptyStatus := pipelineEvaluation(1, status.EvalStatus(""))
	if evaluationComplete(complete, emptyStatus) {
		t.Fatal("empty metric status was accepted")
	}
	changedName := pipelineEvaluation(1, status.EvalStatusPassed)
	changedName.EvalSets[0].Cases[0].Metrics[0].MetricName = "different"
	if evaluationComplete(complete, changedName) {
		t.Fatal("candidate with a different metric set was accepted")
	}
}

func TestRunWithoutRoundArtifactsPersistsAcceptedProfileReference(t *testing.T) {
	initial := testProfile("initial")
	candidate := testProfile("candidate")
	artifacts := &memoryArtifacts{files: map[string][]byte{}}
	report, err := Run(context.Background(), Options{
		Config: Config{
			TrainEvalSetID: "train", ValidationEvalSetID: "validation", TargetSurfaceIDs: []string{"agent#instruction"},
			MaxRounds: 1, MaxRoundsWithoutRelease: 1, ReleaseGate: GatePolicy{MinValidationScoreGain: 0.1, RequireCompleteEvaluation: true},
		},
		Engine:    &pipelineEngine{result: pipelineRunResult(0.5, 0.5, 0.7, candidate)},
		Evaluator: pipelineEvaluator{result: pipelineEvaluation(0.7, status.EvalStatusPassed)}, Meter: pipelineMeter{},
		InitialProfile: initial, Artifacts: artifacts,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.WriteBack.RecommendedForWriteBack || report.WriteBack.AcceptedProfileRef != "released/candidate_profile.json" {
		t.Fatalf("unexpected write-back decision: %#v", report.WriteBack)
	}
	if !reflect.DeepEqual(report.Baseline.Artifacts, ArtifactReferences{}) || !reflect.DeepEqual(report.Rounds[0].Artifacts, ArtifactReferences{}) {
		t.Fatalf("disabled artifacts produced dangling references: baseline=%#v round=%#v", report.Baseline.Artifacts, report.Rounds[0].Artifacts)
	}
	if _, ok := artifacts.files[report.WriteBack.AcceptedProfileRef]; !ok {
		t.Fatalf("accepted profile reference was not persisted: %#v", artifacts.files)
	}
	for path := range artifacts.files {
		if strings.HasPrefix(path, "baseline/") || strings.HasPrefix(path, "round_1/") {
			t.Fatalf("disabled round artifact was persisted: %s", path)
		}
	}
}

func TestAcceptedUnchangedProfileIsNotRecommendedForWriteBack(t *testing.T) {
	initial := testProfile("same")
	normalizedInitial := &promptiter.Profile{StructureID: "structure"}
	candidate := &promptiter.Profile{StructureID: "structure"}
	runResult := pipelineRunResult(0.5, 0.5, 0.5, candidate)
	runResult.Rounds[0].InputProfile = normalizedInitial
	report, err := Run(context.Background(), Options{
		Config: Config{
			TrainEvalSetID: "train", ValidationEvalSetID: "validation", TargetSurfaceIDs: []string{"agent#instruction"},
			MaxRounds: 1, MaxRoundsWithoutRelease: 1, SaveArtifacts: true, ReleaseGate: GatePolicy{RequireCompleteEvaluation: true},
		},
		Engine:    &pipelineEngine{result: runResult},
		Evaluator: pipelineEvaluator{result: pipelineEvaluation(0.5, status.EvalStatusPassed)}, Meter: pipelineMeter{},
		InitialProfile: initial, Artifacts: &memoryArtifacts{files: map[string][]byte{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Rounds[0].ReleaseGate.Accepted || report.WriteBack.RecommendedForWriteBack {
		t.Fatalf("unchanged accepted profile produced a write-back recommendation: %#v", report.WriteBack)
	}
}

func TestRunRejectsIncompleteCollaboratorResults(t *testing.T) {
	valid := pipelineRunResult(0.5, 0.5, 0.6, testProfile("candidate"))
	tests := []struct {
		name      string
		result    *engine.RunResult
		evaluator ProfileEvaluator
		want      string
	}{
		{name: "nil run result", result: nil, evaluator: pipelineEvaluator{}, want: "nil result"},
		{name: "missing baseline", result: cloneRunResult(valid, func(value *engine.RunResult) { value.BaselineValidation = nil }), evaluator: pipelineEvaluator{}, want: "no baseline validation"},
		{name: "missing train", result: cloneRunResult(valid, func(value *engine.RunResult) { value.Rounds[0].Train = nil }), evaluator: pipelineEvaluator{}, want: "no train evaluation"},
		{name: "missing validation", result: cloneRunResult(valid, func(value *engine.RunResult) { value.Rounds[0].Validation = nil }), evaluator: pipelineEvaluator{}, want: "no validation evaluation"},
		{name: "missing profile", result: cloneRunResult(valid, func(value *engine.RunResult) { value.Rounds[0].OutputProfile = nil }), evaluator: pipelineEvaluator{}, want: "no output profile"},
		{name: "nil candidate train", result: valid, evaluator: pipelineEvaluator{}, want: "returned a nil result"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Run(context.Background(), Options{
				Config: Config{TrainEvalSetID: "train", ValidationEvalSetID: "validation", TargetSurfaceIDs: []string{"agent#instruction"}, MaxRounds: 1, MaxRoundsWithoutRelease: 1},
				Engine: &pipelineEngine{result: test.result}, Evaluator: test.evaluator, Meter: pipelineMeter{},
				InitialProfile: testProfile("initial"), Artifacts: &memoryArtifacts{files: map[string][]byte{}},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestMaxRoundsWithoutReleaseStopsAcceptedSearch(t *testing.T) {
	engineStub := &sequencePipelineEngine{results: []*engine.RunResult{
		pipelineRunResult(0.5, 0.5, 0.4, testProfile("one")),
		pipelineRunResult(0.4, 0.4, 0.3, testProfile("two")),
		pipelineRunResult(0.3, 0.3, 0.2, testProfile("three")),
	}}
	evaluator := profilePipelineEvaluator{results: map[string]*engine.EvaluationResult{
		"one": pipelineEvaluation(0.4, status.EvalStatusPassed), "two": pipelineEvaluation(0.3, status.EvalStatusPassed), "three": pipelineEvaluation(0.2, status.EvalStatusPassed),
	}}
	report, err := Run(context.Background(), Options{
		Config: Config{
			TrainEvalSetID: "train", ValidationEvalSetID: "validation", TargetSurfaceIDs: []string{"agent#instruction"},
			MaxRounds: 3, MaxRoundsWithoutRelease: 2, PromptIterMinScoreGain: CandidatePassThroughGain,
			ReleaseGate: GatePolicy{MinValidationScoreGain: 0.1, RequireCompleteEvaluation: true},
		},
		Engine: engineStub, Evaluator: evaluator, Meter: pipelineMeter{}, InitialProfile: testProfile("initial"), Artifacts: &memoryArtifacts{files: map[string][]byte{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rounds) != 2 || len(engineStub.requests) != 2 || !report.Rounds[0].PromptIterAccepted || !report.Rounds[1].PromptIterAccepted {
		t.Fatalf("release stop condition did not stop after two rejected releases: rounds=%d requests=%d", len(report.Rounds), len(engineStub.requests))
	}
}

func cloneRunResult(value *engine.RunResult, mutate func(*engine.RunResult)) *engine.RunResult {
	clone := *value
	clone.Rounds = append([]engine.RoundResult(nil), value.Rounds...)
	mutate(&clone)
	return &clone
}

func TestMeasurementDeltaAndDisabledArtifactPersistence(t *testing.T) {
	before := ResourceMeasurement{Usage: Usage{EvaluationCaseRuns: 1, ModelCalls: 2, ToolCalls: 3}, LatencySeconds: 4, Cost: 5}
	after := ResourceMeasurement{Usage: Usage{EvaluationCaseRuns: 3, ModelCalls: 5, ToolCalls: 7}, LatencySeconds: 10, Cost: 13}
	delta := measurementDelta(before, after)
	if delta.Usage.EvaluationCaseRuns != 2 || delta.Usage.ModelCalls != 3 || delta.Usage.ToolCalls != 4 || delta.LatencySeconds != 6 || delta.Cost != 8 {
		t.Fatalf("unexpected measurement delta: %#v", delta)
	}
	artifacts := &memoryArtifacts{files: map[string][]byte{}}
	options := Options{Config: Config{SaveArtifacts: false}, Artifacts: artifacts}
	if err := persistBaseline(options, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := persistRound(options, 1, nil, nil, nil, nil, DeltaBundle{}, GateDecision{}); err != nil {
		t.Fatal(err)
	}
	if len(artifacts.files) != 0 {
		t.Fatalf("disabled persistence wrote %#v", artifacts.files)
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
			BaselineProfileRef: "baseline/input_profile.json", SaveArtifacts: true,
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
func (pipelineMeter) Total() ResourceMeasurement {
	return ResourceMeasurement{Usage: Usage{ModelCalls: 4}}
}

type profilePipelineMeter struct {
	measurements map[string]ResourceMeasurement
}

func (m profilePipelineMeter) Measure(_ string, profile *promptiter.Profile) ResourceMeasurement {
	return m.measurements[profileTextForTest(profile)]
}
func (profilePipelineMeter) Total() ResourceMeasurement {
	return ResourceMeasurement{Usage: Usage{ModelCalls: 37}}
}

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
