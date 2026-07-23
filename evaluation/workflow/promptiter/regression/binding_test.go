//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalEvaluatorBindsExplicitCandidateOutputToSemanticPrompt(t *testing.T) {
	evaluator, err := NewLocalEvaluator([]MetricConfig{{
		MetricName: metricFinalResponse,
		Threshold:  1,
		Weight:     1,
	}}, "baseline")
	require.NoError(t, err)
	set := &EvalSet{
		EvalSetID:     "binding",
		PassThreshold: testScore(1),
		EvalCases: []EvalCase{newFailureCase(
			"case", "answer", nil, Expectations{},
			FakeOutput{Response: "answer", Usage: Usage{ModelCalls: 1}},
		)},
	}
	prompt := testSemanticPrompt + "\n\n[[trpc-promptiter-candidate:candidate;seed:1]]"
	summary, err := evaluator.Evaluate(context.Background(), set, "candidate", prompt)
	require.NoError(t, err)
	assert.False(t, summary.Cases[0].UsedFallback)
	assert.Equal(t, "candidate", summary.Cases[0].ResponseVariantID)
	assert.Equal(t, HashText(testSemanticPrompt), summary.Cases[0].ResponsePromptSHA256)

	changedPrompt := testSemanticPrompt + " changed\n\n[[trpc-promptiter-candidate:candidate;seed:1]]"
	_, err = evaluator.Evaluate(context.Background(), set, "candidate", changedPrompt)
	require.ErrorContains(t, err, "does not match evaluated prompt")

	output := set.EvalCases[0].FakeResponses["candidate"]
	output.PromptSemanticSHA256 = ""
	set.EvalCases[0].FakeResponses["candidate"] = output
	_, err = evaluator.Evaluate(context.Background(), set, "candidate", prompt)
	require.ErrorContains(t, err, "has no prompt semantic hash binding")
}

func TestLocalEvaluatorAuditsBaselineFallback(t *testing.T) {
	evaluator, err := NewLocalEvaluator([]MetricConfig{{
		MetricName: metricFinalResponse,
		Threshold:  1,
		Weight:     1,
	}}, "baseline")
	require.NoError(t, err)
	evalCase := newFailureCase(
		"case", "answer", nil, Expectations{},
		FakeOutput{Response: "answer", Usage: Usage{ModelCalls: 1}},
	)
	baselineOutput := evalCase.FakeResponses["candidate"]
	baselineOutput.PromptSemanticSHA256 = HashText("baseline prompt")
	delete(evalCase.FakeResponses, "candidate")
	evalCase.FakeResponses["baseline"] = baselineOutput
	set := &EvalSet{EvalSetID: "fallback", PassThreshold: testScore(1), EvalCases: []EvalCase{evalCase}}
	prompt := testSemanticPrompt + "\n\n[[trpc-promptiter-candidate:candidate;seed:1]]"
	summary, err := evaluator.Evaluate(context.Background(), set, "candidate", prompt)
	require.NoError(t, err)
	result := summary.Cases[0]
	assert.True(t, result.UsedFallback)
	assert.Equal(t, "baseline", result.ResponseVariantID)
	assert.Equal(t, HashText("baseline prompt"), result.ResponsePromptSHA256)
}

func TestValidateEvaluationSummaryRejectsInvalidResponseProvenance(t *testing.T) {
	evaluator, err := NewLocalEvaluator([]MetricConfig{{
		MetricName: metricFinalResponse,
		Threshold:  1,
		Weight:     1,
	}}, "baseline")
	require.NoError(t, err)
	set := &EvalSet{
		EvalSetID:     "provenance",
		PassThreshold: testScore(1),
		EvalCases: []EvalCase{newFailureCase(
			"case", "answer", nil, Expectations{},
			FakeOutput{Response: "answer", Usage: Usage{ModelCalls: 1}},
		)},
	}
	prompt := testSemanticPrompt + "\n\n[[trpc-promptiter-candidate:candidate;seed:1]]"
	summary, err := evaluator.Evaluate(context.Background(), set, "candidate", prompt)
	require.NoError(t, err)
	require.NoError(t, validateEvaluationSummary(summary))

	tests := []struct {
		name      string
		mutate    func(*CaseResult)
		wantError string
	}{
		{
			name: "empty response variant",
			mutate: func(result *CaseResult) {
				result.ResponseVariantID = ""
			},
			wantError: "invalid response variant id",
		},
		{
			name: "unreported fallback",
			mutate: func(result *CaseResult) {
				result.ResponseVariantID = "baseline"
			},
			wantError: "does not match summary variant",
		},
		{
			name: "fallback points at requested variant",
			mutate: func(result *CaseResult) {
				result.UsedFallback = true
			},
			wantError: "as fallback output",
		},
		{
			name: "invalid prompt hash",
			mutate: func(result *CaseResult) {
				result.ResponsePromptSHA256 = "bad"
			},
			wantError: "invalid response prompt semantic hash",
		},
		{
			name: "missing candidate binding",
			mutate: func(result *CaseResult) {
				result.ResponsePromptSHA256 = ""
			},
			wantError: "has no prompt semantic hash",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cloned, cloneErr := cloneEvaluationSummary(summary)
			require.NoError(t, cloneErr)
			test.mutate(&cloned.Cases[0])
			require.ErrorContains(t, validateEvaluationSummary(cloned), test.wantError)
		})
	}
}

func TestValidateFallbackBindingsRejectsDifferentBaselineEvidence(t *testing.T) {
	baselineCase := CaseResult{
		CaseID:               "case",
		Score:                0.4,
		FinalResponse:        "baseline answer",
		ResponseVariantID:    "baseline",
		ResponsePromptSHA256: HashText("baseline prompt"),
		MetricResults:        []MetricResult{{MetricName: metricFinalResponse, Score: 0.4}},
		Trace:                []TraceStep{{StepID: "final", Kind: "final_response", Message: "baseline answer"}},
		Usage:                Usage{ModelCalls: 1, InputTokens: 5},
	}
	candidateCase := baselineCase
	candidateCase.UsedFallback = true
	baseline := &EvaluationSummary{Cases: []CaseResult{baselineCase}}
	candidate := &EvaluationSummary{Cases: []CaseResult{candidateCase}}
	require.NoError(t, validateFallbackBindings(baseline, candidate, "baseline"))

	tests := []struct {
		name      string
		mutate    func(*CaseResult)
		wantError string
	}{
		{
			name: "source hash",
			mutate: func(result *CaseResult) {
				result.ResponsePromptSHA256 = HashText("unverified prompt")
			},
			wantError: "does not match its verified baseline source",
		},
		{
			name: "final response",
			mutate: func(result *CaseResult) {
				result.FinalResponse = "fabricated fallback answer"
			},
			wantError: "does not reproduce its verified baseline result",
		},
		{
			name: "score",
			mutate: func(result *CaseResult) {
				result.Score = 1
			},
			wantError: "does not reproduce its verified baseline result",
		},
		{
			name: "metric",
			mutate: func(result *CaseResult) {
				result.MetricResults[0].Score = 1
			},
			wantError: "does not reproduce its verified baseline result",
		},
		{
			name: "trace",
			mutate: func(result *CaseResult) {
				result.Trace[0].Message = "fabricated trace"
			},
			wantError: "does not reproduce its verified baseline result",
		},
		{
			name: "usage",
			mutate: func(result *CaseResult) {
				result.Usage.InputTokens++
			},
			wantError: "does not reproduce its verified baseline result",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cloned, err := cloneEvaluationSummary(candidate)
			require.NoError(t, err)
			test.mutate(&cloned.Cases[0])
			require.ErrorContains(t, validateFallbackBindings(baseline, cloned, "baseline"), test.wantError)
		})
	}
}
