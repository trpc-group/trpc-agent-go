//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rubricresponse

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestConstructMessagesIncludesAllFields(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "test_user_content"},
		FinalResponse: &model.Message{Content: "test_final_response"},
	}
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &llm.LLMCriterion{
				Rubrics: []*llm.Rubric{
					{
						ID: "1",
						Content: &llm.RubricContent{
							Text: "test_rubric_text",
						},
					},
				},
			},
		},
	}

	messages, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, nil, evalMetric)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, model.RoleUser, messages[0].Role)
	assert.Contains(t, messages[0].Content, "test_user_content")
	assert.Contains(t, messages[0].Content, "test_final_response")
	assert.Contains(t, messages[0].Content, "test_rubric_text")
	assert.Contains(t, messages[0].Content, "Produce exactly one rubricScores item")
	assert.Contains(t, messages[0].Content, "Return a single valid JSON object")
	assert.Contains(t, messages[0].Content, "rubricScores")
	assert.NotContains(t, messages[0].Content, "Do not output JSON")
	assert.Contains(t, messages[0].Content, "Output Format")
	assert.Contains(t, messages[0].Content, "Output Rules")
	assert.NotContains(t, messages[0].Content, "Verdict:")
}

func TestStructuredOutputReturnsRubricSchema(t *testing.T) {
	constructor, ok := New().(messagesconstructor.StructuredOutputMessagesConstructor)
	require.True(t, ok)
	output, err := constructor.StructuredOutput(context.Background(), nil, nil, &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &llm.LLMCriterion{
				Rubrics: []*llm.Rubric{{ID: "r1", Content: &llm.RubricContent{Text: "rubric"}}},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, output)
	require.NotNil(t, output.JSONSchema)
	assert.Equal(t, "rubric_response_scores", output.JSONSchema.Name)
	schema := output.JSONSchema.Schema
	properties := schema["properties"].(map[string]any)
	rubricScores := properties["rubricScores"].(map[string]any)
	assert.Equal(t, 1, rubricScores["minItems"])
	assert.Equal(t, 1, rubricScores["maxItems"])
}

func TestConstructMessagesRequiresLLMJudgeRubrics(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "test_user_content"},
		FinalResponse: &model.Message{Content: "test_final_response"},
	}
	_, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eval metric is nil")
	_, err = constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, nil, &metric.EvalMetric{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm judge criterion is required")
	_, err = constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, nil, &metric.EvalMetric{
		Criterion: &criterion.Criterion{LLMJudge: &llm.LLMCriterion{}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm judge rubrics are required")
}
