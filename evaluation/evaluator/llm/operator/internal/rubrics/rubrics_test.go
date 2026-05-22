//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rubrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
)

func TestVisibleReturnsJudgeRenderedRubrics(t *testing.T) {
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &llm.LLMCriterion{
				Rubrics: []*llm.Rubric{
					nil,
					{ID: " skipped "},
					{ID: " one ", Content: &llm.RubricContent{Text: "alpha"}},
					{ID: "two", Content: &llm.RubricContent{Text: "beta"}},
				},
			},
		},
	}
	assert.Equal(t, []VisibleRubric{{ID: "one"}, {ID: "two"}}, Visible(evalMetric))
	assert.Equal(t, 2, Count(evalMetric))
}

func TestValidateStructuredRejectsInvalidRubrics(t *testing.T) {
	_, err := ValidateStructured(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eval metric is nil")
	_, err = ValidateStructured(&metric.EvalMetric{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm judge criterion is required")
	_, err = ValidateStructured(&metric.EvalMetric{
		Criterion: &criterion.Criterion{LLMJudge: &llm.LLMCriterion{}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm judge rubrics are required")
	_, err = ValidateStructured(metricWithRubrics(&llm.Rubric{
		ID:      "",
		Content: &llm.RubricContent{Text: "alpha"},
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rubric id is required")
	_, err = ValidateStructured(metricWithRubrics(
		&llm.Rubric{ID: "same", Content: &llm.RubricContent{Text: "alpha"}},
		&llm.Rubric{ID: " same ", Content: &llm.RubricContent{Text: "beta"}},
	))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate llm judge rubric id "same"`)
}

func TestValidateStructuredReturnsVisibleRubrics(t *testing.T) {
	got, err := ValidateStructured(metricWithRubrics(
		&llm.Rubric{ID: " one ", Content: &llm.RubricContent{Text: "alpha"}},
		&llm.Rubric{ID: "two", Content: &llm.RubricContent{Text: "beta"}},
	))
	require.NoError(t, err)
	assert.Equal(t, []VisibleRubric{{ID: "one"}, {ID: "two"}}, got)
}

func TestScoresOutputBuildsRubricScoreSchema(t *testing.T) {
	output := ScoresOutput("test_scores", "test description", []VisibleRubric{{ID: "one"}, {ID: "two"}})
	require.NotNil(t, output)
	require.NotNil(t, output.JSONSchema)
	assert.Equal(t, "test_scores", output.JSONSchema.Name)
	assert.Equal(t, "test description", output.JSONSchema.Description)
	schema := output.JSONSchema.Schema
	properties := schema["properties"].(map[string]any)
	rubricScores := properties["rubricScores"].(map[string]any)
	assert.Equal(t, 2, rubricScores["minItems"])
	assert.Equal(t, 2, rubricScores["maxItems"])
	items := rubricScores["items"].(map[string]any)
	itemProperties := items["properties"].(map[string]any)
	idSchema := itemProperties["id"].(map[string]any)
	assert.Equal(t, []string{"one", "two"}, idSchema["enum"])
}

func metricWithRubrics(rubrics ...*llm.Rubric) *metric.EvalMetric {
	return &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &llm.LLMCriterion{Rubrics: rubrics},
		},
	}
}
