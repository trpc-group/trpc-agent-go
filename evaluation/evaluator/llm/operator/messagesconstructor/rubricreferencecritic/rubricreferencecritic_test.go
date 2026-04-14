//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rubricreferencecritic

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestConstructMessagesIncludesReferenceAnswer(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "test_user_content"},
		FinalResponse: &model.Message{Content: "test_actual_final_response"},
	}
	expected := &evalset.Invocation{
		FinalResponse: &model.Message{Content: "test_expected_final_response"},
	}
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				Rubrics: []*criterionllm.Rubric{
					{
						ID: "1",
						Content: &criterionllm.RubricContent{
							Text: "test_rubric_text",
						},
					},
				},
			},
		},
	}
	messages, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, []*evalset.Invocation{expected}, evalMetric)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, model.RoleUser, messages[0].Role)
	assert.Contains(t, messages[0].Content, "test_user_content")
	assert.Contains(t, messages[0].Content, "test_actual_final_response")
	assert.Contains(t, messages[0].Content, "test_expected_final_response")
	assert.Contains(t, messages[0].Content, "test_rubric_text")
	assert.NotContains(t, messages[0].Content, "guessed basketball context")
	assert.NotContains(t, messages[0].Content, "current play")
}

func TestConstructMessagesRequiresReferenceAnswer(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "test_user_content"},
		FinalResponse: &model.Message{Content: "test_actual_final_response"},
	}
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{},
		},
	}
	_, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, nil, evalMetric)
	require.Error(t, err)
	assert.ErrorContains(t, err, "expecteds is empty")
}

func TestConstructMessagesRequiresLLMJudgeRubrics(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "test_user_content"},
		FinalResponse: &model.Message{Content: "test_actual_final_response"},
	}
	expected := &evalset.Invocation{
		FinalResponse: &model.Message{Content: "test_expected_final_response"},
	}
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{},
		},
	}
	_, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, []*evalset.Invocation{expected}, evalMetric)
	require.Error(t, err)
	assert.ErrorContains(t, err, "llm judge rubrics are required")
}

func TestConstructMessagesRequiresLLMJudgeCriterion(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "test_user_content"},
		FinalResponse: &model.Message{Content: "test_actual_final_response"},
	}
	expected := &evalset.Invocation{
		FinalResponse: &model.Message{Content: "test_expected_final_response"},
	}
	_, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, []*evalset.Invocation{expected}, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "eval metric is nil")
}
