//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package categorical

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/score"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestScoreBasedOnResponseMapsConfiguredCategory(t *testing.T) {
	scorer := New()
	result, err := scorer.ScoreBasedOnResponse(context.Background(),
		makeResponse(`{"category":"partially_correct","reason":"close"}`), testEvalMetric())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0.5, result.Score)
	assert.Equal(t, "close", result.Reason)
	require.NotNil(t, result.Value)
	assert.Equal(t, score.KindCategorical, result.Value.Kind)
	assert.Equal(t, "partially_correct", result.Value.Categorical)
}

func TestScoreBasedOnResponseTreatsUnknownCategoryAsFailedResult(t *testing.T) {
	scorer := New()
	result, err := scorer.ScoreBasedOnResponse(context.Background(),
		makeResponse(`{"category":"unknown","reason":"model chose another label"}`), testEvalMetric())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0.0, result.Score)
	require.NotNil(t, result.Status)
	assert.Equal(t, status.EvalStatusFailed, *result.Status)
	require.NotNil(t, result.Value)
	assert.Equal(t, "unknown", result.Value.Categorical)
	assert.Contains(t, result.Reason, `unknown categorical label "unknown"`)
}

func TestScoreBasedOnResponseRejectsMissingCategoricalFields(t *testing.T) {
	scorer := New()
	_, err := scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{"reason":"missing category"}`), testEvalMetric())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "category is required")
	_, err = scorer.ScoreBasedOnResponse(context.Background(), makeResponse(`{"category":"correct"}`), testEvalMetric())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reason is required")
}

func testEvalMetric() *metric.EvalMetric {
	return testEvalMetricWithCategories(
		&criterionllm.CategoryScore{Label: "correct", Score: 1},
		&criterionllm.CategoryScore{Label: "partially_correct", Score: 0.5},
		&criterionllm.CategoryScore{Label: "incorrect", Score: 0},
	)
}

func testEvalMetricWithCategories(categories ...*criterionllm.CategoryScore) *metric.EvalMetric {
	return &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				Template: &criterionllm.JudgeTemplateOptions{
					ResponseScorerOptions: &criterionllm.ResponseScorerOptions{
						Categories: categories,
					},
				},
			},
		},
	}
}

func makeResponse(content string) *model.Response {
	return &model.Response{
		Choices: []model.Choice{{Message: model.Message{Content: content}}},
	}
}
