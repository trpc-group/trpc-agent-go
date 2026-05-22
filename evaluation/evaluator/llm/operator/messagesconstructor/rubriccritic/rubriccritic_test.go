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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
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

func TestConstructMessagesBuildsStructuredPrompt(t *testing.T) {
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
	assert.Contains(t, messages[0].Content, "Produce exactly one rubricScores item")
	assert.Contains(t, messages[0].Content, "score 1")
	assert.Contains(t, messages[0].Content, "Semantic equivalence")
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
	output, err := constructor.StructuredOutput(context.Background(), nil, nil, testRubricCriticEvalMetric())
	require.NoError(t, err)
	require.NotNil(t, output)
	require.NotNil(t, output.JSONSchema)
	assert.Equal(t, "rubric_critic_scores", output.JSONSchema.Name)
	schema := output.JSONSchema.Schema
	properties := schema["properties"].(map[string]any)
	rubricScores := properties["rubricScores"].(map[string]any)
	assert.Equal(t, 1, rubricScores["minItems"])
	assert.Equal(t, 1, rubricScores["maxItems"])
}

func TestConstructMessagesAllowsRubricIDsForStructuredOutputValidation(t *testing.T) {
	constructor := New()
	evalMetric := testRubricCriticEvalMetric()
	evalMetric.Criterion.LLMJudge.Rubrics[0].ID = ""
	messages, err := constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{{FinalResponse: &model.Message{Content: "answer"}}},
		[]*evalset.Invocation{{FinalResponse: &model.Message{Content: "reference"}}},
		evalMetric,
	)
	require.NoError(t, err)
	require.Len(t, messages, 1)
}

func TestStructuredOutputRequiresUsableRubricIDs(t *testing.T) {
	constructor := New()
	evalMetric := testRubricCriticEvalMetric()
	evalMetric.Criterion.LLMJudge.Rubrics[0].ID = ""
	structured, ok := constructor.(messagesconstructor.StructuredOutputMessagesConstructor)
	require.True(t, ok)
	_, err := structured.StructuredOutput(context.Background(), nil, nil, evalMetric)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rubric id is required")
	evalMetric = testRubricCriticEvalMetric()
	evalMetric.Criterion.LLMJudge.Rubrics = append(evalMetric.Criterion.LLMJudge.Rubrics, &llm.Rubric{
		ID:      "1",
		Content: &llm.RubricContent{Text: "The answer is concise."},
	})
	_, err = structured.StructuredOutput(context.Background(), nil, nil, evalMetric)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate llm judge rubric id "1"`)
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
