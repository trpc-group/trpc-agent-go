//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package delta

import (
	"math"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

func TestScenarioDeltaExplainsCandidateBehaviorChanges(t *testing.T) {
	scenarios := []struct {
		name               string
		baselinePassed     bool
		candidatePassed    bool
		baselineScore      float64
		candidateScore     float64
		expectedMetricKind regression.ChangeKind
		expectedCaseKind   regression.ChangeKind
	}{
		{"failed case becomes successful", false, true, 0, 1, regression.ChangeNewPass, regression.ChangeNewPass},
		{"successful case becomes a failure", true, false, 1, 0, regression.ChangeNewFail, regression.ChangeNewFail},
		{"passing quality improves", true, true, .4, .8, regression.ChangeImproved, regression.ChangeImproved},
		{"passing quality regresses", true, true, .8, .4, regression.ChangeRegressed, regression.ChangeRegressed},
		{"noise within configured tolerance is unchanged", true, true, .8, .8005, regression.ChangeUnchanged, regression.ChangeUnchanged},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			baseline := snapshot(scenario.baselinePassed, scenario.baselineScore, []regression.MetricResult{{
				Name: "quality", Score: scenario.baselineScore, Passed: scenario.baselinePassed,
			}})
			candidate := snapshot(scenario.candidatePassed, scenario.candidateScore, []regression.MetricResult{{
				Name: "quality", Score: scenario.candidateScore, Passed: scenario.candidatePassed,
			}})
			report, err := New(.001).Compare(baseline, candidate, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := report.Cases[0].Metrics[0].Kind; got != scenario.expectedMetricKind {
				t.Fatalf("metric kind = %q, want %q", got, scenario.expectedMetricKind)
			}
			if got := report.Cases[0].Kind; got != scenario.expectedCaseKind {
				t.Fatalf("case kind = %q, want %q", got, scenario.expectedCaseKind)
			}
		})
	}
}

func TestScenarioCriticalSafetyRegressionCountsReleaseRiskOncePerCase(t *testing.T) {
	baseline := snapshot(true, .9, []regression.MetricResult{
		{Name: "quality", Score: .8, Passed: true},
		{Name: "safety", Score: 1, Passed: true},
	})
	candidate := snapshot(false, .2, []regression.MetricResult{
		{Name: "quality", Score: .4, Passed: false},
		{Name: "safety", Score: 0, Passed: false},
	})
	report, err := New(0).Compare(baseline, candidate, map[string]regression.MetricPolicy{
		"quality": {Weight: 1},
		"safety":  {Weight: 2, HardFail: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.NewFailures != 1 || report.NewHardFailures != 1 || report.CriticalRegressions != 1 {
		t.Fatalf("release-risk counters = %+v", report)
	}
}

func TestScenarioIncompleteEvaluationEvidenceCannotProduceCompleteDelta(t *testing.T) {
	baseline := snapshot(true, .8, []regression.MetricResult{{Name: "quality", Score: .8, Passed: true}})
	candidate := snapshot(true, .8, []regression.MetricResult{{Name: "quality", Score: .8, Passed: true}})

	scenarios := []struct {
		name   string
		mutate func(*regression.EvaluationSnapshot, *regression.EvaluationSnapshot)
		kind   regression.ChangeKind
	}{
		{
			name: "candidate omits a baseline case",
			mutate: func(_ *regression.EvaluationSnapshot, candidate *regression.EvaluationSnapshot) {
				candidate.Cases = nil
			},
			kind: regression.ChangeMissing,
		},
		{
			name: "candidate introduces an unplanned case",
			mutate: func(baseline *regression.EvaluationSnapshot, _ *regression.EvaluationSnapshot) {
				baseline.Cases = nil
			},
			kind: regression.ChangeExtra,
		},
		{
			name: "baseline and candidate expose different metrics",
			mutate: func(baseline, candidate *regression.EvaluationSnapshot) {
				baseline.Cases[0].Metrics[0].Name = "baseline-only"
				candidate.Cases[0].Metrics[0].Name = "candidate-only"
			},
			kind: regression.ChangeMissing,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			baselineCopy := cloneSnapshot(baseline)
			candidateCopy := cloneSnapshot(candidate)
			scenario.mutate(baselineCopy, candidateCopy)
			report, err := New(0).Compare(baselineCopy, candidateCopy, nil)
			if err != nil {
				t.Fatal(err)
			}
			if report.Complete {
				t.Fatalf("incomplete evidence produced a complete delta: %+v", report)
			}
			if len(report.Cases) == 0 {
				t.Fatalf("missing evidence was not preserved: %+v", report)
			}
			if scenario.kind != "" && report.Cases[0].Kind != scenario.kind &&
				(len(report.Cases[0].Metrics) == 0 || report.Cases[0].Metrics[0].Kind != scenario.kind) {
				t.Fatalf("missing evidence kind = %+v, want %q", report.Cases[0], scenario.kind)
			}
		})
	}
}

func TestScenarioWeightedDeltaPreservesEqualEvalSetWeighting(t *testing.T) {
	baseline := &regression.EvaluationSnapshot{
		EvalSetID: "large,small", OverallScore: 0, Complete: true,
		Cases: []regression.CaseResult{
			{EvalSetID: "small", CaseID: "shared", Metrics: []regression.MetricResult{{Name: "quality", Score: 0}}},
			{EvalSetID: "large", CaseID: "shared", Metrics: []regression.MetricResult{{Name: "quality", Score: 0}}},
			{EvalSetID: "large", CaseID: "large-2", Metrics: []regression.MetricResult{{Name: "quality", Score: 0}}},
			{EvalSetID: "large", CaseID: "large-3", Metrics: []regression.MetricResult{{Name: "quality", Score: 0}}},
			{EvalSetID: "large", CaseID: "large-4", Metrics: []regression.MetricResult{{Name: "quality", Score: 0}}},
		},
	}
	candidate := cloneSnapshot(baseline)
	candidate.OverallScore = .5
	candidate.Cases[0].Metrics[0].Score = 1
	report, err := New(0).Compare(baseline, candidate, map[string]regression.MetricPolicy{
		"quality": {Weight: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(report.WeightedScoreDelta-.5) > 1e-12 {
		t.Fatalf("weighted delta = %v, want equal-set delta 0.5", report.WeightedScoreDelta)
	}
}

func TestScenarioMalformedEvaluationEvidenceIsRejectedAtDeltaBoundary(t *testing.T) {
	valid := snapshot(true, .8, []regression.MetricResult{{Name: "quality", Score: .8, Passed: true}})
	scenarios := []struct {
		name   string
		mutate func(*regression.EvaluationSnapshot)
	}{
		{"duplicate case identity", func(value *regression.EvaluationSnapshot) { value.Cases = append(value.Cases, value.Cases[0]) }},
		{"empty case identity", func(value *regression.EvaluationSnapshot) { value.Cases[0].CaseID = "" }},
		{"duplicate metric identity", func(value *regression.EvaluationSnapshot) {
			value.Cases[0].Metrics = append(value.Cases[0].Metrics, value.Cases[0].Metrics[0])
		}},
		{"empty metric identity", func(value *regression.EvaluationSnapshot) { value.Cases[0].Metrics[0].Name = "" }},
		{"non-finite metric score", func(value *regression.EvaluationSnapshot) { value.Cases[0].Metrics[0].Score = math.Inf(1) }},
		{"non-finite overall score", func(value *regression.EvaluationSnapshot) { value.OverallScore = math.NaN() }},
		{"different evaluation set", func(value *regression.EvaluationSnapshot) { value.EvalSetID = "other" }},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			candidate := cloneSnapshot(valid)
			scenario.mutate(candidate)
			if _, err := New(0).Compare(valid, candidate, nil); err == nil {
				t.Fatal("malformed evaluation evidence was accepted")
			}
		})
	}
}

func TestNewNormalizesInvalidEpsilon(t *testing.T) {
	for _, epsilon := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if actual := New(epsilon).Epsilon; actual != 0 {
			t.Fatalf("epsilon %v normalized to %v, want 0", epsilon, actual)
		}
	}
}

func snapshot(
	passed bool,
	score float64,
	metrics []regression.MetricResult,
) *regression.EvaluationSnapshot {
	return &regression.EvaluationSnapshot{
		EvalSetID: "set", OverallScore: score, Complete: true,
		Cases: []regression.CaseResult{{
			CaseID: "critical", Critical: true, Passed: passed, Metrics: metrics,
		}},
	}
}

func cloneSnapshot(source *regression.EvaluationSnapshot) *regression.EvaluationSnapshot {
	cloned := *source
	cloned.Cases = append([]regression.CaseResult(nil), source.Cases...)
	for index := range cloned.Cases {
		cloned.Cases[index].Metrics = append([]regression.MetricResult(nil), source.Cases[index].Metrics...)
	}
	return &cloned
}
