//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression/attribution"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression/delta"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression/gate"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression/report"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type attributorFunc func(context.Context, *regression.CaseResult) (*regression.AttributionResult, error)

func (f attributorFunc) Attribute(
	ctx context.Context,
	result *regression.CaseResult,
) (*regression.AttributionResult, error) {
	return f(ctx, result)
}

type deltaFunc func(
	*regression.EvaluationSnapshot,
	*regression.EvaluationSnapshot,
	map[string]regression.MetricPolicy,
) (*regression.DeltaReport, error)

func (f deltaFunc) Compare(
	baseline *regression.EvaluationSnapshot,
	candidate *regression.EvaluationSnapshot,
	policies map[string]regression.MetricPolicy,
) (*regression.DeltaReport, error) {
	return f(baseline, candidate, policies)
}

type gateFunc func(*regression.GateInput) (*regression.GateDecision, error)

func (f gateFunc) Decide(input *regression.GateInput) (*regression.GateDecision, error) {
	return f(input)
}

type failingAuditPayload struct {
	secret string
}

func (failingAuditPayload) MarshalJSON() ([]byte, error) {
	return nil, errors.New("payload is not JSON serializable")
}

func (p failingAuditPayload) String() string {
	return p.secret
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	validAttributor := attribution.NewRules()
	validDelta := delta.New(0)
	validGate := gate.NewPolicy()
	tests := map[string]regression.Dependencies{
		"attributor":   {DeltaEngine: validDelta, Gate: validGate},
		"delta engine": {Attributor: validAttributor, Gate: validGate},
		"gate":         {Attributor: validAttributor, DeltaEngine: validDelta},
	}
	for name, deps := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := regression.New(deps); err == nil {
				t.Fatal("missing dependency was accepted")
			}
		})
	}
}

func TestAnalyzerReturnsFailedResultForInvalidSpec(t *testing.T) {
	analyzer, err := regression.New(regression.Dependencies{
		Attributor: attribution.NewRules(), DeltaEngine: delta.New(0), Gate: gate.NewPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := analyzer.Analyze(
		context.Background(), nil,
		promptIterResult(profile("baseline"), profile("candidate"), true),
		regression.UsageSupplement{},
	)
	if err == nil || result.Status != regression.RunStatusFailed || result.ErrorMessage == "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestAnalyzerReturnsCanceledResultWhenAttributionIsCanceled(t *testing.T) {
	analyzer, err := regression.New(regression.Dependencies{
		Attributor: attributorFunc(func(context.Context, *regression.CaseResult) (*regression.AttributionResult, error) {
			return nil, context.Canceled
		}),
		DeltaEngine: delta.New(0),
		Gate:        gate.NewPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := analyzer.Analyze(
		context.Background(), auditSpec(),
		promptIterResult(profile("baseline"), profile("candidate"), true),
		regression.UsageSupplement{},
	)
	if !errors.Is(err, context.Canceled) || result.Status != regression.RunStatusCanceled {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestAnalyzerRejectsInconsistentUsageEvidence(t *testing.T) {
	analyzer, err := regression.New(regression.Dependencies{
		Attributor: attribution.NewRules(), DeltaEngine: delta.New(0), Gate: gate.NewPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		engineUsage promptiter.Usage
		supplement  regression.UsageSupplement
	}{
		{name: "negative model calls", engineUsage: promptiter.Usage{Calls: -1}},
		{name: "negative latency", supplement: regression.UsageSupplement{PromptIterLatency: -time.Second}},
		{name: "non-finite cost", supplement: regression.UsageSupplement{CostBreakdown: regression.CostBreakdown{CostEstimate: regression.CostEstimate{CostKnown: true, EstimatedCost: math.NaN(), PricingSource: "test"}}}},
		{name: "unknown cost carries a value", supplement: regression.UsageSupplement{CostBreakdown: regression.CostBreakdown{CostEstimate: regression.CostEstimate{EstimatedCost: .5}}}},
		{name: "unknown cost carries a pricing source", supplement: regression.UsageSupplement{CostBreakdown: regression.CostBreakdown{CostEstimate: regression.CostEstimate{PricingSource: "test"}}}},
		{name: "known cost has no pricing source", supplement: regression.UsageSupplement{CostBreakdown: regression.CostBreakdown{CostEstimate: regression.CostEstimate{CostKnown: true}}}},
		{name: "token total contradicts components", engineUsage: promptiter.Usage{
			PromptTokens: 8, CompletionTokens: 5, TotalTokens: 10,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := promptIterResult(profile("baseline"), profile("candidate"), true)
			source.Usage = test.engineUsage
			result, err := analyzer.Analyze(
				context.Background(), auditSpec(),
				source,
				test.supplement,
			)
			if err == nil || result.Status != regression.RunStatusFailed {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestScenarioUsageEvidenceDerivesMissingTotalFromObservedTokens(t *testing.T) {
	source := promptIterResult(profile("baseline"), profile("candidate"), true)
	source.Usage = promptiter.Usage{PromptTokens: 8, CompletionTokens: 5, Complete: true}
	result := analyzeWith(
		t, auditSpec(), source, regression.UsageSupplement{},
	)
	if result.Usage.TotalTokens != 13 {
		t.Fatalf("total tokens = %d, want 13", result.Usage.TotalTokens)
	}
}

func TestAnalyzerRejectsMalformedPromptIterResults(t *testing.T) {
	analyzer, err := regression.New(regression.Dependencies{
		Attributor: attribution.NewRules(), DeltaEngine: delta.New(0), Gate: gate.NewPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		build func() *engine.RunResult
	}{
		{"nil result", func() *engine.RunResult { return nil }},
		{"unfinished run", func() *engine.RunResult {
			value := promptIterResult(profile("baseline"), profile("candidate"), true)
			value.Status = engine.RunStatusRunning
			return value
		}},
		{"succeeded run carries an error", func() *engine.RunResult {
			value := promptIterResult(profile("baseline"), profile("candidate"), true)
			value.ErrorMessage = "optimizer failed"
			return value
		}},
		{"missing effective initial profile", func() *engine.RunResult {
			value := promptIterResult(profile("baseline"), profile("candidate"), true)
			value.InitialProfile = nil
			return value
		}},
		{"missing baseline validation", func() *engine.RunResult {
			value := promptIterResult(profile("baseline"), profile("candidate"), true)
			value.BaselineValidation = nil
			return value
		}},
		{"missing accepted profile", func() *engine.RunResult {
			value := promptIterResult(profile("baseline"), profile("candidate"), true)
			value.AcceptedProfile = nil
			return value
		}},
		{"missing rounds", func() *engine.RunResult {
			value := promptIterResult(profile("baseline"), profile("candidate"), true)
			value.Rounds = nil
			return value
		}},
		{"round has no train result", func() *engine.RunResult {
			value := promptIterResult(profile("baseline"), profile("candidate"), true)
			value.Rounds[0].Train = nil
			return value
		}},
		{"round has no candidate profile", func() *engine.RunResult {
			value := promptIterResult(profile("baseline"), profile("candidate"), true)
			value.Rounds[0].OutputProfile = nil
			return value
		}},
		{"round has no validation result", func() *engine.RunResult {
			value := promptIterResult(profile("baseline"), profile("candidate"), true)
			value.Rounds[0].Validation = nil
			return value
		}},
		{"round has no acceptance result", func() *engine.RunResult {
			value := promptIterResult(profile("baseline"), profile("candidate"), true)
			value.Rounds[0].Acceptance = nil
			return value
		}},
		{"round sequence is discontinuous", func() *engine.RunResult {
			value := promptIterResult(profile("baseline"), profile("candidate"), true)
			value.Rounds[0].Round = 2
			return value
		}},
		{"round input does not follow accepted state", func() *engine.RunResult {
			value := promptIterResult(profile("baseline"), profile("candidate"), true)
			appendFollowUpRound(
				value, profile("follow-up"),
				evaluationResult("train", "train-case", 1, status.EvalStatusPassed, ""),
				evaluationResult("validation", "validation-case", 0, status.EvalStatusFailed, "no gain"),
				false,
			)
			value.Rounds[1].InputProfile = profile("wrong-state")
			return value
		}},
		{"final accepted profile contradicts history", func() *engine.RunResult {
			value := promptIterResult(profile("baseline"), profile("candidate"), true)
			value.AcceptedProfile = profile("different")
			return value
		}},
		{"current round contradicts history", func() *engine.RunResult {
			value := promptIterResult(profile("baseline"), profile("candidate"), true)
			value.CurrentRound = 0
			return value
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := analyzer.Analyze(
				context.Background(), auditSpec(), test.build(), regression.UsageSupplement{},
			)
			if err == nil || result.Status != regression.RunStatusFailed || result.ErrorMessage == "" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestAnalyzerPropagatesAuditDependencyFailures(t *testing.T) {
	dependencyError := errors.New("dependency failed")
	tests := []struct {
		name string
		deps regression.Dependencies
	}{
		{"nil attribution", regression.Dependencies{
			Attributor: attributorFunc(func(context.Context, *regression.CaseResult) (*regression.AttributionResult, error) {
				return nil, nil
			}), DeltaEngine: delta.New(0), Gate: gate.NewPolicy(),
		}},
		{"attribution error", regression.Dependencies{
			Attributor: attributorFunc(func(context.Context, *regression.CaseResult) (*regression.AttributionResult, error) {
				return nil, dependencyError
			}), DeltaEngine: delta.New(0), Gate: gate.NewPolicy(),
		}},
		{"delta error", regression.Dependencies{
			Attributor: attribution.NewRules(),
			DeltaEngine: deltaFunc(func(*regression.EvaluationSnapshot, *regression.EvaluationSnapshot, map[string]regression.MetricPolicy) (*regression.DeltaReport, error) {
				return nil, dependencyError
			}),
			Gate: gate.NewPolicy(),
		}},
		{"gate error", regression.Dependencies{
			Attributor: attribution.NewRules(), DeltaEngine: delta.New(0),
			Gate: gateFunc(func(*regression.GateInput) (*regression.GateDecision, error) {
				return nil, dependencyError
			}),
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			analyzer, err := regression.New(test.deps)
			if err != nil {
				t.Fatal(err)
			}
			result, err := analyzer.Analyze(
				context.Background(), auditSpec(),
				promptIterResult(profile("baseline"), profile("candidate"), true),
				regression.UsageSupplement{CostBreakdown: regression.CostBreakdown{CostEstimate: regression.CostEstimate{CostKnown: true, PricingSource: "test"}}},
			)
			if err == nil || result.Status != regression.RunStatusFailed {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestAnalyzerAuditsPromptIterResultWithoutReevaluation(t *testing.T) {
	baseline := profile("baseline")
	candidate := profile("candidate")
	source := promptIterResult(baseline, candidate, true)
	appendFollowUpRound(
		source, profile("follow-up"),
		evaluationResult("train", "train-case", 1, status.EvalStatusPassed, ""),
		evaluationResult("validation", "validation-case", 0, status.EvalStatusFailed, "no gain"),
		false,
	)
	analyzer, err := regression.New(regression.Dependencies{
		Attributor:  attribution.NewRules(),
		DeltaEngine: delta.New(1e-9),
		Gate:        gate.NewPolicy(),
		Now:         func() time.Time { return time.Unix(1, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	source.Usage = promptiter.Usage{Calls: 4, TotalTokens: 20, Complete: true}
	result, err := analyzer.Analyze(context.Background(), auditSpec(), source, regression.UsageSupplement{
		PromptIterLatency: time.Second,
		CostBreakdown: regression.CostBreakdown{
			CostEstimate:        regression.CostEstimate{CostKnown: true, PricingSource: "test"},
			RoundEstimatedCosts: map[int]float64{1: 0, 2: 0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != regression.DecisionAccepted || len(result.Candidates) != 2 {
		t.Fatalf("unexpected audit result: decision=%s candidates=%d", result.Decision, len(result.Candidates))
	}
	candidateResult := result.Candidates[0]
	if candidateResult.Train == nil || candidateResult.TrainDelta == nil {
		t.Fatalf("candidate train evidence was not reused from the next PromptIter round: %+v", candidateResult)
	}
	if candidateResult.ValidationDelta.WeightedScoreDelta != 1 || result.SelectedCandidateID == "" {
		t.Fatalf("validation delta or selection missing: %+v", candidateResult.ValidationDelta)
	}
	baselineAttribution := attributionFor(result, regression.AttributionBaselineTrain, "train-case")
	if baselineAttribution == nil || baselineAttribution.Reason == "" {
		t.Fatalf("baseline failure attribution missing: %+v", result.Attributions)
	}
	if got := len(result.BaselineValidation.Cases[0].Runs[0].Trace); got != 1 {
		t.Fatalf("execution trace was not preserved: %d", got)
	}
}

func TestAnalyzerPreservesRepeatedProfileRounds(t *testing.T) {
	candidate := profile("candidate")
	source := promptIterResult(profile("baseline"), candidate, true)
	appendFollowUpRound(
		source, candidate,
		evaluationResult("train", "train-case", 1, status.EvalStatusPassed, ""),
		evaluationResult("validation", "validation-case", 1, status.EvalStatusPassed, ""),
		true,
	)
	source.Rounds[1].Acceptance.Reason = "same profile"
	result := analyze(t, source)
	if len(result.Candidates) != 2 {
		t.Fatalf("candidate rounds = %d, want 2", len(result.Candidates))
	}
	if result.Candidates[0].Candidate.ProfileHash != result.Candidates[1].Candidate.ProfileHash {
		t.Fatalf("test setup did not produce repeated profile: %+v", result.Candidates)
	}
	if result.Candidates[0].Candidate.Round == result.Candidates[1].Candidate.Round {
		t.Fatalf("round identity was lost: %+v", result.Candidates)
	}
	if !result.Candidates[0].ProfileChanged || result.Candidates[1].ProfileChanged {
		t.Fatalf("effective profile change evidence is wrong: %+v", result.Candidates)
	}
}

func TestRegressionSelectionOverridesPromptIterRejectionForWriteBack(t *testing.T) {
	baseline := profile("baseline")
	candidate := profile("candidate")
	source := promptIterResult(baseline, candidate, false)
	result := analyze(t, source)
	if result.Decision != regression.DecisionAccepted {
		t.Fatalf("decision = %q, want accepted", result.Decision)
	}
	selected, err := result.SelectedProfile()
	if err != nil {
		t.Fatal(err)
	}
	if *selected.Overrides[0].Value.Text != "candidate" ||
		*source.AcceptedProfile.Overrides[0].Value.Text != "baseline" {
		t.Fatalf("selected=%+v PromptIter accepted=%+v", selected, source.AcceptedProfile)
	}
}

func TestScenarioCriticalCaseIDMustResolveToOneValidationCase(t *testing.T) {
	source := promptIterResult(profile("baseline"), profile("candidate"), true)
	duplicate := evaluationResult(
		"validation-secondary", "validation-case", 0,
		status.EvalStatusFailed, "secondary validation failure",
	)
	source.BaselineValidation.EvalSets = append(
		source.BaselineValidation.EvalSets, duplicate.EvalSets[0],
	)
	spec := auditSpec()
	spec.CriticalCaseIDs = []string{"validation-case"}
	analyzer, err := regression.New(regression.Dependencies{
		Attributor: attribution.NewRules(), DeltaEngine: delta.New(1e-9), Gate: gate.NewPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := analyzer.Analyze(
		context.Background(), spec, source, regression.UsageSupplement{},
	)
	if err == nil || result.Status != regression.RunStatusFailed {
		t.Fatalf("ambiguous critical case was accepted: result=%+v err=%v", result, err)
	}
}

func TestScenarioEqualGainSelectsTheEarlierOptimizationRound(t *testing.T) {
	source := promptIterResult(profile("baseline"), profile("candidate-one"), true)
	appendFollowUpRound(
		source, profile("candidate-two"),
		evaluationResult("train", "train-case", 1, status.EvalStatusPassed, ""),
		evaluationResult("validation", "validation-case", 1, status.EvalStatusPassed, ""),
		true,
	)
	result := analyze(t, source)
	if len(result.Candidates) != 2 || result.Candidates[0].Candidate.Round != 1 {
		t.Fatalf("unexpected candidate history: %+v", result.Candidates)
	}
	if result.SelectedCandidateID != result.Candidates[0].Candidate.ID {
		t.Fatalf(
			"selected candidate = %q, want earlier equal-gain round %q",
			result.SelectedCandidateID, result.Candidates[0].Candidate.ID,
		)
	}
}

func TestAnalyzerDerivesValidationScoreStdDevFromPerRunMetrics(t *testing.T) {
	source := promptIterResult(profile("baseline"), profile("candidate"), true)
	validationCase := &source.Rounds[0].Validation.EvalSets[0].Cases[0]
	secondRunDetail := *validationCase.RunDetails[0]
	secondRunDetail.RunID = 2
	validationCase.RunDetails = append(validationCase.RunDetails, &secondRunDetail)
	validationCase.RunResults = []*evalresult.EvalCaseResult{
		{RunID: 1, OverallEvalMetricResults: []*evalresult.EvalMetricResult{{
			MetricName: "quality", Score: .5, Threshold: 1, EvalStatus: status.EvalStatusFailed,
		}}},
		{RunID: 2, OverallEvalMetricResults: []*evalresult.EvalMetricResult{{
			MetricName: "quality", Score: 1, Threshold: 1, EvalStatus: status.EvalStatusPassed,
		}}},
	}
	result := analyze(t, source)
	got := result.Candidates[0].Validation.ScoreStdDev
	want := math.Sqrt(.125)
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("score stddev = %v, want %v", got, want)
	}
}

func TestScenarioToolBackendFailureUsesReachableEvaluationEvidence(t *testing.T) {
	source := promptIterResult(profile("baseline"), profile("candidate"), true)
	trainCase := &source.Rounds[0].Train.EvalSets[0].Cases[0]
	trainCase.Metrics[0].Score = 1
	trainCase.Metrics[0].Status = status.EvalStatusPassed
	trainCase.Metrics[0].Reason = ""
	trainCase.RunResults[0].OverallEvalMetricResults[0].Score = 1
	trainCase.RunResults[0].OverallEvalMetricResults[0].EvalStatus = status.EvalStatusPassed
	inference := trainCase.RunDetails[0].Inference
	inference.Status = status.EvalStatusFailed
	inference.ErrorMessage = "tool backend unavailable"
	inference.Inferences[0].Tools = []*evalset.Tool{{
		Name: "get_order", Arguments: map[string]any{"order_id": "A123"},
	}}

	result := analyze(t, source)
	if result.BaselineTrain.Cases[0].Passed {
		t.Fatal("failed tool execution was reported as a passing case")
	}
	baselineAttribution := attributionFor(result, regression.AttributionBaselineTrain, "train-case")
	if baselineAttribution == nil ||
		baselineAttribution.Category != regression.FailureToolResultHandling {
		t.Fatalf("unexpected attribution: %+v", result.Attributions)
	}
}

func TestScenarioTraceFailureOverridesPassingAggregateMetric(t *testing.T) {
	source := promptIterResult(profile("baseline"), profile("candidate"), true)
	trainCase := &source.Rounds[0].Train.EvalSets[0].Cases[0]
	trainCase.Metrics[0].Score = 1
	trainCase.Metrics[0].Status = status.EvalStatusPassed
	trainCase.Metrics[0].Reason = ""
	trainCase.RunResults[0].OverallEvalMetricResults[0].Score = 1
	trainCase.RunResults[0].OverallEvalMetricResults[0].EvalStatus = status.EvalStatusPassed
	trainCase.RunDetails[0].Inference.ExecutionTraces[0].Steps[0].Error = "agent node failed"

	result := analyze(t, source)
	if result.BaselineTrain.Cases[0].Passed {
		t.Fatal("trace failure was hidden by the aggregate metric")
	}
	baselineAttribution := attributionFor(result, regression.AttributionBaselineTrain, "train-case")
	if baselineAttribution == nil ||
		baselineAttribution.Category != regression.FailureInferenceError {
		t.Fatalf("unexpected attribution: %+v", result.Attributions)
	}
}

func TestScenarioRepeatedProfileUsesNearestLaterTrainingEvidence(t *testing.T) {
	baseline := profile("baseline")
	profileX := profile("candidate-x")
	profileY := profile("candidate-y")
	source := &engine.RunResult{
		Status: engine.RunStatusSucceeded,
		Configuration: engine.RunConfiguration{
			EvaluationOptions: engine.EvaluationOptions{NumRuns: 1},
			AcceptancePolicy:  engine.AcceptancePolicy{MinScoreGain: 0},
			MaxRounds:         4,
			TargetSurfaceIDs:  []string{"agent#instruction"},
		},
		InitialProfile:     baseline,
		CurrentRound:       4,
		BaselineValidation: evaluationResult("validation", "validation-case", 0, status.EvalStatusFailed, "baseline failed"),
		AcceptedProfile:    profileX,
		Rounds: []engine.RoundResult{
			{
				Round: 1, InputProfile: baseline,
				Train:         evaluationResult("train", "train-case", 0, status.EvalStatusFailed, "baseline failed"),
				OutputProfile: profileX,
				Validation:    evaluationResult("validation", "validation-case", .4, status.EvalStatusPassed, ""),
				Acceptance:    &engine.AcceptanceDecision{Accepted: true, ScoreDelta: .4},
				Stop:          &engine.StopDecision{},
			},
			{
				Round: 2, InputProfile: profileX,
				Train:         evaluationResult("train", "train-case", .2, status.EvalStatusPassed, ""),
				OutputProfile: profileY,
				Validation:    evaluationResult("validation", "validation-case", .5, status.EvalStatusPassed, ""),
				Acceptance:    &engine.AcceptanceDecision{Accepted: true, ScoreDelta: .1},
				Stop:          &engine.StopDecision{},
			},
			{
				Round: 3, InputProfile: profileY,
				Train:         evaluationResult("train", "train-case", .3, status.EvalStatusPassed, ""),
				OutputProfile: profileX,
				Validation:    evaluationResult("validation", "validation-case", .6, status.EvalStatusPassed, ""),
				Acceptance:    &engine.AcceptanceDecision{Accepted: true, ScoreDelta: .1},
				Stop:          &engine.StopDecision{},
			},
			{
				Round: 4, InputProfile: profileX,
				Train:         evaluationResult("train", "train-case", .9, status.EvalStatusPassed, ""),
				OutputProfile: profile("candidate-z"),
				Validation:    evaluationResult("validation", "validation-case", 0, status.EvalStatusFailed, "no gain"),
				Acceptance:    &engine.AcceptanceDecision{Accepted: false, ScoreDelta: -.6},
				Stop:          &engine.StopDecision{ShouldStop: true, Reason: "maximum rounds reached"},
			},
		},
	}

	result := analyze(t, source)
	trainScoreByRound := make(map[int]float64)
	for _, candidate := range result.Candidates {
		if candidate.Train != nil {
			trainScoreByRound[candidate.Candidate.Round] = candidate.Train.OverallScore
		}
	}
	if trainScoreByRound[1] != .2 || trainScoreByRound[3] != .9 {
		t.Fatalf("candidate train evidence = %v, want round 1=.2 and round 3=.9", trainScoreByRound)
	}
}

func TestScenarioAuditRedactsSecretsAndOwnsCandidateSnapshot(t *testing.T) {
	baseline := profile("Follow policy. api_key=prompt-secret")
	candidate := profile("Follow policy. access_token=candidate-secret")
	modelOverride := promptiter.SurfaceOverride{
		SurfaceID: "agent#model",
		Value: astructure.SurfaceValue{Model: &astructure.ModelRef{
			APIKey:  "model-secret",
			BaseURL: "https://url-user:url-password@example.test/v1?api_key=url-secret&region=test",
			Headers: map[string]string{
				"Authorization": "Bearer header-secret",
				"X-Region":      "test",
			},
		}},
	}
	baseline.Overrides = append(baseline.Overrides, modelOverride)
	candidate.Overrides = append(candidate.Overrides, modelOverride)
	source := promptIterResult(baseline, candidate, true)
	trainRun := source.Rounds[0].Train.EvalSets[0].Cases[0].RunDetails[0].Inference
	trainRun.Inferences[0].UserContent.Content = "api_key=input-secret"
	trainRun.Inferences[0].Tools = []*evalset.Tool{{
		Name:      "lookup",
		Arguments: map[string]any{"access_token": "tool-argument-secret"},
		Result:    map[string]any{"password": "tool-result-secret"},
	}, {
		Name:      "custom_payload",
		Arguments: failingAuditPayload{secret: "unserializable-argument-secret"},
		Result:    failingAuditPayload{secret: "unserializable-result-secret"},
	}}
	trainRun.Inferences[0].FinalResponse.Content = strings.Repeat("response ", 32) +
		"Authorization: Bearer response-secret"
	trainRun.ExecutionTraces[0].Steps[0].Input.Text = "authorization=trace-input-secret"
	trainRun.ExecutionTraces[0].Steps[0].Output.Text = "password=trace-output-secret"

	spec := auditSpec()
	spec.Audit = regression.AuditPolicy{IncludeRawContent: true, MaxContentBytes: 96}
	result := analyzeWith(t, spec, source, regression.UsageSupplement{})
	payload, err := report.JSON(result)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(payload)
	for _, secret := range []string{
		"prompt-secret", "candidate-secret", "model-secret", "header-secret",
		"url-user", "url-password", "url-secret",
		"tool-argument-secret", "tool-result-secret", "response-secret", "input-secret",
		"unserializable-argument-secret", "unserializable-result-secret",
		"trace-input-secret", "trace-output-secret",
	} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("audit report leaked %q: %s", secret, serialized)
		}
	}
	if !strings.Contains(serialized, "[REDACTED]") || !strings.Contains(serialized, "[TRUNCATED]") {
		t.Fatalf("audit report did not record redaction/truncation: %s", serialized)
	}
	if !strings.Contains(serialized, "[UNSERIALIZABLE:") {
		t.Fatalf("audit report did not safely represent unsupported tool payloads: %s", serialized)
	}
	*source.Rounds[0].OutputProfile.Overrides[0].Value.Text = "mutated after audit"
	if got := *result.Candidates[0].Candidate.Profile.Overrides[0].Value.Text; got == "mutated after audit" {
		t.Fatal("candidate audit snapshot aliases the PromptIter result")
	}
}

func TestScenarioCustomPolicyModulesCannotLeakSecretsIntoAudit(t *testing.T) {
	analyzer, err := regression.New(regression.Dependencies{
		Attributor: attributorFunc(func(
			_ context.Context,
			result *regression.CaseResult,
		) (*regression.AttributionResult, error) {
			return &regression.AttributionResult{
				CaseID:   result.CaseID,
				Category: regression.FailureUnknown,
				Reason:   "api_key=attributor-secret",
				Evidence: []regression.Evidence{{
					Source: "custom", Path: "token=path-secret",
					Reason: "authorization=evidence-secret",
				}},
			}, nil
		}),
		DeltaEngine: delta.New(1e-9),
		Gate: gateFunc(func(*regression.GateInput) (*regression.GateDecision, error) {
			return &regression.GateDecision{
				Decision: regression.DecisionAccepted,
				Warnings: []string{"password=gate-warning-secret"},
				Reasons:  []string{"access_token=gate-reason-secret"},
				Rules: []regression.GateRuleResult{{
					Rule: "custom", Passed: true,
					Observed: map[string]any{
						"secret":  "observed-secret",
						"payload": failingAuditPayload{secret: "gate-payload-secret"},
					},
				}},
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := auditSpec()
	spec.Audit = regression.AuditPolicy{IncludeRawContent: true}
	result, err := analyzer.Analyze(
		context.Background(), spec,
		promptIterResult(profile("baseline"), profile("candidate"), true),
		regression.UsageSupplement{},
	)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := report.JSON(result)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(payload)
	for _, secret := range []string{
		"attributor-secret", "path-secret", "evidence-secret",
		"gate-warning-secret", "gate-reason-secret", "observed-secret",
		"gate-payload-secret",
	} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("custom policy output leaked %q: %s", secret, serialized)
		}
	}
	if !strings.Contains(serialized, "[UNSERIALIZABLE:") {
		t.Fatalf("custom gate payload was not converted to safe audit evidence: %s", serialized)
	}
}

func TestAnalyzerUsesRawEvidenceBeforeReportSanitization(t *testing.T) {
	source := promptIterResult(profile("baseline"), profile("candidate"), true)
	invocation := source.Rounds[0].Train.EvalSets[0].Cases[0].RunDetails[0].Inference.Inferences[0]
	invocation.FinalResponse.Content = "raw final response"
	invocation.Tools = []*evalset.Tool{{
		Name: "lookup", Arguments: map[string]any{"query": "raw tool arguments"},
		Result: map[string]any{"value": "raw tool result"},
	}}

	analyzer, err := regression.New(regression.Dependencies{
		Attributor: attributorFunc(func(_ context.Context, result *regression.CaseResult) (*regression.AttributionResult, error) {
			if result.EvalSetID != "train" {
				return &regression.AttributionResult{
					EvalSetID: result.EvalSetID, CaseID: result.CaseID,
					Category: regression.FailureUnknown, Reason: "validation evidence",
					Evidence: []regression.Evidence{{Source: "metric", Path: "metrics", Reason: "validation evidence"}},
				}, nil
			}
			observation := result.Runs[0]
			if observation.FinalResponse != "raw final response" ||
				!strings.Contains(observation.Tools[0].Arguments, "raw tool arguments") ||
				!strings.Contains(observation.Tools[0].Result, "raw tool result") {
				t.Fatalf("attributor received incomplete evidence: %+v", observation)
			}
			return &regression.AttributionResult{
				EvalSetID: result.EvalSetID, CaseID: result.CaseID,
				Category: regression.FailureToolArgument,
				Reason:   "raw tool arguments identify the failure",
				Evidence: []regression.Evidence{{Source: "tool", Path: "runs[0].tools[0].arguments", Reason: observation.Tools[0].Arguments}},
			}, nil
		}),
		DeltaEngine: delta.New(1e-9),
		Gate:        gate.NewPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := analyzer.Analyze(
		context.Background(), auditSpec(), source,
		regression.UsageSupplement{
			CostBreakdown: regression.CostBreakdown{
				CostEstimate:        regression.CostEstimate{CostKnown: true, PricingSource: "test"},
				RoundEstimatedCosts: map[int]float64{1: 0},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	baselineAttribution := attributionFor(result, regression.AttributionBaselineTrain, "train-case")
	if baselineAttribution == nil || baselineAttribution.Category != regression.FailureToolArgument {
		t.Fatalf("unexpected attribution: %+v", result.Attributions)
	}
	payload, err := report.JSON(result)
	if err != nil {
		t.Fatal(err)
	}
	var persisted regression.RunResult
	if err := json.Unmarshal(payload, &persisted); err != nil {
		t.Fatal(err)
	}
	observation := persisted.BaselineTrain.Cases[0].Runs[0]
	if persisted.BaselineTrain.Cases[0].Input != "" || observation.FinalResponse != "" ||
		observation.Tools[0].Arguments != "" || observation.Tools[0].Result != "" ||
		observation.Trace[0].Input != "" || observation.Trace[0].Output != "" {
		t.Fatalf("persisted report retained raw evidence: %+v", observation)
	}
}

func TestAnalyzerRetainsExpectedInvocationForAttribution(t *testing.T) {
	source := promptIterResult(profile("baseline"), profile("candidate"), true)
	trainCase := &source.Rounds[0].Train.EvalSets[0].Cases[0]
	trainCase.Metrics[0].Reason = "contract rejected"
	trainCase.RunResults[0].OverallEvalMetricResults[0].Details.Reason = "contract rejected"
	expected := model.NewAssistantMessage("expected response")
	actual := model.NewAssistantMessage("response")
	trainCase.RunResults[0].EvalMetricResultPerInvocation = []*evalresult.EvalMetricResultPerInvocation{{
		ExpectedInvocation: &evalset.Invocation{FinalResponse: &expected},
		ActualInvocation:   &evalset.Invocation{FinalResponse: &actual},
	}}

	result := analyze(t, source)
	baselineAttribution := attributionFor(result, regression.AttributionBaselineTrain, "train-case")
	if baselineAttribution == nil || baselineAttribution.Category != regression.FailureFinalResponseMismatch {
		t.Fatalf("attributions = %+v", result.Attributions)
	}
	run := result.BaselineTrain.Cases[0].Runs[0]
	if run.ExpectedFinalResponse != "expected response" {
		t.Fatalf("expected final response = %q", run.ExpectedFinalResponse)
	}
	payload, err := report.JSON(result)
	if err != nil {
		t.Fatal(err)
	}
	var persisted regression.RunResult
	if err := json.Unmarshal(payload, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.BaselineTrain.Cases[0].Runs[0].ExpectedFinalResponse != "" {
		t.Fatal("report retained raw expected response while raw content was disabled")
	}
}

func TestAnalyzerAttributesFailuresAcrossAuditSnapshots(t *testing.T) {
	source := promptIterResult(profile("baseline"), profile("candidate"), false)
	setEvaluationMetric(
		source.Rounds[0].Validation, "quality", 0,
		status.EvalStatusFailed, "candidate response mismatch",
	)
	result := analyze(t, source)

	observed := make(map[string]int)
	for _, attribution := range result.Attributions {
		observed[string(attribution.Phase)+"/"+attribution.CandidateID]++
	}
	if observed["baseline_train/"] != 1 || observed["baseline_validation/"] != 1 {
		t.Fatalf("baseline attribution scopes = %v", observed)
	}
	if observed["candidate_validation/"+result.Candidates[0].Candidate.ID] != 1 {
		t.Fatalf("candidate attribution scopes = %v", observed)
	}
}

func TestAnalyzerUsesPerRoundAndCumulativeUsageForCandidateGates(t *testing.T) {
	source := promptIterResult(profile("baseline"), profile("candidate-1"), true)
	appendFollowUpRound(
		source, profile("candidate-2"),
		evaluationResult("train", "train-case", 1, status.EvalStatusPassed, ""),
		evaluationResult("validation", "validation-case", 1, status.EvalStatusPassed, ""),
		true,
	)
	source.BaselineValidation.Usage = promptiter.Usage{Calls: 1, TotalTokens: 10, Complete: true}
	source.Rounds[0].Usage = promptiter.Usage{Calls: 2, TotalTokens: 20, Complete: true}
	source.Rounds[0].Duration = 20 * time.Millisecond
	source.Rounds[1].Usage = promptiter.Usage{Calls: 2, TotalTokens: 20, Complete: true}
	source.Rounds[1].Duration = 30 * time.Millisecond
	source.Usage = promptiter.Usage{Calls: 5, TotalTokens: 50, Complete: true}
	spec := auditSpec()
	spec.Budget.MaxCalls = 3
	result := analyzeWith(t, spec, source, regression.UsageSupplement{
		CostBreakdown: regression.CostBreakdown{
			CostEstimate: regression.CostEstimate{
				EstimatedCost: 0.5, CostKnown: true, PricingSource: "test-pricing",
			},
			BaselineEstimatedCost: 0.1,
			RoundEstimatedCosts:   map[int]float64{1: 0.2, 2: 0.2},
		},
	})

	if result.Candidates[0].RoundUsage.Calls != 2 ||
		result.Candidates[0].RoundUsage.PromptIterLatency != 20*time.Millisecond ||
		result.Candidates[0].RoundUsage.EstimatedCost != 0.2 {
		t.Fatalf("round 1 usage = %+v", result.Candidates[0].RoundUsage)
	}
	if result.Candidates[0].CumulativeUsage.Calls != 3 ||
		result.Candidates[1].CumulativeUsage.Calls != 5 {
		t.Fatalf("cumulative usage = %+v / %+v",
			result.Candidates[0].CumulativeUsage, result.Candidates[1].CumulativeUsage)
	}
	if !gateRule(result.Candidates[0].Gate, "call_budget").Passed ||
		gateRule(result.Candidates[1].Gate, "call_budget").Passed {
		t.Fatalf("candidate gates did not use cumulative usage: %+v", result.Candidates)
	}
}

func gateRule(decision *regression.GateDecision, name string) regression.GateRuleResult {
	if decision == nil {
		return regression.GateRuleResult{}
	}
	for _, rule := range decision.Rules {
		if rule.Rule == name {
			return rule
		}
	}
	return regression.GateRuleResult{}
}

func TestScenarioCanceledAuditStopsBeforeConsumingEvaluationEvidence(t *testing.T) {
	analyzer, err := regression.New(regression.Dependencies{
		Attributor: attributorFunc(func(
			context.Context,
			*regression.CaseResult,
		) (*regression.AttributionResult, error) {
			t.Fatal("attributor was called after the audit was canceled")
			return nil, nil
		}),
		DeltaEngine: delta.New(1e-9),
		Gate:        gate.NewPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := analyzer.Analyze(
		ctx, auditSpec(),
		promptIterResult(profile("baseline"), profile("candidate"), true),
		regression.UsageSupplement{},
	)
	if !errors.Is(err, context.Canceled) || result.Status != regression.RunStatusCanceled {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func analyze(t *testing.T, source *engine.RunResult) *regression.RunResult {
	t.Helper()
	return analyzeWith(t, auditSpec(), source, regression.UsageSupplement{
		CostBreakdown: regression.CostBreakdown{CostEstimate: regression.CostEstimate{
			CostKnown: true, PricingSource: "test",
		}},
	})
}

func attributionFor(
	result *regression.RunResult,
	phase regression.AttributionPhase,
	caseID string,
) *regression.AttributionResult {
	if result == nil {
		return nil
	}
	for index := range result.Attributions {
		attribution := &result.Attributions[index]
		if attribution.Phase == phase && attribution.CaseID == caseID {
			return attribution
		}
	}
	return nil
}

func analyzeWith(
	t *testing.T,
	spec *regression.RunSpec,
	source *engine.RunResult,
	usage regression.UsageSupplement,
) *regression.RunResult {
	t.Helper()
	usage = completeTestCostBreakdown(source, usage)
	analyzer, err := regression.New(regression.Dependencies{
		Attributor:  attribution.NewRules(),
		DeltaEngine: delta.New(1e-9),
		Gate:        gate.NewPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := analyzer.Analyze(context.Background(), spec, source, usage)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func completeTestCostBreakdown(
	source *engine.RunResult,
	usage regression.UsageSupplement,
) regression.UsageSupplement {
	if !usage.CostKnown || usage.RoundEstimatedCosts != nil || source == nil {
		return usage
	}
	usage.BaselineEstimatedCost = usage.EstimatedCost
	usage.RoundEstimatedCosts = make(map[int]float64, len(source.Rounds))
	for _, round := range source.Rounds {
		usage.RoundEstimatedCosts[round.Round] = 0
	}
	return usage
}

func auditSpec() *regression.RunSpec {
	return &regression.RunSpec{
		RunID: "run", TargetSurfaceID: "agent#instruction",
		InputFingerprint: "fingerprint",
		Runtime:          regression.RuntimePolicy{Seed: 7, NumRuns: 1},
		MetricPolicies: map[string]regression.MetricPolicy{
			"quality": {Weight: 1},
		},
		Gate: regression.GatePolicy{
			MinValidationGain: .1,
			MaxCaseRegression: 1,
			RejectAnyNewFail:  true,
		},
	}
}

func promptIterResult(
	baseline *promptiter.Profile,
	candidate *promptiter.Profile,
	accepted bool,
) *engine.RunResult {
	baselineTrain := evaluationResult("train", "train-case", 0, status.EvalStatusFailed, "answer mismatch")
	baselineValidation := evaluationResult("validation", "validation-case", 0, status.EvalStatusFailed, "answer mismatch")
	candidateValidation := evaluationResult("validation", "validation-case", 1, status.EvalStatusPassed, "")
	minimumGain := .5
	if !accepted {
		minimumGain = 2
	}
	return &engine.RunResult{
		Status: engine.RunStatusSucceeded,
		Configuration: engine.RunConfiguration{
			EvaluationOptions: engine.EvaluationOptions{NumRuns: 1},
			AcceptancePolicy:  engine.AcceptancePolicy{MinScoreGain: minimumGain},
			MaxRounds:         1,
			TargetSurfaceIDs:  []string{"agent#instruction"},
		},
		InitialProfile:     baseline,
		CurrentRound:       1,
		BaselineValidation: baselineValidation,
		AcceptedProfile: func() *promptiter.Profile {
			if accepted {
				return candidate
			}
			return baseline
		}(),
		Usage: promptiter.Usage{Complete: true},
		Rounds: []engine.RoundResult{
			{
				Round: 1, InputProfile: baseline, Train: baselineTrain,
				OutputProfile: candidate, Validation: candidateValidation,
				Acceptance: &engine.AcceptanceDecision{Accepted: accepted, ScoreDelta: 1, Reason: "scripted"},
				Stop:       &engine.StopDecision{ShouldStop: true, Reason: "maximum rounds reached"},
			},
		},
	}
}

func appendFollowUpRound(
	source *engine.RunResult,
	outputProfile *promptiter.Profile,
	train *engine.EvaluationResult,
	validation *engine.EvaluationResult,
	accepted bool,
) {
	roundNumber := len(source.Rounds) + 1
	inputProfile := source.AcceptedProfile
	if len(source.Rounds) > 0 {
		source.Rounds[len(source.Rounds)-1].Stop = &engine.StopDecision{}
	}
	source.Rounds = append(source.Rounds, engine.RoundResult{
		Round:         roundNumber,
		InputProfile:  inputProfile,
		Train:         train,
		OutputProfile: outputProfile,
		Validation:    validation,
		Acceptance:    &engine.AcceptanceDecision{Accepted: accepted, ScoreDelta: 0, Reason: "scripted follow-up"},
		Stop:          &engine.StopDecision{ShouldStop: true, Reason: "maximum rounds reached"},
	})
	source.CurrentRound = roundNumber
	source.Configuration.MaxRounds = roundNumber
	if accepted {
		source.AcceptedProfile = outputProfile
	}
}

func evaluationResult(
	setID string,
	caseID string,
	score float64,
	metricStatus status.EvalStatus,
	reason string,
) *engine.EvaluationResult {
	message := model.NewUserMessage("input")
	response := model.NewAssistantMessage("response")
	trace := &atrace.Trace{
		SessionID: "session", Status: atrace.TraceStatusCompleted,
		Steps: []atrace.Step{{
			StepID: "step", NodeID: "agent",
			AppliedSurfaceIDs: []string{"agent#instruction"},
			Input:             &atrace.Snapshot{Text: "input"}, Output: &atrace.Snapshot{Text: "response"},
		}},
	}
	return &engine.EvaluationResult{
		OverallScore: score,
		EvalSets: []engine.EvalSetResult{{
			EvalSetID: setID, OverallScore: score,
			Cases: []engine.CaseResult{{
				EvalSetID: setID, EvalCaseID: caseID, SessionID: "session", Trace: trace,
				RunResults: []*evalresult.EvalCaseResult{{
					RunID: 1,
					OverallEvalMetricResults: []*evalresult.EvalMetricResult{{
						MetricName: "quality", Score: score, Threshold: 1,
						EvalStatus: metricStatus,
						Details:    &evalresult.EvalMetricResultDetails{Reason: reason},
					}},
				}},
				RunDetails: []*evaluation.EvaluationCaseRunDetails{{
					RunID: 1,
					Inference: &evaluation.EvaluationInferenceDetails{
						SessionID:       "session",
						Inferences:      []*evalset.Invocation{{UserContent: &message, FinalResponse: &response}},
						ExecutionTraces: []*atrace.Trace{trace},
					},
				}},
				Metrics: []engine.MetricResult{{
					MetricName: "quality", Score: score, Threshold: 1,
					Status: metricStatus, Reason: reason,
					Details: &evalresult.EvalMetricResultDetails{Reason: reason},
				}},
			}},
		}},
	}
}

func profile(text string) *promptiter.Profile {
	return &promptiter.Profile{
		StructureID: "structure",
		Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: "agent#instruction",
			Value:     astructure.SurfaceValue{Text: &text},
		}},
	}
}
