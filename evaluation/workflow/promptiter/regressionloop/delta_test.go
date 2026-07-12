// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.

package regressionloop

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestDeltaNewlyPassed(t *testing.T) {
	baseline := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case1",
						Metrics: []engine.MetricResult{
							{MetricName: "m1", Score: 0.0, Status: status.EvalStatusFailed},
						},
					},
				},
			},
		},
	}

	candidate := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case1",
						Metrics: []engine.MetricResult{
							{MetricName: "m1", Score: 1.0, Status: status.EvalStatusPassed},
						},
					},
				},
			},
		},
	}

	deltas := ComputeDeltas(baseline, candidate)
	assert.Len(t, deltas, 1)
	assert.Equal(t, DeltaNewlyPassed, deltas[0].DeltaType)
	assert.InDelta(t, 1.0, deltas[0].ScoreDelta, 0.0001)
}

func TestDeltaNewlyFailed(t *testing.T) {
	baseline := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case1",
						Metrics: []engine.MetricResult{
							{MetricName: "m1", Score: 1.0, Status: status.EvalStatusPassed},
						},
					},
				},
			},
		},
	}

	candidate := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case1",
						Metrics: []engine.MetricResult{
							{MetricName: "m1", Score: 0.0, Status: status.EvalStatusFailed},
						},
					},
				},
			},
		},
	}

	deltas := ComputeDeltas(baseline, candidate)
	assert.Len(t, deltas, 1)
	assert.Equal(t, DeltaNewlyFailed, deltas[0].DeltaType)
	assert.InDelta(t, -1.0, deltas[0].ScoreDelta, 0.0001)
}

func TestDeltaScoreUp(t *testing.T) {
	baseline := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case1",
						Metrics: []engine.MetricResult{
							{MetricName: "m1", Score: 0.5, Status: status.EvalStatusPassed},
						},
					},
				},
			},
		},
	}

	candidate := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case1",
						Metrics: []engine.MetricResult{
							{MetricName: "m1", Score: 0.8, Status: status.EvalStatusPassed},
						},
					},
				},
			},
		},
	}

	deltas := ComputeDeltas(baseline, candidate)
	assert.Len(t, deltas, 1)
	assert.Equal(t, DeltaScoreUp, deltas[0].DeltaType)
	assert.InDelta(t, 0.3, deltas[0].ScoreDelta, 0.0001)
}

func TestDeltaScoreDown(t *testing.T) {
	baseline := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case1",
						Metrics: []engine.MetricResult{
							{MetricName: "m1", Score: 0.8, Status: status.EvalStatusPassed},
						},
					},
				},
			},
		},
	}

	candidate := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case1",
						Metrics: []engine.MetricResult{
							{MetricName: "m1", Score: 0.5, Status: status.EvalStatusPassed},
						},
					},
				},
			},
		},
	}

	deltas := ComputeDeltas(baseline, candidate)
	assert.Len(t, deltas, 1)
	assert.Equal(t, DeltaScoreDown, deltas[0].DeltaType)
	assert.InDelta(t, -0.3, deltas[0].ScoreDelta, 0.0001)
}

func TestDeltaUnchanged(t *testing.T) {
	baseline := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case1",
						Metrics: []engine.MetricResult{
							{MetricName: "m1", Score: 0.7, Status: status.EvalStatusPassed},
						},
					},
				},
			},
		},
	}

	candidate := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case1",
						Metrics: []engine.MetricResult{
							{MetricName: "m1", Score: 0.7, Status: status.EvalStatusPassed},
						},
					},
				},
			},
		},
	}

	deltas := ComputeDeltas(baseline, candidate)
	assert.Len(t, deltas, 1)
	assert.Equal(t, DeltaUnchanged, deltas[0].DeltaType)
	assert.Equal(t, 0.0, deltas[0].ScoreDelta)
}

func TestDeltaMissing(t *testing.T) {
	baseline := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case1",
						Metrics: []engine.MetricResult{
							{MetricName: "m1", Score: 0.7, Status: status.EvalStatusPassed},
						},
					},
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case2",
						Metrics: []engine.MetricResult{
							{MetricName: "m1", Score: 0.5, Status: status.EvalStatusPassed},
						},
					},
				},
			},
		},
	}

	candidate := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case1",
						Metrics: []engine.MetricResult{
							{MetricName: "m1", Score: 0.8, Status: status.EvalStatusPassed},
						},
					},
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case3",
						Metrics: []engine.MetricResult{
							{MetricName: "m1", Score: 0.9, Status: status.EvalStatusPassed},
						},
					},
				},
			},
		},
	}

	deltas := ComputeDeltas(baseline, candidate)
	assert.Len(t, deltas, 3)

	var missingCount int
	for _, delta := range deltas {
		if delta.DeltaType == DeltaMissing {
			missingCount++
		}
	}
	assert.Equal(t, 2, missingCount)
}

func TestDeltaMultipleMetrics(t *testing.T) {
	baseline := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case1",
						Metrics: []engine.MetricResult{
							{MetricName: "m1", Score: 0.6, Status: status.EvalStatusPassed},
							{MetricName: "m2", Score: 0.4, Status: status.EvalStatusPassed},
						},
					},
				},
			},
		},
	}

	candidate := &engine.EvaluationResult{
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID: "test_set",
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "test_set",
						EvalCaseID: "case1",
						Metrics: []engine.MetricResult{
							{MetricName: "m1", Score: 0.8, Status: status.EvalStatusPassed},
							{MetricName: "m2", Score: 0.6, Status: status.EvalStatusPassed},
						},
					},
				},
			},
		},
	}

	deltas := ComputeDeltas(baseline, candidate)
	assert.Len(t, deltas, 1)
	assert.Equal(t, DeltaScoreUp, deltas[0].DeltaType)
	assert.InDelta(t, 0.2, deltas[0].ScoreDelta, 0.0001)
	assert.InDelta(t, 0.5, deltas[0].BaselineScore, 0.0001)
	assert.InDelta(t, 0.7, deltas[0].CandidateScore, 0.0001)
}

func TestGetDeltaSummary(t *testing.T) {
	deltas := []CaseDelta{
		{DeltaType: DeltaNewlyPassed},
		{DeltaType: DeltaNewlyPassed},
		{DeltaType: DeltaNewlyFailed},
		{DeltaType: DeltaScoreUp},
		{DeltaType: DeltaScoreDown},
		{DeltaType: DeltaUnchanged},
	}

	summary := GetDeltaSummary(deltas)
	assert.Equal(t, 2, summary["newlyPassed"])
	assert.Equal(t, 1, summary["newlyFailed"])
	assert.Equal(t, 1, summary["scoreUp"])
	assert.Equal(t, 1, summary["scoreDown"])
	assert.Equal(t, 1, summary["unchanged"])
}

func TestCountNewlyFailed(t *testing.T) {
	deltas := []CaseDelta{
		{DeltaType: DeltaNewlyFailed},
		{DeltaType: DeltaNewlyFailed},
		{DeltaType: DeltaNewlyPassed},
		{DeltaType: DeltaScoreUp},
	}

	assert.Equal(t, 2, CountNewlyFailed(deltas))
}

func TestCountRegressedCases(t *testing.T) {
	deltas := []CaseDelta{
		{DeltaType: DeltaNewlyFailed},
		{DeltaType: DeltaScoreDown},
		{DeltaType: DeltaNewlyPassed},
		{DeltaType: DeltaScoreUp},
	}

	assert.Equal(t, 2, CountRegressedCases(deltas))
}
