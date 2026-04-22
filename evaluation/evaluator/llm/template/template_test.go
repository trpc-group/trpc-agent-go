//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package template

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/internal/templateresolver"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewReturnsTemplateEvaluatorMetadata(t *testing.T) {
	e := New()
	assert.Equal(t, EvaluatorName, e.Name())
	assert.Equal(t, "LLM template judge evaluator", e.Description())
}

func TestEvaluateDelegatesToBaseEvaluator(t *testing.T) {
	e := New()
	_, err := e.Evaluate(context.Background(), nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required fields in eval metric")
}

func TestConstructMessagesDelegatesToTemplateConstructor(t *testing.T) {
	e := New().(*templateEvaluator)
	messages, err := e.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{{
			UserContent:   &model.Message{Content: "What is the capital of France?"},
			FinalResponse: &model.Message{Content: "Paris"},
		}},
		[]*evalset.Invocation{{
			FinalResponse: &model.Message{Content: "Paris"},
		}},
		buildTemplateMetric(
			"Question: {{question}}\nAnswer: {{answer}}\nReference: {{reference}}",
			templateresolver.ResponseScorerSingleScoreName,
			"",
			"",
			&criterionllm.TemplateVariableBinding{
				TemplateVariable: "question",
				Source: &criterionllm.TemplateVariableSource{
					Scope: criterionllm.TemplateVariableScopeActual,
					Field: criterionllm.TemplateVariableFieldUserContent,
				},
			},
			&criterionllm.TemplateVariableBinding{
				TemplateVariable: "answer",
				Source: &criterionllm.TemplateVariableSource{
					Scope: criterionllm.TemplateVariableScopeActual,
					Field: criterionllm.TemplateVariableFieldFinalResponse,
				},
			},
			&criterionllm.TemplateVariableBinding{
				TemplateVariable: "reference",
				Source: &criterionllm.TemplateVariableSource{
					Scope: criterionllm.TemplateVariableScopeExpected,
					Field: criterionllm.TemplateVariableFieldFinalResponse,
				},
			},
		),
	)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Content, "Question: What is the capital of France?")
}

func TestScoreBasedOnResponseSupportsSingleScore(t *testing.T) {
	e := New().(*templateEvaluator)
	result, err := e.ScoreBasedOnResponse(context.Background(), &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{Content: `{"score":1,"reason":"matched"}`},
		}},
	}, buildTemplateMetric(
		"Answer: {{answer}}",
		templateresolver.ResponseScorerSingleScoreName,
		"",
		"",
	))
	require.NoError(t, err)
	assert.Equal(t, 1.0, result.Score)
	assert.Equal(t, "matched", result.Reason)
}

func TestScoreBasedOnResponseSupportsRubricScores(t *testing.T) {
	e := New().(*templateEvaluator)
	result, err := e.ScoreBasedOnResponse(context.Background(), &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{Content: `{"rubricScores":[{"id":"1","score":1,"reason":"ok"},{"id":"2","score":0,"reason":"missing"}]}`},
		}},
	}, buildTemplateMetric(
		"Answer: {{answer}}",
		templateresolver.ResponseScorerRubricScoresName,
		"",
		"",
	))
	require.NoError(t, err)
	assert.InDelta(t, 0.5, result.Score, 1e-9)
	require.Len(t, result.RubricScores, 2)
	assert.Equal(t, "1", result.RubricScores[0].ID)
	assert.Contains(t, result.Reason, "ok")
}

func TestAggregateSamplesUsesDefaultAggregator(t *testing.T) {
	e := New().(*templateEvaluator)
	result, err := e.AggregateSamples(context.Background(), []*evaluator.PerInvocationResult{
		{Score: 1, Status: status.EvalStatusPassed},
		{Score: 0, Status: status.EvalStatusFailed},
		{Score: 1, Status: status.EvalStatusPassed},
	}, buildTemplateMetric(
		"Answer: {{answer}}",
		templateresolver.ResponseScorerSingleScoreName,
		"",
		"",
	))
	require.NoError(t, err)
	assert.Equal(t, status.EvalStatusPassed, result.Status)
	assert.Equal(t, 1.0, result.Score)
}

func TestAggregateInvocationsUsesDefaultAggregator(t *testing.T) {
	e := New().(*templateEvaluator)
	result, err := e.AggregateInvocations(context.Background(), []*evaluator.PerInvocationResult{
		{Score: 1, Status: status.EvalStatusPassed},
		{Score: 0, Status: status.EvalStatusFailed},
	}, buildTemplateMetric(
		"Answer: {{answer}}",
		templateresolver.ResponseScorerSingleScoreName,
		"",
		"",
	))
	require.NoError(t, err)
	assert.Equal(t, status.EvalStatusPassed, result.OverallStatus)
	assert.InDelta(t, 0.5, result.OverallScore, 1e-9)
}

func TestResponseScorerNameRequiresConfiguredScorer(t *testing.T) {
	_, err := responseScorerName(buildTemplateMetric("Answer: {{answer}}", "", "", ""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template responseScorerName is empty")
}

func TestJudgeTemplateOptionsRequiresTemplate(t *testing.T) {
	_, err := judgeTemplateOptions(&metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template is nil")
}

func TestScoreBasedOnResponseRejectsUnknownScorer(t *testing.T) {
	e := New().(*templateEvaluator)
	_, err := e.ScoreBasedOnResponse(context.Background(), &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{Content: `{"score":1,"reason":"matched"}`},
		}},
	}, buildTemplateMetric("Answer: {{answer}}", "missing", "", ""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported response scorer "missing"`)
}

func TestAggregateSamplesRejectsUnknownAggregator(t *testing.T) {
	e := New().(*templateEvaluator)
	_, err := e.AggregateSamples(context.Background(), []*evaluator.PerInvocationResult{
		{Score: 1, Status: status.EvalStatusPassed},
	}, buildTemplateMetric("Answer: {{answer}}", templateresolver.ResponseScorerSingleScoreName, "missing", ""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported samples aggregator "missing"`)
}

func TestAggregateInvocationsRejectsUnknownAggregator(t *testing.T) {
	e := New().(*templateEvaluator)
	_, err := e.AggregateInvocations(context.Background(), []*evaluator.PerInvocationResult{
		{Score: 1, Status: status.EvalStatusPassed},
	}, buildTemplateMetric("Answer: {{answer}}", templateresolver.ResponseScorerSingleScoreName, "", "missing"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported invocations aggregator "missing"`)
}

func TestAggregateHelpersPreserveTemplateConfigErrors(t *testing.T) {
	e := New().(*templateEvaluator)
	_, err := e.AggregateSamples(context.Background(), nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing llm judge criterion")
	_, err = e.AggregateInvocations(context.Background(), nil, &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template is nil")
}

func TestJudgeTemplateOptionsRejectsMissingLLMJudgeCriterion(t *testing.T) {
	_, err := judgeTemplateOptions(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing llm judge criterion")
}

func TestTemplateEvaluatorNameHelpers(t *testing.T) {
	assert.Equal(t, templateresolver.SampleAggregatorMajorityVoteName, sampleAggregatorName(nil))
	assert.Equal(t, templateresolver.InvocationAggregatorAverageName, invocationAggregatorName(nil))
	templateOptions, err := judgeTemplateOptions(buildTemplateMetric(
		"Answer: {{answer}}",
		templateresolver.ResponseScorerSingleScoreName,
		"custom_sample",
		"custom_invocation",
	))
	require.NoError(t, err)
	assert.Equal(t, "custom_sample", sampleAggregatorName(templateOptions))
	assert.Equal(t, "custom_invocation", invocationAggregatorName(templateOptions))
}

func buildTemplateMetric(promptText string, responseScorerName string,
	sampleAggregatorName string, invocationAggregatorName string,
	bindings ...*criterionllm.TemplateVariableBinding) *metric.EvalMetric {
	return &metric.EvalMetric{
		Threshold: 0.5,
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				Template: &criterionllm.JudgeTemplateOptions{
					Prompt:                   promptText,
					ResponseScorerName:       responseScorerName,
					SampleAggregatorName:     sampleAggregatorName,
					InvocationAggregatorName: invocationAggregatorName,
					VariableBindings:         bindings,
				},
			},
		},
	}
}

func TestAggregateInvocationsUsesConfiguredAggregatorName(t *testing.T) {
	e := New().(*templateEvaluator)
	result, err := e.AggregateInvocations(context.Background(), []*evaluator.PerInvocationResult{
		{
			Score:  1,
			Status: status.EvalStatusPassed,
			Details: &evaluator.PerInvocationDetails{
				RubricScores: []*evalresult.RubricScore{
					{ID: "1", Score: 1},
				},
			},
		},
	}, buildTemplateMetric(
		"Answer: {{answer}}",
		templateresolver.ResponseScorerSingleScoreName,
		"",
		templateresolver.InvocationAggregatorAverageName,
	))
	require.NoError(t, err)
	assert.Equal(t, status.EvalStatusPassed, result.OverallStatus)
}
