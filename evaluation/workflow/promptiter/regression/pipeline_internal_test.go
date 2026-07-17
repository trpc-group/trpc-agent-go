//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type edgeAttributor func(context.Context, *CaseResult) (*AttributionResult, error)

func (f edgeAttributor) Attribute(ctx context.Context, result *CaseResult) (*AttributionResult, error) {
	return f(ctx, result)
}

func TestValidateGateDecisionRejectsContradictoryEvidence(t *testing.T) {
	tests := []struct {
		name     string
		decision *GateDecision
		error    string
	}{
		{name: "nil", error: "decision is nil"},
		{name: "unknown decision", decision: &GateDecision{Decision: Decision("unknown")}, error: "unknown decision"},
		{name: "missing rules", decision: &GateDecision{Decision: DecisionAccepted}, error: "no rule evidence"},
		{name: "unnamed rule", decision: &GateDecision{Decision: DecisionAccepted, Rules: []GateRuleResult{{Passed: true}}}, error: "unnamed rule"},
		{name: "failed rule without reason", decision: &GateDecision{Decision: DecisionRejected, Rules: []GateRuleResult{{Rule: "gain"}}}, error: "has no reason"},
		{name: "accepted with failed rule", decision: &GateDecision{Decision: DecisionAccepted, Rules: []GateRuleResult{{Rule: "gain", Reason: "too low"}}}, error: "accepted decision contains failed rules"},
		{name: "rejected without failed rule", decision: &GateDecision{Decision: DecisionRejected, Rules: []GateRuleResult{{Rule: "gain", Passed: true}}}, error: "no failed rule"},
		{name: "rejected without reasons", decision: &GateDecision{Decision: DecisionRejected, Rules: []GateRuleResult{{Rule: "gain", Reason: "too low"}}}, error: "no reasons"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorContains(t, validateGateDecision(test.decision), test.error)
		})
	}
	require.NoError(t, validateGateDecision(&GateDecision{
		Decision: DecisionInconclusive,
		Rules:    []GateRuleResult{{Rule: "usage", Reason: "unknown"}},
		Reasons:  []string{"usage is incomplete"},
	}))
}

func TestAttributeSnapshotRejectsInvalidAttributorResults(t *testing.T) {
	base := &CaseResult{EvalSetID: "validation", CaseID: "case", Passed: false}
	tests := []struct {
		name   string
		result *AttributionResult
		error  error
		want   string
	}{
		{name: "dependency error", error: errors.New("classifier failed"), want: "classifier failed"},
		{name: "nil result", want: "returned nil result"},
		{name: "wrong case", result: validEdgeAttribution("other"), want: "returned result for case"},
		{name: "wrong eval set", result: func() *AttributionResult {
			value := validEdgeAttribution("case")
			value.EvalSetID = "train"
			return value
		}(), want: "returned eval set"},
		{name: "missing category", result: func() *AttributionResult { value := validEdgeAttribution("case"); value.Category = ""; return value }(), want: "incomplete evidence"},
		{name: "missing reason", result: func() *AttributionResult { value := validEdgeAttribution("case"); value.Reason = " "; return value }(), want: "incomplete evidence"},
		{name: "missing evidence", result: func() *AttributionResult { value := validEdgeAttribution("case"); value.Evidence = nil; return value }(), want: "incomplete evidence"},
		{name: "empty evidence reason", result: func() *AttributionResult {
			value := validEdgeAttribution("case")
			value.Evidence[0].Reason = " "
			return value
		}(), want: "empty evidence reason"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			analyzer := &analyzer{deps: Dependencies{Attributor: edgeAttributor(func(context.Context, *CaseResult) (*AttributionResult, error) {
				return test.result, test.error
			})}}
			run := &RunResult{}
			err := analyzer.attributeSnapshot(context.Background(), run, &EvaluationSnapshot{Cases: []CaseResult{*base}}, AttributionBaselineValidation, "")
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestAttributeSnapshotFillsEvalSetAndSkipsHealthyCases(t *testing.T) {
	calls := 0
	analyzer := &analyzer{deps: Dependencies{Attributor: edgeAttributor(func(_ context.Context, result *CaseResult) (*AttributionResult, error) {
		calls++
		return validEdgeAttribution(result.CaseID), nil
	})}}
	run := &RunResult{}
	snapshot := &EvaluationSnapshot{Cases: []CaseResult{
		{EvalSetID: "validation", CaseID: "healthy", Passed: true},
		{EvalSetID: "validation", CaseID: "failed", Passed: false},
	}}
	require.NoError(t, analyzer.attributeSnapshot(context.Background(), run, snapshot, AttributionCandidateValidation, "round-1"))
	require.Len(t, run.Attributions, 1)
	assert.Equal(t, 1, calls)
	assert.Equal(t, "validation", run.Attributions[0].EvalSetID)
	assert.Equal(t, AttributionCandidateValidation, run.Attributions[0].Phase)
	assert.Equal(t, "round-1", run.Attributions[0].CandidateID)

	require.NoError(t, analyzer.attributeSnapshot(context.Background(), run, nil, AttributionBaselineTrain, ""))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, analyzer.attributeSnapshot(canceled, &RunResult{}, snapshot, AttributionBaselineTrain, ""), context.Canceled)
}

func TestCriticalCaseValidationAndAttributionSummary(t *testing.T) {
	snapshot := &EvaluationSnapshot{Cases: []CaseResult{
		{EvalSetID: "a", CaseID: "critical"},
		{EvalSetID: "b", CaseID: "critical"},
	}}
	require.ErrorContains(t, validateCriticalCases(snapshot, []string{"missing"}), "absent")
	require.ErrorContains(t, validateCriticalCases(snapshot, []string{"critical"}), "ambiguous")
	require.NoError(t, validateCriticalCases(snapshot, nil))

	result := &RunResult{Attributions: []AttributionResult{
		{Phase: AttributionCandidateValidation, CandidateID: "b", EvalSetID: "z", CaseID: "2", Category: FailureFormat},
		{Phase: AttributionBaselineTrain, EvalSetID: "a", CaseID: "1", Category: FailureFormat},
		{Phase: AttributionCandidateValidation, CandidateID: "a", EvalSetID: "z", CaseID: "1", Category: FailureUnknown},
	}}
	finalizeAttributions(result)
	assert.Equal(t, AttributionBaselineTrain, result.Attributions[0].Phase)
	assert.Equal(t, "a", result.Attributions[1].CandidateID)
	assert.Equal(t, 2, result.AttributionCounts[FailureFormat])
	assert.Equal(t, 1, result.AttributionCounts[FailureUnknown])
}

func validEdgeAttribution(caseID string) *AttributionResult {
	return &AttributionResult{
		CaseID: caseID, Category: FailureUnknown, Reason: "insufficient evidence",
		Evidence: []Evidence{{Reason: "opaque evaluator failure"}},
	}
}
