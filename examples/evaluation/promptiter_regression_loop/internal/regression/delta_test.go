//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

const testEvalSetID = "validation"

func TestCompareClassifiesAndSortsAllDeltaKinds(t *testing.T) {
	baseline := &EvaluationResult{
		OverallScore: 0.52,
		Cases: []CaseResult{
			caseWithMetric("e", 0.7, status.EvalStatusPassed),
			caseWithMetric("d", 0.4, status.EvalStatusFailed),
			caseWithMetric("c", 0.5, status.EvalStatusPassed),
			caseWithMetric("b", 1.0, status.EvalStatusPassed),
			caseWithMetric("a", 0.0, status.EvalStatusFailed),
		},
	}
	candidate := &EvaluationResult{
		OverallScore: 0.54,
		Cases: []CaseResult{
			caseWithMetric("a", 1.0, status.EvalStatusPassed),
			caseWithMetric("b", 0.0, status.EvalStatusFailed),
			caseWithMetric("c", 0.8, status.EvalStatusPassed),
			caseWithMetric("d", 0.2, status.EvalStatusFailed),
			caseWithMetric("e", 0.7+scoreEpsilon/2, status.EvalStatusPassed),
		},
	}

	delta, err := Compare(baseline, candidate)
	require.NoError(t, err)
	require.Len(t, delta.Cases, 5)
	assert.InDelta(t, 0.02, delta.ScoreDelta, scoreEpsilon)
	assert.Equal(t, []DeltaKind{
		DeltaNewPass, DeltaNewFail, DeltaImproved, DeltaDeclined, DeltaUnchanged,
	}, deltaKinds(delta.Cases))
	for _, kind := range []DeltaKind{
		DeltaNewPass, DeltaNewFail, DeltaImproved, DeltaDeclined, DeltaUnchanged,
	} {
		assert.Equal(t, 1, delta.Counts[kind])
	}
	assert.Equal(t, []string{"a", "b", "c", "d", "e"}, caseIDs(delta.Cases))
	assert.Equal(t, DeltaNewPass, delta.Cases[0].Metrics[0].Kind)
}

func TestCompareRejectsInvalidOrMisalignedData(t *testing.T) {
	valid := evaluationWithCases(caseWithMetric("a", 1, status.EvalStatusPassed))
	tests := []struct {
		name      string
		baseline  *EvaluationResult
		candidate *EvaluationResult
		contains  string
	}{
		{name: "nil baseline", candidate: valid, contains: "baseline evaluation is nil"},
		{name: "non finite score", baseline: &EvaluationResult{OverallScore: math.NaN()}, candidate: valid, contains: "not finite"},
		{name: "empty identity", baseline: evaluationWithCases(CaseResult{Score: 1}), candidate: valid, contains: "case identity is empty"},
		{name: "missing candidate case", baseline: valid, candidate: evaluationWithCases(caseWithMetric("b", 1, status.EvalStatusPassed)), contains: "missing case"},
		{name: "candidate count mismatch", baseline: valid, candidate: evaluationWithCases(caseWithMetric("a", 1, status.EvalStatusPassed), caseWithMetric("b", 1, status.EvalStatusPassed)), contains: "case set does not match"},
		{name: "duplicate case", baseline: evaluationWithCases(caseWithMetric("a", 1, status.EvalStatusPassed), caseWithMetric("a", 1, status.EvalStatusPassed)), candidate: valid, contains: "duplicate case"},
		{name: "empty metrics", baseline: evaluationWithCases(CaseResult{EvalSetID: testEvalSetID, CaseID: "a", Score: 1, Passed: true}), candidate: valid, contains: "metrics are empty"},
		{name: "empty metric name", baseline: evaluationWithCases(caseWithNamedMetric("a", "", 1, status.EvalStatusPassed)), candidate: valid, contains: "metric name is empty"},
		{name: "duplicate metric", baseline: evaluationWithCases(caseWithTwoMetrics("a", "quality", "quality")), candidate: valid, contains: "duplicate metric"},
		{name: "non finite metric", baseline: evaluationWithCases(caseWithNamedMetric("a", "quality", math.Inf(1), status.EvalStatusPassed)), candidate: valid, contains: "not finite"},
		{name: "unknown status", baseline: evaluationWithCases(caseWithMetric("a", 1, status.EvalStatusUnknown)), candidate: valid, contains: "invalid status"},
		{name: "metric mismatch", baseline: valid, candidate: evaluationWithCases(caseWithNamedMetric("a", "other", 1, status.EvalStatusPassed)), contains: "missing metric"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compare(test.baseline, test.candidate)
			require.ErrorContains(t, err, test.contains)
		})
	}
}

func TestCompareRepresentsBaselineNotEvaluatedForAuditing(t *testing.T) {
	tests := []struct {
		name           string
		candidate      CaseResult
		wantKind       DeltaKind
		wantMetricKind DeltaKind
	}{
		{
			name: "candidate passes", candidate: caseWithMetric("a", 1, status.EvalStatusPassed),
			wantKind: DeltaNewPass, wantMetricKind: DeltaNewPass,
		},
		{
			name: "candidate remains failed", candidate: caseWithMetric("a", 0, status.EvalStatusFailed),
			wantKind: DeltaUnchanged, wantMetricKind: DeltaUnchanged,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := evaluationWithCases(caseWithMetric("a", 0, status.EvalStatusNotEvaluated))
			delta, err := Compare(baseline, evaluationWithCases(test.candidate))
			require.NoError(t, err)
			require.Len(t, delta.Cases, 1)
			assert.Equal(t, test.wantKind, delta.Cases[0].Kind)
			require.Len(t, delta.Cases[0].Metrics, 1)
			assert.Equal(t, status.EvalStatusNotEvaluated, delta.Cases[0].Metrics[0].BaselineStatus)
			assert.Equal(t, test.wantMetricKind, delta.Cases[0].Metrics[0].Kind)
		})
	}
}

func TestCompareRepresentsCandidateNotEvaluatedForAuditing(t *testing.T) {
	baseline := evaluationWithCases(caseWithMetric("a", 1, status.EvalStatusPassed))
	candidate := evaluationWithCases(caseWithMetric("a", 0, status.EvalStatusNotEvaluated))

	delta, err := Compare(baseline, candidate)
	require.NoError(t, err)
	require.Len(t, delta.Cases, 1)
	assert.Equal(t, DeltaNewFail, delta.Cases[0].Kind)
	assert.Equal(t, status.EvalStatusNotEvaluated, delta.Cases[0].Metrics[0].CandidateStatus)
}

func TestCompareRejectsInconsistentAggregateScores(t *testing.T) {
	baseline := evaluationWithCases(caseWithMetric("a", 0.5, status.EvalStatusFailed))
	candidate := evaluationWithCases(caseWithMetric("a", 0.5, status.EvalStatusFailed))
	candidate.OverallScore = 1
	_, err := Compare(baseline, candidate)
	require.ErrorContains(t, err, "overall score does not match")

	candidate = evaluationWithCases(caseWithMetric("a", 0.5, status.EvalStatusFailed))
	candidate.Cases[0].Score = 1
	candidate.OverallScore = 1
	_, err = Compare(baseline, candidate)
	require.ErrorContains(t, err, "score does not match metrics")
}

func TestAggregateScoreMatchesPromptIterMetricWeighting(t *testing.T) {
	first := caseWithMetric("first", 0.5, status.EvalStatusFailed)
	first.Metrics = []MetricResult{
		{Name: "a", Score: 1, Status: status.EvalStatusPassed},
		{Name: "b", Score: 0, Status: status.EvalStatusFailed},
	}
	second := caseWithMetric("second", 1, status.EvalStatusPassed)
	third := caseWithMetric("third", 0, status.EvalStatusFailed)
	third.EvalSetID = "other"
	result := &EvaluationResult{OverallScore: 1.0 / 3.0, Cases: []CaseResult{first, second, third}}
	_, err := indexCases("weighted", result)
	require.NoError(t, err)

	withNotEvaluated := caseWithMetric("not-evaluated", 1, status.EvalStatusPassed)
	withNotEvaluated.Passed = false
	withNotEvaluated.Metrics = append(withNotEvaluated.Metrics, MetricResult{
		Name: "skipped", Status: status.EvalStatusNotEvaluated,
	})
	_, err = indexCases("not-evaluated", &EvaluationResult{
		OverallScore: 1, Cases: []CaseResult{withNotEvaluated},
	})
	require.NoError(t, err)
}

func TestSummarizeDeltaCopiesAggregateData(t *testing.T) {
	input := &DeltaSummary{
		ScoreDelta: 0.5, Counts: map[DeltaKind]int{DeltaNewPass: 1},
		Cases: []CaseDelta{{CaseID: "omitted"}},
	}
	overview, err := SummarizeDelta(input)
	require.NoError(t, err)
	assert.Equal(t, input.ScoreDelta, overview.ScoreDelta)
	assert.Equal(t, input.Counts, overview.Counts)
	input.Counts[DeltaNewPass] = 2
	assert.Equal(t, 1, overview.Counts[DeltaNewPass])
	_, err = SummarizeDelta(nil)
	assert.Error(t, err)
}

func caseWithTwoMetrics(caseID, first, second string) CaseResult {
	return CaseResult{
		EvalSetID: testEvalSetID, CaseID: caseID, Score: 1, Passed: true,
		Metrics: []MetricResult{
			{Name: first, Score: 1, Status: status.EvalStatusPassed},
			{Name: second, Score: 1, Status: status.EvalStatusPassed},
		},
		Trace: TraceSummary{Status: "completed", Steps: []TraceStep{}},
	}
}

func caseWithMetric(caseID string, score float64, evalStatus status.EvalStatus) CaseResult {
	return caseWithNamedMetric(caseID, "quality", score, evalStatus)
}

func caseWithNamedMetric(caseID, name string, score float64, evalStatus status.EvalStatus) CaseResult {
	return CaseResult{
		EvalSetID: testEvalSetID,
		CaseID:    caseID,
		Score:     score,
		Passed:    evalStatus == status.EvalStatusPassed,
		Metrics: []MetricResult{{
			Name: name, Score: score, Status: evalStatus,
		}},
		Trace: TraceSummary{Status: "completed", Steps: []TraceStep{}},
	}
}

func evaluationWithCases(cases ...CaseResult) *EvaluationResult {
	return &EvaluationResult{OverallScore: averageCaseScore(cases), Cases: cases}
}

func averageCaseScore(cases []CaseResult) float64 {
	if len(cases) == 0 {
		return 0
	}
	total := 0.0
	for _, item := range cases {
		total += item.Score
	}
	return total / float64(len(cases))
}

func deltaKinds(cases []CaseDelta) []DeltaKind {
	result := make([]DeltaKind, 0, len(cases))
	for _, item := range cases {
		result = append(result, item.Kind)
	}
	return result
}

func caseIDs(cases []CaseDelta) []string {
	result := make([]string, 0, len(cases))
	for _, item := range cases {
		result = append(result, item.CaseID)
	}
	return result
}
