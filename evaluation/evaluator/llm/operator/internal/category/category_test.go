//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package category

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
)

func TestScoresRejectsInvalidOptions(t *testing.T) {
	_, err := Scores(testEvalMetricWithCategories())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "categories are required")
	_, err = Scores(testEvalMetricWithCategories(&criterionllm.CategoryScore{Label: "", Score: 1}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "label is empty")
	_, err = Scores(testEvalMetricWithCategories(&criterionllm.CategoryScore{Label: "bad", Score: 2}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `category "bad" score must be between 0 and 1`)
	_, err = Scores(testEvalMetricWithCategories(
		&criterionllm.CategoryScore{Label: "same", Score: 1},
		&criterionllm.CategoryScore{Label: "same", Score: 0},
	))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate categorical category label "same"`)
}

func TestStructuredOutputUsesConfiguredCategoryLabels(t *testing.T) {
	output, err := StructuredOutput(testEvalMetric())
	require.NoError(t, err)
	require.NotNil(t, output)
	require.NotNil(t, output.JSONSchema)
	assert.Equal(t, "categorical_result", output.JSONSchema.Name)
	schema := output.JSONSchema.Schema
	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	category, ok := properties["category"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []string{"correct", "partially_correct", "incorrect"}, category["enum"])
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
