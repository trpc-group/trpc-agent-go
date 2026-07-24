//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestAdaptEvaluationRejectsMalformedEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*engine.EvaluationResult)
		error  string
	}{
		{name: "non finite overall score", mutate: func(value *engine.EvaluationResult) { value.OverallScore = math.NaN() }, error: "overall score must be finite"},
		{name: "empty eval set id", mutate: func(value *engine.EvaluationResult) { value.EvalSets[0].EvalSetID = "" }, error: "evaluation set id is empty"},
		{name: "duplicate eval set", mutate: func(value *engine.EvaluationResult) { value.EvalSets = append(value.EvalSets, value.EvalSets[0]) }, error: "duplicate evaluation set id"},
		{name: "duplicate case", mutate: func(value *engine.EvaluationResult) {
			value.EvalSets[0].Cases = append(value.EvalSets[0].Cases, value.EvalSets[0].Cases[0])
		}, error: "duplicate evaluation case id"},
		{name: "empty case id", mutate: func(value *engine.EvaluationResult) { value.EvalSets[0].Cases[0].EvalCaseID = "" }, error: "evaluation case id is empty"},
		{name: "empty metric name", mutate: func(value *engine.EvaluationResult) { value.EvalSets[0].Cases[0].Metrics[0].MetricName = "" }, error: "metric name is empty"},
		{name: "duplicate aggregate metric", mutate: func(value *engine.EvaluationResult) {
			c := &value.EvalSets[0].Cases[0]
			c.Metrics = append(c.Metrics, c.Metrics[0])
		}, error: "duplicate metric"},
		{name: "non finite aggregate metric", mutate: func(value *engine.EvaluationResult) { value.EvalSets[0].Cases[0].Metrics[0].Score = math.Inf(1) }, error: "score and threshold must be finite"},
		{name: "empty rubric id", mutate: func(value *engine.EvaluationResult) {
			value.EvalSets[0].Cases[0].Metrics[0].Details.RubricScores[0].ID = ""
		}, error: "rubric id is empty"},
		{name: "duplicate rubric", mutate: func(value *engine.EvaluationResult) {
			d := value.EvalSets[0].Cases[0].Metrics[0].Details
			clone := *d.RubricScores[0]
			d.RubricScores = append(d.RubricScores, &clone)
		}, error: "duplicate rubric"},
		{name: "non finite rubric", mutate: func(value *engine.EvaluationResult) {
			value.EvalSets[0].Cases[0].Metrics[0].Details.RubricScores[0].Score = math.NaN()
		}, error: "rubric \"rubric\" score must be finite"},
		{name: "invalid run detail id", mutate: func(value *engine.EvaluationResult) { value.EvalSets[0].Cases[0].RunDetails[0].RunID = 0 }, error: "run detail id 0 must be positive"},
		{name: "duplicate run detail", mutate: func(value *engine.EvaluationResult) {
			c := &value.EvalSets[0].Cases[0]
			c.RunDetails = append(c.RunDetails, c.RunDetails[0])
			c.RunResults = append(c.RunResults, nil)
		}, error: "duplicate run detail id"},
		{name: "invalid run result id", mutate: func(value *engine.EvaluationResult) { value.EvalSets[0].Cases[0].RunResults[0].RunID = 0 }, error: "run result id 0 must be positive"},
		{name: "duplicate run result", mutate: func(value *engine.EvaluationResult) {
			c := &value.EvalSets[0].Cases[0]
			c.RunResults = append(c.RunResults, c.RunResults[0])
			c.RunDetails = append(c.RunDetails, nil)
		}, error: "duplicate run result id"},
		{name: "empty run metric name", mutate: func(value *engine.EvaluationResult) {
			value.EvalSets[0].Cases[0].RunResults[0].OverallEvalMetricResults[0].MetricName = ""
		}, error: "run 1 metric name is empty"},
		{name: "duplicate run metric", mutate: func(value *engine.EvaluationResult) {
			r := value.EvalSets[0].Cases[0].RunResults[0]
			r.OverallEvalMetricResults = append(r.OverallEvalMetricResults, r.OverallEvalMetricResults[0])
		}, error: "duplicate metric"},
		{name: "non finite run metric", mutate: func(value *engine.EvaluationResult) {
			value.EvalSets[0].Cases[0].RunResults[0].OverallEvalMetricResults[0].Threshold = math.Inf(1)
		}, error: "score and threshold must be finite"},
		{name: "threshold mismatch", mutate: func(value *engine.EvaluationResult) {
			value.EvalSets[0].Cases[0].RunResults[0].OverallEvalMetricResults[0].Threshold = .25
		}, error: "threshold does not match"},
		{name: "invalid run metric status", mutate: func(value *engine.EvaluationResult) {
			value.EvalSets[0].Cases[0].RunResults[0].OverallEvalMetricResults[0].EvalStatus = status.EvalStatus("invalid")
		}, error: "invalid status"},
		{name: "detail without result", mutate: func(value *engine.EvaluationResult) {
			value.EvalSets[0].Cases[0].RunResults = []*evalresult.EvalCaseResult{{RunID: 2}}
		}, error: "run detail id 1 has no matching result"},
		{name: "result without detail", mutate: func(value *engine.EvaluationResult) {
			value.EvalSets[0].Cases[0].RunResults = append(
				value.EvalSets[0].Cases[0].RunResults,
				&evalresult.EvalCaseResult{RunID: 2},
			)
		}, error: "run result id 2 has no matching detail"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := richEvidenceEvaluation()
			test.mutate(value)
			_, err := adaptEvaluation(value, testProfile("target", "prompt"), nil)
			require.ErrorContains(t, err, test.error)
		})
	}

	_, err := adaptEvaluation(nil, testProfile("target", "prompt"), nil)
	require.ErrorContains(t, err, "evaluation result and profile are required")
	_, err = adaptEvaluation(richEvidenceEvaluation(), nil, nil)
	require.ErrorContains(t, err, "evaluation result and profile are required")
}

func TestAdaptEvaluationPreservesRichRepeatedRunEvidence(t *testing.T) {
	value := richEvidenceEvaluation()
	caseValue := &value.EvalSets[0].Cases[0]
	secondDetail := cloneEvidenceRunDetail(caseValue.RunDetails[0], 2)
	secondDetail.Inference.Inferences[0].Tools[0].Result = map[string]string{"errorMessage": "backend unavailable"}
	secondDetail.Inference.ExecutionTraces[0].Steps[0].StepID = ""
	secondResult := cloneEvidenceRunResult(caseValue.RunResults[0], 2, .25)
	caseValue.RunDetails = append(caseValue.RunDetails, secondDetail)
	caseValue.RunResults = append(caseValue.RunResults, secondResult)

	snapshot, err := adaptEvaluation(value, testProfile("target", "prompt"), map[string]struct{}{"case": {}})
	require.NoError(t, err)
	require.True(t, snapshot.Complete)
	require.Len(t, snapshot.Cases, 1)
	result := snapshot.Cases[0]
	assert.True(t, result.Critical)
	assert.Equal(t, "question", result.Input)
	assert.InDelta(t, math.Sqrt(.125), snapshot.ScoreStdDev, 1e-9)
	require.Len(t, result.Runs, 2)
	assert.Equal(t, "expected", result.Runs[0].ExpectedFinalResponse)
	assert.Equal(t, "expected-route", result.Runs[0].ExpectedRoute)
	require.Len(t, result.Runs[0].ExpectedTools, 1)
	assert.JSONEq(t, `{"id":"expected"}`, result.Runs[0].ExpectedTools[0].Arguments)
	assert.Equal(t, "backend unavailable", result.Runs[1].Tools[0].Error)
	assert.Equal(t, "step-1", result.Runs[1].Trace[0].StepID)
}

func TestEvidenceCompletenessMarkersFailClosed(t *testing.T) {
	snapshot := &EvaluationSnapshot{Complete: true, Cases: []CaseResult{{
		CaseID: "case", Metrics: []MetricResult{{Name: "quality"}},
		Runs: []Observation{{RunID: 2}},
	}}}
	markConfiguredMetricCoverage(snapshot, map[string]MetricPolicy{"quality": {}, "safety": {}})
	assert.False(t, snapshot.Complete)

	snapshot.Complete = true
	markExpectedRunCoverage(snapshot, 2)
	assert.False(t, snapshot.Complete)
	markConfiguredMetricCoverage(nil, map[string]MetricPolicy{"quality": {}})
	markConfiguredMetricCoverage(snapshot, nil)
	markExpectedRunCoverage(nil, 1)
	markExpectedRunCoverage(snapshot, 0)

	assert.Empty(t, toolResultError("ordinary result"))
	assert.Equal(t, "failure", toolResultError(errors.New(" failure ")))
	assert.Equal(t, "bad request", toolResultError(map[string]any{"err": "bad request"}))
	assert.Empty(t, asStringStringMap(42))
	assert.Equal(t, "[UNSERIALIZABLE:chan int]", marshalAuditValue(make(chan int)))
	assert.Zero(t, scoreStdDev(runMetricScores{1: {}}))
}

func richEvidenceEvaluation() *engine.EvaluationResult {
	user := model.NewUserMessage("question")
	actual := model.NewAssistantMessage("actual")
	expected := model.NewAssistantMessage("expected")
	trace := &atrace.Trace{Steps: []atrace.Step{{
		StepID: "step", NodeID: "agent", Branch: "actual-route",
		Input: &atrace.Snapshot{Text: "question"}, Output: &atrace.Snapshot{Text: "actual"},
	}}}
	expectedTrace := &atrace.Trace{Steps: []atrace.Step{{Branch: "expected-route"}}}
	metric := &evalresult.EvalMetricResult{
		MetricName: "quality", Score: .75, Threshold: .5, EvalStatus: status.EvalStatusPassed,
		Details: &evalresult.EvalMetricResultDetails{RubricScores: []*evalresult.RubricScore{{ID: "rubric", Score: .75, Reason: "acceptable"}}},
	}
	return &engine.EvaluationResult{OverallScore: .75, EvalSets: []engine.EvalSetResult{{
		EvalSetID: "validation", OverallScore: .75, Cases: []engine.CaseResult{{
			EvalSetID: "validation", EvalCaseID: "case", Metrics: []engine.MetricResult{{
				MetricName: "quality", Score: .75, Threshold: .5, Status: status.EvalStatusPassed, Details: metric.Details,
			}},
			RunDetails: []*evaluation.EvaluationCaseRunDetails{{RunID: 1, Inference: &evaluation.EvaluationInferenceDetails{
				Inferences: []*evalset.Invocation{{
					UserContent: &user, FinalResponse: &actual,
					Tools: []*evalset.Tool{{Name: "lookup", Arguments: map[string]any{"id": "actual"}, Result: map[string]any{"status": "ok"}}},
				}},
				ExecutionTraces: []*atrace.Trace{trace},
			}}},
			RunResults: []*evalresult.EvalCaseResult{{
				RunID: 1, OverallEvalMetricResults: []*evalresult.EvalMetricResult{metric},
				EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{{ExpectedInvocation: &evalset.Invocation{
					FinalResponse:  &expected,
					Tools:          []*evalset.Tool{{Name: "lookup", Arguments: map[string]any{"id": "expected"}, Result: map[string]any{"status": "ok"}}},
					ExecutionTrace: expectedTrace,
				}}},
			}},
		}},
	}}}
}

func cloneEvidenceRunDetail(source *evaluation.EvaluationCaseRunDetails, runID int) *evaluation.EvaluationCaseRunDetails {
	clone := *source
	clone.RunID = runID
	inference := *source.Inference
	clone.Inference = &inference
	invocation := *source.Inference.Inferences[0]
	tool := *invocation.Tools[0]
	invocation.Tools = []*evalset.Tool{&tool}
	inference.Inferences = []*evalset.Invocation{&invocation}
	trace := *source.Inference.ExecutionTraces[0]
	trace.Steps = append([]atrace.Step(nil), trace.Steps...)
	inference.ExecutionTraces = []*atrace.Trace{&trace}
	return &clone
}

func cloneEvidenceRunResult(source *evalresult.EvalCaseResult, runID int, score float64) *evalresult.EvalCaseResult {
	clone := *source
	clone.RunID = runID
	metric := *source.OverallEvalMetricResults[0]
	metric.Score = score
	clone.OverallEvalMetricResults = []*evalresult.EvalMetricResult{&metric}
	return &clone
}
