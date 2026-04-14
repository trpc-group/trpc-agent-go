//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rubriccritic

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func testRubricCriticEvalMetric() *metric.EvalMetric {
	return &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &llm.LLMCriterion{
				Rubrics: []*llm.Rubric{
					{
						ID:      "1",
						Content: &llm.RubricContent{Text: "The final answer states the correct city."},
					},
				},
			},
		},
	}
}

func TestConstructMessagesBuildsCriticPrompt(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "What is the capital of France?"},
		FinalResponse: &model.Message{Content: "Paris is the capital."},
	}
	expected := &evalset.Invocation{
		FinalResponse: &model.Message{Content: "The capital of France is Paris."},
	}
	messages, err := constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{actual},
		[]*evalset.Invocation{expected},
		testRubricCriticEvalMetric(),
	)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, model.RoleUser, messages[0].Role)
	assert.Contains(t, messages[0].Content, "capital")
	assert.Contains(t, messages[0].Content, "France")
	assert.Contains(t, messages[0].Content, "Paris")
	assert.Contains(t, messages[0].Content, "llm_rubric_critic")
	assert.Contains(t, messages[0].Content, "<reference_answer>")
	assert.Contains(t, messages[0].Content, "The final answer states the correct city.")
	assert.Contains(t, messages[0].Content, "Verdict:")
	assert.Contains(t, messages[0].Content, "Reason:")
	assert.Contains(t, messages[0].Content, "Semantic equivalence")
}

func TestConstructMessagesRequiresExpecteds(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "prompt"},
		FinalResponse: &model.Message{Content: "answer"},
	}
	_, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, nil, &metric.EvalMetric{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expecteds is empty")
}

func TestConstructMessagesRequiresJudgeCriterion(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "prompt"},
		FinalResponse: &model.Message{Content: "answer"},
	}
	expected := &evalset.Invocation{
		FinalResponse: &model.Message{Content: "reference"},
	}
	_, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, []*evalset.Invocation{expected}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eval metric is nil")
	_, err = constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, []*evalset.Invocation{expected}, &metric.EvalMetric{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm judge criterion is required")
}

func TestConstructMessagesRequiresExpectedFinalResponse(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "prompt"},
		FinalResponse: &model.Message{Content: "answer"},
	}
	expected := &evalset.Invocation{}
	_, err := constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{actual},
		[]*evalset.Invocation{expected},
		testRubricCriticEvalMetric(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected final response is required")
}

func TestConstructMessagesRequiresNonNilInvocationsAndRubrics(t *testing.T) {
	constructor := New()
	expected := &evalset.Invocation{
		FinalResponse: &model.Message{Content: "reference"},
	}
	_, err := constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{nil},
		[]*evalset.Invocation{expected},
		testRubricCriticEvalMetric(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "actual invocation is nil")
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "prompt"},
		FinalResponse: &model.Message{Content: "answer"},
	}
	_, err = constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{actual},
		[]*evalset.Invocation{nil},
		testRubricCriticEvalMetric(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected invocation is nil")
	_, err = constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{actual},
		[]*evalset.Invocation{expected},
		&metric.EvalMetric{
			Criterion: &criterion.Criterion{
				LLMJudge: &llm.LLMCriterion{},
			},
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm judge rubrics are required")
}
