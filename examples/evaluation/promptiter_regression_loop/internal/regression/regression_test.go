//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestCompareEvaluationsClassifiesCaseChanges(t *testing.T) {
	baseline := evaluation(0.5, evalCase("a", 0, status.EvalStatusFailed, "answer mismatch"), evalCase("b", 1, status.EvalStatusPassed, ""))
	candidate := evaluation(0.75, evalCase("a", 1, status.EvalStatusPassed, ""), evalCase("b", 0.5, status.EvalStatusFailed, "format invalid"))
	delta, err := CompareEvaluations(baseline, candidate)
	require.NoError(t, err)
	assert.Equal(t, 1, delta.NewPasses)
	assert.Equal(t, 1, delta.NewFailures)
	assert.Equal(t, 1, delta.Regressions)
	assert.Equal(t, attributionFormat, delta.Cases[1].Attributions[0])
}

func TestCompareEvaluationsRejectsMisalignedCases(t *testing.T) {
	_, err := CompareEvaluations(evaluation(1, evalCase("a", 1, status.EvalStatusPassed, "")), evaluation(1, evalCase("b", 1, status.EvalStatusPassed, "")))
	require.ErrorContains(t, err, "missing case")
}

func TestAttributeFailureUsesTraceEvidence(t *testing.T) {
	input := evalCase("a", 0, status.EvalStatusFailed, "trajectory mismatch")
	input.Trace = &atrace.Trace{Status: atrace.TraceStatusFailed, Steps: []atrace.Step{{NodeType: "tool", Error: "boom"}}}
	assert.ElementsMatch(t, []AttributionCategory{attributionExecution, attributionToolCall}, AttributeFailure(input))
}

func TestDecideGateAcceptsImprovement(t *testing.T) {
	decision := DecideGate(GateConfig{MinScoreGain: 0.1, MaxNewFailures: 0, MaxScoreRegressions: 0}, DeltaSummary{ScoreDelta: 0.2}, UsageSummary{})
	assert.True(t, decision.Accepted)
}

func TestDecideGateRejectsOverfitting(t *testing.T) {
	delta := DeltaSummary{ScoreDelta: 0.2, NewFailures: 1, Regressions: 1, Cases: []CaseDelta{{CaseID: "critical", BaselinePassed: true, CandidatePassed: false}}}
	decision := DecideGate(GateConfig{MinScoreGain: 0.1, CriticalCaseIDs: []string{"critical"}}, delta, UsageSummary{})
	assert.False(t, decision.Accepted)
	assert.Len(t, decision.Reasons, 3)
}

func TestDecideGateRejectsBudgets(t *testing.T) {
	decision := DecideGate(GateConfig{MaxModelCalls: 2, MaxToolCalls: 2, MaxTokens: 10}, DeltaSummary{}, UsageSummary{ModelCalls: 3, ToolCalls: 3, Tokens: 11})
	assert.False(t, decision.Accepted)
	assert.Len(t, decision.Reasons, 3)
}

func TestValidateGateConfig(t *testing.T) {
	require.Error(t, ValidateGateConfig(GateConfig{MinScoreGain: -1}))
	require.Error(t, ValidateGateConfig(GateConfig{CriticalCaseIDs: []string{"critical", "critical"}}))
	require.NoError(t, ValidateGateConfig(GateConfig{CriticalCaseIDs: []string{"critical"}}))
}

func evaluation(score float64, cases ...promptiterengine.CaseResult) *promptiterengine.EvaluationResult {
	return &promptiterengine.EvaluationResult{OverallScore: score, EvalSets: []promptiterengine.EvalSetResult{{EvalSetID: "validation", OverallScore: score, Cases: cases}}}
}

func evalCase(id string, score float64, evalStatus status.EvalStatus, reason string) promptiterengine.CaseResult {
	return promptiterengine.CaseResult{EvalSetID: "validation", EvalCaseID: id, Metrics: []promptiterengine.MetricResult{{MetricName: "quality", Score: score, Status: evalStatus, Reason: reason}}}
}
