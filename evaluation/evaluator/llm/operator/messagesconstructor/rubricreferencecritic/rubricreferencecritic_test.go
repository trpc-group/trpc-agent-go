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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func newValidInvocation() *evalset.Invocation {
	return &evalset.Invocation{
		UserContent:   &model.Message{Content: "test_user_content"},
		FinalResponse: &model.Message{Content: "test_actual_final_response"},
	}
}

func newValidExpectedInvocation() *evalset.Invocation {
	return &evalset.Invocation{
		FinalResponse: &model.Message{Content: "test_expected_final_response"},
	}
}

func newValidEvalMetric() *metric.EvalMetric {
	return &metric.EvalMetric{
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
}

func TestConstructMessagesIncludesReferenceAnswer(t *testing.T) {
	constructor := New()
	actual := newValidInvocation()
	expected := newValidExpectedInvocation()
	evalMetric := newValidEvalMetric()
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
	assert.Contains(t, messages[0].Content, "Produce exactly one rubricScores item")
	assert.Contains(t, messages[0].Content, "Return a single valid JSON object")
	assert.Contains(t, messages[0].Content, "rubricScores")
	assert.NotContains(t, messages[0].Content, "Do not output JSON")
	assert.Contains(t, messages[0].Content, "Output Format")
	assert.Contains(t, messages[0].Content, "Output Rules")
	assert.NotContains(t, messages[0].Content, "Verdict:")
}

func TestConstructMessagesRequiresReferenceAnswer(t *testing.T) {
	constructor := New()
	actual := newValidInvocation()
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
	actual := newValidInvocation()
	expected := newValidExpectedInvocation()
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{},
		},
	}
	_, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, []*evalset.Invocation{expected}, evalMetric)
	require.Error(t, err)
	assert.ErrorContains(t, err, "llm judge rubrics are required")
}

func TestStructuredOutputReturnsRubricSchema(t *testing.T) {
	constructor, ok := New().(messagesconstructor.StructuredOutputMessagesConstructor)
	require.True(t, ok)
	output, err := constructor.StructuredOutput(context.Background(), nil, nil, newValidEvalMetric())
	require.NoError(t, err)
	require.NotNil(t, output)
	require.NotNil(t, output.JSONSchema)
	assert.Equal(t, "rubric_reference_critic_scores", output.JSONSchema.Name)
	schema := output.JSONSchema.Schema
	properties := schema["properties"].(map[string]any)
	rubricScores := properties["rubricScores"].(map[string]any)
	assert.Equal(t, 1, rubricScores["minItems"])
	assert.Equal(t, 1, rubricScores["maxItems"])
}

func TestConstructMessagesRequiresLLMJudgeCriterion(t *testing.T) {
	constructor := New()
	actual := newValidInvocation()
	expected := newValidExpectedInvocation()
	_, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, []*evalset.Invocation{expected}, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "eval metric is nil")
}

func TestConstructMessagesValidationErrors(t *testing.T) {
	constructor := New()
	tests := []struct {
		name       string
		actuals    []*evalset.Invocation
		expecteds  []*evalset.Invocation
		evalMetric *metric.EvalMetric
		wantErr    string
	}{
		{
			name:       "empty actuals",
			actuals:    nil,
			expecteds:  []*evalset.Invocation{newValidExpectedInvocation()},
			evalMetric: newValidEvalMetric(),
			wantErr:    "actuals is empty",
		},
		{
			name:       "nil criterion",
			actuals:    []*evalset.Invocation{newValidInvocation()},
			expecteds:  []*evalset.Invocation{newValidExpectedInvocation()},
			evalMetric: &metric.EvalMetric{},
			wantErr:    "llm judge criterion is required",
		},
		{
			name:       "nil actual invocation",
			actuals:    []*evalset.Invocation{nil},
			expecteds:  []*evalset.Invocation{newValidExpectedInvocation()},
			evalMetric: newValidEvalMetric(),
			wantErr:    "actual invocation is nil",
		},
		{
			name:       "nil expected invocation",
			actuals:    []*evalset.Invocation{newValidInvocation()},
			expecteds:  []*evalset.Invocation{nil},
			evalMetric: newValidEvalMetric(),
			wantErr:    "expected invocation is nil",
		},
		{
			name:    "nil expected final response",
			actuals: []*evalset.Invocation{newValidInvocation()},
			expecteds: []*evalset.Invocation{
				{},
			},
			evalMetric: newValidEvalMetric(),
			wantErr:    "expected final response is required for llm_rubric_reference_critic",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := constructor.ConstructMessages(context.Background(), tt.actuals, tt.expecteds, tt.evalMetric)
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}
